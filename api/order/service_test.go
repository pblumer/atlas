package order

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/pblumer/atlas/api/catalog"
	"github.com/pblumer/atlas/api/httpapi"
	"github.com/pblumer/atlas/api/runloop"
)

// Placing an order is where the catalogue's work pays off: the release already
// says what a product is made of, in what order it is fulfilled, and what needs
// approving, so this reads a release and writes lines — it never consults the
// catalogue and never recomputes a graph.

// release builds a published release: a workplace composed of an account and a
// laptop, the laptop needing the account first, and a screen on offer.
func testRelease(t *testing.T) catalog.Release {
	t.Helper()
	ids := []string{"workplace", "account", "laptop", "screen"}
	var items []catalog.Item
	for _, id := range ids {
		it := catalog.Item{
			ID: id, HomeCatalog: "cat", State: catalog.StateActive,
			Texts:            map[string]string{"de": id},
			Approval:         catalog.Approval{Kind: catalog.KindNone},
			ProvisionProcess: "prov", DeprovisionProcess: "deprov",
		}
		if id == "laptop" {
			it.Approval = catalog.Approval{Kind: catalog.KindSuperior}
		}
		items = append(items, it)
	}
	rel, problems := catalog.Publish(catalog.Input{
		Catalogs: []catalog.Catalog{{ID: "cat", Rank: 1, Languages: []string{"de"}, Items: ids}},
		Items:    items,
		Edges: []catalog.Edge{
			{From: "workplace", To: "account", Kind: catalog.EdgeComposition},
			{From: "workplace", To: "laptop", Kind: catalog.EdgeComposition},
			{From: "workplace", To: "screen", Kind: catalog.EdgeAggregation},
			{From: "laptop", To: "account", Kind: catalog.EdgeRequires},
		},
	})
	if len(problems) != 0 {
		t.Fatalf("Publish: %v", problems)
	}
	rel.ID, rel.CatalogID, rel.CreatedAt = "rel_1", "cat", 1000
	return rel
}

func newService(t *testing.T) *Service {
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
	n := int64(0)
	return New(loop, store, func() int64 { n++; return 1700 + n },
		func(id string) (catalog.Release, bool, error) {
			if id != rel.ID {
				return catalog.Release{}, false, nil
			}
			return rel, true, nil
		},
		// These tests are about placing and reading orders; the catalogue gate and
		// the wake have their own cases below.
		func(*httpapi.Principal, string) (bool, error) { return true, nil },
		func(message, orderID string) error { return nil })
}

func do(t *testing.T, h http.HandlerFunc, p *httpapi.Principal, method, body string, vals ...string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, "/x", strings.NewReader(body))
	if p != nil {
		req = req.WithContext(httpapi.WithPrincipal(context.Background(), p))
	}
	for i := 0; i+1 < len(vals); i += 2 {
		req.SetPathValue(vals[i], vals[i+1])
	}
	rec := httptest.NewRecorder()
	h(rec, req)
	return rec
}

func decode[T any](t *testing.T, rec *httptest.ResponseRecorder) T {
	t.Helper()
	var out T
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode %s: %v", rec.Body.String(), err)
	}
	return out
}

// errTest stands for any failure a collaborator can have.
var errTest = errors.New("the collaborator failed")

func someone(id string) *httpapi.Principal {
	return &httpapi.Principal{UserID: id, Roles: []string{"user"}}
}

func lineIDs(o Order) string {
	var ids []string
	for _, l := range o.Lines {
		ids = append(ids, l.ItemID)
	}
	return strings.Join(ids, ",")
}

