package engine

import "errors"

// ErrHalted is what every run reports once a batch could not be made durable: the
// engine processes nothing more until the process is restarted (ADR-0481).
//
// The reason is the queue, not the disk. A failed batch leaves its commands queued in
// memory, and their callers have already been told they failed. Running them later —
// once space is back — would contradict that answer: an instance whose creation was
// refused would be created anyway, and a client that retried would have it twice.
// After a failure only a restart knows exactly what is durable, because recovery reads
// it from the log, so a restart is where the engine resumes. Reads go on meanwhile:
// the state store holds every batch that committed, and nothing past it.
var ErrHalted = errors.New("engine halted: a batch could not be made durable, so nothing more is processed until the server is restarted")

// haltedError carries the failure that halted the engine. It matches both ErrHalted
// and the underlying cause, so a caller can tell a halt from any other refusal and
// still read why the disk said no.
type haltedError struct{ cause error }

func (e *haltedError) Error() string   { return ErrHalted.Error() + ": " + e.cause.Error() }
func (e *haltedError) Unwrap() []error { return []error{ErrHalted, e.cause} }

// Halted reports why the engine stopped processing, or nil while it runs. It is the
// one Processor method safe to call from any goroutine: the HTTP layer and the
// storage monitor ask it from outside the run loop to refuse writes up front and to
// say on the dashboard that a restart is needed.
func (p *Processor) Halted() error {
	if h := p.halted.Load(); h != nil {
		return h
	}
	return nil
}

// halt records the failure that stops the engine, keeping the first one: it is the
// cause, and whatever fails after it is a consequence.
func (p *Processor) halt(cause error) error {
	p.halted.CompareAndSwap(nil, &haltedError{cause: cause})
	return p.halted.Load()
}
