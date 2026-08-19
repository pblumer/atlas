package benchmarks

import (
	"math"
	"net/http"
	"sort"
	"testing"
	"time"
)

// Go benchmarks report a mean (ns/op), which hides the tail. Under the durable
// profile the per-op time is gated by disk fsync, whose latency is skewed — the mean
// understates what a slow request actually costs. These benchmarks record each
// operation's wall-clock latency and report the distribution's P50/P95/P99 (and max)
// so the tail is visible.
//
// Method: each iteration's latency is sampled with time.Now, the samples are sorted,
// and percentiles are taken by nearest-rank (the smallest sample at or above the
// p-th position). One sample per iteration, so a run collects b.N samples — use
// -benchtime=Nx to fix N; the tail wants a large N (P99 needs >=100 samples to mean
// anything, and stabilizes around a few thousand). The percentiles are reported as
// custom metrics (p50-ms, p95-ms, p99-ms, max-ms); read them from the raw `-bench`
// output or via benchstat, which renders every metric.

// reportLatencyPercentiles sorts the per-op samples and reports the tail as metrics.
func reportLatencyPercentiles(b *testing.B, samples []time.Duration) {
	b.Helper()
	if len(samples) == 0 {
		return
	}
	sort.Slice(samples, func(i, j int) bool { return samples[i] < samples[j] })
	ms := func(d time.Duration) float64 { return float64(d.Nanoseconds()) / 1e6 }
	// Nearest-rank: rank = ceil(p/100 * N), 1-indexed, clamped into range.
	pct := func(p float64) float64 {
		rank := int(math.Ceil(p/100*float64(len(samples)))) - 1
		if rank < 0 {
			rank = 0
		}
		if rank >= len(samples) {
			rank = len(samples) - 1
		}
		return ms(samples[rank])
	}
	b.ReportMetric(pct(50), "p50-ms")
	b.ReportMetric(pct(95), "p95-ms")
	b.ReportMetric(pct(99), "p99-ms")
	b.ReportMetric(ms(samples[len(samples)-1]), "max-ms")
}

// BenchmarkLatencyHTTPLinearCreate reports the end-to-end latency distribution of a
// create-to-completion request over the HTTP path (the durable profile), so the fsync
// tail a client would see is visible, not just the mean.
func BenchmarkLatencyHTTPLinearCreate(b *testing.B) {
	h, _, _ := durableServer(b)
	serveOK(b, h, http.MethodPost, "/api/v1/deployments", linearBPMN, "application/xml")

	samples := make([]time.Duration, 0, b.N)
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		t0 := time.Now()
		serveOK(b, h, http.MethodPost, "/api/v1/processes/1/instances", "{}", "application/json")
		samples = append(samples, time.Since(t0))
	}
	b.StopTimer()
	reportLatencyPercentiles(b, samples)
}

// BenchmarkLatencyEngineLinearSelfCompleting reports the same distribution for the
// pure engine (no HTTP), so the tail can be attributed: the difference from the HTTP
// benchmark is the API layer, and the tail within it is the fsync distribution.
func BenchmarkLatencyEngineLinearSelfCompleting(b *testing.B) {
	p, _, _ := durableEngine(b)
	cp := linearSelfCompleting(b)
	deploy(b, p, cp)

	samples := make([]time.Duration, 0, b.N)
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		t0 := time.Now()
		runToIdle(b, p, cp.Key)
		samples = append(samples, time.Since(t0))
	}
	b.StopTimer()
	reportLatencyPercentiles(b, samples)
}
