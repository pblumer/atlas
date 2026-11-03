package api_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
)

// stammdatenDMN publishes two decisions: Kostenstellen, a boxed list without inputs —
// the shape a form's options come from — and Rabatt, which takes an input and so cannot.
func stammdatenDMN(entries ...string) string {
	var list strings.Builder
	for _, e := range entries {
		list.WriteString(`<literalExpression><text>` + e + `</text></literalExpression>`)
	}
	return `<?xml version="1.0" encoding="UTF-8"?>
<definitions xmlns="https://www.omg.org/spec/DMN/20191111/MODEL/" id="stammdaten" name="Stammdaten" namespace="http://atlas/test">
  <inputData id="i_menge" name="Menge"><variable name="Menge" typeRef="number"/></inputData>
  <decision id="kostenstellen" name="Kostenstellen">
    <variable name="Kostenstellen"/>
    <list id="kostenstellenListe">` + list.String() + `</list>
  </decision>
  <decision id="rabatt" name="Rabatt">
    <variable name="Rabatt" typeRef="number"/>
    <informationRequirement id="ir"><requiredInput href="#i_menge"/></informationRequirement>
    <literalExpression><text>if Menge > 10 then 5 else 0</text></literalExpression>
  </decision>
</definitions>`
}

const kostenstelleForm = `{"id":"bestellung","name":"Bestellung","schema":{"type":"default","components":[
  {"type":"select","key":"kostenstelle","label":"Kostenstelle","atlasDecision":{"version":1,"decisionId":"Kostenstellen"}},
  {"type":"checklist","key":"weitere","label":"Weitere","atlasDecision":{"version":1,"decisionId":"Kostenstellen"}}
]}}`

const kostenstelleBPMN = `<?xml version="1.0" encoding="UTF-8"?>
<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL"
             xmlns:zeebe="http://camunda.org/schema/zeebe/1.0" targetNamespace="http://atlas/test">
  <process id="bestellung" isExecutable="true" name="Bestellung">
    <startEvent id="s"><extensionElements><zeebe:formDefinition formId="bestellung"/></extensionElements></startEvent>
    <userTask id="pruefen" name="Prüfen"><extensionElements><zeebe:formDefinition formId="bestellung"/></extensionElements></userTask>
    <endEvent id="e"/>
    <sequenceFlow id="f1" sourceRef="s" targetRef="pruefen"/>
    <sequenceFlow id="f2" sourceRef="pruefen" targetRef="e"/>
  </process>
</definitions>`

func decodeFD(t *testing.T, body []byte, v any) {
	t.Helper()
	if err := json.Unmarshal(body, v); err != nil {
		t.Fatalf("decode: %v (%s)", err, body)
	}
}

type optionsReply struct {
	DecisionID string `json:"decisionId"`
	Options    []struct {
		Label string `json:"label"`
		Value any    `json:"value"`
	} `json:"options"`
	Skipped int    `json:"skipped"`
	Code    string `json:"code"`
	Error   string `json:"error"`
}

// askOptions posts to an options route and decodes the answer.
func askOptions(t *testing.T, ts *httptest.Server, path, body string) (int, optionsReply) {
	t.Helper()
	code, out := doReq(t, ts, http.MethodPost, path, body, "application/json")
	var r optionsReply
	decodeFD(t, out, &r)
	return code, r
}

// decisionOptions is the form editor asking a decision about inputs of its own.
func decisionOptions(t *testing.T, ts *httptest.Server, id, inputs string) (int, optionsReply) {
	t.Helper()
	return askOptions(t, ts, "/api/v1/form-data/decisions/"+id+"/options", inputs)
}

