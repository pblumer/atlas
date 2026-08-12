package api_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"testing"
)

// csvImportBPMN is the smallest process exercising the CSV-import worker: a service
// task carrying the reserved io.atlas.csv-import job type, which parses the seeded
// csvText/columnConfig into rows and self-completes.
const csvImportBPMN = `<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL"
                    xmlns:zeebe="http://camunda.org/schema/zeebe/1.0">
  <process id="imp" isExecutable="true">
    <startEvent id="s"/>
    <serviceTask id="einlesen">
      <extensionElements><zeebe:taskDefinition type="io.atlas.csv-import"/></extensionElements>
    </serviceTask>
    <endEvent id="e"/>
    <sequenceFlow id="f1" sourceRef="s" targetRef="einlesen"/>
    <sequenceFlow id="f2" sourceRef="einlesen" targetRef="e"/>
  </process>
</definitions>`

// csvConnectorBPMN drives the CSV worker through a first-class csvConnector task
// (ADR-0090): the layout lives on the task (source variable + derive-from-header +
// a renamed result variable), so only the file (csvText) is seeded at runtime — no
// columnConfig variable, no preceding script task.
const csvConnectorBPMN = `<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL"
                    xmlns:atlas="http://atlas.dev/schema/1.0">
  <process id="impc" isExecutable="true">
    <startEvent id="s"/>
    <serviceTask id="einlesen">
      <extensionElements>
        <atlas:csvConnector source="csvText" resultVariable="records"/>
      </extensionElements>
    </serviceTask>
    <endEvent id="e"/>
    <sequenceFlow id="f1" sourceRef="s" targetRef="einlesen"/>
    <sequenceFlow id="f2" sourceRef="einlesen" targetRef="e"/>
  </process>
</definitions>`

// TestCSVConnectorServiceTask proves the first-class csvConnector path (ADR-0090):
// with the layout authored on the task, seeding only csvText runs the worker to
// completion and writes the derived rows into the authored result variable.
func TestCSVConnectorServiceTask(t *testing.T) {
	ts := newTestServer(t)

	code, body := doReq(t, ts, http.MethodPost, "/api/v1/deployments", csvConnectorBPMN, "application/xml")
	if code != http.StatusOK {
		t.Fatalf("deploy: %d %s", code, body)
	}
	var dep struct {
		Key uint64 `json:"key"`
	}
	if err := json.Unmarshal(body, &dep); err != nil {
		t.Fatalf("decode deploy: %v", err)
	}

	// Only the file is seeded — no columnConfig, no script task. The columns are
	// derived from the header row by the connector.
	start := `{"variables":{"csvText":"email,group\nada@x.io,users\nbob,ops\n"}}`
	code, body = doReq(t, ts, http.MethodPost, fmt.Sprintf("/api/v1/processes/%d/instances", dep.Key), start, "application/json")
	if code != http.StatusOK {
		t.Fatalf("create instance: %d %s", code, body)
	}
	if !bytes.Contains(body, []byte(`"activeElementInstances":0`)) {
		t.Fatalf("instance did not complete (connector worker didn't run?): %s", body)
	}

	code, body = doReq(t, ts, http.MethodGet, "/api/v1/instances", "", "")
	if code != http.StatusOK {
		t.Fatalf("list instances: %d %s", code, body)
	}
	var instances []struct {
		Key uint64 `json:"key"`
	}
	if err := json.Unmarshal(body, &instances); err != nil {
		t.Fatalf("decode instances: %v", err)
	}
	if len(instances) != 1 {
		t.Fatalf("instances = %d, want 1", len(instances))
	}

	code, body = doReq(t, ts, http.MethodGet, fmt.Sprintf("/api/v1/instances/%d/variables", instances[0].Key), "", "")
	if code != http.StatusOK {
		t.Fatalf("get variables: %d %s", code, body)
	}
	dec := json.NewDecoder(bytes.NewReader(body))
	dec.UseNumber()
	var vars map[string]any
	if err := dec.Decode(&vars); err != nil {
		t.Fatalf("decode variables: %v (%s)", err, body)
	}
	if got, ok := vars["rowCount"].(json.Number); !ok || got.String() != "2" {
		t.Errorf("rowCount = %v (%T), want 2", vars["rowCount"], vars["rowCount"])
	}
	// The rows landed under the authored result variable name, not the default "rows".
	if _, ok := vars["rows"]; ok {
		t.Errorf("unexpected default 'rows' variable; the task renamed it to 'records'")
	}
	records, ok := vars["records"].([]any)
	if !ok || len(records) != 2 {
		t.Fatalf("records = %v (%T), want an array of 2", vars["records"], vars["records"])
	}
	first, ok := records[0].(map[string]any)
	if !ok || first["email"] != "ada@x.io" || first["group"] != "users" {
		t.Errorf("records[0] = %v, want {email:ada@x.io, group:users}", records[0])
	}
}

