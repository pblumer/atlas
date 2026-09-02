package api

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/pblumer/atlas/compiler"
	"github.com/pblumer/atlas/jobtype"
)

// jobTypesFor builds the engine-wide job-type table a real server always has. The
// catalog resolves atlas.jobType against it, so a fixture without one is not a
// server with no job types — it is a server that cannot start.
func jobTypesFor(t *testing.T, dir string) *jobtype.Registry {
	t.Helper()
	registry, err := jobtype.NewRegistry(filepath.Join(dir, "jobtypes"))
	if err != nil {
		t.Fatalf("jobtype.NewRegistry: %v", err)
	}
	return registry
}

// A store the catalog cannot read is a plain error, never a partial catalog. This
// is the one place where a half-answer would be actively harmful: resolution reads
// an absent entry as "nothing here has that id", so a catalog missing its releases
// because the store was unreadable would report every release binding as missing —
// a model that looks broken because a disk did.
func TestCollectBindingCatalogReportsStoreFailures(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/", nil)

	// A catalog needs more stores than storesFor builds, so each case starts from a
	// server with all of them and breaks exactly one.
	whole := func(t *testing.T) *Server {
		t.Helper()
		dir := t.TempDir()
		s := storesFor(t)
		releases, err := newReleaseStore(filepath.Join(dir, "releases"))
		if err != nil {
			t.Fatalf("newReleaseStore: %v", err)
		}
		connectors, err := newConnectorStore(filepath.Join(dir, "connectors"))
		if err != nil {
			t.Fatalf("newConnectorStore: %v", err)
		}
		targets, err := newTargetStore(filepath.Join(dir, "targets"))
		if err != nil {
			t.Fatalf("newTargetStore: %v", err)
		}
		// The catalog resolves atlas.runtimeId against this node's own descriptor
		// (ADR-0189 §6), so the settings store is one of the stores it needs.
		settings, err := newSettingsStore(filepath.Join(dir, "settings"))
		if err != nil {
			t.Fatalf("newSettingsStore: %v", err)
		}
		s.releases, s.connectors, s.targets, s.settings = releases, connectors, targets, settings
		s.versions = map[string]int32{}
		s.jobTypes = jobTypesFor(t, dir)
		return s
	}

	// A project is needed for the release lookup to be reached at all.
	seeded := func(t *testing.T, s *Server) {
		t.Helper()
		if err := s.projects.Save(project{ID: "p1", Name: "Billing"}); err != nil {
			t.Fatalf("save project: %v", err)
		}
	}

	tests := map[string]func(t *testing.T, s *Server){
		"projects": func(t *testing.T, s *Server) {
			s.projects = brokenStore(newProjectStore(filepath.Join(t.TempDir(), "gone")))
		},
		"releases": func(t *testing.T, s *Server) {
			seeded(t, s)
			s.releases = brokenStore(newReleaseStore(filepath.Join(t.TempDir(), "gone")))
		},
		"connectors": func(t *testing.T, s *Server) {
			s.connectors = brokenStore(newConnectorStore(filepath.Join(t.TempDir(), "gone")))
		},
		"targets": func(t *testing.T, s *Server) {
			s.targets = brokenStore(newTargetStore(filepath.Join(t.TempDir(), "gone")))
		},
	}
	for name, breakOne := range tests {
		t.Run(name, func(t *testing.T) {
			s := whole(t)
			breakOne(t, s)
			catalog, err := s.collectBindingCatalog(req)
			if err == nil {
				t.Fatalf("a broken %s store produced a catalog: %#v", name, catalog)
			}
		})
	}
}

// The catalog leaves out the two kinds nothing can supply, and that absence is the
// signal: a nil map is what the resolver reads as unsupported, while an empty one
// would claim the server looked and found none.
func TestCollectBindingCatalogOmitsKindsWithNoSource(t *testing.T) {
	dir := t.TempDir()
	s := storesFor(t)
	releases, err := newReleaseStore(filepath.Join(dir, "releases"))
	if err != nil {
		t.Fatalf("newReleaseStore: %v", err)
	}
	connectors, err := newConnectorStore(filepath.Join(dir, "connectors"))
	if err != nil {
		t.Fatalf("newConnectorStore: %v", err)
	}
	targets, err := newTargetStore(filepath.Join(dir, "targets"))
	if err != nil {
		t.Fatalf("newTargetStore: %v", err)
	}
	settings, err := newSettingsStore(filepath.Join(dir, "settings"))
	if err != nil {
		t.Fatalf("newSettingsStore: %v", err)
	}
	s.releases, s.connectors, s.targets, s.settings = releases, connectors, targets, settings
	s.versions = map[string]int32{}
	s.jobTypes = jobTypesFor(t, dir)
	identity, err := ensureNodeIdentity(settings)
	if err != nil {
		t.Fatalf("ensureNodeIdentity: %v", err)
	}

	catalog, err := s.collectBindingCatalog(httptest.NewRequest(http.MethodGet, "/", nil))
	if err != nil {
		t.Fatalf("collectBindingCatalog: %v", err)
	}
	// Job types are present and hold the engine's reserved half even on a server
	// where nothing has ever been deployed: those indices are compile-time constants
	// every build reserves, so "this server knows io.atlas.dmn" is true before
	// anything happens on it.
	if len(catalog.JobTypes) == 0 {
		t.Error("JobTypes is empty; the engine's reserved job types are known before any deployment")
	}
	if got, ok := catalog.JobTypes[compiler.DMNJobType]; !ok || got.Name != "Built-in job type" {
		t.Errorf("JobTypes[%q] = %#v, want a resolvable built-in", compiler.DMNJobType, got)
	}
	// Runtimes became answerable with the node descriptor (ADR-0189 §6): this server
	// knows one runtime for certain — itself. The map being present is what turns a
	// binding to some other node from "unsupported" into "missing", which is the
	// true answer here and a different one.
	if len(catalog.Runtimes) != 1 || catalog.Runtimes[identity.ID].ID != identity.ID {
		t.Errorf("Runtimes = %#v, want exactly this node (%s)", catalog.Runtimes, identity.ID)
	}
	for name, supplied := range map[string]bool{
		"Applications": catalog.Applications != nil,
		"Processes":    catalog.Processes != nil,
		"Connectors":   catalog.Connectors != nil,
		"Targets":      catalog.Targets != nil,
		"Releases":     catalog.Releases != nil,
		"Runtimes":     catalog.Runtimes != nil,
	} {
		if !supplied {
			t.Errorf("%s is absent; the server can supply it, so an empty map is the honest answer", name)
		}
	}
}
