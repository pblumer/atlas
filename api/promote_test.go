package api_test

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
)

// Deployment targets and promotion (ADR-0129, sending half). Publishing makes a
// release *here*; promoting puts that same frozen release on another Atlas. These
// tests drive a real promotion against a stand-in remote, so the wire format, the
// credential handling, the learned binding, and the per-target failure reporting
// are all exercised rather than assumed.

// fakeRemote is a stand-in peer Atlas: it records what arrived at its import
// endpoint and replies as the real one would.
type fakeRemote struct {
	*httptest.Server
	gotAuth   string
	gotBundle importBundleReceived
	status    int
	reply     string
}

type importBundleReceived struct {
	Application string `json:"application"`
	Release     struct {
		Version int32  `json:"version"`
		Note    string `json:"note"`
	} `json:"release"`
	Artifacts []struct {
		Kind      string   `json:"kind"`
		ProcessID string   `json:"processId"`
		XML       string   `json:"xml"`
		DMNXMLs   []string `json:"dmnXmls"`
	} `json:"artifacts"`
}

func newFakeRemote(t *testing.T) *fakeRemote {
	t.Helper()
	f := &fakeRemote{status: http.StatusOK, reply: `{"applicationId":"remote-app-1","imported":true}`}
	f.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/applications/import" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		f.gotAuth = r.Header.Get("Authorization")
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &f.gotBundle)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(f.status)
		_, _ = w.Write([]byte(f.reply))
	}))
	t.Cleanup(f.Close)
	return f
}

// mkTarget registers a deployment target pointing at a stand-in remote.
func mkTarget(t *testing.T, ts *httptest.Server, name, baseURL, credRef string) string {
	t.Helper()
	body, _ := json.Marshal(map[string]string{"name": name, "baseUrl": baseURL, "credentialRef": credRef})
	code, raw := doReq(t, ts, http.MethodPost, "/api/v1/targets", string(body), "application/json")
	if code != http.StatusOK {
		t.Fatalf("create target = %d body=%s", code, raw)
	}
	var out struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("decode target: %v (%s)", err, raw)
	}
	return out.ID
}

type promoteResult struct {
	Version int32 `json:"version"`
	Results []struct {
		TargetID     string `json:"targetId"`
		TargetName   string `json:"targetName"`
		OK           bool   `json:"ok"`
		Error        string `json:"error"`
		RemoteAppID  string `json:"remoteApplicationId"`
		RemoteStatus int    `json:"remoteStatus"`
	} `json:"results"`
}

func promote(t *testing.T, ts *httptest.Server, appID string, version int32, targetIDs ...string) (int, promoteResult) {
	t.Helper()
	body, _ := json.Marshal(map[string]any{"targetIds": targetIDs})
	path := "/api/v1/applications/" + appID + "/releases/" + strconv.Itoa(int(version)) + "/promote"
	code, raw := doReq(t, ts, http.MethodPost, path, string(body), "application/json")
	var out promoteResult
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("decode promote response: %v (%s)", err, raw)
	}
	return code, out
}

// seedPublishedApp creates an application with one deployable draft and publishes
// it, returning the application id.
func seedPublishedApp(t *testing.T, ts *httptest.Server, name, processID string) string {
	t.Helper()
	app := mkApp(t, ts, name)
	if code, _ := doReq(t, ts, http.MethodPost, "/api/v1/drafts?projectId="+app, releasableBPMN(processID), "application/xml"); code != http.StatusOK {
		t.Fatal("save draft")
	}
	if code, res := publishApp(t, ts, app, "initial"); code != http.StatusOK || !res.Deployed {
		t.Fatalf("publish = %d", code)
	}
	return app
}