// TestCSVImportServiceTaskErrors covers the worker's error paths: a missing source,
// a missing or wrong-shaped layout, and an unparseable layout each fail the job, so
// the token does not pass the service task — the instance stays active rather than
// completing with an empty or wrong batch.
func TestCSVImportServiceTaskErrors(t *testing.T) {
	cases := []struct {
		name string
		vars string
	}{
		{"missing csvText", `{"columnConfig":{"columns":[{"name":"email"}]}}`},
		{"missing columnConfig", `{"csvText":"email\nada@x.io\n"}`},
		{"columnConfig wrong shape", `{"csvText":"email\nada@x.io\n","columnConfig":[1,2]}`},
		{"unparseable layout (no columns, no header)", `{"csvText":"email\nada@x.io\n","columnConfig":{"columns":[],"hasHeader":false}}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ts := newTestServer(t)
			code, body := doReq(t, ts, http.MethodPost, "/api/v1/deployments", csvImportBPMN, "application/xml")
			if code != http.StatusOK {
				t.Fatalf("deploy: %d %s", code, body)
			}
			var dep struct {
				Key uint64 `json:"key"`
			}
			if err := json.Unmarshal(body, &dep); err != nil {
				t.Fatalf("decode deploy: %v", err)
			}
			code, body = doReq(t, ts, http.MethodPost, fmt.Sprintf("/api/v1/processes/%d/instances", dep.Key), `{"variables":`+tc.vars+`}`, "application/json")
			if code != http.StatusOK {
				t.Fatalf("create instance: %d %s", code, body)
			}
			if bytes.Contains(body, []byte(`"activeElementInstances":0`)) {
				t.Fatalf("instance completed despite a worker error: %s", body)
			}
		})
	}
}

// TestCSVImportServiceTask proves the in-process CSV-import worker (ADR-0087): a
// service task parses an uploaded CSV (the csvText variable) against a column layout
// (the columnConfig variable, as a preceding script task would set it) into rows and
// rowCount, entirely within the process — no ingestion endpoint.
func TestCSVImportServiceTask(t *testing.T) {
	ts := newTestServer(t)

	code, body := doReq(t, ts, http.MethodPost, "/api/v1/deployments", csvImportBPMN, "application/xml")
	if code != http.StatusOK {
		t.Fatalf("deploy: %d %s", code, body)
	}
	var dep struct {
		Key uint64 `json:"key"`
	}
	if err := json.Unmarshal(body, &dep); err != nil {
		t.Fatalf("decode deploy: %v", err)
	}

	// Seed the CSV text and the column layout as a script task / upload task would.
	start := `{"variables":{` +
		`"csvText":"email,group\nada@x.io,users\nbob,ops\n",` +
		`"columnConfig":{"columns":[{"name":"email"},{"name":"group"}]}}}`
	code, body = doReq(t, ts, http.MethodPost, fmt.Sprintf("/api/v1/processes/%d/instances", dep.Key), start, "application/json")
	if code != http.StatusOK {
		t.Fatalf("create instance: %d %s", code, body)
	}
	// The worker parsed and the instance ran to completion (no token parked).
	if !bytes.Contains(body, []byte(`"activeElementInstances":0`)) {
		t.Fatalf("instance did not complete (worker didn't run?): %s", body)
	}

	// Read the rows the worker wrote back at the process root.
	code, body = doReq(t, ts, http.MethodGet, "/api/v1/instances", "", "")
	if code != http.StatusOK {
		t.Fatalf("list instances: %d %s", code, body)
	}
	var instances []struct {
		Key uint64 `json:"key"`
	}
	if err := json.Unmarshal(body, &instances); err != nil {
		t.Fatalf("decode instances: %v", err)
	}
	if len(instances) != 1 {
		t.Fatalf("instances = %d, want 1", len(instances))
	}

	code, body = doReq(t, ts, http.MethodGet, fmt.Sprintf("/api/v1/instances/%d/variables", instances[0].Key), "", "")
	if code != http.StatusOK {
		t.Fatalf("get variables: %d %s", code, body)
	}
	dec := json.NewDecoder(bytes.NewReader(body))
	dec.UseNumber()
	var vars map[string]any
	if err := dec.Decode(&vars); err != nil {
		t.Fatalf("decode variables: %v (%s)", err, body)
	}
	if got, ok := vars["rowCount"].(json.Number); !ok || got.String() != "2" {
		t.Errorf("rowCount = %v (%T), want 2", vars["rowCount"], vars["rowCount"])
	}
	rows, ok := vars["rows"].([]any)
	if !ok || len(rows) != 2 {
		t.Fatalf("rows = %v (%T), want an array of 2", vars["rows"], vars["rows"])
	}
	first, ok := rows[0].(map[string]any)
	if !ok || first["email"] != "ada@x.io" || first["group"] != "users" {
		t.Errorf("rows[0] = %v, want {email:ada@x.io, group:users}", rows[0])
	}
}
