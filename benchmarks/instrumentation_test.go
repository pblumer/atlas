package benchmarks

import (
	"testing"

	"github.com/pblumer/atlas/engine"
	"github.com/pblumer/atlas/metrics"
	"github.com/prometheus/client_golang/prometheus"
)

// ADR-0142 requires instrumentation to be provably cheap rather than assumed cheap. This
// pair is the evidence: the same steady-state workload with and without the engine's
// batch metrics attached, so `ns/op` shows the overhead and `allocs/op` shows there is
// none.
//
//	go test ./benchmarks -bench 'Instrument' -benchmem -count=10 | tee new.txt
//	benchstat new.txt
//
// Expect the two to be statistically indistinguishable in allocs/op (both flat) and
// within noise in ns/op: the cost is two clock reads and a handful of atomic adds per
// *batch*, against a batch that already performed an fsync.

// benchEngineMetrics is the same shape the server registers — every handle resolved once,
// at construction — so the benchmark measures the real implementation and not a stand-in.
type benchEngineMetrics struct {
	batches, commands, events    prometheus.Counter
	syncSeconds, commitSeconds   prometheus.Histogram
	batchEvents                  prometheus.Histogram
	syncFailures, commitFailures prometheus.Counter
	queueDepth                   prometheus.Gauge
}

func newBenchEngineMetrics() *benchEngineMetrics {
	counter := func(name string) prometheus.Counter {
		return prometheus.NewCounter(prometheus.CounterOpts{Namespace: metrics.Namespace, Name: name, Help: name})
	}
	histogram := func(name string, buckets []float64) prometheus.Histogram {
		return prometheus.NewHistogram(prometheus.HistogramOpts{
			Namespace: metrics.Namespace, Name: name, Help: name, Buckets: buckets,
		})
	}
	return &benchEngineMetrics{
		batches:        counter("batches_total"),
		commands:       counter("commands_processed_total"),
		events:         counter("events_written_total"),
		batchEvents:    histogram("batch_events", prometheus.ExponentialBuckets(1, 2, 11)),
		syncSeconds:    histogram("wal_sync_seconds", prometheus.ExponentialBuckets(0.0001, 2, 15)),
		commitSeconds:  histogram("state_commit_seconds", prometheus.ExponentialBuckets(0.0001, 2, 15)),
		syncFailures:   counter("wal_sync_failures_total"),
		commitFailures: counter("state_commit_failures_total"),
		queueDepth: prometheus.NewGauge(prometheus.GaugeOpts{
			Namespace: metrics.Namespace, Name: "command_queue_depth", Help: "command_queue_depth",
		}),
	}
}

func (m *benchEngineMetrics) BatchCommitted(s engine.BatchStats) {
	m.batches.Inc()
	m.commands.Add(float64(s.Commands))
	m.queueDepth.Set(float64(s.QueueDepth))
	if s.Events == 0 {
		return
	}
	m.events.Add(float64(s.Events))
	m.batchEvents.Observe(float64(s.Events))
	m.syncSeconds.Observe(s.SyncSeconds)
	m.commitSeconds.Observe(s.CommitSeconds)
}

func (m *benchEngineMetrics) SyncFailed()   { m.syncFailures.Inc() }
func (m *benchEngineMetrics) CommitFailed() { m.commitFailures.Inc() }

// BenchmarkUninstrumented is the baseline: the service-task lifecycle with no metrics.
func BenchmarkUninstrumented(b *testing.B) {
	benchInstrumentation(b, false)
}

// BenchmarkInstrumented is the same workload with the engine's batch metrics attached.
func BenchmarkInstrumented(b *testing.B) {
	benchInstrumentation(b, true)
}

func benchInstrumentation(b *testing.B, instrument bool) {
	b.Helper()
	p, store, walDir := durableEngine(b)
	if instrument {
		p.SetMetrics(newBenchEngineMetrics())
	}
	cp, jobType := serviceTaskWorkload(b)
	deploy(b, p, cp)
	startPos := appliedPosition(b, store)

	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		runToIdle(b, p, cp.Key)
		completeOneJob(b, p, store, jobType)
	}
	b.StopTimer()
	reportEngineMetrics(b, store, walDir, startPos)
}
