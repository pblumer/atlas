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

// The community repository (ADR-0081) distributes reusable building blocks —
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

// repositoryPackage is one shareable unit in the catalog: manifest fields plus
// the element-template payload the install writes into the local template store.
type repositoryPackage struct {
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
func (p repositoryPackage) carriesCode() bool { return p.Kind == packageKindScriptTask }

// repositoryManifest is the package without its template payload — what the
// gallery list returns. carriesCode is surfaced so the UI can render the trust
// distinction (a one-click "Safe" install vs a "Review" import).
type repositoryManifest struct {
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

func (p repositoryPackage) manifest() repositoryManifest {
	return repositoryManifest{
		ID: p.ID, Version: p.Version, Kind: p.Kind, Title: p.Title, Author: p.Author,
		Description: p.Description, EngineCompat: p.EngineCompat, Checksum: p.Checksum,
		CarriesCode: p.carriesCode(),
	}
}

// repositoryPackageView is the full package the detail endpoint returns: the
// manifest inlined, plus the template payload.
type repositoryPackageView struct {
	repositoryManifest
	Template json.RawMessage `json:"template"`
}

func (p repositoryPackage) view() repositoryPackageView {
	return repositoryPackageView{repositoryManifest: p.manifest(), Template: p.Template}
}

// repositoryChecksum is the integrity checksum over a template payload: sha256 of
// its canonical JSON (keys sorted, whitespace-insensitive), hex, "sha256:"-prefixed.
// It lets a client verify a package's template was not tampered with in transit —
// the provenance mechanism a remote registry will lean on. It is computed over a
// re-serialization so it does not depend on the byte-for-byte formatting of the source.
func repositoryChecksum(template json.RawMessage) (string, error) {
	var v any
	if err := json.Unmarshal(template, &v); err != nil {
		return "", fmt.Errorf("repository: template is not valid JSON: %w", err)
	}
	canon, err := json.Marshal(v) // maps marshal with sorted keys → deterministic
	if err != nil {
		return "", fmt.Errorf("repository: canonicalize template: %w", err)
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
func validatePackage(p repositoryPackage) error {
	if strings.TrimSpace(p.ID) == "" {
		return errors.New("repository: package id is required")
	}
	if strings.TrimSpace(p.Version) == "" {
		return errors.New("repository: package version is required")
	}
	if strings.TrimSpace(p.Title) == "" {
		return errors.New("repository: package title is required")
	}
	switch p.Kind {
	case packageKindConnector, packageKindServiceTask, packageKindScriptTask:
	default:
		return fmt.Errorf("repository: unknown package kind %q", p.Kind)
	}
	if len(p.Template) == 0 {
		return errors.New("repository: package template is empty")
	}
	var obj map[string]any
	if err := json.Unmarshal(p.Template, &obj); err != nil {
		return fmt.Errorf("repository: template is not a JSON object: %w", err)
	}
	if p.Checksum != "" {
		want, err := repositoryChecksum(p.Template)
		if err != nil {
			return err
		}
		if p.Checksum != want {
			return fmt.Errorf("repository: checksum mismatch for %s (have %s, want %s)", p.ID, p.Checksum, want)
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
				return fmt.Errorf("repository: template embeds a secret value under %q; share a credential reference, not the secret (ADR-0081)", k)
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

//go:embed repository_catalog/*.json
var repositoryCatalogFS embed.FS

// loadBundledCatalog parses the curated packages compiled into the binary, fills
// in each one's checksum, validates it, and returns them sorted by id. A malformed
// or invalid bundled package is a build-time programmer error, so it surfaces as a
// construction error (caught by TestBundledCatalogValid) rather than at request time.
func loadBundledCatalog() ([]repositoryPackage, error) {
	return loadCatalog(repositoryCatalogFS, "repository_catalog")
}

// loadCatalog parses every *.json package under dir in fsys, fills in checksums,
// validates, and returns them sorted by id. Split from loadBundledCatalog so its
// branches are testable against an in-memory fs.FS.
func loadCatalog(fsys fs.FS, dir string) ([]repositoryPackage, error) {
	entries, err := fs.ReadDir(fsys, dir)
	if err != nil {
		return nil, fmt.Errorf("repository: read bundled catalog: %w", err)
	}
	var out []repositoryPackage
	seen := map[string]bool{}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		data, err := fs.ReadFile(fsys, dir+"/"+e.Name())
		if err != nil {
			return nil, fmt.Errorf("repository: read %s: %w", e.Name(), err)
		}
		var pkg repositoryPackage
		if err := json.Unmarshal(data, &pkg); err != nil {
			return nil, fmt.Errorf("repository: decode %s: %w", e.Name(), err)
		}
		sum, err := repositoryChecksum(pkg.Template)
		if err != nil {
			return nil, fmt.Errorf("repository: %s: %w", e.Name(), err)
		}
		pkg.Checksum = sum
		if err := validatePackage(pkg); err != nil {
			return nil, fmt.Errorf("repository: %s: %w", e.Name(), err)
		}
		if seen[pkg.ID] {
			return nil, fmt.Errorf("repository: duplicate package id %q", pkg.ID)
		}
		seen[pkg.ID] = true
		out = append(out, pkg)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}

// findPackage returns the catalog package with the given id, or ok=false.
func (s *Server) findPackage(id string) (repositoryPackage, bool) {
	for _, p := range s.repository {
		if p.ID == id {
			return p, true
		}
	}
	return repositoryPackage{}, false
}

// handleListRepository lists the catalog, optionally filtered by ?kind= and a
// free-text ?q= over title/description/author. Templates are omitted from the list.
func (s *Server) handleListRepository(w http.ResponseWriter, r *http.Request) {
	kind := strings.TrimSpace(r.URL.Query().Get("kind"))
	q := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("q")))
	out := []repositoryManifest{}
	for _, p := range s.repository {
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

// handleGetRepositoryPackage returns one package with its template payload.
func (s *Server) handleGetRepositoryPackage(w http.ResponseWriter, r *http.Request) {
	p, ok := s.findPackage(r.PathValue("id"))
	if !ok {
		httpapi.Error(w, http.StatusNotFound, "no such repository package")
		return
	}
	httpapi.JSON(w, http.StatusOK, p.view())
}

// handleInstallRepositoryPackage installs a catalog package into this server's
// template store. A data-only package (connector or service task) installs
// directly; a script task carries code, so it is gated behind the admin role and
// the response flags that the Modeler should import it as a reviewable draft
// rather than enabling it silently (ADR-0081 trust split).
func (s *Server) handleInstallRepositoryPackage(w http.ResponseWriter, r *http.Request) {
	p, ok := s.findPackage(r.PathValue("id"))
	if !ok {
		httpapi.Error(w, http.StatusNotFound, "no such repository package")
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
	s.do(func() { saveErr = s.repositoryStore.Save(rec) })
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
	s.do(func() { recs, loadErr = s.repositoryStore.LoadAll() })
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
	s.do(func() { delErr = s.repositoryStore.Delete(r.PathValue("id")) })
	if delErr != nil {
		httpapi.Error(w, http.StatusInternalServerError, "uninstall template: "+delErr.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
