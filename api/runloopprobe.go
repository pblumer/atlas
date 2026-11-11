package api

import (
	"context"
	"fmt"
	"time"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/pblumer/atlas/metrics"
)

// The run-loop reference probe (ADR-draft-runloop-reference-probe).
//
// The turn histograms (runLoopMetrics) describe the traffic the server carries. When they
// rise, the cause may be more load, a different mix of work, or a writer that has become
// slower at the same work, and those histograms cannot tell the three apart. They are
// also empty while nobody dispatches. The probe closes that gap with a measurement that
// does not depend on traffic: on a fixed cadence it hands the run loop the same fixed
// work and records three things.
//
//   - wait: how long the probe queued before the writer took it. This is the delay any
//     request would have experienced at that moment, sampled at a steady rate rather
//     than only when somebody happened to call.
//   - work{part="store"}: a fixed set of point reads of the state store
//     (state.Store.ReferenceRead). Its duration grows when the store becomes slower to
//     read, for instance with a backlog of compactions or a slower volume.
//   - work{part="compute"}: a fixed computation over a fixed buffer. Its duration grows
//     when the writer's goroutine gets less CPU than it did: CPU throttling under a
//     container quota, a noisy neighbour, garbage-collection assists.
//
// The two work parts are kept apart because together they locate a slowdown: if both
// rise the host is the likelier cause, if only the store part rises it is the store or
// the disk under it.
//
// The probe reads nothing and writes nothing a caller can observe, produces no event
// (invariant I6) and allocates nothing on the engine's batch path (I1). It does hold the
// single writer while it runs (I3), which is why the work is some tens of microseconds
// every few seconds (measured on a small store: about 5µs for the store part and 14µs
// for the compute part). It runs only when the server exports metrics, because without the
// registry nothing would read what it measures.

const (
	// runLoopProbeInterval is the probe's cadence. Five seconds gives a 15-second scrape
	// three samples, and costs the writer well under a thousandth of its time.
	runLoopProbeInterval = 5 * time.Second
	// runLoopProbeDeadline is how long one probe may take, queueing included, before it
	// counts as a timeout. It sits inside the wait histogram's top bucket so that a wait
	// that did complete is never cut off below the bucket it belongs to.
	runLoopProbeDeadline = 10 * time.Second
	// referenceComputeBytes is the size of the buffer the compute part hashes: about 14µs
	// on a current CPU.
	referenceComputeBytes = 64 << 10
)

// referenceComputeBlock is the fixed input of the compute part. Its contents are fixed
// too, so that every run does identical work.
var referenceComputeBlock = func() []byte {
	b := make([]byte, referenceComputeBytes)
	for i := range b {
		b[i] = byte(i*131 + 7)
	}
	return b
}()

// referenceCompute is the compute part: FNV-1a over the fixed block. A byte-at-a-time
// hash has a dependency between every step, so it cannot be vectorised away and its
// duration follows the CPU time the goroutine actually receives.
func referenceCompute() uint64 {
	const (
		offset = 14695981039346656037
		prime  = 1099511628211
	)
	h := uint64(offset)
	for _, c := range referenceComputeBlock {
		h ^= uint64(c)
		h *= prime
	}
	return h
}

// runLoopProbeMetrics is the probe's exposition. The work histogram carries one label,
// part, whose values are a closed set fixed here, so ADR-0142's cardinality rule holds;
// both observers are resolved at construction so a probe does no label lookup.
type runLoopProbeMetrics struct {
	wait     prometheus.Histogram
	work     *prometheus.HistogramVec
	store    prometheus.Observer
	compute  prometheus.Observer
	timeouts prometheus.Counter
	failures prometheus.Counter
}

