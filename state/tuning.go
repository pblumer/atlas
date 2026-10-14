package state

import (
	"runtime"

	"github.com/cockroachdb/pebble"
)

// Tuning is the store-engine configuration a [Store] was opened with, as the caller
// asked for it. Zero in the two memory fields means "Pebble's own default".
//
// It is readable so the translation into Pebble's options can be held by a test. A
// mis-set value here does not fail, it just does not help — which is the kind of
// defect that survives for a long time.
type Tuning struct {
	// BlockCacheBytes is the shared block cache. Pebble's default is 8 MB, which is a
	// reasonable size for an embedded store of a few thousand keys and far too small
	// for one holding millions: below it, a scan that should be served from memory goes
	// to disk, and — worse — evicts whatever else was cached on the way through.
	BlockCacheBytes int64
	// MemtableBytes is the write buffer. Pebble's default is 4 MB, and it stops writes
	// once MemTableStopWritesThreshold (2) of them are unflushed, so 8 MB of unflushed
	// writes is all the headroom a default store has. A stalled write stalls the run
	// loop that issued it, and with it every request in the API.
	MemtableBytes int64
	// MaxConcurrentCompactions is how many compactions may run at once. Pebble's
	// default is one, which is the setting that turns a busy engine into a write stall:
	// a single compaction goroutine cannot keep L0 below L0StopWritesThreshold (12)
	// under sustained writes, and once it is exceeded Pebble stops writes outright.
	MaxConcurrentCompactions int
}

// Option configures a [Store] at open time.
type Option func(*Tuning)

// WithBlockCacheBytes sets the Pebble block cache. A value at or below zero leaves
// Pebble's default, so a caller can pass an unset flag straight through.
//
// It is resident memory for the life of the process and it is **per store**, so it
// belongs to the one long-lived store a server owns rather than to every store a
// process opens — the Playground holds one engine per session (ADR-0176), and a cache
// applied by default would be multiplied by those.
func WithBlockCacheBytes(n int64) Option {
	return func(t *Tuning) {
		if n > 0 {
			t.BlockCacheBytes = n
		}
	}
}

// WithMemtableBytes sets the Pebble write buffer. A value at or below zero leaves
// Pebble's default.
//
// The trade-off it buys is real and worth stating: a larger write buffer means more
// committed state sitting in memory, so after a crash the store trails the log further
// and recovery replays a longer suffix. That costs **recovery time only** — durability
// is the WAL's fsync, never the store's (ADR-0005, invariant I2) — and the ADR-0131
// checkpoint cadence bounds how long that suffix can get.
func WithMemtableBytes(n int64) Option {
	return func(t *Tuning) {
		if n > 0 {
			t.MemtableBytes = n
		}
	}
}

// WithMaxConcurrentCompactions sets how many compactions may run at once. A value
// below one leaves the package default.
func WithMaxConcurrentCompactions(n int) Option {
	return func(t *Tuning) {
		if n >= 1 {
			t.MaxConcurrentCompactions = n
		}
	}
}

// defaultTuning is what a store gets when the caller says nothing.
//
// Only the compaction concurrency is raised off Pebble's default, because it is the
// one that costs CPU rather than resident memory: an idle store starts no compactions,
// so every store in the process can afford the headroom, while a cache or a write
// buffer would be paid for by all of them whether or not they are busy. Those two are
// opt-in, and the server opts in for its own store.
func defaultTuning() Tuning {
	return Tuning{MaxConcurrentCompactions: max(2, runtime.NumCPU()/2)}
}

// pebbleOptions translates the tuning into the options Pebble is opened with, plus the
// cache the caller must release once Pebble has taken its own reference (nil when none
// was configured). Fields left at zero are simply not set, so Pebble applies its own
// default.
//
// The cache is reference-counted and [pebble.Open] takes its reference *during* the
// open, so the one [pebble.NewCache] returns has to outlive this call — releasing it
// here would leave Pebble opening against a cache at refcount zero, which it rejects.
func (t Tuning) pebbleOptions() (*pebble.Options, *pebble.Cache) {
	opts := &pebble.Options{Merger: counterMerger}
	var cache *pebble.Cache
	if t.BlockCacheBytes > 0 {
		cache = pebble.NewCache(t.BlockCacheBytes)
		opts.Cache = cache
	}
	if t.MemtableBytes > 0 {
		opts.MemTableSize = uint64(t.MemtableBytes)
	}
	if t.MaxConcurrentCompactions >= 1 {
		n := t.MaxConcurrentCompactions
		opts.MaxConcurrentCompactions = func() int { return n }
	}
	return opts, cache
}

// Tuning reports the configuration this store was opened with.
func (s *Store) Tuning() Tuning { return s.tuning }
