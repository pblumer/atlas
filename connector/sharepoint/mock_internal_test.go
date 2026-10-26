package sharepoint

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

// The mockup's filter, case by case: what it matches, and that what lies outside its
// subset is refused with the subset named rather than read as something it might mean.
func TestMockFilterCases(t *testing.T) {
	l := &mockList{title: "Antraege", columns: map[string]bool{"Title": true, "Betrag": true, "Dringend": true, "Tags": true}}
	items := []*mockItem{
		{id: 1, fields: map[string]any{"Title": "O'Brien", "Betrag": json.Number("100"), "Dringend": true}},
		{id: 2, fields: map[string]any{"Title": "Alpha", "Betrag": float64(2500), "Dringend": false}},
		{id: 3, fields: map[string]any{"Title": "beta", "Betrag": 7, "Tags": []any{"x"}}},
		{id: 4, fields: map[string]any{"Title": "Gamma", "Betrag": int64(-3)}},
		{id: 5, fields: map[string]any{"Betrag": "viel"}},
	}
	matches := func(filter string) []int {
		t.Helper()
		match, err := parseMockFilter("list-items", filter, l)
		if err != nil {
			t.Fatalf("%q: %v", filter, err)
		}
		var ids []int
		for _, it := range items {
			if match(it) {
				ids = append(ids, it.id)
			}
		}
		return ids
	}
	for filter, want := range map[string]string{
		"fields/Title eq 'O''Brien'":                           "[1]",
		"fields/Title gt 'alpha'":                              "[1 3 4]",
		"fields/Title lt 'b'":                                  "[2]",
		"fields/Title ne null":                                 "[1 2 3 4]",
		"fields/Title eq null":                                 "[5]",
		"fields/Betrag ge 7":                                   "[1 2 3]",
		"fields/Betrag lt -2.5":                                "[4]",
		"fields/Betrag le .5e1":                                "[4]",
		"fields/Betrag ne 100":                                 "[2 3 4 5]",
		"fields/Betrag eq 'viel'":                              "[5]",
		"fields/Dringend ne true":                              "[2 3 4 5]",
		"fields/Dringend gt false":                             "[]",
		"fields/Tags eq 'x'":                                   "[]",
		"fields/Tags ne 1":                                     "[1 2 3 4 5]",
		"NOT (fields/Dringend eq true) and fields/Betrag gt 0": "[2 3]",
		"startswith(fields/Title, 'g') or fields/ID eq 1":      "[1 4]",
		"startswith(fields/Betrag,'1')":                        "[]",
		"startswith(fields/id,'1')":                            "[]",
		"fields/id ge '3'":                                     "[3 4 5]",
		"\tfields/id\neq 2\r":                                  "[2]",
	} {
		if got := fmt.Sprint(matches(filter)); got != want {
			t.Errorf("%q matched %s, want %s", filter, got, want)
		}
	}

	for _, bad := range []string{
		"fields/Title eq 'offen",          // a quote not closed
		"fields/Title eq 'a' extra",       // something after a whole filter
		"fields/Title eq",                 // no value
		"fields/Title",                    // no operator
		"fields/Title has 'a'",            // not an operator of the subset
		"fields/Title eq (",               // punctuation where a value goes
		"fields/Title eq offen",           // an unquoted word
		"fields/ eq 'a'",                  // no column
		"fields/a/b eq 'a'",               // a path
		"Title eq 'a'",                    // not under fields/
		"(fields/Title eq 'a'",            // a parenthesis not closed
		"startswith fields/Title",         // no parenthesis
		"startswith(fields/Title 'a')",    // no comma
		"startswith(fields/Title, Alpha)", // an unquoted prefix
		"startswith(fields/Title, 'a'",    // not closed
		"startswith(",                     // nothing at all
		"not",                             // nothing to negate
		"fields/Title eq 'a' and",         // nothing after and
		"fields/Title eq 'a' or",          // nothing after or
		"fields/Betrag gt Infinity",       // not a number the subset reads
		"fields/Title eq 'a' and )",       // a stray parenthesis
	} {
		if _, err := parseMockFilter("list-items", bad, l); err == nil || !strings.Contains(err.Error(), "the mockup understands") {
			t.Errorf("%q = %v, want a refusal naming the subset", bad, err)
		}
	}
	if _, err := parseMockFilter("list-items", "fields/Fehlt eq 1 or fields/Title eq 'a'", l); err == nil || !strings.Contains(err.Error(), `no column "Fehlt"`) {
		t.Errorf("unknown column = %v, want it named", err)
	}
}

