package main

import (
	"bytes"
	"encoding/json"
	"maps"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// `atlas export` writes the package `atlas import` installs
// (ADR-0467). These tests run both
// against real servers, because the property that matters is the trip: built on
// one installation, installed on another, and the same package when it comes back.

const exampleKey = "beispiel-dienstleistungen-der-verwaltung"

// exampleDMN is a decision model filed in the example's application, so the trip
// carries one.
const exampleDMN = `<?xml version="1.0" encoding="UTF-8"?>
<definitions xmlns="https://www.omg.org/spec/DMN/20191111/MODEL/" id="parkdauer" name="Parkdauer" namespace="http://atlas/dmn">
  <decision id="Parkdauer" name="Parkdauer"><literalExpression id="le"><text>365</text></literalExpression></decision>
</definitions>`

// applicationID finds an application's id by its key.
func applicationID(t *testing.T, ts *httptest.Server, key string) string {
	t.Helper()
	code, raw := apiCall(t, ts, http.MethodGet, "/api/v1/applications", "")
	var apps []struct {
		ID  string `json:"id"`
		Key string `json:"key"`
	}
	if code != http.StatusOK || json.Unmarshal(raw, &apps) != nil {
		t.Fatalf("GET /api/v1/applications = %d %s", code, raw)
	}
	for _, a := range apps {
		if a.Key == key {
			return a.ID
		}
	}
	t.Fatalf("no application %s in %s", key, raw)
	return ""
}

// installExample puts the example shop on a server, as an operator would with
// `atlas import`, and files a decision model in its application. It returns the ids
// this server gave the groups and the person the shop names.
func installExample(t *testing.T, ts *httptest.Server) []string {
	t.Helper()
	ids := []string{
		createdID(t, ts, "/api/v1/groups", `{"name":"Alle Mitarbeitenden"}`),
		createdID(t, ts, "/api/v1/groups", `{"name":"Fachbereich Bau"}`),
		createdID(t, ts, "/api/v1/groups", `{"name":"Geoportal-Team"}`),
		createdID(t, ts, "/api/v1/users", `{"username":"fmeier","displayName":"Fabienne Meier","password":"a-long-password-1"}`),
	}
	answers := writeAnswers(t, map[string]string{
		"zielgruppe": "Alle Mitarbeitenden", "zielgruppe-bau": "Fachbereich Bau",
		"geoportal-verantwortliche": "Geoportal-Team", "facility-management": "Fabienne Meier",
	})
	var out bytes.Buffer
	if err := runImport([]string{"--server", ts.URL, "--answers", answers, examplePackage}, &out); err != nil {
		t.Fatalf("install the example: %v\n%s", err, out.String())
	}
	req, _ := http.NewRequest(http.MethodPost, ts.URL+"/api/v1/dmn-models?name=parkdauer", strings.NewReader(exampleDMN))
	req.Header.Set("Content-Type", "application/xml")
	resp, err := ts.Client().Do(req)
	if err != nil || resp.StatusCode != http.StatusOK {
		t.Fatalf("upload the model: %v %v", resp, err)
	}
	_ = resp.Body.Close()
	ref := `{"name":"Parkdauer","modelRef":"parkdauer","projectId":"` + applicationID(t, ts, exampleKey) + `"}`
	if code, raw := apiCall(t, ts, http.MethodPost, "/api/v1/dmnrefs", ref); code != http.StatusOK {
		t.Fatalf("file the decision = %d %s", code, raw)
	}
	return ids
}

// readPackage reads every file under dir, by slash path.
func readPackage(t *testing.T, dir string) map[string]string {
	t.Helper()
	files := map[string]string{}
	err := filepath.WalkDir(dir, func(p string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		rel, _ := filepath.Rel(dir, p)
		raw, err := os.ReadFile(p)
		files[filepath.ToSlash(rel)] = string(raw)
		return err
	})
	if err != nil {
		t.Fatalf("read %s: %v", dir, err)
	}
	return files
}

func exportArgs(ts *httptest.Server, dir string, extra ...string) []string {
	args := []string{"--server", ts.URL, "--application", exampleKey,
		"--catalog", "cat-verwaltung-dienste", "--catalog", "cat-fachbereich-bau"}
	return append(append(args, extra...), dir)
}

// TestExportThenImportMovesTheShopToAnotherServer is the trip the command exists
// for: an application with its forms, processes and decision model, and the shop
// bound to them, built on one server and installed on another whose groups and
// people carry the same names and other ids — and exported again from there, the
// same package.
func TestExportThenImportMovesTheShopToAnotherServer(t *testing.T) {
	src, _ := liveServer(t)
	srcIDs := installExample(t, src)
	putConstants(t, src, applicationID(t, src, exampleKey), `{"constants":[{"name":"TENANT_ID","type":"string","value":"test-01"}]}`)

	dir := filepath.Join(t.TempDir(), "paket")
	var out bytes.Buffer
	if err := runExport(exportArgs(src, dir), &out); err != nil {
		t.Fatalf("export: %v\n%s", err, out.String())
	}
	for _, want := range []string{
		`application "Beispiel: Dienstleistungen der Verwaltung" (` + exampleKey + `): 5 processes, 10 forms, 1 decisions, 1 decision models`,
		"constants: 1 in konstanten.json, as this server holds them",
		"catalogues: cat-verwaltung-dienste, cat-fachbereich-bau (3 products, published on import)",
		"questions: 4, answered in antworten.json with the names this server gives them",
		"atlas import --server <target> --answers " + filepath.Join(dir, packageAnswers) + " " + dir,
	} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("export output lacks %q:\n%s", want, out.String())
		}
	}

	pkg := readPackage(t, dir)
	for _, name := range []string{packageManifest, packageCatalogue, packageQuestions, packageAnswers, packageConstants, "decisions/parkdauer.dmn"} {
		if _, ok := pkg[name]; !ok {
			t.Errorf("the package lacks %s: %v", name, slices.Sorted(maps.Keys(pkg)))
		}
	}
	// What this server minted stays here: the groups and the person are questions,
	// answered with the names they carry everywhere.
	for _, id := range srcIDs {
		if strings.Contains(pkg[packageCatalogue], id) {
			t.Errorf("katalog.json carries the exporting server's id %s", id)
		}
	}
	var answers map[string]string
	if err := json.Unmarshal([]byte(pkg[packageAnswers]), &answers); err != nil {
		t.Fatalf("antworten.json: %v", err)
	}
	want := map[string]string{"alle-mitarbeitenden": "Alle Mitarbeitenden", "fachbereich-bau": "Fachbereich Bau",
		"geoportal-team": "Geoportal-Team", "fabienne-meier": "Fabienne Meier"}
	for name, value := range want {
		if answers[name] != value || !strings.Contains(pkg[packageCatalogue], "{{"+name+"}}") {
			t.Errorf("question %s: answer %q, in katalog.json %v; want %q", name, answers[name],
				strings.Contains(pkg[packageCatalogue], "{{"+name+"}}"), value)
		}
	}
	var questions map[string]packageQuestion
	if err := json.Unmarshal([]byte(pkg[packageQuestions]), &questions); err != nil {
		t.Fatalf("fragen.json: %v", err)
	}
	if q := questions["fabienne-meier"]; q.Kind != "account" || !strings.Contains(q.Label["de"], "genehmigt «Parkplatz»") ||
		!strings.Contains(q.Label["en"], `approves "Parking space"`) {
		t.Errorf("the approver's question = %+v", q)
	}
	if q := questions["fachbereich-bau"]; q.Kind != "group" || !strings.Contains(q.Label["de"], "Zielgruppe des Katalogs «Fachbereich Bau»") {
		t.Errorf("the audience's question = %+v", q)
	}
	// The processes are the application's, and the application carries them; the
	// shop does not carry them a second time.
	if strings.Contains(pkg[packageCatalogue], `"processes"`) {
		t.Errorf("katalog.json carries processes the application already does:\n%s", pkg[packageCatalogue])
	}

	dst, _ := liveServer(t)
	geo := createdID(t, dst, "/api/v1/groups", `{"name":"Geoportal-Team"}`)
	bau := createdID(t, dst, "/api/v1/groups", `{"name":"Fachbereich Bau"}`)
	createdID(t, dst, "/api/v1/groups", `{"name":"Alle Mitarbeitenden"}`)
	createdID(t, dst, "/api/v1/users", `{"username":"fabienne","displayName":"Fabienne Meier","password":"a-long-password-2"}`)
	out.Reset()
	if err := runImport([]string{"--server", dst.URL, "--answers", filepath.Join(dir, packageAnswers), dir}, &out); err != nil {
		t.Fatalf("import on the second server: %v\n%s", err, out.String())
	}
	for _, want := range []string{"created: 5 processes, 10 forms, 1 decisions, 1 decision models", "catalogues published: 2",
		`constants set: TENANT_ID (string) = "test-01"`} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("import output lacks %q:\n%s", want, out.String())
		}
	}
	code, raw := apiCall(t, dst, http.MethodGet, "/api/v1/catalogs/cat-fachbereich-bau", "")
	var cat struct {
		Groups []string `json:"groups"`
	}
	if code != http.StatusOK || json.Unmarshal(raw, &cat) != nil || len(cat.Groups) != 1 || cat.Groups[0] != bau {
		t.Fatalf("the building department's catalogue on the second server = %d %s, want its audience %s", code, raw, bau)
	}
	code, raw = apiCall(t, dst, http.MethodGet, "/api/v1/catalogs/cat-fachbereich-bau/document", "")
	if code != http.StatusOK || !strings.Contains(string(raw), `"ref":"`+geo+`"`) {
		t.Errorf("the geoportal's approvers on the second server = %d %s, want %s", code, raw, geo)
	}

	// The same package comes back.
	again := filepath.Join(t.TempDir(), "zurueck")
	out.Reset()
	if err := runExport(exportArgs(dst, again), &out); err != nil {
		t.Fatalf("export from the second server: %v\n%s", err, out.String())
	}
	back := readPackage(t, again)
	if !slices.Equal(slices.Sorted(maps.Keys(pkg)), slices.Sorted(maps.Keys(back))) {
		t.Fatalf("the files differ: %v vs %v", slices.Sorted(maps.Keys(pkg)), slices.Sorted(maps.Keys(back)))
	}
	for name, content := range pkg {
		if back[name] != content {
			t.Errorf("%s changed on the trip:\n--- first\n%s\n--- back\n%s", name, content, back[name])
		}
	}

	// A question somebody reworded keeps its wording when the package is exported
	// into its own directory again; the rest are written afresh.
	questions["alle-mitarbeitenden"] = packageQuestion{Kind: "group",
		Label: map[string]string{"de": "Wer bestellt?", "en": "Who orders?"}}
	raw, _ = json.Marshal(questions)
	writeFile(t, dir, packageQuestions, string(raw))
	out.Reset()
	if err := runExport(exportArgs(src, dir), &out); err != nil {
		t.Fatalf("export into the earlier package: %v\n%s", err, out.String())
	}
	var reworded map[string]packageQuestion
	if err := json.Unmarshal([]byte(readPackage(t, dir)[packageQuestions]), &reworded); err != nil {
		t.Fatalf("fragen.json: %v", err)
	}
	if reworded["alle-mitarbeitenden"].Label["de"] != "Wer bestellt?" || reworded["fabienne-meier"].Kind != "account" {
		t.Errorf("questions after a second export = %+v", reworded)
	}
}

