package worker_test

import (
	"context"
	"errors"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/pblumer/atlas/api"
	"github.com/pblumer/atlas/engine"
	"github.com/pblumer/atlas/state"
	"github.com/pblumer/atlas/wal"
	"github.com/pblumer/atlas/worker"
)

const jobWorkerBPMN = `<?xml version="1.0" encoding="UTF-8"?>
<bpmn:definitions xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL"
                  xmlns:zeebe="http://camunda.org/schema/zeebe/1.0" id="defs">
  <bpmn:process id="orders" isExecutable="true">
    <bpmn:startEvent id="s"/>
    <bpmn:serviceTask id="t">
      <bpmn:extensionElements><zeebe:taskDefinition type="send-email" retries="3"/></bpmn:extensionElements>
    </bpmn:serviceTask>
    <bpmn:endEvent id="e"/>
    <bpmn:sequenceFlow id="f1" sourceRef="s" targetRef="t"/>
    <bpmn:sequenceFlow id="f2" sourceRef="t" targetRef="e"/>
  </bpmn:process>
</bpmn:definitions>`

// liveAtlas starts a real Atlas over HTTP with one instance parked on a service
// task, so the worker is exercised against the actual protocol — the lease, the
// fencing token, the completion — rather than a stub of it.
func liveAtlas(t *testing.T, vars string) *httptest.Server {
	t.Helper()
	return liveAtlasWith(t, jobWorkerBPMN, vars)
}

// liveAtlasWith is liveAtlas over a given model and server options.
func liveAtlasWith(t *testing.T, model, vars string, opts ...api.Option) *httptest.Server {
	t.Helper()
	dir := t.TempDir()
	log, err := wal.Open(wal.Options{Dir: filepath.Join(dir, "wal")})
	if err != nil {
		t.Fatalf("wal.Open: %v", err)
	}
	store, err := state.Open(filepath.Join(dir, "state"))
	if err != nil {
		t.Fatalf("state.Open: %v", err)
	}
	proc := engine.New(1, log, store, nil)
	if err := proc.Recover(); err != nil {
		t.Fatalf("Recover: %v", err)
	}
	srv, err := api.New(proc, store, dir, opts...)
	if err != nil {
		t.Fatalf("api.New: %v", err)
	}
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(func() {
		ts.Close()
		srv.Close()
		_ = store.Close()
		_ = log.Close()
	})
	post(t, ts, "/api/v1/deployments", model, "application/xml")
	post(t, ts, "/api/v1/processes/1/instances", `{"variables":`+vars+`}`, "application/json")
	return ts
}

// TestWorkerRunsAJobAndCompletesIt is the whole point: a process parks on a service
// task, an out-of-process worker picks the job up by type, does the work, and the
// instance moves on — with no in-process handler involved anywhere.
func TestWorkerRunsAJobAndCompletesIt(t *testing.T) {
	ts := liveAtlas(t, `{"to":"a@example.com"}`)
	seen := make(chan worker.Job, 1)
	w := worker.New(worker.Options{
		Server: ts.URL,
		ID:     "mailer-1",
		Handlers: map[string]worker.Exec{
			"send-email": worker.ExecFunc(func(_ context.Context, j worker.Job) (map[string]any, error) {
				seen <- j
				return map[string]any{"sent": true}, nil
			}),
		},
	})

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	if err := w.RunOnce(ctx); err != nil {
		t.Fatalf("RunOnce: %v", err)
	}

	select {
	case j := <-seen:
		if j.Type != "send-email" {
			t.Errorf("job type = %q, want send-email", j.Type)
		}
		if j.LeaseToken == 0 {
			t.Error("the job carried no lease token, so its completion could not be fenced")
		}
		// The worker is handed what the task sees, so it need not fetch variables
		// separately and race a concurrent write.
		if j.Variables["to"] != "a@example.com" {
			t.Errorf("variables = %v, want to=a@example.com", j.Variables)
		}
	default:
		t.Fatal("the worker never ran the job")
	}

	// The instance finished, and the worker's output landed in it.
	if running := runningInstances(t, ts); running != 0 {
		t.Errorf("%d instances still running, want 0 — the job was not completed", running)
	}
}

