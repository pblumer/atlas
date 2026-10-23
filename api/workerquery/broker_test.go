package workerquery

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/pblumer/atlas/connector/lookup"
)

const entraType = "io.atlas.entra"

// clock is a hand-driven clock and timer source, so nothing here depends on how fast
// the machine running the test happens to be.
type clock struct {
	mu     sync.Mutex
	at     time.Time
	timers []chan time.Time
}

func newClock() *clock { return &clock{at: time.Unix(1_800_000_000, 0)} }

func (c *clock) now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.at
}

func (c *clock) advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.at = c.at.Add(d)
}

// after hands out timers that fire only when the test says so.
func (c *clock) after(time.Duration) <-chan time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	ch := make(chan time.Time, 1)
	c.timers = append(c.timers, ch)
	return ch
}

// fireAll fires every timer handed out so far.
func (c *clock) fireAll() {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, ch := range c.timers {
		select {
		case ch <- c.at:
		default:
		}
	}
	c.timers = nil
}

func testBroker(c *clock) *Broker {
	b := NewBroker(Limits{QueuePerType: 2, PresentFor: time.Minute, MaxBatch: 4})
	b.now, b.after = c.now, c.after
	return b
}

func aQuery() lookup.Query {
	return lookup.Query{Resource: "users", Search: "Ann", SearchFields: []string{"displayName"},
		Properties: []string{"id", "displayName"}, PageSize: 25}
}

type asked struct {
	page lookup.Page
	err  error
}

// ask runs Ask on its own goroutine, as a request handler would, and hands back the
// channel its answer arrives on.
func ask(b *Broker, ctx context.Context, connector string) <-chan asked {
	out := make(chan asked, 1)
	go func() {
		p, err := b.Ask(ctx, entraType, connector, aQuery(), 5*time.Second)
		out <- asked{p, err}
	}()
	return out
}

// present makes a Worker Instance holding these Workers known to the broker, by a poll
// that finds nothing.
func present(t *testing.T, b *Broker, worker string, connectors ...string) {
	t.Helper()
	if got := b.Lease(context.Background(), entraType, worker, connectors, 4, 0); len(got) != 0 {
		t.Fatalf("an empty poll leased %d queries", len(got))
	}
}

// leaseOne long-polls until a query arrives. The broker's own signal wakes it, so it
// does not depend on how the goroutines happen to be scheduled.
func leaseOne(t *testing.T, b *Broker, worker string, connectors ...string) Leased {
	t.Helper()
	got := b.Lease(context.Background(), entraType, worker, connectors, 4, time.Hour)
	if len(got) != 1 {
		t.Fatalf("leased %d queries, want 1", len(got))
	}
	return got[0]
}

func TestAQueryNobodyCanAnswerIsRefusedAtOnce(t *testing.T) {
	b := testBroker(newClock())
	_, err := b.Ask(context.Background(), entraType, "ucc_entra", aQuery(), 5*time.Second)
	if !errors.Is(err, ErrUnavailable) {
		t.Fatalf("err = %v, want ErrUnavailable", err)
	}
}

func TestAQueryReachesTheInstanceHoldingItsWorkerAndItsAnswerTheAsker(t *testing.T) {
	b := testBroker(newClock())
	present(t, b, "w1", "ucc_entra")
	answer := ask(b, context.Background(), "ucc_entra")

	l := leaseOne(t, b, "w1", "ucc_entra")
	if l.Type != entraType || l.Connector != "ucc_entra" || l.ID == "" || l.LeaseToken == "" {
		t.Fatalf("leased %+v", l)
	}
	var q lookup.Query
	if err := json.Unmarshal(l.Query, &q); err != nil || q.Search != "Ann" {
		t.Fatalf("query = %s (%v)", l.Query, err)
	}
	page := lookup.Page{Items: []map[string]any{{"id": "a", "displayName": "Anna"}}}
	if err := b.Answer(l.ID, "w1", l.LeaseToken, Answer{Page: &page}); err != nil {
		t.Fatal(err)
	}
	got := <-answer
	if got.err != nil || len(got.page.Items) != 1 {
		t.Fatalf("asker got %+v", got)
	}
}

