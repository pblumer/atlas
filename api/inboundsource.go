package api

import (
	"context"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"time"

	"github.com/pblumer/atlas/connector/clio"
	"github.com/pblumer/atlas/connector/googlesheets"
	"github.com/pblumer/atlas/connector/jira"
	"github.com/pblumer/atlas/logging"
)

// The inbound bridge (ADR-0075) was a clio reader with the generic parts factored out
// below it: the polling, the priming, the cursor and the publish knew what a subject
// was. ADR-0214 gives it a second source, so what a source *is* moves behind this
// interface and the bridge keeps only the parts that were never clio's.
//
// The split is the one the engine already draws. A source produces events with a
// sequence; the engine deduplicates on that sequence against a durable high-water mark
// (ADR-0075); the bridge is the piece in between, and it never learns what a subject or
// a JQL is.

// inboundEvent is one event from any source, reduced to what the bridge needs: the
// idempotency mark it is deduplicated under, and the fields a correlation key is
// evaluated over and the started instance is seeded with.
type inboundEvent struct {
	// MarkKey scopes the engine's high-water mark. Empty means the watch's own scalar
	// mark, which is right when the source's sequence is monotonic across the whole
	// watch — a clio event id is its partition's sequence, so clio leaves it empty. A
	// non-empty value gives *this* event's subject its own mark, which is what lets a
	// source whose order is a query's rather than a log's be correct at all (ADR-0214).
	MarkKey string
	// Seq is the sequence the engine compares against that mark. A replay carries the
	// same value and is skipped; anything at or below the mark is already applied.
	Seq uint64
	// Fields is the binding environment for the correlation key and the payload the
	// started or woken instance receives.
	Fields map[string]any
}

// inboundSource reads one kind of watch. Read returns a bounded page and the resume
// cursor to store after it; Prime advances that cursor past a backlog *without*
// publishing, one bounded step per call, reporting done when the tip is reached.
//
// Prime is a step rather than a loop because clio's backlog skip is deliberately spread
// across polls: a large history is drained without handing the run loop one unbounded
// batch. A source that reaches its tip in one call (Jira reads the newest issue and
// stops) simply answers done on the first.
type inboundSource interface {
	Read(ctx context.Context, rec inboundSubscription, limit int) (page []inboundEvent, cursor string, err error)
	Prime(ctx context.Context, rec inboundSubscription) (cursor string, done bool, err error)
}

// --- clio ---

// clioSource reads a clio subject. It is today's behaviour expressed through the
// interface: the event id is both the cursor and the sequence, and the mark stays the
// watch's own scalar one, so no existing subscription's mark changes meaning.
type clioSource struct{ client clio.Client }

func (s clioSource) Read(ctx context.Context, rec inboundSubscription, limit int) ([]inboundEvent, string, error) {
	events, err := s.client.ReadEvents(ctx, clio.ReadEventsRequest{
		Subject:   rec.WatchedSubject,
		AfterID:   rec.LastEventID,
		Recursive: rec.Recursive,
		Limit:     limit,
	})
	if err != nil || len(events) == 0 {
		return nil, "", err
	}
	out := make([]inboundEvent, len(events))
	for i, ev := range events {
		out[i] = inboundEvent{Seq: clioSeq(ev.ID), Fields: clioFields(ev)}
	}
	return out, events[len(events)-1].ID, nil
}

func (s clioSource) Prime(ctx context.Context, rec inboundSubscription) (string, bool, error) {
	events, err := s.client.ReadEvents(ctx, clio.ReadEventsRequest{
		Subject:   rec.WatchedSubject,
		AfterID:   rec.LastEventID,
		Recursive: rec.Recursive,
		Limit:     inboundPrimeBatch,
	})
	if err != nil {
		return "", false, err
	}
	var cursor string
	if len(events) > 0 {
		cursor = events[len(events)-1].ID
	}
	// A short (or empty) page reached the tip.
	return cursor, len(events) < inboundPrimeBatch, nil
}

