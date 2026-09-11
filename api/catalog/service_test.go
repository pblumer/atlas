package catalog

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pblumer/atlas/api/runloop"
)

// The thin vertical slice: a catalogue and its products go in through the API,
// a release comes out, and the release is the frozen thing an order will name.
// Every store access runs on the run loop, which is invariant I3 — this service
// reaches shared state through the loop and through nothing else.

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

	n := int64(0)
	return New(loop, store, func() int64 { n++; return 1700 + n })
}

func do(t *testing.T, h http.HandlerFunc, method, target, body string, vals ...string) *httptest.ResponseRecorder {
	t.Helper()
	var rdr *strings.Reader
	if body == "" {
		rdr = strings.NewReader("")
	} else {
		rdr = strings.NewReader(body)
	}
	req := httptest.NewRequest(method, target, rdr)
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

// TestCatalogueRoundTripThroughTheAPI: create, read back, list.
func TestCatalogueRoundTripThroughTheAPI(t *testing.T) {
	s := newService(t)

	rec := do(t, s.HandleCreateCatalog, "POST", "/api/v1/catalogs",
		`{"rank":3,"languages":["de"],"texts":{"de":"Standard"}}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create = %d (%s), want 201", rec.Code, rec.Body)
	}
	created := decode[Catalog](t, rec)
	if created.ID == "" {
		t.Fatal("created catalogue has no id")
	}
	if created.CreatedAt == 0 {
		t.Error("created catalogue has no timestamp")
	}

	got := do(t, s.HandleGetCatalog, "GET", "/api/v1/catalogs/x", "", "id", created.ID)
	if got.Code != http.StatusOK {
		t.Fatalf("get = %d (%s), want 200", got.Code, got.Body)
	}
	if decode[Catalog](t, got).Rank != 3 {
		t.Error("rank did not survive the round trip")
	}

	list := do(t, s.HandleListCatalogs, "GET", "/api/v1/catalogs", "")
	if n := len(decode[[]Catalog](t, list)); n != 1 {
		t.Fatalf("list has %d catalogues, want 1", n)
	}
}

// TestUnknownCatalogueIs404, not an empty object: a reader must be able to tell
// "no such catalogue" from "a catalogue with nothing in it".
func TestUnknownCatalogueIs404(t *testing.T) {
	s := newService(t)
	rec := do(t, s.HandleGetCatalog, "GET", "/api/v1/catalogs/nope", "", "id", "nope")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("get = %d, want 404", rec.Code)
	}
}

// TestMalformedBodyIsRefused: a request that is not JSON must not reach the store.
func TestMalformedBodyIsRefused(t *testing.T) {
	s := newService(t)
	for _, h := range []http.HandlerFunc{s.HandleCreateCatalog, s.HandleSaveItem} {
		if rec := do(t, h, "POST", "/x", "{not json"); rec.Code != http.StatusBadRequest {
			t.Errorf("code = %d, want 400", rec.Code)
		}
	}
}

// TestPublishingProducesAFrozenRelease is the slice end to end.
func TestPublishingProducesAFrozenRelease(t *testing.T) {
	s := newService(t)

	cat := decode[Catalog](t, do(t, s.HandleCreateCatalog, "POST", "/api/v1/catalogs",
		`{"rank":1,"languages":["de"]}`))

	for _, id := range []string{"laptop", "vpn"} {
		rec := do(t, s.HandleSaveItem, "POST", "/api/v1/catalog-items",
			`{"id":"`+id+`","homeCatalog":"`+cat.ID+`","state":"active",`+
				`"texts":{"de":"`+id+`"},"approval":{"kind":"none"},`+
				`"provisionProcess":"prov-`+id+`","deprovisionProcess":"deprov-`+id+`"}`)
		if rec.Code != http.StatusOK {
			t.Fatalf("save %s = %d (%s)", id, rec.Code, rec.Body)
		}
	}

	// The catalogue offers both, and the VPN needs the laptop first.
	upd := do(t, s.HandleUpdateCatalog, "PATCH", "/api/v1/catalogs/x",
		`{"items":["laptop","vpn"],"edges":[{"from":"vpn","to":"laptop","kind":"requires"}]}`,
		"id", cat.ID)
	if upd.Code != http.StatusOK {
		t.Fatalf("update = %d (%s)", upd.Code, upd.Body)
	}

	pub := do(t, s.HandlePublish, "POST", "/api/v1/catalogs/x/releases", "", "id", cat.ID)
	if pub.Code != http.StatusCreated {
		t.Fatalf("publish = %d (%s), want 201", pub.Code, pub.Body)
	}
	rel := decode[Release](t, pub)

	if len(rel.Waves) != 2 {
		t.Fatalf("waves = %v, want two — the vpn waits for the laptop", rel.Waves)
	}
	if len(rel.Items) != 2 {
		t.Fatalf("release froze %d items, want 2", len(rel.Items))
	}
	if rel.CatalogID != cat.ID || rel.ID == "" || rel.CreatedAt == 0 {
		t.Fatalf("release is not identified: %+v", rel)
	}

	// And it is readable afterwards, which is what an order will do.
	list := do(t, s.HandleListReleases, "GET", "/api/v1/catalogs/x/releases", "", "id", cat.ID)
	got := decode[[]Release](t, list)
	if len(got) != 1 || got[0].ID != rel.ID {
		t.Fatalf("releases = %v, want the one just published", got)
	}
}

// TestPublishingRefusesAndSaysEverythingThatIsWrong: the refusal is the feature.
func TestPublishingRefusesAndSaysEverythingThatIsWrong(t *testing.T) {
	s := newService(t)
	cat := decode[Catalog](t, do(t, s.HandleCreateCatalog, "POST", "/api/v1/catalogs",
		`{"rank":1,"languages":["de","fr"]}`))

	// An item with no deprovision process and no French text.
	do(t, s.HandleSaveItem, "POST", "/api/v1/catalog-items",
		`{"id":"a","homeCatalog":"`+cat.ID+`","state":"active","texts":{"de":"A"},`+
			`"approval":{"kind":"none"},"provisionProcess":"prov"}`)
	do(t, s.HandleUpdateCatalog, "PATCH", "/x", `{"items":["a"]}`, "id", cat.ID)

	rec := do(t, s.HandlePublish, "POST", "/x", "", "id", cat.ID)
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("publish = %d (%s), want 422", rec.Code, rec.Body)
	}
	body := rec.Body.String()
	for _, want := range []string{"deprovision", "fr"} {
		if !strings.Contains(body, want) {
			t.Errorf("refusal does not mention %q: %s", want, body)
		}
	}

	// Nothing was written: a refused publish must leave no release behind.
	list := do(t, s.HandleListReleases, "GET", "/x", "", "id", cat.ID)
	if n := len(decode[[]Release](t, list)); n != 0 {
		t.Fatalf("%d releases after a refused publish, want none", n)
	}
}

func TestPublishingAnUnknownCatalogueIs404(t *testing.T) {
	s := newService(t)
	rec := do(t, s.HandlePublish, "POST", "/x", "", "id", "nope")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("publish = %d, want 404", rec.Code)
	}
}

// TestItemsAreListed so a catalogue maintainer can pick from what exists.
func TestItemsAreListed(t *testing.T) {
	s := newService(t)
	do(t, s.HandleSaveItem, "POST", "/x",
		`{"id":"a","homeCatalog":"c","state":"active","texts":{"de":"A"},`+
			`"approval":{"kind":"none"},"provisionProcess":"p","deprovisionProcess":"d"}`)

	rec := do(t, s.HandleListItems, "GET", "/api/v1/catalog-items", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("list = %d", rec.Code)
	}
	if n := len(decode[[]Item](t, rec)); n != 1 {
		t.Fatalf("got %d items, want 1", n)
	}
}

// TestSavingAnItemWithNoIDIsRefused: the id is the key the store files it under,
// and a blank one would overwrite whatever else has a blank one.
func TestSavingAnItemWithNoIDIsRefused(t *testing.T) {
	s := newService(t)
	rec := do(t, s.HandleSaveItem, "POST", "/x", `{"state":"active"}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("save = %d, want 400", rec.Code)
	}
}

// TestUpdatingAnUnknownCatalogueIs404.
func TestUpdatingAnUnknownCatalogueIs404(t *testing.T) {
	s := newService(t)
	rec := do(t, s.HandleUpdateCatalog, "PATCH", "/x", `{"items":[]}`, "id", "nope")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("update = %d, want 404", rec.Code)
	}
}

