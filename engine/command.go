package engine

import (
	"time"

	"github.com/pblumer/atlas/model"
)

// maxBatchSize caps how many commands one batch folds before flushing. Under
// load batches fill to this; when idle they are as small as one command
// (ADR-0005 group commit).
const maxBatchSize = 1024

// Command is an intention handed to the processor. Commands are processed but
// never persisted (only the events they produce are); on recovery they are not
// replayed (invariant I6). The payload is held by value (see inflightValue) so
// queuing a command does not allocate.
type Command struct {
	Key       uint64
	ValueType model.ValueType
	Intent    model.Intent
	Value     inflightValue
	// SourcePos is the log position of the event that scheduled this command
	// (0 for externally submitted commands), used to thread causality into the
	// events the command produces.
	SourcePos uint64
	// Vars carries variables into the processor from outside: those seeded when
	// an instance is created and those a job completion writes back. It is nil for
	// internally generated followup commands, so the control-flow hot path never
	// allocates for it (invariant I1).
	Vars []model.NamedVariable
}

// sideEffect is work to run after the batch's fsync (invariant I2). It is a
// typed value, not a closure, so registering one does not allocate.
type sideEffect struct {
	jobType int32 // notify that a job of this type became available
}

// Clock supplies wall-clock time. It is injected so tests can drive time
// deterministically (invariant I4: time is read into events, never inside
// applyToState).
type Clock interface {
	Now() int64 // unix nanoseconds
}

// SystemClock reads the host clock.
type SystemClock struct{}

func (SystemClock) Now() int64 { return time.Now().UnixNano() }

// keyGen mints globally unique keys for a partition. The counter is restored on
// recovery to the highest key seen so live keys never collide with replayed
// ones; keys themselves are frozen into events and read back on replay, never
// regenerated (invariant I6).
type keyGen struct {
	partition uint16
	counter   uint64
}

func (k *keyGen) next() uint64 {
	k.counter++
	return model.NewKey(k.partition, k.counter)
}
