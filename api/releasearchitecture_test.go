package api_test

import (
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pblumer/atlas/api/archsketch"
	"github.com/pblumer/atlas/api/panorama"
)

// ADR-0471: a release carries an architecture snapshot taken in the same write that
// records it, and three routes serve it — as JSON, as an SVG sketch and as an
// ArchiMate document. These tests pin what the snapshot reads from a real model, that
// listings leave it out, that it survives a restart, that a promoted release gets its
// own on import, and that a release from before snapshots says so.

// sketchedBPMN exercises every fact a snapshot records: a message that starts an
// instance and one that continues it, a worker named in the model, one named by a FEEL
// expression, and three calls — into another application, into this release, and into
// a process deployed nowhere.
const sketchedBPMN = `<?xml version="1.0" encoding="UTF-8"?>
<bpmn:definitions xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL"
                  xmlns:atlas="http://atlas/schema/1.0"
                  xmlns:zeebe="http://camunda.org/schema/zeebe/1.0" id="defs">
  <bpmn:message id="Msg_auftrag" name="auftrag"/>
  <bpmn:message id="Msg_zahlung" name="zahlung">
    <bpmn:extensionElements><zeebe:subscription correlationKey="= orderId"/></bpmn:extensionElements>
  </bpmn:message>
  <bpmn:process id="rechnung" name="Rechnung" isExecutable="true">
    <bpmn:startEvent id="Start_Auftrag"><bpmn:messageEventDefinition messageRef="Msg_auftrag"/></bpmn:startEvent>
    <bpmn:intermediateCatchEvent id="Catch_Zahlung"><bpmn:messageEventDefinition messageRef="Msg_zahlung"/></bpmn:intermediateCatchEvent>
    <bpmn:serviceTask id="Send_Rechnung">
      <bpmn:extensionElements><atlas:mailConnector connector="Postausgang" to="a@b.ch" subject="Rechnung" body="Anbei."/></bpmn:extensionElements>
    </bpmn:serviceTask>
    <bpmn:serviceTask id="Provision">
      <bpmn:extensionElements><atlas:entraConnector connector="=tenant" operation="list-users" resultVariable="users"/></bpmn:extensionElements>
    </bpmn:serviceTask>
    <bpmn:callActivity id="Call_Mahnung">
      <bpmn:extensionElements><zeebe:calledElement processId="mahnung" bindingType="latest"/></bpmn:extensionElements>
    </bpmn:callActivity>
    <bpmn:callActivity id="Call_Intern">
      <bpmn:extensionElements><zeebe:calledElement processId="intern" bindingType="latest"/></bpmn:extensionElements>
    </bpmn:callActivity>
    <bpmn:callActivity id="Call_Fehlt">
      <bpmn:extensionElements><zeebe:calledElement processId="fehlt" bindingType="latest"/></bpmn:extensionElements>
    </bpmn:callActivity>
    <bpmn:endEvent id="End"/>
    <bpmn:sequenceFlow id="f1" sourceRef="Start_Auftrag" targetRef="Catch_Zahlung"/>
    <bpmn:sequenceFlow id="f2" sourceRef="Catch_Zahlung" targetRef="Send_Rechnung"/>
    <bpmn:sequenceFlow id="f3" sourceRef="Send_Rechnung" targetRef="Provision"/>
    <bpmn:sequenceFlow id="f4" sourceRef="Provision" targetRef="Call_Mahnung"/>
    <bpmn:sequenceFlow id="f5" sourceRef="Call_Mahnung" targetRef="Call_Intern"/>
    <bpmn:sequenceFlow id="f6" sourceRef="Call_Intern" targetRef="Call_Fehlt"/>
    <bpmn:sequenceFlow id="f7" sourceRef="Call_Fehlt" targetRef="End"/>
  </bpmn:process>
</bpmn:definitions>`

type sketchUse struct {
	ProcessID string `json:"processId"`
	ElementID string `json:"elementId"`
}