// TestFormOptionsComeFromADecision walks the feature end to end over HTTP: a list
// published as a decision is offered to the form editor, gives a saved form's fields
// their options, is checked again when the task completes, is written into the form a
// public link serves and checked when that form is submitted — and a newer version of
// the decision is what the next person sees, without touching the form.
func TestFormOptionsComeFromADecision(t *testing.T) {
	ts := newTestServer(t)

	v1 := stammdatenDMN(`{value: "CC100", label: "Einkauf"}`, `{value: "CC200", label: "Verkauf"}`, `null`)
	if code, body := doReq(t, ts, http.MethodPost, "/api/v1/decision-deployments", v1, "application/xml"); code != http.StatusOK {
		t.Fatalf("deploy decision: %d %s", code, body)
	}

	// The editor's picker offers both decisions, each with the inputs a field has to
	// give it.
	code, body := doReq(t, ts, http.MethodGet, "/api/v1/form-data/decisions", "", "")
	if code != http.StatusOK || !strings.Contains(string(body), `"id":"Kostenstellen","name":"Kostenstellen","model":"Stammdaten","inputs":[]`) ||
		!strings.Contains(string(body), `"inputs":[{"name":"Menge","type":"number"}]`) {
		t.Fatalf("picker: %d %s", code, body)
	}

	code, opts := decisionOptions(t, ts, "Kostenstellen", "")
	if code != http.StatusOK || len(opts.Options) != 2 || opts.Options[0].Value != "CC100" || opts.Options[1].Label != "Verkauf" || opts.Skipped != 1 {
		t.Fatalf("options: %d %+v", code, opts)
	}
	if code, r := decisionOptions(t, ts, "Rabatt", ""); code != http.StatusUnprocessableEntity || r.Code != "unbound_input" {
		t.Fatalf("a decision whose input is not given: %d %+v", code, r)
	}
	if code, r := decisionOptions(t, ts, "Rabatt", `{"inputs":{"Menge":20}}`); code != http.StatusUnprocessableEntity || r.Code != "not_a_list" {
		t.Fatalf("a decision that answers a number: %d %+v", code, r)
	}
	if code, r := decisionOptions(t, ts, "Unbekannt", ""); code != http.StatusNotFound || r.Code != "not_deployed" {
		t.Fatalf("an unknown decision: %d %+v", code, r)
	}

	// The save holds the block to its shape.
	bad := strings.Replace(kostenstelleForm, `"version":1,"decisionId":"Kostenstellen"}},
  {"type":"checklist"`, `"version":2,"decisionId":"Kostenstellen"}},
  {"type":"checklist"`, 1)
	if code, body := doReq(t, ts, http.MethodPost, "/api/v1/forms", bad, "application/json"); code != http.StatusBadRequest || !strings.Contains(string(body), "newer than this server reads") {
		t.Fatalf("saving a newer block: %d %s", code, body)
	}
	if code, body := doReq(t, ts, http.MethodPost, "/api/v1/forms", kostenstelleForm, "application/json"); code != http.StatusOK {
		t.Fatalf("save form: %d %s", code, body)
	}
	// What a rendered form asks: the stored form's field, by key.
	if code, r := askOptions(t, ts, "/api/v1/forms/bestellung/fields/weitere/options", `{}`); code != http.StatusOK || len(r.Options) != 2 {
		t.Fatalf("the form's field: %d %+v", code, r)
	}
	if code, r := askOptions(t, ts, "/api/v1/forms/bestellung/fields/nirgends/options", `{}`); code != http.StatusNotFound || r.Code != "not_found" {
		t.Fatalf("a field the form does not have: %d %+v", code, r)
	}
	if code, body := doReq(t, ts, http.MethodPost, "/api/v1/forms/keins/fields/weitere/options", `{}`, "application/json"); code != http.StatusNotFound {
		t.Fatalf("a form that does not exist: %d %s", code, body)
	}

	code, body = doReq(t, ts, http.MethodPost, "/api/v1/deployments", kostenstelleBPMN, "application/xml")
	if code != http.StatusOK {
		t.Fatalf("deploy process: %d %s", code, body)
	}
	var dep struct {
		Key uint64 `json:"key"`
	}
	decodeFD(t, body, &dep)
	if code, body := doReq(t, ts, http.MethodPost, "/api/v1/processes/"+strconv.FormatUint(dep.Key, 10)+"/instances", `{}`, "application/json"); code != http.StatusOK {
		t.Fatalf("start: %d %s", code, body)
	}
	code, body = doReq(t, ts, http.MethodGet, "/api/v1/tasks", "", "")
	if code != http.StatusOK {
		t.Fatalf("tasks: %d %s", code, body)
	}
	var tasks []struct {
		Key       uint64 `json:"key"`
		ProcessID string `json:"processId"`
	}
	decodeFD(t, listRows(t, body), &tasks)
	if len(tasks) != 1 {
		t.Fatalf("tasks = %+v; want the one review", tasks)
	}
	complete := "/api/v1/tasks/" + strconv.FormatUint(tasks[0].Key, 10) + "/complete"

	// A value the list does not offer is refused, and the task stays open.
	for _, vars := range []string{
		`{"variables":{"kostenstelle":"CC999"}}`,
		`{"variables":{"kostenstelle":"CC100","weitere":["CC200","CC300"]}}`,
		`{"variables":{"weitere":"CC200"}}`,
	} {
		code, body := doReq(t, ts, http.MethodPost, complete, vars, "application/json")
		if code != http.StatusBadRequest || !strings.Contains(string(body), `"code":"invalid_value"`) {
			t.Fatalf("completing with %s: %d %s; want 400 invalid_value", vars, code, body)
		}
	}
	if code, body := doReq(t, ts, http.MethodPost, complete, `{"variables":{"kostenstelle":"CC100","weitere":["CC200"]}}`, "application/json"); code != http.StatusOK {
		t.Fatalf("completing with offered values: %d %s", code, body)
	}

	// A public link: the visitor is served the form with the options written in, and
	// what they submit is held to the same list.
	code, body = doReq(t, ts, http.MethodPost, "/api/v1/public-links", `{"processId":"bestellung"}`, "application/json")
	if code != http.StatusOK {
		t.Fatalf("publish: %d %s", code, body)
	}
	var link struct {
		Token string `json:"token"`
	}
	decodeFD(t, body, &link)
	code, body = doReq(t, ts, http.MethodGet, "/public/forms/"+link.Token+"/schema", "", "")
	if code != http.StatusOK || strings.Contains(string(body), "atlasDecision") || !strings.Contains(string(body), `"label":"Einkauf"`) {
		t.Fatalf("public schema: %d %s; want the options written in", code, body)
	}
	if code, body := doReq(t, ts, http.MethodPost, "/public/forms/"+link.Token+"/start", `{"variables":{"kostenstelle":"CC999"}}`, "application/json"); code != http.StatusBadRequest || !strings.Contains(string(body), "invalid_value") {
		t.Fatalf("public start with a value not offered: %d %s", code, body)
	}
	if code, body := doReq(t, ts, http.MethodPost, "/public/forms/"+link.Token+"/start", `{"variables":{"kostenstelle":"CC200"}}`, "application/json"); code != http.StatusOK {
		t.Fatalf("public start: %d %s", code, body)
	}

	// The owners of the list publish a new version; the form is not touched, and the
	// next person to open it sees the new list.
	v2 := stammdatenDMN(`{value: "CC300", label: "Logistik"}`)
	if code, body := doReq(t, ts, http.MethodPost, "/api/v1/decision-deployments", v2, "application/xml"); code != http.StatusOK {
		t.Fatalf("deploy v2: %d %s", code, body)
	}
	if code, opts := askOptions(t, ts, "/api/v1/forms/bestellung/fields/kostenstelle/options", ""); code != http.StatusOK || len(opts.Options) != 1 || opts.Options[0].Value != "CC300" {
		t.Fatalf("options after v2: %d %+v", code, opts)
	}
	if code, body := doReq(t, ts, http.MethodPost, "/public/forms/"+link.Token+"/start", `{"variables":{"kostenstelle":"CC200"}}`, "application/json"); code != http.StatusBadRequest {
		t.Fatalf("a value the new version no longer offers: %d %s", code, body)
	}
}

