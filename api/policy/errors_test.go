package policy

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/iotest"

	"github.com/pblumer/atlas/api/httpapi"
	"github.com/pblumer/atlas/authz"
	"github.com/pblumer/atlas/limits"
)

// corrupt puts bytes that are not a record where a store keeps one.
func corrupt(t *testing.T, path string) {
	t.Helper()
	if err := os.WriteFile(path, []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
}

// blockWrite makes the next save of the record at path fail while reading it still
// works: a store writes through path+".tmp", and a directory there cannot be opened as
// a file. The returned func lifts the block.
func blockWrite(t *testing.T, path string) func() {
	t.Helper()
	if err := os.Mkdir(path+".tmp", 0o700); err != nil {
		t.Fatal(err)
	}
	return func() {
		if err := os.Remove(path + ".tmp"); err != nil {
			t.Fatal(err)
		}
	}
}

// admin calls a handler as an administrator with the given path values.
func admin(h http.HandlerFunc, method, body string, vals map[string]string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, "/", strings.NewReader(body))
	req = req.WithContext(httpapi.WithPrincipal(req.Context(), &httpapi.Principal{UserID: "usr_admin", Roles: []string{"admin"}}))
	for k, v := range vals {
		req.SetPathValue(k, v)
	}
	rec := httptest.NewRecorder()
	h(rec, req)
	return rec
}

func mustDetail(t *testing.T, s *Service, id string) detail {
	t.Helper()
	d, ok, err := s.get(id)
	if err != nil || !ok {
		t.Fatalf("get(%s) = %v, %v", id, ok, err)
	}
	return d
}

func TestOpenRefusesAStoreItCannotCreate(t *testing.T) {
	s, _ := newService(t)
	for name, tc := range map[string]struct {
		setup func(dir string) string // returns the directory to open
		want  string
	}{
		"a directory under a regular file": {
			setup: func(dir string) string {
				file := filepath.Join(dir, "file")
				_ = os.WriteFile(file, nil, 0o600)
				return filepath.Join(file, "policies")
			},
			want: "policy: ",
		},
		"a policy store that is a regular file": {
			setup: func(dir string) string {
				_ = os.WriteFile(filepath.Join(dir, "policies"), nil, 0o600)
				return dir
			},
			want: "policy: create dir",
		},
		"a version store that is a regular file": {
			setup: func(dir string) string {
				_ = os.WriteFile(filepath.Join(dir, "versions"), nil, 0o600)
				return dir
			},
			want: "policy version: create dir",
		},
	} {
		got, err := Open(s.loop, tc.setup(t.TempDir()), s.now)
		if err == nil || got != nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: Open = %v, %v; want an error containing %q", name, got, err, tc.want)
		}
	}
}

// Whatever keeps the published set from being built at startup, the server still
// starts — with no set, so every governed action is Indeterminate — and says why.
func TestAStoreThatCannotBeCompiledAtStartupLeavesNoSetAndSaysWhy(t *testing.T) {
	for name, tc := range map[string]struct {
		document string // the stored version 1; empty corrupts the policy record instead
		want     string
	}{
		"an unreadable policy record":     {want: "decode"},
		"a version that does not parse":   {document: `{"orphan":true}`, want: "orphan"},
		"a version that does not compile": {document: `{"id":"sp-members","rules":[]}`, want: "sp-members"},
	} {
		t.Run(name, func(t *testing.T) {
			s, dir := newService(t)
			if tc.document == "" {
				corrupt(t, s.policies.FileFor("sp-members"))
			} else {
				if err := s.versions.Save(versionRecord{ID: "sp-members", Version: 1, Document: json.RawMessage(tc.document)}); err != nil {
					t.Fatal(err)
				}
				if err := s.policies.Save(record{ID: "sp-members", Current: 1, Enabled: true}); err != nil {
					t.Fatal(err)
				}
			}
			s2, err := Open(s.loop, dir, s.now)
			if err != nil {
				t.Fatalf("the server did not start over a broken store: %v", err)
			}
			if s2.Set() != nil {
				t.Fatal("a set was built from a store that cannot be compiled")
			}
			if lerr := s2.LoadError(); lerr == nil || !strings.Contains(lerr.Error(), tc.want) {
				t.Fatalf("LoadError = %v, want it to mention %q", lerr, tc.want)
			}
			if d := s2.Set().Evaluate(context.Background(), request()); d.Effect != authz.Indeterminate || d.ReasonCode != authz.ReasonSetUnavailable {
				t.Fatalf("decided %v %s, want Indeterminate %s", d.Effect, d.ReasonCode, authz.ReasonSetUnavailable)
			}
		})
	}
}

