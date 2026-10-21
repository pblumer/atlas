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

// needyBPMN reads TENANT_ID from its application, MAIL_DOMAIN and LANGUAGE (with a
// fallback) from the installation, and sends mail through the worker Postausgang.
const needyBPMN = `<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL" xmlns:atlas="http://atlas/schema/1.0">
  <process id="bedarf" name="Bedarf" isExecutable="true">
    <extensionElements>
      <atlas:constants>
        <atlas:constant name="TENANT_ID" type="string" source="application"/>
        <atlas:constant name="MAIL_DOMAIN" type="string" source="server"/>
        <atlas:constant name="LANGUAGE" type="string" source="server" value="de-CH"/>
      </atlas:constants>
    </extensionElements>
    <startEvent id="s"/>
    <serviceTask id="notify">
      <extensionElements><atlas:mailConnector connector="Postausgang" to="a@b.ch" subject="hi" body="hi"/></extensionElements>
    </serviceTask>
    <endEvent id="e"/>
    <sequenceFlow id="f1" sourceRef="s" targetRef="notify"/>
    <sequenceFlow id="f2" sourceRef="notify" targetRef="e"/>
  </process>
</definitions>`

// TestExportAndImportNameWhatTheTargetNeeds: the export lists what the processes read
// from the installation and the package does not carry, and the import's plan names
// what the target lacks of it, before anything is written or published. A constant
// the package carries a value for is the constants' import to report, not a need.
func TestExportAndImportNameWhatTheTargetNeeds(t *testing.T) {
	src, _ := liveServer(t)
	code, raw := apiCall(t, src, http.MethodPost, "/api/v1/applications", `{"name":"Bedarf"}`)
	var app struct {
		ID string `json:"id"`
	}
	if code != http.StatusOK || json.Unmarshal(raw, &app) != nil {
		t.Fatalf("create the application = %d %s", code, raw)
	}
	req, _ := http.NewRequest(http.MethodPost, src.URL+"/api/v1/drafts?projectId="+app.ID, strings.NewReader(needyBPMN))
	req.Header.Set("Content-Type", "application/xml")
	resp, err := src.Client().Do(req)
	if err != nil || resp.StatusCode != http.StatusOK {
		t.Fatalf("save the draft: %v %v", resp, err)
	}
	_ = resp.Body.Close()
	putConstants(t, src, app.ID, `{"constants":[{"name":"TENANT_ID","type":"string","value":"int-01"}]}`)

	dir := filepath.Join(t.TempDir(), "paket")
	var out bytes.Buffer
	if err := runExport([]string{"--server", src.URL, "--application", app.ID, dir}, &out); err != nil {
		t.Fatalf("export: %v\n%s", err, out.String())
	}
	for _, want := range []string{"the target needs, and the package does not carry:",
		"server constant MAIL_DOMAIN (string), read by bedarf: set it in the Console under Admin → Engine → Server constants",
		"server constant LANGUAGE (string, has a fallback), read by bedarf",
		`worker "Postausgang" (mail), read by bedarf: create a worker of that name and Worker Type under Workers`} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("export output lacks %q:\n%s", want, out.String())
		}
	}
	if strings.Contains(out.String(), "application constant TENANT_ID") {
		t.Errorf("the export names a constant the package carries as a need:\n%s", out.String())
	}

	dst, _ := liveServer(t)
	out.Reset()
	if err := runImport([]string{"--server", dst.URL, "--dry-run", dir}, &out); err != nil {
		t.Fatalf("dry run: %v\n%s", err, out.String())
	}
	for _, want := range []string{"this server lacks what the application needs",
		"server constant MAIL_DOMAIN (string), missing here: set it in the Console under Admin → Engine → Server constants",
		`worker "Postausgang" (mail), missing here: create a worker of that name and Worker Type under Workers`,
		"dry run: nothing was written"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("plan lacks %q:\n%s", want, out.String())
		}
	}
	// A fallback answers a missing constant, and TENANT_ID travels in konstanten.json.
	for _, unwanted := range []string{"LANGUAGE", "application constant TENANT_ID"} {
		if strings.Contains(out.String(), unwanted) {
			t.Errorf("plan names %s as lacking:\n%s", unwanted, out.String())
		}
	}
}

