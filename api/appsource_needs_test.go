package api_test

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// A source download carries the application's constants when asked for them, and
// names what its processes need from the installation that no download carries
// (ADR-0467, amended 2026-10-21). The import offers the constants like every other
// artifact, and answers each need with how the importing server stands.

// needyBPMN reads TENANT_ID from its application, MAIL_DOMAIN from the installation
// and LANGUAGE from the installation with a fallback, and sends a mail through the
// worker named Postausgang.
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

type needView struct {
	Kind      string   `json:"kind"`
	Name      string   `json:"name"`
	Type      string   `json:"type"`
	Processes []string `json:"processes"`
	Fallback  bool     `json:"fallback"`
	State     string   `json:"state"`
	Detail    string   `json:"detail"`
}

// needStates maps each need to its state, keyed kind/name.
func needStates(needs []needView) map[string]string {
	out := map[string]string{}
	for _, n := range needs {
		out[n.Kind+"/"+n.Name] = n.State
	}
	return out
}

// sourceImportNeeds is the part of an import answer this file reads.
type sourceImportNeeds struct {
	ApplicationID   string     `json:"applicationId"`
	ConstantsSet    []string   `json:"constantsSet"`
	ConstantsNotSet []string   `json:"constantsNotSet"`
	Needs           []needView `json:"needs"`
	NeedsUnread     []string   `json:"needsUnread"`
	Artifacts       []struct {
		Ref     string `json:"ref"`
		Status  string `json:"status"`
		Outcome string `json:"outcome"`
	} `json:"artifacts"`
}

func (r sourceImportNeeds) outcome(ref string) string {
	for _, a := range r.Artifacts {
		if a.Ref == ref {
			return a.Status + "/" + a.Outcome
		}
	}
	return ""
}

func decodeImportNeeds(t *testing.T, body []byte) sourceImportNeeds {
	t.Helper()
	var res sourceImportNeeds
	if err := json.Unmarshal(body, &res); err != nil {
		t.Fatalf("decode import answer: %v (%s)", err, body)
	}
	return res
}

// archiveFiles reads a source archive into its files by path.
func archiveFiles(t *testing.T, archive []byte) map[string]string {
	t.Helper()
	gzr, err := gzip.NewReader(bytes.NewReader(archive))
	if err != nil {
		t.Fatalf("gzip: %v", err)
	}
	defer gzr.Close()
	out := map[string]string{}
	tr := tar.NewReader(gzr)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			return out
		}
		if err != nil {
			t.Fatalf("tar: %v", err)
		}
		data, err := io.ReadAll(tr)
		if err != nil {
			t.Fatalf("read %s: %v", hdr.Name, err)
		}
		out[hdr.Name] = string(data)
	}
}

func saveNeedyDraft(t *testing.T, ts *httptest.Server, appID string) {
	t.Helper()
	if code, body := doReq(t, ts, http.MethodPost, "/api/v1/drafts?projectId="+appID, needyBPMN, "application/xml"); code != http.StatusOK {
		t.Fatalf("save draft status=%d body=%s", code, body)
	}
}

// holdConstants replaces the constants held under path with the listed ones.
func holdConstants(t *testing.T, ts *httptest.Server, path, constants string) {
	t.Helper()
	putConstants(t, ts, path, `{"constants":[`+constants+`]}`)
}

