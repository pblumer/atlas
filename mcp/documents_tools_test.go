package mcp

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func documentTool(t *testing.T, name string) Tool {
	t.Helper()
	for _, tool := range runtimeTools() {
		if tool.Name == name {
			return tool
		}
	}
	t.Fatalf("no tool %s", name)
	return Tool{}
}

// The three document tools reach the document routes with what the agent gave them,
// and refuse an argument that is missing or malformed before any request
// (ADR-0543).
func TestDocumentToolsReachTheDocumentRoutes(t *testing.T) {
	var seen []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		seen = append(seen, r.Method+" "+r.URL.RequestURI()+" "+r.Header.Get("Content-Type")+" "+string(body))
		switch r.URL.Path {
		case "/api/v1/documents/doc_a/meta":
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, `{"state":"bound"}`)
		case "/api/v1/documents/doc_a":
			w.Header().Set("Content-Type", "text/csv; charset=utf-8")
			_, _ = io.WriteString(w, "a,b\n1,2\n")
		case "/api/v1/documents":
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusCreated)
			_, _ = io.WriteString(w, `{"$document":"doc_n"}`)
		default:
			w.WriteHeader(http.StatusNotFound)
			_, _ = io.WriteString(w, `{"error":"no such document"}`)
		}
	}))
	t.Cleanup(srv.Close)
	c := NewClient(srv.URL)

	meta := documentTool(t, "atlas_document_meta")
	if got, err := meta.Handler(c, map[string]any{"id": "doc_a"}); err != nil || !strings.Contains(got, "bound") {
		t.Errorf("meta = %q, %v", got, err)
	}
	if _, err := meta.Handler(c, map[string]any{}); err == nil {
		t.Error("meta without an id answered")
	}

	read := documentTool(t, "atlas_read_document")
	if got, err := read.Handler(c, map[string]any{"id": "doc_a"}); err != nil || !strings.Contains(got, "a,b\n1,2") {
		t.Errorf("read = %q, %v", got, err)
	}
	if got, err := read.Handler(c, map[string]any{"id": "doc_a", "maxBytes": float64(2)}); err != nil || strings.Contains(got, "1,2") {
		t.Errorf("read of two bytes = %q, %v", got, err)
	}
	if _, err := read.Handler(c, map[string]any{"id": "doc_a", "maxBytes": "many"}); err == nil {
		t.Error("a maxBytes that is not a number was accepted")
	}
	if _, err := read.Handler(c, map[string]any{}); err == nil {
		t.Error("read without an id answered")
	}
	if _, err := read.Handler(c, map[string]any{"id": "doc_gone"}); err == nil || !strings.Contains(err.Error(), "no such document") {
		t.Errorf("read of an unknown document = %v", err)
	}

	up := documentTool(t, "atlas_upload_document")
	if got, err := up.Handler(c, map[string]any{"name": "m.csv", "content": "x,y\n"}); err != nil || !strings.Contains(got, "doc_n") {
		t.Errorf("upload = %q, %v", got, err)
	}
	if _, err := up.Handler(c, map[string]any{"name": "m.csv", "content": "x", "contentType": "text/csv"}); err != nil {
		t.Errorf("upload with a claimed type = %v", err)
	}
	if _, err := up.Handler(c, map[string]any{"content": "x"}); err == nil {
		t.Error("upload without a name answered")
	}
	if _, err := up.Handler(c, map[string]any{"name": "m.csv"}); err == nil {
		t.Error("upload without content answered")
	}

	want := []string{
		"GET /api/v1/documents/doc_a/meta",
		"POST /api/v1/documents?name=m.csv text/plain; charset=utf-8 x,y\n",
		"POST /api/v1/documents?name=m.csv text/csv x",
	}
	all := strings.Join(seen, "\n")
	for _, w := range want {
		if !strings.Contains(all, w) {
			t.Errorf("no request %q in\n%s", w, all)
		}
	}
}
