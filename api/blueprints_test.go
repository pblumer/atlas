package api

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/pblumer/atlas/api/blueprintregistry"
)

type fakeBlueprintSource struct {
	index        blueprintregistry.Index
	release      blueprintregistry.Release
	packageBytes []byte
	loadError    error
	fetchError   error
	fetches      int
}

func (f *fakeBlueprintSource) LoadIndex(context.Context) (blueprintregistry.Index, error) {
	return f.index, f.loadError
}

func (f *fakeBlueprintSource) Fetch(_ context.Context, _, _ string) (blueprintregistry.Release, []byte, error) {
	f.fetches++
	return f.release, f.packageBytes, f.fetchError
}

func sampleBlueprintSource(t *testing.T) *fakeBlueprintSource {
	t.Helper()
	var output bytes.Buffer
	if err := writeSourceArchive(&output, []sourceFile{{
		Path: "atlas.json",
		Data: []byte(`{"formatVersion":1,"key":"purchase","name":"Purchase approvals","processes":[],"forms":[],"decisions":[]}`),
	}}); err != nil {
		t.Fatal(err)
	}
	payload := output.Bytes()
	digest := sha256.Sum256(payload)
	release := blueprintregistry.Release{
		BlueprintID: "org.example.purchase", PublisherID: "org.example",
		Version: "1.0.0", Title: "Purchase approvals",
		License: "Apache-2.0", Description: "Example",
		Atlas: blueprintregistry.Compatibility{MinVersion: "0.1.0"},
		Artifact: blueprintregistry.Artifact{Path: "packages/purchase.tar.gz", SizeBytes: int64(len(payload)),
			Digest: "sha256:" + hex.EncodeToString(digest[:])},
	}
	return &fakeBlueprintSource{
		index:   blueprintregistry.Index{SchemaVersion: 1, RegistryID: "org.example.catalog", Releases: []blueprintregistry.Release{release}},
		release: release, packageBytes: payload,
	}
}

func blueprintRequest(s *Server, method, path, id, version, match string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, nil)
	req.SetPathValue("id", id)
	req.SetPathValue("version", version)
	if match != "" {
		req.Header.Set("If-Match", match)
	}
	w := httptest.NewRecorder()
	switch {
	case strings.HasSuffix(path, "/preview"):
		s.handleBlueprintPreview(w, req)
	case strings.HasSuffix(path, "/package"):
		s.handleBlueprintPackage(w, req)
	default:
		s.handleListBlueprints(w, req)
	}
	return w
}

func TestBlueprintAPIDisabledWithoutOperatorRegistry(t *testing.T) {
	s := &Server{}
	for _, p := range []struct{ path, id, version string }{
		{"/api/v1/blueprints", "", ""},
		{"/api/v1/blueprints/org.example.purchase/releases/1.0.0/preview", "org.example.purchase", "1.0.0"},
		{"/api/v1/blueprints/org.example.purchase/releases/1.0.0/package", "org.example.purchase", "1.0.0"},
	} {
		got := blueprintRequest(s, "GET", p.path, p.id, p.version, `"sha256:x"`)
		if got.Code != http.StatusServiceUnavailable {
			t.Fatalf("%s returned %d without pinned operator registry", p.path, got.Code)
		}
	}
}

