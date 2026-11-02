package engine

import (
	"encoding/binary"
	"io"
	"path/filepath"
	"testing"

	"github.com/cockroachdb/pebble"
	"github.com/pblumer/atlas/compiler"
	"github.com/pblumer/atlas/expr"
	"github.com/pblumer/atlas/state"
	"github.com/pblumer/atlas/wal"
)

// counterSum mirrors the state store's counter merge operator, so the test can
// open its Pebble directory.
type counterSum struct{ sum int64 }

func (m *counterSum) MergeNewer(v []byte) error {
	if len(v) >= 8 {
		m.sum += int64(binary.LittleEndian.Uint64(v))
	}
	return nil
}
func (m *counterSum) MergeOlder(v []byte) error { return m.MergeNewer(v) }
func (m *counterSum) Finish(bool) ([]byte, io.Closer, error) {
	return binary.LittleEndian.AppendUint64(nil, uint64(m.sum)), nil, nil
}

type fixedClock struct{}

func (fixedClock) Now() int64 { return 1 }

// rebuildWorld runs a three-iteration MI script task, then strips the store's MI
// readiness markers so it reads as a store that predates the index.
func rebuildWorld(t *testing.T) (*Processor, *state.Store, string) {
	t.Helper()
	dir := t.TempDir()
	log, err := wal.Open(wal.Options{Dir: filepath.Join(dir, "wal")})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = log.Close() })
	statePath := filepath.Join(dir, "state")
	store, err := state.Open(statePath)
	if err != nil {
		t.Fatal(err)
	}
	b := compiler.NewBuilder(1, "mi-rebuild", 1)
	start := b.AddStartEvent()
	script, err := expr.CompileAuto("loopCounter")
	if err != nil {
		t.Fatal(err)
	}
	card, err := expr.CompileAuto("3")
	if err != nil {
		t.Fatal(err)
	}
	work := b.AddScriptTask(script, "r")
	b.SetMultiInstance(work, false, "", "", nil, card, nil, nil)
	end := b.AddEndEvent()
	b.Connect(start, work)
	b.Connect(work, end)
	cp, err := b.Build()
	if err != nil {
		t.Fatal(err)
	}
	p := New(1, log, store, fixedClock{})
	p.Deploy(cp)
	if err := p.Recover(); err != nil {
		t.Fatal(err)
	}
	p.CreateInstance(cp.Key)
	if err := p.RunUntilIdle(); err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	db, err := pebble.Open(statePath, &pebble.Options{Merger: &pebble.Merger{
		Name: "atlas.counter.sum.v1",
		Merge: func(_, v []byte) (pebble.ValueMerger, error) {
			m := &counterSum{}
			return m, m.MergeNewer(v)
		},
	}})
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range []string{"mi_history_index_v2", "mi_history_decisions_v1", "instance_outline_v1", "mi_iteration_steps_v1"} {
		if err := db.Delete(append([]byte{0x00}, m...), pebble.Sync); err != nil {
			t.Fatal(err)
		}
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	if store, err = state.Open(statePath); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	p = New(1, log, store, fixedClock{})
	if err := p.Recover(); err != nil {
		t.Fatal(err)
	}
	return p, store, filepath.Join(dir, "mi-history-rebuild")
}

// A shadow store left behind by an interrupted rebuild is resumed from its own
// applied position, not replayed from the beginning again.
func TestMIHistoryRebuildResumesAPartialShadowReplay(t *testing.T) {
	p, store, scratch := rebuildWorld(t)
	target, err := store.LastAppliedPosition()
	if err != nil || target < 4 {
		t.Fatalf("target %d %v", target, err)
	}
	shadow, err := state.Open(scratch)
	if err != nil {
		t.Fatal(err)
	}
	first, err := replayInto(p, shadow, 0, target/2)
	if err != nil || first == 0 {
		t.Fatalf("partial replay: %d %v", first, err)
	}
	if err := shadow.Close(); err != nil {
		t.Fatal(err)
	}
	res, err := p.RebuildMIHistory(scratch)
	if err != nil || !res.Rebuilt {
		t.Fatalf("resumed rebuild: %+v %v", res, err)
	}
	// The resumed run replays only what the interrupted one had not.
	all, err := countEvents(p, t.TempDir(), target)
	if err != nil {
		t.Fatal(err)
	}
	if res.Replayed != all-first {
		t.Fatalf("resume replayed %d events; %d of %d were already in the shadow store", res.Replayed, first, all)
	}
	if ready, err := store.MIHistoryIndexReady(); err != nil || !ready {
		t.Fatalf("not ready: %v %v", ready, err)
	}
}

