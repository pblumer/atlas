package api_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/pblumer/atlas/api"
)

// validateProblem mirrors one compiler.Problem row in the /validate response.
type validateProblem struct {
	Element  string `json:"element"`
	Severity string `json:"severity"`
	Rule     string `json:"rule"`
	Message  string `json:"message"`
}

type validateResult struct {
	Valid    bool              `json:"valid"`
	Version  string            `json:"version"`
	Problems []validateProblem `json:"problems"`
}

// postValidate posts a BPMN body to /api/v1/validate and decodes the result,
// asserting the request itself succeeded (200) — an invalid model is a valid
// response, not an HTTP error.
func postValidate(t *testing.T, ts *httptest.Server, body string) validateResult {
	t.Helper()
	code, raw := doReq(t, ts, http.MethodPost, "/api/v1/validate", body, "application/xml")
	if code != http.StatusOK {
		t.Fatalf("POST /validate = %d, want 200; body: %s", code, raw)
	}
	var res validateResult
	if err := json.Unmarshal(raw, &res); err != nil {
		t.Fatalf("decode validate response: %v; body: %s", err, raw)
	}
	return res
}

// TestValidateCleanModel: a well-formed model validates clean — no problems, valid,
// and tagged with the engine version — without deploying anything.
func TestValidateCleanModel(t *testing.T) {
	ts := newTestServer(t)
	res := postValidate(t, ts, sampleBPMN)
	if !res.Valid {
		t.Errorf("clean model valid = false, problems: %+v", res.Problems)
	}
	if len(res.Problems) != 0 {
		t.Errorf("clean model problems = %+v, want none", res.Problems)
	}
	if res.Version != api.Version {
		t.Errorf("version = %q, want %q", res.Version, api.Version)
	}
	// The dry run must not register anything.
	if code, body := doReq(t, ts, http.MethodGet, "/api/v1/processes", "", ""); code != http.StatusOK || string(body) != "[]\n" {
		t.Errorf("validate must not deploy; processes = %d %s", code, body)
	}
}

// TestValidateReachabilityWarning: unreachable dead code is a non-blocking warning
// — the model is still valid (deployable) but the finding is surfaced, anchored to
// the element.
func TestValidateReachabilityWarning(t *testing.T) {
	const xml = `<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL">
	  <process id="p" isExecutable="true">
	    <startEvent id="s"/>
	    <task id="live"/>
	    <task id="orphan"/>
	    <endEvent id="e"/>
	    <sequenceFlow id="f1" sourceRef="s" targetRef="live"/>
	    <sequenceFlow id="f2" sourceRef="live" targetRef="e"/>
	  </process></definitions>`
	res := postValidate(t, newTestServer(t), xml)
	if !res.Valid {
		t.Errorf("a warning must leave the model valid, got %+v", res.Problems)
	}
	if !containsProblem(res.Problems, "reachability", "warning", "orphan") {
		t.Errorf("want a reachability warning on \"orphan\", got %+v", res.Problems)
	}
}

// TestValidateGraphError: a graph-wide fault (an unroutable gateway) is an
// error-severity, element-anchored problem, and marks the model invalid — the same
// finding a deploy would reject, now surfaced non-destructively.
func TestValidateGraphError(t *testing.T) {
	const xml = `<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL">
	  <process id="p" isExecutable="true">
	    <startEvent id="s"/>
	    <exclusiveGateway id="gw"/>
	    <sequenceFlow id="f0" sourceRef="s" targetRef="gw"/>
	  </process></definitions>`
	res := postValidate(t, newTestServer(t), xml)
	if res.Valid {
		t.Error("an unroutable gateway must be invalid")
	}
	if !containsProblem(res.Problems, "gateway.no-outgoing", "error", "gw") {
		t.Errorf("want a gateway.no-outgoing error on \"gw\", got %+v", res.Problems)
	}
}

// TestValidateCompileError: a stage-1–4 fault the compiler reports as an opaque
// error (here malformed XML) is surfaced as one generic "compile" problem, invalid.
func TestValidateCompileError(t *testing.T) {
	res := postValidate(t, newTestServer(t), `<definitions><process`)
	if res.Valid {
		t.Error("malformed XML must be invalid")
	}
	if !containsProblem(res.Problems, "compile", "error", "") {
		t.Errorf("want a compile error problem, got %+v", res.Problems)
	}
}

// TestValidateEmptyBody: a malformed request (empty body) is a 4xx — distinct from
// a well-formed request carrying an invalid model.
func TestValidateEmptyBody(t *testing.T) {
	if code, _ := doReq(t, newTestServer(t), http.MethodPost, "/api/v1/validate", "", "application/xml"); code != http.StatusBadRequest {
		t.Errorf("empty body = %d, want 400", code)
	}
}

// containsProblem reports whether ps has a problem matching rule/severity (and, if
// element != "", that element).
func containsProblem(ps []validateProblem, rule, severity, element string) bool {
	for _, p := range ps {
		if p.Rule == rule && p.Severity == severity && (element == "" || p.Element == element) {
			return true
		}
	}
	return false
}
