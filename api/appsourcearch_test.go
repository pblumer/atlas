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

// A source tree carries the application's information model and what it brings to the
// capability map (format 4, ADR-0528). The map stays
// the server's: a key it lacks is a gap the caller decides, a key it has is never
// written over, and the application's realizations go with the application.

const archProcess = `<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL">
  <process id="konto_anlegen" name="Konto anlegen" isExecutable="true">
    <startEvent id="s"/><endEvent id="e"/><sequenceFlow id="f" sourceRef="s" targetRef="e"/>
  </process>
</definitions>`

const archModel = `{"name":"Konten","documentation":"Was die Prozesse anlegen.","classes":[
  {"id":"cls-konto","name":"Konto","stereotype":"businessObject","attributes":[{"name":"upn","type":"string","multiplicity":"1"}],"x":40,"y":40}
],"associations":[],"stores":[]}`

const archFile = `{
  "_comment": "for the reader",
  "realizations": [{"capability":"konten","kind":"process","processId":"konto_anlegen"}],
  "capabilities": [{"key":"konten","name":"Konten verwalten","state":"active","requires":["zugriffe"]}],
  "valueStreams": [{"key":"arbeitsfaehig","name":"Arbeitsfaehig machen","stages":[{"key":"konto","name":"Konto","capabilities":["konten"]}]}]
}`

func archTree(key, model, arch string) [][2]string {
	return [][2]string{
		{"atlas.json", `{"formatVersion":4,"key":"` + key + `","name":"Konten-App",
		  "processes":[{"id":"konto_anlegen","name":"Konto anlegen","path":"processes/konto.bpmn"}],
		  "informationModels":[{"name":"Konten","path":"architecture/konten.model.json"}],
		  "architecture":"architecture/capabilities.json"}`},
		{"processes/konto.bpmn", archProcess},
		{"architecture/konten.model.json", model},
		{"architecture/capabilities.json", arch},
	}
}

type archArtifact struct {
	Ref      string `json:"ref"`
	Status   string `json:"status"`
	Outcome  string `json:"outcome"`
	Proposed bool   `json:"proposed"`
	MappedTo string `json:"mappedTo"`
	Compared string `json:"compared"`
}

type archAnswer struct {
	ApplicationID string         `json:"applicationId"`
	Artifacts     []archArtifact `json:"artifacts"`
	Plan          *archAnswer    `json:"plan"`
	Error         string         `json:"error"`
}

func (a archAnswer) of(ref string) string {
	arts := a.Artifacts
	if a.Plan != nil {
		arts = a.Plan.Artifacts
	}
	for _, x := range arts {
		if x.Ref == ref {
			return x.Status + "/" + x.Outcome
		}
	}
	return "absent"
}

func importArch(t *testing.T, ts *httptest.Server, files [][2]string, query string) (int, archAnswer) {
	t.Helper()
	code, body := doReqBytes(t, ts, http.MethodPost, "/api/v1/applications/source"+query, sourceArchive(t, files), "application/gzip")
	var a archAnswer
	if err := json.Unmarshal(body, &a); err != nil {
		t.Fatalf("decode %s: %v", body, err)
	}
	return code, a
}

type capView struct {
	Key          string `json:"key"`
	Name         string `json:"name"`
	Requires     []string
	Realizations []struct {
		Kind           string `json:"kind"`
		ApplicationKey string `json:"applicationKey"`
		ProcessID      string `json:"processId"`
		BundledBy      string `json:"bundledBy"`
	} `json:"realizations"`
	Revision int64 `json:"revision"`
}

func getCap(t *testing.T, ts *httptest.Server, key string) (int, capView) {
	t.Helper()
	code, body := doReq(t, ts, http.MethodGet, "/api/v1/capabilities/"+key, "", "")
	var c capView
	_ = json.Unmarshal(body, &c)
	return code, c
}

