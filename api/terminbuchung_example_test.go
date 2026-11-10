package api_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/pblumer/atlas/connector/calendar"
	"github.com/pblumer/atlas/job"
	"github.com/pblumer/atlas/worker"
)

// The appointment booking example (examples/terminbuchung) is the walkthrough of the
// calendar Worker Type (ADR-draft-calendar-worker-type) in a public wizard
// (ADR-0449): a visitor without an account picks a service, sees the free slots read
// from the workshop's calendar, picks one, gives their details and books. These tests
// run it against the files in the repository, as an anonymous visitor of the public
// link, with the calendar Worker served from the mock — the same model a Microsoft 365
// Worker would serve.

const bookingCalendar = "werkstatt@velo-beispiel.ch"

// publishBooking files the example, deploys it and publishes its start link.
func publishBooking(t *testing.T) (*httptest.Server, string) {
	t.Helper()
	ts := newTestServer(t)
	c := ts.Client()
	dir := filepath.Join("..", "examples", "terminbuchung")
	read := func(name string) string {
		t.Helper()
		b, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		return string(b)
	}
	for _, id := range []string{"termin-dienstleistung", "termin-wahl", "termin-angaben", "termin-bestaetigung", "termin-vorbereiten"} {
		body := fmt.Sprintf(`{"id":%q,"name":%q,"schema":%s}`, id, id, read(id+".form.json"))
		if code, out := cReq(t, c, ts, "POST", "/api/v1/forms", body); code != http.StatusOK {
			t.Fatalf("save form %s: %d (%s)", id, code, out)
		}
	}
	deployKey(t, ts, c, read("terminbuchung.bpmn"))
	code, out := cReq(t, c, ts, "POST", "/api/v1/public-links", `{"processId":"proc_terminbuchung"}`)
	if code != http.StatusOK {
		t.Fatalf("publish: %d (%s)", code, out)
	}
	var link struct{ Token string }
	if err := json.Unmarshal(out, &link); err != nil || link.Token == "" {
		t.Fatalf("decode link: %v (%s)", err, out)
	}
	return ts, link.Token
}

// serveCalendar is the calendar Worker: it leases every waiting calendar job, runs it
// through worker.RunCalendarJob against reg — the call a supervised worker makes — and
// completes it, or throws the BPMN error the run ended with. It returns when no job is
// waiting, so a chain of calendar tasks between two screens is served in one call.
func serveCalendar(t *testing.T, ts *httptest.Server, reg *calendar.Registry) int {
	t.Helper()
	c := ts.Client()
	served := 0
	for round := 0; round < 20; round++ {
		code, out := cReq(t, c, ts, "POST", "/api/v1/jobs/activate", `{"type":"io.atlas.calendar","worker":"suite","leaseMs":60000,"maxJobs":10}`)
		if code != http.StatusOK {
			t.Fatalf("lease: %d (%s)", code, out)
		}
		var resp struct {
			Jobs []worker.Job `json:"jobs"`
		}
		if err := json.Unmarshal(out, &resp); err != nil {
			t.Fatalf("decode lease: %v (%s)", err, out)
		}
		if len(resp.Jobs) == 0 {
			return served
		}
		for _, j := range resp.Jobs {
			served++
			vars, err := worker.RunCalendarJob(context.Background(), j, reg)
			var be *job.BPMNError
			switch {
			case errors.As(err, &be):
				body, _ := json.Marshal(map[string]any{"worker": "suite", "leaseToken": j.LeaseToken, "errorCode": be.Code, "errorMessage": be.Message})
				if code, out := cReq(t, c, ts, "POST", fmt.Sprintf("/api/v1/jobs/%d/error", j.JobKey), string(body)); code != http.StatusOK {
					t.Fatalf("throw %s: %d (%s)", be.Code, code, out)
				}
			case err != nil:
				t.Fatalf("calendar job %s: %v", j.ElementID, err)
			default:
				body, _ := json.Marshal(map[string]any{"worker": "suite", "leaseToken": j.LeaseToken, "variables": vars})
				if code, out := cReq(t, c, ts, "POST", fmt.Sprintf("/api/v1/jobs/%d/complete", j.JobKey), string(body)); code != http.StatusOK {
					t.Fatalf("complete %s: %d (%s)", j.ElementID, code, out)
				}
			}
		}
	}
	t.Fatal("calendar jobs kept coming")
	return served
}

