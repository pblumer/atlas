package api

import (
	"context"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"
)

// probedServer is a server whose reference probe runs only when the test ticks it.
func probedServer(t *testing.T) (*Server, chan<- time.Time, <-chan struct{}) {
	t.Helper()
	ticks := make(chan time.Time)
	ticked := make(chan struct{})
	srv, _ := newOffLoopServer(t, withRunLoopProbeTrigger(ticks, ticked))
	return srv, ticks, ticked
}

// probe runs one probe through the server's own prober goroutine and waits for it.
func probe(t *testing.T, ticks chan<- time.Time, ticked <-chan struct{}) {
	t.Helper()
	ticks <- time.Now()
	select {
	case <-ticked:
	case <-time.After(30 * time.Second):
		t.Fatal("the probe did not finish")
	}
}

// TestTheReferenceProbeReachesTheExposition is the whole feature end to end: a tick
// produces one wait sample and one sample of each work part, and nothing is counted as
// a timeout or a failure on a healthy server.
func TestTheReferenceProbeReachesTheExposition(t *testing.T) {
	srv, ticks, ticked := probedServer(t)

	probe(t, ticks, ticked)
	probe(t, ticks, ticked)

	exposition := scrapeServer(t, srv)
	for name, want := range map[string]float64{
		"atlas_runloop_probe_wait_seconds_count":                 2,
		`atlas_runloop_probe_work_seconds_count{part="store"}`:   2,
		`atlas_runloop_probe_work_seconds_count{part="compute"}`: 2,
		"atlas_runloop_probe_timeouts_total":                     0,
		"atlas_runloop_probe_failures_total":                     0,
	} {
		if got := sampleValue(t, exposition, name); got != want {
			t.Errorf("%s = %v after two probes, want %v", name, got, want)
		}
	}
	// The compute part is real work, so after two runs its total cannot be zero; a zero
	// would mean the computation was optimised away and the part measures nothing.
	if sum := sampleValue(t, exposition, `atlas_runloop_probe_work_seconds_sum{part="compute"}`); sum <= 0 {
		t.Errorf("compute work seconds sum = %v, want a positive total", sum)
	}
}

// TestAWedgedWriterCountsAsAProbeTimeout: a probe that cannot get onto the writer is a
// timeout, not a wait sample, because the wait it would report is only a lower bound.
func TestAWedgedWriterCountsAsAProbeTimeout(t *testing.T) {
	srv, _, _ := probedServer(t)
	p := &runLoopProber{srv: srv, metrics: newRunLoopProbeMetrics(), deadline: 50 * time.Millisecond}

	release := make(chan struct{})
	occupied := make(chan struct{})
	go srv.do(func() { close(occupied); <-release })
	<-occupied
	p.probeOnce(context.Background())
	close(release)

	if got := counterValue(t, p.metrics.timeouts); got != 1 {
		t.Errorf("timeouts = %v after a probe behind a wedged writer, want 1", got)
	}
	if got := histogramCount(t, p.metrics.wait); got != 0 {
		t.Errorf("wait samples = %v for a probe that never got onto the writer, want 0", got)
	}
}

// TestAProbeAbandonedAtShutdownIsNotATimeout: a server that is stopping cancels the
// probe, and that is not the writer being slow.
func TestAProbeAbandonedAtShutdownIsNotATimeout(t *testing.T) {
	srv, _, _ := probedServer(t)
	p := &runLoopProber{srv: srv, metrics: newRunLoopProbeMetrics(), deadline: time.Hour}

	release := make(chan struct{})
	occupied := make(chan struct{})
	go srv.do(func() { close(occupied); <-release })
	<-occupied
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	p.probeOnce(ctx)
	close(release)

	if got := counterValue(t, p.metrics.timeouts); got != 0 {
		t.Errorf("timeouts = %v for a probe the caller abandoned, want 0", got)
	}
}

// TestNoProbeRunsWithoutMetrics: with the exposition off nothing would read what the
// probe measures, so it does not hold the writer at all.
func TestNoProbeRunsWithoutMetrics(t *testing.T) {
	srv, _ := newOffLoopServer(t, WithoutMetrics())
	if srv.probeStats != nil {
		t.Error("a server without metrics built the probe's exposition")
	}
}

// TestReferenceComputeIsFixedWork: the compute part must do identical work on every run,
// or a change in its duration would mean nothing.
func TestReferenceComputeIsFixedWork(t *testing.T) {
	if a, b := referenceCompute(), referenceCompute(); a != b {
		t.Errorf("referenceCompute = %x then %x, want the same result every time", a, b)
	}
	if allocs := testing.AllocsPerRun(20, func() { _ = referenceCompute() }); allocs != 0 {
		t.Errorf("referenceCompute allocates %.0f times, want 0", allocs)
	}
}

// counterValue reads a counter's current value.
func counterValue(t *testing.T, c prometheus.Counter) float64 {
	t.Helper()
	var m dto.Metric
	if err := c.Write(&m); err != nil {
		t.Fatalf("read counter: %v", err)
	}
	return m.GetCounter().GetValue()
}

// histogramCount reads how many observations a histogram holds.
func histogramCount(t *testing.T, h prometheus.Histogram) uint64 {
	t.Helper()
	var m dto.Metric
	if err := h.Write(&m); err != nil {
		t.Fatalf("read histogram: %v", err)
	}
	return m.GetHistogram().GetSampleCount()
}
