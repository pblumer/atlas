package api_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// The trip booking example (examples/reisebuchung) is the walkthrough of ADR-0449: a
// public start form, then a public wizard whose screens a decision table picks. Its
// README describes two runs and what each one shows. This test runs both against the
// files in the repository, as an anonymous visitor of the public link, so the example
// cannot drift from what it teaches.

type tripScreen struct {
	TaskKey       uint64         `json:"taskKey"`
	Title         string         `json:"title"`
	Documentation string         `json:"documentation"`
	Data          map[string]any `json:"data"`
}

type tripAnswer struct {
	Pass  string      `json:"pass"`
	State string      `json:"state"`
	Step  *tripScreen `json:"step"`
}

// publishTripBooking files the example the way its README does (forms, the decision
// model and its reference, the process), publishes the process's start link, and
// returns the server, the link's token and the definition's key.
func publishTripBooking(t *testing.T) (*httptest.Server, string, uint64) {
	t.Helper()
	ts := newTestServer(t)
	c := ts.Client()
	dir := filepath.Join("..", "examples", "reisebuchung")
	read := func(name string) string {
		t.Helper()
		b, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		return string(b)
	}
	for _, id := range []string{"reise-start", "reise-visum", "reise-einverstaendnis", "reise-bestaetigung"} {
		body := fmt.Sprintf(`{"id":%q,"name":%q,"schema":%s}`, id, id, read(id+".form.json"))
		if code, out := cReq(t, c, ts, "POST", "/api/v1/forms", body); code != http.StatusOK {
			t.Fatalf("save form %s: %d (%s)", id, code, out)
		}
	}
	if code, out := cReqTyped(t, c, ts, "POST", "/api/v1/dmn-models?handle=reisepflichten", "application/xml", read("reisepflichten.dmn")); code != http.StatusOK {
		t.Fatalf("upload decision model: %d (%s)", code, out)
	}
	if code, out := cReq(t, c, ts, "POST", "/api/v1/dmnrefs", `{"name":"ReisePflichten","modelRef":"reisepflichten"}`); code != http.StatusOK {
		t.Fatalf("register decision: %d (%s)", code, out)
	}
	defKey := deployKey(t, ts, c, read("reisebuchung.bpmn"))
	code, out := cReq(t, c, ts, "POST", "/api/v1/public-links", `{"processId":"proc_reisebuchung"}`)
	if code != http.StatusOK {
		t.Fatalf("publish: %d (%s)", code, out)
	}
	var link struct{ Token string }
	if err := json.Unmarshal(out, &link); err != nil || link.Token == "" {
		t.Fatalf("decode link: %v (%s)", err, out)
	}
	return ts, link.Token, defKey
}

// tripCall is one call of the visitor's page, decoded. A sitting that is pending,
// the decision between two screens still being evaluated off the loop, is asked again
// as the page asks again.
func tripCall(t *testing.T, ts *httptest.Server, method, path, pass, body string) tripAnswer {
	t.Helper()
	code, out := anon(t, ts, method, path, pass, body)
	if code != http.StatusOK {
		t.Fatalf("%s %s: %d (%s)", method, path, code, out)
	}
	var a tripAnswer
	if err := json.Unmarshal(out, &a); err != nil {
		t.Fatalf("decode %s: %v (%s)", path, err, out)
	}
	for i := 0; a.State == "pending" && i < 200; i++ {
		time.Sleep(10 * time.Millisecond)
		a = tripCall(t, ts, "GET", "/public/wizard/step", pass, "")
	}
	return a
}

// tripStart fills the start form and returns the pass and the first screen.
func tripStart(t *testing.T, ts *httptest.Server, token, vars string) (string, tripAnswer) {
	t.Helper()
	code, out := anon(t, ts, "POST", "/public/forms/"+token+"/start", "", `{"variables":`+vars+`}`)
	if code != http.StatusOK {
		t.Fatalf("start: %d (%s)", code, out)
	}
	var started struct {
		Started bool        `json:"started"`
		Wizard  *tripAnswer `json:"wizard"`
	}
	if err := json.Unmarshal(out, &started); err != nil || !started.Started || started.Wizard == nil || started.Wizard.Pass == "" {
		t.Fatalf("the start handed no pass: %v (%s)", err, out)
	}
	pass := started.Wizard.Pass
	a := *started.Wizard
	if a.State == "pending" {
		a = tripCall(t, ts, "GET", "/public/wizard/step", pass, "")
	}
	return pass, a
}

