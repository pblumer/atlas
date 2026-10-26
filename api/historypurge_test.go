package api

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/pblumer/atlas/engine"
	"github.com/pblumer/atlas/model"
	"github.com/pblumer/atlas/opensearch"
	"github.com/pblumer/atlas/state"
	"github.com/pblumer/atlas/wal"
)

// An administrator deletes a deployed version's finished instances up to a cut-off,
// in bounded batches, through the purge event retention already uses
// (ADR-0486). These tests reuse the
// deterministic retention harness: one fake clock stamps CompletedAt and answers the
// default cut-off, so which instance is "before" is decided by the test, never by
// timing.

func purgePath(defKey uint64) string {
	return "/api/v1/processes/" + strconv.FormatUint(defKey, 10) + "/history-purge"
}

func (h retentionHarness) purgePreview(defKey uint64, query string) historyPurgePlan {
	h.t.Helper()
	code, body := h.x.do(http.MethodGet, purgePath(defKey)+query, "")
	if code != http.StatusOK {
		h.t.Fatalf("GET history-purge%s status=%d body=%s", query, code, body)
	}
	var out historyPurgePlan
	if err := json.Unmarshal(body, &out); err != nil {
		h.t.Fatalf("decode preview: %v (%s)", err, body)
	}
	return out
}

func (h retentionHarness) purgeBatch(defKey uint64, query string) (int, historyPurgeBatch, []byte) {
	h.t.Helper()
	code, body := h.x.do(http.MethodPost, purgePath(defKey)+query, "")
	var out historyPurgeBatch
	if code == http.StatusOK {
		if err := json.Unmarshal(body, &out); err != nil {
			h.t.Fatalf("decode batch: %v (%s)", err, body)
		}
	}
	return code, out, body
}

// finishedAt starts an instance, finishes it at the given moment and puts the clock
// back where it was, so a test can lay out a history on a timeline.
func (h retentionHarness) finishedAt(at int64) uint64 {
	h.t.Helper()
	now := h.clk.Now()
	key := h.createParkedOf(1)
	h.clk.set(at)
	h.terminate(key)
	h.clk.set(now)
	return key
}

func (h retentionHarness) stateOf(key uint64) string {
	h.t.Helper()
	for _, r := range h.list() {
		if r.Key == key {
			return r.State
		}
	}
	return ""
}

func nanos(at int64) string { return "?before=" + strconv.FormatInt(at, 10) }

// TestHistoryPurgePreviewSaysWhatWouldGo: the preview counts the version's instances
// that finished before the cut-off, names the running ones it leaves alone, and says
// what the delete has to repeat — and it changes nothing.
func TestHistoryPurgePreviewSaysWhatWouldGo(t *testing.T) {
	const t0 = int64(10 * time.Hour)
	h := newRetentionHarness(t, t0)
	h.deploy()
	h.finishedAt(t0)
	h.finishedAt(t0 + int64(time.Hour))
	h.finishedAt(t0 + int64(2*time.Hour))
	h.createParkedOf(1) // running, never a candidate

	p := h.purgePreview(1, nanos(t0+int64(90*time.Minute)))
	if p.Finished != 2 {
		t.Errorf("finished before the cut-off = %d, want 2", p.Finished)
	}
	if p.Running != 1 {
		t.Errorf("running = %d, want 1", p.Running)
	}
	if !p.Enabled || p.Confirm != "order" || p.ProcessID != "order" || p.Version != 1 {
		t.Errorf("preview = %+v, want enabled, confirm and process id \"order\", version 1", p)
	}
	if p.Gate != historyPurgeGateDurable {
		t.Errorf("gate = %q with no exporter, want %q", p.Gate, historyPurgeGateDurable)
	}
	if p.Before != t0+int64(90*time.Minute) {
		t.Errorf("before = %d, want the cut-off asked for", p.Before)
	}
	if n := len(h.list()); n != 4 {
		t.Fatalf("the preview changed the history: %d instances, want 4", n)
	}

	// Without a cut-off the preview counts up to now, and answers the moment it used so
	// the delete can repeat it.
	h.clk.set(t0 + int64(3*time.Hour))
	if p := h.purgePreview(1, ""); p.Finished != 3 || p.Before != t0+int64(3*time.Hour) {
		t.Errorf("preview without a cut-off = %+v, want 3 finished before now", p)
	}
}