// TestPromoteSendsTheFrozenReleaseToATarget is the core sending contract: the
// bundle carries the release's own version and the exact artifacts that shipped,
// authenticated with the target's credential.
func TestPromoteSendsTheFrozenReleaseToATarget(t *testing.T) {
	ts := newTestServer(t)
	remote := newFakeRemote(t)

	// The credential lives in the vault; the target holds only a reference to it.
	if code, body := doReq(t, ts, http.MethodPut, "/api/v1/secrets/prod-token", `{"value":"atlasdt_secret123"}`, "application/json"); code != http.StatusOK && code != http.StatusNoContent {
		t.Fatalf("store secret = %d body=%s", code, body)
	}
	app := seedPublishedApp(t, ts, "Onboarding", "onboard")
	target := mkTarget(t, ts, "Produktion", remote.URL, "prod-token")

	code, res := promote(t, ts, app, 1, target)
	if code != http.StatusOK {
		t.Fatalf("promote = %d results=%+v", code, res.Results)
	}
	if len(res.Results) != 1 || !res.Results[0].OK {
		t.Fatalf("promote results = %+v, want one success", res.Results)
	}
	if res.Results[0].RemoteAppID != "remote-app-1" {
		t.Errorf("remote application id = %q, want the id the remote reported", res.Results[0].RemoteAppID)
	}

	// The remote received the release, under its own version, with the artifacts.
	if remote.gotBundle.Application != "Onboarding" {
		t.Errorf("remote got application %q, want Onboarding", remote.gotBundle.Application)
	}
	if remote.gotBundle.Release.Version != 1 {
		t.Errorf("remote got version %d, want the publisher's 1", remote.gotBundle.Release.Version)
	}
	if len(remote.gotBundle.Artifacts) != 1 || remote.gotBundle.Artifacts[0].ProcessID != "onboard" {
		t.Fatalf("remote got artifacts %+v, want one for onboard", remote.gotBundle.Artifacts)
	}
	if !strings.Contains(remote.gotBundle.Artifacts[0].XML, "onboard") {
		t.Error("remote got no BPMN XML for the artifact")
	}
	// The credential was presented as a bearer, resolved from the vault.
	if remote.gotAuth != "Bearer atlasdt_secret123" {
		t.Errorf("remote saw Authorization %q, want the vault-resolved deploy token", remote.gotAuth)
	}
}

// TestPromoteRecordsTheRemoteBinding proves the publisher learns and keeps the
// remote's application id, so later status reads can address it (ADR-0129 C1).
func TestPromoteRecordsTheRemoteBinding(t *testing.T) {
	ts := newTestServer(t)
	remote := newFakeRemote(t)
	app := seedPublishedApp(t, ts, "Bound", "bnd")
	target := mkTarget(t, ts, "Test", remote.URL, "")

	if code, res := promote(t, ts, app, 1, target); code != http.StatusOK || !res.Results[0].OK {
		t.Fatalf("promote = %d %+v", code, res.Results)
	}

	_, raw := doReq(t, ts, http.MethodGet, "/api/v1/targets", "", "")
	var targets []struct {
		ID       string            `json:"id"`
		Bindings map[string]string `json:"bindings"`
	}
	if err := json.Unmarshal(raw, &targets); err != nil {
		t.Fatalf("decode targets: %v (%s)", err, raw)
	}
	for _, tg := range targets {
		if tg.ID == target {
			if tg.Bindings[app] != "remote-app-1" {
				t.Fatalf("binding for %s = %q, want remote-app-1", app, tg.Bindings[app])
			}
			return
		}
	}
	t.Fatal("target not listed after promotion")
}

// TestPromoteReportsPerTargetFailure pins the non-atomic rule: one target failing
// does not hide another succeeding, and the failure carries the remote's reason.
func TestPromoteReportsPerTargetFailure(t *testing.T) {
	ts := newTestServer(t)
	good := newFakeRemote(t)
	bad := newFakeRemote(t)
	bad.status = http.StatusConflict
	bad.reply = `{"imported":false,"reason":"release v1 is already imported"}`

	app := seedPublishedApp(t, ts, "Split", "split")
	okTarget := mkTarget(t, ts, "Good", good.URL, "")
	badTarget := mkTarget(t, ts, "Bad", bad.URL, "")

	code, res := promote(t, ts, app, 1, okTarget, badTarget)
	if code != http.StatusOK {
		t.Fatalf("promote = %d, want 200 with per-target results", code)
	}
	if len(res.Results) != 2 {
		t.Fatalf("results = %+v, want one per target", res.Results)
	}
	byName := map[string]bool{}
	for _, r := range res.Results {
		byName[r.TargetName] = r.OK
		if r.TargetName == "Bad" {
			if r.OK {
				t.Error("a refusing target reported ok")
			}
			if !strings.Contains(r.Error, "already imported") {
				t.Errorf("failure reason = %q, want the remote's reason", r.Error)
			}
			if r.RemoteStatus != http.StatusConflict {
				t.Errorf("remote status = %d, want 409", r.RemoteStatus)
			}
		}
	}
	if !byName["Good"] {
		t.Error("the reachable target did not succeed alongside the failing one")
	}
}

