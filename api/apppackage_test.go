package api_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/pblumer/atlas/api"
)

// An application and the catalogues that sell it move from the Console as one
// package (ADR-0467, amended 2026-10-21): the same package `atlas export` writes,
// whose groups and people are questions answered by name, so the other installation
// finds them under its own ids.

// shopDocument is a catalogue that groupID reaches, whose product the group may
// receive and approves, provisioned by the process prov-pkg.
func shopDocument(groupID string) string {
	return `{"catalogs":[{"id":"cat-pkg","texts":{"de":"Amt"},"rank":7,"languages":["de"],"items":["badge"],"groups":["` + groupID + `"]}],
	  "products":[{"id":"badge","homeCatalog":"cat-pkg","state":"active","texts":{"de":"Badge"},
	    "approval":{"kind":"role","ref":"` + groupID + `"},"eligible":["` + groupID + `"],"provisionProcess":"prov-pkg"}]}`
}

// packageOf builds a server holding an application with the process prov-pkg and a
// constant, and the catalogue cat-pkg selling it to the group "Alle Mitarbeitenden".
func packageOf(t *testing.T) (*httptest.Server, string, string) {
	t.Helper()
	ts := newTestServer(t)
	groupID := createGroup(t, http.DefaultClient, ts.URL, "Alle Mitarbeitenden")
	appID := mkApp(t, ts, "Bestellung")
	saveDraftIn(t, ts, appID, "prov-pkg", "Bereitstellen")
	holdConstants(t, ts, "/api/v1/applications/"+appID+"/constants", `{"name":"TENANT_ID","type":"string","value":"int-01"}`)
	if code, body := doReq(t, ts, http.MethodPost, "/api/v1/catalogs/import", shopDocument(groupID), "application/json"); code != http.StatusOK {
		t.Fatalf("set up the catalogue = %d %s", code, body)
	}
	return ts, appID, groupID
}

