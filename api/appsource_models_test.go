package api_test

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// An application whose decisions live in this server's model folder travels with
// its models (ADR-0467). Before, the tree carried the
// references alone, and on a server that had never seen the models every one of
// them pointed at a handle that was not there — the application imported and then
// refused to publish.

const freigabeDMN = `<?xml version="1.0" encoding="UTF-8"?>
<definitions xmlns="https://www.omg.org/spec/DMN/20191111/MODEL/" id="freigabe" name="Freigabe" namespace="http://atlas/dmn">
  <decision id="Freigabestufe" name="Freigabestufe"><literalExpression id="le"><text>"hoch"</text></literalExpression></decision>
</definitions>`

// freigabeBPMN asks the decision, so publishing it resolves the model.
const freigabeBPMN = `<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL">
  <process id="freigabe_antrag" name="Freigabe-Antrag" isExecutable="true">
    <startEvent id="start"/>
    <businessRuleTask id="decide">
      <extensionElements><calledDecision decisionId="Freigabestufe" resultVariable="stufe"/></extensionElements>
    </businessRuleTask>
    <endEvent id="end"/>
    <sequenceFlow id="f1" sourceRef="start" targetRef="decide"/>
    <sequenceFlow id="f2" sourceRef="decide" targetRef="end"/>
  </process>
</definitions>`

// uploadModel stores a model in the server's model folder and returns its handle.
func uploadModel(t *testing.T, ts *httptest.Server, name, xml string) string {
	t.Helper()
	code, body := doReq(t, ts, http.MethodPost, "/api/v1/dmn-models?name="+name, xml, "application/xml")
	var out struct {
		ModelRef string `json:"modelRef"`
	}
	if code != http.StatusOK || json.Unmarshal(body, &out) != nil || out.ModelRef == "" {
		t.Fatalf("upload model status=%d body=%s", code, body)
	}
	return out.ModelRef
}

func TestApplicationSourceCarriesDecisionModelsAcrossServers(t *testing.T) {
	origin := newTestServer(t)
	appID := mkApp(t, origin, "Freigaben")
	ref := uploadModel(t, origin, "freigabe", freigabeDMN)
	addDecisionIn(t, origin, appID, "Freigabestufe", ref)
	if code, body := doReq(t, origin, http.MethodPost, "/api/v1/drafts?projectId="+appID, freigabeBPMN, "application/xml"); code != http.StatusOK {
		t.Fatalf("save draft status=%d body=%s", code, body)
	}
	archive := exportSource(t, origin, appID)

	clone := newTestServer(t)
	code, res, body := importSource(t, clone, archive)
	if code != http.StatusOK {
		t.Fatalf("import status=%d body=%s", code, body)
	}
	if res.Decisions != 1 || res.Models != 1 {
		t.Errorf("imported %+v, want one decision and its model", res)
	}

	// The model is under its handle on the clone, so the application publishes
	// there: the business rule task resolves the decision it asks.
	code, body = doReq(t, clone, http.MethodPost, "/api/v1/applications/"+res.ApplicationID+"/publish", `{"note":"clone"}`, "application/json")
	var pub struct {
		Deployed bool   `json:"deployed"`
		Reason   string `json:"reason"`
	}
	if code != http.StatusOK || json.Unmarshal(body, &pub) != nil || !pub.Deployed {
		t.Fatalf("publish on the clone status=%d body=%s", code, body)
	}

	// The round trip stays byte-stable with a model in it.
	if again := exportSource(t, clone, res.ApplicationID); !bytes.Equal(archive, again) {
		t.Errorf("export → import → export is not byte-stable with a model (%d vs %d bytes)", len(archive), len(again))
	}
}