// TestPromoteUnreachableTargetIsReported covers the network-failure path: an
// unreachable peer is a reported per-target failure, not a 500 for the whole call.
func TestPromoteUnreachableTargetIsReported(t *testing.T) {
	ts := newTestServer(t)
	dead := newFakeRemote(t)
	deadURL := dead.URL
	dead.Close() // nothing listening now

	app := seedPublishedApp(t, ts, "Offline", "off")
	target := mkTarget(t, ts, "Down", deadURL, "")

	code, res := promote(t, ts, app, 1, target)
	if code != http.StatusOK {
		t.Fatalf("promote against a dead target = %d, want 200 with a reported failure", code)
	}
	if len(res.Results) != 1 || res.Results[0].OK || res.Results[0].Error == "" {
		t.Fatalf("results = %+v, want one reported failure", res.Results)
	}
}

// TestPromoteBadRequests covers the 404/400 paths a caller can hit.
func TestPromoteBadRequests(t *testing.T) {
	ts := newTestServer(t)
	remote := newFakeRemote(t)
	app := seedPublishedApp(t, ts, "Edgy", "edgy")
	target := mkTarget(t, ts, "T", remote.URL, "")

	// Unknown application.
	if code, _ := doReq(t, ts, http.MethodPost, "/api/v1/applications/nope/releases/1/promote",
		`{"targetIds":["`+target+`"]}`, "application/json"); code != http.StatusNotFound {
		t.Error("promote of an unknown application: want 404")
	}
	// Unknown release version.
	if code, _ := doReq(t, ts, http.MethodPost, "/api/v1/applications/"+app+"/releases/9/promote",
		`{"targetIds":["`+target+`"]}`, "application/json"); code != http.StatusNotFound {
		t.Error("promote of an unknown release: want 404")
	}
	// No targets named.
	if code, _ := doReq(t, ts, http.MethodPost, "/api/v1/applications/"+app+"/releases/1/promote",
		`{"targetIds":[]}`, "application/json"); code != http.StatusBadRequest {
		t.Error("promote with no targets: want 400")
	}
	// Unknown target.
	if code, _ := doReq(t, ts, http.MethodPost, "/api/v1/applications/"+app+"/releases/1/promote",
		`{"targetIds":["ghost"]}`, "application/json"); code != http.StatusBadRequest {
		t.Error("promote to an unknown target: want 400")
	}
}

// TestPromoteTwiceKeepsTheBinding covers the re-promotion path: the binding is
// already what the remote reports, so nothing is rewritten.
func TestPromoteTwiceKeepsTheBinding(t *testing.T) {
	ts := newTestServer(t)
	remote := newFakeRemote(t)
	app := seedPublishedApp(t, ts, "Twice", "twice")
	target := mkTarget(t, ts, "T", remote.URL, "")

	for i := range 2 {
		if code, res := promote(t, ts, app, 1, target); code != http.StatusOK || !res.Results[0].OK {
			t.Fatalf("promotion %d = %d %+v", i+1, code, res.Results)
		}
	}
	_, raw := doReq(t, ts, http.MethodGet, "/api/v1/targets", "", "")
	if !strings.Contains(string(raw), "remote-app-1") {
		t.Fatalf("binding lost after re-promotion: %s", raw)
	}
}

