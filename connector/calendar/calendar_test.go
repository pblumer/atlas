package calendar

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/pblumer/atlas/connector/calendar/catalog"
	"github.com/pblumer/atlas/job"
)

// now is the fixed instant every test runs at: a Monday morning in Zurich.
var now = time.Date(2030, 3, 25, 7, 0, 0, 0, zurich())

func zurich() *time.Location {
	loc, err := time.LoadLocation("Europe/Zurich")
	if err != nil {
		panic(err)
	}
	return loc
}

func at(day, hour, minute int) time.Time {
	return time.Date(2030, 3, day, hour, minute, 0, 0, zurich())
}

func registry(m *MockCalendars) *Registry {
	reg := NewRegistry()
	reg.Register("shop", m)
	return reg
}

var weekdays = map[string][]Window{
	"mon": {{From: "08:00", To: "12:00"}, {From: "13:30", To: "18:00"}},
	"tue": {{From: "08:00", To: "12:00"}},
}

func findJob(calendars ...string) Job {
	return Job{
		Connector: "shop", Operation: catalog.OpFindFreeSlots, Calendars: calendars,
		From: at(25, 0, 0).Format(time.RFC3339), To: at(27, 0, 0).Format(time.RFC3339),
		DurationMinutes: 60, OpeningHours: weekdays, TimeZone: "Europe/Zurich",
		ResultVariable: "free", ErrorCodes: Codes,
	}
}

func slotsOf(t *testing.T, out map[string]any) []map[string]any {
	t.Helper()
	env, ok := out["free"].(map[string]any)
	if !ok {
		t.Fatalf("no result envelope: %v", out)
	}
	raw := env["slots"].([]any)
	list := make([]map[string]any, len(raw))
	for i, s := range raw {
		list[i] = s.(map[string]any)
	}
	return list
}

func times(slots []map[string]any) string {
	parts := make([]string, len(slots))
	for i, s := range slots {
		parts[i] = s["date"].(string)[8:] + " " + s["time"].(string)
	}
	return strings.Join(parts, ",")
}

func TestTheGridFollowsTheOpeningHours(t *testing.T) {
	m := NewMockCalendars(map[string]MockCalendarSpec{"werkstatt": {}})
	out, err := RunAt(context.Background(), findJob("werkstatt"), registry(m), now)
	if err != nil {
		t.Fatal(err)
	}
	got := times(slotsOf(t, out))
	want := "25 08:00,25 09:00,25 10:00,25 11:00,25 13:30,25 14:30,25 15:30,25 16:30," +
		"26 08:00,26 09:00,26 10:00,26 11:00"
	if got != want {
		t.Fatalf("slots\n got %s\nwant %s", got, want)
	}
	first := slotsOf(t, out)[0]
	if first["start"] != "2030-03-25T08:00:00+01:00" || first["end"] != "2030-03-25T09:00:00+01:00" {
		t.Fatalf("first slot %v, want local times with the zone's offset", first)
	}
}

func TestBusyBufferLeadAndClosedDatesRemoveSlots(t *testing.T) {
	m := NewMockCalendars(map[string]MockCalendarSpec{"werkstatt": {Busy: []MockBusy{
		{Start: at(25, 10, 15), End: at(25, 10, 45)},
		{Start: at(25, 14, 0), End: at(25, 18, 0), Free: true}, // marked free: blocks nothing
	}}})
	j := findJob("werkstatt")
	j.BufferMinutes = 15
	j.MinLeadMinutes = 120 // now is 07:00: nothing before 09:00
	j.ClosedDates = []string{"2030-03-26"}
	out, err := RunAt(context.Background(), j, registry(m), now)
	if err != nil {
		t.Fatal(err)
	}
	// Widened by 15 minutes, 09:00 ends at 10:15 and 11:00 begins at 10:45: an end is
	// exclusive, so neither touches the appointment. 10:00 does.
	if got, want := times(slotsOf(t, out)), "25 09:00,25 11:00,25 13:30,25 14:30,25 15:30,25 16:30"; got != want {
		t.Fatalf("slots\n got %s\nwant %s", got, want)
	}
}

