package calendar

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/pblumer/atlas/connector/calendar/catalog"
	"github.com/pblumer/atlas/connector/calendar/googlefake"
)

func googleRegistry(t *testing.T, cals map[string]googlefake.Calendar, subject string) (*Registry, *googlefake.Server) {
	t.Helper()
	fake := googlefake.New(cals)
	t.Cleanup(fake.Close)
	b, err := NewBackend(fake.APIURL(), googlefake.Bundle(subject), Options{HTTPClient: fake.HTTPClient(), TokenURL: fake.TokenURL()})
	if err != nil {
		t.Fatal(err)
	}
	reg := NewRegistry()
	reg.Register("shop", b)
	return reg, fake
}

func TestTheGoogleBundleIsReadStrictly(t *testing.T) {
	for name, tc := range map[string]struct{ endpoint, bundle, want string }{
		"no key":           {"", `{"provider":"google-calendar","clientEmail":"a@b"}`, "needs clientEmail and privateKey"},
		"not a key":        {"", `{"provider":"google-calendar","clientEmail":"a@b","privateKey":"x"}`, "not valid PEM"},
		"a Graph field":    {"", `{"provider":"google-calendar","clientEmail":"a@b","privateKey":"x","tenantId":"t"}`, "tenantId belongs to another provider"},
		"a Google field":   {"", `{"provider":"microsoft-graph","tenantId":"t","clientId":"c","clientSecret":"s","subject":"x"}`, "subject belongs to another provider"},
		"mock with a key":  {"", `{"provider":"mock","clientEmail":"a@b"}`, "clientEmail belongs to another provider"},
		"http to the open": {"http://www.googleapis.com/calendar/v3", googlefake.Bundle(""), "not an https URL"},
	} {
		err := CheckBundle(tc.endpoint, tc.bundle)
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: err = %v, want %q", name, err, tc.want)
		}
	}
	if err := CheckBundle("", googlefake.Bundle("buchung@firma.ch")); err != nil {
		t.Errorf("a complete Google bundle: %v", err)
	}
}

func TestAnEventIDCarriesItsKey(t *testing.T) {
	for _, key := range []string{"k1", "a3f09c1e", "delivered-twice"} {
		id := googleEventID(key)
		if googleKeyOf(id) != key || len(id) < 5 || strings.ContainsAny(id, "wxyz-") {
			t.Errorf("key %q → id %q → %q", key, id, googleKeyOf(id))
		}
	}
	if googleKeyOf("seed1") != "" || googleKeyOf("atlaszz") != "" {
		t.Error("an id atlas did not choose was read as carrying a key")
	}
}

