package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/pblumer/atlas/api/httpapi"
	"github.com/pblumer/atlas/eventcatalog"
	"github.com/pblumer/atlas/state"
)

// The event feed: what leaves Atlas (ADR-0429 §5).
//
// Every action outcome, every grant and every revocation is a row of the feed, folded
// by applyToState and keyed by where its record sits on the log. A consumer beyond
// Atlas — a CMDB, a billing system — pulls the rows after a cursor it keeps, each as a
// CloudEvents 1.0 envelope in structured JSON. It is answered at least once and
// deduplicates by `id`. A cursor older than the feed's retention is answered 410 with
// the oldest cursor still held: a consumer that fell behind is told what it missed
// rather than silently skipping it.
//
// The route reads a snapshot off the run loop (ADR-0239). It requires the `feedreader`
// role, which a token minted with the `events` scope carries and nothing else, so a
// system that follows the feed holds a credential that reads the feed and no more
// (ADR-0430).
//
// Every row belongs to the catalogue that maintains its product — the item's home — and
// says so as `homeCatalog`. An events token minted with a reach of catalogues reads only
// the rows whose product one of them maintains; the rows it may not read are passed
// over, and the cursor moves past them, so the holder never waits on a row it will not
// be given. The home is read when the page is, not frozen in the fact: an item moved to
// another home moves its rows with it, as its editing already did
// (ADR-0432).

// defaultEventFeedTTL is how long a feed row is kept when the operator set nothing.
const defaultEventFeedTTL = 30 * 24 * time.Hour

const (
	// eventFeedPage is how many events a page holds when the caller names no limit,
	// and eventFeedMaxPage the most it may name.
	eventFeedPage    = 100
	eventFeedMaxPage = 1000
)

// eventFeedScan is how many rows one page reads at most. It bounds the work a narrowed
// reader can cause: a token whose catalogues hold one row in a million would otherwise
// make a single request walk the whole feed. A page cut here answers what it found, its
// cursor at the last row read, and `more`.
//
// A var rather than a const so a test can reach the cut without writing ten thousand
// rows; nothing outside a test writes it.
var eventFeedScan = 10 * eventFeedMaxPage

// pruneEventFeed drops the feed rows older than their retention, at most once an hour.
// It runs on the run loop, inside the retention sweep's turn, and enqueues a prune
// through the last row recorded before the cutoff, so the cut is a fact replay repeats.
func (s *Server) pruneEventFeed(now int64) {
	if now-s.lastFeedPrune < int64(time.Hour) {
		return
	}
	s.lastFeedPrune = now
	ttl := s.eventFeedTTL
	if ttl <= 0 {
		ttl = defaultEventFeedTTL
	}
	part := s.proc.Partition()
	through, found, err := s.store.FeedThroughBefore(part, now-int64(ttl))
	if err != nil || !found {
		return
	}
	if done, err := s.store.FeedPrunedThrough(part); err != nil || through <= done {
		return
	}
	s.proc.PruneFeed(through)
	_ = s.proc.RunUntilIdle()
}

// cloudEvent is one feed row as CloudEvents 1.0 structured JSON.
type cloudEvent struct {
	SpecVersion     string `json:"specversion"`
	ID              string `json:"id"`
	Source          string `json:"source"`
	Type            string `json:"type"`
	Subject         string `json:"subject,omitempty"`
	Time            string `json:"time"`
	DataContentType string `json:"datacontenttype"`
	Data            any    `json:"data"`
}

// eventPage is one page of the feed: the events after the caller's cursor, and the
// cursor to send next — the last event's position, or the caller's own when the page
// is empty.
type eventPage struct {
	Events []cloudEvent `json:"events"`
	Next   string       `json:"next"`
	// More says the page was cut at its limit, so the next page is worth asking now.
	More bool `json:"more"`
}

var errEventPageFull = errors.New("event page full")

