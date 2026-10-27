package wikiwikibangbang

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/pblumer/atlas/connector/wikiwikibangbang/catalog"
)

// A watch on the tasks a space finishes: the read the engine's inbound bridge makes so
// that a process can wait for a task to be done instead of asking get-item again and
// again.
//
// It follows the wiki's change feed (its ADR-0121), not a query over the items. The
// feed is the wiki's own log, read forward from a sequence number, so it is ordered,
// exact and cheap when nothing happened; an item answers no "updated" a query could
// page by. The feed says which items changed, not how, so the items it names are asked
// once more — the finished ones among them, in the watched space — and each is
// published with the time it reached its finished state (its stateSince). That time is
// the event's sequence: an edit of a finished task reads it again at the same time,
// which the engine's mark for the item skips, and a task reopened and finished again is
// finished at a later one, which it does not.
//
// The feed lists a change only when the account watched its space at the time, so the
// watch makes the account watch the space before it reads, and again after a while. It
// reads with a token of scope read and refuses any other: the engine makes these reads
// itself, and a token that can write stays with the worker (ADR-0498,
// ADR-draft-wikiwikibangbang-task-watch).

// watchSlack is how far before the wiki's clock at the start of a watch a task may have
// been finished and still be published: a task finished as the watch starts is
// published rather than lost to a clock the wiki's host set back a little. A task
// finished earlier than that is not, even when an edit puts it in the feed.
const watchSlack = time.Minute

// changesLimit is the most changes one read takes from the feed: the wiki's default
// page, and the most ids one question about them names.
const changesLimit = 100

// WatchRequest is one read of a watch.
type WatchRequest struct {
	// Space is the key of the space whose tasks are watched.
	Space string
	// Query narrows which finished tasks count, in the wiki's query language; empty is
	// every task of the space. It is grouped, so it narrows and never widens.
	Query string
	// Cursor is the cursor the previous read answered. An empty or unreadable one starts
	// the watch at the tip again.
	Cursor string
	// Limit is the most changes the read takes from the feed; zero or more than the
	// wiki's page is the page.
	Limit int
}

// Finished is a task the watched space finished.
type Finished struct {
	ID string
	// At is when the task reached its finished state.
	At time.Time
	// Item is the task as a process sees it, without its body.
	Item map[string]any
}

// WatchPage is what one read found.
type WatchPage struct {
	// Finished are the tasks finished since the cursor, in the order the feed named them.
	Finished []Finished
	// Cursor is where the next read continues.
	Cursor string
	// Gap says what the watch may have missed, when it had to move its cursor back.
	Gap string
}

// spaceRe is a space's key as the wiki mints it: upper-case letters and digits,
// starting with a letter.
var spaceRe = regexp.MustCompile(`^[A-Z][A-Z0-9]{0,63}$`)

// CheckSpace checks the space a watch names.
func CheckSpace(space string) error {
	if !spaceRe.MatchString(space) {
		return fmt.Errorf("wikiwikibangbang: the space %q is not a space key: upper-case letters and digits, starting with a letter, e.g. OPS", clip(space))
	}
	return nil
}

// orderByRe finds an ordering outside the texts of a query.
var orderByRe = regexp.MustCompile(`(?i)\border\s+by\b`)

// CheckWatchQuery checks a watch's own query. The watch puts it in parentheses before
// its own conditions, so a query must keep its parentheses and its texts closed — one
// that closes the watch's group would otherwise widen what the watch asks for — and
// must not order what the watch orders by the feed. Whether it means anything is the
// wiki's to say, on the first read.
func CheckWatchQuery(query string) error {
	query = strings.TrimSpace(query)
	if query == "" {
		return nil
	}
	if len(query) > 2000 {
		return fmt.Errorf("wikiwikibangbang: a watch's query is %d characters long; at most 2000", len(query))
	}
	depth := 0
	var outside strings.Builder
	for i := 0; i < len(query); i++ {
		switch ch := query[i]; ch {
		case '"', '\'':
			// A text runs to the same quote, a backslash taking the character after it, as
			// the wiki reads one.
			j := i + 1
			for ; j < len(query) && query[j] != ch; j++ {
				if query[j] == '\\' {
					j++
				}
			}
			if j >= len(query) {
				return fmt.Errorf("wikiwikibangbang: a watch's query leaves a text open: it is not closed with %c", ch)
			}
			i = j
			outside.WriteByte(' ')
		case '(':
			depth++
			outside.WriteByte(' ')
		case ')':
			if depth--; depth < 0 {
				return fmt.Errorf("wikiwikibangbang: a watch's query closes a parenthesis it did not open; the watch adds its own conditions after the query, and a query that closes their group would widen them")
			}
			outside.WriteByte(' ')
		default:
			outside.WriteByte(ch)
		}
	}
	if depth != 0 {
		return fmt.Errorf("wikiwikibangbang: a watch's query leaves a parenthesis open")
	}
	if orderByRe.MatchString(outside.String()) {
		return fmt.Errorf("wikiwikibangbang: a watch's query must not carry an ORDER BY: the watch publishes in the order of the wiki's change feed")
	}
	return nil
}

