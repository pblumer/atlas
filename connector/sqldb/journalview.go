package sqldb

import (
	"sort"
	"strings"
	"sync"
	"time"
)

// Making a mockup run visible (ADR-0224).
//
// [MockDatabase] is memory, and deliberately so: a restart is an unseeded database and
// nothing it answered was ever real. That is the right lifetime and the wrong
// *visibility*. Until now the only window onto a mockup run was the worker's log, which
// an operator reaches by scrolling past everything else the worker did.
//
// This file is the reporting half. A worker snapshots its own journal and posts it to
// the Atlas whose Console the operator is watching; that server keeps the newest
// snapshot per worker in a [MockJournalView] and shows it. The direction is the preview
// outbox's, for the same reason ([ADR-0213] made the identical call for the mock
// directory): a worker may sit in a network the server cannot dial back into, so what
// crosses is a report the worker sends, never a request the server makes.
//
// # Where this differs from the mock directory, and it matters
//
// The AD view answers "what is in the directory now" — state. This mock holds no state
// at all: it answers statements and executes nothing (ADR-0221), so an INSERT changes
// nothing a later SELECT would see. The journal is therefore not a companion to a view
// of the data, it *is* the view — the only way to see what a mockup run did.
//
// And ADR-0213 could promise that no password travels, because the mock directory
// stores none. **This one cannot.** A journal entry carries the values a process bound,
// and a process under test binds whatever it binds — a password hash on its way into a
// table is a bound parameter like any other, and nothing here can tell it from an id.
// That is why the read is admin-only, why the report only ever leaves a worker whose
// mockup is switched on, and why the whole thing is memory on both sides: no event, no
// log, nothing replayed (I4/I6).
//
// [ADR-0213]: https://github.com/pblumer/atlas/blob/main/docs/adr/0213-ad-mock-directory-in-the-console.md

// MockJournalSnapshot is one worker's journal at one moment: the statements a process
// ran, oldest first, with the values it bound.
//
// Worker names the reporter — the id the Workers view shows — so an operator running
// two mock workers can tell whose run they are looking at. At is stamped by the
// [MockJournalView] that accepted it, never by the reporter.
//
// Held is what the journal actually has, which is not len(Statements) once Truncated is
// set — the number is the point of the flag rather than a decoration on it.
type MockJournalSnapshot struct {
	Worker     string          `json:"worker,omitempty"`
	At         int64           `json:"at,omitempty"`
	Seeded     int             `json:"seeded"`
	Statements []MockStatement `json:"statements"`
	Held       int             `json:"held"`
	Truncated  bool            `json:"truncated,omitempty"`
}

// Snapshot is what this database has been asked, for a view somewhere else.
//
// max bounds the snapshot because the result crosses a network into another process's
// memory. 0 means unbounded, which is what a test and a small mockup want. The
// **newest** are kept when it bites: "what did it just do" is the question being asked,
// and the oldest entries are the ones already read.
//
// It takes the lock once, so what it returns is one consistent moment rather than a
// journal half from before a statement and half from after.
func (m *MockDatabase) Snapshot(max int) MockJournalSnapshot {
	m.mu.Lock()
	defer m.mu.Unlock()

	// Statements is allocated even when empty: a JSON null and an empty list read the
	// same to a person and differently to every consumer, and "mock mode is on, 12
	// answers seeded, nothing asked yet" is an ordinary state rather than an absence
	// of information.
	snap := MockJournalSnapshot{
		Seeded:     len(m.answers),
		Statements: []MockStatement{},
		Held:       len(m.ran),
	}
	ran := m.ran
	if max > 0 && len(ran) > max {
		ran, snap.Truncated = ran[len(ran)-max:], true
	}
	snap.Statements = append(snap.Statements, ran...)
	return snap
}

// Version is how many statements this database has been asked. A reporter that has
// already sent a version has nothing new to say, which is what keeps a worker leasing
// jobs of other kinds from posting the same journal over and over.
func (m *MockDatabase) Version() uint64 {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.seq
}

// maxJournalViewWorkers bounds how many workers a view keeps. A supervised worker is
// restarted whenever its environment changes and comes back under the same id, so this
// is not usually reached; it is here because an operator running many external mock
// workers must not be able to grow a server's memory one dead journal at a time.
const maxJournalViewWorkers = 8

// anonymousJournalWorker files a report from a worker that did not name itself. An
// external worker is configured by hand and may have no id to send; dropping its report
// would answer "why is my journal not showing" with silence.
const anonymousJournalWorker = "(unnamed worker)"

// MockJournalView is the newest snapshot each mock worker reported, and the whole of
// what an Atlas keeps about mockup runs.
//
// Like the preview outbox it holds its own lock rather than living on the run loop: a
// worker posts into it off the loop and after fsync (I2/I3), an HTTP read serves the
// Console from a request goroutine, and neither has any business waiting for the
// engine's single writer.
type MockJournalView struct {
	mu      sync.Mutex
	cap     int
	byName  map[string]MockJournalSnapshot
	nowFunc func() int64
}

// journalNowNanos is the view's default clock.
func journalNowNanos() int64 { return time.Now().UnixNano() }

// NewMockJournalView returns a view holding at most capacity workers; a capacity below
// one falls back to the default.
func NewMockJournalView(capacity int) *MockJournalView {
	return NewMockJournalViewClock(capacity, nil)
}

// NewMockJournalViewClock is [NewMockJournalView] over an injected clock, so a test can
// assert on the arrival stamp without depending on wall time.
func NewMockJournalViewClock(capacity int, now func() int64) *MockJournalView {
	if capacity < 1 {
		capacity = maxJournalViewWorkers
	}
	if now == nil {
		now = journalNowNanos
	}
	return &MockJournalView{cap: capacity, byName: map[string]MockJournalSnapshot{}, nowFunc: now}
}

// Put files one worker's report, replacing whatever that worker last said. A worker
// reports its whole journal every time, so replacing is the only correct merge — and it
// is what makes a restarted worker's empty journal show as empty rather than as
// yesterday's run.
func (v *MockJournalView) Put(s MockJournalSnapshot) {
	name := strings.TrimSpace(s.Worker)
	if name == "" {
		name = anonymousJournalWorker
	}
	v.mu.Lock()
	defer v.mu.Unlock()
	// The stamp is the view's, not the reporter's: a worker that could choose it could
	// make its own report look fresher than another's, and freshness is what decides who
	// is dropped below.
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
func (v *MockJournalView) Snapshots() []MockJournalSnapshot {
	v.mu.Lock()
	defer v.mu.Unlock()
	out := make([]MockJournalSnapshot, 0, len(v.byName))
	for _, s := range v.byName {
		out = append(out, s)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Worker < out[j].Worker })
	return out
}
