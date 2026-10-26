package api_test

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

// An application's constants are imported like its source (ADR-0467,
// amended): a value this installation lacks is set, one it holds as the last import
// brought it is updated, one an administrator set here is kept while the package
// suggests what it suggested before, and only one both sides moved is asked about.
// Setting a value is an administrator's; anyone else imports the plan and is told
// what an administrator still has to set.

type constantsImport struct {
	DryRun    bool `json:"dryRun"`
	Artifacts []struct {
		Ref     string `json:"ref"`
		Status  string `json:"status"`
		Outcome string `json:"outcome"`
	} `json:"artifacts"`
	Set    []string `json:"set"`
	NotSet []string `json:"notSet"`
}

func (r constantsImport) outcomes() map[string]string {
	out := map[string]string{}
	for _, a := range r.Artifacts {
		out[a.Ref] = a.Status + "/" + a.Outcome
	}
	return out
}

func importConstants(t *testing.T, do func(method, path, body string) (int, []byte), appID, query, values string) (int, constantsImport, []byte) {
	t.Helper()
	code, body := do(http.MethodPost, "/api/v1/applications/"+appID+"/constants/import"+query, `{"constants":[`+values+`]}`)
	var res constantsImport
	if code == http.StatusOK {
		if err := json.Unmarshal(body, &res); err != nil {
			t.Fatalf("decode: %v (%s)", err, body)
		}
	}
	return code, res, body
}

func TestConstantsImportTellsAnAdministratorsValueFromTheSuggestion(t *testing.T) {
	ts := newTestServer(t)
	do := func(method, path, body string) (int, []byte) {
		return doReq(t, ts, method, path, body, "application/json")
	}
	appID := mkApp(t, ts, "Konstanten")
	tenant := func(v string) string { return `{"name":"TENANT_ID","type":"string","value":"` + v + `"}` }

	code, res, body := importConstants(t, do, appID, "?dryRun=true", tenant("int-01"))
	if code != http.StatusOK || !res.DryRun || res.outcomes()["constant:TENANT_ID"] != "new/created" || len(res.Set) != 0 {
		t.Fatalf("plan = %d %s", code, body)
	}
	if code, res, body = importConstants(t, do, appID, "", tenant("int-01")); code != http.StatusOK || strings.Join(res.Set, ",") != "TENANT_ID" {
		t.Fatalf("first import = %d %s", code, body)
	}

	// The package moved on, nobody touched it here: updated without a question.
	if code, res, body = importConstants(t, do, appID, "", tenant("int-02")); code != http.StatusOK ||
		res.outcomes()["constant:TENANT_ID"] != "behind/updated" {
		t.Fatalf("a value the package moved on = %d %s", code, body)
	}

	// An administrator sets this installation's value: the same package keeps it.
	if code, body := do(http.MethodPut, "/api/v1/applications/"+appID+"/constants",
		`{"constants":[{"name":"TENANT_ID","type":"string","value":"prod-01"}]}`); code != http.StatusOK {
		t.Fatalf("set the value here = %d %s", code, body)
	}
	if code, res, body = importConstants(t, do, appID, "", tenant("int-02")); code != http.StatusOK ||
		res.outcomes()["constant:TENANT_ID"] != "ahead/kept" || len(res.Set) != 0 {
		t.Fatalf("this installation's value and the same suggestion = %d %s", code, body)
	}

	// Both moved: asked, and nothing written until decided.
	if code, _, body = importConstants(t, do, appID, "", tenant("int-03")); code != http.StatusConflict ||
		!strings.Contains(string(body), `"undecided":["constant:TENANT_ID"]`) {
		t.Fatalf("both moved = %d %s", code, body)
	}
	_, held := doReq(t, ts, http.MethodGet, "/api/v1/applications/"+appID+"/constants", "", "")
	if !strings.Contains(string(held), `"prod-01"`) {
		t.Fatalf("an undecided import wrote: %s", held)
	}
	// A constant the package does not name is left alone.
	if code, res, body = importConstants(t, do, appID, "?keep=constant:TENANT_ID",
		tenant("int-03")+`,{"name":"NEU","type":"number","value":"1"}`); code != http.StatusOK || strings.Join(res.Set, ",") != "NEU" {
		t.Fatalf("keeping TENANT_ID, adding NEU = %d %s", code, body)
	}
	_, held = doReq(t, ts, http.MethodGet, "/api/v1/applications/"+appID+"/constants", "", "")
	if !strings.Contains(string(held), `"prod-01"`) || !strings.Contains(string(held), `"NEU"`) {
		t.Fatalf("constants after = %s", held)
	}

	if code, _, _ := importConstants(t, do, appID, "", `{"name":"klein","type":"string","value":"x"}`); code != http.StatusBadRequest {
		t.Errorf("an invalid name = %d, want 400", code)
	}
	if code, _, _ := importConstants(t, do, appID, "?overwrite=*&keep=*", tenant("x")); code != http.StatusBadRequest {
		t.Errorf("contradicting decisions = %d, want 400", code)
	}
}

// TestConstantsImportByAModeler: a modeler holding the application imports the plan
// but sets no value. What an administrator has to set is named, and is offered again
// next time: an import that did not write a value does not remember it as settled.
func TestConstantsImportByAModeler(t *testing.T) {
	ts, _ := newAuthServer(t, "admin", "password1")
	admin := newClient(t)
	if login(t, admin, ts, "admin", "password1") != http.StatusOK {
		t.Fatal("admin login failed")
	}
	if code, body := cReq(t, admin, ts, "POST", "/api/v1/users",
		`{"username":"mona","password":"password1","roles":["modeler","user"]}`); code != http.StatusCreated {
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
	do := func(method, path, body string) (int, []byte) { return cReq(t, mona, ts, method, path, body) }
	for round := 1; round <= 2; round++ {
		code, res, body := importConstants(t, do, app.ID, "", `{"name":"TENANT_ID","type":"string","value":"int-01"}`)
		if code != http.StatusOK || strings.Join(res.NotSet, ",") != "TENANT_ID" || len(res.Set) != 0 ||
			res.outcomes()["constant:TENANT_ID"] != "new/created" {
			t.Fatalf("round %d = %d %s", round, code, body)
		}
	}
	other := newClient(t)
	if code, body := cReq(t, admin, ts, "POST", "/api/v1/users",
		`{"username":"otto","password":"password1","roles":["modeler","user"]}`); code != http.StatusCreated {
		t.Fatalf("create otto: %d %s", code, body)
	}
	if login(t, other, ts, "otto", "password1") != http.StatusOK {
		t.Fatal("otto login failed")
	}
	if code, _ := cReq(t, other, ts, "POST", "/api/v1/applications/"+app.ID+"/constants/import", `{"constants":[]}`); code != http.StatusNotFound {
		t.Errorf("somebody without a role on the application = %d, want 404", code)
	}
}