// WatchTip starts a watch: it makes the account watch the space, then reads where the
// feed ends and the wiki's clock. Nothing before that is published.
func (c *Client) WatchTip(ctx context.Context, space string) (string, error) {
	if err := c.watchable(ctx, space, ""); err != nil {
		return "", err
	}
	return c.tip(ctx)
}

// FinishedSince reads the tasks the space finished after the cursor.
func (c *Client) FinishedSince(ctx context.Context, req WatchRequest) (WatchPage, error) {
	if err := c.watchable(ctx, req.Space, req.Query); err != nil {
		return WatchPage{}, err
	}
	seq, since, ok := parseWatchCursor(req.Cursor)
	if !ok {
		cursor, err := c.tip(ctx)
		return WatchPage{Cursor: cursor}, err
	}
	limit := req.Limit
	if limit <= 0 || limit > changesLimit {
		limit = changesLimit
	}
	var feed changesAnswer
	q := url.Values{"after": {strconv.Itoa(seq)}, "limit": {strconv.Itoa(limit)}}
	if err := c.call(ctx, "read the change feed", http.MethodGet, "/api/changes", q, nil, &feed); err != nil {
		return WatchPage{}, err
	}
	if feed.Last < seq {
		// The feed ends before the cursor: the wiki was restored from a backup, and what
		// it numbered since then reuses numbers the cursor has passed. Continuing from its
		// end is the one place that is not a guess. The account's watch of the space is a
		// change of the log too and may be gone with it, so the next read makes it again.
		c.mu.Lock()
		delete(c.watchedAt, req.Space)
		c.mu.Unlock()
		return WatchPage{
			Cursor: watchCursor(feed.Last, since),
			Gap: fmt.Sprintf("the wiki's change feed ends at %d, before this watch's cursor %d, as after a restore from a backup; "+
				"a task finished in the restored wiki before now is not published", feed.Last, seq),
		}, nil
	}
	next := watchCursor(feed.Last, since)
	var named []string
	seen := map[string]bool{}
	for _, ch := range feed.Changes {
		// A change with no item is one on a whole space, or on an item the account may
		// not read now; neither is a task this watch can publish.
		if ch.Item == "" || seen[ch.Item] || catalog.CheckRef("item", ch.Item) != nil {
			continue
		}
		seen[ch.Item] = true
		named = append(named, ch.Item)
	}
	if len(named) == 0 {
		return WatchPage{Cursor: next}, nil
	}
	items, err := c.finished(ctx, req.Space, req.Query, named)
	if err != nil {
		return WatchPage{}, err
	}
	out := []Finished{}
	for _, id := range named {
		it, ok := items[id]
		if !ok {
			continue
		}
		raw, _ := it["stateSince"].(string)
		at, err := time.Parse(time.RFC3339Nano, raw)
		if err != nil || at.Before(since) {
			// A task with no time it was finished at cannot be put in order, and one
			// finished before the watch began is not what the watch is for.
			continue
		}
		out = append(out, Finished{ID: id, At: at, Item: summary(it, false)})
	}
	return WatchPage{Finished: out, Cursor: next}, nil
}

// changesAnswer is the wiki's change feed.
type changesAnswer struct {
	Changes []struct {
		Seq  int    `json:"seq"`
		Item string `json:"item"`
	} `json:"changes"`
	Last int `json:"last"`
}

