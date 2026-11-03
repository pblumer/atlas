package catalog

import "testing"

// The store's listing of every release and its two deletes: what an administrator's
// purge (ADR-0469) and a release listing rest on, held to doing exactly that.
func TestTheStoreListsEveryReleaseAndDeletesWhatItIsTold(t *testing.T) {
	s, err := NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := s.SaveCatalog(Catalog{ID: "cat", Rank: 1}); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"rel_1", "rel_2"} {
		if err := s.SaveRelease(Release{ID: id, CatalogID: "cat"}); err != nil {
			t.Fatal(err)
		}
	}
	all, err := s.Releases()
	if err != nil || len(all) != 2 {
		t.Fatalf("releases = %d (%v), want 2", len(all), err)
	}
	if err := s.DeleteRelease("rel_1"); err != nil {
		t.Fatal(err)
	}
	if all, _ := s.Releases(); len(all) != 1 || all[0].ID != "rel_2" {
		t.Errorf("after deleting rel_1: %+v", all)
	}
	if err := s.DeleteCatalog("cat"); err != nil {
		t.Fatal(err)
	}
	if _, found, _ := s.Catalog("cat"); found {
		t.Error("the catalogue survived its delete")
	}
}
