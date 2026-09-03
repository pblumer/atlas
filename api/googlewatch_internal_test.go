package api

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/pblumer/atlas/connector/googlesheets"
)

// fakeGoogleClient answers a row read and a folder listing from canned data, so the
// two watches are exercised without a live Google.
type fakeGoogleClient struct {
	rows    []any            // what read-range answers
	files   []map[string]any // what ListFiles answers
	queries []googlesheets.FileQuery
	reqs    []googlesheets.Request
	err     error
}

func (f *fakeGoogleClient) Do(_ context.Context, req googlesheets.Request) (any, error) {
	f.reqs = append(f.reqs, req)
	return f.rows, f.err
}

func (f *fakeGoogleClient) ListFiles(_ context.Context, q googlesheets.FileQuery) ([]map[string]any, error) {
	f.queries = append(f.queries, q)
	return f.files, f.err
}

func row(cells ...any) any { return cells }

// TestSheetRowWatchPublishesOnlyNewRows is the whole row watch: the cursor is how many
// rows have been seen, everything past it is an event, and its sequence is its own
// row number.
func TestSheetRowWatchPublishesOnlyNewRows(t *testing.T) {
	client := &fakeGoogleClient{rows: []any{
		row("Anna", "42"),
		row("Bo", "7"),
		row("Cem", "13"),
	}}
	src := sheetRowSource{client: client}
	rec := inboundSubscription{SpreadsheetID: "1B", WatchRange: "Anträge!A:B", LastEventID: "1"}

	events, cursor, err := src.Read(context.Background(), rec, 25)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if len(events) != 2 {
		t.Fatalf("events = %d; want the two rows past the cursor", len(events))
	}
	if events[0].Seq != 2 || events[1].Seq != 3 {
		t.Errorf("sequences = %d, %d; want the absolute row numbers 2 and 3", events[0].Seq, events[1].Seq)
	}
	// No per-row mark: a row number is monotonic across the whole watch, so the scalar
	// watch-level mark is correct — the clio case, not the Jira one.
	if events[0].MarkKey != "" {
		t.Errorf("markKey = %q; want the watch's own scalar mark", events[0].MarkKey)
	}
	if cursor != "3" {
		t.Errorf("cursor = %q; want the new row count", cursor)
	}
	if client.reqs[0].Range != "Anträge!A:B" || client.reqs[0].Operation != "read-range" {
		t.Errorf("read = %+v; want a read of the watched range", client.reqs[0])
	}
	f := events[0].Fields
	if f["rowNumber"] != 2 || f["eventType"] != "googlesheets.row" || f["spreadsheetId"] != "1B" {
		t.Errorf("fields = %#v; want the row envelope", f)
	}
	if cells, _ := f["row"].([]any); len(cells) != 2 || cells[0] != "Bo" {
		t.Errorf("row = %#v; want the row's cells", f["row"])
	}
}

// TestSheetRowWatchNamesColumnsFromTheHeader: what makes a correlation key readable —
// `Antragsnummer` rather than an index into a list.
func TestSheetRowWatchNamesColumnsFromTheHeader(t *testing.T) {
	client := &fakeGoogleClient{rows: []any{
		row("Antragsnummer", "Name", ""),
		row("A-1", "Anna"),
	}}
	src := sheetRowSource{client: client}
	events, cursor, err := src.Read(context.Background(), inboundSubscription{
		SpreadsheetID: "1B", HeaderRow: true, LastEventID: "1",
	}, 25)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("events = %d; want the one data row", len(events))
	}
	f := events[0].Fields
	if f["Antragsnummer"] != "A-1" || f["Name"] != "Anna" {
		t.Errorf("fields = %#v; want each cell under its column name", f)
	}
	// A short row has empty cells, not missing keys, so a FEEL path never hits a null
	// it has to defend against.
	if f["rowNumber"] != 2 {
		t.Errorf("rowNumber = %v; want the real row number, with the header counted", f["rowNumber"])
	}
	if cursor != "2" {
		t.Errorf("cursor = %q; want every row read counted, header included", cursor)
	}
	// An unnamed column names no field rather than one called "".
	if _, ok := f[""]; ok {
		t.Errorf("fields = %#v; want no field for the unnamed third column", f)
	}
}