// sourceArchive packs files, in the order given, as the gzip tar a source import
// reads.
func sourceArchive(t *testing.T, files [][2]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	gzw := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gzw)
	for _, f := range files {
		if err := tw.WriteHeader(&tar.Header{Name: f[0], Typeflag: tar.TypeReg, Mode: 0o644, Size: int64(len(f[1]))}); err != nil {
			t.Fatalf("write header: %v", err)
		}
		if _, err := tw.Write([]byte(f[1])); err != nil {
			t.Fatalf("write: %v", err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatalf("close tar: %v", err)
	}
	if err := gzw.Close(); err != nil {
		t.Fatalf("close gzip: %v", err)
	}
	return buf.Bytes()
}

// TestApplicationSourceRefusesAModelThatDoesNotCompile: a model is compiled before
// it is stored under a handle other references resolve, and a tree carrying one
// that does not compile is refused whole.
func TestApplicationSourceRefusesAModelThatDoesNotCompile(t *testing.T) {
	ts := newTestServer(t)
	archive := sourceArchive(t, [][2]string{
		{"atlas.json", `{"formatVersion":2,"key":"kaputt","name":"Kaputt",` +
			`"decisions":[{"id":"d","name":"D","modelRef":"kaputt","path":"decisions/kaputt.dmn"}]}`},
		{"decisions/kaputt.dmn", "not a model"},
	})
	code, _, body := importSource(t, ts, archive)
	if code != http.StatusBadRequest || !strings.Contains(string(body), `decision model \"kaputt\"`) {
		t.Fatalf("status=%d body=%s, want a 400 naming the model", code, body)
	}
	if _, listed := doReq(t, ts, http.MethodGet, "/api/v1/applications", "", ""); strings.Contains(string(listed), "kaputt") {
		t.Errorf("a refused tree created its application: %s", listed)
	}
}

// TestApplicationSourceImportIsIdempotent: the same tree imported again is the
// same application, and the second import writes nothing — no draft saved anew,
// no second copy, however often it runs.
func TestApplicationSourceImportIsIdempotent(t *testing.T) {
	origin := newTestServer(t)
	appID := mkApp(t, origin, "Freigaben")
	saveDraftIn(t, origin, appID, "antrag", "Antrag")
	saveFormIn(t, origin, appID, "antrag-form", "Antragsformular")
	addDecisionIn(t, origin, appID, "Freigabestufe", uploadModel(t, origin, "freigabe", freigabeDMN))
	archive := exportSource(t, origin, appID)

	clone := newTestServer(t)
	for run := 1; run <= 3; run++ {
		code, res, body := importSource(t, clone, archive)
		if code != http.StatusOK {
			t.Fatalf("import %d: status=%d body=%s", run, code, body)
		}
		for ref, got := range res.outcomes() {
			want := "new/created"
			if run > 1 {
				want = "unchanged/unchanged"
			}
			if got != want {
				t.Errorf("import %d: %s = %s, want %s", run, ref, got, want)
			}
		}
	}
	_, listed := doReq(t, clone, http.MethodGet, "/api/v1/applications", "", "")
	if apps := ownApplications(t, listed); len(apps) != 1 {
		t.Fatalf("three imports left %d applications: %s", len(apps), listed)
	}
	_, drafts := doReq(t, clone, http.MethodGet, "/api/v1/drafts", "", "")
	var saved []struct {
		ProcessID string `json:"processId"`
		SavedAt   int64  `json:"savedAt"`
	}
	if err := json.Unmarshal(drafts, &saved); err != nil || len(saved) != 1 {
		t.Fatalf("drafts = %s (%v)", drafts, err)
	}
}

// TestApplicationSourceImportAsksBeforeOverwriting: work done on this server is
// not replaced by an import nobody looked at. An artifact that differs is listed
// and decided — overwritten, or kept — one by one; one left undecided stops the
// import before anything is written.
func TestApplicationSourceImportAsksBeforeOverwriting(t *testing.T) {
	ts := newTestServer(t)
	appID := mkApp(t, ts, "Freigaben")
	saveDraftIn(t, ts, appID, "antrag", "Antrag")
	saveDraftIn(t, ts, appID, "pruefung", "Prüfung")
	saveFormIn(t, ts, appID, "antrag-form", "Antragsformular")
	archive := exportSource(t, ts, appID)

	// Work done here after the export: both drafts and the form change.
	saveDraftIn(t, ts, appID, "antrag", "Antrag, hier geändert")
	saveDraftIn(t, ts, appID, "pruefung", "Prüfung, hier geändert")
	saveFormIn(t, ts, appID, "antrag-form", "Formular, hier geändert")

	code, res, body := importSourceWith(t, ts, archive, "?dryRun=true")
	if code != http.StatusOK || !res.DryRun {
		t.Fatalf("plan: status=%d body=%s", code, body)
	}
	want := map[string]string{
		"application:freigaben": "unchanged/unchanged",
		"process:antrag":        "changed/undecided",
		"process:pruefung":      "changed/undecided",
		"form:antrag-form":      "changed/undecided",
	}
	if got := res.outcomes(); len(got) != len(want) {
		t.Fatalf("plan = %v, want %v", got, want)
	} else {
		for ref, w := range want {
			if got[ref] != w {
				t.Errorf("plan %s = %s, want %s", ref, got[ref], w)
			}
		}
	}

	code, _, body = importSourceWith(t, ts, archive, "?overwrite=process:antrag")
	if code != http.StatusConflict || !strings.Contains(string(body), `"undecided":["process:pruefung","form:antrag-form"]`) {
		t.Fatalf("partly decided: status=%d body=%s, want 409 naming the two left", code, body)
	}
	if _, xml := doReq(t, ts, http.MethodGet, "/api/v1/drafts/antrag/xml", "", ""); !strings.Contains(string(xml), "hier geändert") {
		t.Fatalf("a refused import wrote the draft: %s", xml)
	}

	code, res, body = importSourceWith(t, ts, archive, "?overwrite=process:antrag&keep=process:pruefung,form:antrag-form")
	if code != http.StatusOK {
		t.Fatalf("decided: status=%d body=%s", code, body)
	}
	if got := res.outcomes(); got["process:antrag"] != "changed/updated" || got["process:pruefung"] != "changed/kept" || got["form:antrag-form"] != "changed/kept" {
		t.Errorf("outcomes = %v", got)
	}
	if _, xml := doReq(t, ts, http.MethodGet, "/api/v1/drafts/antrag/xml", "", ""); strings.Contains(string(xml), "hier geändert") {
		t.Errorf("the draft chosen for overwriting kept the local version: %s", xml)
	}
	if _, xml := doReq(t, ts, http.MethodGet, "/api/v1/drafts/pruefung/xml", "", ""); !strings.Contains(string(xml), "hier geändert") {
		t.Errorf("the draft chosen for keeping was overwritten: %s", xml)
	}

	// The import remembers what it brought, so what was kept is now ahead of this
	// tree: kept without being named, replaced when named (ADR-0467, amended).
	code, res, body = importSourceWith(t, ts, archive, "?overwrite=form:antrag-form")
	if code != http.StatusOK || res.outcomes()["form:antrag-form"] != "ahead/updated" || res.outcomes()["process:pruefung"] != "ahead/kept" {
		t.Fatalf("after the decisions: status=%d body=%s", code, body)
	}
	for _, q := range []string{"?overwrite=*&keep=*", "?overwrite=process:antrag&keep=process:antrag"} {
		if code, _, body := importSourceWith(t, ts, archive, q); code != http.StatusBadRequest {
			t.Errorf("%s: status=%d body=%s, want 400", q, code, body)
		}
	}
}