func TestGoogleWalksABookingFromReservationToCancellation(t *testing.T) {
	reg, fake := googleRegistry(t, map[string]googlefake.Calendar{"werkstatt": {Busy: []googlefake.Busy{
		{Start: at(25, 10, 0), End: at(25, 11, 0)},
		{Start: at(25, 13, 30), End: at(25, 18, 0), Transparent: true},
		{Start: at(26, 0, 0), End: at(27, 0, 0), AllDay: true}, // Tuesday off
	}}}, "buchung@firma.ch")
	ctx := context.Background()
	out, err := RunAt(ctx, findJob("werkstatt"), reg, now)
	if err != nil {
		t.Fatal(err)
	}
	if got := times(slotsOf(t, out)); got != "25 08:00,25 09:00,25 11:00,25 13:30,25 14:30,25 15:30,25 16:30" {
		t.Fatalf("slots %s; the all-day appointment must block Tuesday", got)
	}

	res := reserveJob("k1", at(25, 9, 0), at(25, 10, 0))
	res.Calendar = "werkstatt"
	out, err = RunAt(ctx, res, reg, now)
	if err != nil {
		t.Fatal(err)
	}
	id := out["booking"].(map[string]any)["bookingId"].(string)
	if id != googleEventID("k1") {
		t.Fatalf("the booking id %q is not the one chosen from the key", id)
	}
	base := Job{Connector: "shop", Calendar: "werkstatt", BookingID: id, TimeZone: "Europe/Zurich", ResultVariable: "b", ErrorCodes: Codes}

	confirm := base
	confirm.Operation = catalog.OpConfirmBooking
	confirm.Subject, confirm.Body = "Veloservice – Muster", "E-Bike"
	confirm.Attendees = []Attendee{{Email: "kundin@example.ch", Name: "Kundin"}}
	if _, err := RunAt(ctx, confirm, reg, now); err != nil {
		t.Fatal(err)
	}
	ev := fake.Events("werkstatt")[3]
	if ev.Summary != "Veloservice – Muster" || ev.Description != "E-Bike" || len(ev.Attendees) != 1 || ev.SendUpdates != "all" {
		t.Fatalf("confirmed event %+v; the attendees must be told", ev)
	}

	get := base
	get.Operation = catalog.OpGetBooking
	out, err = RunAt(ctx, get, reg, now)
	if err != nil || out["b"].(map[string]any)["exists"] != true || out["b"].(map[string]any)["start"] != "2030-03-25T09:00:00+01:00" {
		t.Fatalf("get: %v %v", out, err)
	}

	cancel := base
	cancel.Operation, cancel.ResultVariable = catalog.OpCancelBooking, ""
	for i := 0; i < 2; i++ { // the second is answered 410 Gone, which is success
		if _, err := RunAt(ctx, cancel, reg, now); err != nil {
			t.Fatalf("cancel %d: %v", i, err)
		}
	}
	if out, err = RunAt(ctx, get, reg, now); err != nil || out["b"].(map[string]any)["exists"] != false {
		t.Fatalf("get after cancel: %v %v", out, err)
	}
	if _, err := RunAt(ctx, confirm, reg, now); bpmnCode(err) != CodeBookingNotFound {
		t.Fatalf("confirm after cancel: %v", err)
	}
}

// TestARepeatedDeliveryRevivesTheReservationItGaveUp: Google never lets an id be used
// twice, a deleted event's included. A reservation that lost and is delivered again
// confirms its cancelled event and runs the rule on it again — and keeps the slot when
// the competitor has gone in the meantime.
func TestARepeatedDeliveryRevivesTheReservationItGaveUp(t *testing.T) {
	reg, fake := googleRegistry(t, map[string]googlefake.Calendar{"werkstatt": {}}, "")
	ctx := context.Background()
	other := reserveJob("other", at(25, 9, 0), at(25, 10, 0))
	other.Calendar = "werkstatt"
	out, err := RunAt(ctx, other, reg, now)
	if err != nil {
		t.Fatal(err)
	}
	j := reserveJob("k1", at(25, 9, 0), at(25, 10, 0))
	j.Calendar = "werkstatt"
	if _, err := RunAt(ctx, j, reg, now); bpmnCode(err) != CodeSlotTaken {
		t.Fatalf("first delivery: %v, want the slot taken", err)
	}
	cancel := Job{Connector: "shop", Operation: catalog.OpCancelBooking, Calendar: "werkstatt",
		BookingID: out["booking"].(map[string]any)["bookingId"].(string), ErrorCodes: Codes}
	if _, err := RunAt(ctx, cancel, reg, now); err != nil {
		t.Fatal(err)
	}
	if _, err := RunAt(ctx, j, reg, now); err != nil {
		t.Fatalf("the repeated delivery did not keep the now free slot: %v", err)
	}
	live := 0
	for _, e := range fake.Events("werkstatt") {
		if e.Status != "cancelled" {
			live++
			if e.ID != googleEventID("k1") {
				t.Errorf("the live event is %q", e.ID)
			}
		}
	}
	if live != 1 {
		t.Fatalf("%d live events, want the revived reservation alone", live)
	}
}