// TestSheetRowWatchAtTheTipPublishesNothing: the steady state. It must also not move
// the cursor, because writing an unchanged value is two fsyncs on the run loop.
func TestSheetRowWatchAtTheTipPublishesNothing(t *testing.T) {
	client := &fakeGoogleClient{rows: []any{row("Anna")}}
	events, cursor, err := sheetRowSource{client: client}.Read(context.Background(),
		inboundSubscription{SpreadsheetID: "1B", LastEventID: "1"}, 25)
	if err != nil || len(events) != 0 {
		t.Fatalf("at the tip = %v, %v; want no events", events, err)
	}
	if cursor != "" {
		t.Errorf("cursor = %q; want no advance when nothing was read", cursor)
	}
}

// TestSheetRowWatchFollowsADeletedRowDown: rows were deleted, so the sheet is shorter
// than the cursor. The cursor follows it down — otherwise the next append lands below
// the cursor and the watch never notices anything again. What that costs is the rows
// in between, which the record states plainly and which nothing can recover: a row's
// only identity is its place.
func TestSheetRowWatchFollowsADeletedRowDown(t *testing.T) {
	client := &fakeGoogleClient{rows: []any{row("Anna")}}
	events, cursor, err := sheetRowSource{client: client}.Read(context.Background(),
		inboundSubscription{SpreadsheetID: "1B", LastEventID: "5"}, 25)
	if err != nil || len(events) != 0 {
		t.Fatalf("after a deletion = %v, %v; want no events", events, err)
	}
	if cursor != "1" {
		t.Errorf("cursor = %q; want it to follow the shortened sheet down to 1", cursor)
	}
}

// TestSheetRowWatchHonoursTheBatchLimit: a sheet that gained a thousand rows must not
// hand the run loop one unbounded batch.
func TestSheetRowWatchHonoursTheBatchLimit(t *testing.T) {
	rows := make([]any, 0, 10)
	for i := 0; i < 10; i++ {
		rows = append(rows, row("r"+strconv.Itoa(i)))
	}
	events, cursor, err := sheetRowSource{client: &fakeGoogleClient{rows: rows}}.Read(
		context.Background(), inboundSubscription{SpreadsheetID: "1B"}, 4)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if len(events) != 4 || cursor != "4" {
		t.Errorf("events = %d, cursor = %q; want one bounded page", len(events), cursor)
	}
}

// TestSheetRowWatchPrimesPastTheBacklog: adding a watch to a sheet that already has a
// year of responses must not start a process per historical row.
func TestSheetRowWatchPrimesPastTheBacklog(t *testing.T) {
	client := &fakeGoogleClient{rows: []any{row("a"), row("b"), row("c")}}
	cursor, done, err := sheetRowSource{client: client}.Prime(context.Background(),
		inboundSubscription{SpreadsheetID: "1B"})
	if err != nil || !done {
		t.Fatalf("Prime = %q, %v, %v; want the tip in one call", cursor, done, err)
	}
	if cursor != "3" {
		t.Errorf("cursor = %q; want every existing row skipped", cursor)
	}
}

// TestSheetRowWatchPropagatesAReadError: a spreadsheet the credential was never shared
// with answers 403 on every poll, and the bridge must not read that as "no new rows".
func TestSheetRowWatchPropagatesAReadError(t *testing.T) {
	client := &fakeGoogleClient{err: errors.New("403")}
	if _, _, err := (sheetRowSource{client: client}).Read(context.Background(),
		inboundSubscription{SpreadsheetID: "1B"}, 25); err == nil {
		t.Error("a failed read: want an error, got nil")
	}
	if _, _, err := (sheetRowSource{client: client}).Prime(context.Background(),
		inboundSubscription{SpreadsheetID: "1B"}); err == nil {
		t.Error("a failed prime: want an error, got nil")
	}
}

// TestSheetRowsSeenReadsAGarbledCursorAsNone: re-publishing is what the engine's mark
// discards; skipping would lose rows silently, which is the mistake worth avoiding.
func TestSheetRowsSeenReadsAGarbledCursorAsNone(t *testing.T) {
	for _, cursor := range []string{"", "keine zahl", "-4"} {
		if got := sheetRowsSeen(inboundSubscription{LastEventID: cursor}); got != 0 {
			t.Errorf("sheetRowsSeen(%q) = %d; want 0", cursor, got)
		}
	}
}

// --- the folder watch ---

func driveFile(id, created string) map[string]any {
	return map[string]any{
		"id": id, "name": id + ".pdf", "mimeType": "application/pdf",
		"createdTime": created, "modifiedTime": created,
		"webViewLink": "https://drive.google.com/file/d/" + id,
	}
}