// clioSeq parses a clio event id — a per-partition monotonic sequence rendered as a
// decimal string — into the uint64 the engine deduplicates on (ADR-0075). clio events
// carry no separate `seq` field; the id itself is the order. A non-numeric id (not a
// real clio event) yields 0, which the engine treats as already-applied and skips, so a
// garbled line can never double-start a process.
func clioSeq(id string) uint64 {
	n, err := strconv.ParseUint(id, 10, 64)
	if err != nil {
		return 0
	}
	return n
}

// clioFields is the binding environment a clio event exposes: the event body, plus four
// reserved envelope fields the body cannot see on its own. subjectTail is the last
// '/'-segment of the subject — "E-123456" for "/employees/E-123456" — so a watch on a
// parent subject can key on the child id. The envelope fields take precedence over a
// body field of the same name, so a subscription can always rely on them.
func clioFields(ev clio.InboundEvent) map[string]any {
	fields := make(map[string]any, len(ev.Data)+4)
	for k, v := range ev.Data {
		fields[k] = v
	}
	fields["subject"] = ev.Subject
	fields["subjectTail"] = subjectTail(ev.Subject)
	fields["eventType"] = ev.Type
	fields["eventId"] = ev.ID
	return fields
}

// subjectTail returns the last '/'-separated segment of a clio subject (trailing
// slashes ignored): the leaf id an event is scoped to.
func subjectTail(subject string) string {
	trimmed := strings.TrimRight(subject, "/")
	if i := strings.LastIndex(trimmed, "/"); i >= 0 {
		return trimmed[i+1:]
	}
	return trimmed
}

// --- jira ---

// jiraCursorLayout is the only date format JQL compares against, and its granularity is
// the reason the cursor cannot also be the idempotency mark: a minute is a wide window,
// and Jira's search is served from an index that lags the write, so a poll re-reads and
// can be a moment behind. The per-issue mark (ADR-0214) is what makes both harmless.
const jiraCursorLayout = "2006/01/02 15:04"

// jiraDefaultLag is how far behind the newest issue seen the cursor is deliberately
// held. An issue indexed a little late is then still inside the next query's window;
// re-reading it costs one skipped publish, because its mark is its own.
const jiraDefaultLag = 2 * time.Minute

// jiraSource reads issues a JQL matches, newest last. Unlike clio it takes its sequence
// and its mark from the issue rather than from the read: the mark is scoped per issue,
// so no delivery order can make one issue suppress another, and the cursor carries no
// correctness weight at all — only progress (ADR-0214).
type jiraSource struct {
	client jira.Client
	now    func() time.Time // injectable for tests; time.Now in production
}

func (s jiraSource) Read(ctx context.Context, rec inboundSubscription, limit int) ([]inboundEvent, string, error) {
	raw, err := s.client.Do(ctx, jira.Request{
		Operation:  "search",
		JQL:        jiraWatchJQL(rec),
		MaxResults: int32(limit),
	})
	if err != nil {
		return nil, "", err
	}
	issues, _ := raw.([]any)
	if len(issues) == 0 {
		return nil, "", nil
	}
	field := jiraCursorField(rec)
	out := make([]inboundEvent, 0, len(issues))
	newest := time.Time{}
	for _, it := range issues {
		issue, _ := it.(map[string]any)
		if issue == nil {
			continue
		}
		id, _ := issue["id"].(string)
		if id == "" {
			continue // not an issue; nothing to key a mark on
		}
		at := jiraFieldTime(issue, field)
		if at.IsZero() {
			// No usable timestamp, so no sequence — and an event with no sequence has
			// no place in a mechanism whose whole correctness rests on one. Skipping is
			// not merely tidy: uint64(time.Time{}.UnixMilli()) is a *negative* millis
			// count wrapped to near the top of the range, which would set this issue's
			// mark so high that nothing about it is ever delivered again.
			continue
		}
		out = append(out, inboundEvent{
			MarkKey: id,
			Seq:     uint64(at.UnixMilli()),
			Fields:  jiraFields(rec, issue),
		})
		if at.After(newest) {
			newest = at
		}
	}
	if newest.IsZero() {
		// Nothing carried a usable timestamp: leave the cursor where it was rather than
		// stepping it somewhere arbitrary. The marks still make a re-read harmless.
		return out, "", nil
	}
	// A page that filled the bridge's cap stopped at the cap, not at the end of the
	// result set — which is what decides whether the safety lag may apply.
	return out, jiraNextCursor(rec, newest, len(issues) >= limit), nil
}