func TestABundleBringsItsArchitectureAndTheMapStaysTheServers(t *testing.T) {
	ts := newTestServer(t)
	tree := archTree("konten-app", archModel, archFile)

	code, plan := importArch(t, ts, tree, "?dryRun=true")
	if code != http.StatusOK {
		t.Fatalf("plan: %d %+v", code, plan)
	}
	for ref, want := range map[string]string{
		"infomodel:Konten":          "new/created",
		"capability:konten":         "gap/undecided",
		"valuestream:arbeitsfaehig": "gap/undecided",
		"realizations:konten-app":   "new/created",
	} {
		if got := plan.of(ref); got != want {
			t.Errorf("plan %s = %s, want %s", ref, got, want)
		}
	}

	// Undecided gaps stop the import, and nothing is filed in the map.
	if code, ans := importArch(t, ts, tree, ""); code != http.StatusConflict {
		t.Fatalf("undecided import: %d %+v", code, ans)
	}
	if code, _ := getCap(t, ts, "konten"); code != http.StatusNotFound {
		t.Fatalf("an undecided import filed the capability: %d", code)
	}

	code, ans := importArch(t, ts, tree, "?adopt=capability:konten,valuestream:arbeitsfaehig")
	if code != http.StatusOK {
		t.Fatalf("import: %d %+v", code, ans)
	}
	code, c := getCap(t, ts, "konten")
	if code != http.StatusOK || c.Name != "Konten verwalten" {
		t.Fatalf("adopted capability: %d %+v", code, c)
	}
	if len(c.Realizations) != 1 || c.Realizations[0].BundledBy != "konten-app" || c.Realizations[0].ProcessID != "konto_anlegen" ||
		c.Realizations[0].ApplicationKey != "konten-app" {
		t.Fatalf("realizations read into the map = %+v", c.Realizations)
	}
	if code, _ := doReq(t, ts, http.MethodGet, "/api/v1/value-streams/arbeitsfaehig", "", ""); code != http.StatusOK {
		t.Fatalf("adopted value stream: %d", code)
	}
	code, body := doReq(t, ts, http.MethodGet, "/api/v1/infomodel/models?applicationId="+ans.ApplicationID, "", "")
	if code != http.StatusOK || !strings.Contains(string(body), `"Konten"`) {
		t.Fatalf("the application's information model: %d %s", code, body)
	}

	// The same tree again changes nothing.
	code, again := importArch(t, ts, tree, "?dryRun=true")
	if code != http.StatusOK {
		t.Fatalf("plan again: %d %+v", code, again)
	}
	for ref, want := range map[string]string{
		"infomodel:Konten":          "unchanged/unchanged",
		"capability:konten":         "linked/unchanged",
		"valuestream:arbeitsfaehig": "linked/unchanged",
		"realizations:konten-app":   "unchanged/unchanged",
	} {
		if got := again.of(ref); got != want {
			t.Errorf("second plan %s = %s, want %s", ref, got, want)
		}
	}

	// Sending the capability back as read does not copy the application's realization
	// onto the record: it stays the application's.
	_, raw := doReq(t, ts, http.MethodGet, "/api/v1/capabilities/konten", "", "")
	if code, body := doReq(t, ts, http.MethodPut, "/api/v1/capabilities/konten", string(raw), "application/json"); code != http.StatusOK {
		t.Fatalf("update: %d %s", code, body)
	}

	// A proposal that differs from the map's record never overwrites it.
	changed := strings.Replace(archFile, `"Konten verwalten"`, `"Konten anders"`, 1)
	code, linked := importArch(t, ts, archTree("konten-app", archModel, changed), "")
	if code != http.StatusOK || linked.of("capability:konten") != "linked/kept" {
		t.Fatalf("differing proposal: %d %s", code, linked.of("capability:konten"))
	}
	if _, c := getCap(t, ts, "konten"); c.Name != "Konten verwalten" {
		t.Fatalf("the map's record was overwritten: %+v", c)
	}

	// Deleting the application removes exactly its realizations; the capability stays.
	if code, body := doReq(t, ts, http.MethodDelete, "/api/v1/projects/"+ans.ApplicationID, "", ""); code != http.StatusNoContent {
		t.Fatalf("delete application: %d %s", code, body)
	}
	code, c = getCap(t, ts, "konten")
	if code != http.StatusOK || len(c.Realizations) != 0 {
		t.Fatalf("after deleting the application: %d %+v", code, c)
	}
}

