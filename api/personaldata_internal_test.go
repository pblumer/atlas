package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pblumer/atlas/api/vault"
	"github.com/pblumer/atlas/engine"
	"github.com/pblumer/atlas/model"
	"github.com/pblumer/atlas/state"
	"github.com/pblumer/atlas/wal"
)

// W5 of ADR-0314, the enciphering half, end to end over the real HTTP surface.
//
// The declaration and the compiler's refusal (compiler/personal_test.go) only say what a
// model may not do. What these tests pin is the mechanism: a declared value is ciphertext
// everywhere the engine can see it, plaintext exactly at the two edges the record permits,
// and permanently unreadable after one erasure — with the instance's state untouched.

// personalBPMN declares vorname personal, personalnummer as the data subject, and parks
// at a service task no in-process worker serves, so there is an activatable job to
// inspect the payload of.
const personalBPMN = `<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL"
                    xmlns:atlas="http://atlas/schema/1.0"
                    xmlns:zeebe="http://camunda.org/schema/zeebe/1.0">
  <process id="order" isExecutable="true" atlas:personal="vorname" atlas:dataSubject="personalnummer">
    <startEvent id="start"/>
    <serviceTask id="provision">
      <extensionElements><zeebe:taskDefinition type="provision" retries="5"/></extensionElements>
    </serviceTask>
    <endEvent id="end"/>
    <sequenceFlow id="f1" sourceRef="start" targetRef="provision"/>
    <sequenceFlow id="f2" sourceRef="provision" targetRef="end"/>
  </process>
</definitions>`

// startPersonalInstance deploys personalBPMN, starts one instance with a subject and a
// personal value, and returns the definition key, the parked job and the instance.
func startPersonalInstance(t *testing.T, srv *Server, vars string) (defKey, jobKey, instKey uint64) {
	t.Helper()
	code, body := serveInternal(t, srv, http.MethodPost, "/api/v1/deployments", personalBPMN, "application/xml")
	if code != http.StatusOK {
		t.Fatalf("deploy: status=%d body=%s", code, body)
	}
	var deploy struct {
		Key uint64 `json:"key"`
	}
	if err := json.Unmarshal(body, &deploy); err != nil {
		t.Fatalf("decode deploy: %v (%s)", err, body)
	}
	path := fmt.Sprintf("/api/v1/processes/%d/instances", deploy.Key)
	if code, body = serveInternal(t, srv, http.MethodPost, path, vars, "application/json"); code != http.StatusOK {
		t.Fatalf("create instance: status=%d body=%s", code, body)
	}
	jobKey, instKey = parkedServiceJob(t, srv, deploy.Key)
	return deploy.Key, jobKey, instKey
}

// rootVar reads one variable of the instance root scope exactly as it is stored — the
// bytes the WAL, the checkpoint, the export and every backup carry.
func rootVar(t *testing.T, srv *Server, instKey uint64, name string) model.VariableValue {
	t.Helper()
	var got model.VariableValue
	var found bool
	srv.do(func() {
		_ = srv.store.VariablesOfScope(instKey, func(v *model.VariableValue) error {
			if v.Name == name {
				got, found = *v, true
			}
			return nil
		})
	})
	if !found {
		t.Fatalf("instance %d has no variable %q", instKey, name)
	}
	return got
}

// TestASubmittedPersonalValueIsStoredEnciphered is the property every other one rests on.
// If the value reaches the engine in the clear, nothing downstream matters: the WAL
// already has it, and so will every copy made from the WAL.
func TestASubmittedPersonalValueIsStoredEnciphered(t *testing.T) {
	srv := newServerForErrors(t)
	_, _, instKey := startPersonalInstance(t, srv, `{"variables":{"personalnummer":"P-4711","vorname":"Ida"}}`)

	stored := rootVar(t, srv, instKey, "vorname")
	if stored.Kind != model.VarJSON || !vault.IsEnciphered(stored.Text) {
		t.Fatalf("vorname is stored as kind=%d text=%q, which is not an envelope", stored.Kind, stored.Text)
	}
	if strings.Contains(stored.Text, "Ida") {
		t.Errorf("the stored value carries the plaintext: %s", stored.Text)
	}
	env, _ := vault.ParseEnvelope(stored.Text)
	if env.Subject != "P-4711" {
		t.Errorf("sealed under subject %q, want P-4711", env.Subject)
	}
	if model.VarKind(env.Kind) != model.VarString {
		t.Errorf("the envelope remembers kind %d, want VarString so the value returns as the type it had", env.Kind)
	}
	// The data subject's own id stays readable: it is a reference, and the enciphering
	// edge needs it to find the key at all.
	if subj := rootVar(t, srv, instKey, "personalnummer"); subj.Kind != model.VarString || subj.Text != "P-4711" {
		t.Errorf("personalnummer = kind %d %q, want the plain string P-4711", subj.Kind, subj.Text)
	}
}

