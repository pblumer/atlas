package engine

// Batch instrumentation (ADR-0142). Some numbers cannot be recovered from state —
// nothing on disk says how many batches ran, how large they were, or how long their
// fsyncs took — so unlike the durability gauges, which are read from disk when
// Prometheus scrapes, these have to be pushed from the batch loop.
//
// That places them squarely on the hot path, under two rules:
//
//   - **Durable before visible (I2).** A batch is reported *after* its state commit,
//     never before. A counter that ran ahead of the log would claim work a crash then
//     loses — the one direction a throughput metric must not err in. A batch that fails
//     to become durable is reported as a failure and is not counted as committed.
//   - **No allocation (I1).** Reporting is one interface call per *batch* — not per
//     command — passing a plain struct by value. Nothing is boxed, nothing is retained,
//     and engine/alloc_test.go asserts it stays that way.
//
// The engine deliberately does not know about Prometheus: it hands out plain numbers,
// and the server maps them onto pre-resolved metric handles. That keeps the exposition
// format, the registry and the naming conventions out of the partition's writer.

// BatchStats is what one durably committed batch did. It is passed by value and never
// retained by the engine.
//
// SyncSeconds and CommitSeconds are zero for a batch that produced no events: there was
// nothing to fsync and nothing to commit, so an implementation should record no
// observation rather than a zero one, which would drag a latency histogram down.
type BatchStats struct {
	// Commands is how many queued commands the batch consumed.
	Commands int
	// Events is how many events it made durable — zero for a batch whose commands
	// produced nothing to write.
	Events int
	// QueueDepth is how many commands remain queued after the batch, including the
	// follow-ups it scheduled. It is the backpressure signal: a depth that climbs
	// batch over batch means the writer is not keeping up with what is arriving.
	QueueDepth int
	// SyncSeconds is how long the batch's single group-commit fsync took.
	SyncSeconds float64
	// CommitSeconds is how long making the batch's state visible took.
	CommitSeconds float64
}

// Metrics observes the batch loop. Every method is called from the single-writer
// goroutine (invariant I3), so an implementation must not block, must not call back into
// the processor, and must not allocate (invariant I1).
//
// A nil Metrics — the default — means the engine reports nothing and does not even read
// the clock, so an uninstrumented processor pays literally nothing.
type Metrics interface {
	// BatchCommitted reports a batch whose events are durable and whose state is
	// committed. It is never called for a batch that failed.
	BatchCommitted(BatchStats)
	// SyncFailed reports a batch whose group-commit fsync failed. Nothing it wrote is
	// durable.
	SyncFailed()
	// CommitFailed reports a batch whose events are durable but whose state commit
	// failed. Recovery will re-apply them from the log.
	CommitFailed()
}

// SetMetrics attaches batch instrumentation, or detaches it with nil. Call it before the
// processor starts handling commands; like SetJobNotifier it is not safe to change while
// batches are running.
func (p *Processor) SetMetrics(m Metrics) { p.metrics = m }
