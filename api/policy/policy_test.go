package policy

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/pblumer/atlas/api/httpapi"
	"github.com/pblumer/atlas/api/runloop"
	"github.com/pblumer/atlas/authz"
)

const members = `{"id":"sp-members","target":{"workerTypes":["sharepoint"]},"ruleCombining":"first-applicable",
  "rules":[{"id":"members","effect":"Permit","when":{"subjectGroupsAny":["grp_documents"]}},
           {"id":"rest","effect":"Deny","reasonCode":"NOT_A_MEMBER"}]}`

func newService(t *testing.T) (*Service, string) {
	t.Helper()
	quit := make(chan struct{})
	loop := runloop.New(quit)
	go loop.Run()
	t.Cleanup(func() { close(quit) })
	dir := filepath.Join(t.TempDir(), "policies")
	s, err := Open(loop, dir, func() time.Time { return time.Unix(1_700_000_000, 0) })
	if err != nil {
		t.Fatal(err)
	}
	return s, dir
}

func request() *authz.Request {
	return &authz.Request{
		Subject:  authz.Subject{ID: "usr_alice", Known: true, Groups: []string{"grp_documents"}},
		Actor:    authz.Actor{IdentityMode: authz.ModeService, Worker: "sp-prod", WorkerType: "sharepoint"},
		Action:   authz.Action{Operation: "get-file", Digest: "d"},
		Resource: authz.Resource{Type: "sharepoint", ID: "sharepoint:site=s"},
		Context:  authz.Context{Time: 1},
	}
}

func TestADraftDecidesNothingUntilItIsPublished(t *testing.T) {
	s, _ := newService(t)
	if _, err := s.SaveDraft("sp-members", []byte(members), "usr_admin"); err != nil {
		t.Fatal(err)
	}
	if d := s.Set().Evaluate(context.Background(), request()); d.Effect != authz.NotApplicable {
		t.Fatalf("a draft decided: %v", d.Effect)
	}
	rec, err := s.Publish("sp-members", "usr_admin")
	if err != nil {
		t.Fatal(err)
	}
	if rec.Current != 1 || !rec.Enabled || rec.Draft != nil {
		t.Fatalf("published record = %+v", rec)
	}
	if d := s.Set().Evaluate(context.Background(), request()); !d.Permitted() || d.PolicyVersion != 1 {
		t.Fatalf("decided %v v%d", d.Effect, d.PolicyVersion)
	}
}

