package order

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/pblumer/atlas/api/catalog"
	"github.com/pblumer/atlas/api/httpapi"
	"github.com/pblumer/atlas/api/runloop"
)

// The window at the door it actually guards
// (ADR-0397).
//
// closedIn is checked on its own beside this, and a gate is not a rule: what has
// to hold is that a real POST is refused, that the refusal is readable, and that
// nothing is written. The unit tests cannot see any of the three, because they
// never place an order.

// serviceOrderingAt builds an order service whose clock reads `now` and whose
// release carries a window on "account".
func serviceOrderingAt(t *testing.T, now, from, until int64) *Service {
	t.Helper()
	store, err := NewStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	quit := make(chan struct{})
	loop := runloop.New(quit)
	go loop.Run()
	t.Cleanup(func() { close(quit) })

	rel := testRelease(t)
	for i := range rel.Items {
		if rel.Items[i].ID == "account" {
			rel.Items[i].Lifecycle = catalog.Lifecycle{From: from, Until: until}
		}
	}
	return New(loop, store, func() int64 { return now },
		func(id string) (catalog.Release, bool, error) {
			if id != rel.ID {
				return catalog.Release{}, false, nil
			}
			return rel, true, nil
		},
		func(p *httpapi.Principal, catalogID string) (bool, error) { return true, nil },
		inAnyGroup, mayOrderForAnyone,
		func(message, orderID string, vars map[string]string) error { return nil },
		func() string { return "https://atlas.example.ch" },
		ignoreGrant, ignoreRevoke, holdsNothing)
}

// TestAnOrderIsRefusedBeforeItsWindowOpens.
func TestAnOrderIsRefusedBeforeItsWindowOpens(t *testing.T) {
	s := serviceOrderingAt(t, at("2026-09-18T08:00:00Z"),
		at("2026-10-01T00:00:00Z"), at("2026-10-31T23:59:59Z"))

	refused := do(t, s.HandlePlace, someone("usr_1"), "POST",
		`{"releaseId":"rel_1","items":["account"]}`)
	if refused.Code != http.StatusForbidden {
		t.Fatalf("ordering before the window opened = %d (%s), want 403",
			refused.Code, refused.Body)
	}
	// The date is the whole value of the refusal: without it somebody has to ask an
	// administrator what "not yet" means, and the answer is in the record that
	// refused them.
	if body := refused.Body.String(); !strings.Contains(body, "2026-10-01") {
		t.Errorf("the refusal does not name the day it opens: %s", body)
	}

	// And nothing was written. A refusal that leaves a half-order behind is worse
	// than one that fails loudly: the next reader finds a record nobody placed.
	listed := do(t, s.HandleList, someone("usr_1"), "GET", "")
	if strings.Contains(listed.Body.String(), "\"releaseId\"") {
		t.Errorf("a refused order was stored: %s", listed.Body)
	}
}

// TestAnOrderIsRefusedAfterItsWindowCloses.
func TestAnOrderIsRefusedAfterItsWindowCloses(t *testing.T) {
	s := serviceOrderingAt(t, at("2026-12-01T00:00:00Z"),
		at("2026-10-01T00:00:00Z"), at("2026-10-31T23:59:59Z"))

	refused := do(t, s.HandlePlace, someone("usr_1"), "POST",
		`{"releaseId":"rel_1","items":["account"]}`)
	if refused.Code != http.StatusForbidden {
		t.Fatalf("ordering after the window closed = %d (%s), want 403",
			refused.Code, refused.Body)
	}
	if body := refused.Body.String(); !strings.Contains(body, "2026-10-31") {
		t.Errorf("the refusal does not name the day it closed: %s", body)
	}
}

// TestAnOrderInsideTheWindowIsPlaced.
//
// The other half, and the one that would fail silently if the comparison were
// inverted: a gate that refuses everything looks exactly like a gate that works,
// until somebody tries to order.
func TestAnOrderInsideTheWindowIsPlaced(t *testing.T) {
	s := serviceOrderingAt(t, at("2026-10-15T09:00:00Z"),
		at("2026-10-01T00:00:00Z"), at("2026-10-31T23:59:59Z"))

	placed := do(t, s.HandlePlace, someone("usr_1"), "POST",
		`{"releaseId":"rel_1","items":["account"]}`)
	if placed.Code != http.StatusCreated {
		t.Fatalf("ordering inside the window = %d (%s), want 201", placed.Code, placed.Body)
	}
}