// An Instance that holds a different tenant is not handed this one's query: it could
// only answer it with "not configured", and the one that can would never see it.
func TestAQueryIsLeasedOnlyToAnInstanceHoldingItsWorker(t *testing.T) {
	b := testBroker(newClock())
	present(t, b, "other", "contoso")
	if _, err := b.Ask(context.Background(), entraType, "ucc_entra", aQuery(), time.Second); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("err = %v, want ErrUnavailable: nobody holds ucc_entra", err)
	}
	present(t, b, "w1", "ucc_entra")
	answer := ask(b, context.Background(), "ucc_entra")
	l := leaseOne(t, b, "w1", "ucc_entra")
	if got := b.Lease(context.Background(), entraType, "other", []string{"contoso"}, 4, 0); len(got) != 0 {
		t.Fatalf("the instance holding contoso leased %d queries", len(got))
	}
	_ = b.Answer(l.ID, "w1", l.LeaseToken, Answer{Page: &lookup.Page{}})
	<-answer
}

func TestAWorkerErrorReachesTheAskerAsItsCode(t *testing.T) {
	b := testBroker(newClock())
	present(t, b, "w1", "ucc_entra")
	answer := ask(b, context.Background(), "ucc_entra")
	l := leaseOne(t, b, "w1", "ucc_entra")
	if err := b.Answer(l.ID, "w1", l.LeaseToken, Answer{Err: &lookup.Error{Code: lookup.CodeThrottled, RetryAfter: 3}}); err != nil {
		t.Fatal(err)
	}
	got := <-answer
	var le *lookup.Error
	if !errors.As(got.err, &le) || le.Code != lookup.CodeThrottled || le.RetryAfter != 3 {
		t.Fatalf("err = %v", got.err)
	}
}

func TestTheQueueIsBoundedPerType(t *testing.T) {
	b := testBroker(newClock()) // QueuePerType: 2
	present(t, b, "w1", "ucc_entra")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	first, second := ask(b, ctx, "ucc_entra"), ask(b, ctx, "ucc_entra")
	// Both are queued once a poll can take both.
	got := b.Lease(context.Background(), entraType, "w1", []string{"ucc_entra"}, 1, time.Hour)
	if len(got) != 1 {
		t.Fatalf("leased %d, want the one asked for", len(got))
	}
	waitQueued(t, b, 1)
	// One leased and one waiting: the waiting one fills the queue together with the next.
	third := ask(b, ctx, "ucc_entra")
	waitQueued(t, b, 2)
	if _, err := b.Ask(ctx, entraType, "ucc_entra", aQuery(), time.Second); !errors.Is(err, ErrBusy) {
		t.Fatalf("err = %v, want ErrBusy", err)
	}
	cancel()
	for _, ch := range []<-chan asked{first, second, third} {
		<-ch
	}
}

// waitFor blocks until cond holds, woken by the broker's own change signal rather than
// by sleeping: the broker announces every queued, withdrawn, leased and waiting query,
// so the test proceeds the moment the state it waits for exists. The timer is a safety
// net that turns a missing signal into a failure instead of a hung run.
func waitFor(t *testing.T, b *Broker, what string, cond func() bool) {
	t.Helper()
	for {
		ch := b.watch()
		if cond() {
			return
		}
		select {
		case <-ch:
		case <-time.After(30 * time.Second):
			t.Fatalf("%s never happened", what)
		}
	}
}

func waitQueued(t *testing.T, b *Broker, n int) {
	t.Helper()
	waitFor(t, b, "a queue of the expected length", func() bool { return b.waiting(entraType) == n })
}

