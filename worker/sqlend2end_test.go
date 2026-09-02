package worker_test

import (
	"context"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/pblumer/atlas/worker"
)

// The whole ask in one test: a Microsoft SQL Server task, from a deployed model to a
// process variable holding the row, with no database anywhere.
//
// Every seam this crosses had a test of its own and none of them met. The compiler
// accepted the extension element, sqldb.Resolve produced a Job, the payload arm sent
// the fields, sqldb.Run executed against a fake — four halves, and nothing that would
// notice if the *names* stopped lining up between them. That is not hypothetical for
// this seam: the resolved-job-to-payload contract has already been broken twice
// (ad.Job.Connector, webscrape.Job.Format), each time compiling and deploying and
// leasing before handing the worker a zero value.
//
// So this runs the real thing: the engine reads the parameters variable up the scope
// chain and offloads the job, a worker built from an environment leases it, the mock
// answers the statement, and the process continues with the row in the variable the
// task named.
func TestAWorkerRunsAnMsSqlTaskEndToEnd(t *testing.T) {
	ts := liveAtlasWith(t, msSqlPositionalModel,
		`{"abteilung":"IT","params":["IT"]}`)

	runOneSQLJob(t, ts, `{"answers":[{
	  "statement":"SELECT id, mail FROM personen WHERE abteilung = @p1",
	  "params":["IT"],
	  "columns":["id","mail"],
	  "rows":[[7,"arno@example.com"],[9,"bea@example.com"]]
	}]}`)

	if running := runningInstances(t, ts); running != 0 {
		t.Errorf("%d instances still running, want 0 — the SQL job was not completed", running)
	}
	vars := instanceVariables(t, ts)
	rows, ok := vars["zeilen"].([]any)
	if !ok {
		t.Fatalf("zeilen = %#v, want the queried rows; the result never reached the process", vars["zeilen"])
	}
	if len(rows) != 2 {
		t.Fatalf("zeilen has %d rows, want 2: %#v", len(rows), rows)
	}
	first, ok := rows[0].(map[string]any)
	if !ok {
		t.Fatalf("row = %#v, want a column-keyed object", rows[0])
	}
	if first["mail"] != "arno@example.com" {
		t.Errorf("zeilen[0].mail = %#v, want the seeded address", first["mail"])
	}
	// An id must arrive as a number a FEEL expression can compare, not as the string
	// or float a JSON round trip through the payload could have made of it.
	if n, ok := first["id"].(float64); !ok || n != 7 {
		t.Errorf("zeilen[0].id = %#v, want the number 7", first["id"])
	}
}

// Named binding is the one thing SQL Server has that MariaDB and PostgreSQL do not, so
// it is the one part of this connector no other product's test can cover. It travels
// as a JSON object from a process variable, through the payload's `named` field, to
// sql.Named on the driver — three hops that each drop a map quietly if they are wrong.
func TestAnMsSqlTaskBindsNamedParametersEndToEnd(t *testing.T) {
	ts := liveAtlasWith(t, msSqlNamedModel,
		`{"params":{"id":42,"aktiv":false}}`)

	runOneSQLJob(t, ts, `{"answers":[
	  {"statement":"UPDATE personen SET aktiv = @aktiv WHERE id = @id","named":{"id":42,"aktiv":false},"affected":1},
	  {"statement":"UPDATE personen SET aktiv = @aktiv WHERE id = @id","affected":0}
	]}`)

	if running := runningInstances(t, ts); running != 0 {
		t.Errorf("%d instances still running, want 0", running)
	}
	// 1, not 0: the answer seeded for this exact binding is the one that ran, so the
	// names and the values both survived every hop. The fallback answering 0 is what
	// this test would report if any of them had not.
	if got := instanceVariables(t, ts)["betroffen"]; got != float64(1) {
		t.Errorf("betroffen = %#v, want 1 — the named parameters did not reach the driver by name", got)
	}
}