func newRunLoopProbeMetrics() *runLoopProbeMetrics {
	work := prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Namespace: metrics.Namespace, Name: "runloop_probe_work_seconds",
		Help: "How long the reference probe's fixed work took on the single writer, by part: " +
			"store (fixed point reads of the state store) or compute (a fixed computation).",
		// 1µs to ~1s. The healthy value is a few microseconds, so the bottom has to
		// resolve it; the top reaches a read stalled on a failing volume.
		Buckets: prometheus.ExponentialBuckets(0.000001, 2, 21),
	}, []string{"part"})
	return &runLoopProbeMetrics{
		wait: prometheus.NewHistogram(prometheus.HistogramOpts{
			Namespace: metrics.Namespace, Name: "runloop_probe_wait_seconds",
			Help: "How long the reference probe queued before the single writer took it, sampled on a fixed cadence " +
				"whether or not anybody else is calling.",
			// 1µs to ~16.8s: finer at the bottom than the turn histograms, because an
			// idle writer takes a probe within microseconds, and past the deadline.
			Buckets: prometheus.ExponentialBuckets(0.000001, 2, 25),
		}),
		work:    work,
		store:   work.WithLabelValues("store"),
		compute: work.WithLabelValues("compute"),
		timeouts: prometheus.NewCounter(prometheus.CounterOpts{
			Namespace: metrics.Namespace, Name: "runloop_probe_timeouts_total",
			Help: "Reference probes the single writer did not take and finish within ten seconds.",
		}),
		failures: prometheus.NewCounter(prometheus.CounterOpts{
			Namespace: metrics.Namespace, Name: "runloop_probe_failures_total",
			Help: "Reference probes whose store read failed; their store duration is not observed.",
		}),
	}
}

func (m *runLoopProbeMetrics) collectors() []prometheus.Collector {
	return []prometheus.Collector{m.wait, m.work, m.timeouts, m.failures}
}

// runLoopProber dispatches the reference probe. sink receives the compute part's result
// so the compiler cannot discard the computation; it is written only on the run loop.
type runLoopProber struct {
	srv     *Server
	metrics *runLoopProbeMetrics
	// deadline is runLoopProbeDeadline unless a test shortened it.
	deadline time.Duration
	sink     uint64
}

// probeOnce runs one probe and records it. The work parts are observed on the loop,
// where they ran, so that a part that finishes after its probe timed out is still
// reported: a store read that took thirty seconds is the observation most worth having.
// The wait is observed here, once the probe is known to have finished.
func (p *runLoopProber) probeOnce(ctx context.Context) {
	waited, ok := p.srv.runLoop.Probe(ctx, p.deadline, p.work)
	if !ok {
		if ctx.Err() == nil {
			p.metrics.timeouts.Inc()
		}
		return
	}
	p.metrics.wait.Observe(waited.Seconds())
}

// work is the probe's fixed work. It runs on the run loop.
func (p *runLoopProber) work() {
	start := time.Now()
	err := p.referenceRead()
	stored := time.Now()
	if err != nil {
		p.metrics.failures.Inc()
	} else {
		p.metrics.store.Observe(stored.Sub(start).Seconds())
	}
	p.sink = referenceCompute()
	p.metrics.compute.Observe(time.Since(stored).Seconds())
}

// referenceRead is the store part. The recover has the same reason as storeReadable's:
// Pebble panics rather than erring when it is used after Close, and a panic here would
// be a panic on the single writer. It is turned into a failure and nothing else.
func (p *runLoopProber) referenceRead() (err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("%v", r)
		}
	}()
	return p.srv.store.ReferenceRead()
}

// runLoopProbe runs the prober until the server stops. ticks replaces the real ticker in
// tests (withRunLoopProbeTrigger); ticked, when set, is signalled after each probe.
//
// A ticker drops ticks its reader is too slow for, so a probe stuck behind a wedged
// writer does not queue further probes behind itself: the next one starts when the
// stuck one has given up.
func (s *Server) runLoopProbe(p *runLoopProber) {
	defer s.wg.Done()
	ticks := s.probeTicks
	if ticks == nil {
		t := time.NewTicker(runLoopProbeInterval)
		defer t.Stop()
		ticks = t.C
	}
	// The context ends with the server, so a probe waiting for a writer that is shutting
	// down gives up at once rather than at its deadline, and Close is not held up by it.
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() {
		select {
		case <-s.quit:
			cancel()
		case <-ctx.Done():
		}
	}()
	for {
		select {
		case <-s.quit:
			return
		case <-ticks:
			p.probeOnce(ctx)
			if s.probeTicked != nil {
				select {
				case s.probeTicked <- struct{}{}:
				case <-s.quit:
					return
				}
			}
		}
	}
}

// withRunLoopProbeTrigger replaces the probe's ticker with an explicit tick channel and,
// optionally, a channel signalled after each probe. A test seam, not an operator knob:
// a test drives probes deterministically, with no wall-clock cadence. Mirrors
// withExporterTrigger.
func withRunLoopProbeTrigger(ticks <-chan time.Time, ticked chan struct{}) Option {
	return func(s *Server) {
		s.probeTicks = ticks
		s.probeTicked = ticked
	}
}
