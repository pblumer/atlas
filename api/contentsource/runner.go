package contentsource

import (
	"context"
	"errors"
	"log/slog"
	"slices"
	"sync"
	"time"

	"github.com/pblumer/atlas/logging"
)

// The recheck (§ 9): a goroutine per source, outside the engine and outside the
// run loop, that asks the source whether what Atlas copied may still be shown.
//
// Its network I/O happens here and nowhere near the processor; what it decides is
// handed to the catalogue through [Bound], which writes it through the run loop
// like any other design-time change. Nothing it does is an event, so nothing of it
// is replayed, and a restart begins with a sweep rather than with a recovery.
//
// Three things trigger a recheck: the source's change feed reporting a watched
// page or ancestor (at once), a full sweep every [RecheckInterval] (the fallback
// for a delivery that never came), and every refresh (which the catalogue does
// itself, before it fetches). There is no tolerance: a source the recheck cannot
// reach quarantines everything of it.

// RecheckInterval is the sweep's period, and the age a passed recheck may reach
// before publishing refuses (§ 9).
const RecheckInterval = 15 * time.Minute

// Target is one bound product and every page it holds content of.
type Target struct {
	Item   string
	Checks []Check
}

// Result is one product's recheck.
type Result struct {
	Item   string
	At     int64
	Passed bool
	Reason string
}

// Bound is the catalogue, as the recheck sees it. Every method is called off the
// run loop and makes its own visit to it.
type Bound interface {
	// Referenced names every source a binding names, configured or not: a
	// binding to a source that was removed is rechecked too, and fails.
	Referenced() ([]string, error)
	// Targets lists the products bound to a source.
	Targets(source string) ([]Target, error)
	// Record writes what a recheck found.
	Record(source string, results []Result) error
}

// Runner owns the recheck goroutines.
type Runner struct {
	reg   *Registry
	bound Bound
	now   func() int64

	// Interval is the sweep's period; Wait is how long one long poll of the feed
	// is held; Backoff is the pause after the feed failed. Settable for tests.
	Interval time.Duration
	Wait     time.Duration
	Backoff  time.Duration

	mu      sync.Mutex
	running map[string]context.CancelFunc
	follows map[string]*follow
	wake    chan struct{}
	wg      sync.WaitGroup
}

// follow is one source's recheck state: which ids it watches and which products
// each id belongs to, so a change names the products to recheck.
type follow struct {
	mu          sync.Mutex
	index       map[string][]string
	watched     map[string]bool
	unreachable bool
}

// NewRunner builds the runner. now is the server's clock, in Unix nanoseconds.
func NewRunner(reg *Registry, bound Bound, now func() int64) *Runner {
	return &Runner{reg: reg, bound: bound, now: now,
		Interval: RecheckInterval, Wait: 25 * time.Second, Backoff: 5 * time.Second,
		running: map[string]context.CancelFunc{}, follows: map[string]*follow{},
		wake: make(chan struct{}, 1)}
}

// Reload asks the runner to look at the configured sources again now.
func (r *Runner) Reload() {
	select {
	case r.wake <- struct{}{}:
	default:
	}
}

// Kick rechecks a source now, in the background — after an unlink, so the
// watches nothing needs any more end without waiting for the next sweep.
func (r *Runner) Kick(source string) {
	r.wg.Add(1)
	go func() {
		defer r.wg.Done()
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()
		_ = r.Sweep(ctx, source)
	}()
}

// Run keeps one goroutine per source until quit closes, then stops them all and
// waits for them.
func (r *Runner) Run(quit <-chan struct{}) {
	t := time.NewTicker(r.Interval)
	defer t.Stop()
	for {
		r.sync()
		select {
		case <-quit:
			r.mu.Lock()
			for _, cancel := range r.running {
				cancel()
			}
			r.mu.Unlock()
			r.wg.Wait()
			return
		case <-r.wake:
		case <-t.C:
		}
	}
}

