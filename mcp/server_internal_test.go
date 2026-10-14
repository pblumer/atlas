package mcp

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

func TestArgUint(t *testing.T) {
	cases := []struct {
		name    string
		args    map[string]any
		want    uint64
		wantErr bool
	}{
		{"float64 valid", map[string]any{"key": float64(7)}, 7, false},
		{"float64 negative", map[string]any{"key": float64(-1)}, 0, true},
		{"float64 fractional", map[string]any{"key": float64(1.5)}, 0, true},
		{"json.Number valid", map[string]any{"key": json.Number("42")}, 42, false},
		{"json.Number invalid", map[string]any{"key": json.Number("nope")}, 0, true},
		{"string valid", map[string]any{"key": "13"}, 13, false},
		{"string invalid", map[string]any{"key": "xyz"}, 0, true},
		{"wrong type", map[string]any{"key": true}, 0, true},
		{"missing", map[string]any{}, 0, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := argUint(tc.args, "key")
			if (err != nil) != tc.wantErr {
				t.Fatalf("argUint err = %v, wantErr = %v", err, tc.wantErr)
			}
			if err == nil && got != tc.want {
				t.Fatalf("argUint = %d, want %d", got, tc.want)
			}
		})
	}
}

func TestArgString(t *testing.T) {
	cases := []struct {
		name    string
		args    map[string]any
		want    string
		wantErr bool
	}{
		{"valid", map[string]any{"xml": "hi"}, "hi", false},
		{"missing", map[string]any{}, "", true},
		{"wrong type", map[string]any{"xml": 3}, "", true},
		{"empty", map[string]any{"xml": ""}, "", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := argString(tc.args, "xml")
			if (err != nil) != tc.wantErr {
				t.Fatalf("argString err = %v, wantErr = %v", err, tc.wantErr)
			}
			if err == nil && got != tc.want {
				t.Fatalf("argString = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestParseUint(t *testing.T) {
	if got, err := parseUint("100"); err != nil || got != 100 {
		t.Fatalf("parseUint(100) = (%d, %v), want (100, nil)", got, err)
	}
	if _, err := parseUint("nope"); err == nil {
		t.Fatal("parseUint(nope) should error")
	}
}

// TestClientDoUnreachable covers the do path where the HTTP request itself fails
// (server closed), which must be wrapped as a reach-atlas error.
func TestClientDoUnreachable(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {}))
	url := ts.URL
	ts.Close() // now nothing is listening

	c := NewClient(url)
	if _, err := c.get("/api/v1/info"); err == nil || !strings.Contains(err.Error(), "reach atlas server") {
		t.Fatalf("get on closed server err = %v, want a reach-atlas error", err)
	}
}

// TestClientErrorBodies covers extractError's fallback branch (non-JSON body)
// and the apiError message rendering, plus a well-formed JSON error body.
func TestClientErrorBodies(t *testing.T) {
	t.Run("plain text body falls back", func(t *testing.T) {
		ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte("  boom  "))
		}))
		defer ts.Close()
		_, err := NewClient(ts.URL).get("/x")
		if err == nil || !strings.Contains(err.Error(), "boom") {
			t.Fatalf("err = %v, want the trimmed plain-text body", err)
		}
	})

	t.Run("json error body", func(t *testing.T) {
		ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"error":"bad thing"}`))
		}))
		defer ts.Close()
		_, err := NewClient(ts.URL).get("/x")
		if err == nil || !strings.Contains(err.Error(), "bad thing") {
			t.Fatalf("err = %v, want the JSON error message", err)
		}
	})

	t.Run("empty body uses status only", func(t *testing.T) {
		ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusServiceUnavailable)
		}))
		defer ts.Close()
		_, err := NewClient(ts.URL).get("/x")
		if err == nil || !strings.Contains(err.Error(), "status 503") {
			t.Fatalf("err = %v, want a status-only message", err)
		}
	})
}

// TestExtractError exercises the helper directly for both shapes.
func TestExtractError(t *testing.T) {
	if got := extractError([]byte(`{"error":"x"}`)); got != "x" {
		t.Fatalf("extractError json = %q, want x", got)
	}
	if got := extractError([]byte("  raw  ")); got != "raw" {
		t.Fatalf("extractError fallback = %q, want raw", got)
	}
	if got := extractError([]byte(`{"error":""}`)); got != `{"error":""}` {
		t.Fatalf("extractError empty-error = %q, want the raw body", got)
	}
}

// TestExtractErrorCarriesFindings covers the shared refusal path both validating areas
// answer through. Before this, a 400 whose body carried every reason a record was
// refused reached the agent as its one-line summary alone: "the capability is not
// valid", with the reasons dropped on the floor. A person has a form to read them out
// of; an agent has the message and nothing else.
func TestExtractErrorCarriesFindings(t *testing.T) {
	tests := []struct {
		name string
		body string
		want []string
		not  []string
	}{
		{
			name: "a list of strings",
			body: `{"error":"the capability is not valid","findings":["key is required","state \"x\" is not one of: proposed, active, deprecated"]}`,
			want: []string{"the capability is not valid", "key is required", "not one of: proposed"},
		},
		{
			name: "a list of objects carrying a message",
			body: `{"error":"the model is not valid","findings":[{"code":"c","reason":"r","message":"Order has no business key"}]}`,
			want: []string{"the model is not valid", "Order has no business key"},
			not:  []string{`"code"`},
		},
		{
			name: "an object with only a reason",
			body: `{"error":"nope","findings":[{"reason":"duplicate-class"}]}`,
			want: []string{"nope", "duplicate-class"},
		},
		{
			name: "no findings at all",
			body: `{"error":"no such capability"}`,
			want: []string{"no such capability"},
			not:  []string{":"},
		},
		{
			name: "an empty findings list adds nothing",
			body: `{"error":"nope","findings":[]}`,
			want: []string{"nope"},
			not:  []string{":"},
		},
		{
			name: "a finding of an unexpected shape is skipped, not fatal",
			body: `{"error":"nope","findings":[42,"a real one",{"unrelated":true},"  "]}`,
			want: []string{"nope", "a real one"},
			not:  []string{"42"},
		},
		{
			name: "a body that is not the error envelope comes back whole",
			body: `plain text failure`,
			want: []string{"plain text failure"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := extractError([]byte(tt.body))
			for _, want := range tt.want {
				if !strings.Contains(got, want) {
					t.Errorf("extractError(%s) = %q, want it to carry %q", tt.body, got, want)
				}
			}
			for _, not := range tt.not {
				if strings.Contains(got, not) {
					t.Errorf("extractError(%s) = %q, want it not to carry %q", tt.body, got, not)
				}
			}
		})
	}
}

// TestReadmeToolCountIsCurrent pins the number the README advertises to the number
// this build actually serves.
//
// It exists because the two had already parted company by thirty-two tools before
// anybody noticed: the README said 65, the adapter served 97, and nothing in the tree
// connected the sentence to the slice. A count in prose is exactly the kind of fact
// that is correct on the day it is written and wrong from the next merge onwards, so
// it is checked rather than remembered.
func TestReadmeToolCountIsCurrent(t *testing.T) {
	readme, err := os.ReadFile(filepath.Join("..", "README.md"))
	if err != nil {
		t.Fatalf("read README.md: %v", err)
	}
	want := len(defaultTools())
	pattern := regexp.MustCompile(`(\d+)\s+(?:Model Context Protocol tools|tools covering)`)
	matches := pattern.FindAllStringSubmatch(string(readme), -1)
	if len(matches) == 0 {
		t.Fatal("README.md no longer states how many MCP tools Atlas serves; either restore the " +
			"sentence or delete this test deliberately")
	}
	for _, m := range matches {
		got, err := strconv.Atoi(m[1])
		if err != nil {
			t.Fatalf("parsing %q: %v", m[0], err)
		}
		if got != want {
			t.Errorf("README.md says %q, but this build serves %d tools", m[0], want)
		}
	}
}