// Switching off the policy whose version is missing is how an administrator gets the
// rest of the set back, and the load error goes with it.
func TestTheLoadErrorClearsOnceTheSetBuildsAgain(t *testing.T) {
	s, dir := newService(t)
	if err := s.LoadError(); err != nil {
		t.Fatalf("a fresh store reports %v", err)
	}
	_, _ = s.SaveDraft("sp-members", []byte(members), "usr_admin")
	_, _ = s.Publish("sp-members", "usr_admin")
	if err := os.Remove(s.versions.FileFor(versionKey("sp-members", 1))); err != nil {
		t.Fatal(err)
	}
	s2, err := Open(s.loop, dir, s.now)
	if err != nil || s2.LoadError() == nil || s2.Set() != nil {
		t.Fatalf("reopen over a missing version: %v loadErr=%v set=%v", err, s2.LoadError(), s2.Set())
	}
	if _, err := s2.SetEnabled("sp-members", false, "usr_admin"); err != nil {
		t.Fatal(err)
	}
	if err := s2.LoadError(); err != nil {
		t.Fatalf("the load error outlived the fix: %v", err)
	}
	if d := s2.Set().Evaluate(context.Background(), request()); d.Effect != authz.NotApplicable {
		t.Fatalf("decided %v, want NotApplicable from an empty set", d.Effect)
	}
}

// An empty store lists as an empty list, never null, and a full one by id; a store
// that cannot be read is a server error, never an empty list.
func TestTheListingTellsAnEmptyStoreFromABrokenOne(t *testing.T) {
	s, _ := newService(t)
	list, err := s.List()
	if err != nil || list == nil || len(list) != 0 {
		t.Fatalf("empty store: %#v, %v", list, err)
	}
	if rec := admin(s.HandleList, "GET", "", nil); rec.Code != 200 || strings.TrimSpace(rec.Body.String()) != "[]" {
		t.Fatalf("empty list route: %d %s", rec.Code, rec.Body)
	}
	for _, id := range []string{"zeta", "alpha"} {
		if _, err := s.SaveDraft(id, []byte(strings.Replace(members, `"sp-members"`, `"`+id+`"`, 1)), "usr_admin"); err != nil {
			t.Fatal(err)
		}
	}
	if list, err := s.List(); err != nil || len(list) != 2 || list[0].ID != "alpha" || list[1].ID != "zeta" {
		t.Fatalf("listing = %+v, %v; want alpha then zeta", list, err)
	}
	corrupt(t, s.policies.FileFor("sp-members"))
	if _, err := s.List(); err == nil {
		t.Fatal("a corrupt store listed")
	}
	if rec := admin(s.HandleList, "GET", "", nil); rec.Code != http.StatusInternalServerError {
		t.Fatalf("broken list route: %d %s", rec.Code, rec.Body)
	}
}

