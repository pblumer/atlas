package mcp_test

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
)

// The hub import is a dry run unless the agent says otherwise, always carries the
// previewed digest, and passes the person's decisions through; the release is
// addressed by path, never by a URL the agent supplies.
func TestHubImportIsADryRunUnlessAskedAndCarriesTheDigest(t *testing.T) {
	var gotMethod, gotPath string
	var gotQuery url.Values
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod, gotPath, gotQuery = r.Method, r.URL.EscapedPath(), r.URL.Query()
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"blueprintId":"io.github.pblumer.atlas.einstieg","dryRun":true}`))
	}))
	defer backend.Close()

	args := map[string]any{"blueprintId": "io.github.pblumer.atlas.einstieg", "version": "1.0.0", "digest": "sha256:abc"}
	callToolAgainstBackend(t, backend, "atlas_hub_import_release", args)
	if gotMethod != http.MethodPost || gotPath != "/api/v1/blueprints/io.github.pblumer.atlas.einstieg/releases/1.0.0/import" {
		t.Fatalf("called %s %s", gotMethod, gotPath)
	}
	if gotQuery.Get("dryRun") != "true" || gotQuery.Get("digest") != "sha256:abc" {
		t.Fatalf("query = %v, want a dry run with the digest", gotQuery)
	}

	args["dryRun"] = false
	args["keep"] = []any{"process:proc_check_order"}
	args["adopt"] = []any{"process:proc_check_order", "form:f1"}
	callToolAgainstBackend(t, backend, "atlas_hub_import_release", args)
	if gotQuery.Has("dryRun") || gotQuery.Get("keep") != "process:proc_check_order" ||
		gotQuery.Get("adopt") != "process:proc_check_order,form:f1" {
		t.Fatalf("query = %v, want the import with the person's decision", gotQuery)
	}

	gotPath = ""
	delete(args, "digest")
	response := callToolAgainstBackend(t, backend, "atlas_hub_import_release", args)
	if gotPath != "" {
		t.Fatalf("an import without a digest reached the server: %s (%v)", gotPath, response)
	}
}

// The hub sketch is read for the previewed digest and the layers asked for, and a
// call without a digest never reaches the server.
func TestHubReleaseArchitectureCarriesTheDigestAndLayers(t *testing.T) {
	var gotMethod, gotPath string
	var gotQuery url.Values
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod, gotPath, gotQuery = r.Method, r.URL.EscapedPath(), r.URL.Query()
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"blueprintId":"io.github.pblumer.atlas.einstieg","architecture":{"packaged":true}}`))
	}))
	defer backend.Close()

	args := map[string]any{"blueprintId": "io.github.pblumer.atlas.einstieg", "version": "1.0.0", "digest": "sha256:abc", "layers": "application,data"}
	callToolAgainstBackend(t, backend, "atlas_hub_release_architecture", args)
	if gotMethod != http.MethodGet || gotPath != "/api/v1/blueprints/io.github.pblumer.atlas.einstieg/releases/1.0.0/architecture" {
		t.Fatalf("called %s %s", gotMethod, gotPath)
	}
	if gotQuery.Get("digest") != "sha256:abc" || gotQuery.Get("layers") != "application,data" {
		t.Fatalf("query = %v, want the digest and the layers", gotQuery)
	}

	gotPath = ""
	delete(args, "digest")
	callToolAgainstBackend(t, backend, "atlas_hub_release_architecture", args)
	if gotPath != "" {
		t.Fatalf("a sketch without a digest reached the server: %s", gotPath)
	}
}
