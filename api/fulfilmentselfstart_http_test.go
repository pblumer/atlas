package api_test

import (
	"net/http"
	"strings"
	"testing"

	"github.com/pblumer/atlas/api"
)

// The orchestration that works an order is never started for one of its own
// positions. A position bound to it in a frozen order made every orchestration
// start another for that position, and each new one did the same: hundreds of
// orchestrations and their provisioning tasks a minute, with nothing failing. The
// start is refused, so the orchestration's start task fails where somebody can
// see it.
func TestTheOrchestrationIsNotStartedForAPosition(t *testing.T) {
	ts, _ := newAuthServerWith(t, "root", "rootpassword", api.WithSystemProcesses())
	admin := newClient(t)
	if login(t, admin, ts, "root", "rootpassword") != http.StatusOK {
		t.Fatal("admin login failed")
	}

	code, body := cReq(t, admin, ts, "POST", "/api/v1/instances",
		`{"processId":"atlas-auftrag-erfuellung","variables":{"orderId":"ord_x","positionId":"tastatur"}}`)
	if code != http.StatusUnprocessableEntity {
		t.Fatalf("starting the orchestration for a position: %d (%s), want 422", code, body)
	}
	if !strings.Contains(string(body), "without end") {
		t.Errorf("the refusal does not say why: %s", body)
	}

	// Nothing was started.
	code, body = cReq(t, admin, ts, "GET", "/api/v1/instances", "")
	if code != http.StatusOK {
		t.Fatalf("list instances: %d (%s)", code, body)
	}
	if strings.Contains(string(body), `"atlas-auftrag-erfuellung"`) {
		t.Errorf("an orchestration was started anyway: %s", body)
	}
}
