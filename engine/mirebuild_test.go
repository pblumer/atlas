package engine_test

import (
	"encoding/binary"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"testing"

	"github.com/cockroachdb/pebble"
	"github.com/pblumer/atlas/compiler"
	"github.com/pblumer/atlas/engine"
	"github.com/pblumer/atlas/model"
	"github.com/pblumer/atlas/state"
	"github.com/pblumer/atlas/wal"
)

// sumMerger mirrors the state store's counter merge operator, so a test can open
// a store's Pebble directory to make it look like one written by an older build.
type sumMerger struct{ sum int64 }

func (m *sumMerger) MergeNewer(v []byte) error {
	if len(v) >= 8 {
		m.sum += int64(binary.LittleEndian.Uint64(v))
	}
	return nil
}
func (m *sumMerger) MergeOlder(v []byte) error { return m.MergeNewer(v) }
func (m *sumMerger) Finish(bool) ([]byte, io.Closer, error) {
	return binary.LittleEndian.AppendUint64(nil, uint64(m.sum)), nil, nil
}

// makeLegacy turns a closed state directory into what a store recorded before
// the MI index existed: no MI rows, no readiness markers — and, to prove they
// are superseded rather than trusted, one stray row of a partial projection.
func makeLegacy(t *testing.T, statePath string) {
	t.Helper()
	db, err := pebble.Open(statePath, &pebble.Options{Merger: &pebble.Merger{
		Name: "atlas.counter.sum.v1",
		Merge: func(_, v []byte) (pebble.ValueMerger, error) {
			m := &sumMerger{}
			return m, m.MergeNewer(v)
		},
	}})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.DeleteRange([]byte{0x38}, []byte{0x44}, pebble.Sync); err != nil {
		t.Fatal(err)
	}
	for _, m := range []string{"mi_history_index_v2", "mi_history_decisions_v1"} {
		if err := db.Delete(append([]byte{0x00}, m...), pebble.Sync); err != nil {
			t.Fatal(err)
		}
	}
	if err := db.Set([]byte{0x38, 1, 2, 3}, []byte("stray"), pebble.Sync); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
}

// runMIWorkload drives a parallel MI with a decision, an incident resolved and
// one left open, and a nested sequential MI subprocess, and returns the instance
// keys and the compiled processes.
func runMIWorkload(t *testing.T, h *harness) ([]uint64, []*compiler.CompiledProcess) {
	t.Helper()
	cp, jobType := miServiceProcess(t, "[1, 2, 3, 4]")
	p := engine.New(1, h.log, h.store, &manualClock{})
	p.Deploy(cp)
	if err := p.Recover(); err != nil {
		t.Fatal(err)
	}
	p.CreateInstance(cp.Key)
	miRun(t, p)
	j1 := jobByLoopCounter(t, h, jobType, 1)
	job1, _, _ := h.store.GetJob(j1)
	p.CompleteJobWithDecision(j1, &model.DecisionEvaluationValue{DecisionId: "d", InputsJSON: `{}`, OutputsJSON: `{}`})
	miRun(t, p)
	j2 := jobByLoopCounter(t, h, jobType, 2)
	job2, _, _ := h.store.GetJob(j2)
	p.FailJob(j2, 0, "x", 0)
	miRun(t, p)
	p.ResolveIncident(job2.ElementInstanceKey, 1)
	miRun(t, p)
	p.FailJob(jobByLoopCounter(t, h, jobType, 3), 0, "y", 0)
	miRun(t, p)
	return []uint64{job1.ProcessInstanceKey}, []*compiler.CompiledProcess{cp}
}

func reopen(t *testing.T, dir string) *harness {
	t.Helper()
	return openHarness(t, dir)
}