// The odds and ends a site answers with: a site addressed by its Graph id, a file type
// nobody registered, a nested value copied rather than shared, and the refusals that
// sit in front of a file operation.
func TestMockSiteEdges(t *testing.T) {
	m := NewMockSites(nil).Site("intranet")
	ctx := context.Background()
	id := "contoso.sharepoint.com,aaaa,bbbb"
	if _, err := m.Do(ctx, Request{Site: id, List: "L", Fields: map[string]any{"Meta": map[string]any{"a": []any{1}}}}); err != nil {
		t.Fatal(err)
	}
	got, err := m.Do(ctx, Request{Operation: "get-item", Site: id, List: "L", ItemID: "1"})
	if err != nil {
		t.Fatal(err)
	}
	item := got.(map[string]any)
	if !strings.HasPrefix(item["webUrl"].(string), "https://contoso.sharepoint.com/") {
		t.Errorf("webUrl of a site named by id = %v", item["webUrl"])
	}
	item["fields"].(map[string]any)["Meta"].(map[string]any)["a"] = "changed"
	again, _ := m.Do(ctx, Request{Operation: "get-item", Site: id, List: "L", ItemID: "1"})
	if _, ok := again.(map[string]any)["fields"].(map[string]any)["Meta"].(map[string]any)["a"].([]any); !ok {
		t.Error("a caller's edit reached the value the mockup keeps")
	}

	up, err := m.Do(ctx, Request{Operation: "upload-file", Site: id, Path: "x.zzzunknown", Content: "x"})
	if err != nil {
		t.Fatal(err)
	}
	if mt := up.(map[string]any)["file"].(map[string]any)["mimeType"]; mt != "application/octet-stream" {
		t.Errorf("mime type of an unknown extension = %v", mt)
	}

	for _, c := range []struct {
		req  Request
		want string
	}{
		{Request{Operation: "upload-file", Site: id, List: "L", Path: "a", Content: "x"}, "is a list"},
		{Request{Operation: "upload-file", Site: id, Path: "b", Content: "!!", Encoding: EncodingBase64}, "not"},
		{Request{Operation: "create-folder", Site: id, List: "L", Path: "a"}, "is a list"},
		{Request{Operation: "create-folder", Site: id, Path: "x.zzzunknown/sub"}, "is a file"},
		{Request{Operation: "download-file", Site: id, List: "Nirgends", Path: "a"}, "no document library"},
		{Request{Operation: "list-folder", Site: id, List: "Nirgends"}, "no document library"},
		{Request{Operation: "list-folder", Site: id, PageToken: "%zz"}, "pass nextPageToken back unchanged"},
		{Request{Operation: "list-items", Site: id, List: "Nirgends"}, "no list"},
		{Request{Operation: "delete-item", Site: id, List: "L", ItemID: "x"}, "not a list item id"},
	} {
		if _, err := m.Do(ctx, c.req); err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s %+v = %v, want a refusal mentioning %q", c.req.Operation, c.req, err, c.want)
		}
	}
	if got, err := m.Do(ctx, Request{Operation: "delete-file", Site: id, List: "Nirgends", Path: "a"}); err != nil || got.(map[string]any)["deleted"] != false {
		t.Errorf("delete-file in a missing library = %v, %v; want deleted=false", got, err)
	}
	if got, err := m.Do(ctx, Request{Operation: "delete-item", Site: id, List: "L", ItemID: "9"}); err != nil || got.(map[string]any)["deleted"] != false {
		t.Errorf("delete-item of a missing item = %v, %v; want deleted=false", got, err)
	}
	// A request prepare refuses still lands in the journal, under the operation it named.
	_, _ = m.Do(ctx, Request{Site: id})
	j := m.Snapshot().Journal
	if last := j[len(j)-1]; last.Operation != OpCreateItem || last.Error == "" {
		t.Errorf("journal entry of a refused create = %+v", last)
	}
}

func TestMockNumberOf(t *testing.T) {
	for _, v := range []any{json.Number("1.5"), float64(1.5), int(1), int64(1)} {
		if _, ok := numberOf(v); !ok {
			t.Errorf("numberOf(%T) not read as a number", v)
		}
	}
	for _, v := range []any{json.Number("x"), "1", true, nil} {
		if _, ok := numberOf(v); ok {
			t.Errorf("numberOf(%#v) read as a number", v)
		}
	}
}
