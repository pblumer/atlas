package engine_test

import (
	"fmt"
	"math"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"testing"

	"github.com/pblumer/atlas/compiler"
	"github.com/pblumer/atlas/engine"
	"github.com/pblumer/atlas/model"
	"github.com/pblumer/atlas/state"
	"github.com/pblumer/atlas/wal"
)

// Deterministic crash-and-recovery harness (v0.2.0 programme C). It turns the
// durability contract into checkable evidence: a workload is run to a durable point,
// the on-disk WAL is edited to model a crash, and a fresh engine recovers from it
// into an empty state store. The recovered state is compared, family by family,
// against a snapshot of the live state — so "state after replay == state built live"
// (invariant I4) is asserted directly, along with the boundary cases a real crash
// produces.
//
// The crashes are modelled on the WAL's own durability boundaries: Append buffers a
// batch's frames in memory and one Sync per batch writes the whole batch and fsyncs,
// so a batch's frames land atomically at a known file offset. That lets a test drop
// an un-fsynced batch at a clean boundary — no production fault hooks, no
// partial-batch ambiguity. The WAL already treats a torn or CRC-bad trailing frame as
// a crash tail and stops cleanly (wal/reader.go); these tests pin that behaviour.
//
// Deferred to later programme-C increments: in-process phase-boundary crash hooks for
// the exact after-append / after-commit cut points, child-process (SIGKILL) crashes,
// the no-side-effect-before-durability ordering assertion, and richer workloads
// (timers, messages, incidents).

// stateSnapshot is a canonical, order-independent view of the durable state families
// a recovery must rebuild identically.
type stateSnapshot struct {
	appliedPos uint64
	instances  []string
	elements   []string
	jobs       []string
	timers     []string
	incidents  []string
	variables  []string
}

// snapshot reads every durable family the workloads here touch into sorted,
// string-encoded slices, so two snapshots compare with reflect.DeepEqual regardless
// of iteration order.
func snapshot(t testing.TB, s *state.Store) stateSnapshot {
	t.Helper()
	var snap stateSnapshot

	pos, err := s.LastAppliedPosition()
	if err != nil {
		t.Fatalf("LastAppliedPosition: %v", err)
	}
	snap.appliedPos = pos

	if err := s.ActiveProcessInstances(func(k uint64, v *model.ProcessInstanceValue) error {
		snap.instances = append(snap.instances, fmt.Sprintf("active %d state=%v def=%d", k, v.State, v.ProcessDefKey))
		return s.VariablesOfScope(k, func(vv *model.VariableValue) error {
			snap.variables = append(snap.variables, fmt.Sprintf("scope=%d %s=%s(kind=%v)", k, vv.Name, vv.Text, vv.Kind))
			return nil
		})
	}); err != nil {
		t.Fatalf("ActiveProcessInstances: %v", err)
	}
	if err := s.CompletedProcessInstances(func(k uint64, v *model.ProcessInstanceValue) error {
		snap.instances = append(snap.instances, fmt.Sprintf("done %d state=%v at=%d pos=%d", k, v.State, v.CompletedAt, v.CompletedPosition))
		return nil
	}); err != nil {
		t.Fatalf("CompletedProcessInstances: %v", err)
	}
	if err := s.ActiveElementInstances(func(k uint64, v *model.ElementInstanceValue) error {
		snap.elements = append(snap.elements, fmt.Sprintf("%d pi=%d el=%d scope=%d token=%d", k, v.ProcessInstanceKey, v.ElementId, v.FlowScopeKey, v.TokenID))
		return nil
	}); err != nil {
		t.Fatalf("ActiveElementInstances: %v", err)
	}
	if err := s.AllActivatableJobs(func(jk uint64) error {
		j, ok, err := s.GetJob(jk)
		if err != nil {
			return err
		}
		if !ok {
			return fmt.Errorf("activatable job %d has no job record", jk)
		}
		snap.jobs = append(snap.jobs, fmt.Sprintf("%d pi=%d el=%d type=%d retries=%d", jk, j.ProcessInstanceKey, j.ElementInstanceKey, j.JobType, j.Retries))
		return nil
	}); err != nil {
		t.Fatalf("AllActivatableJobs: %v", err)
	}
	// All pending instance timers (DueTimers with an effectively-infinite horizon)
	// plus deploy-armed start timers.
	if err := s.DueTimers(math.MaxInt64, func(tk uint64, v *model.TimerValue) error {
		snap.timers = append(snap.timers, fmt.Sprintf("%d pi=%d target=%d due=%d reps=%d", tk, v.ProcessInstanceKey, v.TargetElementId, v.DueDate, v.Repetitions))
		return nil
	}); err != nil {
		t.Fatalf("DueTimers: %v", err)
	}
	if err := s.StartTimers(func(tk uint64, v *model.TimerValue) error {
		snap.timers = append(snap.timers, fmt.Sprintf("start %d def=%d due=%d reps=%d", tk, v.ProcessDefKey, v.DueDate, v.Repetitions))
		return nil
	}); err != nil {
		t.Fatalf("StartTimers: %v", err)
	}
	if err := s.Incidents(func(ek uint64, v *model.IncidentValue) error {
		snap.incidents = append(snap.incidents, fmt.Sprintf("%d pi=%d el=%d msg=%q", ek, v.ProcessInstanceKey, v.ElementId, v.Message))
		return nil
	}); err != nil {
		t.Fatalf("Incidents: %v", err)
	}

	for _, sl := range [][]string{snap.instances, snap.elements, snap.jobs, snap.timers, snap.incidents, snap.variables} {
		sort.Strings(sl)
	}
	return snap
}