// TestHistoryPurgeDeletesWhatFinishedBeforeTheCutOff: the delete removes exactly the
// instances the preview counted. One that finished later and one still running stay.
func TestHistoryPurgeDeletesWhatFinishedBeforeTheCutOff(t *testing.T) {
	const t0 = int64(10 * time.Hour)
	h := newRetentionHarness(t, t0)
	h.deploy()
	early := h.finishedAt(t0)
	middle := h.finishedAt(t0 + int64(time.Hour))
	late := h.finishedAt(t0 + int64(2*time.Hour))
	running := h.createParkedOf(1)

	code, b, body := h.purgeBatch(1, nanos(t0+int64(90*time.Minute))+"&confirm=order")
	if code != http.StatusOK {
		t.Fatalf("purge status=%d body=%s", code, body)
	}
	if b.Purged != 2 || b.Held != 0 || b.Remaining {
		t.Fatalf("batch = %+v, want 2 purged, none held, nothing remaining", b)
	}
	for _, k := range []uint64{early, middle} {
		if st := h.stateOf(k); st != "" {
			t.Errorf("instance %d finished before the cut-off is still listed as %q", k, st)
		}
	}
	if st := h.stateOf(late); st != "terminated" {
		t.Errorf("instance finished after the cut-off = %q, want it kept as terminated", st)
	}
	if st := h.stateOf(running); st != "active" {
		t.Errorf("running instance = %q, want it untouched", st)
	}
	if p := h.purgePreview(1, nanos(t0+int64(90*time.Minute))); p.Finished != 0 {
		t.Errorf("preview after the purge counts %d, want 0", p.Finished)
	}
}

// TestHistoryPurgeNeedsTheProcessIDRepeated: nothing is deleted unless the request
// repeats the process id, the confirmation a person types.
func TestHistoryPurgeNeedsTheProcessIDRepeated(t *testing.T) {
	const t0 = int64(10 * time.Hour)
	h := newRetentionHarness(t, t0)
	h.deploy()
	key := h.finishedAt(t0)
	h.clk.set(t0 + int64(time.Hour))

	for _, q := range []string{"", "?confirm=", "?confirm=other", "?confirm=ORDER"} {
		if code, _, body := h.purgeBatch(1, q); code != http.StatusBadRequest {
			t.Errorf("purge%s status=%d body=%s, want 400", q, code, body)
		}
	}
	if st := h.stateOf(key); st != "terminated" {
		t.Fatalf("a refused purge deleted the instance (state %q)", st)
	}
}

// TestHistoryPurgeRefusedWhereTheServerSwitchedItOff: --instance-purge=false refuses
// the delete before anything is read, and the preview still answers and says so.
func TestHistoryPurgeRefusedWhereTheServerSwitchedItOff(t *testing.T) {
	const t0 = int64(10 * time.Hour)
	h := newRetentionHarness(t, t0, WithoutInstancePurge())
	h.deploy()
	key := h.finishedAt(t0)
	h.clk.set(t0 + int64(time.Hour))

	if p := h.purgePreview(1, ""); p.Enabled || p.Finished != 1 {
		t.Errorf("preview on a server that refuses the purge = %+v, want enabled false and the count", p)
	}
	code, _, body := h.purgeBatch(1, "?confirm=order")
	if code != http.StatusForbidden {
		t.Fatalf("purge status=%d body=%s, want 403", code, body)
	}
	if st := h.stateOf(key); st != "terminated" {
		t.Fatalf("a refused purge deleted the instance (state %q)", st)
	}
}

// TestHistoryPurgeRunsInBatchesWithACursor: a limited call deletes at most its limit,
// says more remain and where to go on, and the calls together delete everything — the
// last one says nothing remains.
func TestHistoryPurgeRunsInBatchesWithACursor(t *testing.T) {
	const t0 = int64(10 * time.Hour)
	h := newRetentionHarness(t, t0)
	h.deploy()
	for i := int64(0); i < 3; i++ {
		h.finishedAt(t0 + i)
	}
	h.clk.set(t0 + int64(time.Hour))
	before := nanos(t0 + int64(time.Hour))

	total, calls, after := 0, 0, ""
	for {
		calls++
		if calls > 10 {
			t.Fatal("the batches never reported that nothing remains")
		}
		q := before + "&confirm=order&limit=2"
		if after != "" {
			q += "&after=" + after
		}
		code, b, body := h.purgeBatch(1, q)
		if code != http.StatusOK {
			t.Fatalf("batch %d status=%d body=%s", calls, code, body)
		}
		if b.Purged > 2 {
			t.Fatalf("batch %d purged %d with limit 2", calls, b.Purged)
		}
		total += b.Purged
		if !b.Remaining {
			break
		}
		if b.NextCursor == "" {
			t.Fatalf("batch %d reports more remaining and no cursor", calls)
		}
		after = b.NextCursor
	}
	if total != 3 || calls != 2 {
		t.Errorf("purged %d in %d calls, want 3 in 2", total, calls)
	}
	if n := len(h.list()); n != 0 {
		t.Errorf("%d instances left after the batches, want 0", n)
	}
}