type releaseArchitecture struct {
	ApplicationID string `json:"applicationId"`
	Application   string `json:"application"`
	ReleaseID     string `json:"releaseId"`
	Version       int32  `json:"version"`
	Architecture  struct {
		FormatVersion  int   `json:"formatVersion"`
		MappingVersion int   `json:"mappingVersion"`
		TakenAt        int64 `json:"takenAt"`
		Processes      []struct {
			ProcessID string `json:"processId"`
			Version   int32  `json:"version"`
			Unread    bool   `json:"unread"`
		} `json:"processes"`
		EntryPoints []struct {
			Kind   string      `json:"kind"`
			Name   string      `json:"name"`
			Starts bool        `json:"starts"`
			UsedBy []sketchUse `json:"usedBy"`
		} `json:"entryPoints"`
		Dependencies []struct {
			Kind              string      `json:"kind"`
			Name              string      `json:"name"`
			WorkerType        string      `json:"workerType"`
			TargetApplication string      `json:"targetApplication"`
			UsedBy            []sketchUse `json:"usedBy"`
		} `json:"dependencies"`
	} `json:"architecture"`
}

// publishSketched publishes the billing application beside a dunning application that
// holds the process billing calls, and returns the billing application's id.
func publishSketched(t *testing.T, doDraft func(appID, bpmn string), publish func(appID string), mk func(name string) string) string {
	t.Helper()
	dunning := mk("Mahnwesen")
	doDraft(dunning, releasableBPMN("mahnung"))
	publish(dunning)

	billing := mk("Rechnungswesen")
	doDraft(billing, sketchedBPMN)
	doDraft(billing, releasableBPMN("intern"))
	publish(billing)
	return billing
}

func getArchitecture(t *testing.T, base string, client func(method, path string) (int, []byte), appID string, version string) releaseArchitecture {
	t.Helper()
	code, raw := client(http.MethodGet, "/api/v1/applications/"+appID+"/releases/"+version+"/architecture")
	if code != http.StatusOK {
		t.Fatalf("%s architecture status=%d body=%s", base, code, raw)
	}
	var out releaseArchitecture
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("decode architecture: %v (%s)", err, raw)
	}
	return out
}

func TestPublishTakesTheArchitectureSnapshot(t *testing.T) {
	ts := newTestServer(t)
	client := func(method, path string) (int, []byte) { return doReq(t, ts, method, path, "", "") }
	app := publishSketched(t,
		func(appID, bpmn string) {
			if code, body := doReq(t, ts, http.MethodPost, "/api/v1/drafts?projectId="+appID, bpmn, "application/xml"); code != http.StatusOK {
				t.Fatalf("save draft status=%d body=%s", code, body)
			}
		},
		func(appID string) {
			if code, res := publishApp(t, ts, appID, ""); code != http.StatusOK || !res.Deployed {
				t.Fatalf("publish status=%d reason=%q", code, res.Reason)
			}
		},
		func(name string) string { return mkApp(t, ts, name) })

	got := getArchitecture(t, "publish", client, app, "1")
	arch := got.Architecture
	if got.Application != "Rechnungswesen" || got.Version != 1 || got.ReleaseID == "" {
		t.Errorf("names = %+v, want the billing application's v1 with its release id", got)
	}
	if arch.FormatVersion != archsketch.FormatVersion || arch.MappingVersion != archsketch.MappingVersion || arch.TakenAt == 0 {
		t.Errorf("versions/time = %d/%d/%d, want %d/%d/non-zero", arch.FormatVersion, arch.MappingVersion, arch.TakenAt,
			archsketch.FormatVersion, archsketch.MappingVersion)
	}
	if len(arch.Processes) != 2 {
		t.Errorf("processes = %+v, want the two members", arch.Processes)
	}

	entries := map[string]bool{}
	for _, e := range arch.EntryPoints {
		entries[e.Name] = e.Starts
		if len(e.UsedBy) != 1 || e.UsedBy[0].ProcessID != "rechnung" {
			t.Errorf("entry point %q used by %+v, want one element of rechnung", e.Name, e.UsedBy)
		}
	}
	if starts, ok := entries["auftrag"]; !ok || !starts {
		t.Errorf("auftrag = %v/%v, want an entry point that starts an instance", starts, ok)
	}
	if starts, ok := entries["zahlung"]; !ok || starts {
		t.Errorf("zahlung = %v/%v, want an entry point that continues an instance", starts, ok)
	}

	deps := map[string]string{}
	for _, d := range arch.Dependencies {
		deps[d.Kind+":"+d.Name] = d.WorkerType + "|" + d.TargetApplication
	}
	want := map[string]string{
		"worker:Postausgang":    "mail|",
		"runtimeWorker:=tenant": "entra|",
		"call:mahnung":          "|Mahnwesen",
		"call:fehlt":            "|",
	}
	for k, v := range want {
		if deps[k] != v {
			t.Errorf("dependency %s = %q, want %q (all: %v)", k, deps[k], v, deps)
		}
	}
	if _, ok := deps["call:intern"]; ok {
		t.Error("a call into a process of the same release is listed as a way out of the application")
	}

	// The listings leave the snapshot out.
	_, raw := client(http.MethodGet, "/api/v1/applications/"+app+"/releases")
	if strings.Contains(string(raw), `"architecture"`) {
		t.Errorf("release history carries the snapshot: %s", raw)
	}
}