// TestExportRefusesADirectoryThatIsNotAPackage: a directory with something in it
// that no export wrote is somebody's, and the command does not write into it — nor
// asks the server anything first.
func TestExportRefusesADirectoryThatIsNotAPackage(t *testing.T) {
	ts, calls := liveServer(t)
	dir := t.TempDir()
	writeFile(t, dir, "notizen.txt", "meins")
	err := runExport([]string{"--server", ts.URL, "--application", "x", dir}, &bytes.Buffer{})
	if err == nil || !strings.Contains(err.Error(), "is not empty and holds no package") {
		t.Fatalf("err = %v", err)
	}
	if got := readPackage(t, dir); len(got) != 1 || got["notizen.txt"] != "meins" {
		t.Errorf("the directory was touched: %v", got)
	}
	for _, c := range calls.calls {
		if strings.HasPrefix(c, "GET /api/v1/applications") {
			t.Errorf("a refused export asked the server: %s", c)
		}
	}

	file := filepath.Join(t.TempDir(), "datei")
	writeFile(t, filepath.Dir(file), "datei", "")
	if err := runExport([]string{"--application", "x", file}, &bytes.Buffer{}); err == nil || !strings.Contains(err.Error(), "not a directory") {
		t.Fatalf("a file as the directory = %v", err)
	}
	if err := runExport([]string{"--application", "x"}, &bytes.Buffer{}); err == nil || !strings.Contains(err.Error(), "name the directory") {
		t.Fatalf("no directory = %v", err)
	}
	if err := runExport([]string{t.TempDir()}, &bytes.Buffer{}); err == nil || !strings.Contains(err.Error(), "name the application") {
		t.Fatalf("no application = %v", err)
	}
}