// TestFolderWatchMarksPerFile is the half a scalar mark cannot do: files.list is a
// query, so the mark is the file's own id and the sequence its own timestamp.
func TestFolderWatchMarksPerFile(t *testing.T) {
	client := &fakeGoogleClient{files: []map[string]any{
		driveFile("f1", "2026-09-03T08:00:00Z"),
		driveFile("f2", "2026-09-03T08:05:00Z"),
	}}
	src := driveFolderSource{client: client, now: func() time.Time { return time.Now() }}
	rec := inboundSubscription{FolderID: "fold", CursorField: "created"}

	events, cursor, err := src.Read(context.Background(), rec, 25)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if len(events) != 2 {
		t.Fatalf("events = %d; want one per file", len(events))
	}
	if events[0].MarkKey != "f1" || events[1].MarkKey != "f2" {
		t.Errorf("marks = %q, %q; want one per file id", events[0].MarkKey, events[1].MarkKey)
	}
	want := time.Date(2026, 9, 3, 8, 0, 0, 0, time.UTC).UnixMilli()
	if events[0].Seq != uint64(want) {
		t.Errorf("seq = %d; want the file's own createdTime in millis (%d)", events[0].Seq, want)
	}
	// A short page is at the tip, so the cursor is held back by the lag: a file Drive
	// indexes late is then still inside the next window, and its own mark makes the
	// re-read free.
	at, err := time.Parse(time.RFC3339, cursor)
	if err != nil {
		t.Fatalf("cursor %q does not parse: %v", cursor, err)
	}
	if got := time.Date(2026, 9, 3, 8, 5, 0, 0, time.UTC).Sub(at); got != driveDefaultLag {
		t.Errorf("cursor is %v behind the newest file; want the %v lag", got, driveDefaultLag)
	}
	f := events[0].Fields
	if f["eventType"] != "googledrive.file.created" || f["fileId"] != "f1" || f["folderId"] != "fold" {
		t.Errorf("fields = %#v; want the file envelope", f)
	}
	if f["webViewLink"] == nil {
		t.Errorf("fields = %#v; want the link, which is what a process sends on", f)
	}
}

// TestFolderWatchOnAFullPageDoesNotHoldTheCursorBack: the lag is right at the tip and
// wrong behind a full page, where holding back asks the next poll for the same files
// for ever. That is not a slow watch, it is a stopped one.
func TestFolderWatchOnAFullPageDoesNotHoldTheCursorBack(t *testing.T) {
	client := &fakeGoogleClient{files: []map[string]any{
		driveFile("f1", "2026-09-03T08:00:00Z"),
		driveFile("f2", "2026-09-03T08:05:00Z"),
	}}
	src := driveFolderSource{client: client, now: time.Now}
	_, cursor, err := src.Read(context.Background(), inboundSubscription{FolderID: "fold"}, 2)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if cursor != "2026-09-03T08:05:00Z" {
		t.Errorf("cursor = %q; want the newest file exactly, with no lag behind a full page", cursor)
	}
}

// TestFolderWatchStepsPastAStalledCursor: a full page whose newest file is already the
// stored cursor cannot be drained by time paging, so the watch steps past it rather
// than re-reading the same page for ever.
func TestFolderWatchStepsPastAStalledCursor(t *testing.T) {
	client := &fakeGoogleClient{files: []map[string]any{driveFile("f1", "2026-09-03T08:00:00Z")}}
	src := driveFolderSource{client: client, now: time.Now}
	_, cursor, err := src.Read(context.Background(),
		inboundSubscription{FolderID: "fold", LastEventID: "2026-09-03T08:00:00Z"}, 1)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if cursor != "2026-09-03T08:00:01Z" {
		t.Errorf("cursor = %q; want one second past the stalled one", cursor)
	}
}

// TestFolderWatchSkipsFilesWithNoUsableTimestamp: an event with no sequence has no
// place in a mechanism whose correctness rests on one — and the zero time would set a
// mark near the top of the uint64 range, silencing that file for ever.
func TestFolderWatchSkipsFilesWithNoUsableTimestamp(t *testing.T) {
	client := &fakeGoogleClient{files: []map[string]any{
		{"id": "f1"},                            // no timestamp at all
		{"id": "f2", "createdTime": "gestern"},  // one that does not parse
		{"createdTime": "2026-09-03T08:00:00Z"}, // no id to key a mark on
	}}
	events, cursor, err := (driveFolderSource{client: client, now: time.Now}).Read(
		context.Background(), inboundSubscription{FolderID: "fold"}, 25)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if len(events) != 0 {
		t.Errorf("events = %#v; want none of the three published", events)
	}
	if cursor != "" {
		t.Errorf("cursor = %q; want it left where it was rather than stepped somewhere arbitrary", cursor)
	}
}

