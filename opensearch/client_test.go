package opensearch_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/pblumer/atlas/opensearch"
)

func TestConfigEnabled(t *testing.T) {
	if (opensearch.Config{}).Enabled() {
		t.Errorf("empty config should be disabled")
	}
	if (opensearch.Config{URL: "   "}).Enabled() {
		t.Errorf("blank URL should be disabled")
	}
	if !(opensearch.Config{URL: "http://os:9200"}).Enabled() {
		t.Errorf("configured URL should be enabled")
	}
}

// TestHTTPClientBulkFormat drives the HTTP client against a stub server and
// asserts the wire format: a POST to /_bulk with x-ndjson body of alternating
// action/document lines, basic-auth applied.
func TestHTTPClientBulkFormat(t *testing.T) {
	var gotPath, gotCT, gotAuth string
	var gotBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotCT = r.Header.Get("Content-Type")
		gotAuth = r.Header.Get("Authorization")
		b, _ := io.ReadAll(r.Body)
		gotBody = string(b)
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"errors":false,"items":[]}`)
	}))
	defer srv.Close()

	c := opensearch.NewHTTPClient(opensearch.Config{URL: srv.URL, Username: "u", Password: "p"})
	items := []opensearch.Item{
		{ID: "1", Body: []byte(`{"position":1}`)},
		{ID: "2", Body: []byte(`{"position":2}`)},
	}
	if err := c.Bulk(context.Background(), "atlas-events", items); err != nil {
		t.Fatalf("Bulk: %v", err)
	}
	if gotPath != "/_bulk" {
		t.Errorf("path = %q, want /_bulk", gotPath)
	}
	if gotCT != "application/x-ndjson" {
		t.Errorf("content-type = %q, want application/x-ndjson", gotCT)
	}
	if gotAuth == "" || !strings.HasPrefix(gotAuth, "Basic ") {
		t.Errorf("authorization = %q, want Basic ...", gotAuth)
	}
	want := `{"index":{"_id":"1","_index":"atlas-events"}}` + "\n" +
		`{"position":1}` + "\n" +
		`{"index":{"_id":"2","_index":"atlas-events"}}` + "\n" +
		`{"position":2}` + "\n"
	if gotBody != want {
		t.Errorf("bulk body =\n%q\nwant\n%q", gotBody, want)
	}
}

func TestHTTPClientBulkEmptyIsNoop(t *testing.T) {
	c := opensearch.NewHTTPClient(opensearch.Config{URL: "http://unused"})
	if err := c.Bulk(context.Background(), "atlas-events", nil); err != nil {
		t.Errorf("empty Bulk should be a no-op, got %v", err)
	}
}

func TestHTTPClientBulkHTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "nope", http.StatusInternalServerError)
	}))
	defer srv.Close()
	c := opensearch.NewHTTPClient(opensearch.Config{URL: srv.URL})
	err := c.Bulk(context.Background(), "i", []opensearch.Item{{ID: "1", Body: []byte(`{}`)}})
	if err == nil || !strings.Contains(err.Error(), "HTTP 500") {
		t.Fatalf("want HTTP 500 error, got %v", err)
	}
}

// TestHTTPClientBulkUnreachable: a transport error (nothing listening) is
// returned so the caller retries.
func TestHTTPClientBulkUnreachable(t *testing.T) {
	c := opensearch.NewHTTPClient(opensearch.Config{URL: "http://127.0.0.1:1"})
	err := c.Bulk(context.Background(), "i", []opensearch.Item{{ID: "1", Body: []byte(`{}`)}})
	if err == nil {
		t.Fatalf("want a transport error, got nil")
	}
}

// TestHTTPClientItemErrorsUnspecified: errors:true with no per-item detail still
// yields an error, described as unspecified.
func TestHTTPClientItemErrorsUnspecified(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, `{"errors":true,"items":[{"index":{"status":429}}]}`)
	}))
	defer srv.Close()
	c := opensearch.NewHTTPClient(opensearch.Config{URL: srv.URL})
	err := c.Bulk(context.Background(), "i", []opensearch.Item{{ID: "1", Body: []byte(`{}`)}})
	if err == nil || !strings.Contains(err.Error(), "unspecified") {
		t.Fatalf("want unspecified item error, got %v", err)
	}
}

// TestHTTPClientBulkMalformedResponse: a 2xx with a body that is not valid JSON
// is an error, so a broken/misconfigured endpoint is not mistaken for success.
func TestHTTPClientBulkMalformedResponse(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, `not json`)
	}))
	defer srv.Close()
	c := opensearch.NewHTTPClient(opensearch.Config{URL: srv.URL})
	err := c.Bulk(context.Background(), "i", []opensearch.Item{{ID: "1", Body: []byte(`{}`)}})
	if err == nil || !strings.Contains(err.Error(), "decode bulk response") {
		t.Fatalf("want decode error, got %v", err)
	}
}

// TestHTTPClientBulkItemErrors: a 2xx response whose body reports item-level
// failures is still an error, so the caller retries the batch.
func TestHTTPClientBulkItemErrors(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := map[string]any{
			"errors": true,
			"items": []map[string]any{
				{"index": map[string]any{"status": 400, "error": map[string]any{"type": "mapper_parsing_exception", "reason": "bad field"}}},
			},
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()
	c := opensearch.NewHTTPClient(opensearch.Config{URL: srv.URL})
	err := c.Bulk(context.Background(), "i", []opensearch.Item{{ID: "1", Body: []byte(`{}`)}})
	if err == nil || !strings.Contains(err.Error(), "mapper_parsing_exception") {
		t.Fatalf("want item error surfaced, got %v", err)
	}
}