// mkApplication creates an application with one draft in it and returns its id.
func mkApplication(t *testing.T, ts *httptest.Server, name, processID string) string {
	t.Helper()
	id := createdApp(t, ts, name)
	xml := strings.Replace(scenarioBPMN, `id="approval"`, `id="`+processID+`"`, 1)
	req, _ := http.NewRequest(http.MethodPost, ts.URL+"/api/v1/drafts?projectId="+id, strings.NewReader(xml))
	req.Header.Set("Content-Type", "application/xml")
	resp, err := ts.Client().Do(req)
	if err != nil || resp.StatusCode != http.StatusOK {
		t.Fatalf("save draft: %v %v", resp, err)
	}
	_ = resp.Body.Close()
	return id
}

func createdApp(t *testing.T, ts *httptest.Server, name string) string {
	t.Helper()
	code, raw := apiCall(t, ts, http.MethodPost, "/api/v1/applications", `{"name":"`+name+`"}`)
	var out struct {
		ID string `json:"id"`
	}
	if code != http.StatusOK || json.Unmarshal(raw, &out) != nil || out.ID == "" {
		t.Fatalf("create application = %d %s", code, raw)
	}
	return out.ID
}

// TestExportReplacesAnEarlierExport: exporting into the package an earlier export
// wrote is how a package is kept in git. What the earlier manifest named and the
// application no longer has goes; a README beside it stays, and nothing outside the
// directory is touched whatever the old manifest says.
func TestExportReplacesAnEarlierExport(t *testing.T) {
	ts, _ := liveServer(t)
	mkApplication(t, ts, "Genehmigung", "pkg-genehmigung")
	parent := t.TempDir()
	dir := filepath.Join(parent, "paket")
	if err := os.MkdirAll(filepath.Join(dir, "processes"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	writeFile(t, parent, "draussen.txt", "bleibt")
	writeFile(t, dir, packageManifest, `{"formatVersion":1,"key":"genehmigung","name":"Genehmigung",
	  "processes":[{"id":"alt","path":"processes/alt.bpmn"},{"id":"x","path":"../draussen.txt"}]}`)
	writeFile(t, filepath.Join(dir, "processes"), "alt.bpmn", "<alt/>")
	writeFile(t, dir, "README.md", "unser Paket")
	writeFile(t, dir, packageCatalogue, "{}")
	writeFile(t, dir, packageAnswers, "{}")

	var out bytes.Buffer
	if err := runExport([]string{"--server", ts.URL, "--application", "Genehmigung", dir}, &out); err != nil {
		t.Fatalf("export: %v\n%s", err, out.String())
	}
	got := readPackage(t, dir)
	for _, gone := range []string{"processes/alt.bpmn", packageCatalogue, packageAnswers} {
		if _, ok := got[gone]; ok {
			t.Errorf("%s is still there", gone)
		}
	}
	if got["README.md"] != "unser Paket" || !strings.Contains(got[packageManifest], "pkg-genehmigung") {
		t.Errorf("package = %v", slices.Sorted(maps.Keys(got)))
	}
	if raw, err := os.ReadFile(filepath.Join(parent, "draussen.txt")); err != nil || string(raw) != "bleibt" {
		t.Errorf("a file outside the package was touched: %q %v", raw, err)
	}
	if !strings.Contains(out.String(), "no catalogue: the package installs the application alone") {
		t.Errorf("output:\n%s", out.String())
	}
}

// TestExportNamesWhatThePackageCannotCarry: a shop can be bound to more than one
// application holds. What can travel travels — a process deployed outside the
// application rides in katalog.json — and what cannot is named, with what it does
// on the target, rather than left for the import to discover.
func TestExportNamesWhatThePackageCannotCarry(t *testing.T) {
	ts, _ := liveServer(t)
	mkApplication(t, ts, "Shop", "pkg-in-app")
	elsewhere := strings.Replace(scenarioBPMN, `id="approval"`, `id="pkg-elsewhere"`, 1)
	if code, raw := apiCall(t, ts, http.MethodPost, "/api/v1/deployments", elsewhere); code != http.StatusOK && code != http.StatusCreated {
		t.Fatalf("deploy = %d %s", code, raw)
	}
	group := createdID(t, ts, "/api/v1/groups", `{"name":"Einkauf"}`)
	doc := `{"catalogs":[
	    {"id":"cat-a","texts":{"de":"Katalog A","en":"Catalogue A"},"rank":1,"languages":["de"],"items":["p1","p2"],
	     "groups":["` + group + `","geloescht"]},
	    {"id":"cat-b","texts":{"de":"Katalog B"},"rank":2,"languages":["de"],"items":["p2"]}],
	  "products":[
	    {"id":"p1","homeCatalog":"cat-a","state":"active","texts":{"de":"Eins mit {{wert}}"},
	     "approval":{"kind":"role","ref":"` + group + `"},"eligible":["` + group + `"],
	     "provisionProcess":"pkg-in-app","deprovisionProcess":"pkg-elsewhere","configForm":"lose-form"},
	    {"id":"p2","homeCatalog":"cat-b","state":"active","texts":{"de":"Zwei"},"approval":{"kind":"none"},
	     "provisionProcess":"pkg-missing","deprovisionProcess":"pkg-in-app"}]}`
	if code, raw := apiCall(t, ts, http.MethodPost, "/api/v1/catalogs/import", doc); code != http.StatusOK {
		t.Fatalf("import the shop = %d %s", code, raw)
	}

	dir := filepath.Join(t.TempDir(), "paket")
	var out bytes.Buffer
	if err := runExport([]string{"--server", ts.URL, "--application", "Shop", "--catalog", "cat-a", dir}, &out); err != nil {
		t.Fatalf("export: %v\n%s", err, out.String())
	}
	for _, want := range []string{
		"process pkg-elsewhere travels in katalog.json: a product is bound to it, and it is not in the application",
		"warning: process pkg-missing is bound to a product and deployed nowhere on this server",
		"warning: form lose-form is asked by product p1 and is not in the application",
		"warning: product p2 is maintained in catalogue cat-b, which the package does not carry",
		`warning: group geloescht is not in this server's directory; geloescht in fragen.json has no answer`,
		"catalogue cat-a has no release here, so the import does not publish",
	} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("output lacks %q:\n%s", want, out.String())
		}
	}
	pkg := readPackage(t, dir)
	var shop struct {
		Publish   bool `json:"publish"`
		Processes []struct {
			ID string `json:"id"`
		} `json:"processes"`
		MissingProcesses []string `json:"missingProcesses"`
	}
	if err := json.Unmarshal([]byte(pkg[packageCatalogue]), &shop); err != nil {
		t.Fatalf("katalog.json: %v", err)
	}
	if shop.Publish || len(shop.Processes) != 1 || shop.Processes[0].ID != "pkg-elsewhere" ||
		!slices.Equal(shop.MissingProcesses, []string{"pkg-missing"}) {
		t.Errorf("katalog.json = %+v", shop)
	}
	// The BPMN stays readable in the file: no HTML escaping of its angle brackets.
	if !strings.Contains(pkg[packageCatalogue], `<process id=\"pkg-elsewhere\"`) {
		t.Errorf("the process is not carried as readable XML:\n%s", pkg[packageCatalogue])
	}

	var answers map[string]string
	var questions map[string]packageQuestion
	if json.Unmarshal([]byte(pkg[packageAnswers]), &answers) != nil || json.Unmarshal([]byte(pkg[packageQuestions]), &questions) != nil {
		t.Fatalf("answers or questions are not JSON:\n%s\n%s", pkg[packageAnswers], pkg[packageQuestions])
	}
	// A text that looks like a question keeps its words: it is asked as one, and
	// answered with itself.
	if questions["wert"].Kind != "text" || answers["wert"] != "{{wert}}" {
		t.Errorf("the literal placeholder: question %+v, answer %q", questions["wert"], answers["wert"])
	}
	if _, answered := answers["geloescht"]; answered || questions["geloescht"].Kind != "group" {
		t.Errorf("the group nobody knows: question %+v, answered %v", questions["geloescht"], answered)
	}
	if answers["einkauf"] != "Einkauf" || strings.Count(pkg[packageCatalogue], "{{einkauf}}") != 3 {
		t.Errorf("the group used three times is one question: %q\n%s", answers["einkauf"], pkg[packageCatalogue])
	}
	if !strings.Contains(questions["einkauf"].Label["de"], "Zielgruppe des Katalogs «Katalog A»; genehmigt «Eins mit {{wert}}»; darf «Eins mit {{wert}}» erhalten") {
		t.Errorf("the group's question names each use: %+v", questions["einkauf"])
	}
}

