package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"testing"

	"github.com/pblumer/atlas/model"
	"github.com/pblumer/atlas/state"
)

// scanStats is what readStats used to be: the authoritative counts, walked out of the
// runtime families. It is kept here, in the test, as the thing the cheap counters are
// held against — O(active instances + tokens) is fine once per assertion and ruinous
// on a request path.
func scanStats(rv *state.ReadView) (statsResp, error) {
	pi, err := rv.ActiveProcessInstanceCount()
	if err != nil {
		return statsResp{}, err
	}
	ei, err := rv.ActiveElementInstanceCount()
	if err != nil {
		return statsResp{}, err
	}
	inc, err := rv.IncidentCount()
	if err != nil {
		return statsResp{}, err
	}
	return statsResp{ActiveProcessInstances: pi, ActiveElementInstances: ei, UnresolvedIncidents: inc}, nil
}

// TestStatsReadFromCountersAgreeWithTheScan is the licence for reading the runtime
// counts off the maintained ADR-0080 counters instead of scanning for them.
//
// The scan is authoritative, so the counters may replace it only where they are the
// same number — and "the same number" has to hold across the transitions that move it,
// not just on a fresh store. So the workload here starts instances, finishes one, and
// cancels another, checking after every step: an increment that is not matched by its
// decrement shows up as a drift the moment a token leaves.
func TestStatsReadFromCountersAgreeWithTheScan(t *testing.T) {
	srv := newServerForErrors(t)

	check := func(step string) {
		t.Helper()
		var counted, scanned statsResp
		if err := srv.readOffLoop(func(rv *state.ReadView, _ defIndex) error {
			var err error
			if counted, err = readStats(rv); err != nil {
				return err
			}
			scanned, err = scanStats(rv)
			return err
		}); err != nil {
			t.Fatalf("%s: readOffLoop: %v", step, err)
		}
		if counted != scanned {
			t.Errorf("%s: counted %+v, scanned %+v — the cheap read must be the same number",
				step, counted, scanned)
		}
	}

	check("fresh store")

	code, body := serveInternal(t, srv, http.MethodPost, "/api/v1/deployments", serviceJobBPMN, "application/xml")
	if code != http.StatusOK {
		t.Fatalf("deploy: status=%d body=%s", code, body)
	}
	check("after deploy")

	for i := range 3 {
		if code, body := serveInternal(t, srv, http.MethodPost, "/api/v1/processes/1/instances", "{}", "application/json"); code != http.StatusOK {
			t.Fatalf("create %d: status=%d body=%s", i, code, body)
		}
		check(fmt.Sprintf("after start %d", i+1))
	}

	// Finishing an instance retires its process-instance record and its last token, so
	// both counters have to come back down together.
	jobKey, _ := parkedServiceJob(t, srv, 1)
	if code, body := serveInternal(t, srv, http.MethodPost,
		fmt.Sprintf("/api/v1/jobs/%d/complete", jobKey), `{"variables":{},"reason":"stats counter equivalence test"}`, "application/json"); code != http.StatusOK {
		t.Fatalf("complete job: status=%d body=%s", code, body)
	}
	check("after one instance completed")

	// Cancelling is the other way a token leaves, and it is the one a maintained
	// counter is most likely to miss.
	insts := activeInstanceKeys(t, srv)
	if len(insts) == 0 {
		t.Fatal("no active instance left to cancel")
	}
	if code, body := serveInternal(t, srv, http.MethodDelete,
		fmt.Sprintf("/api/v1/instances/%d", insts[0]), "", ""); code != http.StatusOK {
		t.Fatalf("cancel: status=%d body=%s", code, body)
	}
	check("after one instance cancelled")

	// And the endpoint itself reports what the scan would.
	code, body = serveInternal(t, srv, http.MethodGet, "/api/v1/stats", "", "")
	if code != http.StatusOK {
		t.Fatalf("stats: status=%d body=%s", code, body)
	}
	var got statsResp
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatalf("decode stats: %v (%s)", err, body)
	}
	var want statsResp
	if err := srv.readOffLoop(func(rv *state.ReadView, _ defIndex) error {
		var err error
		want, err = scanStats(rv)
		return err
	}); err != nil {
		t.Fatalf("readOffLoop: %v", err)
	}
	if got != want {
		t.Errorf("GET /api/v1/stats = %+v, want the authoritative %+v", got, want)
	}
}

// activeInstanceKeys lists the live process instances, on the run loop.
func activeInstanceKeys(t *testing.T, srv *Server) []uint64 {
	t.Helper()
	var keys []uint64
	srv.do(func() {
		_ = srv.store.ActiveProcessInstances(func(k uint64, _ *model.ProcessInstanceValue) error {
			keys = append(keys, k)
			return nil
		})
	})
	return keys
}
