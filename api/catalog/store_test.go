package catalog

import (
	"os"
	"path/filepath"
	"testing"
)

// A release is the frozen thing an order names, so what it freezes has to be the
// items themselves and not a reference to rows that keep changing. These tests
// state that, and the store's job of keeping it.

func newItem(id string) Item {
	return Item{
		ID: id, HomeCatalog: "cat", State: StateActive,
		Texts:            map[string]string{"de": id},
		Approval:         Approval{Kind: KindNone},
		ProvisionProcess: "prov", DeprovisionProcess: "deprov",
	}
}

func publishOne(t *testing.T, in Input) Release {
	t.Helper()
	rel, problems := Publish(in)
	if len(problems) != 0 {
		t.Fatalf("Publish: %v", problems)
	}
	return rel
}

// TestReleaseFreezesTheItems is the snapshot rule: an order names one release, so
// editing the catalogue afterwards must not change what was ordered.
func TestReleaseFreezesTheItems(t *testing.T) {
	in := Input{
		Catalogs: []Catalog{{ID: "cat", Rank: 1, Languages: []string{"de"}, Items: []string{"a"}}},
		Items:    []Item{newItem("a")},
	}
	rel := publishOne(t, in)

	if len(rel.Items) != 1 || rel.Items[0].ID != "a" {
		t.Fatalf("release items = %v, want the item itself", rel.Items)
	}

	// The catalogue changes after publishing. The release must not.
	in.Items[0].Texts["de"] = "renamed"
	in.Items[0].ProvisionProcess = "something-else"
	if rel.Items[0].Texts["de"] == "renamed" {
		t.Error("release text followed an edit made after publishing")
	}
	if rel.Items[0].ProvisionProcess != "prov" {
		t.Error("release binding followed an edit made after publishing")
	}
}

// TestReleaseItemsAreOrdered so two releases of the same catalogue are comparable
// byte for byte.
func TestReleaseItemsAreOrdered(t *testing.T) {
	in := Input{
		Catalogs: []Catalog{{ID: "cat", Rank: 1, Languages: []string{"de"},
			Items: []string{"c", "a", "b"}}},
		Items: []Item{newItem("c"), newItem("a"), newItem("b")},
	}
	rel := publishOne(t, in)
	want := []string{"a", "b", "c"}
	for i, w := range want {
		if rel.Items[i].ID != w {
			t.Fatalf("item %d = %s, want %s", i, rel.Items[i].ID, w)
		}
	}
}

func TestStoreRoundTripsACatalogue(t *testing.T) {
	s, err := NewStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}

	cat := Catalog{ID: "cat_1", Rank: 3, Languages: []string{"de"},
		Texts: map[string]string{"de": "Standard"}, Items: []string{"a"}}
	if err := s.SaveCatalog(cat); err != nil {
		t.Fatalf("SaveCatalog: %v", err)
	}

	got, ok, err := s.Catalog("cat_1")
	if err != nil || !ok {
		t.Fatalf("Catalog: %v, found %v", err, ok)
	}
	if got.Rank != 3 || got.Texts["de"] != "Standard" {
		t.Fatalf("got %+v, want the record back", got)
	}
}

func TestStoreRoundTripsAnItemAndARelease(t *testing.T) {
	s, err := NewStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}

	if err := s.SaveItem(newItem("a")); err != nil {
		t.Fatalf("SaveItem: %v", err)
	}
	items, err := s.Items()
	if err != nil || len(items) != 1 {
		t.Fatalf("Items: %v, got %d", err, len(items))
	}

	rel := Release{ID: "rel_1", CatalogID: "cat_1", CreatedAt: 1700,
		Waves: [][]string{{"a"}}, Items: []Item{newItem("a")}}
	if err := s.SaveRelease(rel); err != nil {
		t.Fatalf("SaveRelease: %v", err)
	}
	got, ok, err := s.Release("rel_1")
	if err != nil || !ok {
		t.Fatalf("Release: %v, found %v", err, ok)
	}
	if len(got.Items) != 1 || got.Items[0].ID != "a" {
		t.Fatalf("release lost its frozen items: %+v", got)
	}
}

