package pubinterface

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/pblumer/atlas/api/httpapi"
	"github.com/pblumer/atlas/api/runloop"
)

// fixture is the service over a real store and run loop, with the deployments, the
// application roles and the directory stood in by maps the test controls.
type fixture struct {
	t     *testing.T
	svc   *Service
	mux   *http.ServeMux
	auth  bool
	defs  map[uint64]Definition
	roles map[string]map[string]int // application -> user -> rank
	users map[string]bool
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	store, err := NewStore(filepath.Join(t.TempDir(), "published-interfaces"))
	if err != nil {
		t.Fatal(err)
	}
	quit := make(chan struct{})
	loop := runloop.New(quit)
	done := make(chan struct{})
	go func() { loop.Run(); close(done) }()
	t.Cleanup(func() { close(quit); <-done })
	f := &fixture{
		t: t, auth: true,
		defs: map[uint64]Definition{
			7: {Key: 7, ProcessID: "kredit", Version: 1, ApplicationID: "app_kredit", Process: compile(t, kreditBPMN)},
		},
		roles: map[string]map[string]int{"app_kredit": {"usr_owner": RankOwner, "usr_editor": RankEditor, "usr_viewer": RankViewer}},
		users: map[string]bool{"usr_owner": true, "usr_editor": true, "usr_viewer": true, "usr_peer": true, "usr_reader": true},
	}
	clock := int64(1762100000)
	f.svc = NewService(loop, store, Host{
		Definition: func(key uint64) (Definition, bool) { d, ok := f.defs[key]; return d, ok },
		Rank: func(p *httpapi.Principal, app string) (int, error) {
			if p == nil {
				return RankNone, nil
			}
			return f.roles[app][p.UserID], nil
		},
		UserExists:  func(id string) (bool, error) { return f.users[id], nil },
		GroupExists: func(id string) (bool, error) { return id == "grp_peers", nil },
		Names:       func(uint64) map[string]string { return map[string]string{"start": "Antrag eingegangen"} },
	}, func() bool { return f.auth }, func() int64 { clock++; return clock })
	f.mux = http.NewServeMux()
	for pattern, h := range map[string]http.HandlerFunc{
		"GET /api/v1/published-interfaces":                                      f.svc.HandleList,
		"GET /api/v1/published-interfaces/{id}":                                 f.svc.HandleGet,
		"GET /api/v1/published-interfaces/{id}/history":                         f.svc.HandleHistory,
		"GET /api/v1/published-interfaces/{id}/versions/{version}":              f.svc.HandleGetVersion,
		"POST /api/v1/published-interfaces/{id}/versions":                       f.svc.HandlePublish,
		"PUT /api/v1/published-interfaces/{id}/versions/{version}/binding":      f.svc.HandleRebind,
		"POST /api/v1/published-interfaces/{id}/versions/{version}/deprecation": f.svc.HandleDeprecate,
		"PUT /api/v1/published-interfaces/{id}/grants":                          f.svc.HandleSetGrants,
		"GET /api/v1/processes/{key}/interface-candidates":                      f.svc.HandleCandidates,
	} {
		f.mux.HandleFunc(pattern, h)
	}
	return f
}

// call sends one request as user (no principal when user is empty), from a session
// unless the user name ends in "-token".
func (f *fixture) call(user, method, path, body string) (int, map[string]any, string) {
	f.t.Helper()
	r := httptest.NewRequest(method, path, strings.NewReader(body))
	if user != "" {
		id := strings.TrimSuffix(user, "-token")
		p := &httpapi.Principal{UserID: id, Username: id, Session: !strings.HasSuffix(user, "-token")}
		if id == "usr_peer" {
			p.GroupIDs = []string{"grp_peers"}
		}
		r = r.WithContext(httpapi.WithPrincipal(r.Context(), p))
	}
	w := httptest.NewRecorder()
	f.mux.ServeHTTP(w, r)
	var out map[string]any
	_ = json.Unmarshal(w.Body.Bytes(), &out)
	return w.Code, out, w.Body.String()
}

const publishBody = `{"definitionKey":7,"expectedRevision":0,
 "entryPoints":[{"name":"antrag","elementId":"start"},{"name":"unterlagen","elementId":"unterlagen"}],
 "outcomes":[{"name":"bewilligt","elementId":"bescheid"},{"name":"abgelehnt","elementId":"abgelehnt"},
             {"name":"abgebrochen","elementIds":["endStorno","nachFehler"]}],
 "replies":["kredit.bescheid"]}`

