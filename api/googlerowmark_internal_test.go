package api

import (
	"context"
	"strings"
	"testing"
)

// A row watch's mark must be its own. Two watches on one Google Worker sharing a mark
// is not a theoretical collision: their sequence is an absolute row number, so the
// watch further down its sheet sets the mark high and the engine then correctly
// discards every row of the other one.
//
// This is the defect ADR-0234 shipped with. The scalar branch of inboundSourceID keys
// on WatchedSubject, which is clio's field and which the Google validator itself
// refuses as non-empty — so every Google row watch on one Worker composed the same
// empty-keyed id.
func TestRowWatchesOnOneWorkerDoNotShareAMark(t *testing.T) {
	a := inboundSubscription{ID: "watch-a", ConnectorID: "conn-1", SpreadsheetID: "sheet-A"}
	b := inboundSubscription{ID: "watch-b", ConnectorID: "conn-1", SpreadsheetID: "sheet-B"}

	ida := inboundSourceID(connectorKindGoogleSheets, a, sheetRowMarkKey(a))
	idb := inboundSourceID(connectorKindGoogleSheets, b, sheetRowMarkKey(b))

	if ida == idb {
		t.Fatalf("two row watches share the mark %q; the one further down its sheet suppresses the other entirely", ida)
	}
	for _, want := range []string{"watch-a", "sheet-A"} {
		if !strings.Contains(ida, want) {
			t.Errorf("sourceID = %q, want it to carry %q", ida, want)
		}
	}
}

// The same worker, the same spreadsheet, two watches publishing different messages:
// still two marks. A spreadsheet watched twice is a real configuration — one watch
// starting an approval, another feeding a report — and sharing a mark would let
// whichever polled first silence the other.
func TestTwoWatchesOnOneSpreadsheetDoNotShareAMark(t *testing.T) {
	a := inboundSubscription{ID: "watch-a", ConnectorID: "conn-1", SpreadsheetID: "1B"}
	b := inboundSubscription{ID: "watch-b", ConnectorID: "conn-1", SpreadsheetID: "1B"}
	if inboundSourceID(connectorKindGoogleSheets, a, sheetRowMarkKey(a)) ==
		inboundSourceID(connectorKindGoogleSheets, b, sheetRowMarkKey(b)) {
		t.Fatal("two watches on one spreadsheet share a mark")
	}
}

// Why scoping the mark needs no migration, pinned as a property rather than left as
// reasoning in a record: a row watch never emits a row at or below its own cursor. The
// cursor bounds what is produced; the mark only discards a page that was published and
// then re-read because the cursor could not advance. So giving a live watch a new,
// empty mark cannot replay its sheet — there is nothing below the cursor to replay.
func TestRowWatchNeverEmitsAtOrBelowItsCursor(t *testing.T) {
	client := &fakeGoogleClient{rows: []any{
		row("Anna"), row("Bo"), row("Cem"), row("Dana"), row("Eren"),
	}}
	rec := inboundSubscription{ID: "w", ConnectorID: "c", SpreadsheetID: "1B", LastEventID: "3"}

	events, _, err := sheetRowSource{client: client}.Read(context.Background(), rec, 25)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if len(events) == 0 {
		t.Fatal("nothing emitted; the fixture must produce rows past the cursor")
	}
	for _, ev := range events {
		if ev.Seq <= 3 {
			t.Errorf("emitted row %d at or below the cursor 3; the cursor is what makes the mark's scope safe to change", ev.Seq)
		}
	}
}