// TestPromoteReleaseWhoseDefinitionIsGone covers the honest failure when a release
// points at a definition that has since been deleted: the promotion reports it
// rather than shipping a bundle missing an artifact.
func TestPromoteReleaseWhoseDefinitionIsGone(t *testing.T) {
	ts := newTestServer(t)
	remote := newFakeRemote(t)
	app := seedPublishedApp(t, ts, "Vanished", "vanish")
	target := mkTarget(t, ts, "T", remote.URL, "")

	// Find and delete the deployed definition the release points at.
	_, raw := doReq(t, ts, http.MethodGet, "/api/v1/processes", "", "")
	var procs []struct {
		Key       uint64 `json:"key"`
		ProcessID string `json:"processId"`
	}
	if err := json.Unmarshal(raw, &procs); err != nil {
		t.Fatalf("decode processes: %v", err)
	}
	for _, p := range procs {
		if p.ProcessID == "vanish" {
			if code, b := doReq(t, ts, http.MethodDelete, "/api/v1/processes/"+strconv.FormatUint(p.Key, 10), "", ""); code != http.StatusNoContent {
				t.Fatalf("delete definition = %d body=%s", code, b)
			}
		}
	}

	if code, _ := doReq(t, ts, http.MethodPost, "/api/v1/applications/"+app+"/releases/1/promote",
		`{"targetIds":["`+target+`"]}`, "application/json"); code != http.StatusInternalServerError {
		t.Errorf("promote of a release whose definition is gone = %d, want 500 with a clear reason", code)
	}
}

// TestTargetManagementIsAdminOnly pins that registering where this server may ship
// work is an administrator's act, while listing stays open to the editors who have
// to pick a target.
func TestTargetManagementIsAdminOnly(t *testing.T) {
	ts, _ := newAuthServer(t, "admin", "password1")
	admin := newClient(t)
	if code := login(t, admin, ts, "admin", "password1"); code != http.StatusOK {
		t.Fatalf("admin login = %d", code)
	}
	// A modeller, not an administrator: promoting a release is an authoring act, and
	// this test is about the two management calls that are not.
	if code, body := cReq(t, admin, ts, http.MethodPost, "/api/v1/users",
		`{"username":"eve","password":"password2","roles":["modeler"]}`); code != http.StatusCreated {
		t.Fatalf("create user = %d body=%s", code, body)
	}
	eve := newClient(t)
	if code := login(t, eve, ts, "eve", "password2"); code != http.StatusOK {
		t.Fatalf("eve login = %d", code)
	}

	if code, _ := cReq(t, eve, ts, http.MethodPost, "/api/v1/targets",
		`{"name":"Sneaky","baseUrl":"https://evil.example"}`); code != http.StatusForbidden {
		t.Errorf("non-admin creating a target = %d, want 403", code)
	}
	if code, _ := cReq(t, eve, ts, http.MethodDelete, "/api/v1/targets/whatever", ""); code != http.StatusForbidden {
		t.Errorf("non-admin deleting a target = %d, want 403", code)
	}
	// Listing is open: an editor must see the targets to promote to one.
	if code, _ := cReq(t, eve, ts, http.MethodGet, "/api/v1/targets", ""); code != http.StatusOK {
		t.Errorf("non-admin listing targets = %d, want 200", code)
	}
}

// TestPromoteWithABrokenTargetStore covers the store-failure branch of the resolve
// phase.
func TestPromoteWithABrokenTargetStore(t *testing.T) {
	ts := newTestServer(t)
	remote := newFakeRemote(t)
	app := seedPublishedApp(t, ts, "Fragile", "frag")
	target := mkTarget(t, ts, "T", remote.URL, "")
	// Point the whole data dir's target store at nothing by deleting the directory
	// out from under it, which is what a failed disk looks like from here.
	if code, _ := doReq(t, ts, http.MethodDelete, "/api/v1/targets/"+target, "", ""); code != http.StatusNoContent {
		t.Fatal("delete target")
	}
	// The target is gone, so the promotion names an unknown target: a 400, not a
	// half-completed shipment.
	if code, _ := doReq(t, ts, http.MethodPost, "/api/v1/applications/"+app+"/releases/1/promote",
		`{"targetIds":["`+target+`"]}`, "application/json"); code != http.StatusBadRequest {
		t.Error("promote to a deleted target: want 400")
	}
}

