package engine_test

import (
	"path/filepath"
	"testing"

	"github.com/pblumer/atlas/compiler"
	"github.com/pblumer/atlas/engine"
	"github.com/pblumer/atlas/model"
	"github.com/pblumer/atlas/state"
	"github.com/pblumer/atlas/wal"
)

// childrenOf reads the reverse call-activity index for one element instance.
func childrenOf(t *testing.T, s *state.Store, callElKey uint64) []uint64 {
	t.Helper()
	var out []uint64
	if err := s.ChildInstancesOfParent(callElKey, func(childPiKey uint64) error {
		out = append(out, childPiKey)
		return nil
	}); err != nil {
		t.Fatalf("ChildInstancesOfParent: %v", err)
	}
	return out
}

// callElementInstance returns the key of the caller's live call-activity element
// instance — the parent side of the link under test.
func callElementInstance(t *testing.T, s *state.Store) uint64 {
	t.Helper()
	var found uint64
	if err := s.ActiveElementInstances(func(key uint64, v *model.ElementInstanceValue) error {
		if v.BpmnElementType == uint8(compiler.TypeCallActivity) {
			found = key
		}
		return nil
	}); err != nil {
		t.Fatalf("ActiveElementInstances: %v", err)
	}
	if found == 0 {
		t.Fatal("no live call-activity element instance")
	}
	return found
}

// TestChildIndexIsWrittenAndRebuiltOnReplay covers the reverse call-activity link as
// derived state: it must be written when the child starts, and — because it is
// written from the child's own activation event and nothing else — rebuilt
// identically when that log is replayed into an empty store (I4/I6).
//
// The index is what makes tearing a caller down a lookup instead of a walk of every
// live instance, so a replay that failed to rebuild it would leave a recovered
// engine unable to find children it is responsible for terminating.
func TestChildIndexIsWrittenAndRebuiltOnReplay(t *testing.T) {
	dir := t.TempDir()
	h1 := openHarness(t, dir)
	p1, childDef := callerWithChildServiceTask(t, h1, &manualClock{}, func(b *compiler.Builder, call int32) {
		b.Connect(call, b.AddEndEvent())
	})
	_ = p1
	_ = childDef

	callEl := callElementInstance(t, h1.store)
	live := childrenOf(t, h1.store, callEl)
	if len(live) != 1 {
		t.Fatalf("index holds %d children for the call activity, want 1", len(live))
	}
	child := live[0]
	h1.close(t)

	// Replay the same log into a fresh, empty store: the index must come back.
	log2, err := wal.Open(wal.Options{Dir: filepath.Join(dir, "wal")})
	if err != nil {
		t.Fatalf("wal.Open 2: %v", err)
	}
	defer log2.Close()
	store2, err := state.Open(filepath.Join(dir, "state2"))
	if err != nil {
		t.Fatalf("state.Open 2: %v", err)
	}
	defer store2.Close()
	p2 := engine.New(1, log2, store2, &manualClock{})
	if err := p2.Recover(); err != nil {
		t.Fatalf("Recover 2: %v", err)
	}
	if got := childrenOf(t, store2, callEl); len(got) != 1 || got[0] != child {
		t.Errorf("after replay the index holds %v, want [%d]", got, child)
	}
}

// TestChildIndexDropsTheLinkWhenTheChildEnds keeps the index to *live* children:
// that is what the teardown asks it for, and a link left behind would make every
// cancel of a long-lived caller walk a growing list of finished children.
func TestChildIndexDropsTheLinkWhenTheChildEnds(t *testing.T) {
	h := openHarness(t, t.TempDir())
	defer h.close(t)
	p, _ := callerWithChildServiceTask(t, h, &manualClock{}, func(b *compiler.Builder, call int32) {
		b.Connect(call, b.AddEndEvent())
	})
	callEl := callElementInstance(t, h.store)
	if got := childrenOf(t, h.store, callEl); len(got) != 1 {
		t.Fatalf("index holds %d children while the child runs, want 1", len(got))
	}

	// Cancelling the caller terminates the child through this very index; once it is
	// gone the link must be gone with it.
	p.CancelInstance(model.NewKey(1, 1))
	if err := p.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle: %v", err)
	}
	if got := childrenOf(t, h.store, callEl); len(got) != 0 {
		t.Errorf("index still holds %v after the child ended, want none", got)
	}
}