// jiraNextCursor is where the watch resumes, and the one rule it may not break is that
// a full page moves it.
//
// The lag holds the cursor deliberately behind the newest issue seen, so an issue the
// search index publishes late is still inside the next window; its own mark makes that
// re-read free (ADR-0214). That is right at the tip, where a page is short because there
// is nothing more to read. It is wrong behind a *full* page: there the read stopped at
// the bridge's cap with more issues behind it, and a cursor held back into the page just
// read asks the next poll for the same issues again. Nothing then ever advances — the
// watch re-reads and re-publishes one page every tick, spends the site's whole API
// budget doing it, and never reaches the issue behind the page. That is not a slow
// watch, it is a stopped one, and it is indistinguishable from a busy Atlas because the
// publishes are real work that the engine correctly discards.
func jiraNextCursor(rec inboundSubscription, newest time.Time, full bool) string {
	at := newest
	if !full {
		at = at.Add(-jiraLag(rec))
	}
	cursor := at.UTC().Format(jiraCursorLayout)
	if !full || cursor != strings.TrimSpace(rec.LastEventID) {
		return cursor
	}
	// The residual case the cursor's granularity cannot express: a full page whose
	// issues all share one minute, read from a cursor that is already that minute. JQL
	// compares to the minute, so no cursor separates them and time paging cannot drain
	// the page however often it is retried. Step past the minute instead. It skips the
	// issues in that one minute beyond the cap, which is why it is logged rather than
	// done quietly — but a watch that cannot move skips every issue after it, for ever.
	logging.Warn(logging.InboundWatchMinuteOverflowed,
		"a jira watch read a full page of issues that all share one minute; stepping the cursor past it, "+
			"which skips the issues in that minute beyond the batch limit. Narrow the watch's jql or raise the batch limit.",
		slog.String("subscription", rec.ID),
		slog.String("messageName", rec.MessageName),
		slog.String("cursorField", jiraCursorField(rec)),
		slog.String("minute", cursor))
	return at.Add(time.Minute).UTC().Format(jiraCursorLayout)
}

func (s jiraSource) Prime(ctx context.Context, rec inboundSubscription) (string, bool, error) {
	// One descending read of a single issue reaches the tip; there is no backlog to
	// page through, because what is being skipped is everything *before* now.
	raw, err := s.client.Do(ctx, jira.Request{
		Operation:  "search",
		JQL:        "(" + strings.TrimSpace(rec.JQL) + ") ORDER BY " + jiraCursorField(rec) + " DESC",
		MaxResults: 1,
	})
	if err != nil {
		return "", false, err
	}
	// An empty project primes to now: there is nothing to skip, and the next poll's
	// window starts here rather than at the beginning of time.
	cursor := s.now().UTC().Add(-jiraLag(rec)).Format(jiraCursorLayout)
	if issues, _ := raw.([]any); len(issues) > 0 {
		if issue, _ := issues[0].(map[string]any); issue != nil {
			if at := jiraFieldTime(issue, jiraCursorField(rec)); !at.IsZero() {
				cursor = at.Add(-jiraLag(rec)).UTC().Format(jiraCursorLayout)
			}
		}
	}
	return cursor, true, nil
}

// jiraWatchJQL composes what is actually asked of Jira: the watch's own query, narrowed
// by the resume cursor and ordered by the cursor field.
//
// The bridge owns the ordering, which is why a watch JQL carrying its own ORDER BY is
// refused when the watch is created: the cursor's progress depends on the last issue of
// a page being the newest one.
func jiraWatchJQL(rec inboundSubscription) string {
	field := jiraCursorField(rec)
	jql := "(" + strings.TrimSpace(rec.JQL) + ")"
	if c := strings.TrimSpace(rec.LastEventID); c != "" {
		jql += ` AND ` + field + ` >= "` + c + `"`
	}
	return jql + " ORDER BY " + field + " ASC"
}