func assertSnapshotEqual(t *testing.T, got, want stateSnapshot, what string) {
	t.Helper()
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("%s: recovered state != reference state\n got: %+v\nwant: %+v", what, got, want)
	}
}

// --- WAL manipulation (models a crash by editing the durable bytes) -----------

// walSegment returns the single active segment file for the tiny workloads here.
func walSegment(t testing.TB, dir string) string {
	t.Helper()
	segs, err := filepath.Glob(filepath.Join(dir, "wal", "*.wal"))
	if err != nil {
		t.Fatalf("glob segments: %v", err)
	}
	if len(segs) != 1 {
		t.Fatalf("expected exactly one WAL segment, got %d (%v)", len(segs), segs)
	}
	return segs[0]
}

func walSize(t testing.TB, dir string) int64 {
	t.Helper()
	fi, err := os.Stat(walSegment(t, dir))
	if err != nil {
		t.Fatalf("stat segment: %v", err)
	}
	return fi.Size()
}

// truncateWAL cuts the segment to size bytes — modelling a crash that left only the
// first size bytes durable (an un-fsynced batch, or a torn frame if size falls
// inside one).
func truncateWAL(t *testing.T, dir string, size int64) {
	t.Helper()
	if err := os.Truncate(walSegment(t, dir), size); err != nil {
		t.Fatalf("truncate segment: %v", err)
	}
}

// corruptWALByte flips one byte at off — modelling media corruption of a frame that
// the WAL's per-frame CRC must catch.
func corruptWALByte(t *testing.T, dir string, off int64) {
	t.Helper()
	f, err := os.OpenFile(walSegment(t, dir), os.O_RDWR, 0)
	if err != nil {
		t.Fatalf("open segment: %v", err)
	}
	defer f.Close()
	var b [1]byte
	if _, err := f.ReadAt(b[:], off); err != nil {
		t.Fatalf("read byte @%d: %v", off, err)
	}
	b[0] ^= 0xFF
	if _, err := f.WriteAt(b[:], off); err != nil {
		t.Fatalf("write byte @%d: %v", off, err)
	}
}

// --- Scenario setup + recovery ------------------------------------------------

// scenario is the fixture the crash tests share: a directory whose WAL holds two
// separately-fsynced batches — A then B — each creating one parked service-task
// instance, plus the state snapshots after A (snapA) and after B (snapAB), and the
// WAL size at the A|B boundary (batch B's frames begin exactly there).
type scenario struct {
	dir      string
	cp       *compiler.CompiledProcess
	snapA    stateSnapshot
	snapAB   stateSnapshot
	walSizeA int64
}

func twoBatchScenario(t *testing.T) scenario {
	t.Helper()
	dir := t.TempDir()
	cp, _ := linearProcess(t) // Start → ServiceTask → End; a created instance parks on the job
	clock := &manualClock{}

	h := openHarness(t, dir)
	p := engine.New(1, h.log, h.store, clock)
	p.Deploy(cp)
	if err := p.Recover(); err != nil {
		t.Fatalf("prime Recover: %v", err)
	}

	// Batch A: one instance with a variable, run to its parked service task.
	p.CreateInstance(cp.Key, model.VariableValue{Name: "n", Kind: model.VarNumber, Text: "1"})
	if err := p.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle A: %v", err)
	}
	snapA := snapshot(t, h.store)
	walSizeA := walSize(t, dir)

	// Batch B: a second instance — a separate fsynced batch whose frames start at
	// walSizeA.
	p.CreateInstance(cp.Key, model.VariableValue{Name: "n", Kind: model.VarNumber, Text: "2"})
	if err := p.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle B: %v", err)
	}
	snapAB := snapshot(t, h.store)

	h.close(t)
	return scenario{dir: dir, cp: cp, snapA: snapA, snapAB: snapAB, walSizeA: walSizeA}
}

