package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/pblumer/atlas/connector/nettimeout"
	"github.com/pblumer/atlas/logging"
)

// Push delivery of the event feed
// (ADR-0433).
//
// A feed subscription (feedsubstore.go) names a cloudevents Worker, and the server POSTs
// the feed's rows after the subscription's cursor to that Worker's endpoint, a batch at a
// time, as the CloudEvents HTTP binding's batched mode: a JSON array of the same
// envelopes the pull route serves. It is the pull feed with the cursor held here: it
// reads the feed through the same function (readFeedPage), adds no fact and no path into
// applyToState, and does its network I/O off the run loop, handing only the cursor's
// write back onto it (invariant I3) — the inbound bridge's shape, run the other way.
//
// Delivery is at least once. The cursor moves only after the endpoint answered 2xx for
// every event up to it, and a crash between the answer and the write delivers the batch
// again; the consumer deduplicates by `id`, exactly as a reader of the pull feed does.
//
// A failing endpoint is held, not skipped. The subscription's cursor stays where it was,
// and the next attempt waits on a ladder — 10 s, doubling to 5 min, the breaker's own
// (ADR-0340) — and the hold lifts on the first batch the endpoint accepts. Skipping a
// batch the endpoint refused would be the one failure a billing system cannot detect.
// A subscription whose cursor the feed's retention has passed is switched off and says
// why: the rows it had not delivered are gone, and delivering on from the oldest held
// would hide that.

const (
	// defaultFeedPushBatch is how many events one POST carries when the subscription
	// names no batch size, and maxFeedPushBatch the most it may name — the pull route's
	// page bounds.
	defaultFeedPushBatch = eventFeedPage
	maxFeedPushBatch     = eventFeedMaxPage
	// feedPushBatchesPerTick bounds how many batches one subscription delivers per tick,
	// so a subscription catching up on a backlog drains it at a steady rate without one
	// tick lasting as long as the backlog; its cursor is written once per tick.
	feedPushBatchesPerTick = 20
	// feedPushContentType is the CloudEvents HTTP binding's batched content mode.
	feedPushContentType = "application/cloudevents-batch+json"
)

// feedPushHold is the delivery state of one subscription that is not in the record: how
// often it has failed in a row, when it may be tried again and what the endpoint last
// said. Runtime only, like a breaker's (ADR-0340) — a restart tries a held subscription
// again at once, which costs one request.
type feedPushHold struct {
	Failures     int       `json:"failures"`
	RetryAt      time.Time `json:"retryAt"`
	LastError    string    `json:"lastError"`
	FailingSince time.Time `json:"failingSince"`
}

// feedPushState is every subscription's hold, behind a lock because the delivery
// goroutine writes it and the listing reads it.
type feedPushState struct {
	mu    sync.Mutex
	holds map[string]feedPushHold
}

func newFeedPushState() *feedPushState { return &feedPushState{holds: map[string]feedPushHold{}} }

// hold answers a subscription's hold and whether it has one.
func (st *feedPushState) hold(id string) (feedPushHold, bool) {
	st.mu.Lock()
	defer st.mu.Unlock()
	h, ok := st.holds[id]
	return h, ok
}

// failed records a failed attempt and sets the next one on the ladder. It answers
// whether this failure began a streak, which is when it is worth a log line.
func (st *feedPushState) failed(id string, now time.Time, why string) bool {
	st.mu.Lock()
	defer st.mu.Unlock()
	h := st.holds[id]
	if h.Failures == 0 {
		h.FailingSince = now
	}
	h.Failures++
	h.LastError = why
	h.RetryAt = now.Add(feedPushBackoff(h.Failures))
	st.holds[id] = h
	return h.Failures == 1
}

// succeeded lifts a subscription's hold and answers whether there was one.
func (st *feedPushState) succeeded(id string) bool {
	st.mu.Lock()
	defer st.mu.Unlock()
	_, held := st.holds[id]
	delete(st.holds, id)
	return held
}

// feedPushBackoff is the wait after the n-th failure in a row: the breaker's cooldown
// ladder (ADR-0340), so an endpoint that is down is asked as often as a tripped Worker is.
func feedPushBackoff(n int) time.Duration {
	d := breakerCooldown
	for i := 1; i < n && d < breakerMaxCooldown; i++ {
		d *= 2
	}
	if d > breakerMaxCooldown {
		d = breakerMaxCooldown
	}
	return d
}