func TestASourceDownloadCarriesConstantsOnlyWhenAsked(t *testing.T) {
	ts := newTestServer(t)
	appID := mkApp(t, ts, "Bedarf")
	saveNeedyDraft(t, ts, appID)
	holdConstants(t, ts, "/api/v1/applications/"+appID+"/constants", `{"name":"TENANT_ID","type":"string","value":"int-01"}`)

	plain := archiveFiles(t, exportSource(t, ts, appID))
	if _, ok := plain["constants.json"]; ok {
		t.Fatalf("a download not asked for its constants carries them: %v", plain)
	}
	var man struct {
		FormatVersion int        `json:"formatVersion"`
		Constants     string     `json:"constants"`
		Needs         []needView `json:"needs"`
	}
	if err := json.Unmarshal([]byte(plain["atlas.json"]), &man); err != nil {
		t.Fatalf("decode manifest: %v", err)
	}
	if man.FormatVersion != 1 || man.Constants != "" {
		t.Errorf("a tree without models or constants: formatVersion=%d constants=%q, want 1 and none", man.FormatVersion, man.Constants)
	}
	// The manifest names what the process needs, without asking any server about it.
	want := []string{"server-constant/LANGUAGE", "server-constant/MAIL_DOMAIN", "application-constant/TENANT_ID", "worker/Postausgang"}
	if len(man.Needs) != len(want) {
		t.Fatalf("needs = %+v, want %v", man.Needs, want)
	}
	for i, n := range man.Needs {
		if got := n.Kind + "/" + n.Name; got != want[i] || n.State != "" || strings.Join(n.Processes, ",") != "bedarf" {
			t.Errorf("need %d = %+v, want %s read by bedarf and no state", i, n, want[i])
		}
	}
	if !man.Needs[0].Fallback || man.Needs[1].Fallback || man.Needs[3].Type != "mail" {
		t.Errorf("needs = %+v: LANGUAGE has a fallback, MAIL_DOMAIN none, Postausgang is a mail worker", man.Needs)
	}

	code, archive := doReq(t, ts, http.MethodGet, "/api/v1/applications/"+appID+"/source?constants=true", "", "")
	if code != http.StatusOK {
		t.Fatalf("download with constants status=%d body=%s", code, archive)
	}
	files := archiveFiles(t, archive)
	if !strings.Contains(files["constants.json"], `"value": "int-01"`) {
		t.Fatalf("constants.json = %q", files["constants.json"])
	}
	if !strings.Contains(files["atlas.json"], `"formatVersion": 3`) || !strings.Contains(files["atlas.json"], `"constants": "constants.json"`) {
		t.Errorf("manifest of a tree with constants = %s", files["atlas.json"])
	}
	if code, _ := doReq(t, ts, http.MethodGet, "/api/v1/applications/"+appID+"/source?constants=ja", "", ""); code != http.StatusBadRequest {
		t.Errorf("constants=ja status=%d, want 400", code)
	}
}