// TestFolderWatchFollowsModifiedTime: the "changed files" watch, which must filter,
// order AND sequence on the same field.
func TestFolderWatchFollowsModifiedTime(t *testing.T) {
	client := &fakeGoogleClient{files: []map[string]any{
		{"id": "f1", "createdTime": "2020-01-01T00:00:00Z", "modifiedTime": "2026-09-03T08:00:00Z"},
	}}
	rec := inboundSubscription{FolderID: "fold", CursorField: "modified", LastEventID: "2026-09-03T07:00:00Z"}
	events, _, err := (driveFolderSource{client: client, now: time.Now}).Read(context.Background(), rec, 25)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if client.queries[0].Field != "modifiedTime" {
		t.Errorf("query field = %q; want modifiedTime", client.queries[0].Field)
	}
	if !client.queries[0].After.Equal(time.Date(2026, 9, 3, 7, 0, 0, 0, time.UTC)) {
		t.Errorf("query window starts at %v; want the cursor", client.queries[0].After)
	}
	want := time.Date(2026, 9, 3, 8, 0, 0, 0, time.UTC).UnixMilli()
	if events[0].Seq != uint64(want) {
		t.Errorf("seq = %d; want the modifiedTime, the same field the query orders on", events[0].Seq)
	}
	if events[0].Fields["eventType"] != "googledrive.file.modified" {
		t.Errorf("eventType = %v; want the modified event", events[0].Fields["eventType"])
	}
}

// TestFolderWatchPrimesPastTheBacklog: pointing a watch at a folder that already holds
// a year of invoices must not start a process per invoice.
func TestFolderWatchPrimesPastTheBacklog(t *testing.T) {
	client := &fakeGoogleClient{files: []map[string]any{driveFile("newest", "2026-09-03T08:00:00Z")}}
	cursor, done, err := (driveFolderSource{client: client, now: time.Now}).Prime(
		context.Background(), inboundSubscription{FolderID: "fold"})
	if err != nil || !done {
		t.Fatalf("Prime = %q, %v, %v; want the tip in one call", cursor, done, err)
	}
	if !client.queries[0].Descending || client.queries[0].Limit != 1 {
		t.Errorf("prime query = %+v; want the newest single file", client.queries[0])
	}
	at, err := time.Parse(time.RFC3339, cursor)
	if err != nil {
		t.Fatalf("cursor %q does not parse: %v", cursor, err)
	}
	if !at.Equal(time.Date(2026, 9, 3, 8, 0, 0, 0, time.UTC).Add(-driveDefaultLag)) {
		t.Errorf("cursor = %v; want the newest file less the lag", at)
	}
}

// TestFolderWatchPrimesAnEmptyFolderToNow: there is nothing to skip, and the next
// poll's window should start here rather than at the beginning of time.
func TestFolderWatchPrimesAnEmptyFolderToNow(t *testing.T) {
	now := time.Date(2026, 9, 3, 12, 0, 0, 0, time.UTC)
	cursor, _, err := (driveFolderSource{client: &fakeGoogleClient{}, now: func() time.Time { return now }}).
		Prime(context.Background(), inboundSubscription{FolderID: "fold"})
	if err != nil {
		t.Fatalf("Prime: %v", err)
	}
	if cursor != now.Add(-driveDefaultLag).Format(time.RFC3339) {
		t.Errorf("cursor = %q; want now less the lag", cursor)
	}
}

// TestFolderWatchLagIsAKnob: how late an index runs is a property of the drive, not of
// Atlas.
func TestFolderWatchLagIsAKnob(t *testing.T) {
	if got := driveLag(inboundSubscription{LagSeconds: 90}); got != 90*time.Second {
		t.Errorf("driveLag = %v; want the watch's own", got)
	}
	if got := driveLag(inboundSubscription{}); got != driveDefaultLag {
		t.Errorf("driveLag = %v; want the default", got)
	}
}

// TestFolderWatchPropagatesAListError: a folder the credential was never shared with
// answers 403, and the bridge must not read that as an empty folder.
func TestFolderWatchPropagatesAListError(t *testing.T) {
	client := &fakeGoogleClient{err: errors.New("403")}
	src := driveFolderSource{client: client, now: time.Now}
	if _, _, err := src.Read(context.Background(), inboundSubscription{FolderID: "fold"}, 25); err == nil {
		t.Error("a failed listing: want an error, got nil")
	}
	if _, _, err := src.Prime(context.Background(), inboundSubscription{FolderID: "fold"}); err == nil {
		t.Error("a failed prime: want an error, got nil")
	}
}

