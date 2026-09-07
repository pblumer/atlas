package taskfolder

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/pblumer/atlas/api/httpapi"
	"github.com/pblumer/atlas/api/runloop"
)

// tasks is the fixed population the counting collaborator scans in these tests —
// the engine's answer, stubbed, so the service can be driven without one.
var tasks = []Task{
	{ProcessID: "kunden-anfrage", TaskName: "Anfrage sichten", Priority: 50},
	{ProcessID: "kunden-anfrage", TaskName: "Angebot erstellen", Assignee: "me", Priority: 50},
	{ProcessID: "service-desk-ticket", TaskName: "Ersatzgerät beschaffen", Priority: 60},
	{ProcessID: "onboarding", TaskName: "Willkommen", Priority: 50},
}

// newService builds a service over a temporary store, a running loop, and stubs
// for the two collaborators the server normally supplies.
func newService(t *testing.T) (*Service, *Store) {
	t.Helper()
	store, err := NewStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	quit := make(chan struct{})
	loop := runloop.New(quit)
	var wg sync.WaitGroup
	wg.Add(1)
	go func() { defer wg.Done(); loop.Run() }()
	t.Cleanup(func() { close(quit); wg.Wait() })

	options := func(User) Options {
		return Options{
			Processes: []Option{{Value: "kunden-anfrage", Label: "Kundenanfrage"}},
			Users:     []Option{{Value: "usr_me", Label: "Patrick"}},
		}
	}
	count := func(ms []*Matcher, u User) ([]int, int, bool, error) {
		out := make([]int, len(ms))
		now := time.Now()
		for i, m := range ms {
			for _, task := range tasks {
				if m.Match(task, u, now) {
					out[i]++
				}
			}
		}
		return out, len(tasks), false, nil
	}
	ids := 0
	newID := func() (string, error) {
		ids++
		return fmt.Sprintf("%032x", ids), nil
	}
	return New(loop, store, options, count, newID), store
}

// as builds a request carrying an authenticated principal.
func as(method, body, userID string, groups ...string) *http.Request {
	var r *http.Request
	if body != "" {
		r = httptest.NewRequest(method, "/", strings.NewReader(body))
	} else {
		r = httptest.NewRequest(method, "/", nil)
	}
	if userID == "" {
		return r
	}
	p := &httpapi.Principal{UserID: userID, Username: strings.TrimPrefix(userID, "usr_"), GroupIDs: groups}
	return r.WithContext(httpapi.WithPrincipal(context.Background(), p))
}

func do(t *testing.T, h http.HandlerFunc, r *http.Request, vals map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	for k, v := range vals {
		r.SetPathValue(k, v)
	}
	rec := httptest.NewRecorder()
	h(rec, r)
	return rec
}

const kundenRule = `{"name":"Kunden Anfragen","rule":{"match":"all","conditions":[` +
	`{"field":"process","op":"is","value":"kunden-anfrage"}]}}`

// create posts one folder and returns the decoded response.
func create(t *testing.T, svc *Service, body, userID string) folderResp {
	t.Helper()
	rec := do(t, svc.HandleCreate, as(http.MethodPost, body, userID), nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("create = %d, body %s", rec.Code, rec.Body)
	}
	var out folderResp
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode: %v (%s)", err, rec.Body)
	}
	return out
}