func TestBlueprintAPIListAndPreviewNoWrites(t *testing.T) {
	f := sampleBlueprintSource(t)
	s := &Server{blueprints: f}
	list := blueprintRequest(s, "GET", "/api/v1/blueprints", "", "", "")
	if list.Code != http.StatusOK || !strings.Contains(list.Body.String(), "org.example.purchase") {
		t.Fatalf("list %d: %s", list.Code, list.Body.String())
	}
	preview := blueprintRequest(s, "GET", "/api/v1/blueprints/org.example.purchase/releases/1.0.0/preview", "org.example.purchase", "1.0.0", "")
	if preview.Code != http.StatusOK || f.fetches != 1 {
		t.Fatalf("preview %d (%d fetches): %s", preview.Code, f.fetches, preview.Body.String())
	}
	var result struct {
		ApplicationKey  string                          `json:"applicationKey"`
		ApplicationName string                          `json:"applicationName"`
		Digest          string                          `json:"digest"`
		Files           []blueprintregistry.PackageFile `json:"files"`
	}
	if err := json.Unmarshal(preview.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if result.ApplicationKey != "purchase" || result.ApplicationName != "Purchase approvals" || len(result.Files) != 1 || result.Digest != f.release.Artifact.Digest {
		t.Fatalf("invalid preview: %+v", result)
	}
	if preview.Header().Get("Cache-Control") != "no-store" {
		t.Fatal("registry response must not be cached")
	}
}

func TestBlueprintAPIDownloadRequiresMatchingPreviewDigest(t *testing.T) {
	f := sampleBlueprintSource(t)
	s := &Server{blueprints: f}
	path := "/api/v1/blueprints/org.example.purchase/releases/1.0.0/package"
	noPreview := blueprintRequest(s, "GET", path, "org.example.purchase", "1.0.0", "")
	if noPreview.Code != http.StatusPreconditionRequired || f.fetches != 0 {
		t.Fatalf("download without preview: %d, fetches: %d", noPreview.Code, f.fetches)
	}
	wrong := blueprintRequest(s, "GET", path, "org.example.purchase", "1.0.0", `"sha256:wrong"`)
	if wrong.Code != http.StatusPreconditionFailed {
		t.Fatalf("digest mismatch: %d", wrong.Code)
	}
	right := blueprintRequest(s, "GET", path, "org.example.purchase", "1.0.0", `"`+f.release.Artifact.Digest+`"`)
	if right.Code != http.StatusOK || right.Header().Get("Content-Type") != "application/gzip" || !bytes.Equal(right.Body.Bytes(), f.packageBytes) {
		t.Fatalf("valid download: %d", right.Code)
	}
}

func TestBlueprintAPIRejectsTamperedPackageAndUnknownSelection(t *testing.T) {
	f := sampleBlueprintSource(t)
	f.packageBytes = append([]byte(nil), f.packageBytes...)
	f.packageBytes[len(f.packageBytes)-1] ^= 0x01
	s := &Server{blueprints: f}
	preview := blueprintRequest(s, "GET", "/api/v1/blueprints/org.example.purchase/releases/1.0.0/preview", "org.example.purchase", "1.0.0", "")
	if preview.Code != http.StatusBadGateway {
		t.Fatalf("tampered package preview accepted: %d", preview.Code)
	}
	f = sampleBlueprintSource(t)
	s.blueprints = f
	unknown := blueprintRequest(s, "GET", "/api/v1/blueprints/other/releases/1.0.0/preview", "other", "1.0.0", "")
	if unknown.Code != http.StatusNotFound || f.fetches != 0 {
		t.Fatalf("unknown blueprint triggered a fetch: %d %d", unknown.Code, f.fetches)
	}
	f.loadError = errors.New("bad signature")
	fail := blueprintRequest(s, "GET", "/api/v1/blueprints", "", "", "")
	if fail.Code != http.StatusBadGateway {
		t.Fatalf("invalid signed index exposed: %d", fail.Code)
	}
}

func TestBlueprintRegistryOperatorConfigFailsClosed(t *testing.T) {
	t.Setenv("ATLAS_BLUEPRINT_REGISTRY_URL", "https://registry.example.org/blueprints/index.json")
	t.Setenv("ATLAS_BLUEPRINT_REGISTRY_KEY", "")
	if _, err := configuredBlueprintSource(); err == nil {
		t.Fatal("operator URL without pinned key must be rejected")
	}
	t.Setenv("ATLAS_BLUEPRINT_REGISTRY_KEY", base64.StdEncoding.EncodeToString(make([]byte, 32)))
	if c, err := configuredBlueprintSource(); err != nil || c == nil {
		t.Fatalf("valid pinned operator config rejected: %v", err)
	}
	t.Setenv("ATLAS_BLUEPRINT_REGISTRY_URL", "http://169.254.169.254/index.json")
	if _, err := configuredBlueprintSource(); err == nil {
		t.Fatal("operator configuration accepted private HTTP metadata endpoint")
	}
}

func TestBlueprintAPIRefusesIncompatibleReleaseBeforeFetch(t *testing.T) {
	f := sampleBlueprintSource(t)
	f.release.Atlas.MinVersion = "999.0.0"
	f.index.Releases[0].Atlas.MinVersion = "999.0.0"
	s := &Server{blueprints: f}
	url := "/api/v1/blueprints/org.example.purchase/releases/1.0.0/preview"
	response := blueprintRequest(s, "GET", url, "org.example.purchase", "1.0.0", "")
	if response.Code != http.StatusConflict || f.fetches != 0 {
		t.Fatalf("incompatible release fetched: status=%d count=%d", response.Code, f.fetches)
	}
}