// sync starts a goroutine for every source that is configured or named by a
// binding, and stops the ones for sources that are neither any more.
func (r *Runner) sync() {
	want := map[string]bool{}
	if all, err := r.reg.Sources(); err == nil {
		for _, s := range all {
			want[s.ID] = true
		}
	}
	if refs, err := r.bound.Referenced(); err == nil {
		for _, id := range refs {
			want[id] = true
		}
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	for id, cancel := range r.running {
		if !want[id] {
			cancel()
			delete(r.running, id)
		}
	}
	for id := range want {
		if _, ok := r.running[id]; ok {
			continue
		}
		ctx, cancel := context.WithCancel(context.Background())
		r.running[id] = cancel
		r.wg.Add(1)
		go func() {
			defer r.wg.Done()
			r.followSource(ctx, id)
		}()
	}
}

func (r *Runner) state(source string) *follow {
	r.mu.Lock()
	defer r.mu.Unlock()
	f, ok := r.follows[source]
	if !ok {
		f = &follow{index: map[string][]string{}, watched: map[string]bool{}}
		r.follows[source] = f
	}
	return f
}

// followSource is one source's goroutine: a sweep, then the feed, with a sweep
// again whenever the interval has passed or the feed failed.
func (r *Runner) followSource(ctx context.Context, source string) {
	// The feed's position is read before the sweep, never after: a change between
	// the two is then in the feed, where a position read afterwards would have
	// skipped it, and a page the sweep found clean could be restricted unnoticed
	// until the next sweep.
	cursor, haveCursor := r.head(ctx, source)
	swept := time.Now()
	_ = r.Sweep(ctx, source)
	backoff := r.Backoff
	for ctx.Err() == nil {
		if !haveCursor {
			if cursor, haveCursor = r.head(ctx, source); haveCursor {
				swept = time.Now()
				_ = r.Sweep(ctx, source)
			}
		}
		if haveCursor && time.Since(swept) >= r.Interval {
			swept = time.Now()
			_ = r.Sweep(ctx, source)
		}
		var (
			batch Changes
			err   = errNoCursor
		)
		if haveCursor {
			var ad Adapter
			if _, ad, err = r.reg.Open(source); err == nil {
				batch, err = ad.Changes(ctx, cursor, r.Wait)
			}
		}
		if ctx.Err() != nil {
			return
		}
		if err != nil {
			// The feed failing is the source failing to answer. The sweep is
			// what says so to the catalogue: it cannot reach the source either,
			// and quarantines. Then wait, and start again from a fresh position —
			// what happened meanwhile is covered by the sweep after it.
			r.unreachable(source, err)
			swept = time.Now()
			_ = r.Sweep(ctx, source)
			haveCursor = false
			r.pause(ctx, backoff)
			// Longer each time, up to a minute: a source that is down is
			// quarantined by the first failed sweep, and asking it every few
			// seconds after that only writes the same finding again.
			backoff = min(2*backoff, time.Minute)
			continue
		}
		backoff = r.Backoff
		cursor = batch.Last
		if len(batch.Changes) == 0 {
			continue
		}
		all := false
		var items []string
		f := r.state(source)
		f.mu.Lock()
		for _, c := range batch.Changes {
			if c.Item == "" {
				// A watched page the account can no longer read, or the
				// account's own roles: nothing is named, so everything is
				// rechecked.
				all = true
				break
			}
			for _, it := range f.index[c.Item] {
				if !slices.Contains(items, it) {
					items = append(items, it)
				}
			}
		}
		f.mu.Unlock()
		switch {
		case all:
			swept = time.Now()
			_ = r.Sweep(ctx, source)
		case len(items) > 0:
			_ = r.RecheckItems(ctx, source, items)
		}
	}
}

// errNoCursor is a feed whose position could not be read.
var errNoCursor = errors.New("the change feed's position could not be read")

// head reads the feed's position, or reports it could not.
func (r *Runner) head(ctx context.Context, source string) (int64, bool) {
	_, ad, err := r.reg.Open(source)
	if err != nil {
		return 0, false
	}
	cursor, err := ad.Head(ctx)
	return cursor, err == nil
}

func (r *Runner) pause(ctx context.Context, d time.Duration) {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
	case <-t.C:
	}
}

func (r *Runner) unreachable(source string, err error) {
	f := r.state(source)
	f.mu.Lock()
	first := !f.unreachable
	f.unreachable = true
	f.mu.Unlock()
	if first {
		logging.Warn(logging.ContentSourceUnreachable, "a content source cannot be reached; its bound products are quarantined",
			slog.String("source", source), slog.String("error", err.Error()))
	}
}

func (r *Runner) reachable(source string) {
	f := r.state(source)
	f.mu.Lock()
	was := f.unreachable
	f.unreachable = false
	f.mu.Unlock()
	if was {
		logging.Info(logging.ContentSourceRecovered, "a content source answers again; quarantines stay until a refresh",
			slog.String("source", source))
	}
}

// Sweep rechecks every product bound to a source.
func (r *Runner) Sweep(ctx context.Context, source string) error {
	return r.recheck(ctx, source, nil)
}

// RecheckItems rechecks the named products of a source.
func (r *Runner) RecheckItems(ctx context.Context, source string, items []string) error {
	if len(items) == 0 {
		return nil
	}
	return r.recheck(ctx, source, items)
}

