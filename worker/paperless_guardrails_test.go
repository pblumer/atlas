package worker

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestPaperlessWorkerRequiresUsableCredentials(t *testing.T) {
	clients, names, err := paperlessRegistryFromEnv(func(string) string { return "" })
	if err != nil || clients != nil || names != nil {
		t.Fatalf("unconfigured must not be a misconfiguration: %v %v %v", clients, names, err)
	}

	for _, tt := range []struct {
		name string
		env  map[string]string
	}{
		{"missing-token", map[string]string{
			"ATLAS_PAPERLESS_CONNECTORS":  "archive",
			"ATLAS_PAPERLESS_ARCHIVE_URL": "https://paperless.example.test",
		}},
		{"http-not-allowed", map[string]string{
			"ATLAS_PAPERLESS_CONNECTORS":    "archive",
			"ATLAS_PAPERLESS_ARCHIVE_URL":   "http://paperless.example.test",
			"ATLAS_PAPERLESS_ARCHIVE_TOKEN": "sensitive",
		}},
		{"duplicate-name", map[string]string{
			"ATLAS_PAPERLESS_CONNECTORS":    "archive,archive",
			"ATLAS_PAPERLESS_ARCHIVE_URL":   "https://paperless.example.test",
			"ATLAS_PAPERLESS_ARCHIVE_TOKEN": "sensitive",
		}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			_, _, err := paperlessRegistryFromEnv(func(k string) string { return tt.env[k] })
			if err == nil || strings.Contains(err.Error(), "sensitive") {
				t.Fatalf("invalid worker credential was accepted or leaked: %v", err)
			}
		})
	}
}

func TestPaperlessWorkerDiscardsProviderResultWhenNotMapped(t *testing.T) {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, "{\"id\":4,\"title\":\"Invoice\",\"content\":\"PRIVATE OCR\"}")
	}))
	defer s.Close()

	env := map[string]string{
		"ATLAS_PAPERLESS_CONNECTORS":         "archive",
		"ATLAS_PAPERLESS_ARCHIVE_URL":        s.URL,
		"ATLAS_PAPERLESS_ARCHIVE_TOKEN":      "test-token",
		"ATLAS_PAPERLESS_ARCHIVE_ALLOW_HTTP": "true",
	}
	built, err := BuiltinConnectors(func(k string) string { return env[k] }, "paperless")
	if err != nil {
		t.Fatal(err)
	}
	result, err := built.Handlers[paperlessJobType].Run(context.Background(), Job{Variables: map[string]any{
		"paperless": map[string]any{
			"worker":     "archive",
			"operation":  "get-document",
			"documentId": 4,
		},
	}})
	if err != nil {
		t.Fatal(err)
	}
	if result != nil {
		t.Fatalf("a process with no result mapping must retain no metadata: %#v", result)
	}
}

func TestPaperlessWorkerRefusesOversizedRequestBeforeProviderCall(t *testing.T) {
	clients, _, err := paperlessRegistryFromEnv(func(k string) string {
		switch k {
		case "ATLAS_PAPERLESS_CONNECTORS":
			return "archive"
		case "ATLAS_PAPERLESS_ARCHIVE_URL":
			return "https://paperless.example.test"
		case "ATLAS_PAPERLESS_ARCHIVE_TOKEN":
			return "stored-secret"
		}
		return ""
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = RunPaperlessJob(context.Background(), Job{Variables: map[string]any{
		"paperless": map[string]any{
			"worker":         "archive",
			"operation":      "search-documents",
			"query":          strings.Repeat("too much untrusted input", 1024),
			"resultVariable": "documents",
		},
	}}, clients)
	if err == nil || strings.Contains(err.Error(), "stored-secret") {
		t.Fatalf("input budget bypassed or token leaked: %v", err)
	}
}