// TestHistoryPurgeWaitsForExport: with the exporter on, an instance whose events are
// not yet in OpenSearch is passed over and counted as held; once export has caught up,
// the same request deletes it (ADR-0115's gate, unchanged).
func TestHistoryPurgeWaitsForExport(t *testing.T) {
	stub := newGateStub(t)
	const t0 = int64(10 * time.Hour)
	h := newRetentionHarness(t, t0,
		WithOpenSearchExporter(opensearch.Config{URL: stub.srv.URL, Index: "atlas-purge-test"}),
		WithOpenSearchExportInterval(time.Hour),
	)
	h.deploy()
	key := h.finishedAt(t0)
	h.clk.set(t0 + int64(time.Hour))

	if p := h.purgePreview(1, ""); p.Gate != historyPurgeGateExporter {
		t.Errorf("gate = %q with the exporter on, want %q", p.Gate, historyPurgeGateExporter)
	}
	code, b, body := h.purgeBatch(1, "?confirm=order")
	if code != http.StatusOK {
		t.Fatalf("purge status=%d body=%s", code, body)
	}
	if b.Purged != 0 || b.Held != 1 || b.Remaining {
		t.Fatalf("batch with export stalled = %+v, want nothing purged, one held, nothing remaining", b)
	}
	if st := h.stateOf(key); st != "terminated" {
		t.Fatalf("an unexported instance was deleted (state %q)", st)
	}

	stub.paused.Store(false)
	h.exportUntilCaughtUp()
	if _, b, _ := h.purgeBatch(1, "?confirm=order"); b.Purged != 1 || b.Held != 0 {
		t.Fatalf("batch after export caught up = %+v, want the instance purged", b)
	}
}

// TestHistoryPurgeAnswersForAKnownVersionOnly: a key that names no deployment is a 404
// for the preview and the delete alike.
func TestHistoryPurgeAnswersForAKnownVersionOnly(t *testing.T) {
	h := newRetentionHarness(t, int64(time.Hour))
	h.deploy()
	if code, body := h.x.do(http.MethodGet, purgePath(99), ""); code != http.StatusNotFound {
		t.Errorf("preview of an unknown version status=%d body=%s, want 404", code, body)
	}
	if code, _, body := h.purgeBatch(99, "?confirm=order"); code != http.StatusNotFound {
		t.Errorf("purge of an unknown version status=%d body=%s, want 404", code, body)
	}
}

// TestHistoryPurgeRefusesMalformedParameters: a cut-off, limit or cursor it cannot read
// is a 400, never a purge of a range somebody did not ask for.
func TestHistoryPurgeRefusesMalformedParameters(t *testing.T) {
	h := newRetentionHarness(t, int64(time.Hour))
	h.deploy()
	for _, q := range []string{"?before=yesterday", "?before=-5", "?before=0"} {
		if code, body := h.x.do(http.MethodGet, purgePath(1)+q, ""); code != http.StatusBadRequest {
			t.Errorf("preview%s status=%d body=%s, want 400", q, code, body)
		}
	}
	for _, q := range []string{"&limit=0", "&limit=-1", "&limit=x", "&after=12", "&after=a:b", "&before=nope"} {
		if code, _, body := h.purgeBatch(1, "?confirm=order"+q); code != http.StatusBadRequest {
			t.Errorf("purge%s status=%d body=%s, want 400", q, code, body)
		}
	}
}

