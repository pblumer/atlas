package api_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
)

// ADR-0127 Phase 2: publishing an application mints an *application release* — a
// design-time manifest of "what we shipped together", versioned by a per-application
// counter that sits above the ADR-0019 per-processId version. These tests pin the
// publish → release → history → deployments-view loop, including the all-or-nothing
// rule (a refused bundle mints no release) and durability across a restart.

// releasableBPMN is a straight-through executable process: it compiles and deploys,
// so a publish of an application holding it succeeds.
func releasableBPMN(id string) string {
	return fmt.Sprintf(`<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL">
  <process id="%s" isExecutable="true">
    <startEvent id="s"/>
    <endEvent id="e"/>
    <sequenceFlow id="f" sourceRef="s" targetRef="e"/>
  </process>
</definitions>`, id)
}

// brokenBPMN has no start event, which the compiler rejects — the lever for the
// "a refused bundle mints no release" case.
const brokenBPMN = `<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL">
  <process id="broken" isExecutable="true"><endEvent id="e"/></process>
</definitions>`

// mkApp creates an application and returns its id.
func mkApp(t *testing.T, ts *httptest.Server, name string) string {
	t.Helper()
	code, body := doReq(t, ts, http.MethodPost, "/api/v1/applications", `{"name":"`+name+`"}`, "application/json")
	if code != http.StatusOK {
		t.Fatalf("create application status=%d body=%s", code, body)
	}
	var a struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(body, &a); err != nil {
		t.Fatalf("decode application: %v", err)
	}
	return a.ID
}

// release is the JSON shape of one application release.
type release struct {
	ID            string `json:"id"`
	ApplicationID string `json:"applicationId"`
	Version       int32  `json:"version"`
	PublishedAt   int64  `json:"publishedAt"`
	Note          string `json:"note"`
	Members       []struct {
		Kind        string `json:"kind"`
		Ref         string `json:"ref"`
		ArtifactVer int32  `json:"artifactVer"`
	} `json:"members"`
}

// publishResult is the publish endpoint's response: the bundle-deploy outcome plus
// the release it minted.
type publishResult struct {
	Deployed    bool   `json:"deployed"`
	Reason      string `json:"reason"`
	Definitions []struct {
		ProcessID string `json:"processId"`
		Version   int32  `json:"version"`
	} `json:"definitions"`
	Release *release `json:"release"`
}

func publishApp(t *testing.T, ts *httptest.Server, appID, note string) (int, publishResult) {
	t.Helper()
	body := `{}`
	if note != "" {
		raw, _ := json.Marshal(map[string]string{"note": note})
		body = string(raw)
	}
	code, raw := doReq(t, ts, http.MethodPost, "/api/v1/applications/"+appID+"/publish", body, "application/json")
	var out publishResult
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("decode publish response: %v (%s)", err, raw)
	}
	return code, out
}

// TestPublishMintsVersionedReleases is the core loop: each successful publish mints
// the next application version and records the artifacts that shipped in it.
func TestPublishMintsVersionedReleases(t *testing.T) {
	ts := newTestServer(t)
	app := mkApp(t, ts, "Onboarding")

	// An application with one deployable draft.
	if code, body := doReq(t, ts, http.MethodPost, "/api/v1/drafts?projectId="+app, releasableBPMN("onboard"), "application/xml"); code != http.StatusOK {
		t.Fatalf("save draft status=%d body=%s", code, body)
	}

	// First publish → v1.
	code, res := publishApp(t, ts, app, "first cut")
	if code != http.StatusOK || !res.Deployed {
		t.Fatalf("publish status=%d deployed=%v reason=%q", code, res.Deployed, res.Reason)
	}
	if res.Release == nil || res.Release.Version != 1 {
		t.Fatalf("release = %+v, want version 1", res.Release)
	}
	if res.Release.ApplicationID != app || res.Release.Note != "first cut" {
		t.Fatalf("release = %+v, want applicationId=%s note=%q", res.Release, app, "first cut")
	}
	if res.Release.PublishedAt == 0 {
		t.Errorf("release has no publishedAt")
	}
	// It records the process that shipped, at its per-process version (ADR-0019).
	if len(res.Release.Members) != 1 || res.Release.Members[0].Ref != "onboard" {
		t.Fatalf("release members = %+v, want one member ref=onboard", res.Release.Members)
	}
	if res.Release.Members[0].Kind != "process" || res.Release.Members[0].ArtifactVer != 1 {
		t.Errorf("member = %+v, want kind=process artifactVer=1", res.Release.Members[0])
	}

	// Second publish → v2, while the per-process version also advances to 2.
	_, res2 := publishApp(t, ts, app, "")
	if res2.Release == nil || res2.Release.Version != 2 {
		t.Fatalf("second release = %+v, want version 2", res2.Release)
	}
	if len(res2.Release.Members) != 1 || res2.Release.Members[0].ArtifactVer != 2 {
		t.Errorf("second release member = %+v, want artifactVer=2", res2.Release.Members)
	}

	// History lists both, newest first.
	code, raw := doReq(t, ts, http.MethodGet, "/api/v1/applications/"+app+"/releases", "", "")
	if code != http.StatusOK {
		t.Fatalf("releases status=%d body=%s", code, raw)
	}
	var hist []release
	if err := json.Unmarshal(raw, &hist); err != nil {
		t.Fatalf("decode releases: %v (%s)", err, raw)
	}
	if len(hist) != 2 || hist[0].Version != 2 || hist[1].Version != 1 {
		t.Fatalf("history = %+v, want v2 then v1", hist)
	}
}