// The release's architecture sketch draws what the bundle brought: the capability its
// process realizes, from the application's realizations alone.
func TestTheSketchDrawsWhatTheBundleBrought(t *testing.T) {
	ts := newTestServer(t)
	code, ans := importArch(t, ts, archTree("skizze-app", archModel, archFile), "?adopt=capability:konten,valuestream:arbeitsfaehig")
	if code != http.StatusOK {
		t.Fatalf("import: %d %+v", code, ans)
	}
	if code, body := doReq(t, ts, http.MethodPost, "/api/v1/applications/"+ans.ApplicationID+"/publish", `{"note":"skizze"}`, "application/json"); code != http.StatusOK && code != http.StatusCreated {
		t.Fatalf("publish: %d %s", code, body)
	}
	code, body := doReq(t, ts, http.MethodGet, "/api/v1/applications/"+ans.ApplicationID+"/releases/1/architecture?layers=strategy", "", "")
	if code != http.StatusOK {
		t.Fatalf("sketch: %d %s", code, body)
	}
	var sketch struct {
		Architecture struct {
			Strategy *struct {
				Capabilities []struct {
					Key        string `json:"key"`
					RealizedBy []struct {
						ProcessID string `json:"processId"`
					} `json:"realizedBy"`
				} `json:"capabilities"`
				ValueStreams []struct {
					Key string `json:"key"`
				} `json:"valueStreams"`
			} `json:"strategy"`
		} `json:"architecture"`
	}
	if err := json.Unmarshal(body, &sketch); err != nil {
		t.Fatal(err)
	}
	st := sketch.Architecture.Strategy
	if st == nil || len(st.Capabilities) != 1 || st.Capabilities[0].Key != "konten" ||
		len(st.Capabilities[0].RealizedBy) != 1 || st.Capabilities[0].RealizedBy[0].ProcessID != "konto_anlegen" {
		t.Fatalf("strategy layer = %s", body)
	}
	if len(st.ValueStreams) != 1 || st.ValueStreams[0].Key != "arbeitsfaehig" {
		t.Fatalf("value streams = %+v", st.ValueStreams)
	}
}

func TestAGapIsMappedOrLeftOut(t *testing.T) {
	ts := newTestServer(t)
	if code, body := doReq(t, ts, http.MethodPost, "/api/v1/capabilities",
		`{"key":"identitaet","name":"Identitaet und Zugang","state":"active"}`, "application/json"); code != http.StatusCreated {
		t.Fatalf("create: %d %s", code, body)
	}
	tree := archTree("map-app", archModel, archFile)
	code, ans := importArch(t, ts, tree, "?map=capability:konten=identitaet&omit=valuestream:arbeitsfaehig")
	if code != http.StatusOK {
		t.Fatalf("import: %d %+v", code, ans)
	}
	if got := ans.of("capability:konten"); got != "gap/mapped" {
		t.Fatalf("capability:konten = %s", got)
	}
	if code, _ := getCap(t, ts, "konten"); code != http.StatusNotFound {
		t.Fatalf("a mapped gap was filed under its own key: %d", code)
	}
	_, c := getCap(t, ts, "identitaet")
	if len(c.Realizations) != 1 || c.Realizations[0].ProcessID != "konto_anlegen" {
		t.Fatalf("realizations under the mapped key = %+v", c.Realizations)
	}
	if code, _ := doReq(t, ts, http.MethodGet, "/api/v1/value-streams/arbeitsfaehig", "", ""); code != http.StatusNotFound {
		t.Fatalf("an omitted value stream was filed: %d", code)
	}

	// Left out, the realization goes too.
	code, ans = importArch(t, ts, tree, "?omit=*")
	if code != http.StatusOK || ans.of("capability:konten") != "gap/omitted" {
		t.Fatalf("omit all: %d %s", code, ans.of("capability:konten"))
	}
	if _, c := getCap(t, ts, "identitaet"); len(c.Realizations) != 0 {
		t.Fatalf("an omitted key kept its realization: %+v", c.Realizations)
	}
}