// A policy record that cannot be read refuses every change to it — none of them may
// overwrite it with a record built from nothing — and the routes answer a server
// error, not "no such policy" or "no draft".
func TestABrokenPolicyRecordRefusesEveryChange(t *testing.T) {
	s, _ := newService(t)
	path := s.policies.FileFor("sp-members")
	corrupt(t, path)

	if _, err := s.SaveDraft("sp-members", []byte(members), "usr_admin"); err == nil {
		t.Error("a draft was saved over an unreadable record")
	}
	if _, err := s.Publish("sp-members", "usr_admin"); err == nil || errors.Is(err, ErrNoDraft) {
		t.Errorf("publish: err = %v, want a store error", err)
	}
	if _, err := s.SetEnabled("sp-members", false, "usr_admin"); err == nil || errors.Is(err, ErrNotFound) {
		t.Errorf("enable: err = %v, want a store error", err)
	}
	if _, _, err := s.get("sp-members"); err == nil {
		t.Error("get read an unreadable record")
	}
	id := map[string]string{"id": "sp-members"}
	for name, rec := range map[string]*httptest.ResponseRecorder{
		"get":     admin(s.HandleGet, "GET", "", id),
		"publish": admin(s.HandlePublish, "POST", "", id),
		"enable":  admin(s.HandleSetEnabled, "POST", `{"enabled":true}`, id),
	} {
		if rec.Code != http.StatusInternalServerError {
			t.Errorf("%s: %d %s, want 500", name, rec.Code, rec.Body)
		}
	}
	if got, _ := os.ReadFile(path); string(got) != "{not json" {
		t.Fatalf("the unreadable record was overwritten: %s", got)
	}
}

// A publication that cannot read or write its version file changes nothing: the
// draft stays a draft and the set does not move.
func TestAPublicationThatCannotStoreItsVersionKeepsTheDraft(t *testing.T) {
	for name, breakVersion := range map[string]func(t *testing.T, path string){
		"the next version cannot be read":    func(t *testing.T, path string) { corrupt(t, path) },
		"the next version cannot be written": func(t *testing.T, path string) { blockWrite(t, path) },
	} {
		t.Run(name, func(t *testing.T) {
			s, _ := newService(t)
			if _, err := s.SaveDraft("sp-members", []byte(members), "usr_admin"); err != nil {
				t.Fatal(err)
			}
			breakVersion(t, s.versions.FileFor(versionKey("sp-members", 1)))
			if _, err := s.Publish("sp-members", "usr_admin"); err == nil {
				t.Fatal("published although the version could not be stored")
			}
			d := mustDetail(t, s, "sp-members")
			if d.Current != 0 || d.Enabled || d.Draft == nil || len(d.Versions) != 0 {
				t.Fatalf("after a failed publication: %+v", d)
			}
			if dec := s.Set().Evaluate(context.Background(), request()); dec.Effect != authz.NotApplicable {
				t.Fatalf("the set moved: %v", dec.Effect)
			}
		})
	}
}

// A version written but never recorded — the policy record could not be saved — is the
// orphan a crash leaves: nothing decides with it, the draft is kept, and the retry
// publishes past it rather than over it.
func TestAPublicationThatCannotBeRecordedLeavesAnOrphanTheRetrySkips(t *testing.T) {
	s, _ := newService(t)
	if _, err := s.SaveDraft("sp-members", []byte(members), "usr_admin"); err != nil {
		t.Fatal(err)
	}
	unblock := blockWrite(t, s.policies.FileFor("sp-members"))
	if _, err := s.Publish("sp-members", "usr_admin"); err == nil {
		t.Fatal("published although the policy record could not be saved")
	}
	if _, ok, err := s.Version("sp-members", 1); err != nil || !ok {
		t.Fatalf("the version file was not written first: %v %v", ok, err)
	}
	if d := mustDetail(t, s, "sp-members"); d.Current != 0 || d.Enabled || d.Draft == nil {
		t.Fatalf("the record moved: %+v", d.Summary)
	}
	if dec := s.Set().Evaluate(context.Background(), request()); dec.Effect != authz.NotApplicable {
		t.Fatalf("an unrecorded version decided: %v", dec.Effect)
	}
	unblock()
	rec, err := s.Publish("sp-members", "usr_admin")
	if err != nil || rec.Current != 2 {
		t.Fatalf("retry: %+v %v, want version 2", rec, err)
	}
	if dec := s.Set().Evaluate(context.Background(), request()); !dec.Permitted() || dec.PolicyVersion != 2 {
		t.Fatalf("after the retry: %v v%d", dec.Effect, dec.PolicyVersion)
	}
}