// recheck is one pass: the products (all when only is nil), the source's
// standing read once, every page through one cache, and the findings recorded
// in one visit to the loop. Every product is answered — passed or failed — and
// none is left as it was: a recheck that could not run is a recheck that failed.
func (r *Runner) recheck(ctx context.Context, source string, only []string) error {
	targets, err := r.bound.Targets(source)
	if err != nil {
		return err
	}
	if only != nil {
		targets = slices.DeleteFunc(targets, func(t Target) bool { return !slices.Contains(only, t.Item) })
	}
	at := r.now()
	results := make([]Result, 0, len(targets))
	failAll := func(reason string) error {
		for _, t := range targets {
			results = append(results, Result{Item: t.Item, At: at, Reason: reason})
		}
		return r.bound.Record(source, results)
	}
	if len(targets) == 0 {
		if only == nil {
			r.forget(ctx, source)
		}
		return nil
	}
	_, ad, err := r.reg.Open(source)
	if err != nil {
		if errors.Is(err, ErrNoSource) {
			return failAll("the content source " + source + " is no longer configured")
		}
		r.unreachable(source, err)
		return failAll("the content source could not be opened: " + err.Error())
	}
	st, err := ad.Standing(ctx)
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		r.unreachable(source, err)
		return failAll("the content source cannot be reached: " + err.Error())
	}
	if why := CheckStanding(st); len(why) > 0 {
		return failAll("the account's standing at the source is wrong: " + why[0])
	}
	c := newPages(ad)
	index := map[string][]string{}
	for _, t := range targets {
		reasons, touched, err := recheck(ctx, c, st, t.Checks)
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			r.unreachable(source, err)
			return failAll("the content source cannot be reached: " + err.Error())
		}
		for _, id := range touched {
			index[id] = append(index[id], t.Item)
		}
		res := Result{Item: t.Item, At: at, Passed: len(reasons) == 0}
		if !res.Passed {
			res.Reason = reasons[0]
		}
		results = append(results, res)
	}
	r.reachable(source)
	if err := r.bound.Record(source, results); err != nil {
		return err
	}
	r.watch(ctx, source, ad, index, only == nil)
	return nil
}

// watch puts a watch on every id a recheck read that is not watched yet, and —
// after a full sweep, which knows the whole set — ends the watches nothing needs
// any more, by id (its ADR-0120). Only item watches: a space watch never reports
// a page the account can no longer read, which is the one report that matters.
func (r *Runner) watch(ctx context.Context, source string, ad Adapter, index map[string][]string, whole bool) {
	f := r.state(source)
	f.mu.Lock()
	for id, items := range index {
		f.index[id] = mergeItems(f.index[id], items)
	}
	var add, drop []string
	for id := range index {
		if !f.watched[id] {
			add = append(add, id)
		}
	}
	if whole {
		for id := range f.watched {
			if _, still := index[id]; !still {
				drop = append(drop, id)
			}
		}
		f.index = index
	}
	f.mu.Unlock()
	slices.Sort(add)
	slices.Sort(drop)
	for _, id := range add {
		if err := ad.Watch(ctx, id); err != nil {
			logging.Warn(logging.ContentWatchFailed, "the content source refused to watch a bound page",
				slog.String("source", source), slog.String("page", id), slog.String("error", err.Error()))
			continue
		}
		f.mu.Lock()
		f.watched[id] = true
		f.mu.Unlock()
	}
	for _, id := range drop {
		if err := ad.Unwatch(ctx, id); err == nil {
			f.mu.Lock()
			delete(f.watched, id)
			f.mu.Unlock()
		}
	}
}

// forget ends every watch of a source nothing is bound to any more.
func (r *Runner) forget(ctx context.Context, source string) {
	f := r.state(source)
	f.mu.Lock()
	n := len(f.watched)
	f.mu.Unlock()
	if n == 0 {
		return
	}
	if _, ad, err := r.reg.Open(source); err == nil {
		r.watch(ctx, source, ad, map[string][]string{}, true)
	}
}

func mergeItems(a, b []string) []string {
	for _, it := range b {
		if !slices.Contains(a, it) {
			a = append(a, it)
		}
	}
	return a
}

// Watched reports the ids the runner watches at a source, sorted — for tests and
// for the status of a source.
func (r *Runner) Watched(source string) []string {
	f := r.state(source)
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]string, 0, len(f.watched))
	for id := range f.watched {
		out = append(out, id)
	}
	slices.Sort(out)
	return out
}
