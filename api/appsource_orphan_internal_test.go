package api

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func postSourceTree(t *testing.T, ts *httptest.Server, key, processID string) (int, string) {
	t.Helper()
	var archive bytes.Buffer
	if err := writeSourceArchive(&archive, []sourceFile{
		{Path: "atlas.json", Data: []byte(`{"formatVersion":1,"key":"` + key + `","name":"` + key + `","processes":[{"id":"` + processID + `","name":"P","path":"p.bpmn"}],"forms":[],"decisions":[]}`)},
		{Path: "p.bpmn", Data: []byte(waitingProcess(processID))},
	}); err != nil {
		t.Fatal(err)
	}
	resp, err := ts.Client().Post(ts.URL+"/api/v1/applications/source", "application/gzip", &archive)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(raw)
}

// TestAnImportTakesTheArtifactsOfADeletedApplication: deleting an application
// without purge leaves its artifacts tagged with an id nothing answers to, and they
// read as Ungrouped (ADR-0034). Importing the same package again therefore creates
// the application afresh and takes them — instead of refusing them as "another
// application's" for ever. An application that still exists keeps its artifacts.
func TestAnImportTakesTheArtifactsOfADeletedApplication(t *testing.T) {
	srv, _ := openAt(t, t.TempDir())
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)

	code, body := postSourceTree(t, ts, "lehrgang", "lg-eintritt")
	if code != http.StatusOK {
		t.Fatalf("first import = %d %s", code, body)
	}
	var first struct {
		ApplicationID string `json:"applicationId"`
	}
	_ = json.Unmarshal([]byte(body), &first)

	// Another application keeps what it holds: a package naming its process is refused.
	if code, body := postSourceTree(t, ts, "other", "lg-eintritt"); code != http.StatusConflict ||
		!strings.Contains(body, "already belongs to another application") {
		t.Fatalf("import into another application = %d %s", code, body)
	}

	req, _ := http.NewRequest(http.MethodDelete, ts.URL+"/api/v1/applications/"+first.ApplicationID, nil)
	resp, err := ts.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("delete without purge = %d", resp.StatusCode)
	}

	code, body = postSourceTree(t, ts, "lehrgang", "lg-eintritt")
	if code != http.StatusOK {
		t.Fatalf("import after the application was deleted = %d %s", code, body)
	}
	var again struct {
		ApplicationID string `json:"applicationId"`
		Created       bool   `json:"created"`
	}
	_ = json.Unmarshal([]byte(body), &again)
	if !again.Created || again.ApplicationID == first.ApplicationID {
		t.Fatalf("second import = %s, want a new application", body)
	}
	resp, err = ts.Client().Get(ts.URL + "/api/v1/drafts?projectId=" + again.ApplicationID)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if !strings.Contains(string(raw), "lg-eintritt") {
		t.Fatalf("the process did not move into the new application: %s", raw)
	}
}
