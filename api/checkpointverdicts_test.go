package api

import (
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/pblumer/atlas/checkpoint"
)

// The /metrics checkpoint gauges remember whether a checkpoint verified instead of
// re-hashing every checkpoint on every scrape
// (ADR-0488). The verifications a scrape makes are
// counted where they happen; the gauges' values are held by metrics_test.go, which this
// must leave passing — a corrupted checkpoint is still reported at the next scrape.

// countVerifications replaces the server's checkpoint verification with one that
// counts. A scrape gathers its collectors on goroutines of its own, hence the atomic.
func countVerifications(h *compactionHarness) *atomic.Int32 {
	var n atomic.Int32
	h.srv.verifyCheckpoint = func(root string, pos uint64) (*checkpoint.Manifest, error) {
		n.Add(1)
		return checkpoint.Verify(root, pos)
	}
	return &n
}

// TestScrapesDoNotReadACheckpointThisServerPublished: the commit has just hashed the
// files it published, so no scrape reads them again.
func TestScrapesDoNotReadACheckpointThisServerPublished(t *testing.T) {
	h := newCompactionHarness(t, t.TempDir())
	n := countVerifications(h)
	h.deploy()
	h.create(2)
	h.pass()

	for i := 0; i < 3; i++ {
		if got := sampleValue(t, scrape(t, h), "atlas_checkpoint_position"); got == 0 {
			t.Fatalf("scrape %d: checkpoint position = 0, want the published one credited", i)
		}
	}
	if got := n.Load(); got != 0 {
		t.Errorf("three scrapes verified %d checkpoint(s), want none", got)
	}
}

// TestACheckpointFromBeforeARestartIsReadOnce: a checkpoint this process did not publish
// is verified by the first scrape and remembered for the next.
func TestACheckpointFromBeforeARestartIsReadOnce(t *testing.T) {
	h := newCompactionHarness(t, t.TempDir())
	h.deploy()
	h.create(2)
	h.pass()
	h.srv.verdicts.mu.Lock()
	h.srv.verdicts.byPos = nil // what a restarted server knows
	h.srv.verdicts.mu.Unlock()
	n := countVerifications(h)

	scrape(t, h)
	scrape(t, h)
	if got := n.Load(); got != 1 {
		t.Errorf("two scrapes after a restart verified %d time(s), want once", got)
	}
}

// TestARememberedVerdictIsRenewedAfterADay: rot that changes no file metadata is
// caught within checkpointReverifyAfter, because the verdict expires.
func TestARememberedVerdictIsRenewedAfterADay(t *testing.T) {
	clk := &fakeClock{}
	clk.set(int64(100 * time.Hour))
	h := newCompactionHarness(t, t.TempDir(), withClock(clk.Now))
	n := countVerifications(h)
	h.deploy()
	h.create(2)
	h.pass()

	clk.set(clk.Now() + int64(checkpointReverifyAfter) - 1)
	scrape(t, h)
	if got := n.Load(); got != 0 {
		t.Fatalf("a scrape inside the day verified %d time(s), want none", got)
	}
	clk.set(clk.Now() + 1)
	scrape(t, h)
	if got := n.Load(); got != 1 {
		t.Errorf("a scrape a day after the verdict verified %d time(s), want once", got)
	}
}

// TestAnIdlePassDoesNotRenewTheVerdict: on a server with nothing new to capture, every
// pass finds its position already published and hashes nothing — so it must not count
// as a verification, or the daily one would never come due.
func TestAnIdlePassDoesNotRenewTheVerdict(t *testing.T) {
	clk := &fakeClock{}
	clk.set(int64(100 * time.Hour))
	h := newCompactionHarness(t, t.TempDir(), withClock(clk.Now))
	n := countVerifications(h)
	h.deploy()
	h.create(2)
	h.pass()

	clk.set(clk.Now() + int64(checkpointReverifyAfter))
	h.pass() // idle: the same position, nothing written
	scrape(t, h)
	if got := n.Load(); got != 1 {
		t.Errorf("a scrape a day after the only real publish verified %d time(s), want once", got)
	}
}

// TestAChangedFileIsVerifiedAtOnce: a state file replaced under a checkpoint changes its
// fingerprint, so the remembered verdict is not used and the scrape verifies again.
func TestAChangedFileIsVerifiedAtOnce(t *testing.T) {
	dir := t.TempDir()
	h := newCompactionHarness(t, dir)
	n := countVerifications(h)
	h.deploy()
	h.create(2)
	h.pass()

	positions, err := checkpoint.List(checkpoint.Dir(dir))
	if err != nil || len(positions) != 1 {
		t.Fatalf("List = %v, %v; want one checkpoint", positions, err)
	}
	cpDir := filepath.Join(checkpoint.Dir(dir), checkpoint.DirName(positions[0]))
	entries, err := os.ReadDir(cpDir)
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	for _, e := range entries {
		if e.IsDir() || e.Name() == checkpoint.ManifestName {
			continue
		}
		// Unlink first: the file is a hard link into the live store.
		p := filepath.Join(cpDir, e.Name())
		if err := os.Remove(p); err != nil {
			t.Fatalf("unlink: %v", err)
		}
		if err := os.WriteFile(p, []byte("changed"), 0o644); err != nil {
			t.Fatalf("rewrite: %v", err)
		}
		break
	}

	if got := sampleValue(t, scrape(t, h), "atlas_checkpoint_position"); got != 0 {
		t.Errorf("checkpoint position = %v after its file changed, want 0", got)
	}
	if got := n.Load(); got != 1 {
		t.Errorf("the scrape after a file changed verified %d time(s), want once", got)
	}
}

// TestVerdictsOfPrunedCheckpointsAreForgotten: the map is as large as the checkpoint
// root, not as large as the history of checkpoints.
func TestVerdictsOfPrunedCheckpointsAreForgotten(t *testing.T) {
	h := newCompactionHarness(t, t.TempDir(), WithCheckpointRetention(1))
	h.deploy()
	for i := 0; i < 3; i++ {
		h.create(1)
		h.pass()
	}
	scrape(t, h)
	h.srv.verdicts.mu.Lock()
	got := len(h.srv.verdicts.byPos)
	h.srv.verdicts.mu.Unlock()
	if got != 1 {
		t.Errorf("verdicts remembered = %d with one checkpoint kept, want 1", got)
	}
}