// TestExportFindsTheApplication: by id, by key or by a name only one application
// carries; any other is refused with what would settle it.
func TestExportFindsTheApplication(t *testing.T) {
	ts, _ := liveServer(t)
	one := mkApplication(t, ts, "Doppelt", "pkg-doppelt-1")
	createdApp(t, ts, "Doppelt")
	c := &exportClient{apiClient: newAPIClient(ts.URL, "")}
	if app, err := c.application(one); err != nil || app.ID != one {
		t.Fatalf("by id = %+v, %v", app, err)
	}
	if _, err := c.application("Doppelt"); err == nil || !strings.Contains(err.Error(), "2 applications are named \"Doppelt\"") {
		t.Fatalf("an ambiguous name = %v", err)
	}
	if _, err := c.application("niemand"); err == nil || !strings.Contains(err.Error(), "no application with the key, id or name \"niemand\"") {
		t.Fatalf("an unknown application = %v", err)
	}
	if err := runExport([]string{"--server", ts.URL, "--application", one, filepath.Join(t.TempDir(), "p")}, &bytes.Buffer{}); err != nil {
		t.Fatalf("export by id: %v", err)
	}
	// The export derived the key, so the key finds it now.
	code, raw := apiCall(t, ts, http.MethodGet, "/api/v1/applications", "")
	if code != http.StatusOK || !strings.Contains(string(raw), `"key":"doppelt"`) {
		t.Fatalf("applications = %d %s", code, raw)
	}
	if app, err := c.application("doppelt"); err != nil || app.ID != one {
		t.Fatalf("by key = %+v, %v", app, err)
	}
	err := runExport([]string{"--server", ts.URL, "--application", one, "--catalog", "nirgends", filepath.Join(t.TempDir(), "p")}, &bytes.Buffer{})
	if err == nil || !strings.Contains(err.Error(), "GET /api/v1/catalogs/nirgends/document?processes=true: 404") {
		t.Fatalf("an unknown catalogue = %v", err)
	}
}

// TestTheShopHandbookDocumentsEveryExportFlag holds the handbook's table of the
// export's flags to the flags runExport defines, as the import's is held.
func TestTheShopHandbookDocumentsEveryExportFlag(t *testing.T) {
	defined := commandFlags(t, "exportpkg.go", "runExport")
	documented := documentedFlags(t, shopHandbook, "export-flags")
	for name := range defined {
		if !documented[name] {
			t.Errorf(`atlas export defines --%s, but the shop handbook's table (<table id="export-flags">) does not list it`, name)
		}
	}
	for name := range documented {
		if !defined[name] {
			t.Errorf("the shop handbook's table lists --%s, which atlas export does not define", name)
		}
	}
}
