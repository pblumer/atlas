package ad

import (
	"sort"
	"strings"
	"sync"
	"time"
)

// Making a mock forest visible (ADR-0213).
//
// [MockDirectory] is memory, and deliberately so: a restart is an empty forest, and
// nothing it holds was ever real. That is the right lifetime and the wrong
// *visibility*. Until now the only window onto a mockup run was the worker's log —
// one line per operation, which answers "did the job reach a directory" and never
// "what is in the directory now". So the account a joiner created could be confirmed
// only by reading a log, and the seed file in the Console kept being mistaken for the
// directory itself, which it is not: it is where every forest starts, an input the
// worker reads once and never writes back.
//
// This file is the reporting half. A worker snapshots its own forests and posts them
// to the Atlas whose Console the operator is watching; that server keeps the newest
// snapshot per worker in a [MockView] and shows it. The direction is the preview
// outbox's, for the same reason (ADR-0150/0168): a worker may sit in a network the
// server cannot dial back into, so what crosses is a report the worker sends, never a
// request the server makes.
//
// **It stays a mock in every respect.** The snapshot is bounded, so a directory that
// grew past what a view can show says so instead of sending a heap into another
// process; it carries no password, because the mock stores none; and the server keeps
// it in memory only — no event, no log, nothing replayed (I4/I6). A restart on either
// side loses it, which is the honest lifetime for a picture of something that does
// not exist.

// MockEntry is one entry as a snapshot carries it. It is [Entry] with names a JSON
// reader would choose; the two are kept apart on purpose, so the wire shape of the
// view is not hostage to a type the connector uses internally.
type MockEntry struct {
	DN         string              `json:"dn"`
	Attributes map[string][]string `json:"attributes,omitempty"`
}

// MockForestSnapshot is one simulated directory: the LDAP URL a job dialled and what
// that directory holds now.
//
// Held is what the forest actually has, which is not len(Entries) once Truncated is
// set — the number is the point of the flag rather than a decoration on it.
type MockForestSnapshot struct {
	URL       string      `json:"url"`
	Entries   []MockEntry `json:"entries"`
	Held      int         `json:"held"`
	Truncated bool        `json:"truncated,omitempty"`
}

// MockSnapshot is one worker's whole mock directory at one moment: every forest it
// has been asked to reach, the seed they all start from, and the newest operations it
// performed.
//
// Worker names the reporter — the id the Workers view shows — so an operator running
// two mock workers can tell whose forest they are looking at. At is stamped by the
// [MockView] that accepted it, never by the reporter.
type MockSnapshot struct {
	Worker string `json:"worker,omitempty"`
	At     int64  `json:"at,omitempty"`
	// Seeded is how many starting entries every forest of this worker begins from.
	// It travels even when Forests is empty, which is the ordinary state of a mock
	// worker that has not leased a job yet: forests exist only once dialled, and
	// "mock mode is on, 12 starting entries, nothing dialled" is exactly what an
	// operator needs told at that moment.
	Seeded     int                  `json:"seeded"`
	Forests    []MockForestSnapshot `json:"forests"`
	Operations []MockOperation      `json:"operations,omitempty"`
}

// Snapshot is what this directory holds now, for a view somewhere else.
//
// maxEntries bounds the *whole* snapshot rather than each forest: the bound exists
// because the result crosses a network into another process's memory, and a limit
// applied per forest would multiply by however many directories a run happens to
// touch. 0 means unbounded, which is what a test and a small mockup want.
//
// It takes the lock once, so what it returns is one consistent moment across every
// forest — the two-directory run this mock exists to make honest would otherwise be
// reported half from before a write and half from after.
func (d *MockDirectory) Snapshot(maxEntries int) MockSnapshot {
	d.mu.Lock()
	defer d.mu.Unlock()
	// Forests is allocated even when empty: a JSON null and an empty list read the
	// same to a person and differently to every consumer, and "this worker has dialled
	// nothing yet" is an ordinary state here rather than an absence of information.
	snap := MockSnapshot{
		Seeded:     len(d.seed),
		Forests:    []MockForestSnapshot{},
		Operations: append([]MockOperation(nil), d.ops...),
	}
	left := maxEntries
	for _, url := range d.urls() {
		f := d.forests[strings.ToLower(strings.TrimSpace(url))]
		live := f.live()
		view := MockForestSnapshot{URL: f.url, Held: len(live)}
		if maxEntries > 0 && len(live) > left {
			live, view.Truncated = live[:max(left, 0)], true
		}
		view.Entries = make([]MockEntry, 0, len(live))
		for _, e := range live {
			view.Entries = append(view.Entries, MockEntry{DN: e.DN, Attributes: e.Attributes})
		}
		left -= len(view.Entries)
		snap.Forests = append(snap.Forests, view)
	}
	return snap
}