func TestAQueryWithoutAnAnswerTimesOutAndALateAnswerIsRefused(t *testing.T) {
	c := newClock()
	b := testBroker(c)
	present(t, b, "w1", "ucc_entra")
	answer := ask(b, context.Background(), "ucc_entra")
	l := leaseOne(t, b, "w1", "ucc_entra")
	c.fireAll()
	got := <-answer
	if !errors.Is(got.err, ErrTimeout) {
		t.Fatalf("err = %v, want ErrTimeout", got.err)
	}
	if err := b.Answer(l.ID, "w1", l.LeaseToken, Answer{Page: &lookup.Page{}}); !errors.Is(err, ErrGone) {
		t.Fatalf("late answer: err = %v, want ErrGone", err)
	}
}

// A person who types the next letter cancels the request for the previous one; the
// query it was waiting on is withdrawn, so no Instance spends a Graph call on it.
func TestACancelledAskWithdrawsItsQuery(t *testing.T) {
	b := testBroker(newClock())
	present(t, b, "w1", "ucc_entra")
	ctx, cancel := context.WithCancel(context.Background())
	answer := ask(b, ctx, "ucc_entra")
	waitQueued(t, b, 1)
	cancel()
	if got := <-answer; !errors.Is(got.err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", got.err)
	}
	if got := b.Lease(context.Background(), entraType, "w1", []string{"ucc_entra"}, 4, 0); len(got) != 0 {
		t.Fatalf("a withdrawn query was leased: %+v", got)
	}
}

func TestAnAnswerIsFencedByItsLease(t *testing.T) {
	b := testBroker(newClock())
	present(t, b, "w1", "ucc_entra")
	present(t, b, "w2", "ucc_entra")
	answer := ask(b, context.Background(), "ucc_entra")
	l := leaseOne(t, b, "w1", "ucc_entra")
	for _, bad := range []struct{ worker, token string }{{"w2", l.LeaseToken}, {"w1", "forged"}, {"w1", ""}} {
		if err := b.Answer(l.ID, bad.worker, bad.token, Answer{Page: &lookup.Page{}}); !errors.Is(err, ErrFenced) {
			t.Errorf("answer as %+v: err = %v, want ErrFenced", bad, err)
		}
	}
	if err := b.Answer("nope", "w1", l.LeaseToken, Answer{Page: &lookup.Page{}}); !errors.Is(err, ErrGone) {
		t.Errorf("unknown id: err = %v, want ErrGone", err)
	}
	if err := b.Answer(l.ID, "w1", l.LeaseToken, Answer{Page: &lookup.Page{}}); err != nil {
		t.Fatal(err)
	}
	if got := <-answer; got.err != nil {
		t.Fatal(got.err)
	}
}

// An Instance is present while it keeps polling. One that stopped — crashed, or an old
// worker that never polls for queries — must not make a person wait out a deadline.
func TestPresenceLapsesWhenAnInstanceStopsPolling(t *testing.T) {
	c := newClock()
	b := testBroker(c)
	present(t, b, "w1", "ucc_entra")
	c.advance(59 * time.Second)
	ctx, cancel := context.WithCancel(context.Background())
	answer := ask(b, ctx, "ucc_entra")
	waitQueued(t, b, 1)
	cancel()
	<-answer
	c.advance(2 * time.Second)
	if _, err := b.Ask(context.Background(), entraType, "ucc_entra", aQuery(), time.Second); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("err = %v, want ErrUnavailable once the last poll is a minute old", err)
	}
}

func TestALongPollReturnsEmptyWhenItsWaitRunsOut(t *testing.T) {
	c := newClock()
	b := testBroker(c)
	done := make(chan []Leased, 1)
	go func() {
		done <- b.Lease(context.Background(), entraType, "w1", []string{"ucc_entra"}, 4, 30*time.Second)
	}()
	waitFor(t, b, "a waiting poll", func() bool { return b.pollers(entraType) == 1 })
	c.fireAll()
	if got := <-done; len(got) != 0 {
		t.Fatalf("leased %d", len(got))
	}
}

