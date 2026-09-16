package catalog

import (
	"reflect"
	"testing"
)

// What "frozen" has to mean, proved structurally rather than field by field.
//
// The Items field promises that only value data is copied, so that an edit to the
// catalogue afterwards cannot reach into a release somebody has already ordered
// against. A struct copy does not deliver that on its own: every slice and every
// map in an Item is a header over memory the original still holds, so the copy
// shares it until something says otherwise.
//
// This was got wrong once. Texts, Variants and Targets were copied by hand and
// Eligible was not, because the copy names the fields somebody remembered on the
// day they wrote it, and the next field added is by definition not one of them.
// So this walks the struct instead of naming anything: a field added tomorrow is
// covered without anybody editing this test — and if it is a slice or a map it
// fails the fixture check below until somebody has put a value in it and thought
// about what freezing it means.

// everything is an item with every slice and every map filled, which is what makes
// the sharing check below non-vacuous.
func everything(id string) Item {
	it := item(id)
	it.Variants = []Variant{{ID: "large", Texts: map[string]string{"de": "gross"}}}
	it.Targets = []TargetRef{{System: "ad", Ref: "CN=X"}}
	it.Eligible = []string{"grp-a"}
	it.Keywords = []string{"Fernzugriff"}
	return it
}

// TestTheFixtureFillsEveryFieldOfAnItem is the half of this that catches the field
// nobody thought about: a new slice or map on Item fails here, in a test whose name
// says what is missing, rather than silently making the sharing check below pass by
// having nothing to compare.
func TestTheFixtureFillsEveryFieldOfAnItem(t *testing.T) {
	v := reflect.ValueOf(everything("vpn"))
	for i := 0; i < v.NumField(); i++ {
		f := v.Type().Field(i)
		switch v.Field(i).Kind() {
		case reflect.Slice, reflect.Map:
			if v.Field(i).Len() == 0 {
				t.Errorf("Item.%s is a %s and the fixture leaves it empty, so nothing "+
					"proves the release stops sharing it — fill it in everything()",
					f.Name, v.Field(i).Kind())
			}
		}
	}
}

// TestAReleaseSharesNothingWithTheCatalogue: after publishing, no slice and no map
// reachable from a released item may be the same memory as the catalogue's.
func TestAReleaseSharesNothingWithTheCatalogue(t *testing.T) {
	in := everything("vpn")
	rel, problems := Publish(Input{
		Catalogs: []Catalog{{ID: "cat", Rank: 1, Languages: []string{"de"}, Items: []string{"vpn"}}},
		Items:    []Item{in},
	})
	if len(problems) != 0 {
		t.Fatalf("publish refused the fixture: %+v", problems)
	}
	if len(rel.Items) != 1 {
		t.Fatalf("the release carries %d items, want 1", len(rel.Items))
	}
	shares(t, "Item", reflect.ValueOf(in), reflect.ValueOf(rel.Items[0]))
}

// shares reports every slice or map the two values hold in common.
//
// Pointer on a slice is the address of its first element and on a map the map
// itself, so two non-empty values sharing one are the same memory. Elements are
// paired by index, which is sound because freeze preserves the order within an
// item; only the items themselves are re-sorted, and the caller pairs those.
func shares(t *testing.T, path string, a, b reflect.Value) {
	t.Helper()
	switch a.Kind() {
	case reflect.Slice:
		if a.Len() > 0 && b.Len() > 0 && a.Pointer() == b.Pointer() {
			t.Errorf("%s: the release shares the catalogue's backing array, so editing "+
				"the catalogue reaches into a published release", path)
			return
		}
		for i := 0; i < a.Len() && i < b.Len(); i++ {
			shares(t, path+"[]", a.Index(i), b.Index(i))
		}
	case reflect.Map:
		if a.Len() > 0 && b.Len() > 0 && a.Pointer() == b.Pointer() {
			t.Errorf("%s: the release shares the catalogue's map, so editing the "+
				"catalogue reaches into a published release", path)
			return
		}
		for _, k := range a.MapKeys() {
			if v := b.MapIndex(k); v.IsValid() {
				shares(t, path+"["+k.String()+"]", a.MapIndex(k), v)
			}
		}
	case reflect.Struct:
		for i := 0; i < a.NumField(); i++ {
			shares(t, path+"."+a.Type().Field(i).Name, a.Field(i), b.Field(i))
		}
	case reflect.Pointer, reflect.Interface:
		if !a.IsNil() && !b.IsNil() {
			shares(t, path+"*", a.Elem(), b.Elem())
		}
	}
}