func TestAnyAndAllCalendars(t *testing.T) {
	m := NewMockCalendars(map[string]MockCalendarSpec{
		"anna":  {Busy: []MockBusy{{Start: at(26, 8, 0), End: at(26, 10, 0)}}},
		"beat":  {Busy: []MockBusy{{Start: at(26, 9, 0), End: at(26, 12, 0)}}},
		"raum1": {},
	})
	j := findJob("anna", "beat")
	j.From = at(26, 0, 0).Format(time.RFC3339)
	out, err := RunAt(context.Background(), j, registry(m), now)
	if err != nil {
		t.Fatal(err)
	}
	slots := slotsOf(t, out)
	if got := times(slots); got != "26 08:00,26 10:00,26 11:00" {
		t.Fatalf("any: %s", got)
	}
	if c := slots[0]["calendars"].([]any); len(c) != 1 || c[0] != "beat" {
		t.Fatalf("08:00 is free in %v, want beat only", c)
	}
	j.Require = catalog.RequireAll
	j.Calendars = []string{"anna", "raum1"}
	out, err = RunAt(context.Background(), j, registry(m), now)
	if err != nil {
		t.Fatal(err)
	}
	if got := times(slotsOf(t, out)); got != "26 10:00,26 11:00" {
		t.Fatalf("all: %s", got)
	}
}

func TestADaylightSavingChangeSkipsTheWallTimeThatDoesNotExist(t *testing.T) {
	// Zurich moves from 02:00 to 03:00 on Sunday 31 March 2030.
	m := NewMockCalendars(map[string]MockCalendarSpec{"nacht": {}})
	j := findJob("nacht")
	j.From = at(31, 0, 0).Format(time.RFC3339)
	j.To = at(31, 5, 0).Format(time.RFC3339)
	j.OpeningHours = map[string][]Window{"sun": {{From: "01:00", To: "05:00"}}}
	out, err := RunAt(context.Background(), j, registry(m), now)
	if err != nil {
		t.Fatal(err)
	}
	if got := times(slotsOf(t, out)); got != "31 01:00,31 03:00,31 04:00" {
		t.Fatalf("slots %s, want 02:00 skipped", got)
	}
}

func TestTheSlotListIsCutAtMaxResultsAndSaysSo(t *testing.T) {
	m := NewMockCalendars(map[string]MockCalendarSpec{"werkstatt": {}})
	j := findJob("werkstatt")
	j.MaxResults = 3
	out, err := RunAt(context.Background(), j, registry(m), now)
	if err != nil {
		t.Fatal(err)
	}
	if got := times(slotsOf(t, out)); got != "25 08:00,25 09:00,25 10:00" {
		t.Fatalf("slots %s", got)
	}
	if out["free"].(map[string]any)["truncated"] != true {
		t.Fatal("a cut list does not say it was cut")
	}
}

func TestFindRefusesWhatItCannotAnswer(t *testing.T) {
	m := NewMockCalendars(map[string]MockCalendarSpec{"werkstatt": {}})
	for name, mutate := range map[string]func(*Job){
		"no calendar":        func(j *Job) { j.Calendars = nil },
		"duplicate calendar": func(j *Job) { j.Calendars = []string{"werkstatt", "werkstatt"} },
		"window too long":    func(j *Job) { j.To = at(25, 0, 0).AddDate(0, 0, 63).Format(time.RFC3339) },
		"from after to":      func(j *Job) { j.From, j.To = j.To, j.From },
		"no offset":          func(j *Job) { j.From = "2030-03-25T08:00:00" },
		"short duration":     func(j *Job) { j.DurationMinutes = 1 },
		"no opening hours":   func(j *Job) { j.OpeningHours = nil },
		"unknown weekday":    func(j *Job) { j.OpeningHours = map[string][]Window{"monday": {{From: "08:00", To: "09:00"}}} },
		"bad clock":          func(j *Job) { j.OpeningHours = map[string][]Window{"mon": {{From: "8:00", To: "09:00"}}} },
		"overlapping hours": func(j *Job) {
			j.OpeningHours = map[string][]Window{"mon": {{From: "08:00", To: "10:00"}, {From: "09:00", To: "11:00"}}}
		},
		"bad closed date": func(j *Job) { j.ClosedDates = []string{"25.03.2030"} },
		"bad require":     func(j *Job) { j.Require = "most" },
		"bad zone":        func(j *Job) { j.TimeZone = "Mars/Olympus" },
	} {
		j := findJob("werkstatt")
		mutate(&j)
		if _, err := RunAt(context.Background(), j, registry(m), now); err == nil {
			t.Errorf("%s: no refusal", name)
		}
	}
}

