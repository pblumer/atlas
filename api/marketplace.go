package api

import (
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/pblumer/atlas/api/httpapi"
)

// The community marketplace (ADR-0081) distributes reusable building blocks —
// connectors, service tasks, and script tasks — as one shareable unit: an
// element-template payload (ADR-0027) wrapped in a manifest. This first slice
// ships a curated, bundled catalog (compiled into the binary, no network) that
// the Modeler can browse and install; a remote registry is a follow-up.
//
// The load-bearing rule is the trust split: a data-only artifact (a connector or
// service task) is safe to install directly, but a script task carries
// executable code (ADR-0047's largest attack surface), so installing one is gated
// behind the admin role and the Modeler imports it as a reviewable draft rather
// than auto-enabling it. And, as everywhere, no secret value ever travels in a
// shared artifact — only references (ADR-0041/0067/0069).

// Package kinds. A connector and a service task are pure data (property bindings
// the compiler already runs); a script task carries a script body.
const (
	packageKindConnector   = "connector"
	packageKindServiceTask = "service-task"
	packageKindScriptTask  = "script-task"
)

// marketplacePackage is one shareable unit in the catalog: manifest fields plus
// the element-template payload the install writes into the local template store.
type marketplacePackage struct {
	ID           string          `json:"id"`
	Version      string          `json:"version"`
	Kind         string          `json:"kind"`
	Title        string          `json:"title"`
	Author       string          `json:"author"`
	Description  string          `json:"description"`
	EngineCompat string          `json:"engineCompat"`
	Checksum     string          `json:"checksum,omitempty"`
	Template     json.RawMessage `json:"template"`
}

// carriesCode reports whether installing this package brings executable code into
// the workspace. Only script tasks do; they are the ones gated on install and
// imported as a draft to review (ADR-0081/0047).
func (p marketplacePackage) carriesCode() bool { return p.Kind == packageKindScriptTask }

// marketplaceManifest is the package without its template payload — what the
// gallery list returns. carriesCode is surfaced so the UI can render the trust
// distinction (a one-click "Safe" install vs a "Review" import).
type marketplaceManifest struct {
	ID           string `json:"id"`
	Version      string `json:"version"`
	Kind         string `json:"kind"`
	Title        string `json:"title"`
	Author       string `json:"author"`
	Description  string `json:"description"`
	EngineCompat string `json:"engineCompat"`
	Checksum     string `json:"checksum"`
	CarriesCode  bool   `json:"carriesCode"`
}

func (p marketplacePackage) manifest() marketplaceManifest {
	return marketplaceManifest{
		ID: p.ID, Version: p.Version, Kind: p.Kind, Title: p.Title, Author: p.Author,
		Description: p.Description, EngineCompat: p.EngineCompat, Checksum: p.Checksum,
		CarriesCode: p.carriesCode(),
	}
}

// marketplacePackageView is the full package the detail endpoint returns: the
// manifest inlined, plus the template payload.
type marketplacePackageView struct {
	marketplaceManifest
	Template json.RawMessage `json:"template"`
}

func (p marketplacePackage) view() marketplacePackageView {
	return marketplacePackageView{marketplaceManifest: p.manifest(), Template: p.Template}
}

// marketplaceChecksum is the integrity checksum over a template payload: sha256 of
// its canonical JSON (keys sorted, whitespace-insensitive), hex, "sha256:"-prefixed.
// It lets a client verify a package's template was not tampered with in transit —
// the provenance mechanism a remote registry will lean on. It is computed over a
// re-serialization so it does not depend on the byte-for-byte formatting of the source.
func marketplaceChecksum(template json.RawMessage) (string, error) {
	var v any
	if err := json.Unmarshal(template, &v); err != nil {
		return "", fmt.Errorf("marketplace: template is not valid JSON: %w", err)
	}
	canon, err := json.Marshal(v) // maps marshal with sorted keys → deterministic
	if err != nil {
		return "", fmt.Errorf("marketplace: canonicalize template: %w", err)
	}
	sum := sha256.Sum256(canon)
	return "sha256:" + hex.EncodeToString(sum[:]), nil
}

// secretKeyPattern matches object keys that would name a credential value. A
// *reference* key (one ending in "ref", e.g. credentialsRef) is allowed — that is
// exactly how a connector points at a server-held secret without embedding it.
var secretKeyPattern = regexp.MustCompile(`(?i)(password|passwd|secret|token|api[_-]?key|private[_-]?key|passphrase)`)

