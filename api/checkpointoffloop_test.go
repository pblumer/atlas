package api

import (
	"context"
	"slices"
	"testing"
	"time"
)

// These tests hold the property the checkpoint split exists for: the run loop — Atlas's
// single writer, and the thing every API request has to reach through — is free while a
// checkpoint is being committed.
//
// It matters because committing reads every byte of the state store to checksum it.
// Done on the loop, that stops command processing and every request with it for as long
// as the read takes, which grows with the store; on a large one it is seconds of a
// server that answers nothing. Nothing about the symptom points at the checkpoint, so
// the guard has to be a test.

// TestCheckpointCommitRunsWithTheRunLoopFree: between staging (which must hold the
// writer) and committing (which must not), the loop answers.
func TestCheckpointCommitRunsWithTheRunLoopFree(t *testing.T) {
	dir := t.TempDir()
	var (
		srv      *Server
		ran      bool
		loopFree bool
	)
	h := bootCheckpointServer(t, dir, withCheckpointOffLoopHook(func(string) {
		ran = true
		// A Ping hands an empty closure to the loop and waits for it to run, so it
		// answers exactly the question here: is the writer free right now? The deadline
		// is a failsafe — a free loop takes it immediately, and a held one could only be
		// held by the commit this hook runs before.
		loopFree = srv.runLoop.Ping(context.Background(), 5*time.Second)
	}))
	srv = h.srv
	h.deploy()
	h.createInstance()
	h.checkpointNow()

	if !ran {
		t.Fatal("the staged hook never ran, so nothing was asserted")
	}
	if !loopFree {
		t.Error("the run loop was still held when the checkpoint was committed; " +
			"the whole-state checksum belongs off the writer")
	}
	if got := h.published(); len(got) != 1 {
		t.Errorf("published = %v, want exactly one checkpoint", got)
	}
}

// TestCheckpointStagesOnTheLoopAndCommitsAfter: the pass is still one ordered sequence —
// the staged position is captured before the hook runs, and the checkpoint appears on
// disk only after it. Without this, a "free loop" could be free because nothing was
// staged at all.
func TestCheckpointStagesOnTheLoopAndCommitsAfter(t *testing.T) {
	dir := t.TempDir()
	var seen []uint64
	h := bootCheckpointServer(t, dir)
	h.deploy()
	h.createInstance()
	// Set after the boot so the hook can read the harness; the checkpoint goroutine
	// only reaches it on the tick below, which happens after this assignment.
	h.srv.checkpointOffLoop = func(string) { seen = h.published() }
	h.checkpointNow()

	if len(seen) != 0 {
		t.Errorf("checkpoints on disk while staging = %v, want none until the commit", seen)
	}
	applied := h.applied()
	got := h.published()
	if len(got) != 1 || got[0] != applied {
		t.Errorf("published = %v, want [%d] — the staged position, published by the commit", got, applied)
	}
}

// TestCompactionVerificationRunsWithTheRunLoopFree: resolving the compaction cut
// verifies checkpoints, and verifying one reads every byte of its state files — the
// same whole-store read as the commit, and the same reason it must not happen on the
// writer. Only the deletion that follows belongs there.
func TestCompactionVerificationRunsWithTheRunLoopFree(t *testing.T) {
	dir := t.TempDir()
	var (
		srv    *Server
		phases []string
		free   = map[string]bool{}
	)
	h := newCompactionHarness(t, dir, WithWALCompaction(), withCheckpointOffLoopHook(func(phase string) {
		phases = append(phases, phase)
		free[phase] = srv.runLoop.Ping(context.Background(), 5*time.Second)
	}))
	srv = h.srv
	h.deploy()
	h.create(2)
	h.pass()

	for _, want := range []string{"commit", "cut"} {
		if !slices.Contains(phases, want) {
			t.Fatalf("phases = %v, want it to include %q", phases, want)
		}
		if !free[want] {
			t.Errorf("the run loop was held during the %q phase; that phase reads the whole "+
				"state store and belongs off the writer", want)
		}
	}
}

// TestCompactionOnAnEmptyStoreReadsNothing: with nothing durable yet there is no
// applied position, so no checkpoint can qualify and the cut is zero. The pass must
// reach that answer without verifying anything — the verification is the whole-store
// read, and paying it to conclude "nothing" is the cost this split exists to avoid.
func TestCompactionOnAnEmptyStoreReadsNothing(t *testing.T) {
	dir := t.TempDir()
	var phases []string
	h := newCompactionHarness(t, dir, WithWALCompaction(), withCheckpointOffLoopHook(func(phase string) {
		phases = append(phases, phase)
	}))
	// No deploy, no instances: nothing has been written, so LastAppliedPosition is zero.
	h.pass()

	if slices.Contains(phases, "cut") {
		t.Errorf("phases = %v; an empty store must short-circuit before the verification read", phases)
	}
	if got := h.segments(); got == 0 {
		t.Errorf("segments = %d, want the log left intact", got)
	}
}