// TestRefusedPublishMintsNoRelease pins the all-or-nothing rule: a bundle that does
// not compile deploys nothing, so there is no release to record.
func TestRefusedPublishMintsNoRelease(t *testing.T) {
	ts := newTestServer(t)
	app := mkApp(t, ts, "Broken")

	if code, _ := doReq(t, ts, http.MethodPost, "/api/v1/drafts?projectId="+app, brokenBPMN, "application/xml"); code != http.StatusOK {
		t.Fatal("save broken draft")
	}

	code, res := publishApp(t, ts, app, "should not land")
	if code != http.StatusConflict {
		t.Fatalf("publish of a broken bundle status=%d, want 409 (body deployed=%v reason=%q)", code, res.Deployed, res.Reason)
	}
	if res.Deployed {
		t.Error("a broken bundle reported deployed=true")
	}
	if res.Release != nil {
		t.Errorf("a refused publish minted release %+v, want none", res.Release)
	}

	// And the history stays empty.
	_, raw := doReq(t, ts, http.MethodGet, "/api/v1/applications/"+app+"/releases", "", "")
	var hist []release
	if err := json.Unmarshal(raw, &hist); err != nil {
		t.Fatalf("decode releases: %v (%s)", err, raw)
	}
	if len(hist) != 0 {
		t.Errorf("history = %+v, want empty after a refused publish", hist)
	}
}

// TestApplicationDeploymentsView proves the per-application live view: what is
// deployed for this application right now, with its instance counts. This is the
// "deployed apps are visible inside the application" ask of ADR-0127.
func TestApplicationDeploymentsView(t *testing.T) {
	ts := newTestServer(t)
	app := mkApp(t, ts, "Live")

	if code, _ := doReq(t, ts, http.MethodPost, "/api/v1/drafts?projectId="+app, releasableBPMN("liveproc"), "application/xml"); code != http.StatusOK {
		t.Fatal("save draft")
	}
	if code, res := publishApp(t, ts, app, ""); code != http.StatusOK || !res.Deployed {
		t.Fatalf("publish status=%d", code)
	}

	code, raw := doReq(t, ts, http.MethodGet, "/api/v1/applications/"+app+"/deployments", "", "")
	if code != http.StatusOK {
		t.Fatalf("deployments status=%d body=%s", code, raw)
	}
	var view struct {
		ID          string `json:"id"`
		Name        string `json:"name"`
		Version     int32  `json:"version"`
		Running     int    `json:"running"`
		Finished    int    `json:"finished"`
		Definitions []struct {
			ProcessID string `json:"processId"`
			Version   int32  `json:"version"`
			Active    bool   `json:"active"`
			Running   int    `json:"running"`
		} `json:"definitions"`
	}
	if err := json.Unmarshal(raw, &view); err != nil {
		t.Fatalf("decode deployments view: %v (%s)", err, raw)
	}
	if view.ID != app || view.Name != "Live" {
		t.Errorf("view = %+v, want id=%s name=Live", view, app)
	}
	if view.Version != 1 {
		t.Errorf("view version = %d, want the published release version 1", view.Version)
	}
	if len(view.Definitions) != 1 || view.Definitions[0].ProcessID != "liveproc" {
		t.Fatalf("definitions = %+v, want one for liveproc", view.Definitions)
	}
	if !view.Definitions[0].Active {
		t.Error("freshly published definition reads as inactive")
	}
	if view.Running != 0 || view.Finished != 0 {
		t.Errorf("counts = running %d finished %d, want 0/0 with no instances started", view.Running, view.Finished)
	}
}