// watchable checks a watch's space, query and token, and makes sure the account
// watches the space.
func (c *Client) watchable(ctx context.Context, space, query string) error {
	if err := CheckSpace(space); err != nil {
		return err
	}
	if err := CheckWatchQuery(query); err != nil {
		return err
	}
	_, scope, err := c.standing(ctx)
	if err != nil {
		return err
	}
	if scope != "read" {
		return fmt.Errorf("wikiwikibangbang: a watch reads with a token of scope read, and this Worker's token has scope %q. "+
			"The engine reads the wiki's change feed itself, and a token that can write stays with the worker: create a token "+
			"of scope read for an account that is a viewer of the space, and configure it as a Worker of its own", scope)
	}
	c.mu.Lock()
	at, ok := c.watchedAt[space]
	fresh := ok && c.now().Sub(at) < standingFor
	c.mu.Unlock()
	if fresh {
		return nil
	}
	// Watching is a write the wiki allows a token of scope read. It is made again after
	// a while because somebody may have stopped it in the wiki, and a feed gone quiet
	// that way looks like a quiet space.
	err = c.call(ctx, "watch space "+space, http.MethodPut, "/api/spaces/"+url.PathEscape(space)+"/watch", nil, nil, nil)
	if nf := (*notFound)(nil); errors.As(err, &nf) {
		return fmt.Errorf("wikiwikibangbang: the space %q does not exist, or the Worker's account holds no role in it; a watch needs the account to be a viewer of the space", space)
	}
	if err != nil {
		return err
	}
	c.mu.Lock()
	if c.watchedAt == nil {
		c.watchedAt = map[string]time.Time{}
	}
	c.watchedAt[space] = c.now()
	c.mu.Unlock()
	return nil
}

// tip reads where the feed ends, and the wiki's clock less the slack as the time from
// which a finished task is published. The wiki's clock is its Date, so the time is
// compared with times the same clock wrote.
func (c *Client) tip(ctx context.Context) (string, error) {
	var feed changesAnswer
	h, err := c.send(ctx, "read the change feed", http.MethodGet, "/api/changes", nil, nil, &feed)
	if err != nil {
		return "", err
	}
	at, err := http.ParseTime(h.Get("Date"))
	if err != nil {
		at = c.now()
	}
	return watchCursor(feed.Last, at.Add(-watchSlack)), nil
}

// finished asks which of the named items are finished tasks of the space, narrowed by
// the watch's query, by id.
func (c *Client) finished(ctx context.Context, space, query string, ids []string) (map[string]map[string]any, error) {
	var b strings.Builder
	if query = strings.TrimSpace(query); query != "" {
		b.WriteString("(" + query + ") AND ")
	}
	// The space is a checked key and every id a checked reference: neither holds a quote.
	b.WriteString(`space = "` + space + `" AND finished = true AND id IN (`)
	for i, id := range ids {
		if i > 0 {
			b.WriteString(", ")
		}
		b.WriteString(`"` + id + `"`)
	}
	b.WriteString(")")
	out := map[string]map[string]any{}
	asked := map[string]bool{}
	for _, id := range ids {
		asked[id] = true
	}
	after := ""
	for {
		q := url.Values{"query": {b.String()}, "limit": {strconv.Itoa(min(pageLimit, len(ids)))}}
		if after != "" {
			q.Set("after", after)
		}
		var a struct {
			Items []map[string]any `json:"items"`
			Next  string           `json:"next"`
		}
		if err := c.call(ctx, "query the finished tasks", http.MethodGet, "/api/items", q, nil, &a); err != nil {
			return nil, err
		}
		for _, it := range a.Items {
			// Only what was asked for, of the space asked about: the answer is checked
			// against the question rather than trusted to it.
			if id, _ := it["id"].(string); asked[id] && it["space"] == space {
				out[id] = it
			}
		}
		if a.Next == "" || len(a.Items) == 0 || len(out) >= len(ids) {
			return out, nil
		}
		after = a.Next
	}
}

// watchCursor is a watch's cursor: the feed's sequence and the time from which a
// finished task is published.
func watchCursor(seq int, since time.Time) string {
	return strconv.Itoa(seq) + "|" + since.UTC().Format(time.RFC3339Nano)
}

// parseWatchCursor reads a watch's cursor.
func parseWatchCursor(cursor string) (int, time.Time, bool) {
	raw, rawSince, ok := strings.Cut(strings.TrimSpace(cursor), "|")
	if !ok {
		return 0, time.Time{}, false
	}
	seq, err := strconv.Atoi(raw)
	if err != nil || seq < 0 {
		return 0, time.Time{}, false
	}
	since, err := time.Parse(time.RFC3339Nano, rawSince)
	if err != nil {
		return 0, time.Time{}, false
	}
	return seq, since, true
}
