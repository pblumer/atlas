package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/pblumer/atlas/api/httpapi"
	"github.com/pblumer/atlas/api/order"
)

// Where one position stands (ADR-0413), for the shapes the HTTP tests do not
// draw: a step with no name, a multi-instance step, a process carrying the order
// id as something other than text, and an order record that cannot be read.

// portalProgressPathsBPMN parks on an unnamed user task and, in parallel, on a
// named multi-instance task of two iterations.
const portalProgressPathsBPMN = `<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL">
  <process id="progress-shapes" isExecutable="true">
    <startEvent id="s"/>
    <parallelGateway id="fork"/>
    <userTask id="anonymous"/>
    <userTask id="review" name="Review the request">
      <multiInstanceLoopCharacteristics isSequential="false">
        <loopCardinality>2</loopCardinality>
      </multiInstanceLoopCharacteristics>
    </userTask>
    <endEvent id="e1"/><endEvent id="e2"/>
    <sequenceFlow id="f0" sourceRef="s" targetRef="fork"/>
    <sequenceFlow id="f1" sourceRef="fork" targetRef="anonymous"/>
    <sequenceFlow id="f2" sourceRef="fork" targetRef="review"/>
    <sequenceFlow id="f3" sourceRef="anonymous" targetRef="e1"/>
    <sequenceFlow id="f4" sourceRef="review" targetRef="e2"/>
  </process>
</definitions>`

// portalProgressPathsAsk calls the route as the orderer.
func portalProgressPathsAsk(s *Server, orderID, position string) (int, string) {
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.SetPathValue("id", orderID)
	req.SetPathValue("position", position)
	req = req.WithContext(httpapi.WithPrincipal(req.Context(), &httpapi.Principal{UserID: "usr_ada"}))
	rec := httptest.NewRecorder()
	s.handleLineProgress(rec, req)
	return rec.Code, rec.Body.String()
}

// TestPortalProgressStepsAreWhereTheWorkIs. An unnamed step is answered by its
// id rather than dropped, and a multi-instance task running two iterations is one
// step — the body seeding them is no step at all. A second read answers the same
// from the parsed names it kept.
func TestPortalProgressStepsAreWhereTheWorkIs(t *testing.T) {
	srv := newServerForErrors(t)
	code, body := recertifyHTTPPathsCall(t, srv.Handler(), http.MethodPost, "/api/v1/deployments",
		strings.NewReader(portalProgressPathsBPMN))
	if code != http.StatusOK {
		t.Fatalf("deploy: %d %s", code, body)
	}
	orderCancelPathsSave(t, srv, order.Order{ID: "ord_p1", Orderer: "usr_ada", Recipient: "usr_ada",
		Lines: []order.Line{{ItemID: "vpn", Status: order.StatusRunning}}})
	// One instance carries the order id as a number, which is not the order's id
	// as either writer writes it, and must not be taken for the position's process.
	pendingWorkPathsStart(t, srv, "progress-shapes", `{"orderId":1,"positionId":"vpn"}`)
	pendingWorkPathsStart(t, srv, "progress-shapes", `{"orderId":"ord_p1","positionId":"vpn"}`)

	for i := 0; i < 2; i++ {
		code, body := portalProgressPathsAsk(srv, "ord_p1", "vpn")
		if code != http.StatusOK {
			t.Fatalf("progress = %d %s", code, body)
		}
		var got positionProgress
		if err := json.Unmarshal([]byte(body), &got); err != nil {
			t.Fatalf("decode: %v", err)
		}
		if got.State != "active" || strings.Join(got.Steps, "|") != "Review the request|anonymous" {
			t.Errorf("read %d: progress = %+v, want active on the named review once and the unnamed task by id",
				i+1, got)
		}
	}
}

// TestPortalProgressAnUnreadableOrderIsAFault. Ownership is decided from the
// order, so an order that cannot be read cannot be answered either way — and is
// not answered "no order".
func TestPortalProgressAnUnreadableOrderIsAFault(t *testing.T) {
	srv := newServerForErrors(t)
	orderCancelPathsSave(t, srv, orderCancelPathsPending("ord_p2"))
	orderCancelPathsCorrupt(t, srv, "ord_p2")

	if code, body := portalProgressPathsAsk(srv, "ord_p2", "vpn"); code != http.StatusInternalServerError ||
		!strings.Contains(body, "read order") {
		t.Errorf("progress = %d %s, want 500", code, body)
	}
}

// TestPortalProgressADocumentNotHeldHasNoNames. Without the deployed document the
// steps fall back to their ids; the name map is empty rather than nil, so a
// lookup in it is safe.
func TestPortalProgressADocumentNotHeldHasNoNames(t *testing.T) {
	if names := elementNamesIn(nil); names == nil || len(names) != 0 {
		t.Errorf("names = %#v, want an empty map", names)
	}
}