func TestGapDecisionsThatCannotHoldAreRefused(t *testing.T) {
	ts := newTestServer(t)
	tree := archTree("refuse-app", archModel, archFile)
	for name, tc := range map[string]struct {
		query string
		want  int
	}{
		"mapped onto a key the map lacks":               {"?map=capability:konten=nirgends&omit=valuestream:arbeitsfaehig", http.StatusBadRequest},
		"a value stream mapped":                         {"?adopt=capability:konten&map=valuestream:arbeitsfaehig=x", http.StatusBadRequest},
		"a value stream adopted without its capability": {"?omit=capability:konten&adopt=valuestream:arbeitsfaehig", http.StatusConflict},
		"adopted with nothing proposed":                 {"?adopt=capability:nichts", http.StatusConflict},
	} {
		t.Run(name, func(t *testing.T) {
			code, ans := importArch(t, ts, tree, tc.query)
			if code != tc.want {
				t.Fatalf("status %d, want %d: %+v", code, tc.want, ans)
			}
			if code, _ := getCap(t, ts, "konten"); code != http.StatusNotFound {
				t.Fatalf("a refused import filed the capability: %d", code)
			}
		})
	}
	noProposal := `{"realizations":[{"capability":"fremd","kind":"process","processId":"konto_anlegen"}],"capabilities":[],"valueStreams":[]}`
	if code, ans := importArch(t, ts, archTree("refuse-app", archModel, noProposal), "?adopt=capability:fremd"); code != http.StatusBadRequest {
		t.Fatalf("adopting a gap without a proposal: %d %+v", code, ans)
	}
}

func TestABrokenArchitectureIsRefused(t *testing.T) {
	ts := newTestServer(t)
	for name, arch := range map[string]string{
		"a process of another application": `{"realizations":[{"capability":"k","kind":"process","processId":"fremd"}]}`,
		"a manual realization":             `{"realizations":[{"capability":"k","kind":"manual"}]}`,
		"a proposal carrying realizations": `{"capabilities":[{"key":"k","name":"K","state":"active","realizations":[{"kind":"manual","note":"x"}]}]}`,
		"an invalid proposal":              `{"capabilities":[{"key":"K!","name":"K","state":"active"}]}`,
		"an unknown field":                 `{"realisations":[]}`,
	} {
		t.Run(name, func(t *testing.T) {
			if code, ans := importArch(t, ts, archTree("broken", archModel, arch), "?dryRun=true"); code != http.StatusBadRequest {
				t.Fatalf("status %d, want 400: %+v", code, ans)
			}
		})
	}
	old := archTree("broken", archModel, archFile)
	old[0][1] = strings.Replace(old[0][1], `"formatVersion":4`, `"formatVersion":3`, 1)
	if code, _ := importArch(t, ts, old, "?dryRun=true"); code != http.StatusBadRequest {
		t.Fatalf("a format-3 manifest naming an architecture: %d, want 400", code)
	}
}