func TestTheSketchRenderings(t *testing.T) {
	ts := newTestServer(t)
	app := publishSketched(t,
		func(appID, bpmn string) {
			if code, body := doReq(t, ts, http.MethodPost, "/api/v1/drafts?projectId="+appID, bpmn, "application/xml"); code != http.StatusOK {
				t.Fatalf("save draft status=%d body=%s", code, body)
			}
		},
		func(appID string) {
			if code, res := publishApp(t, ts, appID, ""); code != http.StatusOK || !res.Deployed {
				t.Fatalf("publish status=%d reason=%q", code, res.Reason)
			}
		},
		func(name string) string { return mkApp(t, ts, name) })

	get := func(suffix string) (*http.Response, []byte) {
		t.Helper()
		res, err := http.Get(ts.URL + "/api/v1/applications/" + app + "/releases/1/" + suffix)
		if err != nil {
			t.Fatalf("GET %s: %v", suffix, err)
		}
		defer res.Body.Close()
		body, _ := io.ReadAll(res.Body)
		return res, body
	}

	res, svg := get("architecture.svg")
	if res.StatusCode != http.StatusOK || !strings.HasPrefix(res.Header.Get("Content-Type"), "image/svg+xml") {
		t.Fatalf("svg status=%d type=%q body=%s", res.StatusCode, res.Header.Get("Content-Type"), svg)
	}
	if csp := res.Header.Get("Content-Security-Policy"); !strings.Contains(csp, "sandbox") || res.Header.Get("X-Content-Type-Options") != "nosniff" {
		t.Errorf("svg headers CSP=%q nosniff=%q, want the sandbox policy", csp, res.Header.Get("X-Content-Type-Options"))
	}
	for _, want := range []string{"Rechnungswesen v1", "Postausgang", "Message auftrag", "in Mahnwesen"} {
		if !strings.Contains(string(svg), want) {
			t.Errorf("svg lacks %q", want)
		}
	}
	if _, again := get("architecture.svg"); string(again) != string(svg) {
		t.Error("the same release rendered twice gives different bytes")
	}

	res, doc := get("architecture.xml")
	if res.StatusCode != http.StatusOK || !strings.HasPrefix(res.Header.Get("Content-Type"), "application/xml") {
		t.Fatalf("xml status=%d type=%q", res.StatusCode, res.Header.Get("Content-Type"))
	}
	if v := panorama.Validate(doc); len(v.Problems) != 0 {
		t.Errorf("the ArchiMate document fails Panorama's validator: %+v", v.Problems)
	}

	for path, want := range map[string]int{
		"/api/v1/applications/" + app + "/releases/9/architecture":     http.StatusNotFound,
		"/api/v1/applications/" + app + "/releases/zero/architecture":  http.StatusBadRequest,
		"/api/v1/applications/nope/releases/1/architecture.svg":        http.StatusNotFound,
		"/api/v1/applications/" + app + "/releases/0/architecture.xml": http.StatusBadRequest,
	} {
		if code, body := doReq(t, ts, http.MethodGet, path, "", ""); code != want {
			t.Errorf("GET %s = %d (%s), want %d", path, code, body, want)
		}
	}
}