// jiraCursorField is the issue timestamp a watch follows: `created` for new issues (the
// default), `updated` for a watch on changes. It is both what the query filters and
// orders on AND what the event's sequence is taken from, and those must be the same
// field — a watch for new issues that sequenced on `updated` would let an edit inside
// the lag window pass the mark and start a second instance for a ticket already
// handled (ADR-0214).
func jiraCursorField(rec inboundSubscription) string {
	if strings.TrimSpace(rec.CursorField) == "updated" {
		return "updated"
	}
	return "created"
}

// jiraLag is how far behind the newest issue the cursor is held, in seconds; 0 uses the
// default. It is a knob rather than a constant because how late an index runs is a
// property of the site, not of Atlas.
func jiraLag(rec inboundSubscription) time.Duration {
	if rec.LagSeconds > 0 {
		return time.Duration(rec.LagSeconds) * time.Second
	}
	return jiraDefaultLag
}

// jiraFieldTime reads one of an issue's timestamp fields. Jira renders them as
// "2006-01-02T15:04:05.000-0700"; a value that does not parse yields the zero time,
// which the caller treats as "no usable timestamp" rather than as the epoch.
func jiraFieldTime(issue map[string]any, name string) time.Time {
	fields, _ := issue["fields"].(map[string]any)
	if fields == nil {
		return time.Time{}
	}
	raw, _ := fields[name].(string)
	if raw == "" {
		return time.Time{}
	}
	for _, layout := range []string{"2006-01-02T15:04:05.000-0700", time.RFC3339} {
		if t, err := time.Parse(layout, raw); err == nil {
			return t
		}
	}
	return time.Time{}
}

// jiraFields is the binding environment a Jira issue exposes: a bounded envelope plus
// the whole issue as one value.
//
// Bounded on purpose. Flattening an issue would seed one variable per field, and a Jira
// project with custom fields has dozens — most of which no model reads. What is named
// here is what a correlation key or a gateway actually uses; everything else stays
// reachable through `issue`.
func jiraFields(rec inboundSubscription, issue map[string]any) map[string]any {
	fields, _ := issue["fields"].(map[string]any)
	out := map[string]any{
		"eventType": "jira.issue." + jiraCursorField(rec),
		"issueId":   issue["id"],
		"issueKey":  issue["key"],
		"issue":     issue,
	}
	if fields == nil {
		return out
	}
	out["summary"] = fields["summary"]
	out["created"] = fields["created"]
	out["updated"] = fields["updated"]
	if p, _ := fields["project"].(map[string]any); p != nil {
		out["projectKey"] = p["key"]
	}
	if it, _ := fields["issuetype"].(map[string]any); it != nil {
		out["issueType"] = it["name"]
	}
	if st, _ := fields["status"].(map[string]any); st != nil {
		out["status"] = st["name"]
	}
	if r, _ := fields["reporter"].(map[string]any); r != nil {
		out["reporter"] = r["displayName"]
	}
	return out
}

// --- google sheets and drive ---

// The two Google watches (ADR-draft-google-inbound-watch), which are two answers to
// the one question the bridge asks of a source: what is this event's sequence.
//
// A spreadsheet row has a real one — its row number rises with every append and never
// rises twice for the same row — so a row watch uses the *scalar* watch-level mark,
// the simple case clio has. A Drive file has none: files.list is a query, its order is
// an index's, and a file indexed late would be lost behind a scalar mark. That is the
// wall ADR-0214 hit with Jira, and the fix transfers whole — the mark is scoped per
// file id, the sequence is the file's own timestamp, and the cursor is held behind the
// newest file seen.

// sheetsDefaultRange is what a row watch reads when the operator names none: the first
// sheet, columns A to Z. It is normalized into the record at create time rather than
// applied here, so nothing downstream has to interpret an empty range.
const sheetsDefaultRange = "A:Z"

// driveDefaultLag is how far behind the newest file seen a folder watch's cursor is
// held, so a file Drive indexes a moment late is still inside the next window.
// Re-reading it costs one skipped publish, because its mark is its own.
const driveDefaultLag = 30 * time.Second