// TestAPublicWizardStepShowsAndChecksADecisionList: a screen of a public wizard whose field
// takes its options from a decision is served with the options written in — the visitor
// has nobody to ask the options route as — and what the visitor submits on it is held to
// the list.
func TestAPublicWizardStepShowsAndChecksADecisionList(t *testing.T) {
	ts := newTestServer(t)
	c := ts.Client()
	if code, body := doReq(t, ts, http.MethodPost, "/api/v1/decision-deployments",
		stammdatenDMN(`"Rom"`, `"Paris"`), "application/xml"); code != http.StatusOK {
		t.Fatalf("deploy decision: %d %s", code, body)
	}
	for _, f := range []string{
		`{"id":"pw-start","name":"Start","schema":{"type":"default","components":[{"type":"textfield","key":"customer"},{"type":"textfield","key":"notiz"}]}}`,
		`{"id":"pw-eins","name":"Eins","schema":{"type":"default","components":[{"type":"textfield","key":"weiter"},{"type":"textfield","key":"customer"}]}}`,
		`{"id":"pw-zwei","name":"Zwei","schema":{"type":"default","components":[{"type":"radio","key":"ziel","label":"Ziel","atlasDecision":{"version":1,"decisionId":"Kostenstellen"}}]}}`,
	} {
		if code, body := cReq(t, c, ts, "POST", "/api/v1/forms", f); code != http.StatusOK {
			t.Fatalf("save form: %d (%s)", code, body)
		}
	}
	deployKey(t, ts, c, publicWizardFlowBPMN)
	code, body := cReq(t, c, ts, "POST", "/api/v1/public-links", `{"processId":"antrag"}`)
	if code != http.StatusOK {
		t.Fatalf("publish: %d (%s)", code, body)
	}
	var link struct{ Token string }
	if err := json.Unmarshal(body, &link); err != nil || link.Token == "" {
		t.Fatalf("decode link: %v (%s)", err, body)
	}
	pass, first := startPublic(t, ts, link.Token)

	code, body = anon(t, ts, "POST", "/public/wizard/steps/"+strconv.FormatUint(first, 10)+"/complete", pass, `{"variables":{"weiter":"a"}}`)
	second := decodePublic(t, code, body)
	if second.Step == nil || second.Step.Title != "Reiseziel" {
		t.Fatalf("after the first screen: %s", body)
	}
	if strings.Contains(string(body), "atlasDecision") || !strings.Contains(string(body), `"label":"Paris"`) {
		t.Fatalf("the screen was not served with the options written in: %s", body)
	}
	if code, body := anon(t, ts, "GET", "/public/wizard/step", pass, ""); code != http.StatusOK || !strings.Contains(string(body), `"label":"Rom"`) {
		t.Fatalf("the step lookup: %d %s", code, body)
	}

	complete := "/public/wizard/steps/" + strconv.FormatUint(second.Step.TaskKey, 10) + "/complete"
	if code, body := anon(t, ts, "POST", complete, pass, `{"variables":{"ziel":"Berlin"}}`); code != http.StatusBadRequest || !strings.Contains(string(body), "invalid_value") {
		t.Fatalf("a value the list does not offer: %d %s", code, body)
	}
	if done := anonAnswer(t, ts, "POST", complete, pass, `{"variables":{"ziel":"Paris"}}`); done.State != "done" {
		t.Fatalf("after the last screen: %+v, want done", done)
	}
}