// TestAWindowedPartClosesTheProductThatCarriesIt.
//
// "account" is an integral part of "workplace", so a window on it decides whether
// the workplace can be ordered at all — and the refusal has to say so, or somebody
// goes looking for a checkbox that does not exist.
func TestAWindowedPartClosesTheProductThatCarriesIt(t *testing.T) {
	s := serviceOrderingAt(t, at("2026-12-01T00:00:00Z"), 0, at("2026-10-31T23:59:59Z"))

	refused := do(t, s.HandlePlace, someone("usr_1"), "POST",
		`{"releaseId":"rel_1","items":["workplace"]}`)
	if refused.Code != http.StatusForbidden {
		t.Fatalf("ordering a product whose integral part is closed = %d (%s), want 403",
			refused.Code, refused.Body)
	}
	body := refused.Body.String()
	if !strings.Contains(body, "workplace") || !strings.Contains(body, "account") {
		t.Errorf("the refusal must name both the part and the product carrying it: %s", body)
	}
}

// TestAProductWithNoWindowIsStillOrdered.
//
// The regression this would be: every catalogue that exists carries no window at
// all, so a gate that read zero as a date would refuse all of them.
func TestAProductWithNoWindowIsStillOrdered(t *testing.T) {
	s := serviceOrderingAt(t, at("2026-10-15T09:00:00Z"), 0, 0)

	placed := do(t, s.HandlePlace, someone("usr_1"), "POST",
		`{"releaseId":"rel_1","items":["account"]}`)
	if placed.Code != http.StatusCreated {
		t.Fatalf("ordering a product with no window = %d (%s), want 201",
			placed.Code, placed.Body)
	}
}

// TestTheOrderIsStampedWithTheMomentItWasChecked.
//
// One reading of the clock for the whole placement. Read twice, a basket placed
// across a boundary could be refused for a window that had already opened at the
// moment the order says it was created — a contradiction inside one record, of the
// kind that surfaces once a year and cannot be reproduced.
//
// Measured rather than read off the source: the clock moves a day per call, so a
// second reading would stamp the order a day after the instant its window was
// checked against, and the stamp is what says which one happened.
func TestTheOrderIsStampedWithTheMomentItWasChecked(t *testing.T) {
	store, err := NewStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	quit := make(chan struct{})
	loop := runloop.New(quit)
	go loop.Run()
	t.Cleanup(func() { close(quit) })

	first := at("2026-10-15T09:00:00Z")
	const day = int64(24 * 60 * 60 * 1e9)
	reads := 0
	clock := func() int64 {
		at := first + int64(reads)*day
		reads++
		return at
	}

	rel := testRelease(t)
	for i := range rel.Items {
		if rel.Items[i].ID == "account" {
			rel.Items[i].Lifecycle = catalog.Lifecycle{
				From: at("2026-10-01T00:00:00Z"), Until: at("2026-10-31T23:59:59Z")}
		}
	}
	s := New(loop, store, clock,
		func(id string) (catalog.Release, bool, error) { return rel, id == rel.ID, nil },
		func(p *httpapi.Principal, catalogID string) (bool, error) { return true, nil },
		inAnyGroup, mayOrderForAnyone,
		func(message, orderID string, vars map[string]string) error { return nil },
		func() string { return "https://atlas.example.ch" },
		ignoreGrant, ignoreRevoke, holdsNothing)

	placed := do(t, s.HandlePlace, someone("usr_1"), "POST",
		`{"releaseId":"rel_1","items":["account"]}`)
	if placed.Code != http.StatusCreated {
		t.Fatalf("place = %d (%s), want 201", placed.Code, placed.Body)
	}
	var got Order
	if err := json.Unmarshal(placed.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode order: %v", err)
	}
	if got.CreatedAt != first {
		t.Errorf("the order is stamped %d and the window was checked against %d; the "+
			"placement reads the clock more than once", got.CreatedAt, first)
	}
}