// TestCreateListUpdateDelete is the area's round trip: a folder created is listed
// with its generated expression, changed, and removed.
func TestCreateListUpdateDelete(t *testing.T) {
	svc, _ := newService(t)
	got := create(t, svc, kundenRule, "usr_me")
	if got.Name != "Kunden Anfragen" || got.Owner != "usr_me" || got.Visibility != VisibilityPrivate {
		t.Fatalf("created folder = %+v", got)
	}
	if got.FEEL != `processId = "kunden-anfrage"` {
		t.Errorf("FEEL = %q", got.FEEL)
	}
	if !got.Editable {
		t.Error("the owner cannot edit their own folder")
	}

	rec := do(t, svc.HandleList, as(http.MethodGet, "", "usr_me"), nil)
	var list []folderResp
	if err := json.Unmarshal(rec.Body.Bytes(), &list); err != nil {
		t.Fatalf("decode list: %v", err)
	}
	if len(list) != 1 || list[0].ID != got.ID {
		t.Fatalf("list = %+v", list)
	}

	upd := `{"name":"Kundenanfragen offen","visibility":"org","rule":{"match":"all","conditions":[` +
		`{"field":"process","op":"is","value":"kunden-anfrage"},{"field":"assignee","op":"isEmpty"}]}}`
	rec = do(t, svc.HandleUpdate, as(http.MethodPut, upd, "usr_me"), map[string]string{"id": got.ID})
	if rec.Code != http.StatusOK {
		t.Fatalf("update = %d, body %s", rec.Code, rec.Body)
	}
	var after folderResp
	if err := json.Unmarshal(rec.Body.Bytes(), &after); err != nil {
		t.Fatal(err)
	}
	if after.Name != "Kundenanfragen offen" || after.Visibility != VisibilityOrg {
		t.Errorf("updated folder = %+v", after)
	}
	if !strings.Contains(after.FEEL, "assignee = null") {
		t.Errorf("updated FEEL = %q", after.FEEL)
	}

	rec = do(t, svc.HandleDelete, as(http.MethodDelete, "", "usr_me"), map[string]string{"id": got.ID})
	if rec.Code != http.StatusOK {
		t.Fatalf("delete = %d, body %s", rec.Code, rec.Body)
	}
	rec = do(t, svc.HandleList, as(http.MethodGet, "", "usr_me"), nil)
	if strings.TrimSpace(rec.Body.String()) != "[]" {
		t.Errorf("list after delete = %s", rec.Body)
	}
}

// TestOnlyTheOwnerCanChangeAFolder covers the sharing rule from the other side: a
// shared folder is visible to the team and editable by nobody but its owner.
func TestOnlyTheOwnerCanChangeAFolder(t *testing.T) {
	svc, _ := newService(t)
	shared := `{"name":"Service Desk","visibility":"group","groupId":"grp_sd",` +
		`"rule":{"match":"all","conditions":[{"field":"process","op":"is","value":"service-desk-ticket"}]}}`
	got := create(t, svc, shared, "usr_lead")

	rec := do(t, svc.HandleList, as(http.MethodGet, "", "usr_member", "grp_sd"), nil)
	var list []folderResp
	if err := json.Unmarshal(rec.Body.Bytes(), &list); err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 || list[0].Editable {
		t.Fatalf("a group member sees %+v; want one folder, not editable", list)
	}

	rec = do(t, svc.HandleList, as(http.MethodGet, "", "usr_outsider", "grp_other"), nil)
	if strings.TrimSpace(rec.Body.String()) != "[]" {
		t.Errorf("an outsider sees %s", rec.Body)
	}

	rec = do(t, svc.HandleUpdate, as(http.MethodPut, kundenRule, "usr_member", "grp_sd"), map[string]string{"id": got.ID})
	if rec.Code != http.StatusForbidden {
		t.Errorf("a member updating someone else's folder = %d, want 403", rec.Code)
	}
	rec = do(t, svc.HandleDelete, as(http.MethodDelete, "", "usr_member", "grp_sd"), map[string]string{"id": got.ID})
	if rec.Code != http.StatusForbidden {
		t.Errorf("a member deleting someone else's folder = %d, want 403", rec.Code)
	}
}

