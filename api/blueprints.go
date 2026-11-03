package api

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"strings"

	"github.com/pblumer/atlas/api/blueprintregistry"
	"github.com/pblumer/atlas/api/httpapi"
	"github.com/pblumer/atlas/api/importplan"
	"github.com/pblumer/atlas/logging"
)

// blueprintSource is atlas hub's current signed application-Blueprint transport.
// It is read-only design-time distribution. The source never
// receives a caller-controlled URL or signing key and never touches the run loop.
type blueprintSource interface {
	LoadIndex(context.Context) (blueprintregistry.Index, error)
	Fetch(context.Context, string, string) (blueprintregistry.Release, []byte, error)
}

// configuredBlueprintSource accepts a single static operator-owned HTTPS
// registry. An incomplete or unsafe config fails startup closed; with neither
// variable set the optional atlas hub Blueprint source is disabled.
//
// ATLAS_BLUEPRINT_REGISTRY_KEY holds one base64 Ed25519 public key or, while
// the registry rotates its signing key, two separated by a comma: the index is
// accepted when either signed it (ADR-0499 amendment 2026-11-02). The pinned
// fingerprints are logged at startup, and two are logged as a warning, so a
// rotation that was never finished does not go unnoticed.
func configuredBlueprintSource() (blueprintSource, error) {
	sourceURL := strings.TrimSpace(os.Getenv("ATLAS_BLUEPRINT_REGISTRY_URL"))
	keyList := strings.TrimSpace(os.Getenv("ATLAS_BLUEPRINT_REGISTRY_KEY"))
	if sourceURL == "" && keyList == "" {
		return nil, nil
	}
	if sourceURL == "" || keyList == "" {
		return nil, errors.New("Blueprint registry requires both ATLAS_BLUEPRINT_REGISTRY_URL and ATLAS_BLUEPRINT_REGISTRY_KEY")
	}
	keys, err := parsePinnedRegistryKeys(keyList)
	if err != nil {
		return nil, err
	}
	client, err := blueprintregistry.NewPublicClient(sourceURL, keys...)
	if err != nil {
		return nil, fmt.Errorf("Blueprint registry configuration: %w", err)
	}
	fingerprints := strings.Join(client.Fingerprints(), ",")
	if len(keys) > 1 {
		logging.Warn(logging.HubRegistryKeysPinned,
			"two atlas hub registry keys are pinned for a key rotation; remove the old one from ATLAS_BLUEPRINT_REGISTRY_KEY once the registry signs with the new one",
			slog.String("fingerprints", fingerprints))
	} else {
		logging.Info(logging.HubRegistryKeysPinned, "atlas hub registry key pinned", slog.String("fingerprints", fingerprints))
	}
	return client, nil
}

// parsePinnedRegistryKeys reads ATLAS_BLUEPRINT_REGISTRY_KEY: one or two
// comma-separated base64 32-byte Ed25519 public keys, none twice.
func parsePinnedRegistryKeys(value string) ([]ed25519.PublicKey, error) {
	parts := strings.Split(value, ",")
	if len(parts) > blueprintregistry.MaxPinnedKeys {
		return nil, fmt.Errorf("Blueprint registry pins at most %d signing keys (the current one and, during a rotation, its successor)", blueprintregistry.MaxPinnedKeys)
	}
	keys := make([]ed25519.PublicKey, 0, len(parts))
	for _, part := range parts {
		decoded, err := base64.StdEncoding.Strict().DecodeString(strings.TrimSpace(part))
		if err != nil || len(decoded) != ed25519.PublicKeySize {
			return nil, errors.New("Blueprint registry signing key must be a base64-encoded 32-byte Ed25519 public key; two are separated by a comma")
		}
		for _, other := range keys {
			if string(other) == string(decoded) {
				return nil, errors.New("Blueprint registry signing key is pinned twice")
			}
		}
		keys = append(keys, ed25519.PublicKey(decoded))
	}
	return keys, nil
}

// WithBlueprintRegistry supplies a preconfigured, operator-trusted registry.
// Embedders must enforce the same out-of-band key pinning as the environment
// configuration; never inject arbitrary caller-requested registry URLs.
func WithBlueprintRegistry(source *blueprintregistry.PublicClient) Option {
	return func(s *Server) { s.blueprints = source }
}

