package worker_test

import (
	"bytes"
	"context"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/pblumer/atlas/api"
	"github.com/pblumer/atlas/examples/testdaten"
	"github.com/pblumer/atlas/worker"
)

// The example "Mitarbeitende prüfen und korrigieren" from upload to corrected file
// (ADR-0548, S2), run as shipped: its model, its decision and its forms are read from
// examples/mitarbeitende-pruefen unchanged. Both correction paths end in
// apply-corrections, and the corrected file passes the last check.

// fix corrects an employee record the way a person would: an address in the domain, a
// known department, a work percentage in range, no exit before the entry. It changes
// only what is wrong, as a person does.
func fix(rec map[string]string) map[string]string {
	out := make(map[string]string, len(rec))
	for k, v := range rec {
		out[k] = v
	}
	if !strings.HasSuffix(out["mail"], "@beispiel.ch") {
		out["mail"] = "p" + out["personalnummer"] + "@beispiel.ch"
	}
	if out["abteilung"] == testdaten.UnknownDepartment {
		out["abteilung"] = "Informatik"
	}
	if p, err := strconv.Atoi(out["pensum"]); err != nil || p < 10 || p > 100 {
		out["pensum"] = "80"
	}
	if out["austritt"] != "" && out["austritt"] < out["eintritt"] {
		out["austritt"] = ""
	}
	return out
}

// correctionAtlas is a server with the example's decision, forms and model deployed;
// csvOffloaded runs apply-corrections on a Worker Instance instead of in process.
func correctionAtlas(t *testing.T, csvOffloaded bool) (*httptest.Server, uint64) {
	t.Helper()
	var opts []api.Option
	if csvOffloaded {
		opts = append(opts, api.WithOffloadedConnectorKinds([]string{"csv"}))
	}
	ts := exampleAtlas(t, opts...)
	var app struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(post(t, ts, "/api/v1/applications", `{"name":"Beispiele: Datenqualität"}`, "application/json"), &app); err != nil || app.ID == "" {
		t.Fatalf("application: %v", err)
	}
	post(t, ts, "/api/v1/dmn-models?handle=mitarbeitende-pruefen", string(exampleFile(t, "mitarbeitende-pruefen", "mitarbeitende-pruefen.dmn")), "application/xml")
	post(t, ts, "/api/v1/dmnrefs", `{"name":"MitarbeitendeGueltig","modelRef":"mitarbeitende-pruefen","projectId":"`+app.ID+`"}`, "application/json")
	for _, f := range []string{"mitarbeitende-hochladen", "datensatz-korrigieren", "datei-korrigieren"} {
		schema := exampleFile(t, "mitarbeitende-pruefen", f+".form.json")
		post(t, ts, "/api/v1/forms", fmt.Sprintf(`{"id":%q,"name":%q,"projectId":%q,"schema":%s}`, f, f, app.ID, schema), "application/json")
	}
	var dep struct {
		Key uint64 `json:"key"`
	}
	if err := json.Unmarshal(post(t, ts, "/api/v1/deployments?projectId="+app.ID, string(exampleFile(t, "mitarbeitende-pruefen", "mitarbeitende-korrigieren.bpmn")), "application/xml"), &dep); err != nil || dep.Key == 0 {
		t.Fatalf("deploy: %v", err)
	}
	post(t, ts, "/api/v1/processes/"+itoa(dep.Key)+"/instances", `{}`, "application/json")
	return ts, dep.Key
}

type openTask struct {
	Key                uint64 `json:"key"`
	ElementID          string `json:"elementId"`
	ElementInstanceKey uint64 `json:"elementInstanceKey"`
}

func openTasks(t *testing.T, ts *httptest.Server) []openTask {
	t.Helper()
	var tasks []openTask
	if err := json.Unmarshal(listRows(t, get(t, ts, "/api/v1/tasks")), &tasks); err != nil {
		t.Fatalf("tasks: %v", err)
	}
	return tasks
}