func (f *fixture) publish() {
	f.t.Helper()
	if code, _, body := f.call("usr_editor", "POST", "/api/v1/published-interfaces/kreditpruefung/versions", publishBody); code != http.StatusOK {
		f.t.Fatalf("publish = %d %s", code, body)
	}
}

func TestPublishingIsAnEditorsActAndNeedsAuthentication(t *testing.T) {
	f := newFixture(t)
	f.auth = false
	if code, _, _ := f.call("", "POST", "/api/v1/published-interfaces/kreditpruefung/versions", publishBody); code != http.StatusForbidden {
		t.Fatalf("without authentication = %d, want 403", code)
	}
	f.auth = true
	if code, _, _ := f.call("usr_viewer", "POST", "/api/v1/published-interfaces/kreditpruefung/versions", publishBody); code != http.StatusForbidden {
		t.Fatalf("a viewer publishing = %d, want 403", code)
	}
	if code, _, _ := f.call("usr_reader", "POST", "/api/v1/published-interfaces/kreditpruefung/versions", publishBody); code != http.StatusNotFound {
		t.Fatalf("a stranger publishing = %d, want 404", code)
	}
	if code, _, _ := f.call("usr_editor", "POST", "/api/v1/published-interfaces/Kredit_Pruefung/versions", publishBody); code != http.StatusBadRequest {
		t.Fatalf("an unusable id = %d, want 400", code)
	}
	code, out, body := f.call("usr_editor", "POST", "/api/v1/published-interfaces/kreditpruefung/versions", publishBody)
	if code != http.StatusOK || out["version"] != float64(1) || out["revision"] != float64(1) {
		t.Fatalf("publish = %d %s", code, body)
	}
	if code, out, _ := f.call("usr_editor", "POST", "/api/v1/published-interfaces/kreditpruefung/versions",
		strings.Replace(publishBody, `"expectedRevision":0`, `"expectedRevision":1`, 1)); code != http.StatusConflict ||
		!strings.Contains(out["error"].(string), "rebind it instead") {
		t.Fatalf("the same contract again = %d %v", code, out)
	}
	if code, out, _ := f.call("usr_editor", "POST", "/api/v1/published-interfaces/kreditpruefung/versions", publishBody); code != http.StatusConflict ||
		out["currentRevision"] != float64(1) {
		t.Fatalf("a stale revision = %d %v", code, out)
	}
}

func TestAPublishRefusesACommitmentAndAnIncompleteSelection(t *testing.T) {
	f := newFixture(t)
	withCommitment := strings.Replace(publishBody, `{"name":"antrag","elementId":"start"}`,
		`{"name":"antrag","elementId":"start","commitment":{"within":"PT1H","scope":"internal"}}`, 1)
	code, out, _ := f.call("usr_editor", "POST", "/api/v1/published-interfaces/kreditpruefung/versions", withCommitment)
	if code != http.StatusBadRequest || !strings.Contains(out["error"].(string), "does not measure commitments yet") {
		t.Fatalf("a commitment = %d %v", code, out)
	}
	incomplete := `{"definitionKey":7,"expectedRevision":0,"entryPoints":[{"name":"antrag","elementId":"start"}]}`
	code, out, body := f.call("usr_editor", "POST", "/api/v1/published-interfaces/kreditpruefung/versions", incomplete)
	if code != http.StatusBadRequest || !strings.Contains(body, "every terminal outcome is named") {
		t.Fatalf("unnamed outcomes = %d %v", code, out)
	}
}