// onScreen fails unless a is the screen titled title.
func onScreen(t *testing.T, a tripAnswer, title string) *tripScreen {
	t.Helper()
	if a.State != "step" || a.Step == nil || a.Step.Title != title {
		t.Fatalf("want the screen %q, got %+v", title, a)
	}
	return a.Step
}

// TestTheTripBookingExampleWalksAMinorOnALongHaulTripThroughBothExtraScreens is run A
// of the README: a long-haul trip for a sixteen-year-old. The decision table asks for
// both the visa and the consent, so the visitor sees three screens after the start
// form, each saying why it is there, and cannot change what the last one only shows.
func TestTheTripBookingExampleWalksAMinorOnALongHaulTripThroughBothExtraScreens(t *testing.T) {
	ts, token, defKey := publishTripBooking(t)
	pass, a := tripStart(t, ts, token, `{"reiseart":"Fernreise","ziel":"th","reisepassNr":"X1234567","geburtsdatum":"2010-05-01"}`)

	visum := onScreen(t, a, "Visum-Angaben")
	if !strings.Contains(visum.Documentation, "ReisePflichten") {
		t.Errorf("the visa screen does not say why it is there: %q", visum.Documentation)
	}
	a = tripCall(t, ts, "POST", fmt.Sprintf("/public/wizard/steps/%d/complete", visum.TaskKey), pass,
		`{"variables":{"staatsangehoerigkeit":"CH","passGueltigBis":"2031-01-31","visumBestaetigt":true}}`)

	// Both branches of the inclusive split are open at once; to the one visitor they are
	// two screens in a row.
	einv := onScreen(t, a, "Einverständnis einholen")
	if !strings.Contains(einv.Documentation, "minderjährig") {
		t.Errorf("the consent screen does not say why it is there: %q", einv.Documentation)
	}
	a = tripCall(t, ts, "POST", fmt.Sprintf("/public/wizard/steps/%d/complete", einv.TaskKey), pass,
		`{"variables":{"vertreterName":"Eva Muster","einverstaendnis":true}}`)

	// The confirmation shows the trip type and the age the process computed, in fields
	// it does not submit: the visitor may not send an age of their own.
	bestaetigen := onScreen(t, a, "Buchung bestätigen")
	if bestaetigen.Data["reiseart"] != "Fernreise" || bestaetigen.Data["alter"] != float64(16) {
		t.Errorf("the confirmation shows %v, want the trip type and the computed age 16", bestaetigen.Data)
	}
	path := fmt.Sprintf("/public/wizard/steps/%d/complete", bestaetigen.TaskKey)
	if code, out := anon(t, ts, "POST", path, pass, `{"variables":{"alter":18,"buchungBestaetigt":true}}`); code != http.StatusBadRequest {
		t.Errorf("the visitor wrote the computed age: %d (%s), want 400", code, out)
	}
	if a = tripCall(t, ts, "POST", path, pass, `{"variables":{"buchungBestaetigt":true}}`); a.State != "done" {
		t.Fatalf("after the confirmation: %+v, want done", a)
	}

	// The README's proof that the split is inclusive: with two branches open, the join
	// is visited once per opened branch, and the default branch was suppressed.
	if v := tripVisits(t, ts, defKey, pass); v["split"] != 1 || v["visum"] != 1 || v["einverstaendnis"] != 1 || v["join"] != 2 || v["bestaetigen"] != 1 {
		t.Errorf("element visits = %v, want split 1, visum 1, einverstaendnis 1, join 2, bestaetigen 1", v)
	}

	// What the process keeps is what the wizard maps out; the screens' own tick boxes
	// stay inside it.
	vars := tripVariables(t, ts, pass)
	if vars["buchungBestaetigt"] != true || vars["vertreterName"] != "Eva Muster" || vars["alter"] != float64(16) {
		t.Errorf("mapped out: %v", vars)
	}
	if p, _ := vars["pflichten"].(map[string]any); p["brauchtVisum"] != true || p["brauchtEinverstaendnis"] != true {
		t.Errorf("pflichten = %v, want both", vars["pflichten"])
	}
	if _, kept := vars["visumBestaetigt"]; kept {
		t.Errorf("visumBestaetigt left the wizard without an output mapping: %v", vars)
	}
}

