package api

import (
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"
	"os"
	"reflect"
	"strings"

	"github.com/pblumer/atlas/api/blueprintregistry"
	"github.com/pblumer/atlas/api/httpapi"
)

// blueprintSource is read-only design-time distribution. The source never
// receives a caller-controlled URL or signing key and never touches the run loop.
type blueprintSource interface {
	LoadIndex(context.Context) (blueprintregistry.Index, error)
	Fetch(context.Context, string, string) (blueprintregistry.Release, []byte, error)
	TrustIdentity() (origin, signerFingerprint string)
}

// configuredBlueprintSource accepts a single static operator-owned HTTPS
// registry. An incomplete or unsafe config fails startup closed; with neither
// variable set the optional Marketplace is disabled.
func configuredBlueprintSource() (blueprintSource, error) {
	sourceURL := strings.TrimSpace(os.Getenv("ATLAS_BLUEPRINT_REGISTRY_URL"))
	publicKey := strings.TrimSpace(os.Getenv("ATLAS_BLUEPRINT_REGISTRY_KEY"))
	if sourceURL == "" && publicKey == "" {
		return nil, nil
	}
	if sourceURL == "" || publicKey == "" {
		return nil, errors.New("Blueprint registry requires both ATLAS_BLUEPRINT_REGISTRY_URL and ATLAS_BLUEPRINT_REGISTRY_KEY")
	}
	decoded, err := base64.StdEncoding.Strict().DecodeString(publicKey)
	if err != nil || len(decoded) != ed25519.PublicKeySize {
		return nil, errors.New("Blueprint registry signing key must be a base64-encoded 32-byte Ed25519 public key")
	}
	client, err := blueprintregistry.NewPublicClient(sourceURL, ed25519.PublicKey(decoded))
	if err != nil {
		return nil, fmt.Errorf("Blueprint registry configuration: %w", err)
	}
	return client, nil
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
func (s *Server) selectedBlueprint(w http.ResponseWriter, r *http.Request) (blueprintregistry.Release, []byte, string, bool) {
	if !s.requireBlueprints(w) {
		return blueprintregistry.Release{}, nil, "", false
	}
	id, version := r.PathValue("id"), r.PathValue("version")
	if id == "" || version == "" {
		httpapi.Error(w, http.StatusBadRequest, "Blueprint identifier and version are required")
		return blueprintregistry.Release{}, nil, "", false
	}
	index, err := s.blueprints.LoadIndex(r.Context())
	if err != nil {
		httpapi.Error(w, http.StatusBadGateway, "trusted Blueprint registry is unavailable or invalid")
		return blueprintregistry.Release{}, nil, "", false
	}
	found := false
	var indexedRelease blueprintregistry.Release
	for _, rel := range index.Releases {
		if rel.BlueprintID == id && rel.Version == version {
			if !blueprintregistry.Compatible(rel.Atlas, Version) {
				httpapi.Error(w, http.StatusConflict, "Blueprint is incompatible with this atlas version")
				return blueprintregistry.Release{}, nil, "", false
			}
			found = true
			indexedRelease = rel
			break
		}
	}
	if !found {
		httpapi.Error(w, http.StatusNotFound, "Blueprint release not in the trusted registry")
		return blueprintregistry.Release{}, nil, "", false
	}
	// Fetch independently re-verifies the current signed index; a changed or
	// revoked release is never silently replaced by an untrusted package.
	release, bytes, err := s.blueprints.Fetch(r.Context(), id, version)
	if err != nil {
		httpapi.Error(w, http.StatusBadGateway, "Blueprint package download or signature/digest verification failed")
		return blueprintregistry.Release{}, nil, "", false
	}
	if !reflect.DeepEqual(indexedRelease, release) {
		httpapi.Error(w, http.StatusConflict, "Blueprint changed between signed index reads; review again")
		return blueprintregistry.Release{}, nil, "", false
	}
	if !blueprintregistry.Compatible(release.Atlas, Version) {
		httpapi.Error(w, http.StatusConflict, "Blueprint release compatibility changed; review again")
		return blueprintregistry.Release{}, nil, "", false
	}
	return release, bytes, index.RegistryID, true
}

func (s *Server) handleBlueprintPreview(w http.ResponseWriter, r *http.Request) {
	release, archive, _, ok := s.selectedBlueprint(w, r)
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
	release, compressed, _, ok := s.selectedBlueprint(w, r)
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