// bookingVisit is the visitor's page: it sends one request and, while the sitting is pending
// on a calendar job, serves the calendar and asks again.
func bookingVisit(t *testing.T, ts *httptest.Server, reg *calendar.Registry, method, path, pass, body string) tripAnswer {
	t.Helper()
	code, out := anon(t, ts, method, path, pass, body)
	if code != http.StatusOK {
		t.Fatalf("%s %s: %d (%s)", method, path, code, out)
	}
	var a tripAnswer
	if err := json.Unmarshal(out, &a); err != nil {
		t.Fatalf("decode %s: %v (%s)", path, err, out)
	}
	for i := 0; a.State == "pending" && i < 100; i++ {
		if serveCalendar(t, ts, reg) == 0 {
			time.Sleep(10 * time.Millisecond)
		}
		code, out = anon(t, ts, "GET", "/public/wizard/step", pass, "")
		if code != http.StatusOK {
			t.Fatalf("step: %d (%s)", code, out)
		}
		a = tripAnswer{}
		if err := json.Unmarshal(out, &a); err != nil {
			t.Fatalf("decode step: %v (%s)", err, out)
		}
	}
	return a
}

func startBooking(t *testing.T, ts *httptest.Server, reg *calendar.Registry, token, service string) (string, tripAnswer) {
	t.Helper()
	code, out := anon(t, ts, "POST", "/public/forms/"+token+"/start", "", `{"variables":{"dienstleistung":"`+service+`"}}`)
	if code != http.StatusOK {
		t.Fatalf("start: %d (%s)", code, out)
	}
	var started struct {
		Wizard *tripAnswer `json:"wizard"`
	}
	if err := json.Unmarshal(out, &started); err != nil || started.Wizard == nil || started.Wizard.Pass == "" {
		t.Fatalf("the start handed no pass: %v (%s)", err, out)
	}
	pass := started.Wizard.Pass
	return pass, bookingVisit(t, ts, reg, "GET", "/public/wizard/step", pass, "")
}

// slotOptions reads the slot options the choice screen offers.
func slotOptions(t *testing.T, s *tripScreen) []map[string]any {
	t.Helper()
	raw, ok := s.Data["terminOptionen"].([]any)
	if !ok || len(raw) == 0 {
		t.Fatalf("the choice screen offers no slots: %v", s.Data)
	}
	out := make([]map[string]any, len(raw))
	for i, o := range raw {
		out[i] = o.(map[string]any)
	}
	return out
}

func bookingComplete(t *testing.T, ts *httptest.Server, reg *calendar.Registry, pass string, s *tripScreen, vars string) tripAnswer {
	t.Helper()
	return bookingVisit(t, ts, reg, "POST", fmt.Sprintf("/public/wizard/steps/%d/complete", s.TaskKey), pass, `{"variables":`+vars+`}`)
}

func mockRegistry() (*calendar.Registry, *calendar.MockCalendars) {
	m := calendar.NewMockCalendars(map[string]calendar.MockCalendarSpec{bookingCalendar: {}})
	reg := calendar.NewRegistry()
	reg.Register("filiale", m)
	return reg, m
}

const visitor = `{"kunde":{"name":"Eva Muster","email":"eva@example.ch"},"velo":{"typ":"ebike","marke":"Flyer"},"mitteilung":"Bremsen quietschen"}`