// handleListEvents serves GET /api/v1/events?after={cursor}&limit={n}.
func (s *Server) handleListEvents(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	var after uint64
	if raw := strings.TrimSpace(q.Get("after")); raw != "" {
		v, err := strconv.ParseUint(raw, 10, 64)
		if err != nil {
			httpapi.Error(w, http.StatusBadRequest, "after must be a cursor this feed answered: a decimal position")
			return
		}
		after = v
	}
	limit := eventFeedPage
	if raw := strings.TrimSpace(q.Get("limit")); raw != "" {
		v, err := strconv.Atoi(raw)
		if err != nil || v < 1 || v > eventFeedMaxPage {
			httpapi.Error(w, http.StatusBadRequest, "limit must be between 1 and "+strconv.Itoa(eventFeedMaxPage))
			return
		}
		limit = v
	}

	view, nodeID, part, err := s.feedSnapshot()
	switch {
	case errors.Is(err, errFeedStopping):
		httpapi.Error(w, http.StatusServiceUnavailable, "event feed: the server is stopping")
		return
	case err != nil:
		httpapi.Error(w, http.StatusInternalServerError, "event feed: "+err.Error())
		return
	}
	defer view.Close()

	through, err := view.FeedPrunedThrough(part)
	if err != nil {
		httpapi.Error(w, http.StatusInternalServerError, "event feed: "+err.Error())
		return
	}
	if after < through && q.Get("after") != "" {
		httpapi.JSON(w, http.StatusGone, map[string]string{
			"error": "the feed no longer holds what follows cursor " + strconv.FormatUint(after, 10) +
				": it keeps its rows for its retention window, and the ones after this cursor were dropped",
			"oldest": strconv.FormatUint(through, 10),
		})
		return
	}
	page, err := s.readFeedPage(view, part, nodeID, after, limit, feedReach(httpapi.PrincipalFrom(r.Context())))
	if err != nil {
		httpapi.Error(w, http.StatusInternalServerError, "event feed: "+err.Error())
		return
	}
	httpapi.JSON(w, http.StatusOK, page)
}

// errFeedStopping says the server is stopping and no snapshot of the feed can be taken.
var errFeedStopping = errors.New("the server is stopping")

// feedSnapshot takes what reading the feed needs from the run loop — the node's id, the
// partition and a read view — and nothing else, so the read itself runs with the loop
// free (ADR-0239). The caller closes the view.
func (s *Server) feedSnapshot() (view *state.ReadView, nodeID string, part uint16, err error) {
	s.do(func() {
		var ident nodeIdentity
		if ident, err = s.nodeIdentity(); err != nil {
			return
		}
		nodeID, part = ident.ID, s.proc.Partition()
		view = s.store.ReadView()
	})
	if err == nil && view == nil {
		err = errFeedStopping
	}
	return view, nodeID, part, err
}

// readFeedPage reads at most limit events after a cursor from a snapshot, narrowed to a
// reach of catalogues (nil reads everything). Both doors read through it: the pull
// route a page at a time, and push delivery a batch at a time (feedpush.go), so a
// subscription and a token with the same reach are given the same events.
func (s *Server) readFeedPage(view *state.ReadView, part uint16, nodeID string, after uint64, limit int,
	reach map[string]bool) (eventPage, error) {
	source := s.eventSource(nodeID)
	homes := s.itemHomes()
	page := eventPage{Events: []cloudEvent{}, Next: strconv.FormatUint(after, 10)}
	read := 0
	err := view.FeedAfter(part, after, func(e state.FeedEntry) error {
		if len(page.Events) == limit || read == eventFeedScan {
			page.More = true
			return errEventPageFull
		}
		read++
		home, err := homes(feedItem(e))
		if err != nil {
			return err
		}
		if reach != nil && !reach[home] {
			// Not this reader's: passed over, and the cursor with it.
			page.Next = strconv.FormatUint(e.Position, 10)
			return nil
		}
		page.Events = append(page.Events, feedEnvelope(e, nodeID, source, home))
		page.Next = strconv.FormatUint(e.Position, 10)
		return nil
	})
	if err != nil && !errors.Is(err, errEventPageFull) {
		return eventPage{}, err
	}
	return page, nil
}

// feedReach is the set of catalogues a reader is narrowed to, or nil for one that
// reads the whole feed. Only a machine credential states a reach, and only an events
// token reaches this route, so the ids are the catalogues its minter named. A reach of
// any other kind matches no row, which is the direction to fail in.
func feedReach(p *httpapi.Principal) map[string]bool {
	if p == nil {
		return nil
	}
	return reachSet(p.Reach)
}

// feedItem is the catalogue item a feed row is about.
func feedItem(e state.FeedEntry) string {
	switch {
	case e.Outcome != nil:
		return e.Outcome.ItemID
	case e.Granted != nil:
		return e.Granted.ItemID
	case e.Revoked != nil:
		return e.Revoked.ItemID
	}
	return ""
}

// itemHomes answers the home catalogue of an item, reading each item once per page. It
// reads the catalogue store off the run loop, as the approval page does (Server.
// catalogStore): its records are written atomically. An item the store does not have
// has no home — "" — which no reach contains, so its rows go only to a reader of the
// whole feed. A store that cannot be read is an error, never "no home": passing a row
// over moves the reader's cursor past it for good.
func (s *Server) itemHomes() func(itemID string) (string, error) {
	seen := map[string]string{}
	return func(itemID string) (string, error) {
		if itemID == "" {
			return "", nil
		}
		if home, ok := seen[itemID]; ok {
			return home, nil
		}
		it, found, err := s.catalogStore.Item(itemID)
		if err != nil {
			return "", err
		}
		home := ""
		if found {
			home = it.HomeCatalog
		}
		seen[itemID] = home
		return home, nil
	}
}

