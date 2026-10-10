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

// The administration-services example is the package `atlas import` was written
// for (ADR-0437): an application with
// five processes and ten forms, and the shop they serve.
const examplePackage = "../../examples/verwaltung-dienstleistungen"

// apiCall makes one JSON request against a test server and returns the status and
// body.
func apiCall(t *testing.T, ts *httptest.Server, method, path, body string) (int, []byte) {
	t.Helper()
	req, _ := http.NewRequest(method, ts.URL+path, strings.NewReader(body))
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := ts.Client().Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, path, err)
	}
	defer func() { _ = resp.Body.Close() }()
	var buf bytes.Buffer
	_, _ = buf.ReadFrom(resp.Body)
	return resp.StatusCode, buf.Bytes()
}

// createdID creates a group or a user and returns its id.
func createdID(t *testing.T, ts *httptest.Server, path, body string) string {
	t.Helper()
	code, raw := apiCall(t, ts, http.MethodPost, path, body)
	var out struct {
		ID string `json:"id"`
	}
	if code != http.StatusCreated || json.Unmarshal(raw, &out) != nil || out.ID == "" {
		t.Fatalf("POST %s = %d %s", path, code, raw)
	}
	return out.ID
}

// writeAnswers writes an answers file and returns its path.
func writeAnswers(t *testing.T, answers map[string]string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "answers.json")
	raw, _ := json.Marshal(answers)
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatalf("write answers: %v", err)
	}
	return path
}

// TestImportInstallsTheExampleShopAndAgain: the example goes in from a terminal as
// it goes in from the shop handbook — application, publish, catalogue — with the
// audiences and approvers answered by name, so one answers file serves every
// server. A second run updates what the first created rather than adding beside
// it.
func TestImportInstallsTheExampleShopAndAgain(t *testing.T) {
	ts, _ := liveServer(t)
	alle := createdID(t, ts, "/api/v1/groups", `{"name":"Alle Mitarbeitenden"}`)
	bau := createdID(t, ts, "/api/v1/groups", `{"name":"Fachbereich Bau"}`)
	geo := createdID(t, ts, "/api/v1/groups", `{"name":"Geoportal-Team"}`)
	fm := createdID(t, ts, "/api/v1/users",
		`{"username":"fmeier","displayName":"Fabienne Meier","password":"a-long-password-1"}`)
	answers := writeAnswers(t, map[string]string{
		"zielgruppe": "Alle Mitarbeitenden", "zielgruppe-bau": "Fachbereich Bau",
		"geoportal-verantwortliche": geo, "facility-management": "nobody",
	})
	args := []string{"--server", ts.URL, "--answers", answers, "--set", "facility-management=Fabienne Meier", examplePackage}

	var out bytes.Buffer
	if err := runImport(args, &out); err != nil {
		t.Fatalf("first import: %v\n%s", err, out.String())
	}
	for _, want := range []string{
		`application "Beispiel: Dienstleistungen der Verwaltung" (beispiel-dienstleistungen-der-verwaltung) created: 5 processes, 10 forms, 0 decisions`,
		"published release 1: 5 processes",
		`zielgruppe: "Alle Mitarbeitenden" is group ` + alle,
		`facility-management: "Fabienne Meier" is user ` + fm,
		"shop created: catalog:cat-verwaltung-dienste, catalog:cat-fachbereich-bau, product:vd-zutrittsbadge",
		"catalogues published: 2",
	} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("first import output lacks %q:\n%s", want, out.String())
		}
	}
	if strings.Contains(out.String(), "geoportal-verantwortliche:") {
		t.Errorf("an answer given as an id is not a resolution worth a line:\n%s", out.String())
	}

	code, raw := apiCall(t, ts, http.MethodGet, "/api/v1/catalogs/cat-fachbereich-bau", "")
	var cat struct {
		Groups []string `json:"groups"`
	}
	if code != http.StatusOK || json.Unmarshal(raw, &cat) != nil || len(cat.Groups) != 1 || cat.Groups[0] != bau {
		t.Fatalf("the building department's catalogue = %d %s, want its audience %s", code, raw, bau)
	}
	if bytes.Contains(raw, []byte("{{")) {
		t.Fatalf("a placeholder was imported literally: %s", raw)
	}

	out.Reset()
	if err := runImport(args, &out); err != nil {
		t.Fatalf("second import: %v\n%s", err, out.String())
	}
	for _, want := range []string{") updated: 5 processes", "published release 2", "shop updated: catalog:cat-verwaltung-dienste"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("second import output lacks %q:\n%s", want, out.String())
		}
	}
}