// sheetRowSource watches a spreadsheet's rows. The cursor is how many rows have been
// seen; every row beyond it is one event, sequenced by its absolute row number.
type sheetRowSource struct{ client googlesheets.Client }

func (s sheetRowSource) Read(ctx context.Context, rec inboundSubscription, limit int) ([]inboundEvent, string, error) {
	rows, header, err := s.rows(ctx, rec)
	if err != nil || len(rows) == 0 {
		return nil, "", err
	}
	seen := sheetRowsSeen(rec)
	if len(rows) <= seen {
		// Nothing new — or fewer rows than last time, which means rows were deleted.
		// The cursor follows the sheet down so the next append is noticed at all; the
		// rows in between keep the marks they already have, and the record says plainly
		// that those are lost. There is no repair: a row's only identity is its place.
		if len(rows) < seen {
			return nil, strconv.Itoa(len(rows)), nil
		}
		return nil, "", nil
	}
	out := make([]inboundEvent, 0, limit)
	for i := seen; i < len(rows) && len(out) < limit; i++ {
		out = append(out, inboundEvent{
			// No MarkKey: the row number *is* the watch's sequence, monotonic across
			// the whole watch, so the scalar mark is correct — the clio case.
			Seq:    uint64(i + 1), // absolute row number, 1-based as Sheets counts
			Fields: sheetRowFields(rec, header, rows[i], i+1),
		})
	}
	return out, strconv.Itoa(seen + len(out)), nil
}

func (s sheetRowSource) Prime(ctx context.Context, rec inboundSubscription) (string, bool, error) {
	rows, _, err := s.rows(ctx, rec)
	if err != nil {
		return "", false, err
	}
	// One read reaches the tip: what is being skipped is every row already there.
	return strconv.Itoa(len(rows)), true, nil
}

// rows reads the watched range and splits off the header when the watch names one.
// The header row is not an event — it names the columns — so it is removed from the
// rows *and* still counted, which keeps a row's sequence its real row number.
func (s sheetRowSource) rows(ctx context.Context, rec inboundSubscription) ([]any, []string, error) {
	got, err := s.client.Do(ctx, googlesheets.Request{
		Operation:   "read-range",
		Spreadsheet: rec.SpreadsheetID,
		Range:       sheetWatchRange(rec),
	})
	if err != nil {
		return nil, nil, err
	}
	rows, _ := got.([]any)
	if !rec.HeaderRow || len(rows) == 0 {
		return rows, nil, nil
	}
	first, _ := rows[0].([]any)
	names := make([]string, len(first))
	for i, cell := range first {
		names[i] = strings.TrimSpace(fmt.Sprint(cell))
	}
	return rows, names, nil
}

// sheetWatchRange is the A1 range a row watch reads; the create endpoint has already
// normalized an unset one to the default.
func sheetWatchRange(rec inboundSubscription) string {
	if r := strings.TrimSpace(rec.WatchRange); r != "" {
		return r
	}
	return sheetsDefaultRange
}

// sheetRowsSeen reads the watch's cursor: how many rows of the range have already been
// published. A cursor that is not a number (there is no way to write one, but a hand-
// edited record could) reads as none, which re-publishes rather than skipping — the
// engine's mark then discards the duplicates, where the opposite mistake would lose
// rows silently.
func sheetRowsSeen(rec inboundSubscription) int {
	n, err := strconv.Atoi(strings.TrimSpace(rec.LastEventID))
	if err != nil || n < 0 {
		return 0
	}
	return n
}

// sheetRowFields is the binding environment one row exposes: the envelope, the row as
// a list, and — when the watch names a header — each cell under its column name.
//
// The named columns are spread as top-level fields the way clio spreads an event body,
// because that is what makes a correlation key readable: `Antragsnummer` rather than
// `row[3]`. The envelope fields take precedence over a column of the same name, so a
// subscription can always rely on them.
func sheetRowFields(rec inboundSubscription, header []string, row any, number int) map[string]any {
	cells, _ := row.([]any)
	fields := make(map[string]any, len(header)+5)
	for i, name := range header {
		if name == "" {
			continue // an unnamed column cannot be addressed
		}
		if i < len(cells) {
			fields[name] = cells[i]
			continue
		}
		fields[name] = "" // a short row has empty cells, not missing ones
	}
	fields["eventType"] = "googlesheets.row"
	fields["spreadsheetId"] = rec.SpreadsheetID
	fields["range"] = sheetWatchRange(rec)
	fields["rowNumber"] = number
	fields["row"] = cells
	return fields
}

