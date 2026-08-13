package api_test

import (
	"net/http"
	"strings"
	"testing"
)

// TestLayoutEndpointRegenerates drives the Modeler's Auto-layout button over HTTP:
// a semantic-only model comes back with a generated diagram, and a model that
// already carries a (stale) diagram comes back with a single, regenerated one.
func TestLayoutEndpointRegenerates(t *testing.T) {
	ts := newTestServer(t)

	// Semantic-only model: a diagram is generated from nothing.
	code, body := doReq(t, ts, http.MethodPost, "/api/v1/layout", sampleBPMN, "application/xml")
	if code != http.StatusOK {
		t.Fatalf("layout status = %d, body = %s", code, body)
	}
	out := string(body)
	for _, want := range []string{
		"<bpmndi:BPMNDiagram",
		`bpmnElement="order"`,
		`<bpmndi:BPMNShape id="start_di" bpmnElement="start">`,
		`<bpmndi:BPMNEdge id="f1_di" bpmnElement="f1">`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("generated layout missing %q\n---\n%s", want, out)
		}
	}

	// Feeding the result back in must not stack a second diagram: the old one is
	// discarded and a fresh single diagram returned.
	code, body2 := doReq(t, ts, http.MethodPost, "/api/v1/layout", out, "application/xml")
	if code != http.StatusOK {
		t.Fatalf("second layout status = %d, body = %s", code, body2)
	}
	if n := strings.Count(string(body2), "<bpmndi:BPMNDiagram"); n != 1 {
		t.Fatalf("expected exactly one diagram after re-layout, got %d:\n%s", n, body2)
	}
}

// TestLayoutEndpointEmptyBody rejects a missing body with 400, matching validate
// and deploy.
func TestLayoutEndpointEmptyBody(t *testing.T) {
	ts := newTestServer(t)
	code, _ := doReq(t, ts, http.MethodPost, "/api/v1/layout", "", "application/xml")
	if code != http.StatusBadRequest {
		t.Fatalf("empty body status = %d, want 400", code)
	}
}

// TestLayoutEndpointBestEffort returns non-BPMN input unchanged rather than
// erroring, so the button degrades gracefully on a model it can't lay out.
func TestLayoutEndpointBestEffort(t *testing.T) {
	ts := newTestServer(t)
	const junk = `<html><body>not bpmn</body></html>`
	code, body := doReq(t, ts, http.MethodPost, "/api/v1/layout", junk, "application/xml")
	if code != http.StatusOK {
		t.Fatalf("layout status = %d, body = %s", code, body)
	}
	if string(body) != junk {
		t.Fatalf("non-BPMN input should be returned unchanged, got %q", body)
	}
}