// eventSource is the CloudEvents `source` of this installation's catalogue facts: its
// external URL and `/catalog`, or — on a server that was given none — a URN naming the
// node, which is unique without claiming an address nobody can reach.
func (s *Server) eventSource(nodeID string) string {
	if base := strings.TrimRight(strings.TrimSpace(s.externalURL), "/"); base != "" {
		return base + "/catalog"
	}
	return "urn:atlas:" + nodeID + ":catalog"
}

// feedEnvelope wraps one feed row as a CloudEvent. Its id is the node, the partition
// and the log position, which no other fact shares and a re-read repeats. The data
// names people by id only (ADR-0314), and the catalogue that maintains the product as
// homeCatalog, where the item has one.
func feedEnvelope(e state.FeedEntry, nodeID, source, home string) cloudEvent {
	ev := cloudEvent{
		SpecVersion:     "1.0",
		ID:              nodeID + ":" + strconv.FormatUint(uint64(e.Partition), 10) + ":" + strconv.FormatUint(e.Position, 10),
		Source:          source,
		Time:            feedTime(e.At),
		DataContentType: "application/json",
	}
	switch {
	case e.Outcome != nil:
		o := e.Outcome
		ev.Type = o.EventType
		if ev.Type == "" {
			ev.Type = eventcatalog.ActionOutcomePrefix + o.Outcome
		}
		ev.Subject = positionSubject(o.OrderID, o.Position)
		data := map[string]any{
			"orderId": o.OrderID, "position": o.Position, "commandId": o.CommandID,
			"action": o.Action, "effect": o.Effect, "outcome": o.Outcome, "source": o.Source,
			"principal": o.Principal, "itemId": o.ItemID, "at": feedTime(o.At),
		}
		if o.VariantID != "" {
			data["variantId"] = o.VariantID
		}
		if o.InstanceKey != 0 {
			data["instanceKey"] = o.InstanceKey
		}
		if o.Result != "" && json.Valid([]byte(o.Result)) {
			data["result"] = json.RawMessage(o.Result)
		}
		ev.Data = data
	case e.Granted != nil:
		g := e.Granted
		ev.Type = eventcatalog.EntitlementGranted
		ev.Subject = holdSubject(g.Principal, g.ItemID, g.VariantID, g.OrderID)
		data := map[string]any{
			"principal": g.Principal, "itemId": g.ItemID, "orderId": g.OrderID,
			"since": feedTime(g.Since), "origin": g.Origin.String(),
		}
		if g.VariantID != "" {
			data["variantId"] = g.VariantID
		}
		if g.Until != 0 {
			data["until"] = feedTime(g.Until)
		}
		ev.Data = data
	case e.Revoked != nil:
		h := e.Revoked
		ev.Type = eventcatalog.EntitlementRevoked
		ev.Subject = holdSubject(h.Principal, h.ItemID, h.VariantID, h.OrderID)
		data := map[string]any{
			"principal": h.Principal, "itemId": h.ItemID, "orderId": h.OrderID,
			"since": feedTime(h.Since), "endedAt": feedTime(h.EndedAt),
			"reason": h.EndedReason.String(), "endedBy": h.EndedBy,
		}
		if h.VariantID != "" {
			data["variantId"] = h.VariantID
		}
		ev.Data = data
	}
	if data, ok := ev.Data.(map[string]any); ok && home != "" {
		data["homeCatalog"] = home
	}
	return ev
}

// positionSubject is the subject of a fact about one order position.
func positionSubject(orderID, position string) string {
	return "orders/" + url.PathEscape(orderID) + "/positions/" + url.PathEscape(position)
}

// holdSubject is the subject of a right: the order position it came from, or for a
// right no order produced — one a commissioning load adopted — the principal and the
// item.
func holdSubject(principal, itemID, variantID, orderID string) string {
	if orderID != "" {
		position := itemID
		if variantID != "" {
			position += "#" + variantID
		}
		return positionSubject(orderID, position)
	}
	return "principals/" + url.PathEscape(principal) + "/items/" + url.PathEscape(itemID)
}

// feedTime renders a unix-nanosecond moment as RFC 3339 in UTC.
func feedTime(ns int64) string {
	return time.Unix(0, ns).UTC().Format(time.RFC3339Nano)
}