// driveFolderSource watches a Drive folder. Unlike a row it takes its sequence and its
// mark from the file rather than from the read: the mark is scoped per file id, so no
// delivery order can make one file suppress another, and the cursor carries no
// correctness weight at all — only progress.
type driveFolderSource struct {
	client googlesheets.Client
	now    func() time.Time // injectable for tests; time.Now in production
}

func (s driveFolderSource) Read(ctx context.Context, rec inboundSubscription, limit int) ([]inboundEvent, string, error) {
	files, err := s.client.ListFiles(ctx, googlesheets.FileQuery{
		Folder: rec.FolderID,
		Field:  driveCursorField(rec),
		After:  driveCursorTime(rec),
		Limit:  limit,
	})
	if err != nil || len(files) == 0 {
		return nil, "", err
	}
	field := driveCursorField(rec)
	out := make([]inboundEvent, 0, len(files))
	newest := time.Time{}
	for _, file := range files {
		id, _ := file["id"].(string)
		if id == "" {
			continue // not a file; nothing to key a mark on
		}
		at := driveFileTime(file, field)
		if at.IsZero() {
			// No usable timestamp, so no sequence — and an event with no sequence has
			// no place in a mechanism whose correctness rests on one. Skipping is not
			// merely tidy: uint64(time.Time{}.UnixMilli()) is a *negative* millis count
			// wrapped near the top of the range, which would set this file's mark so
			// high that nothing about it is ever delivered again.
			continue
		}
		out = append(out, inboundEvent{
			MarkKey: id,
			Seq:     uint64(at.UnixMilli()),
			Fields:  driveFileFields(rec, file, field),
		})
		if at.After(newest) {
			newest = at
		}
	}
	if newest.IsZero() {
		// Nothing carried a usable timestamp: leave the cursor where it was rather than
		// stepping it somewhere arbitrary. The marks make a re-read harmless.
		return out, "", nil
	}
	return out, driveNextCursor(rec, newest, len(files) >= limit), nil
}

func (s driveFolderSource) Prime(ctx context.Context, rec inboundSubscription) (string, bool, error) {
	// One descending read of a single file reaches the tip; there is no backlog to page
	// through, because what is being skipped is everything already in the folder.
	files, err := s.client.ListFiles(ctx, googlesheets.FileQuery{
		Folder: rec.FolderID, Field: driveCursorField(rec), Limit: 1, Descending: true,
	})
	if err != nil {
		return "", false, err
	}
	// An empty folder primes to now: there is nothing to skip, and the next poll's
	// window starts here rather than at the beginning of time.
	cursor := s.now().UTC().Add(-driveLag(rec))
	if len(files) > 0 {
		if at := driveFileTime(files[0], driveCursorField(rec)); !at.IsZero() {
			cursor = at.Add(-driveLag(rec))
		}
	}
	return cursor.UTC().Format(time.RFC3339), true, nil
}

// driveNextCursor is where a folder watch resumes, and the one rule it may not break
// is that a full page moves it.
//
// The lag holds the cursor behind the newest file seen so a file Drive indexes late is
// still inside the next window; its own mark makes that re-read free. That is right at
// the tip, where a page is short because there is nothing more to read. It is wrong
// behind a *full* page: there the read stopped at the bridge's cap with more files
// behind it, and a cursor held back into the page just read asks the next poll for the
// same files again. Nothing then ever advances — which is not a slow watch, it is a
// stopped one, and it looks like a busy Atlas because the publishes are real work the
// engine correctly discards.
func driveNextCursor(rec inboundSubscription, newest time.Time, full bool) string {
	at := newest
	if !full {
		at = at.Add(-driveLag(rec))
	}
	// Drive compares with '>' at second granularity, so a full page whose newest file
	// is exactly the stored cursor would be re-read for ever. One second past it is the
	// smallest step that moves; it can only skip files sharing that second beyond the
	// cap, where standing still skips every file after them.
	cursor := at.UTC().Format(time.RFC3339)
	if full && cursor == strings.TrimSpace(rec.LastEventID) {
		return at.Add(time.Second).UTC().Format(time.RFC3339)
	}
	return cursor
}