// TestOrderingAProductOrdersItsParts: the basket resolution, through the API.
func TestOrderingAProductOrdersItsParts(t *testing.T) {
	s := newService(t)

	rec := do(t, s.HandlePlace, someone("usr_1"), "POST",
		`{"releaseId":"rel_1","items":["workplace"]}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("place = %d (%s), want 201", rec.Code, rec.Body)
	}
	got := decode[Order](t, rec)

	if lineIDs(got) != "account,laptop,workplace" {
		t.Fatalf("lines = %s, want the whole and both its parts", lineIDs(got))
	}
	if got.Orderer != "usr_1" {
		t.Errorf("orderer = %q, want usr_1", got.Orderer)
	}
	// Ordering for nobody in particular means ordering for yourself.
	if got.Recipient != "usr_1" {
		t.Errorf("recipient = %q, want the orderer", got.Recipient)
	}
	if got.ReleaseID != "rel_1" {
		t.Errorf("releaseId = %q, want rel_1", got.ReleaseID)
	}
	for _, l := range got.Lines {
		if l.Status != StatusPending {
			t.Errorf("line %s starts as %s, want pending", l.ItemID, l.Status)
		}
	}
}

// TestTheOrderCarriesTheScheduleItWillFollow: the release computed it, so the
// order records it rather than recomputing it later against a catalogue that may
// have moved on.
func TestTheOrderCarriesTheScheduleItWillFollow(t *testing.T) {
	s := newService(t)
	got := decode[Order](t, do(t, s.HandlePlace, someone("usr_1"), "POST",
		`{"releaseId":"rel_1","items":["workplace"]}`))

	if len(got.Waves) != 2 {
		t.Fatalf("waves = %v, want two — the laptop waits for the account", got.Waves)
	}
	if strings.Join(got.Requires["laptop"], ",") != "account" {
		t.Fatalf("requires[laptop] = %v, want account", got.Requires["laptop"])
	}
	// Only what was ordered: the release also knows about the screen.
	for _, wave := range got.Waves {
		for _, id := range wave {
			if id == "screen" {
				t.Fatal("the schedule carries a product that was not ordered")
			}
		}
	}
}

// TestAnOptionIsOrderedOnlyWhenChosen, through the API this time.
func TestAnOptionIsOrderedOnlyWhenChosen(t *testing.T) {
	s := newService(t)
	got := decode[Order](t, do(t, s.HandlePlace, someone("usr_1"), "POST",
		`{"releaseId":"rel_1","items":["workplace","screen"]}`))
	if lineIDs(got) != "account,laptop,screen,workplace" {
		t.Fatalf("lines = %s, want the option included", lineIDs(got))
	}
}

// TestOrderingForSomebodyElse: an integration manager orders for the people
// assigned to them, so recipient and orderer are separate fields from the start.
func TestOrderingForSomebodyElse(t *testing.T) {
	s := newService(t)
	got := decode[Order](t, do(t, s.HandlePlace, someone("usr_mgr"), "POST",
		`{"releaseId":"rel_1","items":["account"],"recipient":"usr_new"}`))
	if got.Orderer != "usr_mgr" || got.Recipient != "usr_new" {
		t.Fatalf("orderer/recipient = %s/%s, want usr_mgr/usr_new", got.Orderer, got.Recipient)
	}
}

// TestAnOrderNeedsAKnownRelease: naming one that does not exist would produce an
// order nothing can fulfil.
func TestAnOrderNeedsAKnownRelease(t *testing.T) {
	s := newService(t)
	rec := do(t, s.HandlePlace, someone("usr_1"), "POST",
		`{"releaseId":"rel_gone","items":["workplace"]}`)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("place = %d, want 404", rec.Code)
	}
}

// TestAnEmptyOrderIsRefused: an order with no lines is a record of nothing, and
// it would settle as completed the moment it was created.
func TestAnEmptyOrderIsRefused(t *testing.T) {
	s := newService(t)
	for _, body := range []string{
		`{"releaseId":"rel_1","items":[]}`,
		`{"releaseId":"rel_1","items":["ghost"]}`,
	} {
		if rec := do(t, s.HandlePlace, someone("usr_1"), "POST", body); rec.Code != http.StatusBadRequest {
			t.Errorf("place %s = %d, want 400", body, rec.Code)
		}
	}
}

// TestAnonymousOrdersAreRefused: an order is somebody's, and an order with no
// orderer has nobody to notify and nobody to hold responsible.
func TestAnonymousOrdersAreRefused(t *testing.T) {
	s := newService(t)
	rec := do(t, s.HandlePlace, nil, "POST", `{"releaseId":"rel_1","items":["account"]}`)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("place = %d, want 403", rec.Code)
	}
}

func TestMalformedOrderIsRefused(t *testing.T) {
	s := newService(t)
	if rec := do(t, s.HandlePlace, someone("usr_1"), "POST", "{not json"); rec.Code != http.StatusBadRequest {
		t.Fatalf("place = %d, want 400", rec.Code)
	}
}

// TestAnOrderIsReadBackByItsOwner, and listing shows only your own: an order
// names who it is for, and other people's are not yours to read.
func TestAnOrderIsReadBackByItsOwner(t *testing.T) {
	s := newService(t)
	mine := decode[Order](t, do(t, s.HandlePlace, someone("usr_1"), "POST",
		`{"releaseId":"rel_1","items":["account"]}`))
	do(t, s.HandlePlace, someone("usr_2"), "POST", `{"releaseId":"rel_1","items":["account"]}`)

	got := do(t, s.HandleGet, someone("usr_1"), "GET", "", "id", mine.ID)
	if got.Code != http.StatusOK {
		t.Fatalf("get = %d (%s)", got.Code, got.Body)
	}

	if other := do(t, s.HandleGet, someone("usr_2"), "GET", "", "id", mine.ID); other.Code != http.StatusNotFound {
		t.Errorf("somebody else's order = %d, want 404", other.Code)
	}

	list := decode[[]Order](t, do(t, s.HandleList, someone("usr_1"), "GET", ""))
	if len(list) != 1 || list[0].ID != mine.ID {
		t.Fatalf("list = %v, want only my own order", list)
	}
}

// TestUnknownOrderIs404.
func TestUnknownOrderIs404(t *testing.T) {
	s := newService(t)
	if rec := do(t, s.HandleGet, someone("usr_1"), "GET", "", "id", "ord_nope"); rec.Code != http.StatusNotFound {
		t.Fatalf("get = %d, want 404", rec.Code)
	}
}

// Knowing a release id must not be enough to order against it. Which catalogue
// somebody may order from is decided by the catalogue service; this one asks.

// serviceGatedBy builds an order service whose catalogue check answers `allow`.
func serviceGatedBy(t *testing.T, allow bool) *Service {
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
	return New(loop, store, func() int64 { return 1700 },
		func(id string) (catalog.Release, bool, error) {
			if id != rel.ID {
				return catalog.Release{}, false, nil
			}
			return rel, true, nil
		},
		func(p *httpapi.Principal, catalogID string) (bool, error) {
			if catalogID != rel.CatalogID {
				t.Errorf("checked catalogue %q, want the release's %q", catalogID, rel.CatalogID)
			}
			return allow, nil
		},
		func(message, orderID string) error { return nil })
}

// TestOrderingNeedsAccessToTheCatalogue is the gap this closes: the release id
// appears in every publish response and every order, so knowing one is no
// qualification at all.
func TestOrderingNeedsAccessToTheCatalogue(t *testing.T) {
	refused := do(t, serviceGatedBy(t, false).HandlePlace, someone("usr_1"), "POST",
		`{"releaseId":"rel_1","items":["account"]}`)
	if refused.Code != http.StatusForbidden {
		t.Fatalf("outsider got %d (%s), want 403", refused.Code, refused.Body)
	}

	allowed := do(t, serviceGatedBy(t, true).HandlePlace, someone("usr_1"), "POST",
		`{"releaseId":"rel_1","items":["account"]}`)
	if allowed.Code != http.StatusCreated {
		t.Fatalf("audience member got %d (%s), want 201", allowed.Code, allowed.Body)
	}
}

// TestARefusedOrderWritesNothing: a refusal must leave no half-order behind.
func TestARefusedOrderWritesNothing(t *testing.T) {
	s := serviceGatedBy(t, false)
	do(t, s.HandlePlace, someone("usr_1"), "POST", `{"releaseId":"rel_1","items":["account"]}`)

	if n := len(decode[[]Order](t, do(t, s.HandleList, someone("usr_1"), "GET", ""))); n != 0 {
		t.Fatalf("%d orders after a refusal, want none", n)
	}
}

// TestAFailingAccessCheckIsAnError, not a quiet refusal and certainly not a
// quiet pass: if the catalogue cannot be consulted, nobody knows whether this
// order was allowed.
func TestAFailingAccessCheckIsAnError(t *testing.T) {
	store, err := NewStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	quit := make(chan struct{})
	loop := runloop.New(quit)
	go loop.Run()
	t.Cleanup(func() { close(quit) })

	rel := testRelease(t)
	s := New(loop, store, func() int64 { return 1700 },
		func(string) (catalog.Release, bool, error) { return rel, true, nil },
		func(*httpapi.Principal, string) (bool, error) {
			return false, errTest
		},
		func(message, orderID string) error { return nil })

	rec := do(t, s.HandlePlace, someone("usr_1"), "POST", `{"releaseId":"rel_1","items":["account"]}`)
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("code = %d (%s), want 500", rec.Code, rec.Body)
	}
}

// TestAFailingReleaseLookupIsAnError, for the same reason.
func TestAFailingReleaseLookupIsAnError(t *testing.T) {
	store, err := NewStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	quit := make(chan struct{})
	loop := runloop.New(quit)
	go loop.Run()
	t.Cleanup(func() { close(quit) })

	s := New(loop, store, func() int64 { return 1700 },
		func(string) (catalog.Release, bool, error) { return catalog.Release{}, false, errTest },
		func(*httpapi.Principal, string) (bool, error) { return true, nil },
		func(message, orderID string) error { return nil })

	rec := do(t, s.HandlePlace, someone("usr_1"), "POST", `{"releaseId":"rel_1","items":["account"]}`)
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("code = %d (%s), want 500", rec.Code, rec.Body)
	}
}

// TestListingWithNoIdentityIsEmpty: a caller who is nobody owns no orders. It is
// an empty list rather than an error, because the boundary already decided
// whether the route may be reached at all.
func TestListingWithNoIdentityIsEmpty(t *testing.T) {
	s := newService(t)
	rec := do(t, s.HandleList, nil, "GET", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("list = %d, want 200", rec.Code)
	}
	if n := len(decode[[]Order](t, rec)); n != 0 {
		t.Fatalf("got %d orders, want none", n)
	}
}

// TestAnUnreadableStoreIsAnError: the same rule as everywhere — a store that
// cannot be read produces a 500, never a 200 with an empty list, which would
// read as "you have no orders".
func TestAnUnreadableStoreIsAnError(t *testing.T) {
	dir := t.TempDir()
	store, err := NewStore(dir)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	if err := os.RemoveAll(dir); err != nil {
		t.Fatalf("RemoveAll: %v", err)
	}
	if err := os.WriteFile(dir, []byte("not a directory"), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	quit := make(chan struct{})
	loop := runloop.New(quit)
	go loop.Run()
	t.Cleanup(func() { close(quit) })
	rel := testRelease(t)
	s := New(loop, store, func() int64 { return 1700 },
		func(string) (catalog.Release, bool, error) { return rel, true, nil },
		func(*httpapi.Principal, string) (bool, error) { return true, nil },
		func(message, orderID string) error { return nil })

	for _, tt := range []struct {
		name string
		h    http.HandlerFunc
		body string
		id   bool
	}{
		{"place", s.HandlePlace, `{"releaseId":"rel_1","items":["account"]}`, false},
		{"get", s.HandleGet, "", true},
		{"list", s.HandleList, "", false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			var rec *httptest.ResponseRecorder
			if tt.id {
				rec = do(t, tt.h, someone("usr_1"), "GET", tt.body, "id", "ord_1")
			} else {
				rec = do(t, tt.h, someone("usr_1"), "POST", tt.body)
			}
			if rec.Code != http.StatusInternalServerError {
				t.Fatalf("code = %d (%s), want 500", rec.Code, rec.Body)
			}
		})
	}
}

// The orchestrator's two calls: what may start, and what came back. They are
// operator work — the person who ordered does not report their own provisioning
// results, and an operator works orders that are not theirs.

func TestTheOrchestratorDrivesAnOrderThroughTheAPI(t *testing.T) {
	s := newService(t)
	placed := decode[Order](t, do(t, s.HandlePlace, someone("usr_1"), "POST",
		`{"releaseId":"rel_1","items":["workplace"]}`))

	op := &httpapi.Principal{UserID: "usr_op", Roles: []string{"operator"}}

	for rounds := 0; ; rounds++ {
		if rounds > 10 {
			t.Fatal("the order did not settle in ten rounds")
		}
		rec := do(t, s.HandleNext, op, "GET", "", "id", placed.ID)
		if rec.Code != http.StatusOK {
			t.Fatalf("next = %d (%s)", rec.Code, rec.Body)
		}
		next := decode[[]Line](t, rec)
		if len(next) == 0 {
			break
		}
		for _, l := range next {
			// The orchestrator starts what the line names; it never looks the
			// binding up in the catalogue.
			if l.ProvisionProcess != "prov" {
				t.Fatalf("line %s carries process %q, want the one the release froze",
					l.ItemID, l.ProvisionProcess)
			}
			res := do(t, s.HandleReport, op, "POST", `{"status":"done"}`, "id", placed.ID, "item", l.ItemID)
			if res.Code != http.StatusOK {
				t.Fatalf("report %s = %d (%s)", l.ItemID, res.Code, res.Body)
			}
		}
	}

	got := decode[Order](t, do(t, s.HandleGet, someone("usr_1"), "GET", "", "id", placed.ID))
	for _, l := range got.Lines {
		if l.Status != StatusDone {
			t.Fatalf("line %s = %s, want done", l.ItemID, l.Status)
		}
	}
	if Derive(got.Lines) != OrderCompleted {
		t.Fatalf("order = %s, want completed", Derive(got.Lines))
	}
}

// TestReportingAFailureBlocksTheChainThroughTheAPI.
func TestReportingAFailureBlocksTheChainThroughTheAPI(t *testing.T) {
	s := newService(t)
	placed := decode[Order](t, do(t, s.HandlePlace, someone("usr_1"), "POST",
		`{"releaseId":"rel_1","items":["workplace"]}`))
	op := &httpapi.Principal{UserID: "usr_op", Roles: []string{"operator"}}

	do(t, s.HandleReport, op, "POST", `{"status":"failed"}`, "id", placed.ID, "item", "account")

	got := decode[Order](t, do(t, s.HandleGet, someone("usr_1"), "GET", "", "id", placed.ID))
	if s := statusOf(got.Lines, "laptop"); s != StatusBlocked {
		t.Fatalf("laptop = %s, want blocked — it needs the account", s)
	}
}

// TestReportingRefusesADecidedOutcome: those have their own transitions, with an
// author, and a second way in would make the author optional.
func TestReportingRefusesADecidedOutcome(t *testing.T) {
	s := newService(t)
	placed := decode[Order](t, do(t, s.HandlePlace, someone("usr_1"), "POST",
		`{"releaseId":"rel_1","items":["account"]}`))
	op := &httpapi.Principal{UserID: "usr_op", Roles: []string{"operator"}}

	for _, status := range []string{"rejected", "abandoned", "blocked", "nonsense"} {
		rec := do(t, s.HandleReport, op, "POST", `{"status":"`+status+`"}`,
			"id", placed.ID, "item", "account")
		if rec.Code != http.StatusBadRequest {
			t.Errorf("reporting %s = %d, want 400", status, rec.Code)
		}
	}
}

func TestOrchestratorCallsOnAnUnknownOrderAre404(t *testing.T) {
	s := newService(t)
	op := &httpapi.Principal{UserID: "usr_op", Roles: []string{"operator"}}

	if rec := do(t, s.HandleNext, op, "GET", "", "id", "ord_nope"); rec.Code != http.StatusNotFound {
		t.Errorf("next = %d, want 404", rec.Code)
	}
	if rec := do(t, s.HandleReport, op, "POST", `{"status":"done"}`, "id", "ord_nope", "item", "a"); rec.Code != http.StatusNotFound {
		t.Errorf("report = %d, want 404", rec.Code)
	}
}

// TestReportingAnUnknownLineIs404: the order exists, the line does not.
func TestReportingAnUnknownLineIs404(t *testing.T) {
	s := newService(t)
	placed := decode[Order](t, do(t, s.HandlePlace, someone("usr_1"), "POST",
		`{"releaseId":"rel_1","items":["account"]}`))
	op := &httpapi.Principal{UserID: "usr_op", Roles: []string{"operator"}}

	rec := do(t, s.HandleReport, op, "POST", `{"status":"done"}`, "id", placed.ID, "item", "ghost")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("report = %d (%s), want 404", rec.Code, rec.Body)
	}
}

func TestMalformedReportIsRefused(t *testing.T) {
	s := newService(t)
	placed := decode[Order](t, do(t, s.HandlePlace, someone("usr_1"), "POST",
		`{"releaseId":"rel_1","items":["account"]}`))
	op := &httpapi.Principal{UserID: "usr_op", Roles: []string{"operator"}}

	if rec := do(t, s.HandleReport, op, "POST", "{not json", "id", placed.ID, "item", "account"); rec.Code != http.StatusBadRequest {
		t.Fatalf("report = %d, want 400", rec.Code)
	}
}

// Reporting an outcome wakes the fulfilment process.
//
// Without a call activity the orchestrator does not wait on a child, so
// something has to tell it that an order moved. The line's own provisioning
// process reports its result, and that report publishes a message the
// orchestrator is parked on — no polling, and no long-lived wait that exists
// only to be woken.

func TestReportingWakesTheFulfilmentProcess(t *testing.T) {
	store, err := NewStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	quit := make(chan struct{})
	loop := runloop.New(quit)
	go loop.Run()
	t.Cleanup(func() { close(quit) })

	rel := testRelease(t)
	var woken []string
	s := New(loop, store, func() int64 { return 1700 },
		func(string) (catalog.Release, bool, error) { return rel, true, nil },
		func(*httpapi.Principal, string) (bool, error) { return true, nil },
		func(message, orderID string) error {
			woken = append(woken, message+":"+orderID)
			return nil
		})

	placed := decode[Order](t, do(t, s.HandlePlace, someone("usr_1"), "POST",
		`{"releaseId":"rel_1","items":["account"]}`))
	op := &httpapi.Principal{UserID: "usr_op", Roles: []string{"operator"}}

	if rec := do(t, s.HandleReport, op, "POST", `{"status":"done"}`,
		"id", placed.ID, "item", "account"); rec.Code != http.StatusOK {
		t.Fatalf("report = %d (%s)", rec.Code, rec.Body)
	}
	// Two messages: the placing started fulfilment, the report woke it.
	if len(woken) != 2 {
		t.Fatalf("woken = %v, want the start and the wake", woken)
	}
	if woken[0] != PlacedMessage+":"+placed.ID {
		t.Errorf("first = %s, want the placed message", woken[0])
	}
	if woken[1] != AdvancedMessage+":"+placed.ID {
		t.Errorf("second = %s, want the advanced message", woken[1])
	}
}

// TestAFailedWakeIsReported: the outcome is durable and the orchestrator is not
// running. Answering 200 would leave an order that moved with nothing to move it
// again, and the reporter is the one thing that can retry.
func TestAFailedWakeIsReported(t *testing.T) {
	store, err := NewStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	quit := make(chan struct{})
	loop := runloop.New(quit)
	go loop.Run()
	t.Cleanup(func() { close(quit) })

	rel := testRelease(t)
	// Starting fulfilment works; waking it does not. Those are the two halves of
	// the same call, and only the second is under test here.
	s := New(loop, store, func() int64 { return 1700 },
		func(string) (catalog.Release, bool, error) { return rel, true, nil },
		func(*httpapi.Principal, string) (bool, error) { return true, nil },
		func(message, orderID string) error {
			if message == AdvancedMessage {
				return errTest
			}
			return nil
		})

	placed := decode[Order](t, do(t, s.HandlePlace, someone("usr_1"), "POST",
		`{"releaseId":"rel_1","items":["account"]}`))
	op := &httpapi.Principal{UserID: "usr_op", Roles: []string{"operator"}}

	rec := do(t, s.HandleReport, op, "POST", `{"status":"done"}`, "id", placed.ID, "item", "account")
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("report = %d (%s), want 500", rec.Code, rec.Body)
	}

	// The outcome stands: a retry of the same report is what the reporter does
	// next, and recording it twice must change nothing.
	got := decode[Order](t, do(t, s.HandleGet, someone("usr_1"), "GET", "", "id", placed.ID))
	if statusOf(got.Lines, "account") != StatusDone {
		t.Fatalf("the outcome was rolled back: %v", got.Lines)
	}
}

// TestAnOrderNobodyWillFulfilIsReported: the order is durable, and nothing is
// working on it. Answering 201 would hand back an order that looks placed and
// will never move — and replacing it is what the placer would do next, which
// they can only decide if they are told.
func TestAnOrderNobodyWillFulfilIsReported(t *testing.T) {
	store, err := NewStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	quit := make(chan struct{})
	loop := runloop.New(quit)
	go loop.Run()
	t.Cleanup(func() { close(quit) })

	rel := testRelease(t)
	s := New(loop, store, func() int64 { return 1700 },
		func(string) (catalog.Release, bool, error) { return rel, true, nil },
		func(*httpapi.Principal, string) (bool, error) { return true, nil },
		func(message, orderID string) error { return errTest })

	rec := do(t, s.HandlePlace, someone("usr_1"), "POST",
		`{"releaseId":"rel_1","items":["account"]}`)
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("place = %d (%s), want 500", rec.Code, rec.Body)
	}

	// And it is there, because it was written before fulfilment was started (I2).
	// A placer who retries gets a second order rather than a silent duplicate of
	// a first one they were told nothing about.
	if n := len(decode[[]Order](t, do(t, s.HandleList, someone("usr_1"), "GET", ""))); n != 1 {
		t.Fatalf("%d orders after a failed start, want the one that was written", n)
	}
}

// A decision needs a way in. Reporting refuses rejections on purpose — they are
// decisions with an author, not provisioning outcomes — so an approval process
// needs its own call, and that call records who decided and why.

func TestRejectingALineThroughTheAPI(t *testing.T) {
	s := newService(t)
	placed := decode[Order](t, do(t, s.HandlePlace, someone("usr_1"), "POST",
		`{"releaseId":"rel_1","items":["workplace"]}`))
	op := &httpapi.Principal{UserID: "usr_op", Roles: []string{"operator"}}

	rec := do(t, s.HandleDecide, op, "POST",
		`{"by":"usr_boss","reason":"kein Budget"}`, "id", placed.ID, "item", "laptop")
	if rec.Code != http.StatusOK {
		t.Fatalf("reject = %d (%s), want 200", rec.Code, rec.Body)
	}

	got := decode[Order](t, do(t, s.HandleGet, someone("usr_1"), "GET", "", "id", placed.ID))
	var laptop Line
	for _, l := range got.Lines {
		if l.ItemID == "laptop" {
			laptop = l
		}
	}
	if laptop.Status != StatusRejected {
		t.Fatalf("laptop = %s, want rejected", laptop.Status)
	}
	if laptop.DecidedBy != "usr_boss" || laptop.Reason != "kein Budget" {
		t.Fatalf("decision = %q/%q, want the approver and their words",
			laptop.DecidedBy, laptop.Reason)
	}
}

// TestARejectionNeedsAnApproverAndAReason: the API cannot be a way around the
// rule the transition holds.
func TestARejectionNeedsAnApproverAndAReason(t *testing.T) {
	s := newService(t)
	placed := decode[Order](t, do(t, s.HandlePlace, someone("usr_1"), "POST",
		`{"releaseId":"rel_1","items":["account"]}`))
	op := &httpapi.Principal{UserID: "usr_op", Roles: []string{"operator"}}

	for _, body := range []string{
		`{"reason":"kein Budget"}`,
		`{"by":"usr_boss"}`,
		`{}`,
	} {
		if rec := do(t, s.HandleDecide, op, "POST", body, "id", placed.ID, "item", "account"); rec.Code != http.StatusBadRequest {
			t.Errorf("reject %s = %d, want 400", body, rec.Code)
		}
	}
}

// TestARejectionWakesTheFulfilmentProcess: it settles a line exactly as a
// provisioning outcome does, so what waited on it must be told.
func TestARejectionWakesTheFulfilmentProcess(t *testing.T) {
	store, err := NewStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	quit := make(chan struct{})
	loop := runloop.New(quit)
	go loop.Run()
	t.Cleanup(func() { close(quit) })

	rel := testRelease(t)
	var woken []string
	s := New(loop, store, func() int64 { return 1700 },
		func(string) (catalog.Release, bool, error) { return rel, true, nil },
		func(*httpapi.Principal, string) (bool, error) { return true, nil },
		func(message, orderID string) error { woken = append(woken, message); return nil })

	placed := decode[Order](t, do(t, s.HandlePlace, someone("usr_1"), "POST",
		`{"releaseId":"rel_1","items":["workplace"]}`))
	op := &httpapi.Principal{UserID: "usr_op", Roles: []string{"operator"}}
	do(t, s.HandleDecide, op, "POST", `{"by":"usr_boss","reason":"x"}`, "id", placed.ID, "item", "laptop")

	if len(woken) != 2 || woken[1] != AdvancedMessage {
		t.Fatalf("woken = %v, want the start and then the advance", woken)
	}
}

func TestDecidingOnAnUnknownOrderOrLineIs404(t *testing.T) {
	s := newService(t)
	placed := decode[Order](t, do(t, s.HandlePlace, someone("usr_1"), "POST",
		`{"releaseId":"rel_1","items":["account"]}`))
	op := &httpapi.Principal{UserID: "usr_op", Roles: []string{"operator"}}
	body := `{"by":"usr_boss","reason":"x"}`

	if rec := do(t, s.HandleDecide, op, "POST", body, "id", "ord_nope", "item", "account"); rec.Code != http.StatusNotFound {
		t.Errorf("unknown order = %d, want 404", rec.Code)
	}
	if rec := do(t, s.HandleDecide, op, "POST", body, "id", placed.ID, "item", "ghost"); rec.Code != http.StatusNotFound {
		t.Errorf("unknown line = %d, want 404", rec.Code)
	}
	if rec := do(t, s.HandleDecide, op, "POST", "{not json", "id", placed.ID, "item", "account"); rec.Code != http.StatusBadRequest {
		t.Errorf("malformed = %d, want 400", rec.Code)
	}
}
