package remedy_test

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/pblumer/atlas/compiler"
	"github.com/pblumer/atlas/connector/remedy"
	remedymock "github.com/pblumer/atlas/connector/remedy/mock"
	"github.com/pblumer/atlas/engine"
	"github.com/pblumer/atlas/job"
	"github.com/pblumer/atlas/model"
	"github.com/pblumer/atlas/state"
)

// remedyThenWaitProcess: Start → Remedy task (writes ResultVar) → plain
// service task (parks, so the instance stays alive) → End. Parking after the Remedy
// task lets a test read the written entry id before the instance ends.
func remedyThenWaitProcess(t *testing.T, cfg compiler.RemedyConfig) (*compiler.CompiledProcess, int32) {
	t.Helper()
	b := compiler.NewBuilder(remedyDefKey, "ticketing", 1)
	start := b.AddStartEvent()
	call := b.AddRemedyConnectorTask(cfg)
	wait := b.AddServiceTask("wait", 3)
	end := b.AddEndEvent()
	b.Connect(start, call)
	b.Connect(call, wait)
	b.Connect(wait, end)
	cp, err := b.Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	return cp, cp.ConnectorTask(cp.Node(call).Detail).JobType
}

// soleInstanceKey returns the key of the single live process instance.
func soleInstanceKey(t *testing.T, s *state.Store) uint64 {
	t.Helper()
	var keys []uint64
	if err := s.ActiveProcessInstances(func(k uint64, _ *model.ProcessInstanceValue) error {
		keys = append(keys, k)
		return nil
	}); err != nil {
		t.Fatalf("ActiveProcessInstances: %v", err)
	}
	if len(keys) != 1 {
		t.Fatalf("live instances = %d, want exactly 1", len(keys))
	}
	return keys[0]
}

// readVar returns the variable named name in scope, or nil if absent.
func readVar(t *testing.T, s *state.Store, scope uint64, name string) *model.VariableValue {
	t.Helper()
	var found *model.VariableValue
	if err := s.VariablesOfScope(scope, func(v *model.VariableValue) error {
		if v.Name == name {
			cp := *v
			found = &cp
		}
		return nil
	}); err != nil {
		t.Fatalf("VariablesOfScope: %v", err)
	}
	return found
}

// TestAResolvedRemedyJobSurvivesTheWireAndCarriesNoCredential is ADR-0168's split made
// checkable for this kind: the engine resolves the task, the resolved job is
// serialized exactly as a leased job carries it, and a *separate* half — holding the
// only registry with a base URL and a password in it — creates the entry from what
// arrived. Both halves of the promise are asserted: what the worker receives is enough
// to file the ticket, and there is nowhere in it for a credential to ride along.
func TestAResolvedRemedyJobSurvivesTheWireAndCarriesNoCredential(t *testing.T) {
	const password = "hunter2-do-not-travel"
	mock := remedymock.New(remedymock.WithCredentials("svc", password), remedymock.WithIDPrefix("INC"))
	ar := httptest.NewServer(mock.Handler())
	defer ar.Close()

	log, store := openStore(t)
	cp, jobType := remedyThenWaitProcess(t, compiler.RemedyConfig{
		Connector: "helix",
		Form:      compiler.RestExpr{Literal: "HPD:IncidentInterface_Create"},
		Fields: []compiler.RestKV{
			{Name: "Description", Val: compiler.RestExpr{Literal: "Disk full"}},
		},
		ResultVar: "incidentNumber",
		Retries:   3,
	})

	// The worker's half: the registry lives here and nowhere else, built from the AR
	// System URL and the service account this process was configured with.
	reg := remedy.NewRegistry()
	reg.Register("helix", remedy.NewHTTPClient(remedy.Connector{BaseURL: ar.URL, Username: "svc", Password: password}))

	var wire []byte
	p := engine.New(1, log, store, &fixedClock{})
	p.Deploy(cp)
	if err := p.Recover(); err != nil {
		t.Fatalf("Recover: %v", err)
	}
	runner := job.NewRunner(store, p)
	runner.HandleWithOutput(jobType, func(state.Reader) job.OutputHandler {
		return func(j job.Job) ([]model.VariableValue, error) {
			// --- the engine's half: resolve, and hand over plain values ---
			ei, ok, err := store.GetElementInstance(j.ElementInstanceKey)
			if err != nil || !ok {
				t.Fatalf("GetElementInstance: %v (found=%v)", err, ok)
			}
			detail, err := cp.ConnectorTaskOf(ei.ElementId)
			if err != nil {
				t.Fatalf("ConnectorTaskOf: %v", err)
			}
			resolved, err := remedy.Resolve(store, cp, detail, ei, j.ElementInstanceKey, j.Key)
			if err != nil {
				t.Fatalf("Resolve: %v", err)
			}
			if wire, err = json.Marshal(resolved); err != nil {
				t.Fatalf("marshal the resolved job: %v", err)
			}
			// --- the wire ---
			var onTheWorker remedy.Job
			if err := json.Unmarshal(wire, &onTheWorker); err != nil {
				t.Fatalf("unmarshal the resolved job: %v", err)
			}
			// --- the worker's half: only the payload and its own registry ---
			res, err := remedy.Run(context.Background(), onTheWorker, reg)
			if err != nil {
				return nil, err
			}
			return []model.VariableValue{{Name: onTheWorker.ResultVariable, Kind: model.VarString, Text: res.EntryID}}, nil
		}
	})
	p.CreateInstance(cp.Key)
	if err := runner.Drive(); err != nil {
		t.Fatalf("Drive: %v", err)
	}

	// The AR System really was called, through the real client, with what the model
	// authored — and with the job key as X-Request-ID.
	entries := mock.Entries()
	if len(entries) != 1 {
		t.Fatalf("entries created = %d, want 1", len(entries))
	}
	if entries[0].Form != "HPD:IncidentInterface_Create" || entries[0].Values["Description"] != "Disk full" {
		t.Errorf("entry = %+v, want the authored form and field", entries[0])
	}
	if entries[0].RequestID == "" {
		t.Error("the entry carries no X-Request-ID; an at-least-once replay would be unrecognizable")
	}

	// The entry id came back into the instance, through the wire, as the result variable.
	scope := soleInstanceKey(t, store)
	got := readVar(t, store, scope, "incidentNumber")
	if got == nil || got.Text != entries[0].ID {
		t.Fatalf("incidentNumber = %+v, want the created entry id %q", got, entries[0].ID)
	}

	// And the half that mattered: nothing secret was in what travelled.
	for _, secret := range []string{password, "svc", ar.URL} {
		if strings.Contains(string(wire), secret) {
			t.Errorf("the resolved job carries %q: %s", secret, wire)
		}
	}
}