// driveCursorField is the file timestamp a watch follows: `created` for new files (the
// default), `modified` for a watch on changes. It is both what the query filters and
// orders on AND what the event's sequence is taken from, and those must be the same
// field — a watch for new files that sequenced on modifiedTime would let an edit
// inside the lag window pass the mark and start a second instance for a file already
// handled.
func driveCursorField(rec inboundSubscription) string {
	if strings.TrimSpace(rec.CursorField) == "modified" {
		return "modifiedTime"
	}
	return "createdTime"
}

// driveCursorTime parses the resume cursor; an unset or unparseable one is no lower
// bound, which re-reads the folder rather than skipping it.
func driveCursorTime(rec inboundSubscription) time.Time {
	at, err := time.Parse(time.RFC3339, strings.TrimSpace(rec.LastEventID))
	if err != nil {
		return time.Time{}
	}
	return at
}

// driveLag is how far behind the newest file the cursor is held, in seconds; 0 uses
// the default. It is a knob rather than a constant because how late an index runs is a
// property of the drive, not of Atlas.
func driveLag(rec inboundSubscription) time.Duration {
	if rec.LagSeconds > 0 {
		return time.Duration(rec.LagSeconds) * time.Second
	}
	return driveDefaultLag
}

// driveFileTime reads one of a file's timestamps. Drive renders them as RFC 3339 with
// milliseconds; a value that does not parse yields the zero time, which the caller
// treats as "no usable timestamp" rather than as the epoch.
func driveFileTime(file map[string]any, name string) time.Time {
	raw, _ := file[name].(string)
	if raw == "" {
		return time.Time{}
	}
	if t, err := time.Parse(time.RFC3339, raw); err == nil {
		return t
	}
	return time.Time{}
}

// driveFileFields is the binding environment one file exposes: a bounded envelope plus
// the whole file as one value, the shape the Jira watch settled on. What is named here
// is what a correlation key or a gateway actually uses — above all the link, because
// that is what a process puts into the mail or the task it creates next.
func driveFileFields(rec inboundSubscription, file map[string]any, field string) map[string]any {
	kind := "created"
	if field == "modifiedTime" {
		kind = "modified"
	}
	return map[string]any{
		"eventType":    "googledrive.file." + kind,
		"fileId":       file["id"],
		"fileName":     file["name"],
		"mimeType":     file["mimeType"],
		"createdTime":  file["createdTime"],
		"modifiedTime": file["modifiedTime"],
		"webViewLink":  file["webViewLink"],
		"folderId":     rec.FolderID,
		"file":         file,
	}
}