func TestALongPollEndsWithItsRequest(t *testing.T) {
	b := testBroker(newClock())
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan []Leased, 1)
	go func() { done <- b.Lease(ctx, entraType, "w1", []string{"ucc_entra"}, 4, time.Hour) }()
	waitFor(t, b, "a waiting poll", func() bool { return b.pollers(entraType) == 1 })
	cancel()
	if got := <-done; len(got) != 0 {
		t.Fatalf("leased %d", len(got))
	}
}

// A broker built without limits — or with some of them — runs under the defaults for
// the ones it was not given, rather than with a queue of zero or an Instance that is
// never present.
func TestABrokerFillsInTheLimitsItWasNotGiven(t *testing.T) {
	if got := NewBroker(Limits{}).limits; got != DefaultLimits() {
		t.Errorf("no limits: %+v, want %+v", got, DefaultLimits())
	}
	d := DefaultLimits()
	if got := NewBroker(Limits{QueuePerType: 3}).limits; got != (Limits{QueuePerType: 3, PresentFor: d.PresentFor, MaxBatch: d.MaxBatch}) {
		t.Errorf("a queue only: %+v", got)
	}
}

// An Instance polling for another tenant leaves the query where it is, for the
// Instance that holds its Worker — even when it polls first.
func TestAQueryWaitsForTheInstanceHoldingItsWorker(t *testing.T) {
	b := testBroker(newClock())
	present(t, b, "w1", "ucc_entra")
	answer := ask(b, context.Background(), "ucc_entra")
	waitQueued(t, b, 1)
	if got := b.Lease(context.Background(), entraType, "other", []string{"contoso"}, 4, 0); len(got) != 0 {
		t.Fatalf("the instance holding contoso leased %d queries", len(got))
	}
	l := leaseOne(t, b, "w1", "ucc_entra")
	_ = b.Answer(l.ID, "w1", l.LeaseToken, Answer{Page: &lookup.Page{}})
	if a := <-answer; a.err != nil {
		t.Fatalf("asker got %+v", a)
	}
}

// A query whose deadline passed while it waited is dropped by the next poll, not handed
// to an Instance that would spend a Graph request on an asker who has gone.
func TestAQueryPastItsDeadlineIsNotLeased(t *testing.T) {
	c := newClock()
	b := testBroker(c)
	present(t, b, "w1", "ucc_entra")
	answer := ask(b, context.Background(), "ucc_entra")
	waitQueued(t, b, 1)
	c.advance(6 * time.Second) // past ask's five seconds; its timer has not fired yet
	if got := b.Lease(context.Background(), entraType, "w1", []string{"ucc_entra"}, 4, 0); len(got) != 0 {
		t.Fatalf("leased %d expired queries", len(got))
	}
	c.fireAll()
	if a := <-answer; !errors.Is(a.err, ErrTimeout) {
		t.Fatalf("asker got %+v, want ErrTimeout", a)
	}
}

// An answer that carries neither a page nor an error is a failure, never an empty
// page: "nobody by that name" must not be what a broken Instance says by accident.
func TestAnEmptyAnswerIsAFailureNotAnEmptyPage(t *testing.T) {
	b := testBroker(newClock())
	present(t, b, "w1", "ucc_entra")
	answer := ask(b, context.Background(), "ucc_entra")
	l := leaseOne(t, b, "w1", "ucc_entra")
	if err := b.Answer(l.ID, "w1", l.LeaseToken, Answer{}); err != nil {
		t.Fatal(err)
	}
	a := <-answer
	var le *lookup.Error
	if !errors.As(a.err, &le) || le.Code != lookup.CodeUnavailable {
		t.Fatalf("asker got %+v, want %s", a, lookup.CodeUnavailable)
	}
}