// TestTheSnapshotSurvivesARestartAndAnOlderReleaseSaysItHasNone: the snapshot is on
// the release record, so it is as durable as the record. A record from before
// snapshots existed carries none, and the route says so in words.
func TestTheSnapshotSurvivesARestartAndAnOlderReleaseSaysItHasNone(t *testing.T) {
	dir := t.TempDir()
	first := boot(t, dir)
	app := mkApp(t, first.ts, "Durable")
	if code, _ := doReq(t, first.ts, http.MethodPost, "/api/v1/drafts?projectId="+app, sketchedBPMN, "application/xml"); code != http.StatusOK {
		t.Fatal("save draft")
	}
	if code, res := publishApp(t, first.ts, app, ""); code != http.StatusOK || res.Release == nil {
		t.Fatalf("publish status=%d", code)
	}
	if code, res := publishApp(t, first.ts, app, ""); code != http.StatusOK || res.Release == nil || res.Release.Version != 2 {
		t.Fatalf("second publish status=%d", code)
	}
	first.shutdown()

	// Strip v1's snapshot on disk, as a record written before this build would be.
	files, err := filepath.Glob(filepath.Join(dir, "releases", "*.json"))
	if err != nil || len(files) != 2 {
		t.Fatalf("release files = %v (%v), want two", files, err)
	}
	for _, f := range files {
		raw, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		var rec map[string]any
		if err := json.Unmarshal(raw, &rec); err != nil {
			t.Fatal(err)
		}
		if _, ok := rec["architecture"]; !ok {
			t.Fatalf("%s has no snapshot on disk: %s", f, raw)
		}
		if rec["version"].(float64) == 1 {
			delete(rec, "architecture")
			out, _ := json.Marshal(rec)
			if err := os.WriteFile(f, out, 0o600); err != nil {
				t.Fatal(err)
			}
		}
	}

	second := boot(t, dir)
	defer second.shutdown()
	client := func(method, path string) (int, []byte) { return doReq(t, second.ts, method, path, "", "") }
	if got := getArchitecture(t, "after restart", client, app, "2"); len(got.Architecture.EntryPoints) != 2 {
		t.Errorf("v2 after restart = %+v, want its snapshot back", got.Architecture)
	}
	code, body := client(http.MethodGet, "/api/v1/applications/"+app+"/releases/1/architecture.svg")
	if code != http.StatusNotFound || !strings.Contains(string(body), "recorded before Atlas took them") {
		t.Errorf("v1 without a snapshot = %d %s, want a 404 that says why", code, body)
	}
}

// TestAnImportedReleaseGetsItsOwnSnapshot: a promoted release is recorded on the
// target by the import, and the target takes its own snapshot from what it deployed.
func TestAnImportedReleaseGetsItsOwnSnapshot(t *testing.T) {
	ts := newTestServer(t)
	bundle, _ := json.Marshal(map[string]any{
		"application": "Rechnungswesen",
		"release":     map[string]any{"version": 4, "note": "promoted"},
		"artifacts":   []map[string]any{{"kind": "process", "processId": "rechnung", "xml": sketchedBPMN}},
	})
	code, raw := doReq(t, ts, http.MethodPost, "/api/v1/applications/import", string(bundle), "application/json")
	if code != http.StatusOK {
		t.Fatalf("import status=%d body=%s", code, raw)
	}
	if strings.Contains(string(raw), `"architecture"`) {
		t.Errorf("import response carries the snapshot: %s", raw)
	}
	var res struct {
		ApplicationID string `json:"applicationId"`
	}
	if err := json.Unmarshal(raw, &res); err != nil || res.ApplicationID == "" {
		t.Fatalf("decode import: %v (%s)", err, raw)
	}
	client := func(method, path string) (int, []byte) { return doReq(t, ts, method, path, "", "") }
	got := getArchitecture(t, "import", client, res.ApplicationID, "4")
	if got.Version != 4 || len(got.Architecture.Processes) != 1 || len(got.Architecture.EntryPoints) != 2 {
		t.Errorf("imported release architecture = %+v", got)
	}
}