// Version is how many operations this directory has performed. A reporter that has
// already sent a version has nothing new to say, which is what keeps a worker leasing
// read-only jobs from posting the same forest over and over.
func (d *MockDirectory) Version() uint64 {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.opSeq
}

// maxMockViewWorkers bounds how many workers a view keeps. A supervised worker is
// restarted whenever its environment changes and comes back under the same id, so
// this is not usually reached; it is here because an operator running many external
// mock workers must not be able to grow a server's memory one dead directory at a
// time.
const maxMockViewWorkers = 8

// anonymousMockWorker files a report from a worker that did not name itself. An
// external worker is configured by hand and may have no id to send; dropping its
// report would answer "why is my mock forest not showing" with silence.
const anonymousMockWorker = "(unnamed worker)"

// MockView is the newest snapshot each mock worker reported, and the whole of what an
// Atlas keeps about mock directories.
//
// Like the preview outbox it holds its own lock rather than living on the run loop: a
// worker posts into it off the loop and after fsync (I2/I3), an HTTP read serves the
// Console from a request goroutine, and neither has any business waiting for the
// engine's single writer.
type MockView struct {
	mu      sync.Mutex
	cap     int
	byName  map[string]MockSnapshot
	nowFunc func() int64
}

// nowNanos is the view's default clock.
func nowNanos() int64 { return time.Now().UnixNano() }

// NewMockView returns a view holding at most capacity workers; a capacity below one
// falls back to the default.
func NewMockView(capacity int) *MockView { return NewMockViewClock(capacity, nil) }

// NewMockViewClock is [NewMockView] over an injected clock, so a test can assert on
// the arrival stamp without depending on wall time.
func NewMockViewClock(capacity int, now func() int64) *MockView {
	if capacity < 1 {
		capacity = maxMockViewWorkers
	}
	if now == nil {
		now = nowNanos
	}
	return &MockView{cap: capacity, byName: map[string]MockSnapshot{}, nowFunc: now}
}

// Put files one worker's report, replacing whatever that worker last said. A worker
// reports its whole directory every time, so replacing is the only correct merge:
// entries a leaver deleted must leave the view with them.
func (v *MockView) Put(s MockSnapshot) {
	name := strings.TrimSpace(s.Worker)
	if name == "" {
		name = anonymousMockWorker
	}
	v.mu.Lock()
	defer v.mu.Unlock()
	// The stamp is the view's, not the reporter's: a worker that could choose it could
	// make its own report look fresher than another's, and freshness is what decides
	// who is dropped below.
	s.Worker, s.At = name, v.nowFunc()
	v.byName[name] = s
	for len(v.byName) > v.cap {
		stalest, at := "", int64(0)
		for n, held := range v.byName {
			if stalest == "" || held.At < at {
				stalest, at = n, held.At
			}
		}
		delete(v.byName, stalest)
	}
}

// Snapshots returns every worker's newest report, sorted by worker name so a Console
// polling this does not reshuffle under the reader.
func (v *MockView) Snapshots() []MockSnapshot {
	v.mu.Lock()
	defer v.mu.Unlock()
	out := make([]MockSnapshot, 0, len(v.byName))
	for _, s := range v.byName {
		out = append(out, s)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Worker < out[j].Worker })
	return out
}
