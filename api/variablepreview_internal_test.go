package api

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func TestJSONShape(t *testing.T) {
	for _, tc := range []struct {
		in    string
		shape string
		count int
	}{
		{`[]`, "array", 0},
		{` [ ] `, "array", 0},
		{`{}`, "object", 0},
		{`[1]`, "array", 1},
		{`[1,2,3]`, "array", 3},
		{`[{"a":1,"b":[1,2]},{"c":"x,y"}]`, "array", 2},
		{`{"a":1,"b":{"c":2,"d":3}}`, "object", 2},
		// Commas and brackets inside strings, and escaped quotes, are not structure.
		{`["a,b", "c\"],[d", "\\"]`, "array", 3},
		{`{"k":"}"}`, "object", 1},
		{`"text"`, "", 0},
		{`42`, "", 0},
		{``, "", 0},
	} {
		shape, count := jsonShape(tc.in)
		if shape != tc.shape || count != tc.count {
			t.Errorf("jsonShape(%q) = %q %d, want %q %d", tc.in, shape, count, tc.shape, tc.count)
		}
	}
}

func TestPreviewVariableView(t *testing.T) {
	short := variableView{Name: "s", Value: "abc", Kind: "string"}
	if got := previewVariableView(short); got != short {
		t.Fatalf("short value changed: %+v", got)
	}

	// A value of two-byte characters whose limit falls inside one is cut before it.
	long := strings.Repeat("ä", variablePreviewBytes) // 2 bytes each
	got := previewVariableView(variableView{Name: "l", Value: "x" + long, Kind: "string"})
	if !got.Truncated || got.Size != 1+len(long) || got.Lines != 1 {
		t.Fatalf("long = %+v", got)
	}
	if !utf8.ValidString(got.Value) || len(got.Value) > variablePreviewBytes {
		t.Fatalf("preview is %d bytes, valid=%v", len(got.Value), utf8.ValidString(got.Value))
	}

	// A JSON value says its shape; a text its line count.
	arr := "[" + strings.Repeat(`"abcdefghij",`, 100) + `"z"]`
	got = previewVariableView(variableView{Name: "a", Value: arr, Kind: "json"})
	if got.Shape != "array" || got.Count != 101 || got.Lines != 0 {
		t.Fatalf("json preview = shape %q count %d lines %d", got.Shape, got.Count, got.Lines)
	}

	// An archived row is the exported log's answer and cannot be read again here, so
	// it keeps its values whole.
	rows := []instanceResp{
		{Variables: []variableView{{Name: "a", Value: arr, Kind: "json"}}},
		{Archived: true, Variables: []variableView{{Name: "a", Value: arr, Kind: "json"}}},
	}
	previewInstanceRows(rows)
	if !rows[0].Variables[0].Truncated || rows[1].Variables[0].Truncated {
		t.Fatalf("live truncated=%v archived truncated=%v, want true and false",
			rows[0].Variables[0].Truncated, rows[1].Variables[0].Truncated)
	}

	for _, tc := range []struct {
		in      string
		preview bool
		ok      bool
	}{
		{"", false, true}, {"full", false, true}, {"preview", true, true}, {" preview ", true, true}, {"short", false, false},
	} {
		preview, err := parseValuesMode(tc.in)
		if preview != tc.preview || (err == nil) != tc.ok {
			t.Errorf("parseValuesMode(%q) = %v, %v", tc.in, preview, err)
		}
	}
}
