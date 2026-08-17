package api_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
)

// Bundle import (ADR-0129): the receiving half of publishing to another Atlas.
// These drive the endpoint directly (auth off, so no token is needed — the
// credential path is covered in deploytokens_test.go) and pin the contract a
// publisher depends on: the application is created on first import, the
// publisher's release version travels with the bundle, a version already held is
// refused rather than overwritten, and a bundle that does not compile lands
// nothing.

func importBody(app string, version int32, note string, processIDs ...string) string {
	arts := make([]map[string]any, 0, len(processIDs))
	for _, pid := range processIDs {
		arts = append(arts, map[string]any{
			"kind": "process", "processId": pid, "xml": releasableBPMN(pid),
		})
	}
	raw, _ := json.Marshal(map[string]any{
		"application": app,
		"release":     map[string]any{"version": version, "note": note},
		"artifacts":   arts,
	})
	return string(raw)
}

type importResult struct {
	ApplicationID string `json:"applicationId"`
	Application   string `json:"application"`
	Imported      bool   `json:"imported"`
	Reason        string `json:"reason"`
	Definitions   []struct {
		ProcessID string `json:"processId"`
		Version   int32  `json:"version"`
	} `json:"definitions"`
	Release *struct {
		Version int32  `json:"version"`
		Note    string `json:"note"`
	} `json:"release"`
}

func importBundle(t *testing.T, ts *httptest.Server, body string) (int, importResult) {
	t.Helper()
	code, raw := doReq(t, ts, http.MethodPost, "/api/v1/applications/import", body, "application/json")
	var out importResult
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("decode import response: %v (%s)", err, raw)
	}
	return code, out
}

// TestImportCreatesApplicationAndKeepsPublisherVersion is the core contract: the
// receiving server creates the application on first contact and adopts the
// publisher's release number rather than minting its own, so "v5" means the same
// artifacts on both servers.
func TestImportCreatesApplicationAndKeepsPublisherVersion(t *testing.T) {
	ts := newTestServer(t)

	code, res := importBundle(t, ts, importBody("Onboarding", 5, "from Test", "onboard"))
	if code != http.StatusOK || !res.Imported {
		t.Fatalf("import = %d imported=%v reason=%q", code, res.Imported, res.Reason)
	}
	if res.ApplicationID == "" || res.Application != "Onboarding" {
		t.Fatalf("import result = %+v, want a created application named Onboarding", res)
	}
	if res.Release == nil || res.Release.Version != 5 {
		t.Fatalf("release = %+v, want the publisher's v5, not a locally minted number", res.Release)
	}
	if res.Release.Note != "from Test" {
		t.Errorf("release note = %q, want the publisher's note", res.Release.Note)
	}
	if len(res.Definitions) != 1 || res.Definitions[0].ProcessID != "onboard" {
		t.Fatalf("definitions = %+v, want one for onboard", res.Definitions)
	}

	// The application now exists locally and reports the imported release.
	_, raw := doReq(t, ts, http.MethodGet, "/api/v1/applications/"+res.ApplicationID+"/deployments", "", "")
	var view struct {
		Name    string `json:"name"`
		Version int32  `json:"version"`
	}
	if err := json.Unmarshal(raw, &view); err != nil {
		t.Fatalf("decode deployments: %v (%s)", err, raw)
	}
	if view.Name != "Onboarding" || view.Version != 5 {
		t.Fatalf("deployments view = %+v, want Onboarding at v5", view)
	}
}