// A job whose task has no detail is refused rather than being sent as an empty entry.
func TestResolveRefusesATaskWithNoDetail(t *testing.T) {
	if _, err := remedy.Resolve(nil, nil, nil, &model.ElementInstanceValue{}, 1, 2); err == nil {
		t.Error("Resolve with no detail succeeded, want an error")
	}
}

// Run reports an unconfigured worker ahead of an unresolved form. Both are real
// failures, but only one of them is the operator's to fix, and a task that has both
// must not send them looking at the model first.
func TestRunReportsAnUnconfiguredConnectorBeforeAnUnresolvedForm(t *testing.T) {
	reg := remedy.NewRegistry()
	_, err := remedy.Run(context.Background(), remedy.Job{Connector: "helix"}, reg)
	if err == nil || !strings.Contains(err.Error(), "helix") {
		t.Fatalf("error = %v, want one naming the unconfigured worker", err)
	}

	rc := &recordingClient{id: "INC1"}
	reg.Register("helix", rc)
	if _, err := remedy.Run(context.Background(), remedy.Job{Connector: "helix"}, reg); err == nil ||
		!strings.Contains(err.Error(), "form") {
		t.Fatalf("error = %v, want one naming the unresolved form", err)
	}
	if len(rc.created) != 0 {
		t.Error("an entry was created for a task with no form")
	}
}

// The values a resolved job carries reach the AR System as the entry's field values,
// keyed as the model authored them.
func TestRunSendsTheResolvedValues(t *testing.T) {
	rc := &recordingClient{id: "INC000000000007"}
	reg := remedy.NewRegistry()
	reg.Register("helix", rc)

	res, err := remedy.Run(context.Background(), remedy.Job{
		Connector: "helix",
		Form:      "HPD:Help Desk",
		Values:    map[string]string{"Summary": "Printer on fire", "Impact": "1-Extensive"},
		RequestID: "4711",
	}, reg)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.EntryID != "INC000000000007" {
		t.Errorf("EntryID = %q, want the created entry's id", res.EntryID)
	}
	if len(rc.created) != 1 {
		t.Fatalf("entries created = %d, want 1", len(rc.created))
	}
	e := rc.created[0]
	if e.Form != "HPD:Help Desk" || e.Values["Summary"] != "Printer on fire" || e.Values["Impact"] != "1-Extensive" {
		t.Errorf("entry = %+v, want the resolved form and values", e)
	}
	if e.RequestID != "4711" {
		t.Errorf("RequestID = %q, want the job key carried through", e.RequestID)
	}
}
