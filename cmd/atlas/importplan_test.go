package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// `atlas import` shows its plan and replaces nothing unasked
// (ADR-0467).

// copyPackage copies the example package into a directory of its own, so a test
// can add to it.
func copyPackage(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	entries, err := os.ReadDir(examplePackage)
	if err != nil {
		t.Fatalf("read the example: %v", err)
	}
	for _, e := range entries {
		raw, err := os.ReadFile(filepath.Join(examplePackage, e.Name()))
		if err != nil {
			t.Fatalf("read %s: %v", e.Name(), err)
		}
		writeFile(t, dir, e.Name(), string(raw))
	}
	return dir
}

// setConstants replaces what an application holds on a server.
func putConstants(t *testing.T, ts *httptest.Server, appID, body string) {
	t.Helper()
	req, _ := http.NewRequest(http.MethodPut, ts.URL+"/api/v1/applications/"+appID+"/constants", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	resp, err := ts.Client().Do(req)
	if err != nil || resp.StatusCode != http.StatusOK {
		t.Fatalf("set constants: %v %v", resp, err)
	}
	_ = resp.Body.Close()
}

// heldConstants reads what an application holds, by name.
func heldConstants(t *testing.T, ts *httptest.Server, appID string) map[string]string {
	t.Helper()
	code, raw := apiCall(t, ts, http.MethodGet, "/api/v1/applications/"+appID+"/constants", "")
	var body constantsBody
	if code != http.StatusOK || json.Unmarshal(raw, &body) != nil {
		t.Fatalf("constants = %d %s", code, raw)
	}
	out := map[string]string{}
	for _, c := range body.Constants {
		out[c.Name] = c.Value
	}
	return out
}

// changedHere is a server with the example installed, then worked on: a draft
// edited, and a constant holding this installation's value. It returns the server,
// the application's id and a package that moved the same draft on since, whose
// constants hold another installation's value and one this server lacks.
func changedHere(t *testing.T) (*httptest.Server, string, string, []string) {
	t.Helper()
	ts, _ := liveServer(t)
	installExample(t, ts)
	appID := applicationID(t, ts, exampleKey)
	raw, err := os.ReadFile(filepath.Join(examplePackage, "parkplatz-zuteilen.bpmn"))
	if err != nil {
		t.Fatalf("read the model: %v", err)
	}
	req, _ := http.NewRequest(http.MethodPost, ts.URL+"/api/v1/drafts?projectId="+appID,
		strings.NewReader(string(raw)+"<!-- hier geändert -->\n"))
	req.Header.Set("Content-Type", "application/xml")
	resp, err := ts.Client().Do(req)
	if err != nil || resp.StatusCode != http.StatusOK {
		t.Fatalf("edit the draft: %v %v", resp, err)
	}
	_ = resp.Body.Close()
	putConstants(t, ts, appID, `{"constants":[{"name":"TENANT_ID","type":"string","value":"int-01"}]}`)

	// The package moved the same process on since the last import: both sides moved.
	pkg := copyPackage(t)
	writeFile(t, pkg, "parkplatz-zuteilen.bpmn", string(raw)+"<!-- im Paket geändert -->\n")
	writeFile(t, pkg, packageConstants, `{"constants":[{"name":"TENANT_ID","type":"string","value":"prod-01"},{"name":"NEU","type":"number","value":"1"}]}`)
	args := []string{"--server", ts.URL, "--answers", writeAnswers(t, map[string]string{
		"zielgruppe": "Alle Mitarbeitenden", "zielgruppe-bau": "Fachbereich Bau",
		"geoportal-verantwortliche": "Geoportal-Team", "facility-management": "Fabienne Meier",
	})}
	return ts, appID, pkg, args
}

func draftXML(t *testing.T, ts *httptest.Server) string {
	t.Helper()
	_, raw := apiCall(t, ts, http.MethodGet, "/api/v1/drafts/proc_vd_parkplatz_zuteilen/xml", "")
	return string(raw)
}

// TestImportStopsAtWhatNobodyDecided: in a pipeline nobody can answer, so a change
// left undecided stops the run before the first write — every one named, with
// what tells the two versions apart — and the flags then decide them one by one.
func TestImportStopsAtWhatNobodyDecided(t *testing.T) {
	ts, appID, pkg, args := changedHere(t)

	var out bytes.Buffer
	err := runImport(append(args, pkg), &out)
	if err == nil {
		t.Fatalf("an import with undecided changes went through:\n%s", out.String())
	}
	for _, want := range []string{
		"2 artifacts differ from the server's and were not decided; nothing was imported",
		`process:proc_vd_parkplatz_zuteilen "Parkplatz: zuteilen"`,
		`constant:TENANT_ID: here string "int-01", in the package string "prod-01"`,
		"--overwrite REF or --keep REF",
	} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal lacks %q:\n%v", want, err)
		}
	}
	if !strings.Contains(draftXML(t, ts), "hier geändert") {
		t.Fatal("a refused import replaced the draft")
	}
	if held := heldConstants(t, ts, appID); held["TENANT_ID"] != "int-01" || held["NEU"] != "" {
		t.Fatalf("a refused import touched the constants: %v", held)
	}

	out.Reset()
	if err := runImport(append(args, "--dry-run", pkg), &out); err != nil {
		t.Fatalf("dry run: %v\n%s", err, out.String())
	}
	for _, want := range []string{"plan: 1 new, 20 unchanged, 0 behind, 0 ahead, 2 changed",
		`changed   process:proc_vd_parkplatz_zuteilen "Parkplatz: zuteilen" — undecided`,
		"new       constant:NEU", "dry run: nothing was written"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("the dry run lacks %q:\n%s", want, out.String())
		}
	}

	out.Reset()
	if err := runImport(append(args, "--overwrite", "process:proc_vd_parkplatz_zuteilen", "--keep", "constant:TENANT_ID", pkg), &out); err != nil {
		t.Fatalf("decided import: %v\n%s", err, out.String())
	}
	for _, want := range []string{") updated: 5 processes", "published release 2", `constants set: NEU (number) = "1"`,
		"constants kept as the server holds them: constant:TENANT_ID"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("output lacks %q:\n%s", want, out.String())
		}
	}
	if strings.Contains(draftXML(t, ts), "hier geändert") {
		t.Error("the draft chosen for overwriting kept the local version")
	}
	if held := heldConstants(t, ts, appID); held["TENANT_ID"] != "int-01" || held["NEU"] != "1" {
		t.Errorf("constants = %v, want this installation's TENANT_ID kept and NEU set", held)
	}

	for _, bad := range [][]string{{"--overwrite", "all", "--keep", "all"}, {"--overwrite", "process:x", "--keep", "process:x"}} {
		if err := runImport(append(append(args, bad...), pkg), &bytes.Buffer{}); err == nil || !strings.Contains(err.Error(), "contradict") && !strings.Contains(err.Error(), "both") {
			t.Errorf("%v = %v, want a contradiction refused", bad, err)
		}
	}
	if err := (newRefFlag()).Set("ohne-art"); err == nil {
		t.Error("a ref without a kind was accepted")
	}
}