// teamsDMN publishes Gruppen, a list without inputs, and Mitglieder, whose list depends
// on the group it is given.
const teamsDMN = `<?xml version="1.0" encoding="UTF-8"?>
<definitions xmlns="https://www.omg.org/spec/DMN/20191111/MODEL/" id="teams" name="Teams" namespace="http://atlas/test">
  <inputData id="i_gruppe" name="Gruppe"><variable name="Gruppe" typeRef="string"/></inputData>
  <decision id="gruppen" name="Gruppen">
    <variable name="Gruppen"/>
    <list id="gruppenListe">
      <literalExpression><text>"IT"</text></literalExpression>
      <literalExpression><text>"Vertrieb"</text></literalExpression>
    </list>
  </decision>
  <decision id="mitglieder" name="Mitglieder">
    <variable name="Mitglieder"/>
    <informationRequirement id="ir_gruppe"><requiredInput href="#i_gruppe"/></informationRequirement>
    <literalExpression><text>if Gruppe = "IT" then [{value: "anna", label: "Anna"}, "ben"] else if Gruppe = "Vertrieb" then ["carla"] else ["direktion"]</text></literalExpression>
  </decision>
</definitions>`

const teamsForm = `{"id":"team","name":"Team","schema":{"type":"default","components":[
  {"type":"select","key":"gruppe","label":"Gruppe","atlasDecision":{"version":1,"decisionId":"Gruppen"}},
  {"type":"radio","key":"mitglied","label":"Mitglied","atlasDecision":{"version":1,"decisionId":"Mitglieder","inputs":[{"name":"Gruppe","field":"gruppe"}]}}
]}}`