// TestASubmissionWithNoDataSubjectIsRefused is the fail-closed end. The alternatives are
// both silent: storing the value in the clear leaves it un-erasable forever, and sealing
// it under an empty subject gives every instance one shared key.
func TestASubmissionWithNoDataSubjectIsRefused(t *testing.T) {
	srv := newServerForErrors(t)
	code, body := serveInternal(t, srv, http.MethodPost, "/api/v1/deployments", personalBPMN, "application/xml")
	if code != http.StatusOK {
		t.Fatalf("deploy: status=%d body=%s", code, body)
	}
	var deploy struct {
		Key uint64 `json:"key"`
	}
	if err := json.Unmarshal(body, &deploy); err != nil {
		t.Fatalf("decode deploy: %v", err)
	}
	path := fmt.Sprintf("/api/v1/processes/%d/instances", deploy.Key)
	code, body = serveInternal(t, srv, http.MethodPost, path, `{"variables":{"vorname":"Ida"}}`, "application/json")
	if code != http.StatusBadRequest {
		t.Fatalf("a personal value with no data subject was accepted: status=%d body=%s", code, body)
	}
	if !strings.Contains(string(body), "personalnummer") {
		t.Errorf("the refusal does not name the variable to supply: %s", body)
	}
	// A submission carrying neither is ordinary and must still work: the rule is about
	// personal values, not about every start of a process that declares one.
	if code, body = serveInternal(t, srv, http.MethodPost, path, `{"variables":{"kontotyp":"A"}}`, "application/json"); code != http.StatusOK {
		t.Errorf("a start with no personal value at all was refused: status=%d body=%s", code, body)
	}
}

// TestTheWorkerPayloadCarriesPlaintext is the first of the two edges the record permits:
// the job payload handed to a worker after fsync. A worker that received the envelope
// could not do the one thing ADR-0314 sends it: build the value the target system needs.
func TestTheWorkerPayloadCarriesPlaintext(t *testing.T) {
	srv := newServerForErrors(t)
	_, jobKey, _ := startPersonalInstance(t, srv, `{"variables":{"personalnummer":"P-4711","vorname":"Ida"}}`)

	jobs := activateProvisionJobs(t, srv)
	if len(jobs) != 1 {
		t.Fatalf("activated %d jobs, want 1", len(jobs))
	}
	if got := jobs[0].Variables["vorname"]; got != "Ida" {
		t.Errorf("the worker's payload carries vorname = %#v, want the plaintext %q", got, "Ida")
	}
	_ = jobKey
}

// activatedJob is what a worker receives from the type-keyed pull: the payload the
// plaintext has to be in, because that is the edge ADR-0314 permits it at.
type activatedJob struct {
	JobKey    uint64         `json:"jobKey"`
	Variables map[string]any `json:"variables"`
}