func reserveJob(key string, start, end time.Time) Job {
	return Job{
		Connector: "shop", Operation: catalog.OpReserveSlot, Calendar: "werkstatt",
		Start: start.Format(time.RFC3339), End: end.Format(time.RFC3339), Subject: "Veloservice",
		TimeZone: "Europe/Zurich", CreateKey: key, ResultVariable: "booking", ErrorCodes: Codes,
	}
}

func bpmnCode(err error) string {
	var be *job.BPMNError
	if errors.As(err, &be) {
		return be.Code
	}
	return ""
}

func events(t *testing.T, m *MockCalendars) []MockEvent {
	t.Helper()
	evs, err := m.Snapshot("werkstatt")
	if err != nil {
		t.Fatal(err)
	}
	return evs
}

func TestAReservationInAFreeSlotKeepsIt(t *testing.T) {
	m := NewMockCalendars(map[string]MockCalendarSpec{"werkstatt": {}})
	out, err := RunAt(context.Background(), reserveJob("k1", at(25, 9, 0), at(25, 10, 0)), registry(m), now)
	if err != nil {
		t.Fatal(err)
	}
	b := out["booking"].(map[string]any)
	evs := events(t, m)
	if len(evs) != 1 || b["bookingId"] != evs[0].ID || b["start"] != "2030-03-25T09:00:00+01:00" {
		t.Fatalf("booking %v, calendar %+v", b, evs)
	}
	if evs[0].Subject != "Veloservice" || len(evs[0].Attendees) != 0 {
		t.Fatalf("the placeholder is %+v; want the neutral subject and no attendees", evs[0])
	}
}

func TestARepeatedDeliveryCreatesNoSecondEvent(t *testing.T) {
	m := NewMockCalendars(map[string]MockCalendarSpec{"werkstatt": {}})
	j := reserveJob("k1", at(25, 9, 0), at(25, 10, 0))
	first, err := RunAt(context.Background(), j, registry(m), now)
	if err != nil {
		t.Fatal(err)
	}
	second, err := RunAt(context.Background(), j, registry(m), now)
	if err != nil {
		t.Fatalf("the repeated delivery found its own reservation a conflict: %v", err)
	}
	if len(events(t, m)) != 1 || first["booking"].(map[string]any)["bookingId"] != second["booking"].(map[string]any)["bookingId"] {
		t.Fatalf("a repeated delivery booked twice: %+v", events(t, m))
	}
}

func TestATakenSlotIsGivenUpAndThrown(t *testing.T) {
	m := NewMockCalendars(map[string]MockCalendarSpec{"werkstatt": {Busy: []MockBusy{{Start: at(25, 9, 30), End: at(25, 9, 45)}}}})
	_, err := RunAt(context.Background(), reserveJob("k1", at(25, 9, 0), at(25, 10, 0)), registry(m), now)
	if bpmnCode(err) != CodeSlotTaken {
		t.Fatalf("err = %v, want %s", err, CodeSlotTaken)
	}
	if evs := events(t, m); len(evs) != 1 || evs[0].Key != "" {
		t.Fatalf("the reservation was not removed: %+v", evs)
	}
	// Uncaught, the same refusal is a plain failure.
	j := reserveJob("k2", at(25, 9, 0), at(25, 10, 0))
	j.ErrorCodes = nil
	if _, err := RunAt(context.Background(), j, registry(m), now); err == nil || bpmnCode(err) != "" {
		t.Fatalf("uncaught: err = %v, want a plain failure", err)
	}
}

