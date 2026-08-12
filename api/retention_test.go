package api_test

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync/atomic"
	"testing"
	"time"

	"github.com/pblumer/atlas/api"
	"github.com/pblumer/atlas/opensearch"
)

// instanceRow is the subset of the instances listing a retention test inspects.
type instanceRow struct {
	Key   uint64 `json:"key"`
	State string `json:"state"`
}

func listInstances(t *testing.T, ts *httptest.Server) []instanceRow {
	t.Helper()
	code, body := doReq(t, ts, http.MethodGet, "/api/v1/instances", "", "")
	if code != http.StatusOK {
		t.Fatalf("list instances status=%d body=%s", code, body)
	}
	var rows []instanceRow
	if err := json.Unmarshal(body, &rows); err != nil {
		t.Fatalf("decode instances: %v (%s)", err, body)
	}
	return rows
}

// deployAndTerminateOne deploys the sample process, starts one instance, and
// terminates it so it lands in history (PITerminated). Returns the instance key.
func deployAndTerminateOne(t *testing.T, ts *httptest.Server) uint64 {
	t.Helper()
	if code, body := doReq(t, ts, http.MethodPost, "/api/v1/deployments", sampleBPMN, "application/xml"); code != http.StatusOK {
		t.Fatalf("deploy status=%d body=%s", code, body)
	}
	if code, body := doReq(t, ts, http.MethodPost, "/api/v1/processes/1/instances", "{}", "application/json"); code != http.StatusOK {
		t.Fatalf("create status=%d body=%s", code, body)
	}
	rows := listInstances(t, ts)
	if len(rows) != 1 {
		t.Fatalf("want 1 instance, got %d", len(rows))
	}
	key := rows[0].Key
	if code, body := doReq(t, ts, http.MethodDelete, "/api/v1/instances/"+strconv.FormatUint(key, 10), "", ""); code != http.StatusOK {
		t.Fatalf("terminate status=%d body=%s", code, body)
	}
	return key
}

func waitForPurge(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(15 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

// TestRetentionPurgesFinishedInstance: with retention on and no exporter, a
// terminated instance older than the max age is hard-deleted from history — it
// disappears from the instances listing (ADR-0115). The safe position is the durable
// applied position, so a just-terminated instance is immediately export-provable.
func TestRetentionPurgesFinishedInstance(t *testing.T) {
	// The max age is deliberately larger than the sweep interval and the setup time,
	// not a token 1ms: eligibility is wall-clock (now-CompletedAt >= maxAge), so a tiny
	// age lets a sweep tick purge the instance *during* setup — before the pre-purge
	// assertion below — which raced under load. A 500ms age keeps the instance
	// ineligible until well after the assertion, then it ages out and is purged inside
	// waitForPurge's 3s window.
	ts := newTestServerWith(t,
		api.WithRetention(500*time.Millisecond),
		api.WithRetentionInterval(15*time.Millisecond),
	)
	deployAndTerminateOne(t, ts)

	// It is in history now (terminated); retention then purges it.
	if rows := listInstances(t, ts); len(rows) != 1 || rows[0].State != "terminated" {
		t.Fatalf("want one terminated instance before purge, got %v", rows)
	}
	waitForPurge(t, "the terminated instance to be purged", func() bool {
		return len(listInstances(t, ts)) == 0
	})
}

// TestRetentionDrainsBacklogAcrossTicks: with a per-tick batch of 1, several finished
// instances are purged one per tick — exercising the bounded, resumable cursor
// (ADR-0115). All are eventually cleared.
func TestRetentionDrainsBacklogAcrossTicks(t *testing.T) {
	// A 500ms max age (not a token 1ms) keeps all three instances ineligible until
	// after the "3 present before purge" assertion below: eligibility is wall-clock
	// (now-CompletedAt >= maxAge), so a 1ms age let a 15ms sweep tick purge one during
	// setup and the assertion then saw 2 — the flake this hardening fixes. Once they
	// age past the cutoff they still drain one-per-tick (batch 1), and the whole
	// backlog clears inside waitForPurge's 3s window.
	ts := newTestServerWith(t,
		api.WithRetention(500*time.Millisecond),
		api.WithRetentionInterval(15*time.Millisecond),
		api.WithRetentionBatch(1),
	)
	if code, body := doReq(t, ts, http.MethodPost, "/api/v1/deployments", sampleBPMN, "application/xml"); code != http.StatusOK {
		t.Fatalf("deploy status=%d body=%s", code, body)
	}
	for i := 0; i < 3; i++ {
		if code, body := doReq(t, ts, http.MethodPost, "/api/v1/processes/1/instances", "{}", "application/json"); code != http.StatusOK {
			t.Fatalf("create %d status=%d body=%s", i, code, body)
		}
	}
	// Terminate them all → three terminated instances in history.
	if code, body := doReq(t, ts, http.MethodPost, "/api/v1/processes/1/cancel-instances", "{}", "application/json"); code != http.StatusOK {
		t.Fatalf("cancel-instances status=%d body=%s", code, body)
	}
	if n := len(listInstances(t, ts)); n != 3 {
		t.Fatalf("want 3 terminated instances before purge, got %d", n)
	}
	waitForPurge(t, "the whole backlog to drain one per tick", func() bool {
		return len(listInstances(t, ts)) == 0
	})
}

// gateStub is an OpenSearch _bulk endpoint that fails while paused, so the exporter's
// high-water mark cannot advance — letting a test hold the retention export gate shut.
type gateStub struct {
	srv    *httptest.Server
	paused atomic.Bool
}

func newGateStub(t *testing.T) *gateStub {
	t.Helper()
	g := &gateStub{}
	g.paused.Store(true)
	g.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if g.paused.Load() {
			http.Error(w, "paused", http.StatusServiceUnavailable)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"errors":false,"items":[]}`)
	}))
	t.Cleanup(g.srv.Close)
	return g
}

// TestRetentionWaitsForExport: with the exporter enabled, a finished instance is NOT
// purged while its events are unexported (the exporter is stalled), and IS purged once
// export catches up — the export-before-delete gate (ADR-0115).
func TestRetentionWaitsForExport(t *testing.T) {
	stub := newGateStub(t) // starts paused: bulk fails, so the exporter never advances
	ts := newTestServerWith(t,
		api.WithOpenSearchExporter(opensearch.Config{URL: stub.srv.URL, Index: "atlas-retention-test"}),
		api.WithOpenSearchExportInterval(15*time.Millisecond),
		api.WithRetention(time.Millisecond),
		api.WithRetentionInterval(15*time.Millisecond),
	)
	deployAndTerminateOne(t, ts)

	// While export is stalled the high-water mark stays at 0, so the instance's events
	// are not provably exported and retention must leave it alone.
	time.Sleep(300 * time.Millisecond)
	if rows := listInstances(t, ts); len(rows) != 1 || rows[0].State != "terminated" {
		t.Fatalf("instance was purged before its events were exported (gate breached): %v", rows)
	}

	// Let export catch up; the gate opens and retention purges the instance.
	stub.paused.Store(false)
	waitForPurge(t, "the instance to be purged once exported", func() bool {
		return len(listInstances(t, ts)) == 0
	})
}
