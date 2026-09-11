package order

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
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
		})
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
