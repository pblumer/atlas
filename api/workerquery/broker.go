// Package workerquery is the Worker protocol's second message kind: a query, answered by
// a Worker Instance with its Worker's credential and never written anywhere
// (ADR-0473).
//
// A job is durable work. Creating it is an event, leasing it is an event, completing it
// is an event, and recovery replays all three — which is right for work a process
// depends on. A person searching a directory from a task form is not that: there is
// nothing to recover, and a search term in the log is personal data kept for no
// reason. So a query lives in this package's memory for as long as the HTTP request
// waiting on it, and no longer.
//
// What it does share with a job is the boundary that matters. It is leased by the same
// Worker Instances, under the same fail-closed worker scope (ADR-0194/0297), routed by
// the same job type and to an Instance that reports holding the Worker it names
// (ADR-0168), and its answer is fenced by a lease token. The engine still holds no
// credential, and the processor, the WAL, the state store and recovery never see a
// query at all.
//
// The broker is neither engine nor design-time state, so it is not behind the run loop
// (I3 binds process state, and nothing here is). It holds its own mutex, as the
// server's rate limiters do, and every structure under it is bounded: the queue per job
// type, the batch a poll takes, and every query's life by its deadline.
package workerquery

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"slices"
	"sync"
	"time"

	"github.com/pblumer/atlas/connector/lookup"
)

// The answers a waiting asker can get instead of a page.
var (
	// ErrUnavailable: no Worker Instance holding that Worker has polled lately. Said at
	// once rather than after the deadline, because the person is watching a spinner.
	ErrUnavailable = errors.New("workerquery: no worker instance serves this worker")
	// ErrBusy: the queue for that job type is full.
	ErrBusy = errors.New("workerquery: too many queries are waiting for this worker")
	// ErrTimeout: nobody answered before the deadline.
	ErrTimeout = errors.New("workerquery: no answer before the deadline")
)

// The answers a Worker Instance can get for its answer.
var (
	// ErrGone: the query is no longer waiting — answered, withdrawn, timed out, or
	// lost with a restart. The answer is dropped.
	ErrGone = errors.New("workerquery: the query is no longer waiting")
	// ErrFenced: the answer does not carry the lease this query was handed out under.
	ErrFenced = errors.New("workerquery: the answer does not hold this query's lease")
)

// Limits bound the broker.
type Limits struct {
	// QueuePerType is how many queries may wait, unleased, for one job type.
	QueuePerType int
	// PresentFor is how long a poll counts as an Instance being there. It must exceed
	// the longest long poll, or an Instance waiting inside one would read as absent.
	PresentFor time.Duration
	// MaxBatch is the most queries one poll takes.
	MaxBatch int
}

// DefaultLimits are what the server runs with.
func DefaultLimits() Limits {
	return Limits{QueuePerType: 64, PresentFor: time.Minute, MaxBatch: 8}
}

// Answer is what a Worker Instance reports: a page, or the code it failed with.
type Answer struct {
	Page *lookup.Page
	Err  *lookup.Error
}

// Leased is one query as a Worker Instance receives it.
type Leased struct {
	ID         string `json:"id"`
	LeaseToken string `json:"leaseToken"`
	Type       string `json:"type"`
	// Connector names the Worker — the tenant — the query is for. The Instance holds
	// its credential; the query only names it, as a job does.
	Connector string `json:"connector"`
	// Deadline is when the asker stops waiting, in Unix milliseconds. An Instance has
	// no reason to spend a provider call past it.
	Deadline int64           `json:"deadline"`
	Query    json.RawMessage `json:"query"`
}

type query struct {
	Leased
	deadline time.Time
	leasedTo string
	answer   chan Answer
}

type presence struct {
	at         time.Time
	connectors []string
}

