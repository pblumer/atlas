package api

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
)

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
		s.releases, s.connectors, s.targets = releases, connectors, targets
		s.versions = map[string]int32{}
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
	s.releases, s.connectors, s.targets = releases, connectors, targets
	s.versions = map[string]int32{}

	catalog, err := s.collectBindingCatalog(httptest.NewRequest(http.MethodGet, "/", nil))
	if err != nil {
		t.Fatalf("collectBindingCatalog: %v", err)
	}
	if catalog.JobTypes != nil {
		t.Error("JobTypes is present; a job type is authored in a model, not registered")
	}
	if catalog.Runtimes != nil {
		t.Error("Runtimes is present; the node descriptor arrives with P4")
	}
	for name, supplied := range map[string]bool{
		"Applications": catalog.Applications != nil,
		"Processes":    catalog.Processes != nil,
		"Connectors":   catalog.Connectors != nil,
		"Targets":      catalog.Targets != nil,
		"Releases":     catalog.Releases != nil,
	} {
		if !supplied {
			t.Errorf("%s is absent; the server can supply it, so an empty map is the honest answer", name)
		}
	}
}
