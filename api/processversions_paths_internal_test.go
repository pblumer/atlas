package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"testing"
)

// TestProcessVersionsSaysWhenItsIncidentCountsAreNotExact: the version picker reads its
// incident counts from the same whole snapshot of the incident family the live diagram
// does. When that reading cannot be taken, every version still answers its running and
// finished counts — they are counters, and readable — but no version claims a zero it
// has not earned: the incidents are all left at zero and the answer says they are not
// exact, so a reader does not take "0 incidents" for "nothing is stuck".
func TestProcessVersionsSaysWhenItsIncidentCountsAreNotExact(t *testing.T) {
	srv := newServerForErrors(t)
	x := deployTestHarness{t, srv.Handler()}
	def := handlersDeployAt(t, x, "/api/v1/deployments", handlersWaitBPMN)
	inst := handlersStartAt(t, x, def)
	token := handlersTokenOf(t, srv, inst)
	handlersInject(t, srv, func() error { return srv.store.InjectCorruptIncident(token) })

	code, body := x.do(http.MethodGet, fmt.Sprintf("/api/v1/processes/%d/versions", def), "")
	if code != http.StatusOK {
		t.Fatalf("versions: status=%d body=%s", code, body)
	}
	var got processVersionsResp
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatalf("decode: %v (%s)", err, body)
	}
	if got.IncidentCountsExact {
		t.Errorf("incident counts claimed exact from a reading that failed: %s", body)
	}
	if len(got.Versions) != 1 {
		t.Fatalf("versions = %+v, want the one deployed", got.Versions)
	}
	if v := got.Versions[0]; v.Key != def || v.Running != 1 || v.Incidents != 0 {
		t.Errorf("version = %+v, want key %d, the counters' 1 running and no incident claimed", v, def)
	}
}
