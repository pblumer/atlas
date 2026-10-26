package catalog

import (
	"encoding/json"
	"slices"
	"sort"
	"strings"
	"testing"
)

// TestEveryOperationIsConsistent holds the rows to the rules the compiler and the
// worker both read off them, so a row added later cannot contradict itself.
func TestEveryOperationIsConsistent(t *testing.T) {
	names := Names()
	if !sort.StringsAreSorted(names) || len(names) != len(ops) {
		t.Fatalf("Names() = %v", names)
	}
	for _, name := range names {
		op, _ := Lookup(name)
		if !slices.Contains(Areas, op.Area) {
			t.Errorf("%s: area %q is not one of %v", name, op.Area, Areas)
		}
		if op.Label == "" {
			t.Errorf("%s has no label", name)
		}
		for _, a := range append(append([]string{}, op.Needs...), op.Takes...) {
			if !slices.Contains(Attributes, a) {
				t.Errorf("%s names an attribute %q that is not one of %v", name, a, Attributes)
			}
		}
		for _, a := range op.Needs {
			if slices.Contains(op.Takes, a) {
				t.Errorf("%s both needs and takes %q; needs is enough", name, a)
			}
		}
		if op.NeedsOneTaken && len(op.Takes) < 2 {
			t.Errorf("%s needs one of fewer than two optional values", name)
		}
		if (op.MaxResults.Max > 0) != (op.Result == ResultItems) {
			t.Errorf("%s: a maxResults belongs to exactly the listings", name)
		}
		if op.MaxResults.Default > op.MaxResults.Max {
			t.Errorf("%s: default %d above ceiling %d", name, op.MaxResults.Default, op.MaxResults.Max)
		}
		if op.NeedsResult && op.Result == ResultNone {
			t.Errorf("%s needs a result it never returns", name)
		}
		if op.TakesResult() != (op.Result != ResultNone) {
			t.Errorf("%s: TakesResult disagrees with its result", name)
		}
	}
}

// The operations are the loop a process runs on a task and a page, spelled like the
// Jira Worker's where the meaning is the same (ADR-draft-wikiwikibangbang-worker).
func TestTheOperationsAreTheRecordsTable(t *testing.T) {
	want := []string{"add-comment", "append-to-page", "create-item", "get-item", "query-items", "read-page", "transition-item", "update-item"}
	if got := Names(); !slices.Equal(got, want) {
		t.Errorf("Names() = %v, want %v", got, want)
	}
	reads := map[string]Result{"get-item": ResultItem, "read-page": ResultText, "query-items": ResultItems}
	for name, result := range reads {
		op, _ := Lookup(name)
		if !op.NeedsResult || op.Result != result {
			t.Errorf("%s: a read must keep its answer (%v), got needs=%v result=%v", name, result, op.NeedsResult, op.Result)
		}
	}
	for _, name := range []string{"create-item", "update-item", "transition-item", "add-comment", "append-to-page"} {
		op, _ := Lookup(name)
		if op.NeedsResult {
			t.Errorf("%s is a write; its answer is optional", name)
		}
	}
	create, _ := Lookup("create-item")
	for _, a := range []string{AttrSpace, AttrItemType, AttrTitle} {
		if !create.NeedsAttr(a) {
			t.Errorf("create-item does not need %s", a)
		}
	}
	if create.TakesAttr(AttrLabels) {
		t.Error("create-item takes labels; a create must stay one request, so labels are set by update-item")
	}
	update, _ := Lookup("update-item")
	if !update.NeedsOneTaken || !update.TakesAttr(AttrTitle) || !update.TakesAttr(AttrProperties) || !update.TakesAttr(AttrLabels) {
		t.Errorf("update-item = %+v", update)
	}
	appendOp, _ := Lookup("append-to-page")
	if !appendOp.NeedsAttr(AttrBody) || !appendOp.NeedsAttr(AttrItem) {
		t.Errorf("append-to-page = %+v", appendOp)
	}
	if _, ok := Lookup("assign-item"); ok {
		t.Error("an assignee is a property of the wiki's type; assigning is update-item")
	}
}