// TestTheBookingExampleBooksAFreeSlotFromTheCalendar walks the README's first run: the
// free slots come from the calendar, a value the visitor makes up is not taken, the
// chosen slot ends up in the calendar with the visitor invited, and the workshop gets
// its task.
func TestTheBookingExampleBooksAFreeSlotFromTheCalendar(t *testing.T) {
	ts, token := publishBooking(t)
	reg, m := mockRegistry()
	pass, a := startBooking(t, ts, reg, token, "service")

	wahl := onScreen(t, a, "Termin wählen")
	opts := slotOptions(t, wahl)
	first := opts[0]["value"].(string)
	if _, err := time.Parse(time.RFC3339, first); err != nil {
		t.Fatalf("an option's value is not the slot's start: %v", opts[0])
	}

	// The list is the process's, not the visitor's to send.
	path := fmt.Sprintf("/public/wizard/steps/%d/complete", wahl.TaskKey)
	if code, out := anon(t, ts, "POST", path, pass, `{"variables":{"termin":"`+first+`","terminOptionen":[]}}`); code != http.StatusBadRequest {
		t.Fatalf("the visitor sent the option list: %d (%s), want 400", code, out)
	}
	// A slot nobody offered is not taken: the screen comes back.
	a = bookingComplete(t, ts, reg, pass, wahl, `{"termin":"2031-01-01T03:00:00+01:00"}`)
	wahl = onScreen(t, a, "Termin wählen")

	a = bookingComplete(t, ts, reg, pass, wahl, `{"termin":"`+first+`"}`)
	angaben := onScreen(t, a, "Ihre Angaben")
	a = bookingComplete(t, ts, reg, pass, angaben, visitor)

	quittung := onScreen(t, a, "Bestätigung")
	slot, _ := quittung.Data["slot"].(map[string]any)
	if slot == nil || slot["start"] != first {
		t.Fatalf("the confirmation shows %v, want the slot booked", quittung.Data)
	}
	evs, err := m.Snapshot(bookingCalendar)
	if err != nil {
		t.Fatal(err)
	}
	if len(evs) != 1 || evs[0].Start.Format(time.RFC3339) != first && !sameInstant(evs[0].Start, first) {
		t.Fatalf("calendar holds %+v, want the one booking at %s", evs, first)
	}
	if evs[0].Subject != "Veloservice – Eva Muster" || len(evs[0].Attendees) != 1 || evs[0].Attendees[0].Email != "eva@example.ch" {
		t.Fatalf("the booking is %+v; want the confirmed subject and the visitor invited", evs[0])
	}
	if !strings.Contains(evs[0].Body, "ebike") || !strings.Contains(evs[0].Body, "Bremsen quietschen") {
		t.Fatalf("the booking's body is %q", evs[0].Body)
	}
	if a = bookingComplete(t, ts, reg, pass, quittung, `{"gelesen":true}`); a.State != "done" {
		t.Fatalf("after the confirmation: %+v, want done", a)
	}
}

// TestTheBookingExampleLetsTheVisitorChooseAgainWhenTheSlotIsTaken walks the second
// run: while the visitor gives their details, somebody else takes the slot in the
// calendar. The reservation finds it, gives the slot up, and the visitor is back at
// the choice with a fresh list that no longer offers it.
func TestTheBookingExampleLetsTheVisitorChooseAgainWhenTheSlotIsTaken(t *testing.T) {
	ts, token := publishBooking(t)
	reg, m := mockRegistry()
	pass, a := startBooking(t, ts, reg, token, "ergonomie")

	wahl := onScreen(t, a, "Termin wählen")
	first := slotOptions(t, wahl)[0]["value"].(string)
	a = bookingComplete(t, ts, reg, pass, wahl, `{"termin":"`+first+`"}`)
	angaben := onScreen(t, a, "Ihre Angaben")

	// A staff member books the same half hour in Outlook meanwhile.
	start, _ := time.Parse(time.RFC3339, first)
	if _, err := m.Create(context.Background(), bookingCalendar, calendar.NewEvent{Subject: "Lieferung", Start: start, End: start.Add(30 * time.Minute)}); err != nil {
		t.Fatal(err)
	}
	a = bookingComplete(t, ts, reg, pass, angaben, visitor)

	wahl = onScreen(t, a, "Termin wählen")
	if wahl.Data["terminVergeben"] != true {
		t.Errorf("the choice screen does not say the slot was taken: %v", wahl.Data)
	}
	for _, o := range slotOptions(t, wahl) {
		if o["value"] == first {
			t.Fatalf("the taken slot is still offered: %v", o)
		}
	}
	evs, _ := m.Snapshot(bookingCalendar)
	if len(evs) != 1 || evs[0].Subject != "Lieferung" {
		t.Fatalf("calendar holds %+v; the reservation that lost must be gone", evs)
	}
}

func sameInstant(t1 time.Time, s string) bool {
	t2, err := time.Parse(time.RFC3339, s)
	return err == nil && t1.Equal(t2)
}