// TestUpdateChangesEveryEditableField, and leaves the rest alone. The id and the
// creation time are not editable: an id that moved would orphan every release
// naming it.
func TestUpdateChangesEveryEditableField(t *testing.T) {
	s := newService(t)
	cat := decode[Catalog](t, do(t, s.HandleCreateCatalog, "POST", "/x", `{"rank":1}`))

	rec := do(t, s.HandleUpdateCatalog, "PATCH", "/x",
		`{"rank":7,"texts":{"de":"Neu"},"languages":["de","fr"],`+
			`"items":["a"],"groups":["grp_1"],"edges":[{"from":"a","to":"b","kind":"requires"}]}`,
		"id", cat.ID)
	if rec.Code != http.StatusOK {
		t.Fatalf("update = %d (%s)", rec.Code, rec.Body)
	}

	got := decode[Catalog](t, rec)
	if got.Rank != 7 || got.Texts["de"] != "Neu" || len(got.Languages) != 2 ||
		len(got.Items) != 1 || len(got.Groups) != 1 || len(got.Edges) != 1 {
		t.Fatalf("update did not take: %+v", got)
	}
	if got.ID != cat.ID || got.CreatedAt != cat.CreatedAt {
		t.Errorf("identity moved: %s/%d, want %s/%d", got.ID, got.CreatedAt, cat.ID, cat.CreatedAt)
	}
	if got.UpdatedAt == cat.UpdatedAt {
		t.Error("UpdatedAt did not move")
	}
}

