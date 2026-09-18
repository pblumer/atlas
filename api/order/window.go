package order

import (
	"sort"
	"time"

	"github.com/pblumer/atlas/api/catalog"
)

// When a product may be ordered (ADR-0397).
//
// [catalog.Item.Lifecycle] has carried a window since the catalogue was designed
// (ADR-0312) and nothing ever read it. Publishing checked that the window did not
// end before it began and no reader anywhere asked whether *today* was inside it,
// so a product with a window was orderable exactly like a product without one. The
// field was a promise the catalogue made to whoever filled it in, kept by nobody.
//
// # Why the refusal is here and not in the portal
//
// Because the portal is a client. It does keep the window now — a basket that
// cannot be submitted is a control that fails — but a rule enforced only where it
// is displayed is a rule any other caller walks past, and the catalogue's whole
// surface is an HTTP API that agents drive. This is the gate; the portal is
// courtesy.
//
// # Why it is read from the release
//
// Like the price, the ceiling and the approval rule beside it: an order names one
// release and is answered against what that release froze. The window is not
// frozen *against* the orderer — a catalogue republished with a longer window is a
// new release, and the portal orders against the newest one — it is simply read
// where every other rule about this basket is read, so one lookup answers the
// whole question and nothing reaches back into a catalogue that has since moved.
//
// # Why the clock is passed in
//
// The order's own creation stamp and this check must be the same instant. Read
// twice, a basket placed across a boundary could be refused for a window that had
// already opened at the moment the order says it was created — a contradiction
// inside one record, and the kind that surfaces once a year and cannot be
// reproduced.

// closed is one item in the order that cannot be ordered at this moment.
type closed struct {
	// ItemID is the product that was refused, and From/Until its window as the
	// release carries it. Zero on a side means unbounded there.
	ItemID string
	From   int64
	Until  int64
	// IncludedBy is the product whose composition dragged it in, empty when it was
	// asked for directly. It is the same distinction eligibility draws and it is
	// there for the same reason: an integral part is never deselectable, so
	// refusing one by name leaves somebody looking for a checkbox that does not
	// exist.
	IncludedBy string
	// At is the moment the order was placed, so the reason can say which side of
	// the window that was.
	At int64
}

// opensLater reports whether the window has not started yet, as opposed to having
// ended. The two are opposite advice — wait, or stop looking — and a refusal that
// merged them would give neither.
func (c closed) opensLater() bool { return c.From != 0 && c.At < c.From }

// reason renders the refusal for a person who has to do something about it.
//
// It names the date. A refusal that says only "not orderable now" sends somebody
// to ask an administrator what "now" means, and the answer is in the record that
// refused them.
func (c closed) reason() string {
	when := day(c.From)
	what := c.ItemID + " cannot be ordered yet: it is orderable from " + when
	if !c.opensLater() {
		what = c.ItemID + " can no longer be ordered: it was orderable until " + day(c.Until)
	}
	if c.IncludedBy == "" {
		return what
	}
	return what + ". " + c.IncludedBy + " is made of it and always carries it, so " +
		"this is a refusal of " + c.IncludedBy + " at this moment rather than something " +
		"to deselect"
}

// day renders one side of the window as a date in the server's own zone.
//
// A date and not a timestamp: the window is authored as days — a product opens on
// the first of the month, not at 09:17:43 — and a nanosecond precision nobody
// entered would read as precision somebody did.
func day(ns int64) string { return time.Unix(0, ns).Format("2006-01-02") }

// closedIn reports the first item in this order that is outside its window at the
// given moment, or nil.
//
// Deterministic in what it reports, for the reason ineligibleIn is: the items are
// walked in sorted order, so the same basket refused twice names the same product
// rather than making the second attempt look like a second, different problem.
func closedIn(rel catalog.Release, ordered []string, at int64) *closed {
	windows := map[string]catalog.Lifecycle{}
	for _, it := range rel.Items {
		// A product with no window on either side is the ordinary case and is not
		// collected at all, so the common basket walks an empty map and stops.
		if it.Lifecycle.From != 0 || it.Lifecycle.Until != 0 {
			windows[it.ID] = it.Lifecycle
		}
	}
	if len(windows) == 0 {
		return nil
	}

	sorted := append([]string(nil), ordered...)
	sort.Strings(sorted)
	for _, id := range sorted {
		w, ok := windows[id]
		if !ok || within(w, at) {
			continue
		}
		return &closed{
			ItemID: id, From: w.From, Until: w.Until, At: at,
			IncludedBy: wholeCarrying(rel, ordered, id),
		}
	}
	return nil
}

// within reports whether a moment is inside a window.
//
// Both sides are inclusive, and that is the whole of the boundary rule: a window
// is authored as "from this day until that day", and a maintainer who writes the
// last day means the product is orderable on it. The Console stores the end of
// that day, so an order placed at any hour of it is inside.
//
// Zero means unbounded on that side rather than 1970, which is why neither
// comparison is made against a bare value.
func within(w catalog.Lifecycle, at int64) bool {
	if w.From != 0 && at < w.From {
		return false
	}
	if w.Until != 0 && at > w.Until {
		return false
	}
	return true
}