// TestValidateGoogleWatch is the shape half: a subscription names exactly one target,
// and each target refuses the other's fields — so a watch cannot be saved carrying a
// value nothing will ever read.
func TestValidateGoogleWatch(t *testing.T) {
	for name, tc := range map[string]struct {
		rec  inboundSubscription
		want string
	}{
		"neither target":      {inboundSubscription{}, "needs a spreadsheetId"},
		"both targets":        {inboundSubscription{SpreadsheetID: "1B", FolderID: "f"}, "not both"},
		"a clio field":        {inboundSubscription{SpreadsheetID: "1B", WatchedSubject: "/x"}, "belong to a clio or jira watch"},
		"a jira field":        {inboundSubscription{FolderID: "f", JQL: "project = OPS"}, "belong to a clio or jira watch"},
		"cursor on a row":     {inboundSubscription{SpreadsheetID: "1B", CursorField: "created"}, "belongs to a folder watch"},
		"range on a folder":   {inboundSubscription{FolderID: "f", WatchRange: "A:C"}, "belong to a row watch"},
		"header on a folder":  {inboundSubscription{FolderID: "f", HeaderRow: true}, "belong to a row watch"},
		"unknown cursor":      {inboundSubscription{FolderID: "f", CursorField: "gelöscht"}, "cursorField must be"},
		"a negative interval": {inboundSubscription{FolderID: "f", PollSeconds: -1}, "cannot be negative"},
	} {
		rec := tc.rec
		msg := validateInboundWatch(connectorKindGoogleSheets, &rec)
		if !strings.Contains(msg, tc.want) {
			t.Errorf("%s: message %q should mention %q", name, msg, tc.want)
		}
	}
}

// TestValidateGoogleWatchNormalizes: an unset range and an unset cursor field become
// their defaults here, so no reader downstream has to defend against an empty one.
func TestValidateGoogleWatchNormalizes(t *testing.T) {
	rowWatch := inboundSubscription{SpreadsheetID: "1B"}
	if msg := validateInboundWatch(connectorKindGoogleSheets, &rowWatch); msg != "" {
		t.Fatalf("a plain row watch was refused: %s", msg)
	}
	if rowWatch.WatchRange != sheetsDefaultRange {
		t.Errorf("watchRange = %q; want the default filled in", rowWatch.WatchRange)
	}
	folderWatch := inboundSubscription{FolderID: "fold"}
	if msg := validateInboundWatch(connectorKindGoogleSheets, &folderWatch); msg != "" {
		t.Fatalf("a plain folder watch was refused: %s", msg)
	}
	if folderWatch.CursorField != "created" {
		t.Errorf("cursorField = %q; want the created default", folderWatch.CursorField)
	}
	// "modified" is the other legal value and is left alone.
	changed := inboundSubscription{FolderID: "fold", CursorField: "modified"}
	if msg := validateInboundWatch(connectorKindGoogleSheets, &changed); msg != "" {
		t.Errorf("a changed-files watch was refused: %s", msg)
	}
}

// TestGoogleWatchCadenceDefaultsToAMinute: both Google reads are whole-collection
// reads, so the bridge's own two-second tick would spend a quota on answers that say
// nothing changed.
func TestGoogleWatchCadenceDefaultsToAMinute(t *testing.T) {
	if got := inboundCadence(connectorKindGoogleSheets, inboundSubscription{}); got != googleDefaultPoll {
		t.Errorf("cadence = %v; want the kind's default", got)
	}
	if got := inboundCadence(connectorKindGoogleSheets, inboundSubscription{PollSeconds: 5}); got != 5*time.Second {
		t.Errorf("cadence = %v; want the watch's own", got)
	}
}

// TestOtherKindsStillRefuseAWatch: the message names the Worker Types that do have an
// inbound half, because the usual cause is a watch on the wrong Worker.
func TestOtherKindsStillRefuseAWatch(t *testing.T) {
	rec := inboundSubscription{}
	msg := validateInboundWatch(connectorKindMail, &rec)
	if !strings.Contains(msg, "googlesheets") {
		t.Errorf("message %q should name every Worker Type that can carry a watch", msg)
	}
}