// A command that fails must fail the job rather than drop it: the engine's retry and
// incident machinery is what an operator sees, and a worker that swallowed the error
// would park the token silently forever.
func TestWorkerFailsAJobWhenTheWorkFails(t *testing.T) {
	ts := liveAtlas(t, `{}`)
	w := worker.New(worker.Options{
		Server: ts.URL,
		ID:     "mailer-1",
		Handlers: map[string]worker.Exec{
			"send-email": worker.ExecFunc(func(context.Context, worker.Job) (map[string]any, error) {
				return nil, errors.New("smtp: connection refused")
			}),
		},
	})
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	if err := w.RunOnce(ctx); err != nil {
		t.Fatalf("RunOnce: %v", err)
	}

	// The job is back with one retry spent, and the reason is on it.
	jobs := instanceJobs(t, ts)
	if len(jobs) != 1 {
		t.Fatalf("%d activatable jobs, want the failed one back on offer", len(jobs))
	}
	if jobs[0].Retries != 2 {
		t.Errorf("retries = %d after one failure of three, want 2", jobs[0].Retries)
	}
}

// A worker that handles nothing is a configuration error, not a process that quietly
// polls forever.
func TestWorkerNeedsAtLeastOneHandler(t *testing.T) {
	ts := liveAtlas(t, `{}`)
	w := worker.New(worker.Options{Server: ts.URL, ID: "idle"})
	if err := w.RunOnce(context.Background()); err == nil {
		t.Error("RunOnce with no handlers succeeded, want an error naming the misconfiguration")
	}
}