func TestSwitchingOffAPolicyThatCannotBeSavedLeavesItDeciding(t *testing.T) {
	s, _ := newService(t)
	_, _ = s.SaveDraft("sp-members", []byte(members), "usr_admin")
	_, _ = s.Publish("sp-members", "usr_admin")
	blockWrite(t, s.policies.FileFor("sp-members"))
	if _, err := s.SetEnabled("sp-members", false, "usr_admin"); err == nil {
		t.Fatal("switched off although the record could not be saved")
	}
	if d := mustDetail(t, s, "sp-members"); !d.Enabled {
		t.Fatalf("the record says disabled: %+v", d.Summary)
	}
	if dec := s.Set().Evaluate(context.Background(), request()); !dec.Permitted() || dec.PolicyID != "sp-members" {
		t.Fatalf("the set changed: %v %s", dec.Effect, dec.PolicyID)
	}
}

// A body is read within the Definition budget, and a service whose Limits were never
// set reads it with the defaults rather than admitting nothing.
func TestADraftIsReadWithinTheDefinitionBudget(t *testing.T) {
	s, _ := newService(t)
	s.Limits = limits.Limits{}
	if rec := admin(s.HandleSaveDraft, "PUT", members, map[string]string{"id": "sp-members"}); rec.Code != 200 {
		t.Fatalf("unset limits: %d %s", rec.Code, rec.Body)
	}
	s.Limits = limits.Default()
	s.Limits.Definition = 16
	bigger := strings.Replace(members, "grp_documents", "grp_other", 1)
	if rec := admin(s.HandleSaveDraft, "PUT", bigger, map[string]string{"id": "sp-members"}); rec.Code != http.StatusBadRequest {
		t.Fatalf("a draft over the budget: %d %s", rec.Code, rec.Body)
	}
	if d := mustDetail(t, s, "sp-members"); !strings.Contains(string(d.Draft), "grp_documents") {
		t.Fatalf("the draft over the budget replaced the stored one: %s", d.Draft)
	}
}

func TestABodyThatCannotBeReadIsABadRequest(t *testing.T) {
	s, _ := newService(t)
	for name, h := range map[string]http.HandlerFunc{
		"save draft": s.HandleSaveDraft,
		"enable":     s.HandleSetEnabled,
		"simulate":   s.HandleSimulate,
	} {
		req := httptest.NewRequest("POST", "/", iotest.ErrReader(errors.New("connection reset")))
		req.SetPathValue("id", "sp-members")
		rec := httptest.NewRecorder()
		h(rec, req)
		if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "connection reset") {
			t.Errorf("%s: %d %s", name, rec.Code, rec.Body)
		}
	}
	if list, _ := s.List(); len(list) != 0 {
		t.Fatalf("an unread body stored %v", list)
	}
}

// A change made without a principal is attributed to nobody rather than failing.
func TestAChangeWithoutAPrincipalIsAttributedToNobody(t *testing.T) {
	s, _ := newService(t)
	req := httptest.NewRequest("PUT", "/", strings.NewReader(members))
	req.SetPathValue("id", "sp-members")
	rec := httptest.NewRecorder()
	s.HandleSaveDraft(rec, req)
	var out Summary
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil || rec.Code != 200 || out.UpdatedBy != "" {
		t.Fatalf("%d %s (%v)", rec.Code, rec.Body, err)
	}
}

const base = `"identityMode":"service","worker":"sp-prod","workerType":"sharepoint","operation":"get-file","resource":"r","time":"2026-10-23T09:00:00Z"`

func TestTheSimulatorRefusesACandidateThatIsNotAPolicyDocument(t *testing.T) {
	s, _ := newService(t)
	for name, candidate := range map[string]string{
		"not an object":    `42`,
		"an unknown field": `{"id":"x","approved":true}`,
	} {
		code, out := simulate(t, s, `{"subject":{"id":"usr_bob","groups":[]},`+base+`,"policy":`+candidate+`}`)
		msg, _ := out["error"].(string)
		if code != http.StatusBadRequest || out["effect"] != nil || !strings.Contains(msg, "policy: ") {
			t.Errorf("%s: %d %v, want the document's parse error", name, code, out)
		}
	}
}