func TestAnApplicationPackageCarriesItsCataloguesAsQuestions(t *testing.T) {
	src, appID, groupID := packageOf(t)

	// The download dialog offers the catalogue, marked as selling the application.
	code, body := doReq(t, src, http.MethodGet, "/api/v1/applications/"+appID+"/needs", "", "")
	var needs struct {
		Catalogs []struct {
			ID    string `json:"id"`
			Bound bool   `json:"bound"`
		} `json:"catalogs"`
	}
	if code != http.StatusOK || json.Unmarshal(body, &needs) != nil || len(needs.Catalogs) != 1 ||
		needs.Catalogs[0].ID != "cat-pkg" || !needs.Catalogs[0].Bound {
		t.Fatalf("needs = %d %s, want cat-pkg offered as bound", code, body)
	}

	code, body = doReq(t, src, http.MethodGet, "/api/v1/applications/"+appID+"/package?catalog=cat-pkg&constants=true&preview=true", "", "")
	var preview struct {
		Files     []string `json:"files"`
		Products  int      `json:"products"`
		Publish   bool     `json:"publish"`
		Questions int      `json:"questions"`
		Notes     []string `json:"notes"`
	}
	if code != http.StatusOK || json.Unmarshal(body, &preview) != nil {
		t.Fatalf("preview = %d %s", code, body)
	}
	files := strings.Join(preview.Files, ",")
	for _, want := range []string{"atlas.json", "constants.json", "katalog.json", "fragen.json", "antworten.json"} {
		if !strings.Contains(files, want) {
			t.Errorf("preview files = %s, want %s", files, want)
		}
	}
	// One group, met three times, is one question; an unreleased catalogue is not
	// published on import.
	if preview.Products != 1 || preview.Questions != 1 || preview.Publish || len(preview.Notes) == 0 {
		t.Errorf("preview = %+v", preview)
	}

	code, archive := doReq(t, src, http.MethodGet, "/api/v1/applications/"+appID+"/package?catalog=cat-pkg&constants=true", "", "")
	if code != http.StatusOK {
		t.Fatalf("package = %d %s", code, archive)
	}
	pkg := archiveFiles(t, archive)
	shop := pkg["katalog.json"]
	if strings.Contains(shop, groupID) || strings.Count(shop, "{{alle-mitarbeitenden}}") != 3 {
		t.Fatalf("katalog.json carries the group as this server's id, or not as one question:\n%s", shop)
	}
	if !strings.Contains(pkg["antworten.json"], `"alle-mitarbeitenden": "Alle Mitarbeitenden"`) {
		t.Errorf("antworten.json = %s", pkg["antworten.json"])
	}
	if !strings.Contains(pkg["fragen.json"], `"kind": "group"`) || !strings.Contains(pkg["constants.json"], "int-01") {
		t.Errorf("fragen.json = %s\nconstants.json = %s", pkg["fragen.json"], pkg["constants.json"])
	}

	// The other installation: the same group by name, under its own id.
	dst := newTestServer(t)
	createGroup(t, http.DefaultClient, dst.URL, "Andere")
	dstGroup := createGroup(t, http.DefaultClient, dst.URL, "Alle Mitarbeitenden")
	if code, _, body := importSource(t, dst, archive); code != http.StatusOK {
		t.Fatalf("the package as a source tree = %d %s", code, body)
	}
	answers := func(given string) (int, []byte) {
		req := `{"document":` + shop + `,"questions":` + pkg["fragen.json"] + `,"answers":` + given + `}`
		return doReq(t, dst, http.MethodPost, "/api/v1/catalogs/import/answers", req, "application/json")
	}
	code, body = answers(pkg["antworten.json"])
	var filled struct {
		Document json.RawMessage   `json:"document"`
		Resolved map[string]string `json:"resolved"`
		Notes    []string          `json:"notes"`
	}
	if code != http.StatusOK || json.Unmarshal(body, &filled) != nil || filled.Resolved["alle-mitarbeitenden"] != dstGroup {
		t.Fatalf("answers = %d %s", code, body)
	}
	if strings.Contains(string(filled.Document), "{{") || strings.Count(string(filled.Document), dstGroup) != 3 {
		t.Fatalf("filled document = %s", filled.Document)
	}
	if code, body := doReq(t, dst, http.MethodPost, "/api/v1/catalogs/import", string(filled.Document), "application/json"); code != http.StatusOK {
		t.Fatalf("import the filled catalogue = %d %s", code, body)
	}
	if _, cat := doReq(t, dst, http.MethodGet, "/api/v1/catalogs/cat-pkg", "", ""); !strings.Contains(string(cat), dstGroup) {
		t.Fatalf("the catalogue on the target = %s, want its audience under the target's id", cat)
	}
}

