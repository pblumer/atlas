package panorama

import (
	"strings"
	"testing"
)

func TestStorePersistsModelsAndOriginalXML(t *testing.T) {
	dir := t.TempDir()
	store, err := NewStore(dir)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	xml := string(minimalModel(t)) + "\n<!-- preserved vendor metadata -->\n"
	rec := Model{
		ID: "00112233445566778899aabbccddeeff", ApplicationID: "app-1",
		Name: "Landscape", Notation: NotationArchiMate32, Revision: 1,
		XML: xml, CreatedAt: 10, UpdatedAt: 20,
	}
	if err := store.Save(rec); err != nil {
		t.Fatalf("Save: %v", err)
	}

	reopened, err := NewStore(dir)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	got, ok, err := reopened.Get(rec.ID)
	if err != nil || !ok {
		t.Fatalf("Get = ok:%v err:%v", ok, err)
	}
	if got.XML != xml {
		t.Fatal("stored XML changed; Open Exchange round trip must be byte-preserving")
	}
	if got != rec {
		t.Errorf("Get = %#v, want %#v", got, rec)
	}
}

func TestStoreListsNewestFirstAndFiltersByApplication(t *testing.T) {
	store, err := NewStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	for _, rec := range []Model{
		{ID: strings.Repeat("a", 32), ApplicationID: "app-a", Name: "old", UpdatedAt: 10},
		{ID: strings.Repeat("b", 32), ApplicationID: "app-b", Name: "other", UpdatedAt: 30},
		{ID: strings.Repeat("c", 32), ApplicationID: "app-a", Name: "new", UpdatedAt: 20},
	} {
		if err := store.Save(rec); err != nil {
			t.Fatalf("Save %s: %v", rec.Name, err)
		}
	}
	all, err := store.LoadAll()
	if err != nil {
		t.Fatalf("LoadAll: %v", err)
	}
	if len(all) != 3 || all[0].Name != "other" || all[1].Name != "new" || all[2].Name != "old" {
		t.Fatalf("LoadAll order = %#v", all)
	}
	mine, err := store.ForApplication("app-a")
	if err != nil {
		t.Fatalf("ForApplication: %v", err)
	}
	if len(mine) != 2 || mine[0].Name != "new" || mine[1].Name != "old" {
		t.Fatalf("ForApplication = %#v", mine)
	}
}

func TestStoreRefusesUnsafeIDs(t *testing.T) {
	store, err := NewStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	if err := store.Save(Model{ID: "../escape"}); err == nil {
		t.Fatal("Save unsafe id: want error")
	}
	if _, ok, err := store.Get("../escape"); err != nil || ok {
		t.Fatalf("Get unsafe id = ok:%v err:%v, want clean miss", ok, err)
	}
	if err := store.Delete("../escape"); err != nil {
		t.Fatalf("Delete unsafe id: %v", err)
	}
}