// validatePackage checks a package is well-formed and safe to serve or install:
// required fields present, a known kind, a template that parses as a JSON object,
// a matching checksum (when one is set), and no embedded secret value. It never
// executes anything the package carries (deploy-time validation only, ADR-0047).
func validatePackage(p marketplacePackage) error {
	if strings.TrimSpace(p.ID) == "" {
		return errors.New("marketplace: package id is required")
	}
	if strings.TrimSpace(p.Version) == "" {
		return errors.New("marketplace: package version is required")
	}
	if strings.TrimSpace(p.Title) == "" {
		return errors.New("marketplace: package title is required")
	}
	switch p.Kind {
	case packageKindConnector, packageKindServiceTask, packageKindScriptTask:
	default:
		return fmt.Errorf("marketplace: unknown package kind %q", p.Kind)
	}
	if len(p.Template) == 0 {
		return errors.New("marketplace: package template is empty")
	}
	var obj map[string]any
	if err := json.Unmarshal(p.Template, &obj); err != nil {
		return fmt.Errorf("marketplace: template is not a JSON object: %w", err)
	}
	if p.Checksum != "" {
		want, err := marketplaceChecksum(p.Template)
		if err != nil {
			return err
		}
		if p.Checksum != want {
			return fmt.Errorf("marketplace: checksum mismatch for %s (have %s, want %s)", p.ID, p.Checksum, want)
		}
	}
	if err := scanForSecrets(obj); err != nil {
		return err
	}
	return nil
}

// scanForSecrets walks a decoded template and rejects any object key that names a
// credential value while carrying a non-empty literal string. A reference (a key
// ending in "ref") and a FEEL/placeholder expression (starting with "=" or
// containing "${" / "{{") are allowed, so legitimate connector wiring passes while
// a hard-coded token does not. It is a best-effort guard, not a cryptographic one —
// signing is a named ADR-0081 follow-up.
func scanForSecrets(v any) error {
	switch t := v.(type) {
	case map[string]any:
		for k, child := range t {
			if s, ok := child.(string); ok && looksLikeSecretValue(k, s) {
				return fmt.Errorf("marketplace: template embeds a secret value under %q; share a credential reference, not the secret (ADR-0081)", k)
			}
			if err := scanForSecrets(child); err != nil {
				return err
			}
		}
	case []any:
		for _, child := range t {
			if err := scanForSecrets(child); err != nil {
				return err
			}
		}
	}
	return nil
}

func looksLikeSecretValue(key, value string) bool {
	lk := strings.ToLower(strings.TrimSpace(key))
	if !secretKeyPattern.MatchString(lk) || strings.HasSuffix(lk, "ref") {
		return false
	}
	v := strings.TrimSpace(value)
	if v == "" || strings.HasPrefix(v, "=") || strings.Contains(v, "${") || strings.Contains(v, "{{") {
		return false // empty, FEEL expression, or a placeholder — not a literal secret
	}
	return true
}

//go:embed marketplace_catalog/*.json
var marketplaceCatalogFS embed.FS

// loadBundledCatalog parses the curated packages compiled into the binary, fills
// in each one's checksum, validates it, and returns them sorted by id. A malformed
// or invalid bundled package is a build-time programmer error, so it surfaces as a
// construction error (caught by TestBundledCatalogValid) rather than at request time.
func loadBundledCatalog() ([]marketplacePackage, error) {
	return loadCatalog(marketplaceCatalogFS, "marketplace_catalog")
}

// loadCatalog parses every *.json package under dir in fsys, fills in checksums,
// validates, and returns them sorted by id. Split from loadBundledCatalog so its
// branches are testable against an in-memory fs.FS.
func loadCatalog(fsys fs.FS, dir string) ([]marketplacePackage, error) {
	entries, err := fs.ReadDir(fsys, dir)
	if err != nil {
		return nil, fmt.Errorf("marketplace: read bundled catalog: %w", err)
	}
	var out []marketplacePackage
	seen := map[string]bool{}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		data, err := fs.ReadFile(fsys, dir+"/"+e.Name())
		if err != nil {
			return nil, fmt.Errorf("marketplace: read %s: %w", e.Name(), err)
		}
		var pkg marketplacePackage
		if err := json.Unmarshal(data, &pkg); err != nil {
			return nil, fmt.Errorf("marketplace: decode %s: %w", e.Name(), err)
		}
		sum, err := marketplaceChecksum(pkg.Template)
		if err != nil {
			return nil, fmt.Errorf("marketplace: %s: %w", e.Name(), err)
		}
		pkg.Checksum = sum
		if err := validatePackage(pkg); err != nil {
			return nil, fmt.Errorf("marketplace: %s: %w", e.Name(), err)
		}
		if seen[pkg.ID] {
			return nil, fmt.Errorf("marketplace: duplicate package id %q", pkg.ID)
		}
		seen[pkg.ID] = true
		out = append(out, pkg)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}