// TestImportReusesTheExistingApplication proves a second import lands in the same
// application rather than creating a duplicate, and advances its version.
func TestImportReusesTheExistingApplication(t *testing.T) {
	ts := newTestServer(t)

	_, first := importBundle(t, ts, importBody("Repeat", 1, "", "rep"))
	code, second := importBundle(t, ts, importBody("Repeat", 2, "", "rep"))
	if code != http.StatusOK || !second.Imported {
		t.Fatalf("second import = %d reason=%q", code, second.Reason)
	}
	if second.ApplicationID != first.ApplicationID {
		t.Fatalf("second import created a new application (%s vs %s)", second.ApplicationID, first.ApplicationID)
	}
	// The per-process version advanced independently of the release version.
	if second.Definitions[0].Version != 2 {
		t.Errorf("process version = %d, want 2 after a redeploy", second.Definitions[0].Version)
	}
	_, raw := doReq(t, ts, http.MethodGet, "/api/v1/applications/"+first.ApplicationID+"/releases", "", "")
	var hist []release
	if err := json.Unmarshal(raw, &hist); err != nil {
		t.Fatalf("decode releases: %v", err)
	}
	if len(hist) != 2 || hist[0].Version != 2 {
		t.Fatalf("release history = %+v, want v2 then v1", hist)
	}
}

// TestImportRefusesAVersionItAlreadyHolds pins the no-silent-overwrite rule: the
// publisher owns the numbering, so the same number arriving twice is reported
// rather than replacing what is deployed.
func TestImportRefusesAVersionItAlreadyHolds(t *testing.T) {
	ts := newTestServer(t)

	if code, _ := importBundle(t, ts, importBody("Dup", 3, "", "dup")); code != http.StatusOK {
		t.Fatalf("first import = %d", code)
	}
	code, res := importBundle(t, ts, importBody("Dup", 3, "", "dup"))
	if code != http.StatusConflict {
		t.Fatalf("re-import of v3 = %d, want 409", code)
	}
	if res.Imported || res.Reason == "" {
		t.Errorf("re-import result = %+v, want imported=false with a reason", res)
	}
}

// TestImportOfABrokenBundleLandsNothing is the all-or-nothing rule across the wire:
// one artifact that does not compile refuses the whole bundle, and nothing is
// deployed — not even the artifacts that would have compiled.
func TestImportOfABrokenBundleLandsNothing(t *testing.T) {
	ts := newTestServer(t)

	raw, _ := json.Marshal(map[string]any{
		"application": "Broken",
		"release":     map[string]any{"version": 1},
		"artifacts": []map[string]any{
			{"kind": "process", "processId": "good", "xml": releasableBPMN("good")},
			{"kind": "process", "processId": "bad", "xml": brokenBPMN},
		},
	})
	code, res := importBundle(t, ts, string(raw))
	if code != http.StatusConflict {
		t.Fatalf("broken bundle = %d, want 409", code)
	}
	if res.Imported {
		t.Error("broken bundle reported imported=true")
	}

	// Nothing landed: the compiling artifact must not be deployed either.
	_, procs := doReq(t, ts, http.MethodGet, "/api/v1/processes", "", "")
	var deployed []struct {
		ProcessID string `json:"processId"`
	}
	if err := json.Unmarshal(procs, &deployed); err != nil {
		t.Fatalf("decode processes: %v", err)
	}
	for _, d := range deployed {
		if d.ProcessID == "good" {
			t.Fatal("a refused bundle deployed one of its artifacts")
		}
	}
}

// TestImportBadRequests covers the validation branches a malformed publisher would
// hit, each of which must be a clear 400 rather than a partial import.
func TestImportBadRequests(t *testing.T) {
	ts := newTestServer(t)
	cases := []struct{ name, body string }{
		{"malformed json", `{`},
		{"no application", `{"release":{"version":1},"artifacts":[{"xml":"<x/>"}]}`},
		{"zero version", importBody("X", 0, "", "p")},
		{"no artifacts", `{"application":"X","release":{"version":1},"artifacts":[]}`},
		{"empty xml", `{"application":"X","release":{"version":1},"artifacts":[{"kind":"process","xml":"  "}]}`},
		{"unsupported kind", fmt.Sprintf(`{"application":"X","release":{"version":1},"artifacts":[{"kind":"form","xml":%q}]}`, releasableBPMN("p"))},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if code, _ := doReq(t, ts, http.MethodPost, "/api/v1/applications/import", tc.body, "application/json"); code != http.StatusBadRequest {
				t.Errorf("%s = %d, want 400", tc.name, code)
			}
		})
	}
}