// TestTwoCompetingReservationsNeverBothSurvive runs both orders the rule distinguishes:
// one after the other, and the second entirely between the first's create and its read.
func TestTwoCompetingReservationsNeverBothSurvive(t *testing.T) {
	ctx := context.Background()
	t.Run("one after the other", func(t *testing.T) {
		m := NewMockCalendars(map[string]MockCalendarSpec{"werkstatt": {}})
		_, errA := RunAt(ctx, reserveJob("a", at(25, 9, 0), at(25, 10, 0)), registry(m), now)
		_, errB := RunAt(ctx, reserveJob("b", at(25, 9, 30), at(25, 10, 30)), registry(m), now)
		if errA != nil || bpmnCode(errB) != CodeSlotTaken {
			t.Fatalf("errA = %v, errB = %v; want the first to keep and the second to give up", errA, errB)
		}
		if evs := events(t, m); len(evs) != 1 || evs[0].Key != "a" {
			t.Fatalf("calendar %+v", evs)
		}
	})
	t.Run("both read before either gives up", func(t *testing.T) {
		m := NewMockCalendars(map[string]MockCalendarSpec{"werkstatt": {}})
		// Both creates happen before either read, and both reads before either delete:
		// each sees the other, so both give up. Nobody gets the slot; nobody gets it twice.
		reg := NewRegistry()
		reg.Register("shop", &lockstep{Backend: m, created: newBarrier(2), read: newBarrier(2)})
		errs := make(chan error, 2)
		for _, key := range []string{"a", "b"} {
			go func(key string) {
				_, err := RunAt(ctx, reserveJob(key, at(25, 9, 0), at(25, 10, 0)), reg, now)
				errs <- err
			}(key)
		}
		for i := 0; i < 2; i++ {
			if err := <-errs; bpmnCode(err) != CodeSlotTaken {
				t.Fatalf("err = %v, want both to give up", err)
			}
		}
		if evs := events(t, m); len(evs) != 0 {
			t.Fatalf("calendar %+v; want nothing kept", evs)
		}
	})
	t.Run("the second runs between the first's create and its read", func(t *testing.T) {
		m := NewMockCalendars(map[string]MockCalendarSpec{"werkstatt": {}})
		var errB error
		reg := NewRegistry()
		reg.Register("shop", &afterCreate{Backend: m, fn: func(key string) {
			if key == "a" {
				_, errB = RunAt(ctx, reserveJob("b", at(25, 9, 0), at(25, 10, 0)), registry(m), now)
			}
		}})
		_, errA := RunAt(ctx, reserveJob("a", at(25, 9, 0), at(25, 10, 0)), reg, now)
		// b read after a's create, saw it and gave up before a read: a keeps the slot.
		if errA != nil || bpmnCode(errB) != CodeSlotTaken {
			t.Fatalf("errA = %v, errB = %v", errA, errB)
		}
		if evs := events(t, m); len(evs) != 1 || evs[0].Key != "a" {
			t.Fatalf("calendar %+v", evs)
		}
	})
}

func TestReserveRefusals(t *testing.T) {
	m := NewMockCalendars(map[string]MockCalendarSpec{"werkstatt": {}, "lesen": {ReadOnly: true}})
	ctx := context.Background()
	j := reserveJob("k", at(25, 9, 0), at(25, 10, 0))
	j.Calendar = "fehlt"
	if _, err := RunAt(ctx, j, registry(m), now); bpmnCode(err) != CodeCalendarNotFound {
		t.Errorf("unknown calendar: %v", err)
	}
	j.Calendar = "lesen"
	if _, err := RunAt(ctx, j, registry(m), now); bpmnCode(err) != CodeCalendarNotWritable {
		t.Errorf("read-only calendar: %v", err)
	}
	past := reserveJob("k", at(25, 6, 0), at(25, 7, 0))
	past.Start = at(25, 6, 0).Format(time.RFC3339)
	if _, err := RunAt(ctx, past, registry(m), now.Add(time.Minute)); err == nil {
		t.Error("a slot that has begun was reserved")
	}
	noKey := reserveJob("", at(25, 9, 0), at(25, 10, 0))
	if _, err := RunAt(ctx, noKey, registry(m), now); err == nil {
		t.Error("a reservation without a create key was made")
	}
	if len(events(t, m)) != 0 {
		t.Fatalf("a refused job changed the calendar: %+v", events(t, m))
	}
}