func TestAnInformationModelEditedHereIsAskedAbout(t *testing.T) {
	ts := newTestServer(t)
	tree := archTree("modell-app", archModel, `{"realizations":[],"capabilities":[],"valueStreams":[]}`)
	code, ans := importArch(t, ts, tree, "")
	if code != http.StatusOK {
		t.Fatalf("import: %d %+v", code, ans)
	}
	// A newer document is taken while the stored model is as the import left it.
	newer := archTree("modell-app", strings.Replace(archModel, "Was die Prozesse anlegen.", "Neu.", 1), tree[3][1])
	if code, plan := importArch(t, ts, newer, "?dryRun=true"); code != http.StatusOK || plan.of("infomodel:Konten") != "behind/updated" {
		t.Fatalf("newer document: %d %s", code, plan.of("infomodel:Konten"))
	}
	// Edited here, the same document is left; edited on both sides, it is asked about.
	_, body := doReq(t, ts, http.MethodGet, "/api/v1/infomodel/models?applicationId="+ans.ApplicationID, "", "")
	var list []struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(body, &list); err != nil || len(list) != 1 {
		t.Fatalf("models: %s", body)
	}
	modelID := list[0].ID
	_, full := doReq(t, ts, http.MethodGet, "/api/v1/infomodel/models/"+modelID, "", "")
	var m map[string]any
	_ = json.Unmarshal(full, &m)
	m["documentation"] = "Hier geaendert."
	edited, _ := json.Marshal(m)
	if code, body := doReq(t, ts, http.MethodPut, "/api/v1/infomodel/models/"+modelID, string(edited), "application/json"); code != http.StatusOK {
		t.Fatalf("edit the model: %d %s", code, body)
	}
	if code, plan := importArch(t, ts, tree, "?dryRun=true"); code != http.StatusOK || plan.of("infomodel:Konten") != "ahead/kept" {
		t.Fatalf("edited here: %d %s", code, plan.of("infomodel:Konten"))
	}
	if code, plan := importArch(t, ts, newer, ""); code != http.StatusConflict || plan.of("infomodel:Konten") != "changed/undecided" {
		t.Fatalf("edited on both sides: %d %s", code, plan.of("infomodel:Konten"))
	}
}

func TestAnExportCarriesTheArchitectureAndInstallsElsewhere(t *testing.T) {
	src := newTestServer(t)
	tree := archTree("roundtrip", archModel, archFile)
	code, ans := importArch(t, src, tree, "?adopt=capability:konten,valuestream:arbeitsfaehig")
	if code != http.StatusOK {
		t.Fatalf("import: %d %+v", code, ans)
	}
	code, body := doReq(t, src, http.MethodGet, "/api/v1/applications/"+ans.ApplicationID+"/source", "", "")
	if code != http.StatusOK {
		t.Fatalf("export: %d %s", code, body)
	}
	files := untarGz(t, body)
	if !strings.Contains(files["atlas.json"], `"formatVersion": 4`) ||
		!strings.Contains(files["atlas.json"], `"architecture": "architecture/capabilities.json"`) {
		t.Fatalf("manifest: %s", files["atlas.json"])
	}
	if !strings.Contains(files["architecture/capabilities.json"], `"processId": "konto_anlegen"`) ||
		!strings.Contains(files["architecture/capabilities.json"], `"key": "arbeitsfaehig"`) ||
		strings.Contains(files["architecture/capabilities.json"], "bundledBy") {
		t.Fatalf("architecture: %s", files["architecture/capabilities.json"])
	}
	modelPath := ""
	for p := range files {
		if strings.HasSuffix(p, ".model.json") {
			modelPath = p
		}
	}
	if modelPath == "" {
		t.Fatalf("no model in the export: %v", files)
	}

	// The exported tree installs on an empty server, and is unchanged on its own.
	exported := make([][2]string, 0, len(files))
	for p, data := range files {
		exported = append(exported, [2]string{p, data})
	}
	dst := newTestServer(t)
	if code, ans := importArch(t, dst, exported, "?adopt=capability:konten,valuestream:arbeitsfaehig"); code != http.StatusOK {
		t.Fatalf("install the export: %d %+v", code, ans)
	}
	if _, c := getCap(t, dst, "konten"); len(c.Realizations) != 1 {
		t.Fatalf("realizations after the round trip: %+v", c.Realizations)
	}
	if code, plan := importArch(t, src, exported, "?dryRun=true"); code != http.StatusOK ||
		plan.of("infomodel:Konten") != "unchanged/unchanged" || plan.of("realizations:roundtrip") != "unchanged/unchanged" {
		t.Fatalf("the export against its own server: %d model %s realizations %s", code, plan.of("infomodel:Konten"), plan.of("realizations:roundtrip"))
	}
}

func untarGz(t *testing.T, body []byte) map[string]string {
	t.Helper()
	gz, err := gzip.NewReader(bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	tr := tar.NewReader(gz)
	out := map[string]string{}
	for {
		h, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		data, err := io.ReadAll(tr)
		if err != nil {
			t.Fatal(err)
		}
		out[h.Name] = string(data)
	}
	return out
}
