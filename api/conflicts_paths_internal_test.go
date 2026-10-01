package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pblumer/atlas/api/catalog"
	"github.com/pblumer/atlas/limits"
	"github.com/pblumer/atlas/model"
)

// conflictsPathsGrant writes holds into the inventory the way every other writer
// does: as engine commands, folded into events by the processor.
func conflictsPathsGrant(t *testing.T, srv *Server, held ...model.EntitlementValue) {
	t.Helper()
	srv.do(func() {
		for _, v := range held {
			srv.proc.GrantEntitlement(v)
		}
	})
	if err := srv.drive(); err != nil {
		t.Fatalf("drive grants: %v", err)
	}
}

// conflictsPathsSeed files a catalogue and, when excludes is non-nil, one release
// of it declaring those incompatibilities.
func conflictsPathsSeed(t *testing.T, srv *Server, excludes map[string][]string) {
	t.Helper()
	var err error
	srv.do(func() {
		if err = srv.catalogStore.SaveCatalog(catalog.Catalog{ID: "cat-1", Rank: 1}); err != nil || excludes == nil {
			return
		}
		err = srv.catalogStore.SaveRelease(catalog.Release{ID: "rel-1", CatalogID: "cat-1", CreatedAt: 1, Excludes: excludes})
	})
	if err != nil {
		t.Fatalf("seed catalogue: %v", err)
	}
}

// conflictsPathsRead reads the conflict report.
func conflictsPathsRead(t *testing.T, srv *Server) conflictReport {
	t.Helper()
	code, body := serveInternal(t, srv, http.MethodGet, "/api/v1/conflicts", "", "")
	if code != http.StatusOK {
		t.Fatalf("conflicts: %d (%s)", code, body)
	}
	var rep conflictReport
	if err := json.Unmarshal(body, &rep); err != nil {
		t.Fatalf("decode: %v (%s)", err, body)
	}
	return rep
}

// TestConflictsSaysWhichEmptyItIs: a catalogue that has never been published
// declares nothing, and a published rule nobody breaks is a clean estate. The two
// look alike as an empty list, so each says which it is.
func TestConflictsSaysWhichEmptyItIs(t *testing.T) {
	t.Run("unpublished", func(t *testing.T) {
		srv := newServerForErrors(t)
		conflictsPathsSeed(t, srv, nil)
		if rep := conflictsPathsRead(t, srv); rep.Pairs != 0 || !strings.Contains(rep.Note, "declares an incompatibility") {
			t.Errorf("report = %+v, want no pairs and the note saying none is declared", rep)
		}
	})
	t.Run("declared but not held", func(t *testing.T) {
		srv := newServerForErrors(t)
		conflictsPathsSeed(t, srv, map[string][]string{"a": {"b"}, "b": {"a"}})
		conflictsPathsGrant(t, srv, model.EntitlementValue{Principal: "usr_1", ItemID: "a", Since: 1})
		rep := conflictsPathsRead(t, srv)
		if rep.Pairs != 1 || len(rep.Findings) != 0 || !strings.Contains(rep.Note, "healthy answer") ||
			!strings.Contains(rep.Note, "1 declared incompatible pair") {
			t.Errorf("report = %+v, want one pair, no finding, and the healthy note", rep)
		}
	})
}

// TestConflictsBoundsTheListAndKeepsTheOldestFirst: the counts are over everything
// found, the list is bounded by the budget, and what survives the cut is what has
// stood longest — ties broken by person and then by pair, so two runs over the same
// estate read identically.
func TestConflictsBoundsTheListAndKeepsTheOldestFirst(t *testing.T) {
	l := limits.Default()
	l.ConflictReport = 2
	srv := newServerWithOptions(t, WithLimits(l))
	conflictsPathsSeed(t, srv, map[string][]string{"a": {"b", "c"}, "b": {"a"}, "c": {"a"}})
	conflictsPathsGrant(t, srv,
		// usr_2's pair began at 100, and so did both of usr_1's: the later grant of
		// each pair is when it began, whichever half that is.
		model.EntitlementValue{Principal: "usr_2", ItemID: "a", Since: 100},
		model.EntitlementValue{Principal: "usr_2", ItemID: "b", Since: 100},
		model.EntitlementValue{Principal: "usr_1", ItemID: "a", Since: 100},
		model.EntitlementValue{Principal: "usr_1", ItemID: "b", Since: 50},
		model.EntitlementValue{Principal: "usr_1", ItemID: "c", Since: 50},
	)

	rep := conflictsPathsRead(t, srv)
	if rep.Counts.Findings != 3 || rep.Counts.People != 2 {
		t.Fatalf("counts = %+v, want 3 findings over 2 people", rep.Counts)
	}
	if rep.Omitted != 1 || len(rep.Findings) != 2 {
		t.Fatalf("omitted=%d listed=%d, want 1 left out of a list of 2", rep.Omitted, len(rep.Findings))
	}
	for _, f := range rep.Findings {
		if f.Principal != "usr_1" || f.A != "a" {
			t.Errorf("listed %+v, want usr_1's two pairs ahead of usr_2's at the same age", f)
		}
	}
}

// TestConflictsFailsWhenTheCatalogueCannotBeRead: the rules come from the
// catalogue, and a report built over rules nobody could read would say "nothing is
// forbidden" about an estate that may be full of forbidden pairs.
func TestConflictsFailsWhenTheCatalogueCannotBeRead(t *testing.T) {
	for _, broken := range []string{"catalogs", "releases"} {
		t.Run(broken, func(t *testing.T) {
			srv := newServerForErrors(t)
			conflictsPathsSeed(t, srv, nil)
			approvalsPathsDirAsFile(t, filepath.Join(srv.dataDir, "catalog", broken))
			code, body := serveInternal(t, srv, http.MethodGet, "/api/v1/conflicts", "", "")
			if code != http.StatusInternalServerError || !strings.Contains(string(body), "conflicts: ") {
				t.Errorf("unreadable %s: %d (%s), want 500", broken, code, body)
			}
		})
	}
}

// TestConflictsDuringShutdown: the rules were never read, and an empty report would
// read as a clean estate.
func TestConflictsDuringShutdown(t *testing.T) {
	srv, closeSrv := newOffLoopServer(t)
	closeSrv()
	rec := httptest.NewRecorder()
	srv.handleConflicts(rec, httptest.NewRequest(http.MethodGet, "/api/v1/conflicts", nil))
	if rec.Code != http.StatusServiceUnavailable || !strings.Contains(rec.Body.String(), "shutting down") {
		t.Errorf("during shutdown: %d (%s), want 503", rec.Code, rec.Body)
	}
}
