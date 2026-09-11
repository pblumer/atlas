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

}

// TestARevokedMemberLosesTheLandscapeOnTheNextRequest is the property the test above
// cannot reach, and the one a cache is most dangerous to.
//
// Filtering two *concurrent* callers apart proves the decision is made per request. It
// says nothing about what the decision is made *from* — and an access decision read
// off a record that is half a minute old returns the answer from half a minute ago.
// project.effectiveRole answers from the project's own Members, Visibility and
// OwnerID, so a cache holding the project record would keep a removed member inside
// the application for the rest of its TTL: their session is still valid, their role is
// still modeler, and the mesh is the one endpoint that aggregates the whole estate.
//
// The revocation is written straight to the store rather than through the handler,
// and deliberately: it is the strongest form of the claim. It holds for a record
// changed by a handler that forgot to invalidate, by a handler nobody has written
// yet, or by something outside this server entirely — none of which a hook could
// cover.
func TestARevokedMemberLosesTheLandscapeOnTheNextRequest(t *testing.T) {
	srv, done := bootAPIWithModels(t, t.TempDir(), false)
	defer done()
	srv.authEnabled = true

	owner := &httpapi.Principal{UserID: "usr_owner", Username: "owner"}
	member := &httpapi.Principal{UserID: "usr_member", Username: "member"}

	shared := project{
		ID: "app1", Name: "Billing", OwnerID: owner.UserID,
		Visibility: VisibilityShared,
		Members: []projectMember{{
			Ref:  principalRef{Type: PrincipalTypeUser, ID: member.UserID},
			Role: ScopeRoleViewer,
		}},
	}
	if err := srv.projects.Save(shared); err != nil {
		t.Fatalf("save project: %v", err)
	}
	srv.forgetLandscape()

	sees := func(p *httpapi.Principal) bool {
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

	// The member reads it, which warms the structural reading.
	if !sees(member) {
		t.Fatal("a viewer member cannot see the application they are a member of")
	}

	// Revoked, and nothing is told to forget anything.
	revoked := shared
	revoked.Members = nil
	if err := srv.projects.Save(revoked); err != nil {
		t.Fatalf("save project: %v", err)
	}

	// The very next request, well inside the TTL. Nothing about the structure has
	// changed, so the reading is deliberately still warm — which is exactly the case
	// that used to answer from the membership list as it stood before the revoke.
	if sees(member) {
		t.Error("a revoked member still sees the application on their next request")
	}
	// And the owner is unaffected, so the check is a revocation rather than a landscape
	// that simply stopped answering.
	if !sees(owner) {
		t.Error("the owner lost their own application")
	}
}