const teamsBPMN = `<?xml version="1.0" encoding="UTF-8"?>
<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL"
             xmlns:zeebe="http://camunda.org/schema/zeebe/1.0" targetNamespace="http://atlas/test">
  <process id="team" isExecutable="true" name="Team">
    <startEvent id="s"><extensionElements><zeebe:formDefinition formId="team"/></extensionElements></startEvent>
    <userTask id="waehlen" name="Wählen"><extensionElements><zeebe:formDefinition formId="team"/></extensionElements></userTask>
    <endEvent id="e"/>
    <sequenceFlow id="f1" sourceRef="s" targetRef="waehlen"/>
    <sequenceFlow id="f2" sourceRef="waehlen" targetRef="e"/>
  </process>
</definitions>`

// TestAChoiceDecidesWhatTheNextFieldOffers: a field whose decision is bound to another
// field offers what the decision answers for the choice made there — in the console, on
// a public link — and a submission is held to the list its own choice gives.
func TestAChoiceDecidesWhatTheNextFieldOffers(t *testing.T) {
	ts := newTestServer(t)
	if code, body := doReq(t, ts, http.MethodPost, "/api/v1/decision-deployments", teamsDMN, "application/xml"); code != http.StatusOK {
		t.Fatalf("deploy decisions: %d %s", code, body)
	}
	if code, body := doReq(t, ts, http.MethodPost, "/api/v1/forms", teamsForm, "application/json"); code != http.StatusOK {
		t.Fatalf("save form: %d %s", code, body)
	}

	ask := "/api/v1/forms/team/fields/mitglied/options"
	code, r := askOptions(t, ts, ask, `{"values":{"gruppe":"IT"}}`)
	if code != http.StatusOK || len(r.Options) != 2 || r.Options[0].Label != "Anna" || r.Options[1].Value != "ben" {
		t.Fatalf("members of IT: %d %+v", code, r)
	}
	if code, r := askOptions(t, ts, ask, `{"values":{"gruppe":"Vertrieb"}}`); code != http.StatusOK || len(r.Options) != 1 || r.Options[0].Value != "carla" {
		t.Fatalf("members of Vertrieb: %d %+v", code, r)
	}
	// Nothing chosen yet: nothing offered.
	if code, r := askOptions(t, ts, ask, `{"values":{}}`); code != http.StatusOK || len(r.Options) != 0 {
		t.Fatalf("before a group is chosen: %d %+v", code, r)
	}
	// A group the group field does not offer is not asked about — the decision knows
	// members the form never shows.
	if code, r := askOptions(t, ts, ask, `{"values":{"gruppe":"Geschäftsleitung"}}`); code != http.StatusBadRequest || r.Code != "invalid_query" {
		t.Fatalf("a group the form does not offer: %d %+v", code, r)
	}
	if code, r := askOptions(t, ts, ask, `{"values":{"gruppe":"IT","sonst":"x"}}`); code != http.StatusBadRequest || r.Code != "invalid_query" {
		t.Fatalf("a value of a field the options do not depend on: %d %+v", code, r)
	}

	// The task: the member is held to the list of the group submitted with it.
	code, body := doReq(t, ts, http.MethodPost, "/api/v1/deployments", teamsBPMN, "application/xml")
	if code != http.StatusOK {
		t.Fatalf("deploy process: %d %s", code, body)
	}
	var dep struct {
		Key uint64 `json:"key"`
	}
	decodeFD(t, body, &dep)
	if code, body := doReq(t, ts, http.MethodPost, "/api/v1/processes/"+strconv.FormatUint(dep.Key, 10)+"/instances", `{}`, "application/json"); code != http.StatusOK {
		t.Fatalf("start: %d %s", code, body)
	}
	_, body = doReq(t, ts, http.MethodGet, "/api/v1/tasks", "", "")
	var tasks []struct {
		Key uint64 `json:"key"`
	}
	decodeFD(t, listRows(t, body), &tasks)
	if len(tasks) != 1 {
		t.Fatalf("tasks = %+v", tasks)
	}
	complete := "/api/v1/tasks/" + strconv.FormatUint(tasks[0].Key, 10) + "/complete"
	if code, body := doReq(t, ts, http.MethodPost, complete, `{"variables":{"gruppe":"IT","mitglied":"carla"}}`, "application/json"); code != http.StatusBadRequest || !strings.Contains(string(body), "invalid_value") {
		t.Fatalf("a member of another group: %d %s", code, body)
	}
	if code, body := doReq(t, ts, http.MethodPost, complete, `{"variables":{"gruppe":"Vertrieb","mitglied":"carla"}}`, "application/json"); code != http.StatusOK {
		t.Fatalf("a member of the group chosen: %d %s", code, body)
	}

	// A public link: the group's list is written in, the members are asked as the
	// visitor chooses, through the link.
	code, body = doReq(t, ts, http.MethodPost, "/api/v1/public-links", `{"processId":"team"}`, "application/json")
	if code != http.StatusOK {
		t.Fatalf("publish: %d %s", code, body)
	}
	var link struct {
		Token string `json:"token"`
	}
	decodeFD(t, body, &link)
	_, body = doReq(t, ts, http.MethodGet, "/public/forms/"+link.Token+"/schema", "", "")
	if strings.Count(string(body), "atlasDecision") != 1 || !strings.Contains(string(body), `"label":"Vertrieb"`) {
		t.Fatalf("public schema: %s; want the groups written in and the members still asked", body)
	}
	publicAsk := "/public/forms/" + link.Token + "/fields/mitglied/options"
	if code, r := askOptions(t, ts, publicAsk, `{"values":{"gruppe":"IT"}}`); code != http.StatusOK || len(r.Options) != 2 {
		t.Fatalf("public members of IT: %d %+v", code, r)
	}
	if code, r := askOptions(t, ts, publicAsk, `{"values":{"gruppe":"Geschäftsleitung"}}`); code != http.StatusBadRequest {
		t.Fatalf("a visitor asking about a group the form does not offer: %d %+v", code, r)
	}
	if code, _ := doReq(t, ts, http.MethodPost, "/public/forms/nicht-da/fields/mitglied/options", `{}`, "application/json"); code != http.StatusNotFound {
		t.Fatalf("an unknown link: %d", code)
	}
	if code, body := doReq(t, ts, http.MethodPost, "/public/forms/"+link.Token+"/start", `{"variables":{"gruppe":"IT","mitglied":"carla"}}`, "application/json"); code != http.StatusBadRequest {
		t.Fatalf("public start with a member of another group: %d %s", code, body)
	}
	if code, body := doReq(t, ts, http.MethodPost, "/public/forms/"+link.Token+"/start", `{"variables":{"gruppe":"IT","mitglied":"anna"}}`, "application/json"); code != http.StatusOK {
		t.Fatalf("public start: %d %s", code, body)
	}
}