func TestAConsumerReadsTheContractAndNeverTheElements(t *testing.T) {
	f := newFixture(t)
	f.publish()
	// Nothing is visible to somebody outside the application before a grant.
	if _, out, _ := f.call("usr_reader", "GET", "/api/v1/published-interfaces", ""); out["total"] != float64(0) {
		t.Fatalf("a stranger lists %v", out)
	}
	if code, _, _ := f.call("usr_reader", "GET", "/api/v1/published-interfaces/kreditpruefung", ""); code != http.StatusNotFound {
		t.Fatalf("a stranger reads = %d, want 404", code)
	}
	grants := `{"public":false,"view":{"userIds":["usr_reader"]},"send":{"groupIds":["grp_peers"]},"expectedRevision":1}`
	if code, _, body := f.call("usr_owner", "PUT", "/api/v1/published-interfaces/kreditpruefung/grants", grants); code != http.StatusOK {
		t.Fatalf("grants = %d %s", code, body)
	}
	code, out, body := f.call("usr_reader", "GET", "/api/v1/published-interfaces/kreditpruefung", "")
	if code != http.StatusOK || out["mayView"] != true || out["maySend"] != false {
		t.Fatalf("a viewer reads = %d %s", code, body)
	}
	for _, internal := range []string{"elementIds", "kredit.unterlagen", `"binding"`, `"processId"`, `"grants"`, "usr_editor"} {
		if strings.Contains(body, internal) {
			t.Errorf("a consumer's view discloses %s: %s", internal, body)
		}
	}
	if !strings.Contains(body, `"name":"unterlagen"`) || !strings.Contains(body, `"class":"Unterlagen"`) {
		t.Fatalf("the contract is missing from %s", body)
	}
	// The peer may send but, holding no view grant, may not read.
	if code, _, _ := f.call("usr_peer", "GET", "/api/v1/published-interfaces/kreditpruefung", ""); code != http.StatusNotFound {
		t.Fatalf("a sender without view reads = %d, want 404", code)
	}
	if _, ok := f.svc.Store().MaySend("kreditpruefung", 1, Principal{UserID: "usr_peer", GroupIDs: []string{"grp_peers"}}); !ok {
		t.Fatal("the send grant is not answered")
	}
	// The application's own members read the binding.
	_, _, body = f.call("usr_viewer", "GET", "/api/v1/published-interfaces/kreditpruefung/versions/1", "")
	if !strings.Contains(body, `"elementIds":["unterlagen"]`) || !strings.Contains(body, "kredit.unterlagen") {
		t.Fatalf("a member's view lacks the binding: %s", body)
	}
}

func TestGrantsAreTheOwnersFromTheirOwnSession(t *testing.T) {
	f := newFixture(t)
	f.publish()
	grants := `{"public":true,"view":{"userIds":[]},"send":{"userIds":["usr_peer"]},"expectedRevision":1}`
	path := "/api/v1/published-interfaces/kreditpruefung/grants"
	if code, _, _ := f.call("usr_editor", "PUT", path, grants); code != http.StatusForbidden {
		t.Fatalf("an editor granting = %d, want 403", code)
	}
	if code, _, _ := f.call("usr_owner-token", "PUT", path, grants); code != http.StatusForbidden {
		t.Fatalf("an owner's token granting = %d, want 403", code)
	}
	if code, _, body := f.call("usr_owner", "PUT", path, strings.Replace(grants, "usr_peer", "usr_nobody", 1)); code != http.StatusBadRequest ||
		!strings.Contains(body, "no account with user id usr_nobody") {
		t.Fatalf("an unknown user = %d %s", code, body)
	}
	if code, out, body := f.call("usr_owner", "PUT", path, grants); code != http.StatusOK || out["public"] != true {
		t.Fatalf("grants = %d %s", code, body)
	}
	// Public lets anybody read; it never lets anybody send.
	if _, out, _ := f.call("usr_reader", "GET", "/api/v1/published-interfaces", ""); out["total"] != float64(1) {
		t.Fatalf("public interface not listed: %v", out)
	}
	if _, ok := f.svc.Store().MaySend("kreditpruefung", 1, Principal{UserID: "usr_reader"}); ok {
		t.Fatal("public implied send")
	}
	if code, _, _ := f.call("usr_editor", "GET", "/api/v1/published-interfaces/kreditpruefung/history", ""); code != http.StatusForbidden {
		t.Fatalf("an editor reading the audit = %d, want 403", code)
	}
	code, out, _ := f.call("usr_owner", "GET", "/api/v1/published-interfaces/kreditpruefung/history", "")
	items, _ := out["items"].([]any)
	if code != http.StatusOK || len(items) != 2 || items[0].(map[string]any)["action"] != ActionGrants ||
		items[0].(map[string]any)["actorId"] != "usr_owner" || items[1].(map[string]any)["action"] != ActionPublish {
		t.Fatalf("history = %d %v", code, out)
	}
}

// kreditV2 is the publisher's refactoring: the receive task's id changes, nothing a
// consumer binds to does.
var kreditV2 = strings.NewReplacer(`id="unterlagen"`, `id="dokumente"`, `targetRef="unterlagen"`, `targetRef="dokumente"`,
	`sourceRef="unterlagen"`, `sourceRef="dokumente"`).Replace(kreditBPMN)