// pendingFeedPush is one subscription resolved for a tick: its record, its Worker's
// endpoint and the vault reference of the credential sent as a bearer token. The
// credential itself is read only when a batch is about to go, so a round with nothing
// new reads no secret.
type pendingFeedPush struct {
	sub            feedSubscription
	endpoint       string
	credentialsRef string
}

// feedPusher delivers the feed's subscriptions on a fixed cadence until the server stops.
func (s *Server) feedPusher(every time.Duration) {
	defer s.wg.Done()
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		select {
		case <-s.quit:
			return
		case <-t.C:
			s.pushFeed(context.Background())
		}
	}
}

// pushFeed runs one delivery round: it resolves the due subscriptions and their Workers on
// the run loop, then delivers each off it. Subscriptions are delivered one after another,
// so a slow endpoint delays the others by at most its timeout; a held one is not asked.
func (s *Server) pushFeed(ctx context.Context) {
	// The feed is the catalogue's, and a server that switched the catalogue off
	// serves neither its pull route nor its subscriptions — so nothing of it leaves
	// by push either. Every subscription keeps its cursor, and delivery picks up
	// there when the area is back (ADR-0434).
	if s.catalogueOff {
		return
	}
	now := s.feedPushNow()
	var due []pendingFeedPush
	s.do(func() { due = s.resolveFeedPushes(now) })
	for _, p := range due {
		s.deliverFeed(ctx, p, now)
	}
}

// resolveFeedPushes lists the subscriptions to deliver this round: enabled, on an enabled
// cloudevents Worker, and not held. It runs on the run loop, which owns the stores.
func (s *Server) resolveFeedPushes(now time.Time) []pendingFeedPush {
	subs, err := s.feedSubs.LoadAll()
	if err != nil {
		return nil
	}
	var out []pendingFeedPush
	for _, sub := range subs {
		if !sub.Enabled {
			continue
		}
		if h, held := s.feedPushes.hold(sub.ID); held && now.Before(h.RetryAt) {
			continue
		}
		w, ok, err := s.connectors.Get(sub.WorkerID)
		if err != nil || !ok || !w.Enabled || w.Kind != connectorKindCloudEvents {
			continue
		}
		out = append(out, pendingFeedPush{sub: sub, endpoint: w.Endpoint, credentialsRef: w.CredentialsRef})
	}
	return out
}

// deliverFeed delivers up to feedPushBatchesPerTick batches of one subscription and then
// writes its cursor once. A failure holds the subscription where the last accepted batch
// left it.
func (s *Server) deliverFeed(ctx context.Context, p pendingFeedPush, now time.Time) {
	cursor := p.sub.Cursor
	delivered := false
	for i := 0; i < feedPushBatchesPerTick; i++ {
		next, sent, more, err := s.deliverFeedBatch(ctx, p, cursor)
		if errors.Is(err, errFeedBehind) {
			s.do(func() { s.disableFeedSubscription(p.sub.ID, p.sub.Cursor, err.Error()) })
			logging.Warn(logging.FeedSubscriptionDisabled, "a feed subscription fell behind the feed's retention and was switched off",
				slog.String("subscription_id", p.sub.ID), slog.String("reason", err.Error()))
			return
		}
		if err != nil {
			if s.feedPushes.failed(p.sub.ID, now, err.Error()) {
				logging.Warn(logging.FeedPushFailing, "a feed subscription's endpoint is failing; delivery holds and retries",
					slog.String("subscription_id", p.sub.ID), slog.String("error", err.Error()))
			}
			break
		}
		if s.feedPushes.succeeded(p.sub.ID) {
			logging.Info(logging.FeedPushRecovered, "a feed subscription's endpoint accepts deliveries again",
				slog.String("subscription_id", p.sub.ID))
		}
		delivered = delivered || sent
		cursor = next
		if !more {
			break
		}
	}
	if cursor != p.sub.Cursor {
		s.do(func() { s.advanceFeedCursor(p.sub.ID, p.sub.Cursor, cursor, delivered, now) })
	}
}

// errFeedBehind says the feed's retention dropped rows after a subscription's cursor.
var errFeedBehind = errors.New("feed behind")