// TestImportRefusesBeforeItWrites: an unanswered question, an answer the directory
// does not have and a name two people share are all reported at once — and before
// the application goes in, so the server is left as it was.
func TestImportRefusesBeforeItWrites(t *testing.T) {
	ts, calls := liveServer(t)
	createdID(t, ts, "/api/v1/users", `{"username":"kim1","displayName":"Kim Muster","password":"a-long-password-1"}`)
	createdID(t, ts, "/api/v1/users", `{"username":"kim2","displayName":"Kim Muster","password":"a-long-password-2"}`)

	var out bytes.Buffer
	err := runImport([]string{"--server", ts.URL,
		"--set", "zielgruppe=Niemand", "--set", "facility-management=Kim Muster", examplePackage}, &out)
	if err == nil {
		t.Fatalf("an import with open questions went through:\n%s", out.String())
	}
	for _, want := range []string{
		"nothing was imported",
		`zielgruppe: no group with the id or name "Niemand"`,
		`facility-management: 2 users are named "Kim Muster"`,
		"zielgruppe-bau is not answered: Audience of \"Building department\" (--set zielgruppe-bau=...)",
		"geoportal-verantwortliche is not answered",
	} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal lacks %q:\n%v", want, err)
		}
	}
	for _, c := range calls.calls {
		if strings.HasPrefix(c, "POST ") && c != "POST /api/v1/drafts" && c != "POST /api/v1/users" {
			t.Errorf("a refused import wrote: %s", c)
		}
	}
}

// TestImportOfAnApplicationWithoutAShop: a package need not carry a shop. Its
// application goes in and is published, and answers given anyway are said to be
// unused rather than silently dropped.
func TestImportOfAnApplicationWithoutAShop(t *testing.T) {
	ts, _ := liveServer(t)
	dir := t.TempDir()
	writeFile(t, dir, "approval.bpmn", strings.Replace(scenarioBPMN, `id="approval"`, `id="pkg-approval"`, 1))
	writeFile(t, dir, packageManifest, `{"formatVersion":1,"key":"genehmigung","name":"Genehmigung",
	  "processes":[{"id":"pkg-approval","name":"Approval","path":"approval.bpmn"}],"forms":[],"decisions":[]}`)
	writeFile(t, dir, "README.md", "not part of the application")

	var out bytes.Buffer
	if err := runImport([]string{"--server", ts.URL, "--set", "zielgruppe=x", dir}, &out); err != nil {
		t.Fatalf("import: %v\n%s", err, out.String())
	}
	for _, want := range []string{"has no katalog.json, so its answers are not used", `"Genehmigung" (genehmigung) created: 1 processes, 0 forms`, "published release 1: 1 processes"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("output lacks %q:\n%s", want, out.String())
		}
	}
	if strings.Contains(out.String(), "shop") || strings.Contains(out.String(), "catalogues") {
		t.Errorf("a package without a shop reported a catalogue:\n%s", out.String())
	}
}

func writeFile(t *testing.T, dir, name, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o600); err != nil {
		t.Fatalf("write %s: %v", name, err)
	}
}

// TestAPackageIsReadWholeOrNotAtAll: what is wrong with the package itself is said
// before the server is asked anything.
func TestAPackageIsReadWholeOrNotAtAll(t *testing.T) {
	manifest := func(path string) string {
		return `{"formatVersion":1,"key":"k","name":"n","processes":[{"id":"p","path":"` + path + `"}]}`
	}
	cases := []struct {
		name, want string
		files      map[string]string
	}{
		{"no manifest", "is not a package", map[string]string{}},
		{"a manifest that is not JSON", "atlas.json", map[string]string{packageManifest: "{"}},
		{"a file outside the package", "outside the package", map[string]string{packageManifest: manifest("../x.bpmn")}},
		{"a file the package lacks", "names gone.bpmn", map[string]string{packageManifest: manifest("gone.bpmn")}},
		{"questions that are not JSON", "fragen.json", map[string]string{packageManifest: `{"formatVersion":1}`, packageQuestions: "["}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			for name, content := range tc.files {
				writeFile(t, dir, name, content)
			}
			_, err := readImportPackage(dir)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want it to name %q", err, tc.want)
			}
		})
	}

	if err := runImport([]string{}, &bytes.Buffer{}); err == nil || !strings.Contains(err.Error(), "name the package") {
		t.Fatalf("no package named = %v", err)
	}
	if err := runImport([]string{"--answers", filepath.Join(t.TempDir(), "none.json"), examplePackage}, &bytes.Buffer{}); err == nil ||
		!strings.Contains(err.Error(), "read answers") {
		t.Fatalf("a missing answers file = %v", err)
	}
	bad := filepath.Join(t.TempDir(), "answers.json")
	writeFile(t, filepath.Dir(bad), "answers.json", `["not","an","object"]`)
	if err := runImport([]string{"--answers", bad, examplePackage}, &bytes.Buffer{}); err == nil ||
		!strings.Contains(err.Error(), "is not a JSON object of answers") {
		t.Fatalf("an answers file that is not an object = %v", err)
	}
	if err := (answerFlags{}).Set("no-equals-sign"); err == nil {
		t.Fatal("--set without = was accepted")
	}
}

