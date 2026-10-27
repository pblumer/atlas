package api

import (
	"context"
	"crypto/sha256"
	"log/slog"
	"strings"
	"time"

	"github.com/pblumer/atlas/connector/nettimeout"
	"github.com/pblumer/atlas/connector/wikiwikibangbang"
	"github.com/pblumer/atlas/logging"
)

// --- wikiwikibangbang ---
//
// A wikiwikibangbang watch publishes the tasks a space of the wiki finishes, so a
// process that opened a task can wait for it at a message catch event correlated on the
// task's id, instead of asking get-item again and again
// (ADR-draft-wikiwikibangbang-task-watch).
//
// The Worker Type is worker-only: the engine runs none of its tasks and holds no token
// that can write (ADR-0498). The watch is the one read the engine makes itself, as it
// does for every other kind it watches, and it makes it only with a token of scope
// read — the connector refuses any other before it reads. An operator who wants both
// configures two Workers: one whose token writes, which the supervised worker runs
// tasks with, and one whose token only reads, which this watch reads with.

// wikiWatchVariable is the one variable a wikiwikibangbang watch hands a process: the
// event, as an object — wikiEvent.itemKey, wikiEvent.item.properties.
const wikiWatchVariable = "wikiEvent"

// wikiDefaultPoll is how often a wikiwikibangbang watch is read when it names no cadence
// of its own. A read with nothing new is one request for the feed and nothing else, to
// a wiki an organisation runs itself; a person who finished a task is waiting for what
// comes next, so it sits between Discord's quarter minute and Jira's minute.
const wikiDefaultPoll = 30 * time.Second

// wikiWatchBudget bounds one read of a wikiwikibangbang watch: the standing, the watch
// of the space, the feed and one question about the items it named, each under the
// shared worker budget.
const wikiWatchBudget = 3 * nettimeout.Default

// wikiWatchClientFor is how long the engine reads with a client before it resolves the
// Worker's credential again. Resolving it is a vault read on the run loop, which a
// watch due every few ticks should not make on every one; a token rotated in the vault
// is read with within this time.
const wikiWatchClientFor = 5 * time.Minute

// wikiWatchClient is a client the engine reads one Worker's wiki with, and what it was
// built from.
type wikiWatchClient struct {
	endpoint, ref string
	sum           [sha256.Size]byte
	builtAt       time.Time
	client        *wikiwikibangbang.Client
}

// wikiWatchClientOf answers the client a watch on a wikiwikibangbang Worker reads with,
// building it when the Worker's endpoint or credential changed. A client that is kept
// keeps what it learnt: the account's standing and the spaces it made sure to watch.
//
// It reads the vault and the run-loop owned cache, so it runs on the run loop.
func (s *Server) wikiWatchClientOf(c connector, now time.Time) (*wikiwikibangbang.Client, error) {
	endpoint := strings.TrimSpace(c.Endpoint)
	cached, ok := s.wikiWatchClients[c.ID]
	if ok && cached.endpoint == endpoint && cached.ref == c.CredentialsRef && now.Sub(cached.builtAt) < wikiWatchClientFor {
		return cached.client, nil
	}
	bundle := s.resolveConnectorSecret(c.CredentialsRef)
	sum := sha256.Sum256([]byte(bundle))
	if ok && cached.endpoint == endpoint && cached.sum == sum {
		cached.ref, cached.builtAt = c.CredentialsRef, now
		s.wikiWatchClients[c.ID] = cached
		return cached.client, nil
	}
	client, err := wikiwikibangbang.NewClient(endpoint, bundle, wikiwikibangbang.Options{})
	if err != nil {
		delete(s.wikiWatchClients, c.ID)
		return nil, err
	}
	if s.wikiWatchClients == nil {
		s.wikiWatchClients = map[string]wikiWatchClient{}
	}
	s.wikiWatchClients[c.ID] = wikiWatchClient{endpoint: endpoint, ref: c.CredentialsRef, sum: sum, builtAt: now, client: client}
	return client, nil
}

// wikiSource reads the tasks one space of a wiki finishes
// (ADR-draft-wikiwikibangbang-task-watch).
//
// The wiki's change feed is a log, but what the watch publishes is a state — finished —
// that a task can leave and reach again. So the mark is one per task, as Jira's is one
// per issue, and the sequence is when the task reached its finished state: an edit of
// a finished task is read again at the same time and skipped, a task finished again
// after it was reopened is published again.
type wikiSource struct{ client *wikiwikibangbang.Client }