// brokenService returns a service whose store cannot be read, by replacing each
// of its directories with a file.
//
// The point is not the contrived failure but the response to it: a store that
// cannot be read must produce a 500, never a 200 with an empty list. The second
// is the dangerous one — it reads as "there are no catalogues" and would have a
// portal show an empty shop instead of an error.
func brokenService(t *testing.T) *Service {
	t.Helper()
	dir := t.TempDir()
	store, err := NewStore(dir)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	for _, sub := range []string{"catalogs", "items", "releases"} {
		p := filepath.Join(dir, sub)
		if err := os.RemoveAll(p); err != nil {
			t.Fatalf("RemoveAll: %v", err)
		}
		if err := os.WriteFile(p, []byte("not a directory"), 0o600); err != nil {
			t.Fatalf("WriteFile: %v", err)
		}
	}
	quit := make(chan struct{})
	loop := runloop.New(quit)
	go loop.Run()
	t.Cleanup(func() { close(quit) })
	return New(loop, store, func() int64 { return 1700 })
}

func TestAnUnreadableStoreIsAnError(t *testing.T) {
	s := brokenService(t)
	tests := []struct {
		name    string
		h       http.HandlerFunc
		method  string
		body    string
		pathVal bool
	}{
		{"list catalogues", s.HandleListCatalogs, "GET", "", false},
		{"create catalogue", s.HandleCreateCatalog, "POST", `{"rank":1}`, false},
		{"get catalogue", s.HandleGetCatalog, "GET", "", true},
		{"update catalogue", s.HandleUpdateCatalog, "PATCH", `{"rank":2}`, true},
		{"list products", s.HandleListItems, "GET", "", false},
		{"save product", s.HandleSaveItem, "POST", `{"id":"a"}`, false},
		{"publish", s.HandlePublish, "POST", "", true},
		{"list releases", s.HandleListReleases, "GET", "", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var rec *httptest.ResponseRecorder
			if tt.pathVal {
				rec = do(t, tt.h, tt.method, "/x", tt.body, "id", "cat_1")
			} else {
				rec = do(t, tt.h, tt.method, "/x", tt.body)
			}
			if rec.Code != http.StatusInternalServerError {
				t.Fatalf("code = %d (%s), want 500", rec.Code, rec.Body)
			}
		})
	}
}
