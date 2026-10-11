package api_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

// The workplace example (examples/arbeitsplatz) is the internal counterpart of the trip
// booking: a wizard a signed-in person walks in the Tasks app (ADR-0449). The personnel
// office reports a new hire, the first screen is addressed to the hire's supervisor,
// the answer on it picks the next screen, and the provisioning after the sitting is the
// IT group's work. These tests run the example's own files the way its README
// describes: root starts as the personnel office, alice is the supervisor, ivan is in
// the group it.

type workplaceBooks struct {
	ts                 *httptest.Server
	admin, alice, ivan *http.Client
	defKey             uint64
}

func fileWorkplaceExample(t *testing.T) workplaceBooks {
	t.Helper()
	ts, _ := newAuthServer(t, "root", "rootpassword")
	admin := newClient(t)
	if login(t, admin, ts, "root", "rootpassword") != http.StatusOK {
		t.Fatal("admin login failed")
	}
	ids := map[string]string{}
	clients := map[string]*http.Client{}
	for _, u := range []string{"alice", "ivan"} {
		code, b := cReq(t, admin, ts, "POST", "/api/v1/users", `{"username":"`+u+`","password":"password1"}`)
		if code != http.StatusCreated {
			t.Fatalf("create %s: %d (%s)", u, code, b)
		}
		var created struct{ ID string }
		if err := json.Unmarshal(b, &created); err != nil || created.ID == "" {
			t.Fatalf("decode %s: %v (%s)", u, err, b)
		}
		ids[u] = created.ID
		c := newClient(t)
		if login(t, c, ts, u, "password1") != http.StatusOK {
			t.Fatalf("%s login failed", u)
		}
		clients[u] = c
	}
	code, b := cReq(t, admin, ts, "POST", "/api/v1/groups", `{"name":"it"}`)
	if code != http.StatusCreated {
		t.Fatalf("create group: %d (%s)", code, b)
	}
	var grp struct{ ID string }
	if err := json.Unmarshal(b, &grp); err != nil || grp.ID == "" {
		t.Fatalf("decode group: %v (%s)", err, b)
	}
	if code, b := cReq(t, admin, ts, "PUT", "/api/v1/groups/"+grp.ID+"/members/"+ids["ivan"], ""); code != http.StatusNoContent && code != http.StatusOK {
		t.Fatalf("add ivan: %d (%s)", code, b)
	}

	dir := filepath.Join("..", "examples", "arbeitsplatz")
	read := func(name string) string {
		t.Helper()
		raw, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		return string(raw)
	}
	for _, id := range []string{"arbeitsplatz-start", "arbeitsplatz-geraet", "arbeitsplatz-laptop", "arbeitsplatz-desktop",
		"arbeitsplatz-software", "arbeitsplatz-abschluss", "arbeitsplatz-bereitstellen"} {
		body := fmt.Sprintf(`{"id":%q,"name":%q,"schema":%s}`, id, id, read(id+".form.json"))
		if code, out := cReq(t, admin, ts, "POST", "/api/v1/forms", body); code != http.StatusOK {
			t.Fatalf("save form %s: %d (%s)", id, code, out)
		}
	}
	defKey := deployKey(t, ts, admin, read("arbeitsplatz.bpmn"))
	return workplaceBooks{ts: ts, admin: admin, alice: clients["alice"], ivan: clients["ivan"], defKey: defKey}
}

// report has the personnel office start the process with its start form. The first
// screen is alice's, so the person who started is told it was handed over, and is
// shown nothing of it.
func (b workplaceBooks) report(t *testing.T) uint64 {
	t.Helper()
	code, body := cReq(t, b.admin, b.ts, "POST", fmt.Sprintf("/api/v1/processes/%d/instances", b.defKey),
		`{"variables":{"neuePerson":"Mia Muster","eintrittsdatum":"2026-11-02","vorgesetzter":"alice"}}`)
	a := decodeWizardAnswer(t, code, body)
	if a.Wizard == nil || a.Wizard.State != "handedOver" || a.Wizard.Task != nil {
		t.Fatalf("the start into alice's first screen: %+v, want handedOver without the task", a.Wizard)
	}
	return a.InstanceKey
}

// inbox lists the open tasks c sees by element id.
func (b workplaceBooks) inbox(t *testing.T, c *http.Client) map[string]uint64 {
	t.Helper()
	code, body := cReq(t, c, b.ts, "GET", "/api/v1/tasks", "")
	if code != http.StatusOK {
		t.Fatalf("list tasks: %d (%s)", code, body)
	}
	var page struct {
		Items []struct {
			Key       uint64 `json:"key"`
			ElementID string `json:"elementId"`
		} `json:"items"`
	}
	if err := json.Unmarshal(body, &page); err != nil {
		t.Fatalf("decode tasks: %v (%s)", err, body)
	}
	out := map[string]uint64{}
	for _, it := range page.Items {
		out[it.ElementID] = it.Key
	}
	return out
}

// open is alice opening the first screen from her inbox and submitting it.
func (b workplaceBooks) open(t *testing.T, vars string) wizardAnswer {
	t.Helper()
	first, ok := b.inbox(t, b.alice)["geraet_waehlen"]
	if !ok {
		t.Fatal("the first screen is not in alice's inbox")
	}
	return completeTask(t, b.ts, b.alice, first, vars)
}