// tripVisits reads how often each element of the visitor's instance was visited.
func tripVisits(t *testing.T, ts *httptest.Server, defKey uint64, pass string) map[string]int {
	t.Helper()
	key, _, _ := strings.Cut(pass, ".")
	code, out := cReq(t, ts.Client(), ts, "GET", fmt.Sprintf("/api/v1/processes/%d/runtime?instance=%s", defKey, key), "")
	if code != http.StatusOK {
		t.Fatalf("runtime: %d (%s)", code, out)
	}
	var rt struct {
		Elements []struct {
			ElementID string `json:"elementId"`
			Visits    int    `json:"visits"`
		} `json:"elements"`
	}
	if err := json.Unmarshal(out, &rt); err != nil {
		t.Fatalf("decode runtime: %v (%s)", err, out)
	}
	visits := map[string]int{}
	for _, e := range rt.Elements {
		visits[e.ElementID] = e.Visits
	}
	return visits
}

// tripVariables reads what the finished instance holds.
func tripVariables(t *testing.T, ts *httptest.Server, pass string) map[string]any {
	t.Helper()
	key, _, _ := strings.Cut(pass, ".")
	code, out := cReq(t, ts.Client(), ts, "GET", "/api/v1/instances/"+key+"/variables", "")
	if code != http.StatusOK {
		t.Fatalf("variables: %d (%s)", code, out)
	}
	var vars map[string]any
	if err := json.Unmarshal(out, &vars); err != nil {
		t.Fatalf("decode variables: %v (%s)", err, out)
	}
	return vars
}

// TestTheTripBookingExampleTakesAnAdultOnADomesticTripStraightToTheConfirmation is run
// B: the decision table asks for nothing extra, the split takes its default, and the
// first screen after the start form is the confirmation.
func TestTheTripBookingExampleTakesAnAdultOnADomesticTripStraightToTheConfirmation(t *testing.T) {
	ts, token, defKey := publishTripBooking(t)
	pass, a := tripStart(t, ts, token, `{"reiseart":"Inland","ziel":"sylt","geburtsdatum":"1990-03-15"}`)

	bestaetigen := onScreen(t, a, "Buchung bestätigen")
	if bestaetigen.Data["reiseart"] != "Inland" || bestaetigen.Data["alter"] != float64(36) {
		t.Errorf("the confirmation shows %v, want the trip type and the computed age 36", bestaetigen.Data)
	}
	a = tripCall(t, ts, "POST", fmt.Sprintf("/public/wizard/steps/%d/complete", bestaetigen.TaskKey), pass,
		`{"variables":{"buchungBestaetigt":true}}`)
	if a.State != "done" {
		t.Fatalf("after the confirmation: %+v, want done", a)
	}
	if v := tripVisits(t, ts, defKey, pass); v["split"] != 1 || v["visum"] != 0 || v["einverstaendnis"] != 0 || v["join"] != 1 {
		t.Errorf("element visits = %v, want the default branch alone: split 1, join 1, no extra screen", v)
	}

	// A screen nobody was shown answered nothing: its mapped-out fields are null.
	vars := tripVariables(t, ts, pass)
	if p, _ := vars["pflichten"].(map[string]any); p["brauchtVisum"] != false || p["brauchtEinverstaendnis"] != false {
		t.Errorf("pflichten = %v, want neither", vars["pflichten"])
	}
	if v, ok := vars["vertreterName"]; !ok || v != nil || vars["buchungBestaetigt"] != true {
		t.Errorf("mapped out: %v", vars)
	}
}