// deliverFeedBatch reads one batch after the cursor and POSTs it. It answers the cursor
// to move to, whether anything was sent, and whether more is waiting. A batch the
// subscription's reach passes over entirely moves the cursor without a request.
func (s *Server) deliverFeedBatch(ctx context.Context, p pendingFeedPush, after uint64) (uint64, bool, bool, error) {
	view, nodeID, part, err := s.feedSnapshot()
	if err != nil {
		return after, false, false, err
	}
	defer view.Close()
	through, err := view.FeedPrunedThrough(part)
	if err != nil {
		return after, false, false, err
	}
	if after < through {
		return after, false, false, fmt.Errorf("%w: the feed's retention dropped the rows after position %d, "+
			"which this subscription had not delivered; the oldest it holds follows %d", errFeedBehind, after, through)
	}
	size := p.sub.BatchSize
	if size <= 0 {
		size = defaultFeedPushBatch
	}
	page, err := s.readFeedPage(view, part, nodeID, after, size, reachSet(p.sub.Reach))
	if err != nil {
		return after, false, false, err
	}
	next, err := strconv.ParseUint(page.Next, 10, 64)
	if err != nil {
		return after, false, false, err
	}
	if len(page.Events) == 0 {
		return next, false, page.More, nil
	}
	if err := s.postFeedBatch(ctx, p, page.Events); err != nil {
		return after, false, false, err
	}
	return next, true, page.More, nil
}

// postFeedBatch POSTs one batch to the endpoint and answers nil only for a 2xx. It does
// not follow a redirect: the credential is for the endpoint the Worker names, and a
// redirect elsewhere is a refusal, not an address.
func (s *Server) postFeedBatch(ctx context.Context, p pendingFeedPush, events []cloudEvent) error {
	body, err := json.Marshal(events)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, p.endpoint, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", feedPushContentType)
	req.Header.Set("Atlas-Feed-Subscription", p.sub.ID)
	if p.credentialsRef != "" {
		// The vault is the run loop's; the secret lives only in this request.
		var secret string
		s.do(func() { secret = s.resolveConnectorSecret(p.credentialsRef) })
		if secret != "" {
			req.Header.Set("Authorization", "Bearer "+secret)
		}
	}
	resp, err := s.feedPushHTTP().Do(req)
	if err != nil {
		return fmt.Errorf("deliver to %s: %w", p.endpoint, err)
	}
	defer resp.Body.Close()
	// What a refusal said is kept as a diagnostic quotation, bounded as every failed
	// response's is.
	excerpt, _ := io.ReadAll(io.LimitReader(resp.Body, s.budgets().ErrorBody))
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		msg := "the endpoint answered " + resp.Status
		if text := strings.TrimSpace(string(excerpt)); text != "" {
			msg += ": " + text
		}
		return errors.New(msg)
	}
	return nil
}

// feedPushHTTP is the client delivery uses: the connectors' timeout, no redirects.
func (s *Server) feedPushHTTP() *http.Client {
	if s.feedPushClient != nil {
		return s.feedPushClient
	}
	c := nettimeout.HTTPClient()
	c.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return c
}

// feedPushNow is the clock delivery is paced by, injectable so a test does not wait out
// a backoff.
func (s *Server) feedPushNow() time.Time {
	if s.feedPushClock != nil {
		return s.feedPushClock()
	}
	return time.Now()
}

// advanceFeedCursor writes a subscription's new cursor, unless the record moved while the
// batch was out: an administrator who rewound or deleted it meanwhile wins. Runs on the
// run loop.
func (s *Server) advanceFeedCursor(id string, from, to uint64, delivered bool, now time.Time) {
	rec, ok, err := s.feedSubs.Get(id)
	if err != nil || !ok || rec.Cursor != from {
		return
	}
	rec.Cursor = to
	if delivered {
		rec.DeliveredAt = now.Unix()
	}
	_ = s.feedSubs.Save(rec)
}

// disableFeedSubscription switches a subscription off with the reason, unless it moved
// meanwhile. Runs on the run loop.
func (s *Server) disableFeedSubscription(id string, at uint64, why string) {
	rec, ok, err := s.feedSubs.Get(id)
	if err != nil || !ok || rec.Cursor != at {
		return
	}
	rec.Enabled = false
	rec.DisabledReason = strings.TrimPrefix(why, errFeedBehind.Error()+": ")
	_ = s.feedSubs.Save(rec)
	s.feedPushes.succeeded(id)
}

// reachSet is a reach as the set readFeedPage narrows by, or nil for none.
func reachSet(reach []string) map[string]bool {
	if len(reach) == 0 {
		return nil
	}
	out := make(map[string]bool, len(reach))
	for _, id := range reach {
		out[id] = true
	}
	return out
}