// The command contract: the job arrives as JSON on stdin and result variables are
// read back as JSON from stdout — the same "stdout is the result" shape the script
// connector already uses, so one contract covers both.
func TestCmdExecPassesTheJobOnStdinAndReadsVariablesFromStdout(t *testing.T) {
	if _, err := lookPath("sh"); err != nil {
		t.Skip("no sh on this machine")
	}
	e := worker.CmdExec{Name: "sh", Args: []string{"-c",
		`cat > /tmp/atlas-worker-stdin.json; echo '{"greeting":"hello"}'`}}
	got, err := e.Run(context.Background(), worker.Job{
		JobKey: 7, Type: "send-email", Variables: map[string]any{"who": "world"},
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if got["greeting"] != "hello" {
		t.Errorf("variables = %v, want greeting=hello", got)
	}
	stdin := readFile(t, "/tmp/atlas-worker-stdin.json")
	for _, want := range []string{`"jobKey":7`, `"send-email"`, `"who":"world"`} {
		if !strings.Contains(strings.ReplaceAll(stdin, " ", ""), strings.ReplaceAll(want, " ", "")) {
			t.Errorf("the command's stdin %q does not carry %q", stdin, want)
		}
	}
}

// A command that exits non-zero fails the job, and its stderr is the message an
// operator will read on the incident.
func TestCmdExecSurfacesTheCommandsError(t *testing.T) {
	if _, err := lookPath("sh"); err != nil {
		t.Skip("no sh on this machine")
	}
	e := worker.CmdExec{Name: "sh", Args: []string{"-c", `echo "boom" >&2; exit 3`}}
	_, err := e.Run(context.Background(), worker.Job{JobKey: 1, Type: "t"})
	if err == nil {
		t.Fatal("a non-zero exit returned no error")
	}
	if !strings.Contains(err.Error(), "boom") {
		t.Errorf("error = %v, want it to carry the command's stderr", err)
	}
}

// TestWorkerSurvivesAJobTypeThatIsNotDeployedYet is the failure a supervised worker
// hits first: it starts with the server, and the process using its job type may not
// be deployed yet. The engine answers 404 for a type it has never seen — the honest
// answer, and what makes a typo visible — so the worker must treat it as "not yet"
// and keep polling rather than exiting.
func TestWorkerSurvivesAJobTypeThatIsNotDeployedYet(t *testing.T) {
	ts := liveAtlas(t, `{}`)
	w := worker.New(worker.Options{
		Server: ts.URL, ID: "early", Wait: 50 * time.Millisecond, Retry: 10 * time.Millisecond,
		Handlers: map[string]worker.Exec{
			"never-deployed": worker.ExecFunc(func(context.Context, worker.Job) (map[string]any, error) {
				return nil, nil
			}),
		},
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- w.Run(ctx) }()

	select {
	case err := <-done:
		t.Fatalf("the worker exited on an unknown job type instead of waiting for it: %v", err)
	case <-time.After(400 * time.Millisecond):
		// Still polling, which is the point.
	}
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Errorf("Run returned %v after cancellation, want nil", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the worker did not stop when its context was cancelled")
	}
}

// TestWorkerRunsAnOffloadedCsvConnector is ADR-0166's first slice end to end: a
// connector kind the engine no longer serves, worked by an external process. The
// engine resolved the task — found its detail in the compiled process and read the
// source text up the scope chain — and the worker did the parsing, which needs
// nothing but the values it was handed. No credential is involved, which is exactly
// why this kind goes first: the mechanism is proved before any secret rides on it.
func TestWorkerRunsAnOffloadedCsvConnector(t *testing.T) {
	ts := liveAtlasWith(t, csvConnectorModel, `{"upload":"name,amount\nAda,12\nGrace,7\n"}`,
		api.WithOffloadedConnectorKinds([]string{"csv"}))

	w := worker.New(worker.Options{
		Server: ts.URL, ID: "csv-1", Handlers: worker.BuiltinConnectors("csv"),
	})
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	if err := w.RunOnce(ctx); err != nil {
		t.Fatalf("RunOnce: %v", err)
	}

	if running := runningInstances(t, ts); running != 0 {
		t.Errorf("%d instances still running, want 0 — the connector job was not completed", running)
	}
	vars := instanceVariables(t, ts)
	rows, ok := vars["orders"]
	if !ok {
		t.Fatalf("the result variable was not written; variables = %v", vars)
	}
	list, ok := rows.([]any)
	if !ok || len(list) != 2 {
		t.Fatalf("orders = %v, want the two parsed rows", rows)
	}
	if first, _ := list[0].(map[string]any); first["name"] != "Ada" {
		t.Errorf("first row = %v, want Ada", list[0])
	}
	if vars["rowCount"] != float64(2) {
		t.Errorf("rowCount = %v, want 2", vars["rowCount"])
	}
}

// A worker asked for a connector kind it does not implement is a configuration
// error, not a process that leases work it cannot do.
func TestBuiltinConnectorsRejectsAnUnknownKind(t *testing.T) {
	if got := worker.BuiltinConnectors("no-such-kind"); len(got) != 0 {
		t.Errorf("BuiltinConnectors returned %d handlers for an unknown kind, want none", len(got))
	}
}

const csvConnectorModel = `<?xml version="1.0" encoding="UTF-8"?>
<bpmn:definitions xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL"
                  xmlns:atlas="http://atlas.dev/schema/1.0" id="defs">
  <bpmn:process id="import" isExecutable="true">
    <bpmn:startEvent id="s"/>
    <bpmn:serviceTask id="t">
      <bpmn:extensionElements>
        <atlas:csvConnector source="upload" resultVariable="orders" hasHeader="true"/>
      </bpmn:extensionElements>
    </bpmn:serviceTask>
    <bpmn:endEvent id="e"/>
    <bpmn:sequenceFlow id="f1" sourceRef="s" targetRef="t"/>
    <bpmn:sequenceFlow id="f2" sourceRef="t" targetRef="e"/>
  </bpmn:process>
</bpmn:definitions>`