// TestUnknownFolderIs404 covers both routes that address a folder by id.
func TestUnknownFolderIs404(t *testing.T) {
	svc, _ := newService(t)
	for name, h := range map[string]http.HandlerFunc{"update": svc.HandleUpdate, "delete": svc.HandleDelete} {
		body := ""
		method := http.MethodDelete
		if name == "update" {
			body, method = kundenRule, http.MethodPut
		}
		rec := do(t, h, as(method, body, "usr_me"), map[string]string{"id": "ffff"})
		if rec.Code != http.StatusNotFound {
			t.Errorf("%s unknown id = %d, want 404 (%s)", name, rec.Code, rec.Body)
		}
	}
}

// TestCreateRejectsBadRequests covers what the editor cannot send but a script
// can: a missing name, an over-long one, a group folder naming no group, an
// unknown visibility, a rule the catalogue does not describe, and broken JSON.
func TestCreateRejectsBadRequests(t *testing.T) {
	svc, _ := newService(t)
	cases := map[string]string{
		"no name":         `{"rule":{"match":"all","conditions":[]}}`,
		"blank name":      `{"name":"   "}`,
		"long name":       `{"name":"` + strings.Repeat("x", 61) + `"}`,
		"group unnamed":   `{"name":"n","visibility":"group"}`,
		"bad visibility":  `{"name":"n","visibility":"world"}`,
		"unknown field":   `{"name":"n","rule":{"match":"all","conditions":[{"field":"colour","op":"is","value":"red"}]}}`,
		"bad operator":    `{"name":"n","rule":{"match":"all","conditions":[{"field":"form","op":"contains","value":"x"}]}}`,
		"broken json":     `{"name":`,
		"unknown match":   `{"name":"n","rule":{"match":"either","conditions":[]}}`,
		"missing a value": `{"name":"n","rule":{"match":"all","conditions":[{"field":"process","op":"is"}]}}`,
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			rec := do(t, svc.HandleCreate, as(http.MethodPost, body, "usr_me"), nil)
			if rec.Code != http.StatusBadRequest {
				t.Errorf("create = %d, want 400 (%s)", rec.Code, rec.Body)
			}
		})
	}
}

// TestNewFoldersLandAtTheEnd covers the sidebar order a person expects: the
// folder they just made is below the ones they already had.
func TestNewFoldersLandAtTheEnd(t *testing.T) {
	svc, _ := newService(t)
	first := create(t, svc, `{"name":"A","rule":{"match":"all","conditions":[]}}`, "usr_me")
	second := create(t, svc, `{"name":"B","rule":{"match":"all","conditions":[]}}`, "usr_me")
	if second.Position <= first.Position {
		t.Errorf("positions %d then %d — a new folder did not land at the end", first.Position, second.Position)
	}
	explicit := create(t, svc, `{"name":"C","position":0,"rule":{"match":"all","conditions":[]}}`, "usr_me")
	if explicit.Position != 0 {
		t.Errorf("an explicit position was ignored: %d", explicit.Position)
	}
}

// TestCountsAreOneScan covers the sidebar badges: every visible folder answered
// from a single pass over the tasks.
func TestCountsAreOneScan(t *testing.T) {
	svc, _ := newService(t)
	kunden := create(t, svc, kundenRule, "usr_me")
	mine := create(t, svc, `{"name":"Meine","rule":{"match":"all","conditions":[`+
		`{"field":"assignee","op":"isMe"}]}}`, "usr_me")

	rec := do(t, svc.HandleCounts, as(http.MethodGet, "", "usr_me"), nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("counts = %d (%s)", rec.Code, rec.Body)
	}
	var out Counts
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if out.Total != len(tasks) {
		t.Errorf("total = %d, want %d", out.Total, len(tasks))
	}
	if out.Folders[kunden.ID] != 2 {
		t.Errorf("kunden-anfrage count = %d, want 2", out.Folders[kunden.ID])
	}
	if out.Folders[mine.ID] != 1 {
		t.Errorf("assigned-to-me count = %d, want 1", out.Folders[mine.ID])
	}
}