// TestAnAnswerCannotBreakTheDocument: an answer goes in as the inside of a JSON
// string, so a quote in it is a quote in the value and not the end of the field.
func TestAnAnswerCannotBreakTheDocument(t *testing.T) {
	doc := []byte(`{"catalogs":[{"id":"c","groups":["{{g}}"]}]}`)
	filled, err := fillDocument(doc, map[string]string{"g": `a"b\c`})
	if err != nil {
		t.Fatalf("fill: %v", err)
	}
	var parsed struct {
		Catalogs []struct {
			Groups []string `json:"groups"`
		} `json:"catalogs"`
	}
	if err := json.Unmarshal(filled, &parsed); err != nil || parsed.Catalogs[0].Groups[0] != `a"b\c` {
		t.Fatalf("filled = %s (%v)", filled, err)
	}
	if _, err := fillDocument([]byte(`{"broken":`), nil); err == nil {
		t.Fatal("a catalogue that is not JSON was sent")
	}

	// A question without a kind takes the answer as written; one without English
	// asks in the language it has.
	pkg := importPackage{
		catalogue: []byte(`{"x":"{{free}}","y":"{{de-only}}"}`),
		questions: map[string]packageQuestion{"de-only": {Label: map[string]string{"de": "Nur Deutsch"}}},
	}
	_, _, err = resolveAnswers(pkg, map[string]string{"free": "anything"}, nil)
	if err == nil || !strings.Contains(err.Error(), "de-only is not answered: Nur Deutsch") {
		t.Fatalf("err = %v", err)
	}
	resolved, notes, err := resolveAnswers(pkg, map[string]string{"free": "anything", "de-only": "ja", "extra": "1"}, nil)
	if err != nil || resolved["free"] != "anything" || len(notes) != 1 || !strings.Contains(notes[0], "does not ask for extra") {
		t.Fatalf("resolved = %v, notes = %v, err = %v", resolved, notes, err)
	}
}