// A statement the mock cannot answer fails the job rather than completing it with
// nothing, and the failure reaches the operator as an incident against the task that
// asked. That is the mock's refusal seen from where it matters: a process that would
// have branched on an invented empty result stops instead.
func TestAnUnseededStatementRaisesAnIncident(t *testing.T) {
	ts := liveAtlasWith(t, msSqlPositionalModel,
		`{"abteilung":"IT","params":["IT"]}`)

	runOneSQLJob(t, ts, `{"answers":[{"statement":"SELECT 1","columns":["n"],"rows":[[1]]}]}`)

	if running := runningInstances(t, ts); running != 1 {
		t.Errorf("%d instances running, want the one whose job failed", running)
	}
	if _, ok := instanceVariables(t, ts)["zeilen"]; ok {
		t.Error("the task wrote a result variable for a statement the mock refused")
	}
}

// runOneSQLJob builds a worker the way an operator does — from an environment, with
// mock mode on and a seed file — and lets it work one round against the live server.
func runOneSQLJob(t *testing.T, ts *httptest.Server, seed string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "seed.json")
	if err := os.WriteFile(path, []byte(seed), 0o600); err != nil {
		t.Fatalf("write seed: %v", err)
	}
	env := map[string]string{
		"ATLAS_MSSQL_CONNECTORS": "hr-db",
		"ATLAS_MSSQL_MOCK":       "1",
		"ATLAS_MSSQL_MOCK_SEED":  path,
	}
	execs, err := worker.BuiltinConnectors(func(k string) string { return env[k] }, "mssql")
	if err != nil {
		t.Fatalf("BuiltinConnectors: %v", err)
	}
	w := worker.New(worker.Options{Server: ts.URL, ID: "mssql-1", Handlers: execs.Handlers})
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	if err := w.RunOnce(ctx); err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
}

// The parameters variable is a list, so it binds positionally against SQL Server's
// @p1 — the ordinary shape, and the one the modeler's placeholder text shows.
const msSqlPositionalModel = `<?xml version="1.0" encoding="UTF-8"?>
<bpmn:definitions xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL"
                  xmlns:atlas="http://atlas.dev/schema/1.0" id="defs">
  <bpmn:process id="abteilungsliste" isExecutable="true">
    <bpmn:startEvent id="s"/>
    <bpmn:serviceTask id="t">
      <bpmn:extensionElements>
        <atlas:mssqlConnector connector="hr-db" operation="query"
                              statement="SELECT id, mail FROM personen WHERE abteilung = @p1"
                              parametersVariable="params" resultVariable="zeilen"/>
      </bpmn:extensionElements>
    </bpmn:serviceTask>
    <bpmn:endEvent id="e"/>
    <bpmn:sequenceFlow id="f1" sourceRef="s" targetRef="t"/>
    <bpmn:sequenceFlow id="f2" sourceRef="t" targetRef="e"/>
  </bpmn:process>
</bpmn:definitions>`

// The parameters variable is an object, so it binds by name — accepted for SQL Server
// and refused for the other two products rather than flattened into an order nobody
// wrote (ADR-0173).
const msSqlNamedModel = `<?xml version="1.0" encoding="UTF-8"?>
<bpmn:definitions xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL"
                  xmlns:atlas="http://atlas.dev/schema/1.0" id="defs">
  <bpmn:process id="deaktivieren" isExecutable="true">
    <bpmn:startEvent id="s"/>
    <bpmn:serviceTask id="t">
      <bpmn:extensionElements>
        <atlas:mssqlConnector connector="hr-db" operation="execute"
                              statement="UPDATE personen SET aktiv = @aktiv WHERE id = @id"
                              parametersVariable="params" resultVariable="betroffen"/>
      </bpmn:extensionElements>
    </bpmn:serviceTask>
    <bpmn:endEvent id="e"/>
    <bpmn:sequenceFlow id="f1" sourceRef="s" targetRef="t"/>
    <bpmn:sequenceFlow id="f2" sourceRef="t" targetRef="e"/>
  </bpmn:process>
</bpmn:definitions>`
