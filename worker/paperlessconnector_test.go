package worker

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

func TestPaperlessWorkerDoesNotWriteOCRToVariables(t *testing.T) {
	const sensitive = "FULL_OCR_PRIVATE_DATA"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Token example-secret" {
			t.Error("worker did not use its server-held token")
		}
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/documents/":
			fmt.Fprint(w, "{\"count\":1,\"results\":[{\"id\":31,\"title\":\"Invoice\",\"content\":\""+sensitive+"\",\"__search_hit__\":{\"highlights\":\""+sensitive+"\"}}]}")
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	env := map[string]string{
		"ATLAS_PAPERLESS_CONNECTORS":         "archive",
		"ATLAS_PAPERLESS_ARCHIVE_URL":        server.URL,
		"ATLAS_PAPERLESS_ARCHIVE_TOKEN":      "example-secret",
		"ATLAS_PAPERLESS_ARCHIVE_ALLOW_HTTP": "true",
	}
	config, err := BuiltinConnectors(func(k string) string { return env[k] }, "paperless")
	if err != nil {
		t.Fatal(err)
	}
	handler := config.Handlers[paperlessJobType]
	if handler == nil {
		t.Fatal("paperless.v1 handler missing")
	}
	result, err := handler.Run(context.Background(), Job{Variables: map[string]any{
		"paperless": map[string]any{"worker": "archive", "operation": "search-documents", "query": "invoice", "resultVariable": "found"},
	}})
	if err != nil {
		t.Fatal(err)
	}
	bytes, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(bytes), sensitive) || strings.Contains(string(bytes), "highlights") {
		t.Fatalf("OCR text escaped to completion variables: %s", bytes)
	}
	if _, ok := result["found"]; !ok {
		t.Fatalf("expected mapped search result, got %s", bytes)
	}
}

func TestPaperlessWorkerReusesCorrespondentAfterRetry(t *testing.T) {
	var count atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Method == http.MethodGet {
			if count.Load() == 0 {
				fmt.Fprint(w, "{\"next\":null,\"results\":[]}")
			} else {
				fmt.Fprint(w, "{\"next\":null,\"results\":[{\"id\":18,\"name\":\"Muster AG\"}]}")
			}
			return
		}
		count.Add(1)
		fmt.Fprint(w, "{\"id\":18,\"name\":\"Muster AG\"}")
	}))
	defer server.Close()
	env := map[string]string{
		"ATLAS_PAPERLESS_CONNECTORS":         "archive",
		"ATLAS_PAPERLESS_ARCHIVE_URL":        server.URL,
		"ATLAS_PAPERLESS_ARCHIVE_TOKEN":      "example-secret",
		"ATLAS_PAPERLESS_ARCHIVE_ALLOW_HTTP": "true",
	}
	c, err := BuiltinConnectors(func(k string) string { return env[k] }, "paperless")
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		result, err := c.Handlers[paperlessJobType].Run(context.Background(), Job{Variables: map[string]any{
			"paperless": map[string]any{"worker": "archive", "operation": "add-correspondent", "name": "Muster AG", "resultVariable": "correspondent"},
		}})
		if err != nil {
			t.Fatal(err)
		}
		if result["correspondent"] == nil {
			t.Fatalf("no result in attempt %d", i)
		}
	}
	if count.Load() != 1 {
		t.Fatalf("retry caused %d creates", count.Load())
	}
}

func TestPaperlessWorkerGuardsInvalidJobs(t *testing.T) {
	clients, _, err := paperlessRegistryFromEnv(func(k string) string {
		switch k {
		case "ATLAS_PAPERLESS_CONNECTORS":
			return "test"
		case "ATLAS_PAPERLESS_TEST_URL":
			return "https://paperless.example.test"
		case "ATLAS_PAPERLESS_TEST_TOKEN":
			return "stored-token"
		}
		return ""
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, vars := range []map[string]any{
		nil,
		{"paperless": "no"},
		{"paperless": map[string]any{"worker": "other", "operation": "get-document", "documentId": 1}},
		{"paperless": map[string]any{"worker": "test", "operation": "get-document", "documentId": -1}},
		{"paperless": map[string]any{"worker": "test", "operation": "get-document", "documentId": 1, "resultVariable": "bad.dot"}},
	} {
		if _, err := RunPaperlessJob(context.Background(), Job{Variables: vars}, clients); err == nil {
			t.Fatalf("invalid job accepted: %#v", vars)
		}
	}
}