// activateProvisionJobs leases whatever is waiting on the example's job type, the way an
// external worker does.
func activateProvisionJobs(t *testing.T, srv *Server) []activatedJob {
	t.Helper()
	code, body := serveInternal(t, srv, http.MethodPost, "/api/v1/jobs/activate",
		`{"type":"provision","worker":"w1","maxJobs":5}`, "application/json")
	if code != http.StatusOK {
		t.Fatalf("activate jobs: status=%d body=%s", code, body)
	}
	var resp struct {
		Jobs []activatedJob `json:"jobs"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		t.Fatalf("decode jobs: %v (%s)", err, body)
	}
	return resp.Jobs
}

// TestEveryConnectorExpressionSeesPlaintext is the test this whole change owes.
//
// compiler/personal.go exempts 74 connector expression fields from the refusal on the
// grounds that a worker evaluates them, where ADR-0314 permits the plaintext to exist for
// the duration of one call. Every one of those connectors builds its FEEL bindings through
// state.VisibleVariablesMap over a state.Reader — so if that reader yields envelopes, all
// 74 silently evaluate FEEL over ciphertext and the exemption is worthless while still
// looking correct.
//
// This asserts at that exact seam, which is why it is worth more than a test through one
// connector: it covers every connector that exists and every one that will be added.
func TestEveryConnectorExpressionSeesPlaintext(t *testing.T) {
	srv := newServerForErrors(t)
	_, _, instKey := startPersonalInstance(t, srv, `{"variables":{"personalnummer":"P-4711","vorname":"Ida"}}`)

	var raw, opened map[string]model.VariableValue
	var rawErr, openErr error
	srv.do(func() {
		raw, rawErr = state.VisibleVariablesMap(srv.store, instKey)
		opened, openErr = state.VisibleVariablesMap(srv.personalReader(srv.store), instKey)
	})
	if rawErr != nil || openErr != nil {
		t.Fatalf("read variables: %v / %v", rawErr, openErr)
	}
	if !vault.IsEnciphered(raw["vorname"].Text) {
		t.Fatal("the unwrapped reader does not yield an envelope, so this test compares nothing")
	}
	got := opened["vorname"]
	if got.Kind != model.VarString || got.Text != "Ida" {
		t.Errorf("a connector would bind vorname as kind=%d %q, want the plain string %q", got.Kind, got.Text, "Ida")
	}
	// And it does not disturb anything else it passes through.
	if opened["personalnummer"].Text != "P-4711" {
		t.Errorf("the opening reader changed an ordinary value: %q", opened["personalnummer"].Text)
	}
}

// TestAWorkerResultIsEncipheredOnTheWayBackIn closes the loop. The worker built the value
// from plaintext; what it reports back must be sealed again before it becomes a command,
// or the first round trip through a worker would put the plaintext in the log.
func TestAWorkerResultIsEncipheredOnTheWayBackIn(t *testing.T) {
	srv := newServerForErrors(t)
	_, jobKey, instKey := startPersonalInstance(t, srv, `{"variables":{"personalnummer":"P-4711"}}`)

	path := fmt.Sprintf("/api/v1/jobs/%d/complete", jobKey)
	code, body := serveInternal(t, srv, http.MethodPost, path, `{"reason":"test: worker result","variables":{"vorname":"Ida"}}`, "application/json")
	if code != http.StatusOK {
		t.Fatalf("complete job: status=%d body=%s", code, body)
	}
	stored := rootVar(t, srv, instKey, "vorname")
	if !vault.IsEnciphered(stored.Text) {
		t.Fatalf("a worker's output landed in the clear: kind=%d text=%q", stored.Kind, stored.Text)
	}
	if strings.Contains(stored.Text, "Ida") {
		t.Errorf("the stored value carries the plaintext: %s", stored.Text)
	}
}

// TestAnOperatorOverrideIsEncipheredToo keeps the one hand-written path from being the
// hole in the mechanism. An operator correcting a stuck instance is not an exception to
// erasability.
func TestAnOperatorOverrideIsEncipheredToo(t *testing.T) {
	srv := newServerForErrors(t)
	_, _, instKey := startPersonalInstance(t, srv, `{"variables":{"personalnummer":"P-4711"}}`)

	path := fmt.Sprintf("/api/v1/instances/%d/variables", instKey)
	code, body := serveInternal(t, srv, http.MethodPost, path, `{"variables":{"vorname":"Ida"}}`, "application/json")
	if code != http.StatusOK {
		t.Fatalf("set variables: status=%d body=%s", code, body)
	}
	if stored := rootVar(t, srv, instKey, "vorname"); !vault.IsEnciphered(stored.Text) {
		t.Errorf("an operator's write landed in the clear: kind=%d text=%q", stored.Kind, stored.Text)
	}
}

// TestErasingASubjectLeavesTheEngineUntouched is the record's central claim tested where
// it matters most: erasure must not touch the log, the state or replay. What changes is
// one key in the vault, and the consequence is that the same bytes no longer mean
// anything — in this store and in every copy of it that was ever made.
func TestErasingASubjectLeavesTheEngineUntouched(t *testing.T) {
	srv := newServerForErrors(t)
	_, jobKey, instKey := startPersonalInstance(t, srv, `{"variables":{"personalnummer":"P-4711","vorname":"Ida"}}`)
	before := rootVar(t, srv, instKey, "vorname")

	code, body := serveInternal(t, srv, http.MethodDelete, "/api/v1/personal-data/P-4711", "", "")
	if code != http.StatusOK {
		t.Fatalf("erase: status=%d body=%s", code, body)
	}
	var erasure struct {
		Subject string `json:"subject"`
		Erased  bool   `json:"erased"`
	}
	if err := json.Unmarshal(body, &erasure); err != nil || !erasure.Erased || erasure.Subject != "P-4711" {
		t.Fatalf("erase reported %s (err %v)", body, err)
	}

	// The state record is byte-for-byte what it was. Nothing was rewritten, which is why
	// replay still produces exactly this state (I4, I6).
	after := rootVar(t, srv, instKey, "vorname")
	if after.Text != before.Text || after.Kind != before.Kind {
		t.Errorf("erasure changed the stored value: %q → %q", before.Text, after.Text)
	}

	// And the value is now unreadable at the edge that used to open it. The job is
	// withheld rather than handed over with a blank or a ciphertext name: an erased
	// subject's live instance can no longer provision, which is the consequence ADR-0314
	// states and accepts.
	for _, j := range activateProvisionJobs(t, srv) {
		if j.JobKey == jobKey {
			t.Errorf("an erased subject's job was still handed to a worker with %#v", j.Variables)
		}
	}

	// A repeated request is not an error, and says that there was nothing left to erase.
	code, body = serveInternal(t, srv, http.MethodDelete, "/api/v1/personal-data/P-4711", "", "")
	if code != http.StatusOK {
		t.Fatalf("second erase: status=%d body=%s", code, body)
	}
	if err := json.Unmarshal(body, &erasure); err != nil || erasure.Erased {
		t.Errorf("the second erasure reported %s, want erased=false", body)
	}
}

// TestTheDataSubjectListingNamesWhoIsStillErasable is the operator's view of the one
// thing an erasure acts on. It reveals ids and no content, which is the part ADR-0314
// deliberately keeps readable.
func TestTheDataSubjectListingNamesWhoIsStillErasable(t *testing.T) {
	srv := newServerForErrors(t)
	startPersonalInstance(t, srv, `{"variables":{"personalnummer":"P-4711","vorname":"Ida"}}`)

	code, body := serveInternal(t, srv, http.MethodGet, "/api/v1/personal-data", "", "")
	if code != http.StatusOK {
		t.Fatalf("list: status=%d body=%s", code, body)
	}
	var subjects []struct {
		Name string `json:"name"`
	}
	if err := json.Unmarshal(body, &subjects); err != nil {
		t.Fatalf("decode: %v (%s)", err, body)
	}
	if len(subjects) != 1 || subjects[0].Name != "P-4711" {
		t.Errorf("data subjects = %s, want only P-4711", body)
	}
	// The same key must not appear among the operator's secrets, where deleting it would
	// look like removing a credential.
	if code, body = serveInternal(t, srv, http.MethodGet, "/api/v1/secrets", "", ""); code != http.StatusOK {
		t.Fatalf("list secrets: status=%d body=%s", code, body)
	}
	if strings.Contains(string(body), "P-4711") {
		t.Errorf("a data key is listed as an operator secret: %s", body)
	}
}

// TestADataKeyCannotBeTouchedThroughTheSecretsAPI closes the way round the erasure route.
// Overwriting a data key would make a subject's data unreadable without erasing it —
// the same effect, with no record that it happened and no intent to do it.
func TestADataKeyCannotBeTouchedThroughTheSecretsAPI(t *testing.T) {
	srv := newServerForErrors(t)
	startPersonalInstance(t, srv, `{"variables":{"personalnummer":"P-4711","vorname":"Ida"}}`)

	name := vault.DataKeyName("P-4711")
	if code, body := serveInternal(t, srv, http.MethodPut, "/api/v1/secrets/"+name, `{"value":"AAAA"}`, "application/json"); code != http.StatusForbidden {
		t.Errorf("overwriting a data key through the secrets API: status=%d body=%s", code, body)
	}
	if code, body := serveInternal(t, srv, http.MethodDelete, "/api/v1/secrets/"+name, "", ""); code != http.StatusForbidden {
		t.Errorf("deleting a data key through the secrets API: status=%d body=%s", code, body)
	}
	// It is still readable, so neither attempt did anything. Both reads happen before
	// the run-loop visit below: rootVar dispatches onto the loop itself, and calling it
	// from inside do() would deadlock the server against its own single writer.
	stored := rootVar(t, srv, mustInstanceOf(t, srv), "vorname")
	if !vault.IsEnciphered(stored.Text) {
		t.Fatal("the value is no longer an envelope")
	}
	env, ok := vault.ParseEnvelope(stored.Text)
	if !ok {
		t.Fatal("the stored value does not parse as an envelope")
	}
	var opened string
	var openErr error
	srv.do(func() { opened, openErr = srv.vault.Open("vorname", env) })
	if openErr != nil || opened != "Ida" {
		t.Errorf("the data key no longer opens its value: %q, %v", opened, openErr)
	}
}

// mustInstanceOf returns the single process instance in the store, for tests that do not
// carry its key around.
func mustInstanceOf(t *testing.T, srv *Server) uint64 {
	t.Helper()
	var key uint64
	srv.do(func() {
		_ = srv.store.ActiveElementInstances(func(_ uint64, v *model.ElementInstanceValue) error {
			if key == 0 {
				key = v.ProcessInstanceKey
			}
			return nil
		})
	})
	if key == 0 {
		t.Fatal("no instance in the store")
	}
	return key
}

// TestAnEncipheredValueIsLabelledNotShown answers the question ADR-0314 left for the
// variable audit: a view that is about *what happened to a value* reports that it is
// personal and whose, rather than printing base64 nobody can read or spending a vault
// read per row.
func TestAnEncipheredValueIsLabelledNotShown(t *testing.T) {
	srv := newServerForErrors(t)
	_, _, instKey := startPersonalInstance(t, srv, `{"variables":{"personalnummer":"P-4711","vorname":"Ida"}}`)

	view := toVariableView(ptrOf(rootVar(t, srv, instKey, "vorname")))
	if view.Kind != "personal" {
		t.Errorf("kind = %q, want personal", view.Kind)
	}
	if !strings.Contains(view.Value, "P-4711") {
		t.Errorf("the view does not say whose data it is: %q", view.Value)
	}
	if strings.Contains(view.Value, "Ida") || strings.Contains(view.Value, "atlas:personal") {
		t.Errorf("the view shows the value or the raw envelope: %q", view.Value)
	}
}

func ptrOf(v model.VariableValue) *model.VariableValue { return &v }

// TestAPersonalDeployIsRefusedWithoutAVault keeps the declaration from becoming a lie on
// a server that cannot honour it. With no vault there is no key, so the values would be
// stored in the clear while the model said they were protected.
func TestAPersonalDeployIsRefusedWithoutAVault(t *testing.T) {
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
	srv, err := New(proc, store, dir, WithoutVault())
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(func() {
		srv.Close()
		_ = store.Close()
		_ = log.Close()
	})

	code, body := serveInternal(t, srv, http.MethodPost, "/api/v1/deployments", personalBPMN, "application/xml")
	if code == http.StatusOK {
		t.Fatalf("a process declaring personal data deployed with the vault disabled: %s", body)
	}
	if !strings.Contains(string(body), "vault") {
		t.Errorf("the refusal does not say why: %s", body)
	}
}