// TestReleasesSurviveRestart is the recovery test (first-class per AGENTS.md): the
// release history and the per-application version counter are rebuilt from disk, so
// the next publish after a restart continues the sequence instead of restarting it.
func TestReleasesSurviveRestart(t *testing.T) {
	dir := t.TempDir()

	first := boot(t, dir)
	app := mkApp(t, first.ts, "Durable")
	if code, _ := doReq(t, first.ts, http.MethodPost, "/api/v1/drafts?projectId="+app, releasableBPMN("dur"), "application/xml"); code != http.StatusOK {
		t.Fatal("save draft")
	}
	if code, res := publishApp(t, first.ts, app, "before restart"); code != http.StatusOK || res.Release.Version != 1 {
		t.Fatalf("first publish status=%d", code)
	}
	if code, res := publishApp(t, first.ts, app, ""); code != http.StatusOK || res.Release.Version != 2 {
		t.Fatalf("second publish did not reach v2 (status=%d)", code)
	}
	first.shutdown()

	second := boot(t, dir)
	defer second.shutdown()

	// History survived.
	code, raw := doReq(t, second.ts, http.MethodGet, "/api/v1/applications/"+app+"/releases", "", "")
	if code != http.StatusOK {
		t.Fatalf("releases after restart status=%d body=%s", code, raw)
	}
	var hist []release
	if err := json.Unmarshal(raw, &hist); err != nil {
		t.Fatalf("decode releases: %v (%s)", err, raw)
	}
	if len(hist) != 2 || hist[0].Version != 2 {
		t.Fatalf("history after restart = %+v, want 2 releases newest-first", hist)
	}
	if hist[1].Note != "before restart" {
		t.Errorf("release note not restored: %+v", hist[1])
	}

	// The counter continued: the next publish is v3, not v1.
	if code, res := publishApp(t, second.ts, app, ""); code != http.StatusOK || res.Release == nil || res.Release.Version != 3 {
		t.Fatalf("publish after restart = status %d release %+v, want version 3", code, res.Release)
	}
}

// TestDeletingApplicationClearsItsReleases pins the cleanup: a release is metadata
// *about* an application and is only reachable through its id, so once the
// application is gone the records would be unreachable dead weight. Deleting the
// application removes them, and a new application does not inherit the old
// counter — it starts at v1.
func TestDeletingApplicationClearsItsReleases(t *testing.T) {
	ts := newTestServer(t)
	app := mkApp(t, ts, "Transient")
	if code, _ := doReq(t, ts, http.MethodPost, "/api/v1/drafts?projectId="+app, releasableBPMN("tmp"), "application/xml"); code != http.StatusOK {
		t.Fatal("save draft")
	}
	if code, res := publishApp(t, ts, app, ""); code != http.StatusOK || res.Release.Version != 1 {
		t.Fatalf("publish status=%d", code)
	}

	if code, _ := doReq(t, ts, http.MethodDelete, "/api/v1/applications/"+app, "", ""); code != http.StatusNoContent {
		t.Fatal("delete application")
	}
	// The history is unreachable (the application 404s) and the records are gone:
	// a fresh application publishing its own draft starts at v1.
	app2 := mkApp(t, ts, "Successor")
	if code, _ := doReq(t, ts, http.MethodPost, "/api/v1/drafts?projectId="+app2, releasableBPMN("tmp2"), "application/xml"); code != http.StatusOK {
		t.Fatal("save second draft")
	}
	if code, res := publishApp(t, ts, app2, ""); code != http.StatusOK || res.Release == nil || res.Release.Version != 1 {
		t.Fatalf("successor publish = status %d release %+v, want version 1", code, res.Release)
	}
}

// TestPublishUnknownApplication is the 404 path.
func TestPublishUnknownApplication(t *testing.T) {
	ts := newTestServer(t)
	if code, _ := doReq(t, ts, http.MethodPost, "/api/v1/applications/nope/publish", `{}`, "application/json"); code != http.StatusNotFound {
		t.Error("publish of an unknown application: want 404")
	}
	if code, _ := doReq(t, ts, http.MethodGet, "/api/v1/applications/nope/releases", "", ""); code != http.StatusNotFound {
		t.Error("releases of an unknown application: want 404")
	}
	if code, _ := doReq(t, ts, http.MethodGet, "/api/v1/applications/nope/deployments", "", ""); code != http.StatusNotFound {
		t.Error("deployments of an unknown application: want 404")
	}
}
