package api_test

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

const panoramaExchangeXML = `<?xml version="1.0" encoding="UTF-8"?>
<model xmlns="http://www.opengroup.org/xsd/archimate/3.0/"
       xmlns:xsi="http://www.w3.org/2001/XMLSchema-instance"
       identifier="model-http-test">
  <name xml:lang="en">HTTP Landscape</name>
  <elements>
    <element identifier="atlas" xsi:type="ApplicationComponent"><name>Atlas</name></element>
  </elements>
</model>`

func TestPanoramaModelLibraryHTTP(t *testing.T) {
	ts := newTestServer(t)

	code, body := doReq(t, ts, http.MethodPost, "/api/v1/applications", `{"name":"Enterprise Architecture"}`, "application/json")
	if code != http.StatusOK {
		t.Fatalf("create application: %d %s", code, body)
	}
	var app struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(body, &app); err != nil {
		t.Fatalf("decode application: %v", err)
	}

	payload, err := json.Marshal(map[string]any{
		"applicationId": app.ID,
		"name":          "Enterprise Landscape",
		"notation":      "archimate-3.2",
		"xml":           panoramaExchangeXML,
	})
	if err != nil {
		t.Fatalf("marshal Panorama model: %v", err)
	}
	code, body = doReq(t, ts, http.MethodPost, "/api/v1/panorama/models", string(payload), "application/json")
	if code != http.StatusCreated {
		t.Fatalf("create Panorama model: %d %s", code, body)
	}
	var model struct {
		ID       string `json:"id"`
		Revision int64  `json:"revision"`
	}
	if err := json.Unmarshal(body, &model); err != nil {
		t.Fatalf("decode model: %v", err)
	}
	if model.ID == "" || model.Revision != 1 {
		t.Fatalf("model = %+v", model)
	}

	// Panorama contributes to the owning application's artifact count, and strict
	// ownership means deleting the application is refused while a model would be
	// orphaned.
	code, body = doReq(t, ts, http.MethodGet, "/api/v1/applications", "", "")
	var applications []struct {
		ID        string `json:"id"`
		Artifacts int    `json:"artifacts"`
	}
	if err := json.Unmarshal(body, &applications); err != nil {
		t.Fatalf("decode applications: %v", err)
	}
	foundCount := false
	for _, candidate := range applications {
		if candidate.ID == app.ID {
			foundCount = candidate.Artifacts == 1
		}
	}
	if !foundCount {
		t.Fatalf("owning application does not report the Panorama artifact: %s", body)
	}
	if code, body = doReq(t, ts, http.MethodDelete, "/api/v1/applications/"+app.ID, "", ""); code != http.StatusConflict || !strings.Contains(string(body), "Panorama models") {
		t.Fatalf("delete owning application with Panorama model: %d %s", code, body)
	}

	code, body = doReq(t, ts, http.MethodGet, "/api/v1/panorama/models?applicationId="+app.ID, "", "")
	if code != http.StatusOK || !strings.Contains(string(body), "Enterprise Landscape") {
		t.Fatalf("list Panorama models: %d %s", code, body)
	}
	if strings.Contains(string(body), `"xml"`) {
		t.Fatalf("list contains XML: %s", body)
	}

	code, body = doReq(t, ts, http.MethodGet, "/api/v1/panorama/models/"+model.ID+"/xml", "", "")
	if code != http.StatusOK || string(body) != panoramaExchangeXML {
		t.Fatalf("export Panorama model: %d %s", code, body)
	}

	code, body = doReq(t, ts, http.MethodPost, "/api/v1/panorama/validate", panoramaExchangeXML, "application/xml")
	if code != http.StatusOK || !strings.Contains(string(body), `"valid":true`) {
		t.Fatalf("validate Panorama model: %d %s", code, body)
	}

	// The design-time backup must carry both the owning application and its
	// architecture model, and a restore must make the model readable immediately.
	code, archive := doReq(t, ts, http.MethodGet, "/api/v1/backup", "", "")
	if code != http.StatusOK {
		t.Fatalf("backup with Panorama model: %d %s", code, archive)
	}
	if entries := tarGzEntries(t, archive); !hasPrefix(entries, "panorama-models/") {
		t.Fatalf("backup missing Panorama model: %v", entries)
	}
	restored, _ := newBackupServer(t)
	if code, body = doReqBytes(t, restored, http.MethodPost, "/api/v1/restore", archive, "application/gzip"); code != http.StatusOK {
		t.Fatalf("restore Panorama backup: %d %s", code, body)
	}
	if code, body = doReq(t, restored, http.MethodGet, "/api/v1/panorama/models", "", ""); code != http.StatusOK || !strings.Contains(string(body), "Enterprise Landscape") {
		t.Fatalf("list restored Panorama models: %d %s", code, body)
	}

	if code, body = doReq(t, ts, http.MethodDelete, "/api/v1/panorama/models/"+model.ID, "", ""); code != http.StatusNoContent {
		t.Fatalf("delete Panorama model: %d %s", code, body)
	}
	if code, body = doReq(t, ts, http.MethodDelete, "/api/v1/applications/"+app.ID, "", ""); code != http.StatusNoContent {
		t.Fatalf("delete application after its Panorama model: %d %s", code, body)
	}
}