func TestTheSourceImportOffersConstantsAndAnswersNeeds(t *testing.T) {
	src := newTestServer(t)
	appID := mkApp(t, src, "Bedarf")
	saveNeedyDraft(t, src, appID)
	holdConstants(t, src, "/api/v1/applications/"+appID+"/constants", `{"name":"TENANT_ID","type":"string","value":"int-01"}`)
	download := func() []byte {
		code, archive := doReq(t, src, http.MethodGet, "/api/v1/applications/"+appID+"/source?constants=true", "", "")
		if code != http.StatusOK {
			t.Fatalf("download status=%d", code)
		}
		return archive
	}

	dst := newTestServer(t)
	importWith := func(archive []byte, query string) (int, sourceImportNeeds, []byte) {
		code, body := doReqBytes(t, dst, http.MethodPost, "/api/v1/applications/source"+query, archive, "application/gzip")
		var res sourceImportNeeds
		if code == http.StatusOK {
			res = decodeImportNeeds(t, body)
		}
		return code, res, body
	}

	// The plan: the constant is new, and the needs are answered as they will stand
	// once the import is in — TENANT_ID by the import itself.
	v1 := download()
	code, plan, body := importWith(v1, "?dryRun=true")
	if code != http.StatusOK || plan.outcome("constant:TENANT_ID") != "new/created" {
		t.Fatalf("plan status=%d body=%s", code, body)
	}
	states := needStates(plan.Needs)
	if states["application-constant/TENANT_ID"] != "set" || states["server-constant/MAIL_DOMAIN"] != "missing" ||
		states["server-constant/LANGUAGE"] != "fallback" || states["worker/Postausgang"] != "missing" {
		t.Fatalf("needs in the plan = %v", states)
	}

	code, res, body := importWith(v1, "")
	if code != http.StatusOK || strings.Join(res.ConstantsSet, ",") != "TENANT_ID" {
		t.Fatalf("import status=%d body=%s", code, body)
	}
	_, held := doReq(t, dst, http.MethodGet, "/api/v1/applications/"+res.ApplicationID+"/constants", "", "")
	if !strings.Contains(string(held), `"int-01"`) {
		t.Fatalf("constants after the import = %s", held)
	}
	// Idempotent: the same tree again sets nothing.
	if code, res, body = importWith(v1, ""); code != http.StatusOK || res.outcome("constant:TENANT_ID") != "unchanged/unchanged" || len(res.ConstantsSet) != 0 {
		t.Fatalf("the same tree again: status=%d body=%s", code, body)
	}

	// The installation's own value is kept while the tree suggests what it did before.
	holdConstants(t, dst, "/api/v1/applications/"+res.ApplicationID+"/constants", `{"name":"TENANT_ID","type":"string","value":"prod-01"}`)
	if code, res, body = importWith(v1, ""); code != http.StatusOK || res.outcome("constant:TENANT_ID") != "ahead/kept" {
		t.Fatalf("this installation's value and the same tree: status=%d body=%s", code, body)
	}
	// Both moved: asked, with the needs in the plan the 409 carries.
	holdConstants(t, src, "/api/v1/applications/"+appID+"/constants", `{"name":"TENANT_ID","type":"string","value":"int-02"}`)
	v2 := download()
	code, _, body = importWith(v2, "")
	if code != http.StatusConflict || !strings.Contains(string(body), `"undecided":["constant:TENANT_ID"]`) ||
		!strings.Contains(string(body), `"needs":[`) {
		t.Fatalf("both moved: status=%d body=%s, want a 409 naming the constant, with the needs", code, body)
	}
	if code, res, body = importWith(v2, "?keep=constant:TENANT_ID"); code != http.StatusOK || res.outcome("constant:TENANT_ID") != "changed/kept" {
		t.Fatalf("keeping it: status=%d body=%s", code, body)
	}
	_, held = doReq(t, dst, http.MethodGet, "/api/v1/applications/"+res.ApplicationID+"/constants", "", "")
	if !strings.Contains(string(held), `"prod-01"`) {
		t.Fatalf("a kept constant was replaced: %s", held)
	}

	// What the target sets up is answered on the next import.
	holdConstants(t, dst, "/api/v1/server/constants", `{"name":"MAIL_DOMAIN","type":"number","value":"1"}`)
	if code, body := doReq(t, dst, http.MethodPost, "/api/v1/configured-workers",
		`{"name":"Postausgang","kind":"mail","provider":"preview","sender":"portal@example.ch"}`, "application/json"); code != http.StatusOK {
		t.Fatalf("create the worker: %d %s", code, body)
	}
	if code, plan, body = importWith(v2, "?dryRun=true"); code != http.StatusOK {
		t.Fatalf("plan after setting up: status=%d body=%s", code, body)
	}
	states = needStates(plan.Needs)
	if states["server-constant/MAIL_DOMAIN"] != "type-mismatch" || states["worker/Postausgang"] != "configured" {
		t.Fatalf("needs after setting up = %v", states)
	}
}

func TestATreeWithBrokenConstantsIsRefused(t *testing.T) {
	ts := newTestServer(t)
	for name, files := range map[string][][2]string{
		"the file is missing": {
			{"atlas.json", `{"formatVersion":3,"key":"k","name":"K","constants":"constants.json"}`},
		},
		"a name not in capitals": {
			{"atlas.json", `{"formatVersion":3,"key":"k","name":"K","constants":"constants.json"}`},
			{"constants.json", `{"constants":[{"name":"klein","type":"string","value":"x"}]}`},
		},
		"a value not of its type": {
			{"atlas.json", `{"formatVersion":3,"key":"k","name":"K","constants":"constants.json"}`},
			{"constants.json", `{"constants":[{"name":"ANZAHL","type":"number","value":"viele"}]}`},
		},
	} {
		t.Run(name, func(t *testing.T) {
			if code, body := doReqBytes(t, ts, http.MethodPost, "/api/v1/applications/source?dryRun=true",
				sourceArchive(t, files), "application/gzip"); code != http.StatusBadRequest {
				t.Fatalf("status=%d body=%s, want 400", code, body)
			}
		})
	}
}

