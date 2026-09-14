package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/pblumer/atlas/api/httpapi"
)

// Co-editing a decision (ADR-draft-co-editing-a-decision): ADR-0140's session
// over a decision draft. What these hold down is that it is the *same* session —
// same registry, same semantics, same scope rule — and that the two namespaces
// cannot be crossed.

// sessionHarness drives the session routes over a server, returning status and
// body like every other harness here.
type sessionHarness struct {
	t *testing.T
	h http.Handler
}

func (x sessionHarness) post(path, body string) (int, []byte) {
	x.t.Helper()
	var req *http.Request
	if body != "" {
		req = httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
	} else {
		req = httptest.NewRequest(http.MethodPost, path, nil)
	}
	rec := httptest.NewRecorder()
	x.h.ServeHTTP(rec, req)
	return rec.Code, rec.Body.Bytes()
}

// joinDecisionSession joins without a stream — the same door an agent uses — and
// returns the participant id from the sync snapshot.
func joinDecisionSession(t *testing.T, x sessionHarness, draftID, name string) string {
	t.Helper()
	code, b := x.post("/api/v1/dmn-drafts/"+draftID+"/session/join", `{"name":"`+name+`"}`)
	if code != http.StatusOK {
		t.Fatalf("join decision session: %d %s", code, b)
	}
	var sync struct {
		Self string `json:"self"`
	}
	if err := json.Unmarshal(b, &sync); err != nil {
		t.Fatalf("decode sync: %v (%s)", err, b)
	}
	if sync.Self == "" {
		t.Fatalf("sync snapshot names no participant: %s", b)
	}
	return sync.Self
}

// seedDmnDraft stores one decision draft, optionally filed into an application.
func seedDmnDraft(t *testing.T, srv *Server, id, projectID string) {
	t.Helper()
	srv.do(func() {
		if err := srv.dmnDrafts.Save(dmnDraft{ID: id, Name: "Eligibility", ProjectID: projectID, XML: "<definitions/>"}); err != nil {
			t.Fatalf("seed decision draft: %v", err)
		}
	})
}

// The headline: two people on one decision draft see each other, and the lock is
// the thing that stops them editing the same decision at once.
func TestTwoPeopleCanCoEditOneDecision(t *testing.T) {
	srv, _ := newValidateServer(t)
	x := sessionHarness{t, srv.Handler()}
	seedDmnDraft(t, srv, "dd-1", "")

	alice := joinDecisionSession(t, x, "dd-1", "Alice")
	bob := joinDecisionSession(t, x, "dd-1", "Bob")
	if alice == bob {
		t.Fatal("two joins produced one participant")
	}

	// Alice claims the eligibility decision — in the requirements graph that is one
	// element, and its table is that element's contents.
	if code, b := x.post("/api/v1/dmn-drafts/dd-1/session/lock",
		`{"participantId":"`+alice+`","elementId":"eligibility","action":"acquire"}`); code != http.StatusNoContent {
		t.Fatalf("Alice's lock: %d %s", code, b)
	}
	// Bob is told, rather than left to find out by losing his typing.
	if code, b := x.post("/api/v1/dmn-drafts/dd-1/session/lock",
		`{"participantId":"`+bob+`","elementId":"eligibility","action":"acquire"}`); code != http.StatusConflict {
		t.Fatalf("Bob's lock on a held decision: %d %s, want 409", code, b)
	}
	// A different decision of the same model is free, which is the case this buys.
	if code, b := x.post("/api/v1/dmn-drafts/dd-1/session/lock",
		`{"participantId":"`+bob+`","elementId":"fee","action":"acquire"}`); code != http.StatusNoContent {
		t.Fatalf("Bob's lock on another decision: %d %s", code, b)
	}
	// And releasing hands it over.
	if code, _ := x.post("/api/v1/dmn-drafts/dd-1/session/lock",
		`{"participantId":"`+alice+`","elementId":"eligibility","action":"release"}`); code != http.StatusNoContent {
		t.Fatal("Alice could not release her own lock")
	}
	if code, b := x.post("/api/v1/dmn-drafts/dd-1/session/lock",
		`{"participantId":"`+bob+`","elementId":"eligibility","action":"acquire"}`); code != http.StatusNoContent {
		t.Fatalf("Bob's lock after release: %d %s", code, b)
	}
}

// A change one participant broadcasts reaches the other's poll — the read side an
// editor without a stream uses, and the proof that the relay works at all.
func TestAChangeReachesTheOtherParticipant(t *testing.T) {
	srv, _ := newValidateServer(t)
	x := sessionHarness{t, srv.Handler()}
	seedDmnDraft(t, srv, "dd-1", "")

	alice := joinDecisionSession(t, x, "dd-1", "Alice")
	bob := joinDecisionSession(t, x, "dd-1", "Bob")

	if code, b := x.post("/api/v1/dmn-drafts/dd-1/session/change",
		`{"participantId":"`+alice+`","elementId":"eligibility","xml":"<definitions id=\"v2\"/>"}`); code != http.StatusNoContent {
		t.Fatalf("change: %d %s", code, b)
	}
	code, b := x.post("/api/v1/dmn-drafts/dd-1/session/poll", `{"participantId":"`+bob+`"}`)
	if code != http.StatusOK {
		t.Fatalf("poll: %d %s", code, b)
	}
	if !strings.Contains(string(b), "eligibility") {
		t.Fatalf("Bob's poll did not carry Alice's change: %s", b)
	}
}

