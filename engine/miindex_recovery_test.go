package engine_test

import (
	"path/filepath"
	"reflect"
	"testing"

	"github.com/pblumer/atlas/compiler"
	"github.com/pblumer/atlas/engine"
	"github.com/pblumer/atlas/state"
	"github.com/pblumer/atlas/wal"
)

func miHistoryProjection(t *testing.T, s *state.Store, piKey uint64) []state.MIHistoryEntry {
	t.Helper()
	var entries []state.MIHistoryEntry
	err := s.MIBodies(piKey, 100, func(body state.MIHistoryEntry) error {
		entries = append(entries, body)
		return s.MIIterations(piKey, body.ElementInstanceKey, body.ActivationPosition, 100,
			func(round state.MIHistoryEntry) error {
				entries = append(entries, round)
				return nil
			})
	})
	if err != nil {
		t.Fatalf("MI projection: %v", err)
	}
	return entries
}

// TestMIHistoryProjectionReplayEqualsLive proves that the same WAL produces
// identical body activation positions, round ownership and lifecycle outcomes
// when recovered into a completely fresh Pebble store.
func TestMIHistoryProjectionReplayEqualsLive(t *testing.T) {
	tests := []struct {
		name  string
		build func(*testing.T, string) (*compiler.CompiledProcess, int32)
	}{
		{"parallel", miServiceProcess},
		{"sequential", miSeqServiceProcess},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			cp, jobType := tt.build(t, "[1, 2, 3]")
			h := openHarness(t, dir)
			p := engine.New(1, h.log, h.store, &manualClock{})
			p.Deploy(cp)
			if err := p.Recover(); err != nil {
				t.Fatal(err)
			}
			p.CreateInstance(cp.Key)
			if err := p.RunUntilIdle(); err != nil {
				t.Fatal(err)
			}
			first := activatableJobs(t, h.store, jobType)
			if len(first) == 0 {
				t.Fatal("no MI jobs")
			}
			job, ok, err := h.store.GetJob(first[0])
			if err != nil || !ok {
				t.Fatalf("job lookup: %v, %v", ok, err)
			}
			piKey := job.ProcessInstanceKey
			for i := 0; i < 3; i++ {
				jobs := activatableJobs(t, h.store, jobType)
				if len(jobs) == 0 {
					t.Fatalf("no remaining job at iteration %d", i)
				}
				p.CompleteJob(jobs[0])
				if err := p.RunUntilIdle(); err != nil {
					t.Fatal(err)
				}
			}
			live := miHistoryProjection(t, h.store, piKey)
			if len(live) != 4 {
				t.Fatalf("live projection has %d records, want body and 3 iterations: %+v", len(live), live)
			}
			if live[0].State != state.MIHistoryCompleted {
				t.Fatalf("body not complete: %+v", live[0])
			}
			for _, r := range live[1:] {
				if r.BodyElementKey != live[0].ElementInstanceKey ||
					r.BodyActivationPosition != live[0].ActivationPosition ||
					r.State != state.MIHistoryCompleted {
					t.Fatalf("wrong round lineage/outcome: %+v", r)
				}
			}
			h.close(t)

			log, err := wal.Open(wal.Options{Dir: filepath.Join(dir, "wal")})
			if err != nil {
				t.Fatal(err)
			}
			defer log.Close()
			recoveredStore, err := state.Open(filepath.Join(t.TempDir(), "state"))
			if err != nil {
				t.Fatal(err)
			}
			defer recoveredStore.Close()
			replay := engine.New(1, log, recoveredStore, &manualClock{})
			replay.Deploy(cp)
			if err := replay.Recover(); err != nil {
				t.Fatal(err)
			}
			got := miHistoryProjection(t, recoveredStore, piKey)
			if !reflect.DeepEqual(got, live) {
				t.Fatalf("MI index differs after WAL replay\nlive: %+v\nreplay: %+v", live, got)
			}
		})
	}
}