func (s wikiSource) Read(ctx context.Context, rec inboundSubscription, limit int) ([]inboundEvent, string, error) {
	ctx, cancel := context.WithTimeout(ctx, wikiWatchBudget)
	defer cancel()
	page, err := s.client.FinishedSince(ctx, wikiwikibangbang.WatchRequest{
		Space: rec.WikiSpace, Query: rec.WikiQuery, Cursor: rec.LastEventID, Limit: limit,
	})
	if err != nil {
		return nil, "", err
	}
	if page.Gap != "" {
		logging.Warn(logging.InboundWatchGap, "a wikiwikibangbang watch moved its cursor back: "+page.Gap,
			slog.String("subscription", rec.ID), slog.String("messageName", rec.MessageName))
	}
	out := make([]inboundEvent, 0, len(page.Finished))
	for _, f := range page.Finished {
		ms := f.At.UnixMilli()
		if ms <= 0 {
			// No time, no sequence; the engine would read a zero as already applied.
			continue
		}
		event := wikiFields(f)
		// As for mail: a correlation key may name the flat field or the object's
		// (itemId, wikiEvent.itemId); the instance gets the object alone.
		fields := make(map[string]any, len(event)+1)
		for k, v := range event {
			fields[k] = v
		}
		fields[wikiWatchVariable] = event
		out = append(out, inboundEvent{MarkKey: f.ID, Seq: uint64(ms), Fields: fields,
			Payload: map[string]any{wikiWatchVariable: event}})
	}
	return out, page.Cursor, nil
}

func (s wikiSource) Prime(ctx context.Context, rec inboundSubscription) (string, bool, error) {
	// One read reaches the tip: what is skipped is everything before now, and the feed
	// says where now is.
	ctx, cancel := context.WithTimeout(ctx, wikiWatchBudget)
	defer cancel()
	cursor, err := s.client.WatchTip(ctx, rec.WikiSpace)
	if err != nil {
		return "", false, err
	}
	return cursor, true, nil
}

// wikiFields is what a finished task's event says: a curated envelope, and the task as
// the Worker's own operations return it under item.
func wikiFields(f wikiwikibangbang.Finished) map[string]any {
	return map[string]any{
		"eventType":  "wikiwikibangbang.item.finished",
		"itemId":     f.ID,
		"itemKey":    f.Item["key"],
		"itemType":   f.Item["type"],
		"space":      f.Item["space"],
		"title":      f.Item["title"],
		"state":      f.Item["state"],
		"finishedAt": f.At.UTC().Format(time.RFC3339Nano),
		"item":       f.Item,
	}
}

// validateWikiWatch checks a watch on a wikiwikibangbang Worker. It names a space and
// may narrow it with a query of the wiki's own language, and nothing that belongs to
// another kind's watch. The feed is a log the watch follows by its own number, so there
// is no cursor field to choose and no lag to tune.
func validateWikiWatch(rec *inboundSubscription) string {
	if rec.WatchedSubject != "" || rec.Recursive || rec.JQL != "" || rec.SpreadsheetID != "" ||
		rec.FolderID != "" || rec.ChannelID != "" || rec.WatchRange != "" || rec.HeaderRow {
		return "watchedSubject, recursive, jql, spreadsheetId, folderId, channelId, watchRange and headerRow " +
			"belong to another kind's watch; a wikiwikibangbang watch names a wikiSpace"
	}
	if rec.CursorField != "" || rec.LagSeconds != 0 {
		return "cursorField and lagSeconds belong to a jira or google watch; a wikiwikibangbang watch follows " +
			"the wiki's change feed by its own number"
	}
	if rec.PollSeconds < 0 {
		return "pollSeconds cannot be negative"
	}
	if rec.WikiSpace == "" {
		return "wikiSpace is required for a wikiwikibangbang watch: the key of the space whose finished tasks " +
			"are published, e.g. OPS"
	}
	if err := wikiwikibangbang.CheckSpace(rec.WikiSpace); err != nil {
		return strings.TrimPrefix(err.Error(), "wikiwikibangbang: ")
	}
	if err := wikiwikibangbang.CheckWatchQuery(rec.WikiQuery); err != nil {
		return strings.TrimPrefix(err.Error(), "wikiwikibangbang: ")
	}
	// The wiki's feed lists a change only while the account watched the space at the
	// time, and the watch starts watching it on its first read — so there is no history
	// to replay. Said here, as for Gmail, where the person asking can change their mind.
	if !rec.StartFromTip {
		return "a wikiwikibangbang watch cannot backfill: the wiki's change feed lists only what the Worker's " +
			"account watched at the time, so a watch starts at the newest change. Leave startFromTip on."
	}
	return ""
}