// waitTasks waits until n tasks of the element are open.
func waitTasks(t *testing.T, ts *httptest.Server, element string, n int) []openTask {
	t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	for {
		var at []openTask
		for _, task := range openTasks(t, ts) {
			if task.ElementID == element {
				at = append(at, task)
			}
		}
		if len(at) == n {
			return at
		}
		if time.Now().After(deadline) {
			t.Fatalf("%d open %s tasks, want %d", len(at), element, n)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func complete(t *testing.T, ts *httptest.Server, task openTask, vars map[string]any) {
	t.Helper()
	b, _ := json.Marshal(map[string]any{"variables": vars})
	post(t, ts, fmt.Sprintf("/api/v1/tasks/%d/complete", task.Key), string(b), "application/json")
}

func document(t *testing.T, ts *httptest.Server, ref any) [][]string {
	t.Helper()
	id, _ := ref.(map[string]any)["$document"].(string)
	resp, err := http.Get(ts.URL + "/api/v1/documents/" + id)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	rows, err := csv.NewReader(bytes.NewReader(bytes.TrimPrefix(body, []byte("\uFEFF")))).ReadAll()
	if err != nil || resp.StatusCode != http.StatusOK {
		t.Fatalf("document %s: %d %v", id, resp.StatusCode, err)
	}
	return rows
}

// finish checks how the instance ended: no findings left, and the file it ended with
// is the corrected copy, every record still there.
func finish(t *testing.T, ts *httptest.Server, n, replaced int) {
	t.Helper()
	waitFor(t, "the instance to end", func() bool { return runningInstances(t, ts) == 0 })
	vars := instanceVariables(t, ts)
	raw, _ := json.Marshal(vars["pruefung"])
	var sum struct{ Total, Invalid, Errors int }
	_ = json.Unmarshal(raw, &sum)
	if sum.Total != n || sum.Invalid != 0 || sum.Errors != 0 {
		t.Fatalf("last check = %s", raw)
	}
	k, _ := vars["korrigiert"].(map[string]any)
	if k["replaced"] != float64(replaced) || k["dropped"] != 0.0 || k["records"] != float64(n) {
		t.Fatalf("korrigiert = %v", k)
	}
	datei, _ := vars["datei"].(map[string]any)
	if datei["name"] != "mitarbeitende-korrigiert.csv" {
		t.Fatalf("the instance ended with %v, not the corrected file", datei)
	}
	if rows := document(t, ts, datei); len(rows) != n+1 || strings.Join(rows[0], ",") != strings.Join(testdaten.Columns, ",") {
		t.Fatalf("corrected file: %d rows, header %v", len(rows), rows[0])
	}
}

// Few findings: one token each. The 200 records the example ships carry 12; each is
// corrected in its task, re-checked at once, and applied in process.
func TestTheCorrectionExampleCorrectsFewFindingsTaskByTask(t *testing.T) {
	ts, _ := correctionAtlas(t, false)
	upload := waitTasks(t, ts, "hochladen", 1)[0]
	ref := post201(t, ts, "/api/v1/documents?name=mitarbeitende.csv", exampleFile(t, "mitarbeitende-pruefen", "mitarbeitende.csv"), "text/csv")
	complete(t, ts, upload, map[string]any{"mitarbeitende": json.RawMessage(ref)})

	tasks := waitTasks(t, ts, "k_bearbeiten", testdaten.Defective(200))
	// The first is answered wrong once: the re-check gives it back.
	for i, task := range tasks {
		var local map[string]any
		_ = json.Unmarshal(get(t, ts, "/api/v1/instances/"+itoa(task.ElementInstanceKey)+"/variables"), &local)
		rec := map[string]string{}
		for _, c := range testdaten.Columns {
			s, _ := local[c].(string)
			rec[c] = s
		}
		if i == 0 {
			wrong := fix(rec)
			wrong["mail"] = "immer-noch-falsch"
			complete(t, ts, task, stringMap(wrong))
			again := waitFor1(t, ts, task.ElementInstanceKey)
			complete(t, ts, again, stringMap(fix(rec)))
			continue
		}
		complete(t, ts, task, stringMap(fix(rec)))
	}
	finish(t, ts, 200, testdaten.Defective(200))
}

// Many findings: one file. 1,000 generated records carry 60, more than the model's 20;
// the failures document is downloaded, corrected and uploaded, and a Worker Instance
// applies it.
func TestTheCorrectionExampleCorrectsManyFindingsInAFile(t *testing.T) {
	ts, _ := correctionAtlas(t, true)
	upload := waitTasks(t, ts, "hochladen", 1)[0]
	ref := post201(t, ts, "/api/v1/documents?name=mitarbeitende.csv", testdaten.CSV(testdaten.Population(1000)), "text/csv")
	complete(t, ts, upload, map[string]any{"mitarbeitende": json.RawMessage(ref)})

	task := waitTasks(t, ts, "datei_korrigieren", 1)[0]
	vars := instanceVariables(t, ts)
	failures := document(t, ts, vars["befundeDatei"])
	if len(failures) != testdaten.Defective(1000)+1 || failures[0][len(failures[0])-1] != "_drop" {
		t.Fatalf("failures document: %d rows, header %v", len(failures), failures[0])
	}
	// Completing without a file gives the task back rather than failing.
	complete(t, ts, task, map[string]any{"akzeptieren": false})
	task = waitTasks(t, ts, "datei_korrigieren", 1)[0]

	var buf bytes.Buffer
	w := csv.NewWriter(&buf)
	_ = w.Write(failures[0])
	for _, row := range failures[1:] {
		rec := map[string]string{}
		for i, c := range failures[0] {
			rec[c] = row[i]
		}
		fixed := fix(rec)
		out := make([]string, len(failures[0]))
		for i, c := range failures[0] {
			out[i] = fixed[c]
		}
		_ = w.Write(out)
	}
	w.Flush()
	corrected := post201(t, ts, "/api/v1/documents?name=befunde-korrigiert.csv", buf.Bytes(), "text/csv")
	complete(t, ts, task, map[string]any{"korrekturen": json.RawMessage(corrected), "akzeptieren": false})

	runWorker(t, ts, "io.atlas.csv-import", func(ctx context.Context, j worker.Job) (map[string]any, error) {
		return mustConnectors(t, "csv")["io.atlas.csv-import"].Run(ctx, j)
	})
	finish(t, ts, 1000, testdaten.Defective(1000))
}

// Accepting the rest ends the instance with the findings named and the reason given.
func TestTheCorrectionExampleAcceptsTheRest(t *testing.T) {
	ts, _ := correctionAtlas(t, false)
	upload := waitTasks(t, ts, "hochladen", 1)[0]
	ref := post201(t, ts, "/api/v1/documents?name=mitarbeitende.csv", testdaten.CSV(testdaten.Population(500)), "text/csv")
	complete(t, ts, upload, map[string]any{"mitarbeitende": json.RawMessage(ref)})
	task := waitTasks(t, ts, "datei_korrigieren", 1)[0]
	complete(t, ts, task, map[string]any{"akzeptieren": true, "begruendung": "Bereinigung im Personalsystem geplant"})
	waitFor(t, "the instance to end", func() bool { return runningInstances(t, ts) == 0 })
	vars := instanceVariables(t, ts)
	if vars["begruendung"] != "Bereinigung im Personalsystem geplant" || vars["korrigiert"] != nil {
		t.Fatalf("vars = begruendung %v, korrigiert %v", vars["begruendung"], vars["korrigiert"])
	}
}

func stringMap(m map[string]string) map[string]any {
	out := make(map[string]any, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}

// waitFor1 waits for the correction task of the same iteration to open again after
// its re-check failed: a new task, the same body.
func waitFor1(t *testing.T, ts *httptest.Server, previous uint64) openTask {
	t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		for _, task := range openTasks(t, ts) {
			if task.ElementID == "k_bearbeiten" && task.ElementInstanceKey != previous {
				var local map[string]any
				_ = json.Unmarshal(get(t, ts, "/api/v1/instances/"+itoa(task.ElementInstanceKey)+"/variables"), &local)
				if local["mail"] == "immer-noch-falsch" {
					return task
				}
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("the rejected correction did not come back")
	return openTask{}
}
