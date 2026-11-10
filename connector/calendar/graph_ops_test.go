package calendar

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/pblumer/atlas/connector/calendar/catalog"
	"github.com/pblumer/atlas/connector/calendar/graphfake"
)

func graphRegistry(t *testing.T, boxes map[string]graphfake.Mailbox) (*Registry, *graphfake.Server) {
	t.Helper()
	fake := graphfake.New(boxes)
	t.Cleanup(fake.Close)
	reg := NewRegistry()
	reg.Register("shop", graphBackend(t, fake))
	return reg, fake
}

// TestGraphWalksABookingFromReservationToCancellation runs every operation through the
// Graph backend against the fake: what the conformance suites check across backends,
// here with the requests the backend sends in view.
func TestGraphWalksABookingFromReservationToCancellation(t *testing.T) {
	reg, fake := graphRegistry(t, map[string]graphfake.Mailbox{"werkstatt": {Busy: []graphfake.Busy{
		{Start: at(25, 10, 0), End: at(25, 11, 0)},
		{Start: at(25, 13, 30), End: at(25, 18, 0), ShowAs: "free"},
	}}})
	ctx := context.Background()
	find := findJob("werkstatt")
	out, err := RunAt(ctx, find, reg, now)
	if err != nil {
		t.Fatal(err)
	}
	if got := times(slotsOf(t, out)); !strings.HasPrefix(got, "25 08:00,25 09:00,25 11:00,25 13:30") {
		t.Fatalf("slots %s", got)
	}

	res := reserveJob("k1", at(25, 9, 0), at(25, 10, 0))
	res.Calendar = "werkstatt"
	out, err = RunAt(ctx, res, reg, now)
	if err != nil {
		t.Fatal(err)
	}
	id := out["booking"].(map[string]any)["bookingId"].(string)
	base := Job{Connector: "shop", Calendar: "werkstatt", BookingID: id, TimeZone: "Europe/Zurich", ResultVariable: "b", ErrorCodes: Codes}

	confirm := base
	confirm.Operation = catalog.OpConfirmBooking
	confirm.Subject, confirm.Body = "Veloservice – Muster", "E-Bike"
	confirm.Attendees = []Attendee{{Email: "kundin@example.ch", Name: "Kundin"}}
	if _, err := RunAt(ctx, confirm, reg, now); err != nil {
		t.Fatal(err)
	}
	ev := fake.Events("werkstatt")[2]
	if ev.Subject != "Veloservice – Muster" || ev.Body != "E-Bike" || len(ev.Attendees) != 1 || ev.TransactionID != "k1" {
		t.Fatalf("confirmed event %+v", ev)
	}

	get := base
	get.Operation = catalog.OpGetBooking
	out, err = RunAt(ctx, get, reg, now)
	if err != nil || out["b"].(map[string]any)["exists"] != true || out["b"].(map[string]any)["start"] != "2030-03-25T09:00:00+01:00" {
		t.Fatalf("get: %v %v", out, err)
	}

	cancel := base
	cancel.Operation, cancel.ResultVariable = catalog.OpCancelBooking, ""
	for i := 0; i < 2; i++ {
		if _, err := RunAt(ctx, cancel, reg, now); err != nil {
			t.Fatalf("cancel %d: %v", i, err)
		}
	}
	out, err = RunAt(ctx, get, reg, now)
	if err != nil || out["b"].(map[string]any)["exists"] != false {
		t.Fatalf("get after cancel: %v %v", out, err)
	}
	if _, err := RunAt(ctx, confirm, reg, now); bpmnCode(err) != CodeBookingNotFound {
		t.Fatalf("confirm after cancel: %v", err)
	}
}

func TestGraphTellsACalendarItCannotReachFromABookingItCannotFind(t *testing.T) {
	reg, _ := graphRegistry(t, map[string]graphfake.Mailbox{"werkstatt": {}, "lesen": {ReadOnly: true}})
	ctx := context.Background()
	for name, tc := range map[string]struct {
		job  Job
		code string
	}{
		"find in an unknown mailbox":    {func() Job { j := findJob("fehlt"); return j }(), CodeCalendarNotFound},
		"reserve in a read-only one":    {func() Job { j := reserveJob("k", at(25, 9, 0), at(25, 10, 0)); j.Calendar = "lesen"; return j }(), CodeCalendarNotWritable},
		"get from an unknown mailbox":   {Job{Connector: "shop", Operation: catalog.OpGetBooking, Calendar: "fehlt", BookingID: "x", ResultVariable: "b", ErrorCodes: Codes}, CodeCalendarNotFound},
		"cancel in an unknown mailbox":  {Job{Connector: "shop", Operation: catalog.OpCancelBooking, Calendar: "fehlt", BookingID: "x", ErrorCodes: Codes}, CodeCalendarNotFound},
		"cancel in a read-only mailbox": {Job{Connector: "shop", Operation: catalog.OpCancelBooking, Calendar: "lesen", BookingID: "x", ErrorCodes: Codes}, CodeCalendarNotWritable},
		"confirm a booking not there":   {Job{Connector: "shop", Operation: catalog.OpConfirmBooking, Calendar: "werkstatt", BookingID: "x", ErrorCodes: Codes}, CodeBookingNotFound},
	} {
		if _, err := RunAt(ctx, tc.job, reg, now); bpmnCode(err) != tc.code {
			t.Errorf("%s: err = %v, want %s", name, err, tc.code)
		}
	}
}