// TestCountsWithNoFoldersDoesNotScan proves the empty sidebar costs nothing: with
// no folders there is nothing to count, so the engine is not walked at all.
func TestCountsWithNoFoldersDoesNotScan(t *testing.T) {
	store, err := NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	quit := make(chan struct{})
	loop := runloop.New(quit)
	var wg sync.WaitGroup
	wg.Add(1)
	go func() { defer wg.Done(); loop.Run() }()
	t.Cleanup(func() { close(quit); wg.Wait() })

	scanned := false
	svc := New(loop, store, func(User) Options { return Options{} },
		func([]*Matcher, User) ([]int, int, bool, error) {
			scanned = true
			return nil, 0, false, nil
		}, NewID)
	rec := do(t, svc.HandleCounts, as(http.MethodGet, "", "usr_me"), nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("counts = %d", rec.Code)
	}
	if scanned {
		t.Error("an empty sidebar still scanned the task population")
	}
}

// TestPreviewAnswersAnUnsavedRule covers the editor's live counter, including the
// half-built rule that is the normal state of an open dialog.
func TestPreviewAnswersAnUnsavedRule(t *testing.T) {
	svc, _ := newService(t)
	body := `{"rule":{"match":"all","conditions":[{"field":"process","op":"is","value":"kunden-anfrage"}]}}`
	rec := do(t, svc.HandlePreview, as(http.MethodPost, body, "usr_me"), nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("preview = %d (%s)", rec.Code, rec.Body)
	}
	var out struct {
		OK      bool   `json:"ok"`
		FEEL    string `json:"feel"`
		Matched int    `json:"matched"`
		Total   int    `json:"total"`
		Error   string `json:"error"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if !out.OK || out.Matched != 2 || out.Total != len(tasks) {
		t.Errorf("preview = %+v", out)
	}
	if out.FEEL != `processId = "kunden-anfrage"` {
		t.Errorf("preview FEEL = %q", out.FEEL)
	}

	half := `{"rule":{"match":"all","conditions":[{"field":"process","op":"is"}]}}`
	rec = do(t, svc.HandlePreview, as(http.MethodPost, half, "usr_me"), nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("half-built preview = %d, want 200 with a reason", rec.Code)
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if out.OK || out.Error == "" {
		t.Errorf("half-built preview = %+v, want ok:false with a reason", out)
	}
}

// TestFieldsDescribesTheEditor covers the catalogue response the editor is drawn
// from, and the property that makes the console translatable: the server sends
// ids and directory data, never interface text.
func TestFieldsDescribesTheEditor(t *testing.T) {
	svc, _ := newService(t)
	rec := do(t, svc.HandleFields, as(http.MethodGet, "", "usr_me"), nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("fields = %d", rec.Code)
	}
	var out struct {
		Fields  []FieldSpec `json:"fields"`
		Options Options     `json:"options"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if len(out.Fields) != len(Catalog()) {
		t.Errorf("fields = %d, want %d", len(out.Fields), len(Catalog()))
	}
	if len(out.Options.Processes) != 1 || out.Options.Processes[0].Value != "kunden-anfrage" {
		t.Errorf("options = %+v", out.Options)
	}
	body := rec.Body.String()
	for _, word := range []string{"Prozess", "Process", "is one of", "ist eines von"} {
		if strings.Contains(body, word) {
			t.Errorf("the catalogue response carries interface text (%q); labels belong to the client", word)
		}
	}
}

// TestViewerFallsBackToTheTypedIdentity covers the server running without
// authentication: `?me=` names who "assigned to me" means, but owns nothing.
func TestViewerFallsBackToTheTypedIdentity(t *testing.T) {
	r := httptest.NewRequest(http.MethodGet, "/?me=patrick", nil)
	if v := Viewer(r); v.Name != "patrick" || v.ID != "" {
		t.Errorf("Viewer = %+v, want the typed identity as a name and no account id", v)
	}
	if got := owner(r); got != "" {
		t.Errorf("owner = %q; a display-only name must not own a folder", got)
	}
	auth := as(http.MethodGet, "", "usr_me", "grp_a")
	if v := Viewer(auth); v.ID != "usr_me" || v.Name != "me" || len(v.Groups) != 1 {
		t.Errorf("Viewer(authenticated) = %+v", v)
	}
	if got := owner(auth); got != "usr_me" {
		t.Errorf("owner(authenticated) = %q", got)
	}
}

// TestMatcherForHidesWhatTheViewerCannotSee covers the lookup the task listing
// uses: someone else's private folder is indistinguishable from no folder.
func TestMatcherForHidesWhatTheViewerCannotSee(t *testing.T) {
	svc, _ := newService(t)
	got := create(t, svc, kundenRule, "usr_owner")

	_, m, ok, err := svc.MatcherFor(got.ID, User{ID: "usr_owner"})
	if err != nil || !ok || m == nil {
		t.Fatalf("owner lookup: ok=%v err=%v", ok, err)
	}
	if _, _, ok, _ := svc.MatcherFor(got.ID, User{ID: "usr_other"}); ok {
		t.Error("a stranger resolved someone else's private folder")
	}
	if _, _, ok, _ := svc.MatcherFor("ffff", User{ID: "usr_owner"}); ok {
		t.Error("an unknown id resolved to a folder")
	}
}

// TestMatcherIsRecompiledAfterAnEdit covers the cache: it is keyed by the
// folder's own UpdatedAt, so an edit cannot be served by the previous rule.
func TestMatcherIsRecompiledAfterAnEdit(t *testing.T) {
	svc, _ := newService(t)
	got := create(t, svc, kundenRule, "usr_me")
	_, before, _, err := svc.MatcherFor(got.ID, User{ID: "usr_me"})
	if err != nil {
		t.Fatal(err)
	}
	if !before.Match(tasks[0], User{ID: "usr_me", Name: "me"}, time.Now()) {
		t.Fatal("the original rule does not match the task it names")
	}
	upd := `{"name":"Andere","rule":{"match":"all","conditions":[` +
		`{"field":"process","op":"is","value":"onboarding"}]}}`
	rec := do(t, svc.HandleUpdate, as(http.MethodPut, upd, "usr_me"), map[string]string{"id": got.ID})
	if rec.Code != http.StatusOK {
		t.Fatalf("update = %d (%s)", rec.Code, rec.Body)
	}
	_, after, _, err := svc.MatcherFor(got.ID, User{ID: "usr_me"})
	if err != nil {
		t.Fatal(err)
	}
	if after.Match(tasks[0], User{ID: "usr_me", Name: "me"}, time.Now()) {
		t.Error("the edited folder is still filtered by its previous rule")
	}
}

// TestVisibleSkipsAFolderThatNoLongerCompiles covers the sidebar's resilience: a
// record that predates a catalogue change is dropped from the listing rather than
// failing it, so one bad folder cannot empty somebody's sidebar.
func TestVisibleSkipsAFolderThatNoLongerCompiles(t *testing.T) {
	svc, store := newService(t)
	good := create(t, svc, kundenRule, "usr_me")
	if err := store.Save(Folder{
		ID: "dead", Name: "Von gestern", Owner: "usr_me", Visibility: VisibilityPrivate,
		Position: 99, Rule: Rule{Match: MatchAll, Conditions: []Condition{{Field: "colour", Op: OpIs, Value: "red"}}},
	}); err != nil {
		t.Fatal(err)
	}
	folders, matchers, err := svc.Visible(User{ID: "usr_me"})
	if err != nil {
		t.Fatalf("Visible: %v", err)
	}
	if len(folders) != 1 || len(matchers) != 1 || folders[0].ID != good.ID {
		t.Errorf("Visible returned %d folders, want only the compilable one", len(folders))
	}
}