func TestCheckRef(t *testing.T) {
	for _, ok := range []string{"OPS-12", "0de480317d5a01d67a16aa208b67a4d2", "a.b_c~d"} {
		if err := CheckRef("item", ok); err != nil {
			t.Errorf("CheckRef(%q) = %v", ok, err)
		}
	}
	for _, bad := range []string{"", " ", "OPS/12", "../x", "a b", "a?b", "a#b", strings.Repeat("a", 201)} {
		if err := CheckRef("item", bad); err == nil {
			t.Errorf("CheckRef(%q) accepted a value that is no item id or key", bad)
		}
	}
}

func TestCheckFieldName(t *testing.T) {
	for _, ok := range []string{"source-id", "due date", "Kundennummer"} {
		if err := CheckFieldName(ok); err != nil {
			t.Errorf("CheckFieldName(%q) = %v", ok, err)
		}
	}
	for _, bad := range []string{"", "  ", "a=b", "a\nb", strings.Repeat("x", 201)} {
		if err := CheckFieldName(bad); err == nil {
			t.Errorf("CheckFieldName(%q) accepted it", bad)
		}
	}
}

func decode(t *testing.T, raw string) any {
	t.Helper()
	dec := json.NewDecoder(strings.NewReader(raw))
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err != nil {
		t.Fatal(err)
	}
	return v
}

func TestCheckBlocks(t *testing.T) {
	ok := decode(t, `[{"kind":"heading","level":2,"text":"Log"},{"kind":"paragraph","text":"x","marks":[{"type":"bold","start":0,"end":1}]},{"kind":"divider"}]`)
	if err := CheckBlocks(ok); err != nil {
		t.Errorf("CheckBlocks = %v", err)
	}
	for raw, why := range map[string]string{
		`{"kind":"paragraph"}`:         "an object, not a list",
		`[]`:                           "an empty list writes nothing",
		`["text"]`:                     "a string, not a block",
		`[{"text":"no kind"}]`:         "a block without a kind",
		`[{"kind":""}]`:                "an empty kind",
		`[{"kind":3}]`:                 "a kind that is no string",
		`[{"kind":"paragraph"}, null]`: "a null block",
	} {
		if err := CheckBlocks(decode(t, raw)); err == nil {
			t.Errorf("CheckBlocks(%s) accepted %s", raw, why)
		}
	}
}

func TestCheckProperties(t *testing.T) {
	ok := decode(t, `{"assignee":"=form.owner","due":"2026-11-01","points":3,"source-id":"REQ-1"}`).(map[string]any)
	if err := CheckProperties(ok, "source-id"); err != nil {
		t.Errorf("CheckProperties = %v", err)
	}
	if err := CheckProperties(ok, "missing"); err == nil {
		t.Error("a matchField the properties do not set was accepted")
	}
	if err := CheckProperties(decode(t, `{"":"x"}`).(map[string]any), ""); err == nil {
		t.Error("an empty property name was accepted")
	}
	if err := CheckProperties(decode(t, `{"a=b":"x"}`).(map[string]any), ""); err == nil {
		t.Error("a property name the wiki's where cannot address was accepted")
	}
}

func TestSplitLabels(t *testing.T) {
	got := SplitLabels(" onboarding, ops ,, it ")
	if want := []string{"onboarding", "ops", "it"}; !slices.Equal(got, want) {
		t.Errorf("SplitLabels = %v, want %v", got, want)
	}
	if got := SplitLabels("  "); len(got) != 0 {
		t.Errorf("SplitLabels(blank) = %v, want none", got)
	}
}

func TestIsExpression(t *testing.T) {
	if !IsExpression(" =x") || IsExpression("x") || IsExpression(3) {
		t.Error("IsExpression is wrong")
	}
}