// TestPromoteBetweenTwoRealServers is the whole ADR-0129 feature end to end, with
// no stand-ins: server B mints a deploy token, server A registers B as a target and
// promotes a release to it, and B really compiles, deploys, and records it. This is
// the test that proves the two halves fit together — everything else checks one
// side against an assumption about the other.
func TestPromoteBetweenTwoRealServers(t *testing.T) {
	// B, the receiver, with auth on so the deploy token actually gates.
	receiver, _ := newAuthServer(t, "admin", "password1")
	adminB := newClient(t)
	if code := login(t, adminB, receiver, "admin", "password1"); code != http.StatusOK {
		t.Fatalf("receiver login = %d", code)
	}
	_, secret := mintToken(t, adminB, receiver, "From A")

	// A, the publisher: hold B's credential in the vault, register B as a target.
	sender := newTestServer(t)
	if code, body := doReq(t, sender, http.MethodPut, "/api/v1/secrets/b-token",
		`{"value":"`+secret+`"}`, "application/json"); code != http.StatusOK && code != http.StatusNoContent {
		t.Fatalf("store credential = %d body=%s", code, body)
	}
	app := seedPublishedApp(t, sender, "Cross-Server", "crossproc")
	target := mkTarget(t, sender, "Receiver", receiver.URL, "b-token")

	code, res := promote(t, sender, app, 1, target)
	if code != http.StatusOK {
		t.Fatalf("promote = %d results=%+v", code, res.Results)
	}
	if len(res.Results) != 1 || !res.Results[0].OK {
		t.Fatalf("promote results = %+v, want one success", res.Results)
	}
	remoteAppID := res.Results[0].RemoteAppID
	if remoteAppID == "" {
		t.Fatal("the receiver reported no application id")
	}

	// B really has it: the application exists there, at the publisher's version,
	// with the process deployed and startable.
	code, raw := cReq(t, adminB, receiver, http.MethodGet, "/api/v1/applications/"+remoteAppID+"/deployments", "")
	if code != http.StatusOK {
		t.Fatalf("receiver deployments = %d body=%s", code, raw)
	}
	var view struct {
		Name        string `json:"name"`
		Version     int32  `json:"version"`
		Definitions []struct {
			ProcessID string `json:"processId"`
			Current   bool   `json:"current"`
		} `json:"definitions"`
	}
	if err := json.Unmarshal(raw, &view); err != nil {
		t.Fatalf("decode receiver deployments: %v (%s)", err, raw)
	}
	if view.Name != "Cross-Server" || view.Version != 1 {
		t.Fatalf("receiver view = %+v, want Cross-Server at the publisher's v1", view)
	}
	if len(view.Definitions) != 1 || view.Definitions[0].ProcessID != "crossproc" || !view.Definitions[0].Current {
		t.Fatalf("receiver definitions = %+v, want crossproc as current", view.Definitions)
	}

	// Promoting the same release again is refused by the receiver, not silently
	// re-deployed — and the sender reports that refusal per target.
	_, again := promote(t, sender, app, 1, target)
	if len(again.Results) != 1 || again.Results[0].OK {
		t.Fatalf("re-promotion = %+v, want a reported refusal", again.Results)
	}
	if !strings.Contains(again.Results[0].Error, "already imported") {
		t.Errorf("refusal reason = %q, want the receiver's reason", again.Results[0].Error)
	}
}

// TestPromoteAReleaseWithNoArtifacts covers a real case rather than a contrived
// one: publishing an application that holds no drafts still mints a release, and
// promoting that release has nothing to ship. It is refused with a reason instead
// of sending an empty bundle a peer would reject as malformed.
func TestPromoteAReleaseWithNoArtifacts(t *testing.T) {
	ts := newTestServer(t)
	remote := newFakeRemote(t)
	app := mkApp(t, ts, "Hollow")
	if code, res := publishApp(t, ts, app, ""); code != http.StatusOK || res.Release == nil {
		t.Fatalf("publish of an empty application = %d, want a release", code)
	}
	target := mkTarget(t, ts, "T", remote.URL, "")

	if code, _ := doReq(t, ts, http.MethodPost, "/api/v1/applications/"+app+"/releases/1/promote",
		`{"targetIds":["`+target+`"]}`, "application/json"); code != http.StatusConflict {
		t.Errorf("promote of an artifact-less release = %d, want 409", code)
	}
}

