package taskfolder

import (
	"testing"
)

// TestStoreRoundTrip covers the durable half: a folder written is a folder read
// back, unknown ids miss cleanly, and deletion is idempotent.
func TestStoreRoundTrip(t *testing.T) {
	st, err := NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	rec := Folder{
		ID: "aa11", Name: "Kunden Anfragen", Owner: "usr_1",
		Visibility: VisibilityPrivate, CreatedAt: 10, UpdatedAt: 10,
		Rule: Rule{Match: MatchAll, Conditions: []Condition{
			{Field: FieldProcess, Op: OpIs, Value: "kunden-anfrage"},
		}},
	}
	if err := st.Save(rec); err != nil {
		t.Fatalf("Save: %v", err)
	}
	got, ok, err := st.Get("aa11")
	if err != nil || !ok {
		t.Fatalf("Get: ok=%v err=%v", ok, err)
	}
	if got.Name != rec.Name || len(got.Rule.Conditions) != 1 || got.Rule.Conditions[0].Value != "kunden-anfrage" {
		t.Errorf("round trip lost data: %+v", got)
	}
	if _, ok, err := st.Get("bb22"); ok || err != nil {
		t.Errorf("Get(unknown) = ok %v, err %v; want a clean miss", ok, err)
	}
	if _, ok, err := st.Get("../escape"); ok || err != nil {
		t.Errorf("Get(unsafe id) = ok %v, err %v; want a clean miss", ok, err)
	}
	if err := st.Delete("aa11"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if err := st.Delete("aa11"); err != nil {
		t.Errorf("Delete is not idempotent: %v", err)
	}
}

// TestStoreOrdersForTheSidebar pins the listing order the sidebar depends on.
func TestStoreOrdersForTheSidebar(t *testing.T) {
	st, err := NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range []Folder{
		{ID: "cc", Name: "third", Position: 2, CreatedAt: 1},
		{ID: "aa", Name: "second", Position: 1, CreatedAt: 9},
		{ID: "bb", Name: "first", Position: 1, CreatedAt: 3},
	} {
		if err := st.Save(f); err != nil {
			t.Fatal(err)
		}
	}
	all, err := st.LoadAll()
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, f := range all {
		names = append(names, f.Name)
	}
	if len(names) != 3 || names[0] != "first" || names[1] != "second" || names[2] != "third" {
		t.Errorf("LoadAll order = %v, want [first second third]", names)
	}
}

// TestVisibleTo covers who sees which folder: the owner always, a group folder
// only for members, an org folder for everyone, and a stranger's private folder
// for nobody.
func TestVisibleTo(t *testing.T) {
	st, err := NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range []Folder{
		{ID: "11", Name: "mine", Owner: "me", Visibility: VisibilityPrivate, Position: 1},
		{ID: "22", Name: "theirs", Owner: "them", Visibility: VisibilityPrivate, Position: 2},
		{ID: "33", Name: "team", Owner: "them", Visibility: VisibilityGroup, GroupID: "grp_sd", Position: 3},
		{ID: "44", Name: "other team", Owner: "them", Visibility: VisibilityGroup, GroupID: "grp_hr", Position: 4},
		{ID: "55", Name: "everyone", Owner: "them", Visibility: VisibilityOrg, Position: 5},
	} {
		if err := st.Save(f); err != nil {
			t.Fatal(err)
		}
	}
	got, err := st.VisibleTo("me", []string{"grp_sd"})
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, f := range got {
		names = append(names, f.Name)
	}
	want := []string{"mine", "team", "everyone"}
	if len(names) != len(want) {
		t.Fatalf("VisibleTo = %v, want %v", names, want)
	}
	for i := range want {
		if names[i] != want[i] {
			t.Fatalf("VisibleTo = %v, want %v", names, want)
		}
	}
}

// TestEditableBy holds the rule that a shared folder is shared to be used, not
// rewritten: only its owner may change it.
func TestEditableBy(t *testing.T) {
	f := Folder{Owner: "me", Visibility: VisibilityOrg}
	if !f.EditableBy("me") {
		t.Error("the owner may not edit their own folder")
	}
	if f.EditableBy("them") {
		t.Error("a viewer of a shared folder may edit it")
	}
}

// TestNewIDIsAddressable proves a minted id is one the store will accept as a
// filename — the guard that keeps a request-supplied id inside the directory.
func TestNewIDIsAddressable(t *testing.T) {
	id, err := NewID()
	if err != nil {
		t.Fatal(err)
	}
	st, err := NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := st.Save(Folder{ID: id, Name: "x"}); err != nil {
		t.Fatalf("a minted id was refused by its own store: %v", err)
	}
	if _, ok, err := st.Get(id); !ok || err != nil {
		t.Errorf("Get(minted id) = ok %v err %v", ok, err)
	}
}