func (s *Server) requireBlueprints(w http.ResponseWriter) bool {
	w.Header().Set("Cache-Control", "no-store")
	if s.blueprints != nil {
		return true
	}
	httpapi.Error(w, http.StatusServiceUnavailable, "Blueprint Marketplace is not configured by this server's operator")
	return false
}

func (s *Server) handleListBlueprints(w http.ResponseWriter, r *http.Request) {
	if !s.requireBlueprints(w) {
		return
	}
	index, err := s.blueprints.LoadIndex(r.Context())
	if err != nil {
		httpapi.Error(w, http.StatusBadGateway, "trusted Blueprint registry is unavailable or invalid")
		return
	}
	httpapi.JSON(w, http.StatusOK, index)
}

// selectedBlueprint checks selection against the signed index, rather than
// sending an arbitrary user-supplied location to the outbound HTTP client.
func (s *Server) selectedBlueprint(w http.ResponseWriter, r *http.Request) (blueprintregistry.Release, []byte, bool) {
	if !s.requireBlueprints(w) {
		return blueprintregistry.Release{}, nil, false
	}
	id, version := r.PathValue("id"), r.PathValue("version")
	if id == "" || version == "" {
		httpapi.Error(w, http.StatusBadRequest, "Blueprint identifier and version are required")
		return blueprintregistry.Release{}, nil, false
	}
	index, err := s.blueprints.LoadIndex(r.Context())
	if err != nil {
		httpapi.Error(w, http.StatusBadGateway, "trusted Blueprint registry is unavailable or invalid")
		return blueprintregistry.Release{}, nil, false
	}
	found := false
	for _, rel := range index.Releases {
		if rel.BlueprintID == id && rel.Version == version {
			if !blueprintregistry.Compatible(rel.Atlas, Version) {
				httpapi.Error(w, http.StatusConflict, "Blueprint is incompatible with this atlas version")
				return blueprintregistry.Release{}, nil, false
			}
			found = true
			break
		}
	}
	if !found {
		httpapi.Error(w, http.StatusNotFound, "Blueprint release not in the trusted registry")
		return blueprintregistry.Release{}, nil, false
	}
	// Fetch independently re-verifies the current signed index; a changed or
	// revoked release is never silently replaced by an untrusted package.
	release, bytes, err := s.blueprints.Fetch(r.Context(), id, version)
	if err != nil {
		httpapi.Error(w, http.StatusBadGateway, "Blueprint package download or signature/digest verification failed")
		return blueprintregistry.Release{}, nil, false
	}
	if !blueprintregistry.Compatible(release.Atlas, Version) {
		httpapi.Error(w, http.StatusConflict, "Blueprint release compatibility changed; review again")
		return blueprintregistry.Release{}, nil, false
	}
	return release, bytes, true
}

func (s *Server) handleBlueprintPreview(w http.ResponseWriter, r *http.Request) {
	release, archive, ok := s.selectedBlueprint(w, r)
	if !ok {
		return
	}
	inspection, err := blueprintregistry.InspectPackage(release, archive)
	if err != nil {
		httpapi.Error(w, http.StatusBadGateway, "Blueprint package failed safe archive inspection")
		return
	}
	httpapi.JSON(w, http.StatusOK, struct {
		BlueprintID      string                               `json:"blueprintId"`
		Version          string                               `json:"version"`
		PublisherID      string                               `json:"publisherId"`
		License          string                               `json:"license"`
		Digest           string                               `json:"digest"`
		Needs            blueprintregistry.Needs              `json:"needs"`
		ApplicationKey   string                               `json:"applicationKey"`
		ApplicationName  string                               `json:"applicationName"`
		Files            []blueprintregistry.PackageFile      `json:"files"`
		Catalogues       []blueprintregistry.PackageCatalogue `json:"catalogues"`
		Products         []blueprintregistry.PackageProduct   `json:"products"`
		CataloguePublish bool                                 `json:"cataloguePublish"`
	}{
		BlueprintID: release.BlueprintID, Version: release.Version,
		PublisherID: release.PublisherID, License: release.License,
		Digest: release.Artifact.Digest, Needs: release.Needs,
		ApplicationKey:  inspection.ApplicationKey,
		ApplicationName: inspection.ApplicationName, Files: inspection.Files,
		Catalogues: inspection.Catalogues, Products: inspection.Products,
		CataloguePublish: inspection.CataloguePublish,
	})
}

