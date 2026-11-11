package worker

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// The worker runs the resolved task against the server its Worker names, with that
// Worker's token, and completes with the report under the task's result variable.
func TestRunAtlasReportJobRunsTheResolvedTask(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer atl_x" || r.URL.Path != "/api/v1/incidents/summary" {
			http.Error(w, "unexpected", http.StatusTeapot)
			return
		}
		_, _ = w.Write([]byte(`{"total":0,"byProcess":[],"byElement":[]}`))
	}))
	defer srv.Close()
	env := map[string]string{
		"ATLAS_ATLASREPORT_CONNECTORS":        "atlas",
		"ATLAS_ATLASREPORT_ATLAS_URL":         srv.URL,
		"ATLAS_ATLASREPORT_ATLAS_CREDENTIALS": "atl_x",
	}
	reg, names, err := atlasReportRegistryFromEnv(func(k string) string { return env[k] })
	if err != nil || len(names) != 1 {
		t.Fatalf("registry: %v %v", names, err)
	}
	out, err := RunAtlasReportJob(context.Background(), Job{Connector: &ConnectorPayload{Kind: "atlasreport", Fields: map[string]any{
		"connector": "atlas", "operation": "incident-summary", "resultVariable": "incidents", "language": "en",
	}}}, reg)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(out["incidents"])
	if !strings.Contains(string(raw), `"schema":"atlas.report/1"`) {
		t.Errorf("completed with %s, want a report", raw)
	}

	if _, err := RunAtlasReportJob(context.Background(), Job{}, reg); err == nil || !strings.HasPrefix(err.Error(), "atlasreport:") {
		t.Errorf("a job without a resolved task: %v", err)
	}
	// A field this worker does not know is a server newer than the worker: refused, not
	// silently dropped.
	if _, err := RunAtlasReportJob(context.Background(), Job{Connector: &ConnectorPayload{Kind: "atlasreport", Fields: map[string]any{
		"connector": "atlas", "operation": "incident-summary", "audience": "all",
	}}}, reg); err == nil {
		t.Error("an unknown field was accepted")
	}
}

func TestAtlasReportRegistryFromEnv(t *testing.T) {
	if reg, _, err := atlasReportRegistryFromEnv(func(string) string { return "" }); reg != nil || err != nil {
		t.Errorf("unconfigured: %v %v", reg, err)
	}
	env := map[string]string{"ATLAS_ATLASREPORT_CONNECTORS": "atlas", "ATLAS_ATLASREPORT_ATLAS_URL": "https://atlas.example.ch"}
	if _, _, err := atlasReportRegistryFromEnv(func(k string) string { return env[k] }); err == nil || !strings.Contains(err.Error(), "ATLAS_ATLASREPORT_ATLAS_CREDENTIALS") {
		t.Errorf("a named server without a credential: %v", err)
	}
}