// TestImportAsksAtATerminal: with somebody at the terminal, each change is asked
// for on its own, and keeping the server's version is what an empty answer does.
func TestImportAsksAtATerminal(t *testing.T) {
	ts, appID, pkg, args := changedHere(t)
	answering(t, "\ny\n")
	var out bytes.Buffer
	if err := runImport(append(args, pkg), &out); err != nil {
		t.Fatalf("import: %v\n%s", err, out.String())
	}
	if strings.Count(out.String(), "Replace it with the package's? [y/N]") != 2 {
		t.Errorf("not every change was asked:\n%s", out.String())
	}
	if !strings.Contains(out.String(), "kept as the server has them: process:proc_vd_parkplatz_zuteilen") {
		t.Errorf("output:\n%s", out.String())
	}
	if !strings.Contains(draftXML(t, ts), "hier geändert") {
		t.Error("the draft answered with keep was replaced")
	}
	if held := heldConstants(t, ts, appID); held["TENANT_ID"] != "prod-01" {
		t.Errorf("constants = %v, want TENANT_ID replaced as answered", held)
	}

	ts, _, pkg, args = changedHere(t)
	answering(t, "")
	if err := runImport(append(args, pkg), &bytes.Buffer{}); err == nil || !strings.Contains(err.Error(), "no answer was given; nothing was imported") {
		t.Fatalf("a terminal that answers nothing = %v", err)
	}
	if !strings.Contains(draftXML(t, ts), "hier geändert") {
		t.Error("an unanswered import replaced the draft")
	}
}

// TestConstantsAnAdministratorMustSet: setting a constant is an administrator's,
// and a token never carries that role. The import goes on, and names what is left
// to set rather than failing for it.
func TestConstantsAnAdministratorMustSet(t *testing.T) {
	refusing := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"artifacts":[{"ref":"constant:TENANT_ID","name":"TENANT_ID","status":"new","outcome":"created"}],"set":[],"notSet":["TENANT_ID"]}`))
	}))
	defer refusing.Close()
	c := &importClient{apiClient: newAPIClient(refusing.URL, "")}
	var out bytes.Buffer
	if err := c.installConstants(&out, "app1", []packageConstant{{Name: "TENANT_ID", Type: "string", Value: "prod-01"}}, ""); err != nil {
		t.Fatalf("installConstants: %v", err)
	}
	if !strings.Contains(out.String(), `constants not set — setting them is an administrator's`) ||
		!strings.Contains(out.String(), `TENANT_ID (string) = "prod-01"`) {
		t.Errorf("output:\n%s", out.String())
	}
	if err := c.installConstants(&out, "app1", nil, ""); err != nil {
		t.Errorf("nothing to set: %v", err)
	}
}

// TestAPackageUpdateNeedsNoAnswers: installed once and left alone on the server,
// an application is updated by the next version of its package without a question
// — in a pipeline too. Only what was worked on here is a question.
func TestAPackageUpdateNeedsNoAnswers(t *testing.T) {
	ts, _ := liveServer(t)
	installExample(t, ts)
	raw, err := os.ReadFile(filepath.Join(examplePackage, "parkplatz-zuteilen.bpmn"))
	if err != nil {
		t.Fatalf("read the model: %v", err)
	}
	pkg := copyPackage(t)
	writeFile(t, pkg, "parkplatz-zuteilen.bpmn", string(raw)+"<!-- Version 2 -->\n")
	var out bytes.Buffer
	args := []string{"--server", ts.URL, "--answers", writeAnswers(t, map[string]string{
		"zielgruppe": "Alle Mitarbeitenden", "zielgruppe-bau": "Fachbereich Bau",
		"geoportal-verantwortliche": "Geoportal-Team", "facility-management": "Fabienne Meier",
	}), pkg}
	if err := runImport(args, &out); err != nil {
		t.Fatalf("an update nobody worked on here: %v\n%s", err, out.String())
	}
	for _, want := range []string{"plan: 0 new, 20 unchanged, 1 behind, 0 ahead, 0 changed",
		`behind    process:proc_vd_parkplatz_zuteilen "Parkplatz: zuteilen" — updated`, ") updated: 5 processes", "published release 2"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("output lacks %q:\n%s", want, out.String())
		}
	}
	if !strings.Contains(draftXML(t, ts), "Version 2") {
		t.Error("the newer version was not taken")
	}
}