// validateInboundWatch checks a watch against the kind of connector it names, returning
// the message to refuse it with or "" when it is usable. The kind is the discriminator
// (ADR-0214), so this is where a clio watch's subject and a jira watch's query are each
// required — and where each is refused on the other's kind, so a watch cannot be saved
// carrying a field nothing will ever read.
//
// It normalizes in place, the way the connector validators do: an unset cursor field
// becomes the default rather than a value every reader has to defend against.
func validateInboundWatch(kind string, rec *inboundSubscription) string {
	switch kind {
	case connectorKindClio:
		if rec.WatchedSubject == "" {
			return "watchedSubject is required for a clio watch"
		}
		if rec.JQL != "" {
			return "jql belongs to a jira watch; a clio watch names a subject"
		}
		rec.CursorField = ""
		return ""

	case connectorKindJira:
		if rec.JQL == "" {
			return "jql is required for a jira watch: the query whose issues start processes, " +
				`written exactly as in Jira's own search box (e.g. project = OPS AND issuetype = Bug)`
		}
		if rec.WatchedSubject != "" || rec.Recursive {
			return "watchedSubject and recursive belong to a clio watch; a jira watch names a query"
		}
		// The ordering is the bridge's, because the cursor's progress depends on the
		// last issue of a page being the newest one. A query that carries its own is
		// refused here rather than silently overridden, so an author who wrote one
		// learns that it would not have been honoured.
		if strings.Contains(strings.ToUpper(rec.JQL), "ORDER BY") {
			return "a jira watch's jql must not carry an ORDER BY: the watch orders by its own " +
				"cursor field, because that is what makes its resume position mean anything"
		}
		// Jira Cloud's search endpoint refuses an unbounded query outright ("Unbounded
		// JQL queries are not allowed here"), and a watch that only says how to sort is
		// exactly that. Catching it here names the fix; from Jira it arrives as an
		// HTTP 400 on every poll.
		if !jqlLooksBounded(rec.JQL) {
			return "a jira watch's jql must restrict what it matches — Jira refuses an unbounded " +
				"query. Name at least a project, an issue type, an assignee or a label, " +
				`e.g. project = OPS`
		}
		switch rec.CursorField {
		case "", "created":
			rec.CursorField = "created"
		case "updated":
		default:
			return `cursorField must be "created" (new issues, the default) or "updated" (changed issues)`
		}
		if rec.LagSeconds < 0 || rec.PollSeconds < 0 {
			return "lagSeconds and pollSeconds cannot be negative"
		}
		return ""

	case connectorKindGoogleSheets:
		return validateGoogleWatch(rec)

	case "":
		return "no connector with that id"
	default:
		return "the Worker Type " + kind + " has no inbound half: only clio, jira and googlesheets Workers can carry a watch"
	}
}

// validateGoogleWatch checks a watch on a Google Worker. The subscription names
// exactly one target, and which one it is decides everything else about the watch
// (ADR-draft-google-inbound-watch) — so naming both, or neither, is refused here
// rather than resolved by a precedence rule nobody would remember.
//
// It normalizes in place, the way the connector validators do: an unset range and an
// unset cursor field become their defaults rather than values every reader has to
// defend against.
func validateGoogleWatch(rec *inboundSubscription) string {
	if rec.WatchedSubject != "" || rec.Recursive || rec.JQL != "" {
		return "watchedSubject, recursive and jql belong to a clio or jira watch; " +
			"a google watch names a spreadsheet or a Drive folder"
	}
	sheet, folder := rec.SpreadsheetID != "", rec.FolderID != ""
	switch {
	case sheet && folder:
		return "a google watch names either a spreadsheetId (its new rows start processes) " +
			"or a folderId (the files put in it do), not both"
	case !sheet && !folder:
		return "a google watch needs a spreadsheetId (its new rows start processes) " +
			"or a folderId (the files put in it do)"
	case sheet:
		// A row watch follows the sheet's own numbering, so it has no cursor field to
		// choose; the range is normalized rather than left for every reader to default.
		if rec.CursorField != "" {
			return `cursorField belongs to a folder watch; a row watch follows the sheet's own row numbers`
		}
		if rec.WatchRange == "" {
			rec.WatchRange = sheetsDefaultRange
		}
	default:
		if rec.WatchRange != "" || rec.HeaderRow {
			return "watchRange and headerRow belong to a row watch; a folder watch names a folder"
		}
		switch rec.CursorField {
		case "", "created":
			rec.CursorField = "created"
		case "modified":
		default:
			return `cursorField must be "created" (new files, the default) or "modified" (changed files)`
		}
	}
	if rec.LagSeconds < 0 || rec.PollSeconds < 0 {
		return "lagSeconds and pollSeconds cannot be negative"
	}
	return ""
}

// jqlLooksBounded reports whether a query says anything about *which* issues it wants,
// as opposed to only how to sort them. It is deliberately a shape check and not a JQL
// parser: Jira decides what counts as bounded, and this only catches the case that is
// unambiguous and common — a query with no comparison in it at all.
func jqlLooksBounded(jql string) bool {
	return strings.ContainsAny(jql, "=<>~") || strings.Contains(strings.ToUpper(jql), " IN ")
}
