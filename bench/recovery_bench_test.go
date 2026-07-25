package bench_test

import (
	"fmt"
	"path/filepath"
	"testing"

	"github.com/pblumer/atlas/bench"
	"github.com/pblumer/atlas/bench/scenarios"
)

// recoveryInstances is the log size each recovery op replays. Fixed so the
// number is stable across runs and comparable over time; large enough that
// replay dominates the fixed cost of opening the store.
const recoveryInstances = 1000

// BenchmarkRecovery measures cold restart: replay a populated write-ahead log
// into a fresh, empty store. This is the engine's core correctness promise —
// state after replay equals state built live (invariant I4) — and the only place
// its cost is measured. The log is built once outside the timer; each op opens a
// new store and replays the whole log through the same applyToState used live.
//
// ns/op is the full recovery of recoveryInstances instances; the events/op
// metric reports how many durable events that log holds, so ns/op ÷ events/op is
// the per-event replay cost.
func BenchmarkRecovery(b *testing.B) {
	for _, s := range scenarios.All() {
		b.Run(s.Name, func(b *testing.B) {
			logDir := b.TempDir()
			cp, jobTypes := s.Compile(7)
			events, err := bench.BuildLog(logDir, cp, jobTypes, recoveryInstances, s.StartVars)
			if err != nil {
				b.Fatalf("BuildLog: %v", err)
			}

			b.ReportMetric(float64(events), "events/op")
			b.ResetTimer()
			for i := range b.N {
				storeDir := filepath.Join(b.TempDir(), fmt.Sprintf("r%d", i))
				e, err := bench.Recover(logDir, storeDir, cp)
				if err != nil {
					b.Fatalf("Recover: %v", err)
				}
				if err := e.Close(); err != nil {
					b.Fatalf("Close: %v", err)
				}
			}
			b.StopTimer()
		})
	}
}