// Broker holds the queries waiting for a Worker Instance and the ones leased to one.
// Build it with [NewBroker]; the zero value is not usable.
type Broker struct {
	limits Limits
	now    func() time.Time
	after  func(time.Duration) <-chan time.Time

	mu sync.Mutex
	// queue is the unleased queries per job type, oldest first; byID every query still
	// waiting for an answer, leased or not.
	queue map[string][]*query
	byID  map[string]*query
	// arrived is closed and replaced when a query of a job type is queued, waking the
	// long polls of that type.
	arrived map[string]chan struct{}
	// seen is when each Instance last polled a job type, and which Workers it holds.
	seen map[string]map[string]presence
	// polling counts the long polls waiting per job type.
	polling map[string]int
	// changed is closed and replaced on every change of state. Tests wait on it
	// instead of sleeping.
	changed chan struct{}
}

// NewBroker builds an empty broker.
func NewBroker(l Limits) *Broker {
	if l.QueuePerType <= 0 || l.PresentFor <= 0 || l.MaxBatch <= 0 {
		d := DefaultLimits()
		if l.QueuePerType <= 0 {
			l.QueuePerType = d.QueuePerType
		}
		if l.PresentFor <= 0 {
			l.PresentFor = d.PresentFor
		}
		if l.MaxBatch <= 0 {
			l.MaxBatch = d.MaxBatch
		}
	}
	return &Broker{
		limits:  l,
		now:     time.Now,
		after:   time.After,
		queue:   map[string][]*query{},
		byID:    map[string]*query{},
		arrived: map[string]chan struct{}{},
		seen:    map[string]map[string]presence{},
		polling: map[string]int{},
		changed: make(chan struct{}),
	}
}

// Ask queues one query for an Instance holding connector and waits for its answer, the
// timeout, or ctx — whichever comes first. A query nobody can answer is refused at
// once, and so is one that would overfill its queue.
//
// A Worker's error comes back as the *lookup.Error it reported.
func (b *Broker) Ask(ctx context.Context, jobType, connector string, q lookup.Query, timeout time.Duration) (lookup.Page, error) {
	body, err := json.Marshal(q)
	if err != nil {
		return lookup.Page{}, err
	}
	// The timer exists before the query is visible to anybody, so a test that fires
	// the timers it handed out once the query has been leased cannot miss this one.
	timer := b.after(timeout)
	b.mu.Lock()
	if !b.presentLocked(jobType, connector) {
		b.mu.Unlock()
		return lookup.Page{}, ErrUnavailable
	}
	if len(b.queue[jobType]) >= b.limits.QueuePerType {
		b.mu.Unlock()
		return lookup.Page{}, ErrBusy
	}
	deadline := b.now().Add(timeout)
	qq := &query{
		Leased: Leased{ID: token(), LeaseToken: token(), Type: jobType, Connector: connector,
			Deadline: deadline.UnixMilli(), Query: body},
		deadline: deadline,
		answer:   make(chan Answer, 1),
	}
	b.queue[jobType] = append(b.queue[jobType], qq)
	b.byID[qq.ID] = qq
	if ch, ok := b.arrived[jobType]; ok {
		close(ch)
		delete(b.arrived, jobType)
	}
	b.changedLocked()
	b.mu.Unlock()

	select {
	case a := <-qq.answer:
		if a.Err != nil {
			return lookup.Page{}, a.Err
		}
		if a.Page == nil {
			return lookup.Page{}, lookup.Fail(lookup.CodeUnavailable)
		}
		return *a.Page, nil
	case <-timer:
		b.withdraw(qq)
		return lookup.Page{}, ErrTimeout
	case <-ctx.Done():
		b.withdraw(qq)
		return lookup.Page{}, ctx.Err()
	}
}

// withdraw removes a query whose asker stopped waiting, leased or not, so a late
// answer is refused and an unleased query is never handed out.
func (b *Broker) withdraw(q *query) {
	b.mu.Lock()
	defer b.mu.Unlock()
	delete(b.byID, q.ID)
	if i := slices.Index(b.queue[q.Type], q); i >= 0 {
		b.queue[q.Type] = slices.Delete(b.queue[q.Type], i, i+1)
	}
	b.changedLocked()
}