func TestGoogleTellsACalendarFromABookingAndRefusesAttendeesItMayNotInvite(t *testing.T) {
	reg, _ := googleRegistry(t, map[string]googlefake.Calendar{
		"werkstatt": {}, "lesen": {ReadOnly: true}, "ohne-delegation": {RefuseAttendees: true},
	}, "")
	ctx := context.Background()
	res := reserveJob("k", at(25, 9, 0), at(25, 10, 0))
	res.Calendar = "ohne-delegation"
	out, err := RunAt(ctx, res, reg, now)
	if err != nil {
		t.Fatal(err)
	}
	attendees := Job{Connector: "shop", Operation: catalog.OpConfirmBooking, Calendar: "ohne-delegation",
		BookingID: out["booking"].(map[string]any)["bookingId"].(string), Attendees: []Attendee{{Email: "kundin@example.ch"}}, ErrorCodes: Codes}
	for name, tc := range map[string]struct {
		job  Job
		code string
	}{
		"find in an unknown calendar":    {findJob("fehlt"), CodeCalendarNotFound},
		"reserve in a read-only one":     {func() Job { j := reserveJob("k2", at(25, 9, 0), at(25, 10, 0)); j.Calendar = "lesen"; return j }(), CodeCalendarNotWritable},
		"reserve in an unknown one":      {func() Job { j := reserveJob("k3", at(25, 9, 0), at(25, 10, 0)); j.Calendar = "fehlt"; return j }(), CodeCalendarNotFound},
		"get from an unknown calendar":   {Job{Connector: "shop", Operation: catalog.OpGetBooking, Calendar: "fehlt", BookingID: "x", ResultVariable: "b", ErrorCodes: Codes}, CodeCalendarNotFound},
		"cancel in an unknown calendar":  {Job{Connector: "shop", Operation: catalog.OpCancelBooking, Calendar: "fehlt", BookingID: "x", ErrorCodes: Codes}, CodeCalendarNotFound},
		"cancel in a read-only calendar": {Job{Connector: "shop", Operation: catalog.OpCancelBooking, Calendar: "lesen", BookingID: "x", ErrorCodes: Codes}, CodeCalendarNotWritable},
		"confirm a booking not there":    {Job{Connector: "shop", Operation: catalog.OpConfirmBooking, Calendar: "werkstatt", BookingID: "x", ErrorCodes: Codes}, CodeBookingNotFound},
		"confirm in an unknown calendar": {Job{Connector: "shop", Operation: catalog.OpConfirmBooking, Calendar: "fehlt", BookingID: "x", ErrorCodes: Codes}, CodeCalendarNotFound},
		"attendees without delegation":   {attendees, CodeAttendeesNotAllowed},
	} {
		if _, err := RunAt(ctx, tc.job, reg, now); bpmnCode(err) != tc.code {
			t.Errorf("%s: err = %v, want %s", name, err, tc.code)
		}
	}
}

func TestGoogleFailsAWindowTooFullToReadWhole(t *testing.T) {
	var busy []googlefake.Busy
	for i := 0; i < maxGraphPages*2+1; i++ {
		busy = append(busy, googlefake.Busy{Start: at(25, 8, 0).Add(time.Duration(i) * time.Minute), End: at(25, 8, 1).Add(time.Duration(i) * time.Minute)})
	}
	reg, fake := googleRegistry(t, map[string]googlefake.Calendar{"werkstatt": {Busy: busy}}, "")
	fake.PageSize = 2
	if _, err := RunAt(context.Background(), findJob("werkstatt"), reg, now); err == nil || !strings.Contains(err.Error(), "shorter window") {
		t.Fatalf("err = %v; a busy list cut short would offer taken slots", err)
	}
}

func TestGoogleReadsWhatItCanAndSaysWhatItCannot(t *testing.T) {
	if _, err := (googleEvent{Start: googleTime{DateTime: "kaputt"}}).event(time.UTC); err == nil {
		t.Fatal("an unreadable start was accepted")
	}
	if _, err := (googleEvent{Start: googleTime{Date: "2030-03-25"}, End: googleTime{Date: "x"}}).event(time.UTC); err == nil {
		t.Fatal("an unreadable end was accepted")
	}
	f := &googleFailure{method: "GET", path: "/calendars/x", status: 500, reason: "backendError", message: "boom"}
	if !strings.Contains(f.Error(), "HTTP 500") || classifyGoogle(f, false) != error(f) {
		t.Fatalf("an unknown failure is %v", classifyGoogle(f, false))
	}
	if status, _ := googleStatus(context.Canceled); status != 0 {
		t.Fatal("a plain error has a status")
	}
}