// recoverFrom opens the (possibly edited) source WAL, recovers it into a brand-new,
// empty state store, and returns a snapshot of the rebuilt state. The empty store's
// applied position is 0, so recovery replays every surviving frame from genesis.
func recoverFrom(t *testing.T, sc scenario) stateSnapshot {
	t.Helper()
	log, err := wal.Open(wal.Options{Dir: filepath.Join(sc.dir, "wal")})
	if err != nil {
		t.Fatalf("reopen wal: %v", err)
	}
	stateDir := t.TempDir()
	store, err := state.Open(filepath.Join(stateDir, "state"))
	if err != nil {
		t.Fatalf("fresh state.Open: %v", err)
	}
	p := engine.New(1, log, store, &manualClock{})
	p.Deploy(sc.cp)
	if err := p.Recover(); err != nil {
		t.Fatalf("Recover: %v", err)
	}
	snap := snapshot(t, store)
	if err := store.Close(); err != nil {
		t.Errorf("store.Close: %v", err)
	}
	if err := log.Close(); err != nil {
		t.Errorf("log.Close: %v", err)
	}
	return snap
}

// TestCrashRecoveryReplayEqualsLive: recovering the intact WAL into an empty store
// rebuilds exactly the state the live run produced — invariant I4.
func TestCrashRecoveryReplayEqualsLive(t *testing.T) {
	sc := twoBatchScenario(t)
	assertSnapshotEqual(t, recoverFrom(t, sc), sc.snapAB, "intact replay")
}

// TestCrashRecoveryUnsyncedBatchAbsent: a crash after batch A committed but before
// batch B's fsync leaves only A's bytes durable. Recovery must show A's full state
// and none of B — un-acknowledged work is legitimately absent, the acknowledged
// prefix is intact and consistent.
func TestCrashRecoveryUnsyncedBatchAbsent(t *testing.T) {
	sc := twoBatchScenario(t)
	truncateWAL(t, sc.dir, sc.walSizeA)
	assertSnapshotEqual(t, recoverFrom(t, sc), sc.snapA, "un-fsynced batch B dropped")
}

// TestCrashRecoveryTornFrameSkipped: a crash mid-write leaves batch B's first frame
// torn (a few bytes past the boundary). The WAL stops at the torn frame, so recovery
// yields exactly the state after A.
func TestCrashRecoveryTornFrameSkipped(t *testing.T) {
	sc := twoBatchScenario(t)
	truncateWAL(t, sc.dir, sc.walSizeA+3) // partial header of B's first frame
	assertSnapshotEqual(t, recoverFrom(t, sc), sc.snapA, "torn frame skipped")
}

// TestCrashRecoveryCorruptFrameSkipped: flipping a byte inside batch B's first frame
// fails its CRC; the WAL treats it as a crash tail and stops there, so recovery
// yields the state after A. Pins the per-frame CRC guard.
func TestCrashRecoveryCorruptFrameSkipped(t *testing.T) {
	sc := twoBatchScenario(t)
	corruptWALByte(t, sc.dir, sc.walSizeA+8) // first payload byte of B's first frame
	assertSnapshotEqual(t, recoverFrom(t, sc), sc.snapA, "CRC-corrupt frame skipped")
}

// TestCrashRecoveryIdempotent: two independent recoveries of the same WAL produce the
// same state, and re-running Recover on an already-recovered store changes nothing —
// restart is idempotent, replayed events are not double-applied.
func TestCrashRecoveryIdempotent(t *testing.T) {
	sc := twoBatchScenario(t)
	assertSnapshotEqual(t, recoverFrom(t, sc), sc.snapAB, "first recovery")
	assertSnapshotEqual(t, recoverFrom(t, sc), sc.snapAB, "second independent recovery")

	// Re-run Recover on the same store: a no-op that must not double-apply.
	log, err := wal.Open(wal.Options{Dir: filepath.Join(sc.dir, "wal")})
	if err != nil {
		t.Fatalf("reopen wal: %v", err)
	}
	stateDir := t.TempDir()
	store, err := state.Open(filepath.Join(stateDir, "state"))
	if err != nil {
		t.Fatalf("state.Open: %v", err)
	}
	defer func() {
		if err := store.Close(); err != nil {
			t.Errorf("store.Close: %v", err)
		}
		if err := log.Close(); err != nil {
			t.Errorf("log.Close: %v", err)
		}
	}()
	p := engine.New(1, log, store, &manualClock{})
	p.Deploy(sc.cp)
	if err := p.Recover(); err != nil {
		t.Fatalf("Recover once: %v", err)
	}
	once := snapshot(t, store)
	if err := p.Recover(); err != nil {
		t.Fatalf("Recover twice: %v", err)
	}
	assertSnapshotEqual(t, snapshot(t, store), once, "re-running Recover is a no-op")
}
