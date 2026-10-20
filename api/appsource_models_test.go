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
// its models (ADR-draft-application-package-export). Before, the tree carried the
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