func (s *Server) handleBlueprintPackage(w http.ResponseWriter, r *http.Request) {
	if !s.requireBlueprints(w) {
		return
	}
	if r.Header.Get("If-Match") == "" {
		httpapi.Error(w, http.StatusPreconditionRequired, "preview a Blueprint before requesting its package")
		return
	}
	release, compressed, ok := s.selectedBlueprint(w, r)
	if !ok {
		return
	}
	etag := `"` + release.Artifact.Digest + `"`
	if r.Header.Get("If-Match") != etag {
		httpapi.Error(w, http.StatusPreconditionFailed, "Blueprint digest changed since preview; review again")
		return
	}
	if _, err := blueprintregistry.InspectPackage(release, compressed); err != nil {
		httpapi.Error(w, http.StatusBadGateway, "Blueprint package failed safe archive inspection")
		return
	}
	w.Header().Set("Content-Type", "application/gzip")
	w.Header().Set("ETag", etag)
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(compressed)
}

// blueprintImportResult is a hub import's answer: the release it installed and the
// import result, field for field what POST /api/v1/applications/source answers.
type blueprintImportResult struct {
	BlueprintID string `json:"blueprintId"`
	Version     string `json:"version"`
	Digest      string `json:"digest"`
	// CataloguesNotImported names the catalogues the package offers that this route
	// does not install: their questions ask for this installation's groups and people,
	// which a person answers, in the Console's atlas hub dialog.
	CataloguesNotImported []string `json:"cataloguesNotImported"`
	sourceImportResult
}

// handleBlueprintImport installs the application of a verified atlas hub release
// through the same source import an uploaded package takes (importSourceFiles): the
// server fetches the release from its operator-pinned registry, checks it against
// the digest the caller previewed, inspects it and imports it. The plan, the
// decisions (?dryRun, ?overwrite, ?keep), the editor rule and the needs are the
// source import's own. It installs the application only; a package's catalogues
// are named, not imported.
//
// It exists so that a client that cannot move package bytes itself — an agent over
// MCP — installs exactly what the Console does, with nothing it could alter between
// the registry and the import.
func (s *Server) handleBlueprintImport(w http.ResponseWriter, r *http.Request) {
	if !s.requireBlueprints(w) {
		return
	}
	digest := r.URL.Query().Get("digest")
	if digest == "" {
		httpapi.Error(w, http.StatusPreconditionRequired, "preview the Blueprint release first and pass the digest it reported as ?digest=")
		return
	}
	decide, err := importplan.Parse(r.URL.Query())
	if err != nil {
		httpapi.Error(w, http.StatusBadRequest, err.Error())
		return
	}
	release, compressed, ok := s.selectedBlueprint(w, r)
	if !ok {
		return
	}
	if release.Artifact.Digest != digest {
		httpapi.Error(w, http.StatusPreconditionFailed, "Blueprint digest changed since preview; review again")
		return
	}
	if int64(len(compressed)) > s.budgets().AppBundle {
		httpapi.Error(w, http.StatusRequestEntityTooLarge, "Blueprint package exceeds this server's application bundle limit")
		return
	}
	inspection, err := blueprintregistry.InspectPackage(release, compressed)
	if err != nil {
		httpapi.Error(w, http.StatusBadGateway, "Blueprint package failed safe archive inspection")
		return
	}
	files, err := s.readSourceArchive(bytes.NewReader(compressed))
	if err != nil {
		httpapi.Error(w, http.StatusBadGateway, "Blueprint package is not a readable source tree: "+err.Error())
		return
	}
	catalogues := make([]string, 0, len(inspection.Catalogues))
	for _, c := range inspection.Catalogues {
		catalogues = append(catalogues, c.ID)
	}
	status, body := s.importSourceFiles(r, files, decide)
	switch v := body.(type) {
	case sourceImportResult:
		httpapi.JSON(w, status, blueprintImportResult{
			BlueprintID: release.BlueprintID, Version: release.Version, Digest: release.Artifact.Digest,
			CataloguesNotImported: catalogues, sourceImportResult: v,
		})
	case map[string]any:
		// The error envelope stays the envelope: "error" at the top, the release beside it.
		v["blueprintId"], v["version"], v["digest"] = release.BlueprintID, release.Version, release.Artifact.Digest
		httpapi.JSON(w, status, v)
	default:
		httpapi.JSON(w, status, body)
	}
}