// TestHistoryPurgeIsForAdministrators: deleting history for good is an
// administrator's, and so is reading what it would delete — both decided at the
// boundary like every role.
func TestHistoryPurgeIsForAdministrators(t *testing.T) {
	ordinary := []string{RoleModeler, RoleOperator, RoleUser}
	for _, method := range []string{http.MethodGet, http.MethodPost} {
		if got := boundaryStatus(t, ordinary, method, purgePath(1)); got != http.StatusForbidden {
			t.Errorf("%s history-purge without an admin principal: status=%d, want 403", method, got)
		}
	}
}

// TestHistoryPurgeGate pins the rule an instance is deleted under. With the exporter
// on it is ADR-0115's: a known terminal position at or below the high-water mark. With
// it off the gate is durability, which a record in the state store already has, so a
// record from before the position was kept (zero) goes too.
func TestHistoryPurgeGate(t *testing.T) {
	for _, tc := range []struct {
		name     string
		pos      uint64
		safe     uint64
		exporter bool
		want     bool
	}{
		{"exported", 5, 9, true, true},
		{"at the mark", 9, 9, true, true},
		{"not yet exported", 10, 9, true, false},
		{"no position, exporter on", 0, 9, true, false},
		{"durable", 5, 9, false, true},
		{"no position, exporter off", 0, 9, false, true},
		{"beyond the durable position", 10, 9, false, false},
	} {
		pi := &model.ProcessInstanceValue{CompletedPosition: tc.pos}
		if got := historyPurgeable(pi, tc.safe, tc.exporter); got != tc.want {
			t.Errorf("%s: historyPurgeable(pos %d, safe %d, exporter %v) = %v, want %v",
				tc.name, tc.pos, tc.safe, tc.exporter, got, tc.want)
		}
	}
}

// TestHistoryPurgeIsReplayedOnRecovery: the delete is the durable purge event, so a
// server whose state store is rebuilt from the log alone has not got the instance back
// (invariants I4 and I6) — the property that makes a purge a purge rather than a
// cache eviction.
func TestHistoryPurgeIsReplayedOnRecovery(t *testing.T) {
	dir := t.TempDir()
	clk := &fakeClock{}
	clk.set(int64(10 * time.Hour))

	open := func() (*Server, *state.Store, *wal.Log) {
		lg, err := wal.Open(wal.Options{Dir: filepath.Join(dir, "wal")})
		if err != nil {
			t.Fatalf("wal.Open: %v", err)
		}
		store, err := state.Open(filepath.Join(dir, "state"))
		if err != nil {
			t.Fatalf("state.Open: %v", err)
		}
		proc := engine.New(1, lg, store, clk)
		if err := proc.Recover(); err != nil {
			t.Fatalf("Recover: %v", err)
		}
		srv, err := New(proc, store, dir, withClock(clk.Now))
		if err != nil {
			t.Fatalf("New: %v", err)
		}
		return srv, store, lg
	}
	shut := func(srv *Server, store *state.Store, lg *wal.Log) {
		srv.Close()
		if err := store.Close(); err != nil {
			t.Fatalf("store.Close: %v", err)
		}
		if err := lg.Close(); err != nil {
			t.Fatalf("log.Close: %v", err)
		}
	}

	srv, store, lg := open()
	h := retentionHarness{t: t, srv: srv, x: deployTestHarness{t, srv.Handler()}, clk: clk}
	h.deploy()
	gone := h.finishedAt(clk.Now())
	kept := h.createParkedOf(1)
	clk.set(clk.Now() + int64(time.Hour))
	if code, b, body := h.purgeBatch(1, "?confirm=order"); code != http.StatusOK || b.Purged != 1 {
		t.Fatalf("purge = %d %+v %s, want one purged", code, b, body)
	}
	shut(srv, store, lg)

	// Throw the state store away: what comes back is what the log says.
	if err := os.RemoveAll(filepath.Join(dir, "state")); err != nil {
		t.Fatalf("remove the state store: %v", err)
	}
	srv, store, lg = open()
	defer shut(srv, store, lg)

	if _, ok, err := store.ProcessInstance(gone); err != nil || ok {
		t.Errorf("after replay the purged instance is back (found=%v, err=%v)", ok, err)
	}
	if n, err := store.CountFinishedOfDefBefore(1, clk.Now()+1); err != nil || n != 0 {
		t.Errorf("after replay %d finished instances are indexed (err %v), want 0", n, err)
	}
	if v, ok, err := store.ProcessInstance(kept); err != nil || !ok || v.State != model.PIActive {
		t.Errorf("after replay the running instance = (%+v, %v, %v), want it active", v, ok, err)
	}
}