func TestAPublishedVersionIsImmutableAndANewOneSupersedesIt(t *testing.T) {
	s, dir := newService(t)
	if _, err := s.SaveDraft("sp-members", []byte(members), "usr_admin"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Publish("sp-members", "usr_admin"); err != nil {
		t.Fatal(err)
	}
	stricter := strings.Replace(members, `"effect":"Permit","when":{"subjectGroupsAny":["grp_documents"]}`,
		`"effect":"Permit","when":{"subjectGroupsAny":["grp_nobody"]}`, 1)
	if _, err := s.SaveDraft("sp-members", []byte(stricter), "usr_admin2"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Publish("sp-members", "usr_admin2"); err != nil {
		t.Fatal(err)
	}
	if d := s.Set().Evaluate(context.Background(), request()); d.Effect != authz.Deny || d.PolicyVersion != 2 {
		t.Fatalf("decided %v v%d", d.Effect, d.PolicyVersion)
	}
	v1, ok, err := s.Version("sp-members", 1)
	if err != nil || !ok || !strings.Contains(string(v1.Document), "grp_documents") || v1.PublishedBy != "usr_admin" {
		t.Fatalf("version 1 changed: %+v %v %v", v1, ok, err)
	}
	// A restart rebuilds the same set from the stores.
	s2, err := Open(s.loop, dir, s.now)
	if err != nil {
		t.Fatal(err)
	}
	if d := s2.Set().Evaluate(context.Background(), request()); d.PolicyVersion != 2 {
		t.Fatalf("after restart: v%d", d.PolicyVersion)
	}
}

// A version file a crash left behind — written, but never recorded as published — is
// never overwritten: the next publication takes the number after it.
func TestAnOrphanVersionIsSkippedNotOverwritten(t *testing.T) {
	s, _ := newService(t)
	if err := s.versions.Save(versionRecord{ID: "sp-members", Version: 1, Document: json.RawMessage(`{"orphan":true}`), PublishedBy: "crash"}); err != nil {
		t.Fatal(err)
	}
	_, _ = s.SaveDraft("sp-members", []byte(members), "usr_admin")
	rec, err := s.Publish("sp-members", "usr_admin")
	if err != nil {
		t.Fatal(err)
	}
	if rec.Current != 2 {
		t.Fatalf("published as v%d, want 2", rec.Current)
	}
	v1, _, _ := s.Version("sp-members", 1)
	if v1.PublishedBy != "crash" {
		t.Fatalf("the orphan was overwritten: %+v", v1)
	}
}

func TestPublicationRefusesAPolicyThatDoesNotCompile(t *testing.T) {
	s, _ := newService(t)
	if _, err := s.SaveDraft("bad", []byte(`{"id":"bad","rules":[]}`), "usr_admin"); err == nil {
		t.Fatal("an invalid draft was saved")
	}
	if _, err := s.SaveDraft("other", []byte(members), "usr_admin"); !errors.Is(err, ErrIDMismatch) {
		t.Fatalf("a document under another id: %v", err)
	}
	if _, err := s.Publish("missing", "usr_admin"); !errors.Is(err, ErrNoDraft) {
		t.Fatalf("publish without a draft: %v", err)
	}
}

func TestDisablingAPolicyTakesItOutOfTheSet(t *testing.T) {
	s, _ := newService(t)
	_, _ = s.SaveDraft("sp-members", []byte(members), "usr_admin")
	_, _ = s.Publish("sp-members", "usr_admin")
	if _, err := s.SetEnabled("sp-members", false, "usr_admin"); err != nil {
		t.Fatal(err)
	}
	if d := s.Set().Evaluate(context.Background(), request()); d.Effect != authz.NotApplicable {
		t.Fatalf("a disabled policy decided: %v", d.Effect)
	}
	if _, err := s.SetEnabled("missing", true, "usr_admin"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("err = %v", err)
	}
}

func TestTheSimulatorSaysItIsASimulation(t *testing.T) {
	s, _ := newService(t)
	_, _ = s.SaveDraft("sp-members", []byte(members), "usr_admin")
	_, _ = s.Publish("sp-members", "usr_admin")
	body := `{"subject":{"id":"usr_alice","known":true,"groups":["grp_documents"]},"identityMode":"service",
	  "worker":"sp-prod","workerType":"sharepoint","operation":"delete-file","resource":"sharepoint:site=s",
	  "processId":"document-approval","approved":true}`
	req := httptest.NewRequest("POST", "/api/v1/security/policies/simulate", strings.NewReader(body))
	rec := httptest.NewRecorder()
	s.HandleSimulate(rec, req)
	if rec.Code != 200 {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	var out map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	if out["simulation"] != true || out["effect"] != "Permit" || out["policyId"] != "sp-members" {
		t.Fatalf("answer = %v", out)
	}
	// "approved" is not an attribute anybody can assert: it is refused, not ignored.
	if !strings.Contains(rec.Body.String(), "approved") {
		t.Fatalf("the ignored claim was not called out: %s", rec.Body)
	}
}

func TestTheAdminRoutes(t *testing.T) {
	s, _ := newService(t)
	admin := &httpapi.Principal{UserID: "usr_admin", Roles: []string{"admin"}}
	do := func(h http.HandlerFunc, method, body string, vals map[string]string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, "/", strings.NewReader(body))
		req = req.WithContext(httpapi.WithPrincipal(req.Context(), admin))
		for k, v := range vals {
			req.SetPathValue(k, v)
		}
		rec := httptest.NewRecorder()
		h(rec, req)
		return rec
	}
	if rec := do(s.HandleSaveDraft, "PUT", members, map[string]string{"id": "sp-members"}); rec.Code != 200 {
		t.Fatalf("save: %d %s", rec.Code, rec.Body)
	}
	if rec := do(s.HandleSaveDraft, "PUT", `{"nope":1}`, map[string]string{"id": "sp-members"}); rec.Code != 400 {
		t.Fatalf("bad draft: %d", rec.Code)
	}
	if rec := do(s.HandlePublish, "POST", "", map[string]string{"id": "sp-members"}); rec.Code != 200 {
		t.Fatalf("publish: %d %s", rec.Code, rec.Body)
	}
	if rec := do(s.HandlePublish, "POST", "", map[string]string{"id": "ghost"}); rec.Code != 409 {
		t.Fatalf("publish nothing: %d", rec.Code)
	}
	if rec := do(s.HandleList, "GET", "", nil); rec.Code != 200 || !strings.Contains(rec.Body.String(), `"current":1`) {
		t.Fatalf("list: %d %s", rec.Code, rec.Body)
	}
	if rec := do(s.HandleGet, "GET", "", map[string]string{"id": "sp-members"}); rec.Code != 200 || !strings.Contains(rec.Body.String(), `"versions"`) {
		t.Fatalf("get: %d %s", rec.Code, rec.Body)
	}
	if rec := do(s.HandleGet, "GET", "", map[string]string{"id": "ghost"}); rec.Code != 404 {
		t.Fatalf("get ghost: %d", rec.Code)
	}
	if rec := do(s.HandleSetEnabled, "POST", `{"enabled":false}`, map[string]string{"id": "sp-members"}); rec.Code != 200 {
		t.Fatalf("disable: %d %s", rec.Code, rec.Body)
	}
	if rec := do(s.HandleSetEnabled, "POST", `{`, map[string]string{"id": "sp-members"}); rec.Code != 400 {
		t.Fatalf("bad enable: %d", rec.Code)
	}
	if rec := do(s.HandleSetEnabled, "POST", `{"enabled":true}`, map[string]string{"id": "ghost"}); rec.Code != 404 {
		t.Fatalf("enable ghost: %d", rec.Code)
	}
}

func simulate(t *testing.T, s *Service, body string) (int, map[string]any) {
	t.Helper()
	req := httptest.NewRequest("POST", "/", strings.NewReader(body))
	rec := httptest.NewRecorder()
	s.HandleSimulate(rec, req)
	var out map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	return rec.Code, out
}

func TestTheSimulatorTriesAnUnpublishedPolicyAndResolvesANamedSubject(t *testing.T) {
	s, _ := newService(t)
	_, _ = s.SaveDraft("sp-members", []byte(members), "usr_admin")
	_, _ = s.Publish("sp-members", "usr_admin")
	s.SetSubjectResolver(func(id string) (authz.Subject, bool) {
		if id != "usr_bob" {
			return authz.Subject{}, false
		}
		return authz.Subject{ID: "usr_bob", Known: true, Groups: []string{"grp_other"}}, true
	})
	base := `"identityMode":"service","worker":"sp-prod","workerType":"sharepoint","operation":"get-file","resource":"r","time":"2026-10-23T09:00:00Z"`
	code, out := simulate(t, s, `{"subject":{"id":"usr_bob"},`+base+`}`)
	if code != 200 || out["effect"] != "Deny" || out["subjectResolved"] != true {
		t.Fatalf("resolved bob: %d %v", code, out)
	}
	code, out = simulate(t, s, `{"subject":{"id":"usr_ghost"},`+base+`}`)
	if code != 200 || out["reasonCode"] != "SUBJECT_UNKNOWN" {
		t.Fatalf("unknown subject: %d %v", code, out)
	}
	// The candidate replaces the published version of the same id, as v2.
	candidate := strings.Replace(members, `"grp_documents"`, `"grp_other"`, 1)
	code, out = simulate(t, s, `{"subject":{"id":"usr_bob"},`+base+`,"policy":`+candidate+`}`)
	if code != 200 || out["effect"] != "Permit" || out["policyVersion"] != float64(2) {
		t.Fatalf("candidate: %d %v", code, out)
	}
	if code, _ := simulate(t, s, `{"policy":{"id":"x"}}`); code != 400 {
		t.Fatalf("broken candidate: %d", code)
	}
	if code, _ := simulate(t, s, `{"time":"yesterday"}`); code != 400 {
		t.Fatalf("bad time: %d", code)
	}
	if code, _ := simulate(t, s, `{`); code != 400 {
		t.Fatalf("bad json: %d", code)
	}
	code, out = simulate(t, s, `{"subject":{"id":"usr_x","known":false,"roles":[]},`+base+`}`)
	if code != 200 || out["reasonCode"] != "SUBJECT_UNKNOWN" {
		t.Fatalf("explicitly unknown: %v", out)
	}
}

// A set that cannot be rebuilt is no set at all: the PDP answers Indeterminate, never
// "no policies".
func TestABrokenStoreLeavesNoSet(t *testing.T) {
	s, dir := newService(t)
	_, _ = s.SaveDraft("sp-members", []byte(members), "usr_admin")
	_, _ = s.Publish("sp-members", "usr_admin")
	s.loop.Do(func() {
		_ = s.versions.Delete(versionKey("sp-members", 1))
		if err := s.rebuild(); err == nil {
			t.Error("a missing version rebuilt")
		}
	})
	if s.Set() != nil {
		t.Fatal("a set survived a failed rebuild")
	}
	// Reopening over the broken store still opens — the server must start — and says why.
	s2, err := Open(s.loop, dir, s.now)
	if err != nil || s2.Set() != nil || s2.LoadError() == nil {
		t.Fatalf("reopen: %v set=%v loadErr=%v", err, s2.Set(), s2.LoadError())
	}
	if d := s.Set().Evaluate(context.Background(), request()); d.Effect != authz.Indeterminate {
		t.Fatalf("decided %v", d.Effect)
	}
	list, err := s.List()
	if err != nil || len(list) != 1 {
		t.Fatalf("list = %v %v", list, err)
	}
}