// TestReleasesComeBackNewestFirst: the current release of a catalogue is the one
// anybody asks for, so a listing that buried it would be answered by scanning.
func TestReleasesComeBackNewestFirst(t *testing.T) {
	s, err := NewStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	for _, r := range []Release{
		{ID: "rel_1", CatalogID: "cat", CreatedAt: 100},
		{ID: "rel_3", CatalogID: "cat", CreatedAt: 300},
		{ID: "rel_2", CatalogID: "cat", CreatedAt: 200},
		{ID: "rel_x", CatalogID: "other", CreatedAt: 400},
	} {
		if err := s.SaveRelease(r); err != nil {
			t.Fatalf("SaveRelease %s: %v", r.ID, err)
		}
	}

	got, err := s.ReleasesOf("cat")
	if err != nil {
		t.Fatalf("ReleasesOf: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("got %d releases, want 3 — the other catalogue's must not appear", len(got))
	}
	if got[0].ID != "rel_3" {
		t.Fatalf("newest is %s, want rel_3", got[0].ID)
	}
}

// TestAnUnknownRecordIsNotAnError: asking for something that is not there is an
// ordinary answer, not a failure, and callers must be able to tell the two apart.
func TestAnUnknownRecordIsNotAnError(t *testing.T) {
	s, err := NewStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	if _, ok, err := s.Catalog("nope"); err != nil || ok {
		t.Fatalf("Catalog(nope) = found %v, err %v; want not found, no error", ok, err)
	}
	if _, ok, err := s.Release("nope"); err != nil || ok {
		t.Fatalf("Release(nope) = found %v, err %v; want not found, no error", ok, err)
	}
}

// TestCatalogsComeBackByRank: rank decides which catalogue a user sees, so a
// listing in rank order is what every reader of it wants.
func TestCatalogsComeBackByRank(t *testing.T) {
	s, err := NewStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	for _, c := range []Catalog{{ID: "c_hi", Rank: 9}, {ID: "c_lo", Rank: 1}, {ID: "c_mid", Rank: 5}} {
		if err := s.SaveCatalog(c); err != nil {
			t.Fatalf("SaveCatalog: %v", err)
		}
	}
	got, err := s.Catalogs()
	if err != nil {
		t.Fatalf("Catalogs: %v", err)
	}
	want := []string{"c_lo", "c_mid", "c_hi"}
	for i, w := range want {
		if got[i].ID != w {
			t.Fatalf("position %d = %s, want %s", i, got[i].ID, w)
		}
	}
}

func TestItemRoundTrip(t *testing.T) {
	s, err := NewStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	if err := s.SaveItem(newItem("vpn")); err != nil {
		t.Fatalf("SaveItem: %v", err)
	}
	got, ok, err := s.Item("vpn")
	if err != nil || !ok {
		t.Fatalf("Item: %v, found %v", err, ok)
	}
	if got.ProvisionProcess != "prov" {
		t.Fatalf("got %+v, want the record back", got)
	}
	if _, ok, err := s.Item("nope"); err != nil || ok {
		t.Fatalf("Item(nope) = found %v, err %v", ok, err)
	}
}

// TestInputForCarriesOnlyWhatTheCatalogueOffers, but every catalogue — because
// rank uniqueness is a property of the set, not of the one being published.
func TestInputForCarriesOnlyWhatTheCatalogueOffers(t *testing.T) {
	s, err := NewStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	if err := s.SaveCatalog(Catalog{ID: "cat", Rank: 1, Languages: []string{"de"},
		Items: []string{"a", "b"}}); err != nil {
		t.Fatalf("SaveCatalog: %v", err)
	}
	if err := s.SaveCatalog(Catalog{ID: "other", Rank: 2}); err != nil {
		t.Fatalf("SaveCatalog: %v", err)
	}
	for _, id := range []string{"a", "b", "unoffered"} {
		if err := s.SaveItem(newItem(id)); err != nil {
			t.Fatalf("SaveItem %s: %v", id, err)
		}
	}

	edges := []Edge{{From: "b", To: "a", Kind: EdgeRequires}}
	in, ok, err := s.InputFor("cat", edges)
	if err != nil || !ok {
		t.Fatalf("InputFor: %v, found %v", err, ok)
	}
	if len(in.Items) != 2 || in.Items[0].ID != "a" || in.Items[1].ID != "b" {
		t.Fatalf("items = %v, want a and b only, sorted", in.Items)
	}
	if len(in.Catalogs) != 2 {
		t.Fatalf("catalogs = %d, want both — rank uniqueness spans the set", len(in.Catalogs))
	}
	if len(in.Edges) != 1 {
		t.Fatalf("edges = %v, want the one passed in", in.Edges)
	}

	// And it publishes, which is the point of gathering it this way.
	if _, problems := Publish(in); len(problems) != 0 {
		t.Fatalf("Publish: %v", problems)
	}
}

func TestInputForAnUnknownCatalogue(t *testing.T) {
	s, err := NewStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	if _, ok, err := s.InputFor("nope", nil); err != nil || ok {
		t.Fatalf("InputFor(nope) = found %v, err %v; want not found, no error", ok, err)
	}
}

// TestReleaseFreezesVariantsToo: a variant's label is what an orderer picked
// from, so an edit to it after publishing would change what they chose.
func TestReleaseFreezesVariantsToo(t *testing.T) {
	it := newItem("laptop")
	it.Variants = []Variant{{ID: "std", Texts: map[string]string{"de": "Standard"}}}
	in := Input{
		Catalogs: []Catalog{{ID: "cat", Rank: 1, Languages: []string{"de"}, Items: []string{"laptop"}}},
		Items:    []Item{it},
	}
	rel := publishOne(t, in)

	if len(rel.Items[0].Variants) != 1 {
		t.Fatalf("variants = %v, want one", rel.Items[0].Variants)
	}
	in.Items[0].Variants[0].Texts["de"] = "renamed"
	if rel.Items[0].Variants[0].Texts["de"] != "Standard" {
		t.Fatal("release variant followed an edit made after publishing")
	}
}

// TestStoreRefusesAnUnusableDirectory: a store that silently accepted a path it
// cannot write to would fail later, on somebody's save, with no clue why.
func TestStoreRefusesAnUnusableDirectory(t *testing.T) {
	f := filepath.Join(t.TempDir(), "afile")
	if err := os.WriteFile(f, []byte("x"), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	if _, err := NewStore(f); err == nil {
		t.Fatal("NewStore on a path that is a file must fail")
	}
}