func TestConfirmCancelAndGet(t *testing.T) {
	m := NewMockCalendars(map[string]MockCalendarSpec{"werkstatt": {}})
	ctx := context.Background()
	out, err := RunAt(ctx, reserveJob("k1", at(25, 9, 0), at(25, 10, 0)), registry(m), now)
	if err != nil {
		t.Fatal(err)
	}
	id := out["booking"].(map[string]any)["bookingId"].(string)
	base := Job{Connector: "shop", Calendar: "werkstatt", BookingID: id, TimeZone: "Europe/Zurich", ResultVariable: "booking", ErrorCodes: Codes}

	confirm := base
	confirm.Operation = catalog.OpConfirmBooking
	confirm.Subject, confirm.Body = "Veloservice – Muster", "Velo: E-Bike"
	confirm.Attendees = []Attendee{{Email: "kundin@example.ch", Name: "Kundin"}}
	if _, err := RunAt(ctx, confirm, registry(m), now); err != nil {
		t.Fatal(err)
	}
	if ev := events(t, m)[0]; ev.Subject != "Veloservice – Muster" || len(ev.Attendees) != 1 {
		t.Fatalf("confirmed event %+v", ev)
	}
	confirm.Attendees = []Attendee{{Email: "Kundin <kundin@example.ch>"}}
	if _, err := RunAt(ctx, confirm, registry(m), now); err == nil {
		t.Error("an attendee that is not one bare address was accepted")
	}

	get := base
	get.Operation = catalog.OpGetBooking
	got, err := RunAt(ctx, get, registry(m), now)
	if err != nil || got["booking"].(map[string]any)["exists"] != true {
		t.Fatalf("get: %v %v", got, err)
	}

	cancel := base
	cancel.Operation = catalog.OpCancelBooking
	cancel.ResultVariable = ""
	for i := 0; i < 2; i++ { // the second cancel finds nothing, which is success
		if _, err := RunAt(ctx, cancel, registry(m), now); err != nil {
			t.Fatalf("cancel %d: %v", i, err)
		}
	}
	got, err = RunAt(ctx, get, registry(m), now)
	if err != nil || got["booking"].(map[string]any)["exists"] != false {
		t.Fatalf("get after cancel: %v %v", got, err)
	}
	confirm.Attendees = nil
	if _, err := RunAt(ctx, confirm, registry(m), now); bpmnCode(err) != CodeBookingNotFound {
		t.Fatalf("confirm after cancel: %v", err)
	}
}

func TestAttendeesRefusedByTheProviderAreThrown(t *testing.T) {
	m := NewMockCalendars(map[string]MockCalendarSpec{"werkstatt": {RefuseAttendees: true}})
	ctx := context.Background()
	out, err := RunAt(ctx, reserveJob("k1", at(25, 9, 0), at(25, 10, 0)), registry(m), now)
	if err != nil {
		t.Fatal(err)
	}
	_, err = RunAt(ctx, Job{
		Connector: "shop", Operation: catalog.OpConfirmBooking, Calendar: "werkstatt",
		BookingID: out["booking"].(map[string]any)["bookingId"].(string),
		Attendees: []Attendee{{Email: "kundin@example.ch"}}, ErrorCodes: Codes,
	}, registry(m), now)
	if bpmnCode(err) != CodeAttendeesNotAllowed {
		t.Fatalf("err = %v", err)
	}
}

func TestAnUnknownWorkerAndOperationAreNamed(t *testing.T) {
	m := NewMockCalendars(nil)
	if _, err := RunAt(context.Background(), Job{Connector: "other", Operation: catalog.OpGetBooking}, registry(m), now); err == nil || !strings.Contains(err.Error(), `"other"`) {
		t.Errorf("unknown worker: %v", err)
	}
	if _, err := RunAt(context.Background(), Job{Connector: "shop", Operation: "book-everything"}, registry(m), now); err == nil || !strings.Contains(err.Error(), "find-free-slots") {
		t.Errorf("unknown operation: %v", err)
	}
}