func TestMIHistoryRebuildFromTheLogMatchesALiveIndex(t *testing.T) {
	dir := t.TempDir()
	h := openHarness(t, dir)
	pis, cps := runMIWorkload(t, h)
	live := miReadSnapshot(t, h.store, pis[0])
	h.close(t)
	makeLegacy(t, filepath.Join(dir, "state"))

	h = reopen(t, dir)
	defer h.close(t)
	if ready, err := h.store.MIHistoryIndexReady(); err != nil || ready {
		t.Fatalf("legacy store read as ready: %v %v", ready, err)
	}
	p := engine.New(1, h.log, h.store, &manualClock{})
	for _, cp := range cps {
		p.Deploy(cp)
	}
	if err := p.Recover(); err != nil {
		t.Fatal(err)
	}
	scratch := filepath.Join(dir, "mi-history-rebuild")
	res, err := p.RebuildMIHistory(scratch)
	if err != nil || !res.Rebuilt || res.Replayed == 0 || res.Copied == 0 {
		t.Fatalf("rebuild: %+v %v", res, err)
	}
	if _, err := os.Stat(scratch); !os.IsNotExist(err) {
		t.Fatalf("shadow store left behind: %v", err)
	}
	got := miReadSnapshot(t, h.store, pis[0])
	if !reflect.DeepEqual(got, live) {
		t.Fatalf("rebuilt MI history differs from the live index\nlive:    %+v\nrebuilt: %+v", live, got)
	}
	for _, ready := range []func() (bool, error){h.store.MIHistoryIndexReady, h.store.MIDecisionIndexReady} {
		if ok, err := ready(); err != nil || !ok {
			t.Fatalf("not marked complete: %v %v", ok, err)
		}
	}
	// A second run finds nothing to do.
	if res, err := p.RebuildMIHistory(scratch); err != nil || res.Rebuilt {
		t.Fatalf("second run: %+v %v", res, err)
	}
}

func TestMIHistoryRebuildRefusesACompactedLog(t *testing.T) {
	dir := t.TempDir()
	logDir := filepath.Join(dir, "wal")
	wl, err := wal.Open(wal.Options{Dir: logDir, MaxSegmentSize: 1})
	if err != nil {
		t.Fatal(err)
	}
	store, err := state.Open(filepath.Join(dir, "state"))
	if err != nil {
		t.Fatal(err)
	}
	h := &harness{log: wl, store: store}
	runMIWorkload(t, h)
	h.close(t)
	makeLegacy(t, filepath.Join(dir, "state"))
	// Compaction deletes the oldest segments; remove the first one as it would.
	segs, err := filepath.Glob(filepath.Join(logDir, "*"))
	if err != nil || len(segs) < 3 {
		t.Fatalf("segments: %v %v", segs, err)
	}
	sort.Strings(segs)
	if err := os.Remove(segs[0]); err != nil {
		t.Fatal(err)
	}
	if wl, err = wal.Open(wal.Options{Dir: logDir, MaxSegmentSize: 1}); err != nil {
		t.Fatal(err)
	}
	if store, err = state.Open(filepath.Join(dir, "state")); err != nil {
		t.Fatal(err)
	}
	h = &harness{log: wl, store: store}
	defer h.close(t)
	p := engine.New(1, h.log, h.store, &manualClock{})
	if err := p.Recover(); err != nil {
		t.Fatal(err)
	}
	res, err := p.RebuildMIHistory(filepath.Join(dir, "mi-history-rebuild"))
	if err != nil || res.Rebuilt || res.Reason == "" {
		t.Fatalf("compacted log: %+v %v", res, err)
	}
	if ready, _ := h.store.MIHistoryIndexReady(); ready {
		t.Fatal("a compacted log produced a complete index")
	}
}

func TestMIHistoryRebuildOfACompleteStoreDoesNothing(t *testing.T) {
	h := openHarness(t, t.TempDir())
	defer h.close(t)
	p := engine.New(1, h.log, h.store, &manualClock{})
	if err := p.Recover(); err != nil {
		t.Fatal(err)
	}
	res, err := p.RebuildMIHistory(filepath.Join(t.TempDir(), "x"))
	if err != nil || res.Rebuilt || res.Reason == "" {
		t.Fatalf("%+v %v", res, err)
	}
}