// next has alice submit the screen a carried her to.
func (b workplaceBooks) next(t *testing.T, a wizardAnswer, vars string) wizardAnswer {
	t.Helper()
	return completeTask(t, b.ts, b.alice, a.Wizard.Task.Key, vars)
}

// carried asserts a is the screen elementID, claimed for alice.
func carried(t *testing.T, a wizardAnswer, elementID string) {
	t.Helper()
	wantStep(t, a, elementID)
	if a.Wizard.Task.Assignee != "alice" {
		t.Errorf("the screen %s is held by %q, want alice", elementID, a.Wizard.Task.Assignee)
	}
}

// variables reads what the instance holds.
func (b workplaceBooks) variables(t *testing.T, instance uint64) map[string]any {
	t.Helper()
	code, out := cReq(t, b.admin, b.ts, "GET", fmt.Sprintf("/api/v1/instances/%d/variables", instance), "")
	if code != http.StatusOK {
		t.Fatalf("variables: %d (%s)", code, out)
	}
	var vars map[string]any
	if err := json.Unmarshal(out, &vars); err != nil {
		t.Fatalf("decode variables: %v (%s)", err, out)
	}
	return vars
}

// TestTheWorkplaceExampleCarriesTheSupervisorThroughTheLaptopScreens is the README's
// first run: a laptop. The personnel office's start is handed over, alice opens her
// first screen from the inbox and is carried from there, the laptop screen follows the
// laptop, and the provisioning is the IT group's, not hers.
func TestTheWorkplaceExampleCarriesTheSupervisorThroughTheLaptopScreens(t *testing.T) {
	b := fileWorkplaceExample(t)
	instance := b.report(t)

	a := b.open(t, `{"geraet":"Laptop","arbeitsort":"Beides"}`)
	carried(t, a, "laptop")
	a = b.next(t, a, `{"modell":"Leistung","dockingStation":true}`)
	carried(t, a, "software")
	a = b.next(t, a, `{"pakete":["Office","Entwicklung"],"bemerkung":"Zweiter Bildschirm im Homeoffice"}`)
	carried(t, a, "abschluss")
	if a = b.next(t, a, `{"bestaetigt":true}`); a.Wizard == nil || a.Wizard.State != "done" || a.Wizard.Task != nil {
		t.Fatalf("after the summary: %+v, want done", a.Wizard)
	}

	if _, seen := b.inbox(t, b.alice)["bereitstellen"]; seen {
		t.Error("the IT's provisioning is in the supervisor's inbox")
	}
	bereitstellen, ok := b.inbox(t, b.ivan)["bereitstellen"]
	if !ok {
		t.Fatal("the provisioning is not in the IT group's inbox")
	}

	// What the IT reads is what the wizard maps out: the choices and the estimate the
	// script computed. The summary's own tick box stays inside.
	vars := b.variables(t, instance)
	if vars["geraet"] != "Laptop" || vars["modell"] != "Leistung" || vars["kosten"] != float64(2400) {
		t.Errorf("mapped out: %v", vars)
	}
	if _, kept := vars["bestaetigt"]; kept {
		t.Errorf("bestaetigt left the wizard without an output mapping: %v", vars)
	}
	if done := completeTask(t, b.ts, b.ivan, bereitstellen, `{"bereitgestellt":true}`); done.Wizard != nil {
		t.Errorf("the provisioning outside the sitting answered with a wizard: %+v", done.Wizard)
	}
}

// TestTheWorkplaceExampleShowsTheDesktopScreenForADesktop: the other branch is the
// other screen, and the estimate follows the screens counted on it.
func TestTheWorkplaceExampleShowsTheDesktopScreenForADesktop(t *testing.T) {
	b := fileWorkplaceExample(t)
	instance := b.report(t)

	a := b.open(t, `{"geraet":"Desktop","arbeitsort":"Büro"}`)
	carried(t, a, "desktop")
	a = b.next(t, a, `{"bildschirme":2,"standort":"Haus B, 3.12"}`)
	carried(t, a, "software")
	a = b.next(t, a, `{"pakete":["Office"]}`)
	carried(t, a, "abschluss")
	if a = b.next(t, a, `{"bestaetigt":true}`); a.Wizard == nil || a.Wizard.State != "done" {
		t.Fatalf("after the summary: %+v, want done", a.Wizard)
	}
	if vars := b.variables(t, instance); vars["kosten"] != float64(1600) || vars["modell"] != nil {
		t.Errorf("a desktop with two screens: %v, want kosten 1600 and no laptop model", vars)
	}
}

// TestTheWorkplaceExampleSkipsTheDeviceScreensWithoutADevice: no device takes the
// gateway's default past both device screens.
func TestTheWorkplaceExampleSkipsTheDeviceScreensWithoutADevice(t *testing.T) {
	b := fileWorkplaceExample(t)
	instance := b.report(t)

	a := b.open(t, `{"geraet":"Kein Gerät","arbeitsort":"Büro"}`)
	carried(t, a, "software")
	a = b.next(t, a, `{}`)
	carried(t, a, "abschluss")
	if a = b.next(t, a, `{"bestaetigt":true}`); a.Wizard == nil || a.Wizard.State != "done" {
		t.Fatalf("after the summary: %+v, want done", a.Wizard)
	}
	if vars := b.variables(t, instance); vars["kosten"] != float64(0) {
		t.Errorf("no device: kosten = %v, want 0", vars["kosten"])
	}
}
