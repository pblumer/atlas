package api

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/pblumer/atlas/api/httpapi"
)

// The Starmap's structural cache (ADR-0211 §7).
//
// It exists because the landscape is derived on the run loop — the single writer
// (I3) — and reading it costs a directory listing and a JSON decode per record
// across four sidecar stores plus a walk of every compiled process. Twenty tabs on
// a large estate is one operations team, and it used to be twenty of those readings.
//
// Three properties carry the whole design, and each has a way of being wrong that
// would not show up as a failure anywhere else:
//
//   - Structure is reused inside the TTL, or the cache does nothing.
//   - Health is never reused, or a status view makes trouble wait out a timer.
//   - Visibility is never reused, or one principal's landscape is served to another.

// TestLandscapeStructureIsReadOncePerTTL. The identity of the returned facts is the
// assertion: a second reading inside the window is the *same* one, so nothing was
// read off disk to produce it.
func TestLandscapeStructureIsReadOncePerTTL(t *testing.T) {
	srv, done := bootAPIWithModels(t, t.TempDir(), false)
	defer done()

	at := time.Now()
	first, err := srv.landscapeFacts(false, at)
	if err != nil {
		t.Fatalf("landscapeFacts: %v", err)
	}
	again, err := srv.landscapeFacts(false, at.Add(meshCacheTTL-time.Millisecond))
	if err != nil {
		t.Fatalf("landscapeFacts: %v", err)
	}
	if again != first {
		t.Error("a second reading inside the TTL read the stores again")
	}

	// Past it, a fresh look — and it is dated when it was taken rather than when it
	// was asked for, which is what lets the picture say how old it is.
	later := at.Add(meshCacheTTL)
	fresh, err := srv.landscapeFacts(false, later)
	if err != nil {
		t.Fatalf("landscapeFacts: %v", err)
	}
	if fresh == first {
		t.Error("the reading outlived its TTL")
	}
	if !fresh.at.Equal(later) {
		t.Errorf("fresh.at = %v, want %v — the facts are dated when they were read", fresh.at, later)
	}

	// Drafts are a different question, so they are a different entry: asking for them
	// must not be answered from a reading that never looked for them.
	drafted, err := srv.landscapeFacts(true, later)
	if err != nil {
		t.Fatalf("landscapeFacts: %v", err)
	}
	if drafted == fresh {
		t.Error("the drafts question was answered from the reading that excluded them")
	}
}

// TestForgettingTheLandscapeCostsTheNextReaderAFreshLook. The changes that alter the
// shape of the picture do not wait out the TTL — deploying is the change a person
// makes and then immediately goes looking for.
func TestForgettingTheLandscapeCostsTheNextReaderAFreshLook(t *testing.T) {
	srv, done := bootAPIWithModels(t, t.TempDir(), false)
	defer done()

	at := time.Now()
	first, err := srv.landscapeFacts(false, at)
	if err != nil {
		t.Fatalf("landscapeFacts: %v", err)
	}
	srv.forgetLandscape()
	again, err := srv.landscapeFacts(false, at)
	if err != nil {
		t.Fatalf("landscapeFacts: %v", err)
	}
	if again == first {
		t.Error("forgetting the landscape left the old reading in place")
	}
}

// TestTheLandscapeIsFilteredForEveryCaller is the security half of the design, and
// the property a cache is most likely to break while looking like it works.
//
// Two callers, one reading. It is asserted against a *warm* reading and in both
// directions, because both halves of that matter: against a cold one the filter would
// pass whatever it did, and a filter applied once and remembered would still pass a
// one-way check if it happened to start closed.
func TestTheLandscapeIsFilteredForEveryCaller(t *testing.T) {
	srv, done := bootAPIWithModels(t, t.TempDir(), false)
	defer done()
	// With auth off every caller is an owner, which is the one setting under which
	// this test could not fail.
	srv.authEnabled = true

	owner := &httpapi.Principal{UserID: "usr_owner", Username: "owner"}
	other := &httpapi.Principal{UserID: "usr_other", Username: "other"}

	// One application, owned by one of them and shared with nobody, so the two callers
	// must see it differently (ADR-0071).
	if err := srv.projects.Save(project{ID: "app1", Name: "Billing", OwnerID: owner.UserID}); err != nil {
		t.Fatalf("save project: %v", err)
	}
	srv.forgetLandscape()

	as := func(p *httpapi.Principal) bool {
		t.Helper()
		r := httptest.NewRequest(http.MethodGet, "/api/v1/panorama/mesh", nil)
		r = r.WithContext(httpapi.WithPrincipal(r.Context(), p))
		land, _, err := srv.collectLandscape(r)
		if err != nil {
			t.Fatalf("collectLandscape: %v", err)
		}
		for _, a := range land.Applications {
			if a.ID == "app1" {
				return a.CanView
			}
		}
		t.Fatal("the application is not on the landscape at all")
		return false
	}

	// The owner first, which warms the reading; then the other caller, answered from
	// the same one; then the owner again.
	if !as(owner) {
		t.Fatal("the owner cannot see their own application")
	}
	if as(other) {
		t.Error("a second caller inherited the first caller's visibility from the cache")
	}
	if !as(owner) {
		t.Error("the owner lost sight of their own application to another caller's answer")
	}

	// And the held reading itself says nothing about either of them: it carries the
	// inputs a decision is made from — the projects and each thing's owner — and never
	// a decision, which is what makes sharing it safe at all.
	facts, err := srv.landscapeFacts(false, time.Now())
	if err != nil {
		t.Fatalf("landscapeFacts: %v", err)
	}
	if facts.projs["app1"].OwnerID != owner.UserID {
		t.Errorf("the reading carries no owner for app1, so the filter has nothing to read")
	}
}