func TestCreateKeyIsStablePerJobAndInstallation(t *testing.T) {
	if CreateKey("node-a", 7) != CreateKey("node-a", 7) {
		t.Fatal("the key of one job differs between deliveries")
	}
	if CreateKey("node-a", 7) == CreateKey("node-b", 7) || CreateKey("node-a", 7) == CreateKey("node-a", 8) {
		t.Fatal("two jobs share a key")
	}
}

func TestEveryOperationClaimsItsContractUnderOneProfile(t *testing.T) {
	for _, name := range catalog.Names() {
		op, _ := catalog.Lookup(name)
		if len(op.Capabilities) != 1 || op.Capabilities[0].Profile != catalog.Profile || op.Idempotency == "" || op.Effect == "" {
			t.Errorf("%s: claims %+v, idempotency %q, effect %q", name, op.Capabilities, op.Idempotency, op.Effect)
		}
	}
}

// barrier releases its n callers together.
type barrier struct {
	mu   sync.Mutex
	n    int
	done chan struct{}
}

func newBarrier(n int) *barrier { return &barrier{n: n, done: make(chan struct{})} }

func (b *barrier) wait() {
	b.mu.Lock()
	b.n--
	if b.n == 0 {
		close(b.done)
	}
	b.mu.Unlock()
	<-b.done
}

// lockstep holds every reservation after its create until all have created, and after
// its read until all have read.
type lockstep struct {
	Backend
	created, read *barrier
}

func (l *lockstep) Create(ctx context.Context, cal string, ne NewEvent) (Event, error) {
	ev, err := l.Backend.Create(ctx, cal, ne)
	l.created.wait()
	return ev, err
}

func (l *lockstep) Events(ctx context.Context, cal string, from, to time.Time) ([]Event, error) {
	evs, err := l.Backend.Events(ctx, cal, from, to)
	l.read.wait()
	return evs, err
}

// afterCreate runs fn after each create returned and before the caller reads.
type afterCreate struct {
	Backend
	fn func(key string)
}

func (a *afterCreate) Create(ctx context.Context, cal string, ne NewEvent) (Event, error) {
	ev, err := a.Backend.Create(ctx, cal, ne)
	if err == nil {
		a.fn(ne.Key)
	}
	return ev, err
}

// failingDelete is a backend whose delete fails: a reservation that lost must then
// fail the job, so a retry deletes again rather than leaving the placeholder.
type failingDelete struct{ Backend }

func (failingDelete) Delete(context.Context, string, string) error {
	return errors.New("the provider is unavailable")
}

func TestALostReservationWhoseDeleteFailsIsRetried(t *testing.T) {
	m := NewMockCalendars(map[string]MockCalendarSpec{"werkstatt": {Busy: []MockBusy{{Start: at(25, 9, 0), End: at(25, 10, 0)}}}})
	reg := NewRegistry()
	reg.Register("shop", failingDelete{m})
	_, err := RunAt(context.Background(), reserveJob("k", at(25, 9, 0), at(25, 10, 0)), reg, now)
	if err == nil || bpmnCode(err) != "" || !strings.Contains(err.Error(), "retried") {
		t.Fatalf("err = %v, want a plain failure the job retries", err)
	}
}

func TestTheMockNamesAnUnknownCalendar(t *testing.T) {
	m := NewMockCalendars(nil)
	if _, _, err := m.Get(context.Background(), "fehlt", "x"); kindOf(err) != KindCalendarNotFound {
		t.Errorf("Get: %v", err)
	}
	if _, err := m.Snapshot("fehlt"); kindOf(err) != KindCalendarNotFound {
		t.Errorf("Snapshot: %v", err)
	}
	if _, err := m.Update(context.Background(), "fehlt", "x", Details{}); kindOf(err) != KindCalendarNotFound {
		t.Errorf("Update: %v", err)
	}
	if err := m.Delete(context.Background(), "fehlt", "x"); kindOf(err) != KindCalendarNotFound {
		t.Errorf("Delete: %v", err)
	}
	if (&Error{Message: "x"}).Error() != "calendar: x" {
		t.Error("an error does not name its Worker Type")
	}
}