func TestTheNeedsRouteAnswersForTheDrafts(t *testing.T) {
	ts := newTestServer(t)
	appID := mkApp(t, ts, "Bedarf")
	saveNeedyDraft(t, ts, appID)
	holdConstants(t, ts, "/api/v1/applications/"+appID+"/constants", `{"name":"TENANT_ID","type":"string","value":"int-01"}`)
	holdConstants(t, ts, "/api/v1/server/constants", `{"name":"MAIL_DOMAIN","type":"string","value":"example.ch"}`)
	// A draft that does not compile is named, not guessed at.
	if code, body := doReq(t, ts, http.MethodPost, "/api/v1/drafts?projectId="+appID,
		`<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL"><process id="halb" isExecutable="true"><task id="t"/></process></definitions>`,
		"application/xml"); code != http.StatusOK {
		t.Fatalf("save the unfinished draft: %d %s", code, body)
	}

	code, body := doReq(t, ts, http.MethodGet, "/api/v1/applications/"+appID+"/needs", "", "")
	var res struct {
		Needs     []needView `json:"needs"`
		Unread    []string   `json:"unread"`
		Constants int        `json:"constants"`
	}
	if code != http.StatusOK || json.Unmarshal(body, &res) != nil {
		t.Fatalf("needs status=%d body=%s", code, body)
	}
	states := needStates(res.Needs)
	if states["application-constant/TENANT_ID"] != "set" || states["server-constant/MAIL_DOMAIN"] != "set" ||
		states["server-constant/LANGUAGE"] != "fallback" || states["worker/Postausgang"] != "missing" {
		t.Errorf("needs = %v", states)
	}
	if strings.Join(res.Unread, ",") != "halb" || res.Constants != 1 {
		t.Errorf("unread=%v constants=%d, want halb and 1", res.Unread, res.Constants)
	}
	if code, _ := doReq(t, ts, http.MethodGet, "/api/v1/applications/nope/needs", "", ""); code != http.StatusNotFound {
		t.Errorf("an unknown application status=%d, want 404", code)
	}
}

// TestASourceImportByAModelerSetsNoConstant: a modeler importing a tree with constants
// gets the plan, the values an administrator still has to set, and needs that say so.
func TestASourceImportByAModelerSetsNoConstant(t *testing.T) {
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
	archive := sourceArchive(t, [][2]string{
		{"atlas.json", `{"formatVersion":3,"key":"monas","name":"Monas","constants":"constants.json",
			"processes":[{"id":"bedarf","name":"Bedarf","path":"processes/bedarf.bpmn"}]}`},
		{"processes/bedarf.bpmn", needyBPMN},
		{"constants.json", `{"constants":[{"name":"TENANT_ID","type":"string","value":"int-01"}]}`},
	})
	for round := 1; round <= 2; round++ {
		req, err := http.NewRequest(http.MethodPost, ts.URL+"/api/v1/applications/source", bytes.NewReader(archive))
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Content-Type", "application/gzip")
		resp, err := mona.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("round %d status=%d body=%s", round, resp.StatusCode, body)
		}
		res := decodeImportNeeds(t, body)
		if strings.Join(res.ConstantsNotSet, ",") != "TENANT_ID" || len(res.ConstantsSet) != 0 ||
			res.outcome("constant:TENANT_ID") != "new/created" {
			t.Fatalf("round %d = %s", round, body)
		}
		if needStates(res.Needs)["application-constant/TENANT_ID"] != "missing" {
			t.Fatalf("round %d: the need for TENANT_ID = %v, want missing", round, needStates(res.Needs))
		}
	}
}