// TestImportSaysWhatTheServerRefused: whichever step the server refuses — the
// directory, the source, the publish, the catalogue — the run stops there with the
// server's own words, every problem of a refused catalogue on a line of its own.
func TestImportSaysWhatTheServerRefused(t *testing.T) {
	type answer struct {
		status int
		body   string
	}
	ok := func(body string) answer { return answer{http.StatusOK, body} }
	answers := map[string]answer{}
	reset := func() {
		answers["/api/v1/info"] = ok(`{"version":"test"}`)
		answers["/api/v1/principals"] = ok(`[{"type":"group","id":"g1","name":"G"},{"type":"user","id":"u1","name":"U"}]`)
		answers["/api/v1/applications/source"] = ok(`{"applicationId":"app1","key":"k","name":"n","untracked":["old-form"]}`)
		answers["/api/v1/applications/app1/publish"] = ok(`{"deployed":true,"definitions":[{}],"warnings":["worker verzeichnis is not configured"],"release":{"version":4}}`)
		answers["/api/v1/catalogs/import"] = ok(`{"created":[],"updated":["catalog:c"],"releases":[{}],
		  "warnings":[{"subject":"product:park","problem":"the answers fahrzeug of form f reach p in the clear"}]}`)
	}
	mock := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v1/applications/source" && r.Header.Get("Content-Type") != "application/gzip" {
			http.Error(w, "not a gzip", http.StatusUnsupportedMediaType)
			return
		}
		a := answers[r.URL.Path]
		w.WriteHeader(a.status)
		_, _ = w.Write([]byte(a.body))
	}))
	defer mock.Close()
	args := []string{"--server", mock.URL, "--set", "zielgruppe=g1", "--set", "zielgruppe-bau=g1",
		"--set", "facility-management=u1", "--set", "geoportal-verantwortliche=g1", examplePackage}

	cases := []struct {
		name, path string
		answer     answer
		want       string
	}{
		{"a server with its catalogue switched off", "/api/v1/info",
			ok(`{"catalogue":false}`), "catalogue switched off (--catalogue=false); nothing was imported"},
		{"a server that cannot say what it offers", "/api/v1/info",
			answer{http.StatusUnauthorized, `{"error":"authentication required"}`}, "GET /api/v1/info: 401 Unauthorized"},
		{"no token for the directory", "/api/v1/principals",
			answer{http.StatusUnauthorized, `{"error":"authentication required"}`}, "GET /api/v1/principals: 401 Unauthorized: authentication required"},
		{"the source taken by another application", "/api/v1/applications/source",
			answer{http.StatusConflict, `{"error":"process \"proc_vd_zutrittsbadge\" already belongs to another application"}`}, "already belongs to another application"},
		{"a source import that names no application", "/api/v1/applications/source", ok(`{}`), "without naming the application"},
		{"a publish refused at the door", "/api/v1/applications/app1/publish",
			answer{http.StatusForbidden, `{"error":"editor access required"}`}, "403 Forbidden: editor access required"},
		{"a publish the server declines", "/api/v1/applications/app1/publish",
			ok(`{"deployed":false,"reason":"proc_vd_zutrittsbadge does not compile"}`), "was not published: proc_vd_zutrittsbadge does not compile"},
		{"a catalogue refused for two reasons", "/api/v1/catalogs/import",
			answer{http.StatusForbidden, `{"problems":[{"subject":"catalog:cat-verwaltung-dienste","problem":"you do not maintain this catalogue"},
			  {"subject":"product:vd-parkplatz","problem":"no home catalogue"}]}`},
			"403 Forbidden: refused:\n  catalog:cat-verwaltung-dienste: you do not maintain this catalogue\n  product:vd-parkplatz: no home catalogue"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			reset()
			answers[tc.path] = tc.answer
			var out bytes.Buffer
			if err := runImport(args, &out); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want %q\n%s", err, tc.want, out.String())
			}
		})
	}

	reset()
	var out bytes.Buffer
	if err := runImport(args, &out); err != nil {
		t.Fatalf("import: %v", err)
	}
	for _, want := range []string{"left in place, not in the package: old-form", "published release 4: 1 processes",
		"warning: worker verzeichnis is not configured", "shop updated: catalog:c", "catalogues published: 1",
		"warning: product:park: the answers fahrzeug of form f reach p in the clear"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("output lacks %q:\n%s", want, out.String())
		}
	}
}

// TestTheServersWordsAreKept: every shape a refusal comes in reads as itself.
func TestTheServersWordsAreKept(t *testing.T) {
	for raw, want := range map[string]string{
		`{"error":"no application with that key"}`: "no application with that key",
		`{"reason":"nothing to deploy"}`:           "nothing to deploy",
		"plain text\n":                             "plain text",
	} {
		if got := serverMessage([]byte(raw)); got != want {
			t.Errorf("serverMessage(%s) = %q, want %q", raw, got, want)
		}
	}
}

// The shop handbook teaches `atlas import` with a table of its flags
// (api/web/shop-handbuch.html, #terminal), held to the flags runImport defines in
// both directions, as the playground's table is held to its runner's.
const shopHandbook = "../../api/web/shop-handbuch.html"

func TestTheShopHandbookDocumentsEveryImportFlag(t *testing.T) {
	defined := commandFlags(t, "importpkg.go", "runImport")
	documented := documentedFlags(t, shopHandbook, "import-flags")
	for name := range defined {
		if !documented[name] {
			t.Errorf(`atlas import defines --%s, but the shop handbook's table (<table id="import-flags">) does not list it`, name)
		}
	}
	for name := range documented {
		if !defined[name] {
			t.Errorf("the shop handbook's table lists --%s, which atlas import does not define", name)
		}
	}
}