// Lease hands an Instance up to max queries of jobType addressed to one of the Workers
// it holds, waiting up to wait for one to arrive. Every poll, productive or not, is
// what keeps the Instance present.
func (b *Broker) Lease(ctx context.Context, jobType, worker string, connectors []string, max int, wait time.Duration) []Leased {
	if max <= 0 || max > b.limits.MaxBatch {
		max = b.limits.MaxBatch
	}
	var timer <-chan time.Time
	if wait > 0 {
		// Taken before the poll is counted as waiting, for Ask's reason.
		timer = b.after(wait)
	}
	defer b.seenNow(jobType, worker, connectors)
	for {
		b.mu.Lock()
		b.seenLocked(jobType, worker, connectors)
		got := b.takeLocked(jobType, worker, connectors, max)
		if len(got) > 0 || wait <= 0 {
			b.mu.Unlock()
			return got
		}
		arrived, ok := b.arrived[jobType]
		if !ok {
			arrived = make(chan struct{})
			b.arrived[jobType] = arrived
		}
		b.polling[jobType]++
		b.changedLocked()
		b.mu.Unlock()

		var stop bool
		select {
		case <-arrived:
		case <-timer:
			stop = true
		case <-ctx.Done():
			stop = true
		}
		b.mu.Lock()
		b.polling[jobType]--
		b.changedLocked()
		b.mu.Unlock()
		if stop {
			return nil
		}
	}
}

// takeLocked leases the oldest waiting queries this Instance can answer. One whose
// asker has already given up is dropped rather than handed out.
func (b *Broker) takeLocked(jobType, worker string, connectors []string, max int) []Leased {
	var got []Leased
	now := b.now()
	rest := b.queue[jobType][:0]
	for _, q := range b.queue[jobType] {
		switch {
		case !now.Before(q.deadline):
			delete(b.byID, q.ID)
		case len(got) < max && slices.Contains(connectors, q.Connector):
			q.leasedTo = worker
			got = append(got, q.Leased)
		default:
			rest = append(rest, q)
		}
	}
	b.queue[jobType] = rest
	if len(got) > 0 {
		b.changedLocked()
	}
	return got
}

// Answer delivers an Instance's answer to the asker waiting on query id.
func (b *Broker) Answer(id, worker, leaseToken string, a Answer) error {
	b.mu.Lock()
	q, ok := b.byID[id]
	if !ok {
		b.mu.Unlock()
		return ErrGone
	}
	if q.leasedTo == "" || q.leasedTo != worker || leaseToken == "" || q.LeaseToken != leaseToken {
		b.mu.Unlock()
		return ErrFenced
	}
	delete(b.byID, id)
	b.changedLocked()
	b.mu.Unlock()
	// Buffered by one and written once: the asker reads it, or has gone and never will.
	q.answer <- a
	return nil
}

func (b *Broker) presentLocked(jobType, connector string) bool {
	now := b.now()
	for w, p := range b.seen[jobType] {
		if now.Sub(p.at) >= b.limits.PresentFor {
			delete(b.seen[jobType], w)
			continue
		}
		if slices.Contains(p.connectors, connector) {
			return true
		}
	}
	return false
}

func (b *Broker) seenNow(jobType, worker string, connectors []string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.seenLocked(jobType, worker, connectors)
}

func (b *Broker) seenLocked(jobType, worker string, connectors []string) {
	if b.seen[jobType] == nil {
		b.seen[jobType] = map[string]presence{}
	}
	b.seen[jobType][worker] = presence{at: b.now(), connectors: slices.Clone(connectors)}
}

func (b *Broker) changedLocked() {
	close(b.changed)
	b.changed = make(chan struct{})
}

// watch returns a channel closed on the next change of state.
func (b *Broker) watch() <-chan struct{} {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.changed
}

// waiting is how many unleased queries of jobType are queued.
func (b *Broker) waiting(jobType string) int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return len(b.queue[jobType])
}

// pollers is how many long polls of jobType are waiting.
func (b *Broker) pollers(jobType string) int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.polling[jobType]
}

// token is an unguessable id: a query id and a lease token are what a Worker Instance
// presents to answer, so neither may be derivable from the other or from a counter.
func token() string {
	var raw [16]byte
	if _, err := rand.Read(raw[:]); err != nil {
		panic("workerquery: no randomness: " + err.Error())
	}
	return hex.EncodeToString(raw[:])
}
