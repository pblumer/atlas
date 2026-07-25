// Package bench is the performance-test harness for Atlas.
//
// It measures the engine along the axes an event-sourced workflow engine is
// actually judged on — and it is built to be *honest* about them:
//
//   - Throughput is measured through the real group-commit path (many commands
//     folded into one fsync), because driving one instance per fsync would
//     measure fsync latency, not throughput.
//   - Durability is never faked. The write-ahead log fsyncs once per batch
//     (invariant I2); the storage medium is reported by scripts/bench.sh, since
//     an fsync on tmpfs is nearly free and a throughput number without its
//     medium is meaningless.
//   - The catalogue (package scenarios) exercises gateways, expressions and a
//     realistic mix, not just a linear happy path.
//   - Recovery — "state after replay == state built live" — is measured
//     explicitly by replaying a populated log into an empty store.
//
// The suite's purpose is regression tracking over time: run it, capture the
// output with scripts/bench.sh, and compare with benchstat against a checked-in
// baseline. See bench/README.md for the method and the honesty rules.
package bench

import (
	"errors"
	"fmt"
	"path/filepath"

	"github.com/pblumer/atlas/compiler"
	"github.com/pblumer/atlas/engine"
	"github.com/pblumer/atlas/model"
	"github.com/pblumer/atlas/state"
	"github.com/pblumer/atlas/wal"
)

// monotonicClock is a deterministic clock: the suite must not depend on
// wall-clock time (AGENTS.md testing conventions). It advances one tick per read
// so successive events get distinct, ordered timestamps.
type monotonicClock struct{ t int64 }

func (c *monotonicClock) Now() int64 { c.t++; return c.t }

// Engine bundles an open write-ahead log, state store and processor over one
// temp directory — the smallest real, durable Atlas engine a benchmark can
// drive. It is not safe for concurrent use; a single goroutine owns it, matching
// the single-writer invariant (I3).
type Engine struct {
	Proc  *engine.Processor
	Store *state.Store
	Log   *wal.Log
}

// open builds an engine with its log under logDir/wal and its state under
// storeDir/state, deploys cp if given, and runs recovery so it is ready for
// commands. A single cleanup path closes whatever opened if a later step fails,
// so no partially-opened engine leaks a handle. Open and Recover are thin
// wrappers over it.
func open(logDir, storeDir string, cp *compiler.CompiledProcess) (_ *Engine, err error) {
	log, err := wal.Open(wal.Options{Dir: filepath.Join(logDir, "wal")})
	if err != nil {
		return nil, fmt.Errorf("open wal: %w", err)
	}
	defer func() {
		if err != nil {
			_ = log.Close()
		}
	}()
	store, err := state.Open(filepath.Join(storeDir, "state"))
	if err != nil {
		return nil, fmt.Errorf("open state: %w", err)
	}
	defer func() {
		if err != nil {
			_ = store.Close()
		}
	}()
	p := engine.New(1, log, store, &monotonicClock{})
	if cp != nil {
		p.Deploy(cp)
	}
	if err = p.Recover(); err != nil {
		return nil, fmt.Errorf("recover: %w", err)
	}
	return &Engine{Proc: p, Store: store, Log: log}, nil
}

// Open builds a durable engine rooted at dir (dir/wal and dir/state) and runs
// recovery so it is ready to accept commands. The caller must Close it.
func Open(dir string) (*Engine, error) {
	return open(dir, dir, nil)
}

// Close releases the store and log, always attempting both so a failed store
// close does not leak the log handle, and reporting either error.
func (e *Engine) Close() error {
	return errors.Join(e.Store.Close(), e.Log.Close())
}

// enqueueWindow bounds how many instances RunInstances stages before draining
// them. It matches the engine's per-batch command cap so each window is about
// one group-commit's worth of work: large enough that the batch cycle folds many
// commands into one fsync (the honest throughput path, not one fsync per
// instance), small enough that a million-instance benchmark run stays bounded in
// memory instead of queuing every command up front.
const enqueueWindow = 1024

// RunInstances creates n instances of defKey and drives them — and any jobs they
// park on — to completion, in windows of enqueueWindow. Instances are staged in
// bulk and folded through the batch cycle together, so throughput reflects group
// commit rather than per-instance fsync latency. jobTypes are the service-task
// job types to complete; an empty slice means the scenario finishes with no
// external worker.
func (e *Engine) RunInstances(defKey uint64, jobTypes []int32, n int, startVars []model.VariableValue) error {
	for created := 0; created < n; {
		k := min(enqueueWindow, n-created)
		for range k {
			e.Proc.CreateInstance(defKey, startVars...)
		}
		if err := e.Proc.RunUntilIdle(); err != nil {
			return err
		}
		if err := e.drainJobs(jobTypes); err != nil {
			return err
		}
		created += k
	}
	return nil
}

// drainJobs completes every activatable job of the given types, in batches, until
// none remain — the worker side of an instance's lifecycle. Each round collects
// all activatable jobs, completes them together, then folds the completions
// through one more batch cycle. A scenario with no service task passes no job
// types and this returns immediately.
func (e *Engine) drainJobs(jobTypes []int32) error {
	for len(jobTypes) > 0 {
		var keys []uint64
		for _, jt := range jobTypes {
			if err := e.Store.ActivatableJobs(jt, func(k uint64) error {
				keys = append(keys, k)
				return nil
			}); err != nil {
				return err
			}
		}
		if len(keys) == 0 {
			return nil
		}
		for _, k := range keys {
			e.Proc.CompleteJob(k)
		}
		if err := e.Proc.RunUntilIdle(); err != nil {
			return err
		}
	}
	return nil
}

// BuildLog populates dir with the durable log and state of having run n
// instances of cp to completion, then closes the engine so the log is a
// self-contained artifact a fresh engine can recover from. It returns the number
// of durable events the recovery must replay (the log's final position), which
// the recovery benchmark reports as the work done per op.
func BuildLog(dir string, cp *compiler.CompiledProcess, jobTypes []int32, n int, startVars []model.VariableValue) (events uint64, err error) {
	e, err := open(dir, dir, cp)
	if err != nil {
		return 0, err
	}
	defer func() {
		if cerr := e.Close(); err == nil {
			err = cerr
		}
	}()
	if err = e.RunInstances(cp.Key, jobTypes, n, startVars); err != nil {
		return 0, err
	}
	return e.Store.LastAppliedPosition()
}

// Recover opens the log already written under logDir and replays it into a fresh,
// empty store at storeDir, returning the recovered engine. This is the cold
// restart path: state after replay must equal state built live (invariant I4).
// The recovery benchmark times this call; the caller Closes the result.
func Recover(logDir, storeDir string, cp *compiler.CompiledProcess) (*Engine, error) {
	return open(logDir, storeDir, cp)
}