func TestPackageAnswersSayWhatTheyCannotAnswer(t *testing.T) {
	ts := newTestServer(t)
	// Group names are unique on a server; people's display names are not.
	person := func(username string) string {
		code, body := doReq(t, ts, http.MethodPost, "/api/v1/users",
			`{"username":"`+username+`","displayName":"Doppelt","password":"a-long-password-1"}`, "application/json")
		var u struct {
			ID string `json:"id"`
		}
		if code != http.StatusCreated || json.Unmarshal(body, &u) != nil {
			t.Fatalf("create %s = %d %s", username, code, body)
		}
		return u.ID
	}
	first, second := person("doppelt1"), person("doppelt2")
	doc := `{"catalogs":[{"id":"c","members":[{"ref":{"type":"user","id":"{{gruppe}}"},"role":"maintainer"}]}],"note":"{{frei}}"}`
	questions := `{"gruppe":{"kind":"user","label":{"en":"audience"}},"frei":{"kind":"text"}}`
	ask := func(answers string) (int, []byte) {
		return doReq(t, ts, http.MethodPost, "/api/v1/catalogs/import/answers",
			`{"document":`+doc+`,"questions":`+questions+`,"answers":`+answers+`}`, "application/json")
	}
	type problems struct {
		Problems []struct {
			Name       string   `json:"name"`
			Reason     string   `json:"reason"`
			Question   string   `json:"question"`
			Candidates []string `json:"candidates"`
		} `json:"problems"`
	}
	read := func(body []byte) problems {
		var p problems
		if err := json.Unmarshal(body, &p); err != nil {
			t.Fatalf("decode: %v (%s)", err, body)
		}
		return p
	}

	code, body := ask(`{}`)
	if p := read(body); code != http.StatusUnprocessableEntity || len(p.Problems) != 2 || p.Problems[0].Reason != "unanswered" || p.Problems[0].Question != "audience" {
		t.Fatalf("nothing answered = %d %s", code, body)
	}
	code, body = ask(`{"gruppe":"Doppelt","frei":"x"}`)
	if p := read(body); code != http.StatusUnprocessableEntity || len(p.Problems) != 1 || p.Problems[0].Reason != "ambiguous" || len(p.Problems[0].Candidates) != 2 {
		t.Fatalf("a name two people carry = %d %s", code, body)
	}
	code, body = ask(`{"gruppe":"Niemand","frei":"x"}`)
	if p := read(body); code != http.StatusUnprocessableEntity || p.Problems[0].Reason != "unknown" {
		t.Fatalf("a name nobody carries = %d %s", code, body)
	}
	// An id settles it; a text question takes its answer as written.
	code, body = ask(`{"gruppe":"` + second + `","frei":"Wert \"zitiert\""}`)
	if code != http.StatusOK || !strings.Contains(string(body), second) || strings.Contains(string(body), first) {
		t.Fatalf("answered by id = %d %s", code, body)
	}
	if code, _ := doReq(t, ts, http.MethodPost, "/api/v1/catalogs/import/answers", `{"document":"{"}`, "application/json"); code != http.StatusBadRequest {
		t.Errorf("a document that is not JSON = %d, want 400", code)
	}
}

func TestAPackageAsksForWhatItsCataloguesNeed(t *testing.T) {
	src, appID, _ := packageOf(t)
	if code, _ := doReq(t, src, http.MethodGet, "/api/v1/applications/"+appID+"/package?catalog=nope", "", ""); code != http.StatusNotFound {
		t.Errorf("an unknown catalogue = %d, want 404", code)
	}
	if code, _ := doReq(t, src, http.MethodGet, "/api/v1/applications/"+appID+"/package?preview=vielleicht", "", ""); code != http.StatusBadRequest {
		t.Errorf("preview=vielleicht = %d, want 400", code)
	}
	// Without catalogues the package is the source tree.
	code, archive := doReq(t, src, http.MethodGet, "/api/v1/applications/"+appID+"/package", "", "")
	if files := archiveFiles(t, archive); code != http.StatusOK || files["katalog.json"] != "" || files["atlas.json"] == "" {
		t.Errorf("a package without catalogues = %d %v", code, files)
	}

	off := newTestServerWith(t, api.WithoutCatalogue())
	offApp := mkApp(t, off, "Ohne Shop")
	if code, body := doReq(t, off, http.MethodGet, "/api/v1/applications/"+offApp+"/package?catalog=c", "", ""); code != http.StatusConflict {
		t.Errorf("a catalogue from a server without one = %d %s, want 409", code, body)
	}
	if _, body := doReq(t, off, http.MethodGet, "/api/v1/applications/"+offApp+"/needs", "", ""); !strings.Contains(string(body), `"catalogs":[]`) {
		t.Errorf("needs on a server without a catalogue = %s", body)
	}

	// A modeler without the productmanager role exports the application, not its shop.
	ts, _ := newAuthServer(t, "admin", "password1")
	admin := newClient(t)
	if login(t, admin, ts, "admin", "password1") != http.StatusOK {
		t.Fatal("admin login failed")
	}
	if code, body := cReq(t, admin, ts, "POST", "/api/v1/users", `{"username":"mona","password":"password1","roles":["modeler","user"]}`); code != http.StatusCreated {
		t.Fatalf("create mona: %d %s", code, body)
	}
	mona := newClient(t)
	if login(t, mona, ts, "mona", "password1") != http.StatusOK {
		t.Fatal("mona login failed")
	}
	code, body := cReq(t, mona, ts, "POST", "/api/v1/applications", `{"name":"Monas"}`)
	var app struct {
		ID string `json:"id"`
	}
	if code != http.StatusOK || json.Unmarshal(body, &app) != nil {
		t.Fatalf("create application: %d %s", code, body)
	}
	if code, body := cReq(t, mona, ts, "GET", "/api/v1/applications/"+app.ID+"/package?catalog=c&preview=true", ""); code != http.StatusForbidden || !strings.Contains(string(body), "productmanager") {
		t.Errorf("mona's package with a catalogue = %d %s, want 403 naming the role", code, body)
	}
	if code, body := cReq(t, mona, ts, "GET", "/api/v1/applications/"+app.ID+"/package?preview=true", ""); code != http.StatusOK {
		t.Errorf("mona's package without one = %d %s", code, body)
	}
	if code, body := cReq(t, mona, ts, "GET", "/api/v1/applications/"+app.ID+"/needs", ""); code != http.StatusOK || !strings.Contains(string(body), `"catalogs":[]`) {
		t.Errorf("mona's needs = %d %s, want no catalogues offered", code, body)
	}
}