// TestPromoteRequiresEditorRole pins the authorization gate: promoting ships an
// application's work to another server, so it needs the same editor role that
// publishing does — a viewer may look but not ship (ADR-0071).
func TestPromoteRequiresEditorRole(t *testing.T) {
	ts, _ := newAuthServer(t, "admin", "password1")
	admin := newClient(t)
	if code := login(t, admin, ts, "admin", "password1"); code != http.StatusOK {
		t.Fatalf("admin login = %d", code)
	}
	if code, body := cReq(t, admin, ts, http.MethodPost, "/api/v1/users",
		`{"username":"vic","password":"password2","roles":[]}`); code != http.StatusCreated {
		t.Fatalf("create user = %d body=%s", code, body)
	}

	// The admin owns an application with a release, and a target to ship it to.
	code, raw := cReq(t, admin, ts, http.MethodPost, "/api/v1/applications", `{"name":"Guarded"}`)
	if code != http.StatusOK {
		t.Fatalf("create application = %d body=%s", code, raw)
	}
	var app struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(raw, &app); err != nil {
		t.Fatalf("decode application: %v", err)
	}

	vic := newClient(t)
	if code := login(t, vic, ts, "vic", "password2"); code != http.StatusOK {
		t.Fatalf("vic login = %d", code)
	}
	// A private application is invisible to a non-member, so promoting reports 404
	// rather than confirming it exists — the same information hiding the rest of the
	// scoped surface uses.
	code, _ = cReq(t, vic, ts, http.MethodPost, "/api/v1/applications/"+app.ID+"/releases/1/promote",
		`{"targetIds":["whatever"]}`)
	if code != http.StatusNotFound && code != http.StatusForbidden {
		t.Errorf("promote by a non-member = %d, want 404 or 403", code)
	}
}

// TestTargetLifecycle covers target CRUD and the rule that a target never returns
// anything a credential could be recovered from.
func TestTargetLifecycle(t *testing.T) {
	ts := newTestServer(t)
	id := mkTarget(t, ts, "Produktion", "https://atlas.prod.example", "prod-token")

	code, raw := doReq(t, ts, http.MethodGet, "/api/v1/targets", "", "")
	if code != http.StatusOK {
		t.Fatalf("list targets = %d", code)
	}
	if !strings.Contains(string(raw), "Produktion") || !strings.Contains(string(raw), "atlas.prod.example") {
		t.Fatalf("listing does not show the target: %s", raw)
	}
	// The reference name may show; a resolved secret value must never appear.
	if strings.Contains(string(raw), "atlasdt_") {
		t.Fatalf("listing leaked a credential value: %s", raw)
	}

	if code, _ := doReq(t, ts, http.MethodDelete, "/api/v1/targets/"+id, "", ""); code != http.StatusNoContent {
		t.Fatalf("delete target = %d", code)
	}
	if _, raw := doReq(t, ts, http.MethodGet, "/api/v1/targets", "", ""); strings.Contains(string(raw), id) {
		t.Fatalf("deleted target still listed: %s", raw)
	}
}

// TestTargetRequiresHTTPSOrExplicitLocal pins the transport rule: a target is a
// trust relationship, so a plaintext http:// base URL is refused unless it is
// loopback (the shape tests and single-host setups need).
func TestTargetRejectsPlaintextRemote(t *testing.T) {
	ts := newTestServer(t)
	body, _ := json.Marshal(map[string]string{"name": "Insecure", "baseUrl": "http://elsewhere.example"})
	if code, _ := doReq(t, ts, http.MethodPost, "/api/v1/targets", string(body), "application/json"); code != http.StatusBadRequest {
		t.Error("plaintext non-loopback target: want 400")
	}
	for _, bad := range []string{"", "not a url", "ftp://x", "https://"} {
		b, _ := json.Marshal(map[string]string{"name": "X", "baseUrl": bad})
		if code, _ := doReq(t, ts, http.MethodPost, "/api/v1/targets", string(b), "application/json"); code != http.StatusBadRequest {
			t.Errorf("baseUrl %q: want 400", bad)
		}
	}
}