func TestGraphReadsTimesInUTCOnly(t *testing.T) {
	if _, err := (graphTime{DateTime: "2030-03-25T09:00:00", TimeZone: "W. Europe Standard Time"}).parse(); err == nil {
		t.Fatal("a time in another zone was read as UTC")
	}
	if _, err := (graphEvent{Start: graphTime{DateTime: "kaputt", TimeZone: "UTC"}}).event(); err == nil {
		t.Fatal("an unreadable start was accepted")
	}
	if _, err := (graphEvent{Start: graphTime{DateTime: "2030-03-25T09:00:00", TimeZone: "UTC"}, End: graphTime{DateTime: "x", TimeZone: "UTC"}}).event(); err == nil {
		t.Fatal("an unreadable end was accepted")
	}
	f := &graphFailure{method: "GET", path: "/users/x", status: 500, code: "ErrorInternalServerError", message: "boom"}
	if !strings.Contains(f.Error(), "HTTP 500") || classify(f, false, false) != error(f) {
		t.Fatalf("an unknown failure is %v", classify(f, false, false))
	}
	if clip(strings.Repeat("x", 80)) == strings.Repeat("x", 80) {
		t.Fatal("a long value is quoted whole")
	}
}

func TestRunUsesTheClock(t *testing.T) {
	m := NewMockCalendars(map[string]MockCalendarSpec{"werkstatt": {}})
	j := findJob("werkstatt")
	start := time.Now().Add(48 * time.Hour)
	j.From, j.To = start.Format(time.RFC3339), start.Add(24*time.Hour).Format(time.RFC3339)
	j.OpeningHours = map[string][]Window{}
	for _, d := range catalog.Weekdays {
		j.OpeningHours[d] = []Window{{From: "00:00", To: "24:00"}}
	}
	out, err := Run(context.Background(), j, registry(m))
	if err != nil || len(slotsOf(t, out)) == 0 {
		t.Fatalf("Run: %v %v", out, err)
	}
}

func TestReserveAndBookingValuesAreChecked(t *testing.T) {
	m := NewMockCalendars(map[string]MockCalendarSpec{"werkstatt": {}})
	ctx := context.Background()
	for name, mutate := range map[string]func(*Job){
		"no calendar":      func(j *Job) { j.Calendar = " " },
		"bad start":        func(j *Job) { j.Start = "morgen" },
		"bad end":          func(j *Job) { j.End = "abend" },
		"end before start": func(j *Job) { j.Start, j.End = j.End, j.Start },
		"longer than a day": func(j *Job) {
			j.End = at(27, 9, 0).Format(time.RFC3339)
		},
		"subject too long": func(j *Job) { j.Subject = strings.Repeat("x", catalog.MaxSubjectLength+1) },
	} {
		j := reserveJob("k", at(25, 9, 0), at(25, 10, 0))
		mutate(&j)
		if _, err := RunAt(ctx, j, registry(m), now); err == nil {
			t.Errorf("%s: no refusal", name)
		}
	}
	base := Job{Connector: "shop", Operation: catalog.OpConfirmBooking, Calendar: "werkstatt", BookingID: "x"}
	for name, mutate := range map[string]func(*Job){
		"no calendar":       func(j *Job) { j.Calendar = "" },
		"no booking":        func(j *Job) { j.BookingID = "" },
		"subject too long":  func(j *Job) { j.Subject = strings.Repeat("x", catalog.MaxSubjectLength+1) },
		"body too long":     func(j *Job) { j.Body = strings.Repeat("x", catalog.MaxBodyLength+1) },
		"too many invitees": func(j *Job) { j.Attendees = make([]Attendee, catalog.MaxAttendees+1) },
	} {
		j := base
		mutate(&j)
		if _, err := RunAt(ctx, j, registry(m), now); err == nil {
			t.Errorf("confirm, %s: no refusal", name)
		}
	}
	find := findJob("werkstatt")
	find.Calendars = make([]string, catalog.MaxCalendars+1)
	if _, err := RunAt(ctx, find, registry(m), now); err == nil {
		t.Error("too many calendars were read")
	}
	find = findJob("werkstatt", " ")
	if _, err := RunAt(ctx, find, registry(m), now); err == nil {
		t.Error("an empty calendar name was read")
	}
	find = findJob("werkstatt")
	find.ClosedDates = make([]string, catalog.MaxClosedDates+1)
	if _, err := RunAt(ctx, find, registry(m), now); err == nil {
		t.Error("too many closed dates were taken")
	}
	find = findJob("werkstatt")
	find.MinLeadMinutes = -1
	if _, err := RunAt(ctx, find, registry(m), now); err == nil {
		t.Error("a negative lead time was taken")
	}
}