func TestARefactoringRebindsAndAContractChangeDoesNot(t *testing.T) {
	f := newFixture(t)
	f.publish()
	f.defs[8] = Definition{Key: 8, ProcessID: "kredit", Version: 2, ApplicationID: "app_kredit", Process: compile(t, kreditV2)}
	path := "/api/v1/published-interfaces/kreditpruefung/versions/1/binding"
	code, out, body := f.call("usr_editor", "PUT", path, `{"definitionKey":8,"expectedRevision":1}`)
	if code != http.StatusConflict || !strings.Contains(body, `receives no message in this definition`) {
		t.Fatalf("rebinding with the old element ids = %d %v", code, out)
	}
	moved := `{"definitionKey":8,"expectedRevision":1,"entryPoints":[{"name":"antrag","elementId":"start"},{"name":"unterlagen","elementId":"dokumente"}]}`
	code, out, body = f.call("usr_editor", "PUT", path, moved)
	if code != http.StatusOK || !strings.Contains(body, `"definitionKey":8`) || !strings.Contains(body, `"elementIds":["dokumente"]`) {
		t.Fatalf("rebind = %d %s", code, body)
	}

	// A new correlation key is a new contract, and a rebinding says so.
	f.defs[9] = Definition{Key: 9, ProcessID: "kredit", Version: 3, ApplicationID: "app_kredit",
		Process: compile(t, strings.Replace(kreditBPMN, `<bpmn:message id="Msg_unterlagen" name="kredit.unterlagen">
    <bpmn:extensionElements><zeebe:subscription correlationKey="= antragId"/>`, `<bpmn:message id="Msg_unterlagen" name="kredit.unterlagen">
    <bpmn:extensionElements><zeebe:subscription correlationKey="= kundeId"/>`, 1))}
	code, out, body = f.call("usr_editor", "PUT", path,
		`{"definitionKey":9,"expectedRevision":2,"entryPoints":[{"name":"antrag","elementId":"start"},{"name":"unterlagen","elementId":"unterlagen"}]}`)
	if code != http.StatusConflict || !strings.Contains(body, "entry point unterlagen changes its correlation key") {
		t.Fatalf("a changed correlation key rebinds = %d %v", code, out)
	}
	f.defs[10] = Definition{Key: 10, ProcessID: "andere", Version: 1, ApplicationID: "app_kredit", Process: compile(t, kreditBPMN)}
	if code, _, _ := f.call("usr_editor", "PUT", path, `{"definitionKey":10,"expectedRevision":2}`); code != http.StatusConflict {
		t.Fatalf("rebinding onto another process = %d, want 409", code)
	}
}

func TestDeprecationAndDeactivationKeepTheContractLegible(t *testing.T) {
	f := newFixture(t)
	f.publish()
	path := "/api/v1/published-interfaces/kreditpruefung/versions/1/deprecation"
	if code, _, body := f.call("usr_editor", "POST", path, `{"note":"use version 2","expectedRevision":1}`); code != http.StatusOK ||
		!strings.Contains(body, `"deprecated":true`) || !strings.Contains(body, "use version 2") {
		t.Fatalf("deprecate = %d %s", code, body)
	}
	if code, _, _ := f.call("usr_editor", "POST", path, `{"expectedRevision":2}`); code != http.StatusConflict {
		t.Fatalf("deprecating twice = %d, want 409", code)
	}
	d := f.defs[7]
	d.Inactive = true
	f.defs[7] = d
	_, out, body := f.call("usr_viewer", "GET", "/api/v1/published-interfaces/kreditpruefung/versions/1", "")
	if out["available"] != false || !strings.Contains(body, UnavailableDefinitionInactive) || !strings.Contains(body, `"entryPoints"`) {
		t.Fatalf("an inactive definition's version = %s", body)
	}
	delete(f.defs, 7)
	_, out, body = f.call("usr_viewer", "GET", "/api/v1/published-interfaces", "")
	if !strings.Contains(body, UnavailableDefinitionMissing) || out["total"] != float64(1) {
		t.Fatalf("a deleted definition's interface = %s", body)
	}
}

func TestCandidatesAreReadByTheApplicationsViewers(t *testing.T) {
	f := newFixture(t)
	if code, _, _ := f.call("usr_reader", "GET", "/api/v1/processes/7/interface-candidates", ""); code != http.StatusNotFound {
		t.Fatalf("a stranger = %d, want 404", code)
	}
	code, out, body := f.call("usr_viewer", "GET", "/api/v1/processes/7/interface-candidates", "")
	if code != http.StatusOK || out["processId"] != "kredit" || !strings.Contains(body, `"elementName":"Antrag eingegangen"`) {
		t.Fatalf("candidates = %d %s", code, body)
	}
	if eps, _ := out["entryPoints"].([]any); len(eps) != 6 {
		t.Fatalf("entry points = %d, want 6: %s", len(eps), body)
	}
	f.publish()
	if _, out, _ := f.call("usr_viewer", "GET", "/api/v1/processes/7/interface-candidates", ""); strconv.Quote(out["interfaces"].([]any)[0].(string)) != `"kreditpruefung"` {
		t.Fatalf("published interfaces of the process = %v", out["interfaces"])
	}
}
