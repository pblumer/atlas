package api

import (
	"github.com/pblumer/atlas/api/sidecar"
)

// inboundSubscription is an operator-configured inbound event binding for a clio
// worker (ADR-0075): the bridge watches WatchedSubject on the worker's clio
// instance and republishes each new event as an Atlas message named MessageName,
// correlated on CorrelationKey (a FEEL expression over the event body; empty =
// keyless), so the event both starts message-start processes and wakes waiting
// message-catch instances. LastEventID is a *best-effort* cursor advanced after each
// batch is durably published: it only speeds a restart's resume. Correctness — no
// duplicate delivery across a crash — is guaranteed by the engine's durable
// high-water mark (ADR-0075), not by this cursor, so losing it re-reads harmlessly.
type inboundSubscription struct {
	ID             string `json:"id"`
	ConnectorID    string `json:"connectorId"`    // FK → a kind:"clio" worker record
	WatchedSubject string `json:"watchedSubject"` // clio subject path, e.g. "orders/new"
	Recursive      bool   `json:"recursive"`      // include the subject's subtree
	MessageName    string `json:"messageName"`    // published Atlas message name
	CorrelationKey string `json:"correlationKey"` // FEEL over the event body; "" = keyless
	Enabled        bool   `json:"enabled"`
	CreatedAt      int64  `json:"createdAt"`
	LastEventID    string `json:"lastEventId,omitempty"` // best-effort resume cursor
	// StartFromTip makes a new subscription forward-only: on first activation the
	// bridge primes its cursor past the subject's existing backlog WITHOUT
	// republishing it, so connecting a watch to a subject that already has history
	// does not start a process per historical event (the reported /employees flood,
	// ADR-0075). Defaults to true on create; set false to intentionally backfill the
	// whole history. Primed records that the one-time skip has completed.
	StartFromTip bool `json:"startFromTip"`
	Primed       bool `json:"primed,omitempty"`

	// The fields below belong to a jira watch (ADR-0214). Which set applies is decided
	// by the *worker's* kind, not by a discriminator stored here: resolveInboundSubs
	// already loads the worker record, so the kind it names is the discriminator and
	// a record written before these existed needs no migration.

	// JQL is the query a jira watch follows, written exactly as in Jira's own search
	// box. It carries no ORDER BY — the bridge owns the ordering, because the cursor's
	// progress depends on it — and the create endpoint refuses one that does.
	JQL string `json:"jql,omitempty"`
	// CursorField is the issue timestamp the watch follows and takes each event's
	// sequence from: "created" (the default) for new issues, "updated" for changes.
	// One field for both, deliberately: sequencing a new-issue watch on `updated`
	// would let an edit inside the lag window start a second instance for a ticket
	// already handled.
	CursorField string `json:"cursorField,omitempty"`
	// LagSeconds holds the cursor deliberately behind the newest issue seen, so an
	// issue the search index publishes late is still inside the next window. 0 uses
	// the default. Re-reading costs nothing: a jira event's mark is its own issue's.
	LagSeconds int `json:"lagSeconds,omitempty"`
	// PollSeconds is this watch's own cadence, 0 meaning the kind's default. The
	// bridge's ticker is shared and fast; a Jira site rate-limits per site, and
	// spending that budget on empty answers every two seconds is not what it is for.
	PollSeconds int `json:"pollSeconds,omitempty"`

	// The fields below belong to a Google watch (ADR-0234).
	// Which of the two it is follows from which target it names — a subscription names
	// exactly one, and the create endpoint refuses both or neither.

	// SpreadsheetID is a *row watch*: the spreadsheet whose new rows are published.
	// WatchRange is the A1 range read on each poll (normalized to a default when the
	// operator names none) and HeaderRow makes the range's first row the column names,
	// so the event's fields carry each row by name and a correlation key can say
	// `Antragsnummer` rather than index into a list.
	//
	// Its sequence is the row's own absolute number, which rises with every append and
	// never rises twice for the same row — so the mark stays the watch's scalar one.
	// The hole this leaves is stated in the record and in the Console: deleting a row
	// renumbers the tail, and a later append landing on a delivered number is not
	// delivered again. The sheets people watch are the append-only ones.
	SpreadsheetID string `json:"spreadsheetId,omitempty"`
	WatchRange    string `json:"watchRange,omitempty"`
	HeaderRow     bool   `json:"headerRow,omitempty"`
	// FolderID is a *file watch*: the Drive folder whose files are published, which is
	// the drop folder people already use as a queue. It has no sequence of its own —
	// files.list is a query and its order is an index's — so, exactly as for Jira, the
	// mark is scoped per file id and the sequence is the file's own timestamp.
	// CursorField ("created", the default, or "modified") selects which, and LagSeconds
	// holds the cursor behind the newest file so one indexed late is still inside the
	// next window.
	FolderID string `json:"folderId,omitempty"`

	// LastPolledAt is when this watch was last read, in unix seconds. It is what makes
	// PollSeconds a cadence rather than a wish, and like LastEventID it is
	// best-effort: losing it re-reads, which the marks make harmless.
	LastPolledAt int64 `json:"lastPolledAt,omitempty"`

	// The fields below are the loop guard (ADR-0225). A watch can
	// feed itself — a process started by an event writes to the system the watch reads,
	// the watch matches what it wrote — and nothing downstream can tell that apart from
	// a busy morning, because every instance is well-formed and every task succeeds.
	// Only the rate can, so the ceiling lives on the watch.

	// MaxPerHour caps how many events this watch may publish per hour; 0 means
	// defaultInboundPerHour. Crossing it switches the watch off rather than throttling
	// it: a runaway that is merely slowed is still a runaway, and a watch that stopped
	// is a state an operator can see and decide about.
	MaxPerHour int `json:"maxPerHour,omitempty"`
	// WindowStart is when the current budget window opened, in unix seconds, and
	// PublishedInWindow how many events have been published into it. Both are durable
	// so a restart does not hand a looping watch a fresh budget.
	WindowStart       int64 `json:"windowStart,omitempty"`
	PublishedInWindow int   `json:"publishedInWindow,omitempty"`
	// DisabledReason says why the guard switched this watch off, for whoever finds it
	// off later. Empty for a watch an operator disabled themselves — "it stopped"
	// without "why" is indistinguishable from a defect.
	DisabledReason string `json:"disabledReason,omitempty"`
}

// inboundSubStore is a durable store for inbound subscriptions, one JSON file per id
// under a directory — the same sidecar approach as the worker store
// (ADR-0019/0041). It is owned solely by the server's run-loop goroutine, so it needs
// no locking, and it holds no secret material.

// inboundSubStore is a durable store for inboundSubscription records, one JSON file per id
// under a single directory (ADR-0019). Like every design-time store it is owned
// solely by the server's run-loop goroutine, so it needs no locking of its own.
type inboundSubStore = sidecar.Store[inboundSubscription]

// newInboundSubStore opens (creating if needed) the inboundsub directory.
func newInboundSubStore(dir string) (*inboundSubStore, error) {
	return sidecar.NewStore(dir, "inboundsubstore",
		func(rec inboundSubscription) string { return rec.ID },
		sidecar.Order(func(a, b inboundSubscription) bool {
			if a.CreatedAt != b.CreatedAt {
				return a.CreatedAt < b.CreatedAt
			}
			return a.ID < b.ID
		}),
	)
}
