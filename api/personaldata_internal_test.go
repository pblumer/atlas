package api

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/pblumer/atlas/api/vault"
	"github.com/pblumer/atlas/compiler"
	"github.com/pblumer/atlas/engine"
	"github.com/pblumer/atlas/logging"
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
	_, _, instKey := startPersonalInstance(t, srv, `{"variables":{"personalnummer":"P-4711","vorname":"Ida-Luise-Mustermann"}}`)

	stored := rootVar(t, srv, instKey, "vorname")
	if stored.Kind != model.VarJSON || !vault.IsEnciphered(stored.Text) {
		t.Fatalf("vorname is stored as kind=%d text=%q, which is not an envelope", stored.Kind, stored.Text)
	}
	// The name is twenty characters for a measured reason, recorded at
	// vault.TestTheStoredTextHoldsNoPlaintext: this search runs over base64, where a
	// three-letter needle collides by chance once in 5 882 seals.
	if strings.Contains(stored.Text, "Ida-Luise-Mustermann") {
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
	code, body = serveInternal(t, srv, http.MethodPost, path, `{"variables":{"vorname":"Ida-Luise-Mustermann"}}`, "application/json")
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
	_, jobKey, _ := startPersonalInstance(t, srv, `{"variables":{"personalnummer":"P-4711","vorname":"Ida-Luise-Mustermann"}}`)

	jobs := activateProvisionJobs(t, srv)
	if len(jobs) != 1 {
		t.Fatalf("activated %d jobs, want 1", len(jobs))
	}
	if got := jobs[0].Variables["vorname"]; got != "Ida-Luise-Mustermann" {
		t.Errorf("the worker's payload carries vorname = %#v, want the plaintext %q", got, "Ida-Luise-Mustermann")
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
	_, _, instKey := startPersonalInstance(t, srv, `{"variables":{"personalnummer":"P-4711","vorname":"Ida-Luise-Mustermann"}}`)

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
	if got.Kind != model.VarString || got.Text != "Ida-Luise-Mustermann" {
		t.Errorf("a connector would bind vorname as kind=%d %q, want the plain string %q", got.Kind, got.Text, "Ida-Luise-Mustermann")
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
	code, body := serveInternal(t, srv, http.MethodPost, path, `{"reason":"test: worker result","variables":{"vorname":"Ida-Luise-Mustermann"}}`, "application/json")
	if code != http.StatusOK {
		t.Fatalf("complete job: status=%d body=%s", code, body)
	}
	stored := rootVar(t, srv, instKey, "vorname")
	if !vault.IsEnciphered(stored.Text) {
		t.Fatalf("a worker's output landed in the clear: kind=%d text=%q", stored.Kind, stored.Text)
	}
	// The name is twenty characters for a measured reason, recorded at
	// vault.TestTheStoredTextHoldsNoPlaintext: this search runs over base64, where a
	// three-letter needle collides by chance once in 5 882 seals.
	if strings.Contains(stored.Text, "Ida-Luise-Mustermann") {
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
	code, body := serveInternal(t, srv, http.MethodPost, path, `{"variables":{"vorname":"Ida-Luise-Mustermann"}}`, "application/json")
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
	_, jobKey, instKey := startPersonalInstance(t, srv, `{"variables":{"personalnummer":"P-4711","vorname":"Ida-Luise-Mustermann"}}`)
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

	// And the form read says so plainly rather than failing as a server error: the
	// instance holds a value nobody can read any more, which is the intended outcome and
	// not a fault to debug.
	code, body = serveInternal(t, srv, http.MethodGet, fmt.Sprintf("/api/v1/instances/%d/variables", instKey), "", "")
	if code != http.StatusConflict {
		t.Errorf("reading an erased instance's variables: status=%d body=%s, want 409", code, body)
	}
	if !strings.Contains(string(body), "erased") {
		t.Errorf("the refusal does not say what happened: %s", body)
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

// captureAuditLog points the process logger at a buffer in the JSON shape an operator
// ships to a SIEM, and restores stderr afterwards. The api_test package has its own copy;
// an internal test cannot reach it, and duplicating fifteen lines beats exporting a test
// helper from production code.
type auditSink struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *auditSink) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *auditSink) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

func captureAuditLog(t *testing.T) *auditSink {
	t.Helper()
	sink := &auditSink{}
	if err := logging.Setup(sink, logging.FormatJSON); err != nil {
		t.Fatalf("logging.Setup: %v", err)
	}
	t.Cleanup(func() { _ = logging.Setup(os.Stderr, logging.DefaultFormat) })
	return sink
}

// TestAnErasureLeavesAnAuditLine is not a nicety. After an erasure the key is gone, the
// ciphertext says nothing and the subject leaves no other trace anywhere in Atlas — so this
// line is the *only* remaining evidence that a deletion request was honoured, on the date it
// was honoured, by whom. Demonstrability is half of what the obligation asks for, and
// without it an operator would have destroyed the data and be unable to show it.
func TestAnErasureLeavesAnAuditLine(t *testing.T) {
	srv := newServerForErrors(t)
	startPersonalInstance(t, srv, `{"variables":{"personalnummer":"P-4711","vorname":"Ida-Luise-Mustermann"}}`)

	sink := captureAuditLog(t)
	if code, body := serveInternal(t, srv, http.MethodDelete, "/api/v1/personal-data/P-4711", "", ""); code != http.StatusOK {
		t.Fatalf("erase: status=%d body=%s", code, body)
	}
	line := sink.String()
	for _, want := range []string{"personal_data.erased", "P-4711"} {
		if !strings.Contains(line, want) {
			t.Errorf("the audit trail does not record %q: %s", want, line)
		}
	}

	// And the attempt to reach a data key through the secrets API is a security-relevant
	// refusal, so it is recorded too.
	if code, _ := serveInternal(t, srv, http.MethodPut, "/api/v1/secrets/"+vault.DataKeyName("P-0815"), `{"value":"AAAA"}`, "application/json"); code != http.StatusForbidden {
		t.Fatalf("the secrets API accepted a data-key name: %d", code)
	}
	if !strings.Contains(sink.String(), "personal_data.key_write_refused") {
		t.Errorf("a refused data-key write is not audited: %s", sink.String())
	}
}

// TestTheDataSubjectListingNamesWhoIsStillErasable is the operator's view of the one
// thing an erasure acts on. It reveals ids and no content, which is the part ADR-0314
// deliberately keeps readable.
func TestTheDataSubjectListingNamesWhoIsStillErasable(t *testing.T) {
	srv := newServerForErrors(t)
	startPersonalInstance(t, srv, `{"variables":{"personalnummer":"P-4711","vorname":"Ida-Luise-Mustermann"}}`)

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
	startPersonalInstance(t, srv, `{"variables":{"personalnummer":"P-4711","vorname":"Ida-Luise-Mustermann"}}`)

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
	if openErr != nil || opened != "Ida-Luise-Mustermann" {
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
	_, _, instKey := startPersonalInstance(t, srv, `{"variables":{"personalnummer":"P-4711","vorname":"Ida-Luise-Mustermann"}}`)

	view := toVariableView(ptrOf(rootVar(t, srv, instKey, "vorname")))
	if view.Kind != "personal" {
		t.Errorf("kind = %q, want personal", view.Kind)
	}
	if !strings.Contains(view.Value, "P-4711") {
		t.Errorf("the view does not say whose data it is: %q", view.Value)
	}
	// Long name, same measured reason as above: a short one collides with base64 by chance.
	if strings.Contains(view.Value, "Ida-Luise-Mustermann") || strings.Contains(view.Value, "atlas:personal") {
		t.Errorf("the view shows the value or the raw envelope: %q", view.Value)
	}
}

func ptrOf(v model.VariableValue) *model.VariableValue { return &v }

// newServerWithoutVault is a server started the way `--vault=false` starts one: nothing can
// be sealed, so nothing that declares personal data may run.
func newServerWithoutVault(t *testing.T) *Server {
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
	srv, err := New(proc, store, dir, WithoutVault())
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(func() {
		srv.Close()
		_ = store.Close()
		_ = log.Close()
	})
	return srv
}

// TestAPersonalDeployIsRefusedWithoutAVault keeps the declaration from becoming a lie on
// a server that cannot honour it. With no vault there is no key, so the values would be
// stored in the clear while the model said they were protected.
func TestAPersonalDeployIsRefusedWithoutAVault(t *testing.T) {
	srv := newServerWithoutVault(t)
	code, body := serveInternal(t, srv, http.MethodPost, "/api/v1/deployments", personalBPMN, "application/xml")
	if code == http.StatusOK {
		t.Fatalf("a process declaring personal data deployed with the vault disabled: %s", body)
	}
	if !strings.Contains(string(body), "vault") {
		t.Errorf("the refusal does not say why: %s", body)
	}
}

// TestTheErasureRoutesAnswerWithoutAVault is what an operator meets on a server that runs
// without one: the routes exist, and they say the vault is not configured rather than
// reporting an empty list of data subjects — which would read as "nobody has personal data
// here" when the truth is that nothing could have been sealed in the first place.
func TestTheErasureRoutesAnswerWithoutAVault(t *testing.T) {
	srv := newServerWithoutVault(t)
	for _, tc := range []struct{ method, path string }{
		{http.MethodGet, "/api/v1/personal-data"},
		{http.MethodDelete, "/api/v1/personal-data/P-4711"},
	} {
		if code, body := serveInternal(t, srv, tc.method, tc.path, "", ""); code != http.StatusServiceUnavailable {
			t.Errorf("%s %s: status=%d body=%s, want 503", tc.method, tc.path, code, body)
		}
	}
	// And the reader a connector would be handed is the plain one, because with no vault
	// there is nothing to open.
	if got := srv.personalReader(srv.store); got != state.Reader(srv.store) {
		t.Error("a server with no vault still wraps its reader, which would cost every read a parse for nothing")
	}
	// Sealing refuses rather than storing the value readable, which is the one thing it
	// must never do. This path is unreachable through a handler — the deploy above is
	// refused first — and is the layer under it.
	vars := []model.VariableValue{{Name: "vorname", Kind: model.VarString, Text: "Ida-Luise-Mustermann"}}
	cp := mustCompilePersonal(t, srv)
	if err := srv.seal(personalSealing{cp: cp, subject: "P-4711"}, vars); err == nil {
		t.Error("sealing succeeded with no vault")
	} else if !strings.Contains(err.Error(), "vault") {
		t.Errorf("the refusal does not say why: %v", err)
	}
	if vars[0].Text != "Ida-Luise-Mustermann" || vars[0].Kind != model.VarString {
		t.Errorf("a refused seal changed the value anyway: %+v", vars[0])
	}
}

// mustCompilePersonal compiles the fixture process without deploying it, for the layers
// below the handlers.
func mustCompilePersonal(t *testing.T, _ *Server) *compiler.CompiledProcess {
	t.Helper()
	deployables, err := compiler.ParseAll(1, 1, strings.NewReader(personalBPMN))
	if err != nil {
		t.Fatalf("compile the fixture: %v", err)
	}
	return deployables[0].Process
}

// TestADataSubjectMustBeAnIdentifierNotAValue is the fail-closed reading of the subject. The
// vault names a key by a string, so a boolean or a structured value is not a subject anybody
// could later ask an erasure for — and accepting one would seal data under a name no request
// can reach.
func TestADataSubjectMustBeAnIdentifierNotAValue(t *testing.T) {
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
	for _, subject := range []string{`true`, `{"nr":4711}`, `["P-4711"]`, `null`} {
		body := `{"variables":{"personalnummer":` + subject + `,"vorname":"Ida-Luise-Mustermann"}}`
		if code, resp := serveInternal(t, srv, http.MethodPost, path, body, "application/json"); code != http.StatusBadRequest {
			t.Errorf("a data subject of %s was accepted: status=%d body=%s", subject, code, resp)
		}
	}
	// A number is an identifier — a personnel number usually is one — so it is accepted.
	if code, resp := serveInternal(t, srv, http.MethodPost, path, `{"variables":{"personalnummer":4711,"vorname":"Ida-Luise-Mustermann"}}`, "application/json"); code != http.StatusOK {
		t.Errorf("a numeric data subject was refused: status=%d body=%s", code, resp)
	}
}

// TestSealingIsIdempotentAndKeepsTheValuesType covers the two things the sealing loop has to
// get right beyond the happy path: a value that is already an envelope must not be wrapped
// twice — a re-submitted form would otherwise nest one envelope in another and the opened
// value would come back as JSON — and a boolean has to return as a boolean.
func TestSealingIsIdempotentAndKeepsTheValuesType(t *testing.T) {
	srv := newServerForErrors(t)
	cp := mustCompilePersonal(t, srv)
	p := personalSealing{cp: cp, subject: "P-4711"}

	vars := []model.VariableValue{
		{Name: "vorname", Kind: model.VarBool, Bool: true},
		{Name: "kontotyp", Kind: model.VarString, Text: "A"},
		{Name: "leer", Kind: model.VarNull},
	}
	if err := srv.seal(p, vars); err != nil {
		t.Fatalf("seal: %v", err)
	}
	if !vault.IsEnciphered(vars[0].Text) {
		t.Fatalf("the boolean was not sealed: %+v", vars[0])
	}
	if vars[1].Text != "A" || vars[2].Kind != model.VarNull {
		t.Errorf("an undeclared value or a null was touched: %+v %+v", vars[1], vars[2])
	}

	sealedOnce := vars[0].Text
	if err := srv.seal(p, vars); err != nil {
		t.Fatalf("second seal: %v", err)
	}
	if vars[0].Text != sealedOnce {
		t.Errorf("a sealed value was sealed again: %s then %s", sealedOnce, vars[0].Text)
	}

	// And it opens back as the boolean it was, which is what the envelope's kind is for.
	opened, err := srv.personalReader(srv.store).(personalReader).open(&vars[0])
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if opened.Kind != model.VarBool || !opened.Bool || opened.Text != "" {
		t.Errorf("the boolean came back as %+v", *opened)
	}
}

// TestSealingAsksNothingOfAnUnknownInstance keeps the edges from failing a request because
// the thing they were asked about is gone. A job completed twice, an instance cancelled
// between two calls: the edge finds no definition, seals nothing, and leaves the handler to
// report the 404 it was going to report anyway.
func TestSealingAsksNothingOfAnUnknownInstance(t *testing.T) {
	srv := newServerForErrors(t)
	vars := []model.VariableValue{{Name: "vorname", Kind: model.VarString, Text: "Ida-Luise-Mustermann"}}
	if err := srv.encipherScopeVars(999999, vars); err != nil {
		t.Errorf("encipherScopeVars on an unknown scope: %v", err)
	}
	if err := srv.encipherJobVars(999999, vars); err != nil {
		t.Errorf("encipherJobVars on an unknown job: %v", err)
	}
	if err := srv.encipherStartVars(999999, vars); err != nil {
		t.Errorf("encipherStartVars on an unknown definition: %v", err)
	}
	if vars[0].Text != "Ida-Luise-Mustermann" {
		t.Errorf("a value was sealed for an instance that does not exist: %+v", vars[0])
	}
	// No variables is the commonest completion of all, and it must not cost a visit to the
	// run loop at all.
	if err := srv.encipherScopeVars(999999, nil); err != nil {
		t.Errorf("encipherScopeVars with no variables: %v", err)
	}
	var cp *compiler.CompiledProcess
	srv.do(func() { cp = srv.compiledOfScope(999999) })
	if cp != nil {
		t.Error("compiledOfScope found a definition for a scope that does not exist")
	}
}

// TestErasingNeedsASubject is the last refusal on the route: a blank subject would delete a
// vault entry named by the prefix alone, which belongs to nobody.
func TestErasingNeedsASubject(t *testing.T) {
	srv := newServerForErrors(t)
	if code, body := serveInternal(t, srv, http.MethodDelete, "/api/v1/personal-data/%20", "", ""); code != http.StatusBadRequest {
		t.Errorf("a blank data subject was accepted: status=%d body=%s", code, body)
	}
	// And with no subject ever sealed, the listing is an empty array rather than null: a
	// client iterating the answer should not have to special-case "none yet".
	code, body := serveInternal(t, srv, http.MethodGet, "/api/v1/personal-data", "", "")
	if code != http.StatusOK || strings.TrimSpace(string(body)) != "[]" {
		t.Errorf("empty listing = status %d body %s, want 200 []", code, body)
	}
}

// TestEveryWriteEndpointFailsClosedWithoutASubject is the fail-closed property at each door
// rather than at one. A start that cannot resolve a data subject is refused (above); a
// worker's completion, a task's form and an operator's override have to be refused for the
// same reason, and each is a separate call site that could have been the one that forgot.
//
// The instance here is started without its data subject — which is allowed, because it
// carries no personal value yet — so every later attempt to write one has nobody to seal for.
func TestEveryWriteEndpointFailsClosedWithoutASubject(t *testing.T) {
	srv := newServerForErrors(t)
	_, jobKey, instKey := startPersonalInstance(t, srv, `{"variables":{"kontotyp":"A"}}`)

	for _, tc := range []struct{ name, path, body string }{
		{
			name: "operator override",
			path: fmt.Sprintf("/api/v1/instances/%d/variables", instKey),
			body: `{"variables":{"vorname":"Ida-Luise-Mustermann"}}`,
		},
		{
			name: "worker completion",
			path: fmt.Sprintf("/api/v1/jobs/%d/complete", jobKey),
			body: `{"reason":"test","variables":{"vorname":"Ida-Luise-Mustermann"}}`,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			code, body := serveInternal(t, srv, http.MethodPost, tc.path, tc.body, "application/json")
			if code != http.StatusBadRequest {
				t.Fatalf("status=%d body=%s, want 400", code, body)
			}
			if !strings.Contains(string(body), "personalnummer") {
				t.Errorf("the refusal does not name the variable to supply: %s", body)
			}
		})
	}
	// Nothing was written, which is the point: a refused seal must not leave the value
	// behind in the clear.
	var found bool
	srv.do(func() {
		_ = srv.store.VariablesOfScope(instKey, func(v *model.VariableValue) error {
			if v.Name == "vorname" {
				found = true
			}
			return nil
		})
	})
	if found {
		t.Error("a refused write left the personal value in the instance")
	}
}

// TestTheVariableAuditLabelsAnEncipheredOverride answers ADR-0314's own follow-up for the one
// view whose whole subject is that somebody changed a value by hand. It has to show *that*
// the value is personal and whose, not the envelope and not the plaintext: an audit trail is
// read to find out who acted, and spending a vault read per row to show a name nobody asked
// for would be the wrong trade in the one place the actor matters most.
func TestTheVariableAuditLabelsAnEncipheredOverride(t *testing.T) {
	srv := newServerForErrors(t)
	_, _, instKey := startPersonalInstance(t, srv, `{"variables":{"personalnummer":"P-4711"}}`)

	path := fmt.Sprintf("/api/v1/instances/%d/variables", instKey)
	if code, body := serveInternal(t, srv, http.MethodPost, path, `{"variables":{"vorname":"Ida-Luise-Mustermann"}}`, "application/json"); code != http.StatusOK {
		t.Fatalf("set variables: status=%d body=%s", code, body)
	}
	code, body := serveInternal(t, srv, http.MethodGet, fmt.Sprintf("/api/v1/instances/%d/variable-audit", instKey), "", "")
	if code != http.StatusOK {
		t.Fatalf("variable audit: status=%d body=%s", code, body)
	}
	var audit []struct {
		Name  string `json:"name"`
		Kind  string `json:"kind"`
		Value any    `json:"value"`
	}
	if err := json.Unmarshal(body, &audit); err != nil {
		t.Fatalf("decode: %v (%s)", err, body)
	}
	var row *struct {
		Name  string `json:"name"`
		Kind  string `json:"kind"`
		Value any    `json:"value"`
	}
	for i := range audit {
		if audit[i].Name == "vorname" {
			row = &audit[i]
		}
	}
	if row == nil {
		t.Fatalf("the override is not in the audit trail: %s", body)
	}
	if row.Kind != "personal" {
		t.Errorf("kind = %q, want personal", row.Kind)
	}
	text, _ := row.Value.(string)
	if !strings.Contains(text, "P-4711") || strings.Contains(text, "Ida-Luise-Mustermann") || strings.Contains(text, "atlas:personal") {
		t.Errorf("value = %#v, want a label naming the subject and neither the plaintext nor the envelope", row.Value)
	}
}

// TestASealThatFailsIsReportedNotSwallowed covers the edge's own failure. A vault whose data
// key has been corrupted — a restore beside a regenerated key file — must stop the write, not
// let it through in the clear: the value would be un-erasable and the model would say
// otherwise.
func TestASealThatFailsIsReportedNotSwallowed(t *testing.T) {
	srv := newServerForErrors(t)
	cp := mustCompilePersonal(t, srv)
	p := personalSealing{cp: cp, subject: "P-4711"}

	// A key that is not a key, left where a restore would leave one.
	srv.do(func() {
		if _, err := srv.vault.Set(vault.DataKeyName("P-4711"), "not-a-key"); err != nil {
			t.Errorf("Set: %v", err)
		}
	})
	vars := []model.VariableValue{{Name: "vorname", Kind: model.VarString, Text: "Ida-Luise-Mustermann"}}
	err := srv.seal(p, vars)
	if err == nil {
		t.Fatal("a broken data key let the write through")
	}
	if !strings.Contains(err.Error(), "32 bytes") {
		t.Errorf("the error does not name the problem: %v", err)
	}
	if vars[0].Text != "Ida-Luise-Mustermann" || vars[0].Kind != model.VarString {
		t.Errorf("the value was altered by a failed seal: %+v", vars[0])
	}
}

// userTaskPersonalBPMN parks on a *user task* rather than a service task, so the task-form
// door can be tested: completing a task is its own endpoint and its own call to the sealing
// edge, and a door that forgot to seal would look exactly like one that did.
const userTaskPersonalBPMN = `<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL"
                    xmlns:atlas="http://atlas/schema/1.0"
                    xmlns:zeebe="http://camunda.org/schema/zeebe/1.0">
  <process id="freigabe" isExecutable="true" atlas:personal="vorname" atlas:dataSubject="personalnummer">
    <startEvent id="start"/>
    <userTask id="freigeben">
      <extensionElements><zeebe:assignmentDefinition assignee="admin"/></extensionElements>
    </userTask>
    <endEvent id="end"/>
    <sequenceFlow id="f1" sourceRef="start" targetRef="freigeben"/>
    <sequenceFlow id="f2" sourceRef="freigeben" targetRef="end"/>
  </process>
</definitions>`

// TestTheTaskFormDoorSealsAndFailsClosed is that door: a form's submitted personal value is
// enciphered before it becomes a command, and a submission with no data subject to seal
// under is refused rather than stored readable.
func TestTheTaskFormDoorSealsAndFailsClosed(t *testing.T) {
	srv := newServerForErrors(t)
	code, body := serveInternal(t, srv, http.MethodPost, "/api/v1/deployments", userTaskPersonalBPMN, "application/xml")
	if code != http.StatusOK {
		t.Fatalf("deploy: status=%d body=%s", code, body)
	}
	var deploy struct {
		Key uint64 `json:"key"`
	}
	if err := json.Unmarshal(body, &deploy); err != nil {
		t.Fatalf("decode deploy: %v", err)
	}
	start := fmt.Sprintf("/api/v1/processes/%d/instances", deploy.Key)

	// First without a data subject: the form's answer cannot be sealed, so it is refused.
	if code, body = serveInternal(t, srv, http.MethodPost, start, "{}", "application/json"); code != http.StatusOK {
		t.Fatalf("create instance: status=%d body=%s", code, body)
	}
	taskKey, instKey := openUserTask(t, srv)
	complete := fmt.Sprintf("/api/v1/tasks/%d/complete", taskKey)
	code, body = serveInternal(t, srv, http.MethodPost, complete, `{"variables":{"vorname":"Ida-Luise-Mustermann"}}`, "application/json")
	if code != http.StatusBadRequest {
		t.Fatalf("a form answer with no data subject: status=%d body=%s, want 400", code, body)
	}
	if !strings.Contains(string(body), "personalnummer") {
		t.Errorf("the refusal does not name the variable to supply: %s", body)
	}

	// Then with one: the value is sealed on the way in.
	setVars := fmt.Sprintf("/api/v1/instances/%d/variables", instKey)
	if code, body = serveInternal(t, srv, http.MethodPost, setVars, `{"variables":{"personalnummer":"P-4711"}}`, "application/json"); code != http.StatusOK {
		t.Fatalf("set the data subject: status=%d body=%s", code, body)
	}
	if code, body = serveInternal(t, srv, http.MethodPost, complete, `{"variables":{"vorname":"Ida-Luise-Mustermann"}}`, "application/json"); code != http.StatusOK {
		t.Fatalf("complete the task: status=%d body=%s", code, body)
	}
	if stored := rootVar(t, srv, instKey, "vorname"); !vault.IsEnciphered(stored.Text) {
		t.Errorf("a form's personal answer landed in the clear: kind=%d text=%q", stored.Kind, stored.Text)
	}
}

// openUserTask returns the single open user task's job key and its instance.
func openUserTask(t *testing.T, srv *Server) (taskKey, instKey uint64) {
	t.Helper()
	srv.do(func() {
		_ = srv.store.ActiveElementInstances(func(_ uint64, v *model.ElementInstanceValue) error {
			if instKey == 0 {
				instKey = v.ProcessInstanceKey
			}
			return nil
		})
		_ = srv.store.ActivatableJobs(compiler.UserTaskJobTypeIndex, func(k uint64) error {
			taskKey = k
			return nil
		})
	})
	if taskKey == 0 || instKey == 0 {
		t.Fatalf("no open user task found (task=%d inst=%d)", taskKey, instKey)
	}
	return taskKey, instKey
}

// publicFormPersonalBPMN carries a start form, which is what a public link may be issued
// for — and a public form is the door a portal's personal data actually arrives through.
const publicFormPersonalBPMN = `<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL"
                    xmlns:atlas="http://atlas/schema/1.0"
                    xmlns:zeebe="http://camunda.org/schema/zeebe/1.0">
  <process id="bestellung" isExecutable="true" atlas:personal="vorname" atlas:dataSubject="personalnummer">
    <startEvent id="start">
      <extensionElements><zeebe:formDefinition formId="bestellformular"/></extensionElements>
    </startEvent>
    <serviceTask id="provision">
      <extensionElements><zeebe:taskDefinition type="provision" retries="5"/></extensionElements>
    </serviceTask>
    <endEvent id="end"/>
    <sequenceFlow id="f1" sourceRef="start" targetRef="provision"/>
    <sequenceFlow id="f2" sourceRef="provision" targetRef="end"/>
  </process>
</definitions>`

// TestThePublicFormDoorSealsAndFailsClosed is the door this mechanism is most likely to be
// used through, and the only in-edge whose definition is not known until a token is resolved
// — which is why it seals in a visit of its own before the start command. An unsealed public
// form would put personal data from the open internet into the log in the clear.
func TestThePublicFormDoorSealsAndFailsClosed(t *testing.T) {
	srv := newServerForErrors(t)
	code, body := serveInternal(t, srv, http.MethodPost, "/api/v1/deployments", publicFormPersonalBPMN, "application/xml")
	if code != http.StatusOK {
		t.Fatalf("deploy: status=%d body=%s", code, body)
	}
	code, body = serveInternal(t, srv, http.MethodPost, "/api/v1/public-links", `{"processId":"bestellung"}`, "application/json")
	if code != http.StatusOK {
		t.Fatalf("create public link: status=%d body=%s", code, body)
	}
	var link struct {
		Token string `json:"token"`
	}
	if err := json.Unmarshal(body, &link); err != nil || link.Token == "" {
		t.Fatalf("decode link: %v (%s)", err, body)
	}
	start := "/public/forms/" + link.Token + "/start"

	// Without a data subject the submission is refused, rather than stored readable.
	code, body = serveInternal(t, srv, http.MethodPost, start, `{"variables":{"vorname":"Ida-Luise-Mustermann"}}`, "application/json")
	if code != http.StatusBadRequest {
		t.Fatalf("a public submission with no data subject: status=%d body=%s, want 400", code, body)
	}
	if !strings.Contains(string(body), "personalnummer") {
		t.Errorf("the refusal does not name the variable to supply: %s", body)
	}

	// With one it starts, and the value is an envelope before it ever becomes a command.
	code, body = serveInternal(t, srv, http.MethodPost, start,
		`{"variables":{"personalnummer":"P-4711","vorname":"Ida-Luise-Mustermann"}}`, "application/json")
	if code != http.StatusOK {
		t.Fatalf("public start: status=%d body=%s", code, body)
	}
	instKey := mustInstanceOf(t, srv)
	stored := rootVar(t, srv, instKey, "vorname")
	if !vault.IsEnciphered(stored.Text) {
		t.Fatalf("a public form's personal value landed in the clear: kind=%d text=%q", stored.Kind, stored.Text)
	}
	// The name is twenty characters for a measured reason, recorded at
	// vault.TestTheStoredTextHoldsNoPlaintext: this search runs over base64, where a
	// three-letter needle collides by chance once in 5 882 seals.
	if strings.Contains(stored.Text, "Ida-Luise-Mustermann") {
		t.Errorf("the stored value carries the plaintext: %s", stored.Text)
	}
}

// TestAJobIsWithheldWhenItsValuesCannotBeOpened is the consequence ADR-0314 accepts, at the
// level it happens: the payload assembly cannot open a value whose subject has been erased, so
// the job is not handed to a worker at all. Handing it over with a blank or a ciphertext name
// would have the worker provision an account for nobody.
func TestAJobIsWithheldWhenItsValuesCannotBeOpened(t *testing.T) {
	srv := newServerForErrors(t)
	_, jobKey, _ := startPersonalInstance(t, srv, `{"variables":{"personalnummer":"P-4711","vorname":"Ida-Luise-Mustermann"}}`)

	var before, after bool
	srv.do(func() { _, before = srv.pulledJob(jobKey, "provision") })
	if !before {
		t.Fatal("the job could not be read even before the erasure")
	}
	if code, body := serveInternal(t, srv, http.MethodDelete, "/api/v1/personal-data/P-4711", "", ""); code != http.StatusOK {
		t.Fatalf("erase: status=%d body=%s", code, body)
	}
	srv.do(func() { _, after = srv.pulledJob(jobKey, "provision") })
	if after {
		t.Error("the job is still handed out after its subject was erased, which would send a worker an unreadable name")
	}
}
