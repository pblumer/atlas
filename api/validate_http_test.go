package api_test

import (
	"encoding/json"
	"net/http"
	"testing"
)

// validateProblem mirrors the compiler.Problem shape the endpoint serializes, so
// the test can assert on findings without importing the compiler package.
type validateProblem struct {
	Element  string `json:"element"`
	Severity string `json:"severity"`
	Rule     string `json:"rule"`
	Message  string `json:"message"`
}

type validateResult struct {
	Version  string            `json:"version"`
	Problems []validateProblem `json:"problems"`
}

// TestValidateEndpointCleanModel: a well-formed model validates with no problems
// and reports the engine version, so the panel shows a green, version-attributed
// result.
func TestValidateEndpointCleanModel(t *testing.T) {
	ts := newTestServer(t)
	const xml = `<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL">
	  <process id="p" isExecutable="true">
	    <startEvent id="s"/>
	    <task id="t"/>
	    <endEvent id="e"/>
	    <sequenceFlow id="f1" sourceRef="s" targetRef="t"/>
	    <sequenceFlow id="f2" sourceRef="t" targetRef="e"/>
	  </process></definitions>`
	code, body := doReq(t, ts, http.MethodPost, "/api/v1/validate", xml, "application/xml")
	if code != http.StatusOK {
		t.Fatalf("status=%d body=%s", code, body)
	}
	var res validateResult
	if err := json.Unmarshal(body, &res); err != nil {
		t.Fatalf("unmarshal %s: %v", body, err)
	}
	if len(res.Problems) != 0 {
		t.Errorf("clean model produced problems: %+v", res.Problems)
	}
	if res.Version == "" {
		t.Errorf("response must carry the engine version")
	}
}

// TestValidateEndpointDeadCodeWarning is the reported scenario: a disconnected end
// event. The endpoint returns a reachability warning (not an error), which is
// exactly what makes the panel able to explain the inert "Ende" symbol.
func TestValidateEndpointDeadCodeWarning(t *testing.T) {
	ts := newTestServer(t)
	const xml = `<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL">
	  <process id="p" isExecutable="true">
	    <startEvent id="s"/>
	    <task id="live"/>
	    <endEvent id="Ende"/>
	    <sequenceFlow id="f1" sourceRef="s" targetRef="live"/>
	  </process></definitions>`
	code, body := doReq(t, ts, http.MethodPost, "/api/v1/validate", xml, "application/xml")
	if code != http.StatusOK {
		t.Fatalf("status=%d body=%s", code, body)
	}
	var res validateResult
	if err := json.Unmarshal(body, &res); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	var found bool
	for _, p := range res.Problems {
		if p.Element == "Ende" && p.Severity == "warning" && p.Rule == "reachability" {
			found = true
		}
		if p.Severity == "error" {
			t.Errorf("a disconnected end event must not be an error: %+v", p)
		}
	}
	if !found {
		t.Errorf("want a reachability warning on \"Ende\", got %+v", res.Problems)
	}
}

// TestValidateEndpointGraphError: a structural error (a gateway with no outgoing
// flow) is returned as an error-severity problem — the dry run reports it without
// deploying anything.
func TestValidateEndpointGraphError(t *testing.T) {
	ts := newTestServer(t)
	const xml = `<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL">
	  <process id="p" isExecutable="true">
	    <startEvent id="s"/>
	    <exclusiveGateway id="gw"/>
	    <sequenceFlow id="f1" sourceRef="s" targetRef="gw"/>
	  </process></definitions>`
	code, body := doReq(t, ts, http.MethodPost, "/api/v1/validate", xml, "application/xml")
	if code != http.StatusOK {
		t.Fatalf("status=%d body=%s", code, body)
	}
	var res validateResult
	if err := json.Unmarshal(body, &res); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	var found bool
	for _, p := range res.Problems {
		if p.Element == "gw" && p.Severity == "error" && p.Rule == "gateway.no-outgoing" {
			found = true
		}
	}
	if !found {
		t.Errorf("want a no-outgoing error on gw, got %+v", res.Problems)
	}
}

// TestValidateEndpointMalformedXML: an unparseable document comes back as a 200
// with a parse-severity problem, not an HTTP error — the panel renders it like
// any other finding rather than showing a broken request.
func TestValidateEndpointMalformedXML(t *testing.T) {
	ts := newTestServer(t)
	code, body := doReq(t, ts, http.MethodPost, "/api/v1/validate", "<definitions><process", "application/xml")
	if code != http.StatusOK {
		t.Fatalf("status=%d body=%s", code, body)
	}
	var res validateResult
	if err := json.Unmarshal(body, &res); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	var found bool
	for _, p := range res.Problems {
		if p.Severity == "error" && p.Rule == "parse" {
			found = true
		}
	}
	if !found {
		t.Errorf("want a parse error, got %+v", res.Problems)
	}
}

// TestValidateEndpointEmptyBody rejects an empty request as a client error, like
// the deploy endpoint.
func TestValidateEndpointEmptyBody(t *testing.T) {
	ts := newTestServer(t)
	code, _ := doReq(t, ts, http.MethodPost, "/api/v1/validate", "", "application/xml")
	if code != http.StatusBadRequest {
		t.Fatalf("status=%d, want 400", code)
	}
}