// TestAConsoleDownloadInstallsWithItsConstants: a Console download asked for with its
// constants, unpacked, is a package too. Its tree carries constants.json, and the
// import sends it along: the source import sets the value and says so.
func TestAConsoleDownloadInstallsWithItsConstants(t *testing.T) {
	src, _ := liveServer(t)
	code, raw := apiCall(t, src, http.MethodPost, "/api/v1/applications", `{"name":"Bedarf"}`)
	var app struct {
		ID string `json:"id"`
	}
	if code != http.StatusOK || json.Unmarshal(raw, &app) != nil {
		t.Fatalf("create the application = %d %s", code, raw)
	}
	req, _ := http.NewRequest(http.MethodPost, src.URL+"/api/v1/drafts?projectId="+app.ID, strings.NewReader(needyBPMN))
	req.Header.Set("Content-Type", "application/xml")
	resp, err := src.Client().Do(req)
	if err != nil || resp.StatusCode != http.StatusOK {
		t.Fatalf("save the draft: %v %v", resp, err)
	}
	_ = resp.Body.Close()
	putConstants(t, src, app.ID, `{"constants":[{"name":"TENANT_ID","type":"string","value":"int-01"}]}`)
	code, archive := apiCall(t, src, http.MethodGet, "/api/v1/applications/"+app.ID+"/source?constants=true", "")
	if code != http.StatusOK {
		t.Fatalf("download = %d", code)
	}
	files, err := unpackSource(archive)
	if err != nil {
		t.Fatalf("unpack: %v", err)
	}
	dir := t.TempDir()
	for _, f := range files {
		if err := os.MkdirAll(filepath.Join(dir, filepath.Dir(filepath.FromSlash(f.name))), 0o700); err != nil {
			t.Fatal(err)
		}
		writeFile(t, dir, filepath.FromSlash(f.name), string(f.data))
	}

	dst, _ := liveServer(t)
	var out bytes.Buffer
	if err := runImport([]string{"--server", dst.URL, dir}, &out); err != nil {
		t.Fatalf("import: %v\n%s", err, out.String())
	}
	if !strings.Contains(out.String(), "constants set: TENANT_ID") {
		t.Errorf("output:\n%s", out.String())
	}
	if got := heldConstants(t, dst, applicationID(t, dst, "bedarf")); got["TENANT_ID"] != "int-01" {
		t.Errorf("constants on the target = %v", got)
	}
}

// TestATargetRemembersWhatANameMeans: INT calls the audience "Fachbereich Bau",
// PROD calls it "FB Bau". The first import is answered once with --set, and PROD
// remembers it; the next import of the same package needs no answer at all.
func TestATargetRemembersWhatANameMeans(t *testing.T) {
	src, _ := liveServer(t)
	group := createdID(t, src, "/api/v1/groups", `{"name":"Fachbereich Bau"}`)
	code, raw := apiCall(t, src, http.MethodPost, "/api/v1/applications", `{"name":"Bau"}`)
	var app struct {
		ID string `json:"id"`
	}
	if code != http.StatusOK || json.Unmarshal(raw, &app) != nil {
		t.Fatalf("create the application = %d %s", code, raw)
	}
	req, _ := http.NewRequest(http.MethodPost, src.URL+"/api/v1/drafts?projectId="+app.ID, strings.NewReader(needyBPMN))
	req.Header.Set("Content-Type", "application/xml")
	resp, err := src.Client().Do(req)
	if err != nil || resp.StatusCode != http.StatusOK {
		t.Fatalf("save the draft: %v %v", resp, err)
	}
	_ = resp.Body.Close()
	doc := `{"catalogs":[{"id":"cat-bau","texts":{"de":"Bau"},"rank":4,"languages":["de"],"items":["helm"],"groups":["` + group + `"]}],
	  "products":[{"id":"helm","homeCatalog":"cat-bau","state":"active","texts":{"de":"Helm"},"approval":{"kind":"none"},"provisionProcess":"bedarf"}]}`
	if code, raw := apiCall(t, src, http.MethodPost, "/api/v1/catalogs/import", doc); code != http.StatusOK {
		t.Fatalf("set up the catalogue = %d %s", code, raw)
	}
	dir := filepath.Join(t.TempDir(), "paket")
	var out bytes.Buffer
	if err := runExport([]string{"--server", src.URL, "--application", app.ID, "--catalog", "cat-bau", dir}, &out); err != nil {
		t.Fatalf("export: %v\n%s", err, out.String())
	}

	dst, _ := liveServer(t)
	here := createdID(t, dst, "/api/v1/groups", `{"name":"FB Bau"}`)
	out.Reset()
	if err := runImport([]string{"--server", dst.URL, dir}, &out); err == nil || !strings.Contains(err.Error(), `fachbereich-bau: no group with the id or name "Fachbereich Bau"`) {
		t.Fatalf("a name the target does not carry = %v\n%s", err, out.String())
	}
	out.Reset()
	if err := runImport([]string{"--server", dst.URL, "--set", "fachbereich-bau=FB Bau", dir}, &out); err != nil {
		t.Fatalf("answered once: %v\n%s", err, out.String())
	}
	if !strings.Contains(out.String(), `remembered on this server: group "Fachbereich Bau" is `+here) {
		t.Errorf("output:\n%s", out.String())
	}
	out.Reset()
	if err := runImport([]string{"--server", dst.URL, dir}, &out); err != nil {
		t.Fatalf("the next import, unanswered: %v\n%s", err, out.String())
	}
	if !strings.Contains(out.String(), `fachbereich-bau: "Fachbereich Bau" is group `+here+`, as this server remembers`) ||
		strings.Contains(out.String(), "remembered on this server") {
		t.Errorf("output:\n%s", out.String())
	}
	_, cat := apiCall(t, dst, http.MethodGet, "/api/v1/catalogs/cat-bau", "")
	if !strings.Contains(string(cat), here) {
		t.Errorf("the catalogue on the target = %s, want its audience %s", cat, here)
	}
}
