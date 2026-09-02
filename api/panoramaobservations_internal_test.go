package api

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/pblumer/atlas/compiler"
	"github.com/pblumer/atlas/engine"
	"github.com/pblumer/atlas/job"
	"github.com/pblumer/atlas/state"
	"github.com/pblumer/atlas/wal"
)

// TestCollectLocalFactsReportsStoreFailures. Every source this projection reads on
// the run loop can fail, and each failure has to reach the caller as a failure.
//
// The alternative is what makes this worth a test of its own: a store that cannot
// be read yields no facts, and a document built from no facts reports every bound
// element as unobserved — an architecture where nothing is running. That is the
// single most damaging thing a live view can say incorrectly, and it would say it
// with a 200.
func TestCollectLocalFactsReportsStoreFailures(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/", nil)

	// A collector needs more stores than storesFor builds, so each case starts from
	// a server with all of them and breaks exactly one.
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
		settings, err := newSettingsStore(filepath.Join(dir, "settings"))
		if err != nil {
			t.Fatalf("newSettingsStore: %v", err)
		}
		if _, err := ensureNodeIdentity(settings); err != nil {
			t.Fatalf("ensureNodeIdentity: %v", err)
		}
		// A real engine store: the collector scans incidents and live instances, and
		// there is no honest way to stub a scan of state that has none.
		store, err := state.Open(filepath.Join(dir, "state"))
		if err != nil {
			t.Fatalf("state.Open: %v", err)
		}
		t.Cleanup(func() { _ = store.Close() })
		// And a processor over it: the runtime fact names the partition this server
		// drives, and a Server without one is not a Server.
		log, err := wal.Open(wal.Options{Dir: filepath.Join(dir, "wal")})
		if err != nil {
			t.Fatalf("wal.Open: %v", err)
		}
		t.Cleanup(func() { _ = log.Close() })
		s.proc = engine.New(1, log, store, nil)

		s.releases, s.connectors, s.targets, s.settings = releases, connectors, targets, settings
		s.store = store
		s.versions = map[string]int32{}
		s.workers = newWorkerRegistry(func() int64 { return 1 })
		// The engine-wide job-type table and the in-process runner, because the
		// projection now observes each kind of work this server knows — and, like the
		// processor above, a Server without them is not a Server.
		s.jobTypes = jobTypesFor(t, dir)
		s.jobRunner = job.NewRunner(store, s.proc)
		return s
	}

	// A project is needed for the release lookup to be reached at all.
	seeded := func(t *testing.T, s *Server) {
		t.Helper()
		if err := s.projects.Save(project{ID: "p1", Name: "Billing"}); err != nil {
			t.Fatalf("save project: %v", err)
		}
	}

	for name, breakOne := range map[string]func(t *testing.T, s *Server){
		"projects": func(t *testing.T, s *Server) {
			s.projects = brokenStore(newProjectStore(filepath.Join(t.TempDir(), "gone")))
		},
		"workers": func(t *testing.T, s *Server) {
			s.connectors = brokenStore(newConnectorStore(filepath.Join(t.TempDir(), "gone")))
		},
		"targets": func(t *testing.T, s *Server) {
			s.targets = brokenStore(newTargetStore(filepath.Join(t.TempDir(), "gone")))
		},
		"releases": func(t *testing.T, s *Server) {
			seeded(t, s)
			s.releases = brokenStore(newReleaseStore(filepath.Join(t.TempDir(), "gone")))
		},
		"node identity": func(t *testing.T, s *Server) {
			settings, err := newSettingsStore(filepath.Join(t.TempDir(), "settings"))
			if err != nil {
				t.Fatalf("newSettingsStore: %v", err)
			}
			// Present but unreadable, which nodeIdentity refuses rather than
			// treating as "this server has no identity".
			s.settings = settings
		},
	} {
		t.Run(name, func(t *testing.T) {
			s := whole(t)
			breakOne(t, s)
			if _, _, err := s.collectLocalFacts(req); err == nil {
				t.Fatal("a broken source produced a full set of facts")
			}
		})
	}

	// And the whole thing works when nothing is broken, so the cases above are
	// failing for the reason they claim rather than because the fixture is wrong.
	s := whole(t)
	seeded(t, s)
	facts, peers, err := s.collectLocalFacts(req)
	if err != nil {
		t.Fatalf("collectLocalFacts on a healthy server: %v", err)
	}
	// No targets are configured, so there is nobody for phase two to ask.
	if len(peers) != 0 {
		t.Errorf("peers = %+v, want none on a server with no deployment targets", peers)
	}
	// Job types are observed now, and the reserved half is there before anything is
	// deployed: those indices are compile-time constants every build reserves, so a
	// bare server already knows what io.atlas.dmn is and that it runs it itself.
	if len(facts.JobTypes) == 0 {
		t.Error("JobTypes is empty; the engine's reserved kinds are known before any deployment")
	}
	// The state is not asserted here: this fixture's runner registers no handlers, so
	// what it reports is a property of the fixture rather than of the mapping. The
	// mapping is pinned in TestJobTypeStatusAnswersOnlyWhatTheEngineCanSee, and on a
	// real server in TestJobTypeObservationSaysWhatItCanAndCannotSee.
	if _, known := facts.JobTypes[compiler.DMNJobType]; !known {
		t.Errorf("JobTypes does not carry %q; the reserved half is known to every build",
			compiler.DMNJobType)
	}
	if len(facts.Runtimes) != 1 {
		t.Errorf("Runtimes = %v, want exactly this node", facts.Runtimes)
	}
	for name, supplied := range map[string]bool{
		"Applications": facts.Applications != nil,
		"Processes":    facts.Processes != nil,
		"Connectors":   facts.Connectors != nil,
		"Targets":      facts.Targets != nil,
		"Releases":     facts.Releases != nil,
	} {
		if !supplied {
			t.Errorf("%s is absent; the server observes it, so an empty map is the honest answer", name)
		}
	}
}