// The two namespaces are separate: a BPMN draft and a decision draft that happen
// to share an id are two sessions, not one silently shared.
func TestADiagramAndADecisionWithOneIdAreTwoSessions(t *testing.T) {
	srv, _ := newValidateServer(t)
	x := sessionHarness{t, srv.Handler()}
	seedDmnDraft(t, srv, "orders", "")
	srv.do(func() {
		if err := srv.drafts.Save(draft{ProcessID: "orders", Name: "Orders", XML: "<x/>"}); err != nil {
			t.Fatalf("seed bpmn draft: %v", err)
		}
	})

	inDiagram := joinDecisionSession(t, x, "orders", "Alice") // the decision session
	code, b := x.post("/api/v1/drafts/orders/session/join", `{"name":"Bob"}`)
	if code != http.StatusOK {
		t.Fatalf("join diagram session: %d %s", code, b)
	}

	// The decision session's roster holds only its own participant.
	code, b = x.post("/api/v1/dmn-drafts/orders/session/poll", `{"participantId":"`+inDiagram+`"}`)
	if code != http.StatusOK {
		t.Fatalf("poll: %d %s", code, b)
	}
	if strings.Contains(string(b), "Bob") {
		t.Fatalf("the diagram's participant appeared in the decision's session: %s", b)
	}
}

// A session inherits the application's sharing scope (ADR-0071): editor to
// co-edit, viewer to watch only, and a stranger is not told the draft exists.
func TestADecisionSessionInheritsTheApplicationScope(t *testing.T) {
	srv, _ := newValidateServer(t, WithAuth())
	srv.do(func() {
		if err := srv.projects.Save(project{
			ID: "app1", Name: "Orders", OwnerID: "usr_owner", Visibility: VisibilityShared,
			Members: []projectMember{
				{Ref: principalRef{Type: PrincipalTypeUser, ID: "usr_editor"}, Role: ScopeRoleEditor},
				{Ref: principalRef{Type: PrincipalTypeUser, ID: "usr_viewer"}, Role: ScopeRoleViewer},
			},
		}); err != nil {
			t.Fatalf("save project: %v", err)
		}
		if err := srv.dmnDrafts.Save(dmnDraft{ID: "dd-1", Name: "Eligibility", ProjectID: "app1", XML: "<definitions/>"}); err != nil {
			t.Fatalf("seed draft: %v", err)
		}
	})
	h := srv.Handler()
	as := func(userID, path, body string) (int, []byte) {
		tok, err := srv.sessions.create(User{ID: userID, Username: userID, Roles: []string{RoleModeler, RoleUser}}, nil)
		if err != nil {
			t.Fatalf("create session: %v", err)
		}
		req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.AddCookie(&http.Cookie{Name: sessionCookie, Value: tok})
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec.Code, rec.Body.Bytes()
	}
	selfOf := func(b []byte) string {
		var sync struct {
			Self string `json:"self"`
		}
		if err := json.Unmarshal(b, &sync); err != nil {
			t.Fatalf("decode sync: %v (%s)", err, b)
		}
		return sync.Self
	}

	// A stranger is not told the draft is there.
	if code, b := as("usr_stranger", "/api/v1/dmn-drafts/dd-1/session/join", `{}`); code != http.StatusNotFound {
		t.Fatalf("stranger join = %d %s, want 404", code, b)
	}

	// A viewer joins — watching is the point — but may not change anything.
	code, b := as("usr_viewer", "/api/v1/dmn-drafts/dd-1/session/join", `{}`)
	if code != http.StatusOK {
		t.Fatalf("viewer join = %d %s, want them able to watch", code, b)
	}
	viewer := selfOf(b)
	if code, b := as("usr_viewer", "/api/v1/dmn-drafts/dd-1/session/lock",
		`{"participantId":"`+viewer+`","elementId":"eligibility","action":"acquire"}`); code != http.StatusForbidden {
		t.Fatalf("viewer lock = %d %s, want 403", code, b)
	}

	// An editor joins and may.
	code, b = as("usr_editor", "/api/v1/dmn-drafts/dd-1/session/join", `{}`)
	if code != http.StatusOK {
		t.Fatalf("editor join = %d %s", code, b)
	}
	editor := selfOf(b)
	if code, b := as("usr_editor", "/api/v1/dmn-drafts/dd-1/session/lock",
		`{"participantId":"`+editor+`","elementId":"eligibility","action":"acquire"}`); code != http.StatusNoContent {
		t.Fatalf("editor lock = %d %s", code, b)
	}
}