// TestAFixedApproverTravelsAsAUsername: a fixed approval is a task assigned to a
// username, so the package asks for an account and the target answers it with the
// username of the person carrying that name there — not with an id nobody is
// assigned by.
func TestAFixedApproverTravelsAsAUsername(t *testing.T) {
	src := newTestServer(t)
	if code, body := doReq(t, src, http.MethodPost, "/api/v1/users", `{"username":"fmeier","displayName":"Fabienne Meier","password":"a-long-password-1"}`, "application/json"); code != http.StatusCreated {
		t.Fatalf("create the approver = %d %s", code, body)
	}
	appID := mkApp(t, src, "Parkplatz")
	saveDraftIn(t, src, appID, "prov-pkg", "Bereitstellen")
	doc := `{"catalogs":[{"id":"cat-park","texts":{"de":"Park"},"rank":8,"languages":["de"],"items":["platz"]}],
	  "products":[{"id":"platz","homeCatalog":"cat-park","state":"active","texts":{"de":"Platz"},
	    "approval":{"kind":"fixed","ref":"fmeier"},"provisionProcess":"prov-pkg"}]}`
	if code, body := doReq(t, src, http.MethodPost, "/api/v1/catalogs/import", doc, "application/json"); code != http.StatusOK {
		t.Fatalf("set up the catalogue = %d %s", code, body)
	}
	_, archive := doReq(t, src, http.MethodGet, "/api/v1/applications/"+appID+"/package?catalog=cat-park", "", "")
	pkg := archiveFiles(t, archive)
	if !strings.Contains(pkg["fragen.json"], `"kind": "account"`) || !strings.Contains(pkg["antworten.json"], `"Fabienne Meier"`) {
		t.Fatalf("fragen.json = %s\nantworten.json = %s", pkg["fragen.json"], pkg["antworten.json"])
	}

	dst := newTestServer(t)
	if code, body := doReq(t, dst, http.MethodPost, "/api/v1/users", `{"username":"fmm","displayName":"Fabienne Meier","password":"a-long-password-1"}`, "application/json"); code != http.StatusCreated {
		t.Fatalf("create the approver on the target = %d %s", code, body)
	}
	code, body := doReq(t, dst, http.MethodPost, "/api/v1/catalogs/import/answers",
		`{"document":`+pkg["katalog.json"]+`,"questions":`+pkg["fragen.json"]+`,"answers":`+pkg["antworten.json"]+`}`, "application/json")
	if code != http.StatusOK || !strings.Contains(string(body), `"ref":"fmm"`) {
		t.Fatalf("answers = %d %s, want the approval assigned to the target's username", code, body)
	}
	// A username answers too, whatever its case.
	code, body = doReq(t, dst, http.MethodPost, "/api/v1/catalogs/import/answers",
		`{"document":`+pkg["katalog.json"]+`,"questions":`+pkg["fragen.json"]+`,"answers":{"fabienne-meier":"FMM"}}`, "application/json")
	if code != http.StatusOK || !strings.Contains(string(body), `"ref":"fmm"`) {
		t.Fatalf("answered by username = %d %s", code, body)
	}
}