// findPackage returns the catalog package with the given id, or ok=false.
func (s *Server) findPackage(id string) (marketplacePackage, bool) {
	for _, p := range s.marketplace {
		if p.ID == id {
			return p, true
		}
	}
	return marketplacePackage{}, false
}

// handleListMarketplace lists the catalog, optionally filtered by ?kind= and a
// free-text ?q= over title/description/author. Templates are omitted from the list.
func (s *Server) handleListMarketplace(w http.ResponseWriter, r *http.Request) {
	kind := strings.TrimSpace(r.URL.Query().Get("kind"))
	q := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("q")))
	out := []marketplaceManifest{}
	for _, p := range s.marketplace {
		if kind != "" && p.Kind != kind {
			continue
		}
		if q != "" && !strings.Contains(strings.ToLower(p.Title+" "+p.Description+" "+p.Author), q) {
			continue
		}
		out = append(out, p.manifest())
	}
	httpapi.JSON(w, http.StatusOK, out)
}

// handleGetMarketplacePackage returns one package with its template payload.
func (s *Server) handleGetMarketplacePackage(w http.ResponseWriter, r *http.Request) {
	p, ok := s.findPackage(r.PathValue("id"))
	if !ok {
		httpapi.Error(w, http.StatusNotFound, "no such marketplace package")
		return
	}
	httpapi.JSON(w, http.StatusOK, p.view())
}

// handleInstallMarketplacePackage installs a catalog package into this server's
// template store. A data-only package (connector or service task) installs
// directly; a script task carries code, so it is gated behind the admin role and
// the response flags that the Modeler should import it as a reviewable draft
// rather than enabling it silently (ADR-0081 trust split).
func (s *Server) handleInstallMarketplacePackage(w http.ResponseWriter, r *http.Request) {
	p, ok := s.findPackage(r.PathValue("id"))
	if !ok {
		httpapi.Error(w, http.StatusNotFound, "no such marketplace package")
		return
	}
	if p.carriesCode() && !s.requireAdmin(w, r) {
		return // requireAdmin wrote 403
	}
	if err := validatePackage(p); err != nil {
		httpapi.Error(w, http.StatusUnprocessableEntity, err.Error())
		return
	}
	rec := installedTemplate{
		ID: p.ID, PackageID: p.ID, Version: p.Version, Kind: p.Kind,
		Title: p.Title, Template: p.Template, InstalledAt: time.Now().Unix(),
	}
	var saveErr error
	s.do(func() { saveErr = s.marketplaceStore.Save(rec) })
	if saveErr != nil {
		httpapi.Error(w, http.StatusInternalServerError, "install package: "+saveErr.Error())
		return
	}
	httpapi.JSON(w, http.StatusOK, struct {
		installedTemplate
		ReviewRequired bool `json:"reviewRequired"`
	}{installedTemplate: rec, ReviewRequired: p.carriesCode()})
}

// handleListInstalled lists the templates installed on this server, oldest first.
func (s *Server) handleListInstalled(w http.ResponseWriter, _ *http.Request) {
	var (
		recs    []installedTemplate
		loadErr error
	)
	s.do(func() { recs, loadErr = s.marketplaceStore.LoadAll() })
	if loadErr != nil {
		httpapi.Error(w, http.StatusInternalServerError, "list installed templates: "+loadErr.Error())
		return
	}
	if recs == nil {
		recs = []installedTemplate{}
	}
	httpapi.JSON(w, http.StatusOK, recs)
}

// handleUninstall removes an installed template. Idempotent: uninstalling one that
// is not present still succeeds.
func (s *Server) handleUninstall(w http.ResponseWriter, r *http.Request) {
	var delErr error
	s.do(func() { delErr = s.marketplaceStore.Delete(r.PathValue("id")) })
	if delErr != nil {
		httpapi.Error(w, http.StatusInternalServerError, "uninstall template: "+delErr.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