// What the log cannot prove stays unavailable and says why.
func TestMIHistoryRebuildLeavesUnprovableStoresUnavailable(t *testing.T) {
	t.Run("log ends before the store", func(t *testing.T) {
		p, store, scratch := rebuildWorld(t)
		target, _ := store.LastAppliedPosition()
		tx := store.NewTransaction()
		if err := tx.SetLastAppliedPosition(target + 100); err != nil {
			t.Fatal(err)
		}
		if err := tx.Commit(); err != nil {
			t.Fatal(err)
		}
		_ = tx.Close()
		res, err := p.RebuildMIHistory(scratch)
		if err != nil || res.Rebuilt || res.Reason == "" {
			t.Fatalf("%+v %v", res, err)
		}
	})
	t.Run("shadow store ahead of the live store", func(t *testing.T) {
		p, store, scratch := rebuildWorld(t)
		target, _ := store.LastAppliedPosition()
		shadow, err := state.Open(scratch)
		if err != nil {
			t.Fatal(err)
		}
		tx := shadow.NewTransaction()
		if err := tx.SetLastAppliedPosition(target + 1); err != nil {
			t.Fatal(err)
		}
		if err := tx.Commit(); err != nil {
			t.Fatal(err)
		}
		_ = tx.Close()
		_ = shadow.Close()
		if _, err := p.RebuildMIHistory(scratch); err == nil {
			t.Fatal("a shadow store ahead of the live store was used")
		}
	})
	t.Run("history without an applied position", func(t *testing.T) {
		p, store, scratch := rebuildWorld(t)
		tx := store.NewTransaction()
		if err := tx.SetLastAppliedPosition(0); err != nil {
			t.Fatal(err)
		}
		if err := tx.Commit(); err != nil {
			t.Fatal(err)
		}
		_ = tx.Close()
		res, err := p.RebuildMIHistory(scratch)
		if err != nil || res.Rebuilt || res.Reason == "" {
			t.Fatalf("%+v %v", res, err)
		}
	})
}

// countEvents counts the log's events up to target.
func countEvents(p *Processor, dir string, target uint64) (int, error) {
	shadow, err := state.Open(dir)
	if err != nil {
		return 0, err
	}
	defer shadow.Close()
	return replayInto(p, shadow, 0, target)
}

// A long log is folded in bounded transactions, each recording the applied
// position, so the shadow store's progress survives an interruption between them.
func TestMIHistoryRebuildCommitsInBoundedTransactions(t *testing.T) {
	p, store, scratch := rebuildWorld(t)
	defer func(n int) { miRebuildCommitEvery = n }(miRebuildCommitEvery)
	miRebuildCommitEvery = 2
	target, _ := store.LastAppliedPosition()
	shadow, err := state.Open(scratch)
	if err != nil {
		t.Fatal(err)
	}
	n, err := replayInto(p, shadow, 0, target)
	if err != nil || n < 4 {
		t.Fatalf("replayed %d: %v", n, err)
	}
	reached, err := shadow.LastAppliedPosition()
	if err != nil || reached != target {
		t.Fatalf("shadow reached %d of %d: %v", reached, target, err)
	}
	if err := shadow.Close(); err != nil {
		t.Fatal(err)
	}
	res, err := p.RebuildMIHistory(scratch)
	if err != nil || !res.Rebuilt || res.Replayed != 0 {
		t.Fatalf("a shadow store already at the target is adopted without replay: %+v %v", res, err)
	}
}