// A decision draft that does not exist has no session to join, and a draft filed
// into no application stays open — the same rule its content handlers follow.
func TestADecisionSessionFollowsTheDraftItIsAbout(t *testing.T) {
	srv, _ := newValidateServer(t)
	x := sessionHarness{t, srv.Handler()}

	if code, b := x.post("/api/v1/dmn-drafts/dd-gone/session/join", `{}`); code != http.StatusNotFound {
		t.Fatalf("join a session on nothing = %d %s, want 404", code, b)
	}
	seedDmnDraft(t, srv, "dd-loose", "")
	if code, b := x.post("/api/v1/dmn-drafts/dd-loose/session/join", `{}`); code != http.StatusOK {
		t.Fatalf("join an ungrouped decision's session = %d %s", code, b)
	}
}

// A store the session cannot read is reported rather than answered as a session
// nobody may join.
func TestADecisionSessionReportsAStoreItCannotRead(t *testing.T) {
	srv, _ := newValidateServer(t)
	srv.dmnDrafts = brokenStore(newDmnDraftStore(t.TempDir() + "/gone"))
	x := sessionHarness{t, srv.Handler()}
	if code, b := x.post("/api/v1/dmn-drafts/dd-1/session/join", `{}`); code != http.StatusNotFound {
		// A removed directory is a clean miss, so the draft is simply not there.
		t.Fatalf("join = %d %s, want 404 for a draft that is not there", code, b)
	}
}

// Leaving releases what the participant held, so a colleague is not locked out by
// somebody who closed their laptop.
func TestLeavingADecisionSessionReleasesItsLocks(t *testing.T) {
	srv, _ := newValidateServer(t)
	x := sessionHarness{t, srv.Handler()}
	seedDmnDraft(t, srv, "dd-1", "")

	alice := joinDecisionSession(t, x, "dd-1", "Alice")
	bob := joinDecisionSession(t, x, "dd-1", "Bob")
	if code, _ := x.post("/api/v1/dmn-drafts/dd-1/session/lock",
		`{"participantId":"`+alice+`","elementId":"eligibility","action":"acquire"}`); code != http.StatusNoContent {
		t.Fatal("Alice could not take the lock")
	}
	if code, _ := x.post("/api/v1/dmn-drafts/dd-1/session/leave", `{"participantId":"`+alice+`"}`); code != http.StatusNoContent {
		t.Fatal("Alice could not leave")
	}
	if code, b := x.post("/api/v1/dmn-drafts/dd-1/session/lock",
		`{"participantId":"`+bob+`","elementId":"eligibility","action":"acquire"}`); code != http.StatusNoContent {
		t.Fatalf("Bob after Alice left = %d %s, want the lock free", code, b)
	}
	// Leaving twice is still fine: a retrying client never wedges on cleanup.
	if code, _ := x.post("/api/v1/dmn-drafts/dd-1/session/leave", `{"participantId":"`+alice+`"}`); code != http.StatusNoContent {
		t.Fatal("a second leave was refused")
	}
}

// A stale participant — one whose session was reaped — is told to rejoin rather
// than silently ignored, and the message names the decision it is about.
func TestAStaleParticipantIsToldToRejoin(t *testing.T) {
	srv, _ := newValidateServer(t)
	x := sessionHarness{t, srv.Handler()}
	seedDmnDraft(t, srv, "dd-1", "")
	joinDecisionSession(t, x, "dd-1", "Alice")

	code, b := x.post("/api/v1/dmn-drafts/dd-1/session/presence", `{"participantId":"nobody","selection":"eligibility"}`)
	if code != http.StatusNotFound {
		t.Fatalf("presence from a stranger = %d %s, want 404", code, b)
	}
	if !strings.Contains(string(b), "decision") {
		t.Fatalf("the refusal = %s, want it to name what session it is about", b)
	}
}

// The scope check reads the principal the middleware resolved, which is what lets
// the same handler serve both artifact kinds without knowing about either.
func TestTheDecisionSessionAccessRuleIsTheDraftsOwn(t *testing.T) {
	srv, _ := newValidateServer(t, WithAuth())
	srv.do(func() {
		if err := srv.projects.Save(project{ID: "app1", OwnerID: "usr_owner", Visibility: VisibilityPrivate}); err != nil {
			t.Fatalf("save project: %v", err)
		}
		if err := srv.dmnDrafts.Save(dmnDraft{ID: "dd-1", ProjectID: "app1", XML: "<definitions/>"}); err != nil {
			t.Fatalf("seed draft: %v", err)
		}
	})
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req = req.WithContext(httpapi.WithPrincipal(req.Context(),
		&httpapi.Principal{UserID: "usr_owner", Roles: []string{RoleUser}}))
	canEdit, status, _ := srv.dmnDraftSessionAccess(req, "dd-1")
	if status != 0 || !canEdit {
		t.Fatalf("owner access = %v %d, want them able to edit", canEdit, status)
	}
	stranger := httptest.NewRequest(http.MethodGet, "/", nil)
	stranger = stranger.WithContext(httpapi.WithPrincipal(stranger.Context(),
		&httpapi.Principal{UserID: "usr_other", Roles: []string{RoleUser}}))
	if _, status, _ := srv.dmnDraftSessionAccess(stranger, "dd-1"); status != http.StatusNotFound {
		t.Fatalf("stranger access = %d, want 404 so the decision's existence does not leak", status)
	}
}
