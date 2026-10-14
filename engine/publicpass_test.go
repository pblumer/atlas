package engine_test

import (
	"path/filepath"
	"testing"

	"github.com/pblumer/atlas/engine"
	"github.com/pblumer/atlas/state"
	"github.com/pblumer/atlas/wal"
)

// TestThePublicPassHashIsAFactOfTheInstance: a public start of a process holding a
// public wizard hands its visitor a pass, and the instance keeps the pass's hash
// (ADR-0449). It is written by the activation event, so it is on the record as soon as
// the instance is, and a replay of the log onto an empty store reads it back rather
// than computing anything (I4, I6).
func TestThePublicPassHashIsAFactOfTheInstance(t *testing.T) {
	const hash = "9f86d081884c7d659a2feaa0c55ad015a3bf4f1b2b0b822cd15d6c15b0f00a08"
	dir := t.TempDir()
	v1, v2, _, _ := forkVersions(t)
	clock := &manualClock{}

	h1 := openHarness(t, dir)
	p1 := engine.New(1, h1.log, h1.store, clock)
	p1.Deploy(v1)
	p1.Deploy(v2)
	if err := p1.Recover(); err != nil {
		t.Fatalf("Recover 1: %v", err)
	}
	var key uint64
	p1.CreateInstanceWithPass(v1.Key, &key, hash)
	p1.CreateInstance(v1.Key)
	if err := p1.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle: %v", err)
	}
	if key == 0 {
		t.Fatal("the created instance's key was not reported")
	}
	for k, v := range activeInstances(t, h1.store) {
		want := ""
		if k == key {
			want = hash
		}
		if v.PublicPassHash != want {
			t.Errorf("instance %d carries pass hash %q, want %q", k, v.PublicPassHash, want)
		}
	}
	h1.close(t)

	log2, err := wal.Open(wal.Options{Dir: filepath.Join(dir, "wal")})
	if err != nil {
		t.Fatalf("wal.Open 2: %v", err)
	}
	store2, err := state.Open(filepath.Join(dir, "state2"))
	if err != nil {
		t.Fatalf("state.Open 2: %v", err)
	}
	defer func() {
		_ = store2.Close()
		_ = log2.Close()
	}()
	p2 := engine.New(1, log2, store2, clock)
	p2.Deploy(v1)
	p2.Deploy(v2)
	if err := p2.Recover(); err != nil {
		t.Fatalf("Recover 2: %v", err)
	}
	got, ok, err := store2.ProcessInstance(key)
	if err != nil || !ok {
		t.Fatalf("ProcessInstance after replay: %v (ok=%v)", err, ok)
	}
	if got.PublicPassHash != hash {
		t.Errorf("after replay the pass hash is %q, want %q", got.PublicPassHash, hash)
	}
}