// A candidate is compiled beside the published set, so a policy store that cannot be
// read leaves nothing to simulate against and the simulation is refused — as the
// server's failure, not the caller's.
func TestTheSimulatorRefusesACandidateWhenThePolicyStoreCannotBeRead(t *testing.T) {
	s, _ := newService(t)
	corrupt(t, s.policies.FileFor("sp-broken"))
	code, out := simulate(t, s, `{"subject":{"id":"usr_bob","groups":["grp_documents"]},`+base+`,"policy":`+members+`}`)
	msg, _ := out["error"].(string)
	if code != http.StatusInternalServerError || out["effect"] != nil || !strings.Contains(msg, "decode") {
		t.Fatalf("%d %v", code, out)
	}
}

// A simulation with a candidate is asked against the published set with the candidate
// in its place. When a published version cannot be read, enforcement has no set and
// refuses every governed action; a simulation that left the unreadable policy out
// would answer Permit where enforcement refuses, so it refuses to answer instead.
func TestASimulationRefusesWhenAPublishedPolicyCannotBeRead(t *testing.T) {
	s, _ := newService(t)
	_, _ = s.SaveDraft("sp-members", []byte(members), "usr_admin")
	_, _ = s.Publish("sp-members", "usr_admin")
	bob := `{"subject":{"id":"usr_bob","groups":["grp_other"]},` + base
	if code, out := simulate(t, s, bob+`}`); code != 200 || out["effect"] != "Deny" {
		t.Fatalf("published set: %d %v", code, out)
	}
	if err := os.Remove(s.versions.FileFor(versionKey("sp-members", 1))); err != nil {
		t.Fatal(err)
	}
	open := `{"id":"sp-open","target":{"workerTypes":["sharepoint"]},"ruleCombining":"first-applicable","rules":[{"id":"all","effect":"Permit"}]}`
	code, out := simulate(t, s, bob+`,"policy":`+open+`}`)
	if code != http.StatusInternalServerError || out["effect"] == "Permit" {
		t.Fatalf("candidate beside an unreadable published policy: %d %v", code, out)
	}
	// A candidate that does not parse is the caller's mistake, not the server's.
	if code, _ := simulate(t, s, bob+`,"policy":{"id":"x","rules":"nope"}}`); code != http.StatusBadRequest {
		t.Fatalf("a candidate that does not parse: %d", code)
	}
}

func asJSON(v any) string { b, _ := json.Marshal(v); return string(b) }

func TestTraceViewRendersEveryStepAndNeverNull(t *testing.T) {
	got := asJSON(TraceView([]authz.Step{{PolicyID: "p", PolicyVersion: 2, RuleID: "r", Effect: authz.Deny, Reason: "NOPE", Note: "n"}}))
	want := `[{"policyId":"p","policyVersion":2,"ruleId":"r","effect":"Deny","reasonCode":"NOPE","note":"n"}]`
	if got != want {
		t.Fatalf("TraceView = %s, want %s", got, want)
	}
	if got := asJSON(TraceView(nil)); got != "[]" {
		t.Fatalf("an empty trace = %s, want []", got)
	}
}

func TestCompactKeepsADocumentItCannotCompact(t *testing.T) {
	if got := string(compact(json.RawMessage("{ \"a\" : [1, 2] }"))); got != `{"a":[1,2]}` {
		t.Errorf("compact = %s", got)
	}
	if got := string(compact(json.RawMessage(`{"a":`))); got != `{"a":` {
		t.Errorf("an invalid document came back as %s", got)
	}
}

// A draft the server cannot store is the server's failure, answered 500; a draft that
// does not compile is the caller's, answered 400. The Console tells the two apart.
func TestADraftTheServerCannotStoreIsNotTheCallersMistake(t *testing.T) {
	s, _ := newService(t)
	defer blockWrite(t, s.policies.FileFor("sp-members"))()
	if rec := admin(s.HandleSaveDraft, "PUT", members, map[string]string{"id": "sp-members"}); rec.Code != http.StatusInternalServerError {
		t.Fatalf("an unwritable store: %d %s", rec.Code, rec.Body)
	}
	if rec := admin(s.HandleSaveDraft, "PUT", `{"id":"sp-members","rules":"nope"}`, map[string]string{"id": "sp-members"}); rec.Code != http.StatusBadRequest {
		t.Fatalf("a draft that does not parse: %d %s", rec.Code, rec.Body)
	}
}
