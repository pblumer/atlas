package api

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"testing"
)

// leaseIdem leases the one waiting job of jobType and returns its key and idempotency key.
func leaseIdem(t *testing.T, srv *Server, jobType string) (uint64, uint64, string) {
	t.Helper()
	code, raw := serveInternal(t, srv, http.MethodPost, "/api/v1/jobs/activate",
		`{"type":"`+jobType+`","worker":"w1","maxJobs":1}`, "application/json")
	if code != http.StatusOK {
		t.Fatalf("activate: %d %s", code, raw)
	}
	var out struct {
		Jobs []struct {
			JobKey         uint64 `json:"jobKey"`
			LeaseToken     uint64 `json:"leaseToken"`
			IdempotencyKey string `json:"idempotencyKey"`
		} `json:"jobs"`
	}
	if err := json.Unmarshal(raw, &out); err != nil || len(out.Jobs) != 1 {
		t.Fatalf("activate answered %s (%v), want one job", raw, err)
	}
	j := out.Jobs[0]
	return j.JobKey, j.LeaseToken, j.IdempotencyKey
}

// Every delivery of one task execution carries the same idempotency key — after a
// failure with a retry left, and after an operator resolves the incident the last
// failure raised — and the key names this installation and the job
// (ADR-draft-operations-declare-idempotency-and-effect §2).
func TestTheIdempotencyKeyIsStableAcrossEveryDeliveryOfOneExecution(t *testing.T) {
	srv, _ := jobPullSrvClockedWith(t, errorBoundaryBPMN("orders", "charge-card", "CARD_DECLINED"), `{}`)
	jobKey, lease, first := leaseIdem(t, srv, "charge-card")
	if want := "atlas:" + srv.nodeID + ":" + strconv.FormatUint(jobKey, 10); first != want || srv.nodeID == "" {
		t.Fatalf("idempotencyKey = %q, want %q", first, want)
	}
	fail := func(lease uint64, retries int) {
		t.Helper()
		body := `{"worker":"w1","leaseToken":` + strconv.FormatUint(lease, 10) + `,"retries":` + strconv.Itoa(retries) + `,"message":"down"}`
		if code, raw := serveInternal(t, srv, http.MethodPost, "/api/v1/jobs/"+strconv.FormatUint(jobKey, 10)+"/fail", body, "application/json"); code != http.StatusOK {
			t.Fatalf("fail: %d %s", code, raw)
		}
	}

	fail(lease, 1)
	again, lease2, second := leaseIdem(t, srv, "charge-card")
	if again != jobKey || second != first {
		t.Errorf("after a retry: job %d key %q, want job %d key %q", again, second, jobKey, first)
	}

	fail(lease2, 0)
	incidents := openIncidents(t, srv)
	if len(incidents) != 1 {
		t.Fatalf("incidents = %+v, want the one the last failure raised", incidents)
	}
	if code, raw := serveInternal(t, srv, http.MethodPost, "/api/v1/incidents/"+strconv.FormatUint(incidents[0].ElementInstanceKey, 10)+"/resolve", `{"retries":1}`, "application/json"); code != http.StatusOK {
		t.Fatalf("resolve: %d %s", code, raw)
	}
	resumed, _, third := leaseIdem(t, srv, "charge-card")
	if resumed != jobKey || third != first {
		t.Errorf("after resolving the incident: job %d key %q, want job %d key %q", resumed, third, jobKey, first)
	}
	if !strings.HasPrefix(first, "atlas:") {
		t.Errorf("idempotencyKey %q does not name atlas", first)
	}
}
