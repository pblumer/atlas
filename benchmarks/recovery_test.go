package benchmarks

import (
	"path/filepath"
	"testing"

	"github.com/pblumer/atlas/compiler"
	"github.com/pblumer/atlas/engine"
	"github.com/pblumer/atlas/state"
	"github.com/pblumer/atlas/wal"
)

// These benchmarks measure the startup/recovery axis rather than steady-state
// throughput: how fast the engine rebuilds state by replaying the WAL from genesis
// after a restart (recovery still replays from genesis — there is no checkpoint
// yet). Each op is one recovered instance, so `ns/op` is the per-instance recovery
// cost and the summary's derived instances/sec is the recovery rate;
// recovery-events/sec = events/op × instances/sec.
//
// The workload for a run is populated into a source WAL with b.N instances, using a
// single RunUntilIdle so the create commands batch into few fsyncs — keeping setup
// (which is excluded from the timer) cheap even at large b.N. Only the fresh engine's
// Recover call — the WAL replay into an empty state store — is measured.

// benchmarkRecovery populates a WAL with b.N instances of cp, then measures recovering
// them into a fresh, empty state store from that WAL.
func benchmarkRecovery(b *testing.B, cp *compiler.CompiledProcess) {
	dir := b.TempDir()
	srcWAL := filepath.Join(dir, "wal")

	// Populate: run b.N instances through one engine, batching the create commands
	// into few fsyncs. This whole block is setup — the timer is not running yet.
	log, err := wal.Open(wal.Options{Dir: srcWAL})
	if err != nil {
		b.Fatalf("wal.Open: %v", err)
	}
	srcStore, err := state.Open(filepath.Join(dir, "state-src"))
	if err != nil {
		b.Fatalf("state.Open (src): %v", err)
	}
	src := engine.New(1, log, srcStore, &benchClock{})
	src.Deploy(cp)
	if err := src.Recover(); err != nil {
		b.Fatalf("Recover (src prime): %v", err)
	}
	for range b.N {
		src.CreateInstance(cp.Key)
	}
	if err := src.RunUntilIdle(); err != nil {
		b.Fatalf("RunUntilIdle (populate): %v", err)
	}
	if err := srcStore.Close(); err != nil { // keep the WAL; discard the source state
		b.Fatalf("Close (src state): %v", err)
	}

	// Recover the populated WAL into a fresh, empty state store — this is the measured
	// work. The empty store's applied position is 0, so recovery replays every event.
	dstStore, err := state.Open(filepath.Join(dir, "state-recover"))
	if err != nil {
		b.Fatalf("state.Open (recover): %v", err)
	}
	dst := engine.New(1, log, dstStore, &benchClock{})
	dst.Deploy(cp)

	b.ReportAllocs()
	b.ResetTimer()
	if err := dst.Recover(); err != nil {
		b.Fatalf("Recover: %v", err)
	}
	b.StopTimer()

	// events/op = events replayed / b.N (per-instance events); walB/op = WAL bytes / b.N.
	reportEngineMetrics(b, dstStore, srcWAL, 0)
	_ = dstStore.Close()
	_ = log.Close()
}

// BenchmarkRecoveryLinearCompleted recovers b.N self-completing linear instances —
// their full lifecycle (Start → Script → End) replays and they land completed in
// history.
func BenchmarkRecoveryLinearCompleted(b *testing.B) {
	benchmarkRecovery(b, linearSelfCompleting(b))
}

// BenchmarkRecoveryServiceTaskParked recovers b.N service-task instances parked on an
// activatable job — recovery restores the active instances and their jobs, the state
// an operator restart must rebuild.
func BenchmarkRecoveryServiceTaskParked(b *testing.B) {
	cp, _ := serviceTaskWorkload(b)
	benchmarkRecovery(b, cp)
}
