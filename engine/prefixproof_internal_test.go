package engine

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pblumer/atlas/checkpoint"
	"github.com/pblumer/atlas/compiler"
	"github.com/pblumer/atlas/state"
	"github.com/pblumer/atlas/wal"
)

// compactedRun builds a log whose oldest segments have been compacted away, with a
// checkpoint that covers what was deleted. It returns the data directory, the
// checkpoint root, the compiled process, and the shape a correct recovery reaches.
func compactedRun(t *testing.T) (dir, root string, cp *compiler.CompiledProcess, want runShape) {
	t.Helper()
	dir = t.TempDir()
	cp = childViewProcess(t, 7, "compacted", false)

	l, err := wal.Open(wal.Options{Dir: filepath.Join(dir, "wal"), MaxSegmentSize: 1})
	if err != nil {
		t.Fatalf("wal.Open: %v", err)
	}
	s, err := state.Open(filepath.Join(dir, "state"))
	if err != nil {
		t.Fatalf("state.Open: %v", err)
	}
	p := New(1, l, s, &wbClock{})
	p.Deploy(cp)
	p.CreateInstance(cp.Key)
	if err := p.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle: %v", err)
	}
	want = shapeOf(t, s)

	root = filepath.Join(dir, "checkpoints")
	if _, err := p.Checkpoint(root); err != nil {
		t.Fatalf("Checkpoint: %v", err)
	}
	n, err := p.CompactLog(root, nil)
	if err != nil {
		t.Fatalf("CompactLog: %v", err)
	}
	if n == 0 {
		t.Fatal("fixture compacted nothing; it proves nothing about a missing prefix")
	}
	t.Logf("compacted %d segments; correct recovery reaches %s", n, want)
	if err := s.Close(); err != nil {
		t.Fatalf("state.Close: %v", err)
	}
	if err := l.Close(); err != nil {
		t.Fatalf("wal.Close: %v", err)
	}
	return dir, root, cp, want
}

// recoverInto opens a fresh processor over dir's log and the named state
// directory, and returns whatever Recover reports.
func recoverIntoState(t *testing.T, dir, stateName, root string, cp *compiler.CompiledProcess) (runShape, error) {
	t.Helper()
	l, err := wal.Open(wal.Options{Dir: filepath.Join(dir, "wal")})
	if err != nil {
		return runShape{}, err
	}
	defer l.Close()
	s, err := state.Open(filepath.Join(dir, stateName))
	if err != nil {
		t.Fatalf("state.Open: %v", err)
	}
	defer s.Close()
	p := New(1, l, s, &wbClock{})
	p.Deploy(cp)
	if rerr := p.RecoverFrom(root); rerr != nil {
		return runShape{}, rerr
	}
	return shapeOf(t, s), nil
}

// TestCompactedLogWithNoStateFailsClosed is F04. Compaction deletes the WAL prefix
// a checkpoint covers, so that prefix exists nowhere else. Recovering into a state
// store that does not already hold it cannot work — and the one thing recovery
// must not do is report success anyway.
//
// It did. checkpointSeed only ever skipped reading a prefix; it never installed
// one, and it refused any checkpoint ahead of the store. With an empty store that
// meant no checkpoint qualified, recovery fell back to what it took for a replay
// from genesis, and genesis was no longer there. The engine came up reporting a
// clean recovery with the instances simply missing.
func TestCompactedLogWithNoStateFailsClosed(t *testing.T) {
	dir, root, cp, want := compactedRun(t)

	got, err := recoverIntoState(t, dir, "fresh-state", root, cp)
	if err != nil {
		// Refusing is correct. It has to say what to do about it.
		if !strings.Contains(strings.ToLower(err.Error()), "checkpoint") {
			t.Errorf("refusal %q does not point at the checkpoint that would fix it", err)
		}
		return
	}
	if got != want {
		t.Fatalf("recovery reported success and reached %s; the uncompacted run reaches %s. "+
			"A prefix that was compacted away cannot be replayed, so this must either restore "+
			"it or refuse", got, want)
	}
}

// TestRecoveryStillWorksWhereThePrefixIsProvable: the gate must only close where
// the prefix is genuinely missing. The state that produced the checkpoint still
// covers it, and an uncompacted log carries its own genesis — both must recover
// exactly as before.
func TestRecoveryStillWorksWhereThePrefixIsProvable(t *testing.T) {
	t.Run("state that already holds the prefix", func(t *testing.T) {
		dir, root, cp, want := compactedRun(t)
		got, err := recoverIntoState(t, dir, "state", root, cp)
		if err != nil {
			t.Fatalf("recovery into the state that produced the checkpoint: %v", err)
		}
		if got != want {
			t.Fatalf("reached %s, want %s", got, want)
		}
	})

	t.Run("whole log into an empty store", func(t *testing.T) {
		dir := t.TempDir()
		cp := childViewProcess(t, 7, "whole", false)
		l, err := wal.Open(wal.Options{Dir: filepath.Join(dir, "wal")})
		if err != nil {
			t.Fatalf("wal.Open: %v", err)
		}
		s, err := state.Open(filepath.Join(dir, "state"))
		if err != nil {
			t.Fatalf("state.Open: %v", err)
		}
		p := New(1, l, s, &wbClock{})
		p.Deploy(cp)
		p.CreateInstance(cp.Key)
		if err := p.RunUntilIdle(); err != nil {
			t.Fatalf("RunUntilIdle: %v", err)
		}
		want := shapeOf(t, s)
		s.Close()
		l.Close()

		got, err := recoverIntoState(t, dir, "fresh-state", "", cp)
		if err != nil {
			t.Fatalf("recovery of a whole log into an empty store: %v", err)
		}
		if got != want {
			t.Fatalf("reached %s, want %s", got, want)
		}
	})
}

// TestCompactedLogWithAnUnusableCheckpointFailsClosed: a checkpoint that cannot be
// trusted is not a prefix. Falling back to a replay of what is left would boot an
// engine missing everything below the cut, and the fallback cannot tell the
// difference.
func TestCompactedLogWithAnUnusableCheckpointFailsClosed(t *testing.T) {
	dir, root, cp, _ := compactedRun(t)
	positions, err := checkpoint.List(root)
	if err != nil || len(positions) == 0 {
		t.Fatalf("fixture has no checkpoint: %v", err)
	}
	// Corrupt the manifest of every checkpoint the run produced.
	for _, pos := range positions {
		manifest := filepath.Join(root, checkpoint.DirName(pos), checkpoint.ManifestName)
		if err := os.WriteFile(manifest, []byte("{not json"), 0o644); err != nil {
			t.Fatalf("corrupt manifest: %v", err)
		}
	}
	if _, err := recoverIntoState(t, dir, "fresh-state", root, cp); err == nil {
		t.Fatal("recovery with a compacted log and no usable checkpoint reported success")
	}
}