// TestAWizardScreenAsksItsBoundField: on a public wizard's screen, the field bound to
// another field of the same screen is asked through the visitor's pass.
func TestAWizardScreenAsksItsBoundField(t *testing.T) {
	ts := newTestServer(t)
	c := ts.Client()
	if code, body := doReq(t, ts, http.MethodPost, "/api/v1/decision-deployments", teamsDMN, "application/xml"); code != http.StatusOK {
		t.Fatalf("deploy decisions: %d %s", code, body)
	}
	for _, f := range []string{
		`{"id":"pw-start","name":"Start","schema":{"type":"default","components":[{"type":"textfield","key":"customer"},{"type":"textfield","key":"notiz"}]}}`,
		`{"id":"pw-eins","name":"Eins","schema":{"type":"default","components":[{"type":"textfield","key":"weiter"},{"type":"textfield","key":"customer"}]}}`,
		`{"id":"pw-zwei","name":"Zwei","schema":{"type":"default","components":[
		  {"type":"select","key":"gruppe","atlasDecision":{"version":1,"decisionId":"Gruppen"}},
		  {"type":"select","key":"ziel","atlasDecision":{"version":1,"decisionId":"Mitglieder","inputs":[{"name":"Gruppe","field":"gruppe"}]}}]}}`,
	} {
		if code, body := cReq(t, c, ts, "POST", "/api/v1/forms", f); code != http.StatusOK {
			t.Fatalf("save form: %d (%s)", code, body)
		}
	}
	deployKey(t, ts, c, publicWizardFlowBPMN)
	code, body := cReq(t, c, ts, "POST", "/api/v1/public-links", `{"processId":"antrag"}`)
	if code != http.StatusOK {
		t.Fatalf("publish: %d (%s)", code, body)
	}
	var link struct{ Token string }
	if err := json.Unmarshal(body, &link); err != nil {
		t.Fatal(err)
	}
	pass, first := startPublic(t, ts, link.Token)
	second := anonAnswer(t, ts, "POST", "/public/wizard/steps/"+strconv.FormatUint(first, 10)+"/complete", pass, `{"variables":{"weiter":"a"}}`)
	if second.Step == nil {
		t.Fatalf("no second screen: %+v", second)
	}
	step := strconv.FormatUint(second.Step.TaskKey, 10)
	code, body = anon(t, ts, "POST", "/public/wizard/steps/"+step+"/fields/ziel/options", pass, `{"values":{"gruppe":"Vertrieb"}}`)
	if code != http.StatusOK || !strings.Contains(string(body), `"value":"carla"`) {
		t.Fatalf("the screen's bound field: %d %s", code, body)
	}
	if code, _ := anon(t, ts, "POST", "/public/wizard/steps/"+step+"/fields/ziel/options", "", `{"values":{"gruppe":"Vertrieb"}}`); code != http.StatusNotFound {
		t.Fatalf("without a pass: %d", code)
	}
	if code, _ := anon(t, ts, "POST", "/public/wizard/steps/"+strconv.FormatUint(first, 10)+"/fields/ziel/options", pass, `{}`); code != http.StatusNotFound {
		t.Fatalf("a screen that is no longer open: %d", code)
	}
	if code, _ := anon(t, ts, "POST", "/public/wizard/steps/abc/fields/ziel/options", pass, `{}`); code != http.StatusNotFound {
		t.Fatalf("not a task key: %d", code)
	}
	complete := "/public/wizard/steps/" + step + "/complete"
	if code, body := anon(t, ts, "POST", complete, pass, `{"variables":{"gruppe":"Vertrieb","ziel":"anna"}}`); code != http.StatusBadRequest {
		t.Fatalf("a member of another group: %d %s", code, body)
	}
	if done := anonAnswer(t, ts, "POST", complete, pass, `{"variables":{"gruppe":"Vertrieb","ziel":"carla"}}`); done.State != "done" {
		t.Fatalf("after the screen: %+v", done)
	}
}
