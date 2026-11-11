// Package calendarbooking holds the conformance suites that check the calendar Worker
// Type's five operations against the drafted technical capability contracts
// io.atlas.calendar.*@1.0.0+free-busy-calendar (ADR-0483, ADR-0552).
//
// # Every case against every backend
//
// One operation serves every provider backend, and the contracts carry one profile, so
// a case passes only when it passes on every backend the Worker Type has. The backends
// are listed in [backends]; each brings a world the case seeds, runs the operation in,
// and inspects afterwards: the mock, Graph, Google, CalDAV and EWS. A provider backend joins with
// an in-process fake of its API, and every record that names one of these suites moves
// with it, as it should.
//
// # What a fake cannot show
//
// Every case runs calendar.RunAt — the operation itself — at a fixed instant, so a
// record derived today verifies next year. The fakes see every create that returned
// before a read began, by construction. Whether Microsoft Graph, Google Calendar, a CalDAV
// server and Exchange Server do
// is the open question the record names; the reservation suite reports that case as
// not exercised rather than passing it on the fakes' word.
//
// The package's dependency digest is the fixture revision an evidence record carries.
package calendarbooking

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/pblumer/atlas/connector/calendar"
	"github.com/pblumer/atlas/connector/calendar/caldavfake"
	"github.com/pblumer/atlas/connector/calendar/catalog"
	"github.com/pblumer/atlas/connector/calendar/ewsfake"
	"github.com/pblumer/atlas/connector/calendar/googlefake"
	"github.com/pblumer/atlas/connector/calendar/graphfake"
	"github.com/pblumer/atlas/job"
	"github.com/pblumer/atlas/workertype"
	"github.com/pblumer/atlas/workertype/evidence"
)

// The suite names evidence records use.
const (
	NameFind    = "calendar-slot-find"
	NameReserve = "calendar-booking-reserve"
	NameConfirm = "calendar-booking-confirm"
	NameCancel  = "calendar-booking-cancel"
	NameGet     = "calendar-booking-get"
)

const contractVersion = "1.0.0"

// Each suite checks exactly one contract identity and refuses to run against another.
var contractOf = map[string]string{
	NameFind:    "io.atlas.calendar.slot.find",
	NameReserve: "io.atlas.calendar.booking.reserve",
	NameConfirm: "io.atlas.calendar.booking.confirm",
	NameCancel:  "io.atlas.calendar.booking.cancel",
	NameGet:     "io.atlas.calendar.booking.get",
}

var operationOf = map[string]string{
	NameFind:    catalog.OpFindFreeSlots,
	NameReserve: catalog.OpReserveSlot,
	NameConfirm: catalog.OpConfirmBooking,
	NameCancel:  catalog.OpCancelBooking,
	NameGet:     catalog.OpGetBooking,
}

// codeOf maps the Worker Type's BPMN codes to the contracts' codes.
var codeOf = map[string]string{
	calendar.CodeSlotTaken:           "SLOT_TAKEN",
	calendar.CodeCalendarNotFound:    "CALENDAR_NOT_FOUND",
	calendar.CodeCalendarNotWritable: "CALENDAR_NOT_WRITABLE",
	calendar.CodeBookingNotFound:     "BOOKING_NOT_FOUND",
	calendar.CodeAttendeesNotAllowed: "ATTENDEES_NOT_ALLOWED",
}

// runOperation is the operation under test; the suite's own tests replace it with
// operations that misbehave, to show each case catches what it names.
var runOperation = calendar.RunAt

// now is the instant every case runs at: Monday 25 March 2030, 07:00 in Zurich.
var now = time.Date(2030, time.March, 25, 7, 0, 0, 0, zurich)

const zone = "Europe/Zurich"

var zurich = func() *time.Location {
	loc, err := time.LoadLocation(zone)
	if err != nil {
		panic("calendarbooking: " + err.Error())
	}
	return loc
}()

func at(day, hour, minute int) time.Time {
	return time.Date(2030, time.March, day, hour, minute, 0, 0, zurich)
}

func stamp(t time.Time) string { return t.Format(time.RFC3339) }

// observed is an event as a case inspects it after the operation ran.
type observed struct {
	ID, Key    string
	Start, End time.Time
	Subject    string
	Body       string
	Attendees  []calendar.Attendee
}

// world is one backend with seeded calendars, and the means to look into them.
type world interface {
	backend() calendar.Backend
	events(cal string) []observed
	close()
}

// backendUnderTest builds a world for every case. build answers nil when the
// calendars asked for describe a refusal the provider never makes — Graph never
// refuses attendees — and the case is then vacuously met on that backend: a provider
// that never refuses cannot refuse wrongly.
type backendUnderTest struct {
	name  string
	build func(specs map[string]calendar.MockCalendarSpec) world
}

// backends are every backend the Worker Type has. A provider backend is added here
// with its fake in the change that adds it.
var backends = []backendUnderTest{
	{name: "mock", build: func(specs map[string]calendar.MockCalendarSpec) world {
		return mockWorld{calendar.NewMockCalendars(specs)}
	}},
	{name: calendar.ProviderGraph, build: buildGraphWorld},
	{name: calendar.ProviderGoogle, build: buildGoogleWorld},
	{name: calendar.ProviderCalDAV, build: buildCalDAVWorld},
	{name: calendar.ProviderEWS, build: buildEWSWorld},
}

// ewsWorld is the EWS backend against the in-process fake of Exchange Web Services,
// behind NTLM.
type ewsWorld struct {
	fake *ewsfake.Server
	b    calendar.Backend
}

func buildEWSWorld(specs map[string]calendar.MockCalendarSpec) world {
	boxes := map[string]ewsfake.Mailbox{}
	for name, spec := range specs {
		mb := ewsfake.Mailbox{ReadOnly: spec.ReadOnly, SendAsDenied: spec.RefuseAttendees}
		for _, b := range spec.Busy {
			fb := "Busy"
			if b.Free {
				fb = "Free"
			}
			mb.Busy = append(mb.Busy, ewsfake.Busy{Start: b.Start, End: b.End, FreeBusy: fb, Weekly: b.Weekly})
		}
		boxes[name] = mb
	}
	fake := ewsfake.New(boxes)
	b, err := calendar.NewBackend(fake.URL(), ewsfake.Bundle(""), calendar.Options{HTTPClient: fake.HTTPClient()})
	if err != nil {
		panic("calendarbooking: the EWS backend does not build against the fake: " + err.Error())
	}
	return ewsWorld{fake: fake, b: b}
}

func (w ewsWorld) backend() calendar.Backend { return w.b }

func (w ewsWorld) close() { w.fake.Close() }

func (w ewsWorld) events(cal string) []observed {
	items := w.fake.Items(cal)
	out := make([]observed, len(items))
	for i, it := range items {
		att := make([]calendar.Attendee, len(it.Attendees))
		for j, a := range it.Attendees {
			att[j] = calendar.Attendee{Email: a.Email, Name: a.Name}
		}
		out[i] = observed{ID: it.ID, Key: it.Key, Start: it.Start, End: it.End, Subject: it.Subject, Body: it.Body, Attendees: att}
	}
	return out
}

// caldavWorld is the CalDAV backend against the in-process CalDAV server.
type caldavWorld struct {
	fake *caldavfake.Server
	b    calendar.Backend
}

func buildCalDAVWorld(specs map[string]calendar.MockCalendarSpec) world {
	cals := map[string]caldavfake.Calendar{}
	for name, spec := range specs {
		if spec.RefuseAttendees {
			return nil // a CalDAV server records attendees; it does not refuse them
		}
		c := caldavfake.Calendar{ReadOnly: spec.ReadOnly}
		for _, b := range spec.Busy {
			c.Busy = append(c.Busy, caldavfake.Busy{Start: b.Start, End: b.End, Transparent: b.Free, Weekly: b.Weekly})
		}
		cals[name] = c
	}
	fake := caldavfake.New(cals)
	b, err := calendar.NewBackend(fake.HomeURL(), caldavfake.Bundle(zone), calendar.Options{HTTPClient: fake.HTTPClient()})
	if err != nil {
		panic("calendarbooking: the CalDAV backend does not build against the fake: " + err.Error())
	}
	return caldavWorld{fake: fake, b: b}
}

func (w caldavWorld) backend() calendar.Backend { return w.b }

func (w caldavWorld) close() { w.fake.Close() }

func (w caldavWorld) events(cal string) []observed {
	var out []observed
	for _, o := range w.fake.Objects(cal) {
		att := make([]calendar.Attendee, len(o.Attendees))
		for j, a := range o.Attendees {
			att[j] = calendar.Attendee{Email: a}
		}
		key, _ := strings.CutPrefix(o.UID, "atlas-")
		if !strings.HasPrefix(o.UID, "atlas-") {
			key = ""
		}
		out = append(out, observed{ID: o.Name, Key: key, Start: o.Start, End: o.End, Subject: o.Summary, Body: o.Description, Attendees: att})
	}
	return out
}

// googleWorld is the Google backend against the in-process fake of the Calendar API.
type googleWorld struct {
	fake *googlefake.Server
	b    calendar.Backend
}

func buildGoogleWorld(specs map[string]calendar.MockCalendarSpec) world {
	cals := map[string]googlefake.Calendar{}
	for name, spec := range specs {
		c := googlefake.Calendar{ReadOnly: spec.ReadOnly, RefuseAttendees: spec.RefuseAttendees}
		for _, b := range spec.Busy {
			c.Busy = append(c.Busy, googlefake.Busy{Start: b.Start, End: b.End, Transparent: b.Free, Weekly: b.Weekly})
		}
		cals[name] = c
	}
	fake := googlefake.New(cals)
	fake.PageSize = 2 // every read pages
	b, err := calendar.NewBackend(fake.APIURL(), googlefake.Bundle(""), calendar.Options{HTTPClient: fake.HTTPClient(), TokenURL: fake.TokenURL()})
	if err != nil {
		panic("calendarbooking: the Google backend does not build against the fake: " + err.Error())
	}
	return googleWorld{fake: fake, b: b}
}

func (w googleWorld) backend() calendar.Backend { return w.b }

func (w googleWorld) close() { w.fake.Close() }

// events are the calendar's events that are not cancelled: a reservation the conflict
// rule gave up is a cancelled event to Google, and gone to the case.
func (w googleWorld) events(cal string) []observed {
	var out []observed
	for _, e := range w.fake.Events(cal) {
		if e.Status == "cancelled" {
			continue
		}
		att := make([]calendar.Attendee, len(e.Attendees))
		for j, a := range e.Attendees {
			att[j] = calendar.Attendee{Email: a.Email, Name: a.Name}
		}
		key := ""
		if rest, ok := strings.CutPrefix(e.ID, "atlas"); ok {
			if raw, err := hex.DecodeString(rest); err == nil {
				key = string(raw)
			}
		}
		out = append(out, observed{ID: e.ID, Key: key, Start: e.Start, End: e.End, Subject: e.Summary, Body: e.Description, Attendees: att})
	}
	return out
}

// graphWorld is the Graph backend against the in-process fake of Microsoft Graph.
type graphWorld struct {
	fake *graphfake.Server
	b    calendar.Backend
}

func buildGraphWorld(specs map[string]calendar.MockCalendarSpec) world {
	boxes := map[string]graphfake.Mailbox{}
	for name, spec := range specs {
		if spec.RefuseAttendees {
			return nil
		}
		mb := graphfake.Mailbox{ReadOnly: spec.ReadOnly}
		for _, b := range spec.Busy {
			show := "busy"
			if b.Free {
				show = "free"
			}
			mb.Busy = append(mb.Busy, graphfake.Busy{Start: b.Start, End: b.End, ShowAs: show, Weekly: b.Weekly})
		}
		boxes[name] = mb
	}
	fake := graphfake.New(boxes)
	fake.PageSize = 2 // every read pages
	bundle := `{"provider":"microsoft-graph","tenantId":"suite","clientId":"suite","clientSecret":"suite"}`
	b, err := calendar.NewBackend(fake.GraphURL(), bundle, calendar.Options{HTTPClient: fake.HTTPClient(), TokenURL: fake.TokenURL()})
	if err != nil {
		panic("calendarbooking: the Graph backend does not build against the fake: " + err.Error())
	}
	return graphWorld{fake: fake, b: b}
}

func (w graphWorld) backend() calendar.Backend { return w.b }

func (w graphWorld) close() { w.fake.Close() }

func (w graphWorld) events(cal string) []observed {
	evs := w.fake.Events(cal)
	out := make([]observed, len(evs))
	for i, e := range evs {
		att := make([]calendar.Attendee, len(e.Attendees))
		for j, a := range e.Attendees {
			att[j] = calendar.Attendee{Email: a.Email, Name: a.Name}
		}
		out[i] = observed{ID: e.ID, Key: e.TransactionID, Start: e.Start, End: e.End, Subject: e.Subject, Body: e.Body, Attendees: att}
	}
	return out
}

type mockWorld struct{ m *calendar.MockCalendars }

func (w mockWorld) backend() calendar.Backend { return w.m }

func (w mockWorld) close() {}

func (w mockWorld) events(cal string) []observed {
	evs, err := w.m.Snapshot(cal)
	if err != nil {
		return nil
	}
	out := make([]observed, len(evs))
	for i, e := range evs {
		out[i] = observed{ID: e.ID, Key: e.Key, Start: e.Start, End: e.End, Subject: e.Subject, Body: e.Body, Attendees: e.Attendees}
	}
	return out
}

func init() {
	for name := range contractOf {
		evidence.Register(name, Suite(name))
	}
}

// Suite is the registered suite of that name.
func Suite(name string) evidence.Suite {
	names := make([]string, len(backends))
	for i, b := range backends {
		names[i] = b.name
	}
	return evidence.Suite{
		Package: "github.com/pblumer/atlas/workertype/evidence/suites/calendarbooking",
		Environment: "The calendar Worker Type's " + operationOf[name] + " run through calendar.RunAt at 2030-03-25T07:00 " +
			"Europe/Zurich against each backend: " + strings.Join(names, ", ") + ". A case passes only when it passes on " +
			"every backend. microsoft-graph runs against connector/calendar/graphfake, an in-process fake written from " +
			"Graph's documentation, not observed from a tenant; it pages every read and does not filter a repeated " +
			"transactionId. A refusal a provider never makes (Graph never refuses attendees) is met vacuously on it. " +
			"google-calendar runs against connector/calendar/googlefake, written from Google's documentation, not observed " +
			"from a domain; it pages every read, expands a series only for singleEvents, refuses an event id it has seen " +
			"and keeps a deleted event as cancelled. caldav runs against connector/calendar/caldavfake, written from RFC 4791 " +
			"and RFC 5545, not observed from a server; it expands a series only when asked, refuses a calendar-query that " +
			"reads more than times and availability, and keeps ETags. A CalDAV server records attendees and does not refuse " +
			"them, so that case is met vacuously on it. exchange-ews runs against connector/calendar/ewsfake, written from " +
			"Microsoft's EWS reference, not observed from an Exchange server; it authenticates every request with NTLMv2 " +
			"against the account's password, expands a series only in a calendar view, refuses an item shape wider than " +
			"times and availability and a calendar item out of the schema's element order, and answers ErrorSendAsDenied " +
			"to attendees from a mailbox the account may not send for. " +
			"The backends see every create that returned before a read began; whether a provider does is not shown here.",
		Run: func(ctx context.Context, contract workertype.TechnicalContract) ([]evidence.Result, error) {
			return run(ctx, name, contract)
		},
	}
}

type suiteCase struct {
	name string
	// check runs the case on one world and answers "" when it passed, or why not.
	check func(s *session, b backendUnderTest) (string, error)
	// notExercised, when set, is why no backend can show the case.
	notExercised string
}

func run(ctx context.Context, name string, contract workertype.TechnicalContract) ([]evidence.Result, error) {
	m := contract.Metadata
	if m.ID != contractOf[name] || m.Version != contractVersion || m.Profile != catalog.Profile {
		return nil, fmt.Errorf("suite %s checks %s@%s+%s, not %s@%s+%s", name, contractOf[name], contractVersion, catalog.Profile, m.ID, m.Version, m.Profile)
	}
	compiled, err := workertype.CompileTechnicalContract(contract)
	if err != nil {
		return nil, err
	}
	s := &session{ctx: ctx, contract: compiled, operation: operationOf[name]}
	var out []evidence.Result
	for _, c := range casesOf[name] {
		if c.notExercised != "" {
			out = append(out, evidence.Result{Case: c.name, Outcome: evidence.OutcomeNotExercised, Detail: c.notExercised})
			continue
		}
		var failures []string
		for _, b := range backends {
			why, err := c.check(s, s.tracked(b))
			s.closeWorlds()
			if err != nil {
				return nil, fmt.Errorf("case %q on %s: %w", c.name, b.name, err)
			}
			if why != "" {
				failures = append(failures, b.name+": "+why)
			}
		}
		if len(failures) > 0 {
			out = append(out, evidence.Result{Case: c.name, Outcome: evidence.OutcomeFailed, Detail: strings.Join(failures, "; ")})
			continue
		}
		out = append(out, evidence.Result{Case: c.name, Outcome: evidence.OutcomePassed})
	}
	return out, nil
}

// session carries what every case shares.
type session struct {
	ctx       context.Context
	contract  *workertype.CompiledTechnicalContract
	operation string
	jobs      int
	worlds    []world
}

// tracked is b with every world it builds recorded, so the case's fakes are closed
// when it ends. A world a backend cannot build stands for a refusal it never makes.
func (s *session) tracked(b backendUnderTest) backendUnderTest {
	return backendUnderTest{name: b.name, build: func(specs map[string]calendar.MockCalendarSpec) world {
		w := b.build(specs)
		if w == nil {
			return nil
		}
		s.worlds = append(s.worlds, w)
		return w
	}}
}

func (s *session) closeWorlds() {
	for _, w := range s.worlds {
		w.close()
	}
	s.worlds = nil
}

const (
	workerName = "suite"
	resultVar  = "result"
)

// operate runs the operation for one contract input in w, as the job a model's task
// resolves to, and answers the contract's output. The input is checked against the
// contract first: an input the contract refuses is a defect of the suite, not an
// outcome. key is the job's create key; the cases that repeat a delivery pass the same.
func (s *session) operate(w world, in map[string]any, key string) (any, error) {
	return s.operateOn(w.backend(), s.operation, in, key)
}

func (s *session) operateOn(b calendar.Backend, operation string, in map[string]any, key string) (any, error) {
	raw, err := json.Marshal(in)
	if err != nil {
		return nil, err
	}
	if operation == s.operation {
		if err := s.contract.ValidateInputJSON(raw); err != nil {
			return nil, fmt.Errorf("suite input refused by the contract: %w", err)
		}
	}
	// The contract's input names are the job's own: the adapter a model writes is the
	// identity, which is the point of writing the operations against the contracts.
	var j calendar.Job
	if err := json.Unmarshal(raw, &j); err != nil {
		return nil, fmt.Errorf("suite input does not map onto a job: %w", err)
	}
	j.Connector, j.Operation, j.ErrorCodes = workerName, operation, calendar.Codes
	op, _ := catalog.Lookup(operation)
	if op.Result != catalog.ResultNone {
		j.ResultVariable = resultVar
	}
	if op.Create {
		if key == "" {
			s.jobs++
			key = "suite-job-" + strconv.Itoa(s.jobs)
		}
		j.CreateKey = key
	}
	reg := calendar.NewRegistry()
	reg.Register(workerName, b)
	// Only the operation under test is the one a suite's own tests may replace; a
	// reservation made to set a case up is always the real one.
	run := calendar.RunAt
	if operation == s.operation {
		run = runOperation
	}
	res, err := run(s.ctx, j, reg, now)
	if err != nil {
		return nil, err
	}
	if op.Result == catalog.ResultNone {
		return nil, nil
	}
	return res[resultVar], nil
}

// validOutput reports whether out is what the contract allows a success to answer.
func (s *session) validOutput(out any) bool {
	raw, err := json.Marshal(out)
	return err == nil && s.contract.ValidateOutputJSON(raw) == nil
}

// contractCode is the contract code err carries, or "" when the operation ended with a
// plain failure a process cannot catch.
func contractCode(err error) string {
	var be *job.BPMNError
	if errors.As(err, &be) {
		return codeOf[be.Code]
	}
	return ""
}

func isSuiteDefect(err error) bool {
	return err != nil && strings.HasPrefix(err.Error(), "suite input")
}

// expectCode is the common shape of a refusal case: the operation fails with code,
// and the calendars hold what they held before.
func expectCode(err error, code string, w world, cal string, before int) (string, error) {
	switch {
	case isSuiteDefect(err):
		return "", err
	case err == nil:
		return "the operation reported success", nil
	case contractCode(err) != code:
		return "the operation ended with " + describe(err) + ", not " + code, nil
	case w != nil && len(w.events(cal)) != before:
		return "the calendar changed although the operation was refused", nil
	}
	return "", nil
}

func describe(err error) string {
	if c := contractCode(err); c != "" {
		return c
	}
	return "a plain failure a process cannot tell apart"
}

// --- the cases ------------------------------------------------------------------

var openingHours = map[string]any{
	"mon": []any{map[string]any{"from": "08:00", "to": "12:00"}, map[string]any{"from": "13:30", "to": "18:00"}},
	"tue": []any{map[string]any{"from": "08:00", "to": "12:00"}},
	"sun": []any{map[string]any{"from": "01:00", "to": "05:00"}},
}

func findInput(cals ...string) map[string]any {
	list := make([]any, len(cals))
	for i, c := range cals {
		list[i] = c
	}
	return map[string]any{
		"calendars": list, "from": stamp(at(25, 0, 0)), "to": stamp(at(27, 0, 0)),
		"durationMinutes": 60, "timeZone": zone, "openingHours": openingHours,
	}
}

// slotTimes renders a slot list as "day hh:mm" entries, or why it cannot.
func slotTimes(s *session, out any) (string, string) {
	if !s.validOutput(out) {
		return "", "the answer is not what the contract's output schema allows"
	}
	raw, _ := json.Marshal(out)
	var got struct {
		Slots []struct {
			Date, Time string
			Calendars  []string
		}
	}
	_ = json.Unmarshal(raw, &got)
	parts := make([]string, len(got.Slots))
	for i, sl := range got.Slots {
		parts[i] = sl.Date[8:] + " " + sl.Time + " " + strings.Join(sl.Calendars, "+")
	}
	return strings.Join(parts, ","), ""
}

const (
	caseFindGrid      = "offers every slot of the grid that no busy appointment touches, and no other"
	caseFindFree      = "an appointment marked free blocks nothing"
	caseFindAll       = "under require all, a slot is offered only when every calendar is free in it"
	caseFindDST       = "a wall time a daylight-saving change skips is not offered"
	caseFindUnknown   = "an unknown calendar is CALENDAR_NOT_FOUND"
	caseFindRecurring = "every occurrence of a recurring appointment blocks its time"

	caseReserveFree       = "reserves a free slot and answers its booking"
	caseReserveRepeat     = "a repeated delivery of one reservation creates no second appointment"
	caseReserveTaken      = "a slot another appointment takes is SLOT_TAKEN and nothing is kept"
	caseReserveSequential = "of two overlapping reservations one after the other, the first is kept and the second is SLOT_TAKEN"
	caseReserveLockstep   = "of two reservations whose reads each see the other, neither is kept"
	caseReserveUnknown    = "an unknown calendar is CALENDAR_NOT_FOUND and nothing is created"
	caseReserveReadOnly   = "a calendar the caller may not write to is CALENDAR_NOT_WRITABLE and nothing is created"
	caseReserveVisible    = "a read that starts after another client's create returned sees that appointment"

	caseConfirmSets      = "sets the subject, body and attendees on the reserved appointment"
	caseConfirmRepeat    = "a repeated confirmation leaves one appointment with the same details"
	caseConfirmGone      = "a booking that no longer exists is BOOKING_NOT_FOUND"
	caseConfirmAttendees = "attendees the provider refuses are ATTENDEES_NOT_ALLOWED and nothing is changed"

	caseCancelRemoves = "removes the booking"
	caseCancelGone    = "a booking already gone is cancelled without failure"
	caseCancelUnknown = "an unknown calendar is CALENDAR_NOT_FOUND"

	caseGetExists  = "answers an existing booking with its start and end"
	caseGetGone    = "answers a booking that no longer exists as not existing"
	caseGetUnknown = "an unknown calendar is CALENDAR_NOT_FOUND"
)

var casesOf = map[string][]suiteCase{
	NameFind: {
		{name: caseFindGrid, check: func(s *session, b backendUnderTest) (string, error) {
			w := b.build(map[string]calendar.MockCalendarSpec{"werkstatt": {Busy: []calendar.MockBusy{
				{Start: at(25, 10, 15), End: at(25, 10, 45)},
				{Start: at(26, 8, 0), End: at(26, 10, 0)},
			}}})
			out, err := s.operate(w, findInput("werkstatt"), "")
			if isSuiteDefect(err) || err != nil {
				return "the operation failed: " + fmt.Sprint(err), suiteErr(err)
			}
			got, why := slotTimes(s, out)
			want := "25 08:00 werkstatt,25 09:00 werkstatt,25 11:00 werkstatt,25 13:30 werkstatt,25 14:30 werkstatt," +
				"25 15:30 werkstatt,25 16:30 werkstatt,26 10:00 werkstatt,26 11:00 werkstatt"
			if why == "" && got != want {
				why = "offered " + got + "; want " + want
			}
			return why, nil
		}},
		{name: caseFindFree, check: func(s *session, b backendUnderTest) (string, error) {
			w := b.build(map[string]calendar.MockCalendarSpec{"werkstatt": {Busy: []calendar.MockBusy{
				{Start: at(26, 8, 0), End: at(26, 12, 0), Free: true},
			}}})
			in := findInput("werkstatt")
			in["from"] = stamp(at(26, 0, 0))
			out, err := s.operate(w, in, "")
			if err != nil {
				return "the operation failed: " + err.Error(), suiteErr(err)
			}
			got, why := slotTimes(s, out)
			if want := "26 08:00 werkstatt,26 09:00 werkstatt,26 10:00 werkstatt,26 11:00 werkstatt"; why == "" && got != want {
				why = "offered " + got + "; want " + want
			}
			return why, nil
		}},
		{name: caseFindAll, check: func(s *session, b backendUnderTest) (string, error) {
			w := b.build(map[string]calendar.MockCalendarSpec{
				"anna":  {Busy: []calendar.MockBusy{{Start: at(26, 8, 0), End: at(26, 10, 0)}}},
				"raum1": {Busy: []calendar.MockBusy{{Start: at(26, 11, 0), End: at(26, 12, 0)}}},
			})
			in := findInput("anna", "raum1")
			in["from"], in["require"] = stamp(at(26, 0, 0)), "all"
			out, err := s.operate(w, in, "")
			if err != nil {
				return "the operation failed: " + err.Error(), suiteErr(err)
			}
			got, why := slotTimes(s, out)
			if want := "26 10:00 anna+raum1"; why == "" && got != want {
				why = "offered " + got + "; want " + want
			}
			return why, nil
		}},
		{name: caseFindDST, check: func(s *session, b backendUnderTest) (string, error) {
			w := b.build(map[string]calendar.MockCalendarSpec{"werkstatt": {}})
			in := findInput("werkstatt")
			in["from"], in["to"] = stamp(at(31, 0, 0)), stamp(at(31, 6, 0))
			out, err := s.operate(w, in, "")
			if err != nil {
				return "the operation failed: " + err.Error(), suiteErr(err)
			}
			got, why := slotTimes(s, out)
			if want := "31 01:00 werkstatt,31 03:00 werkstatt,31 04:00 werkstatt"; why == "" && got != want {
				why = "offered " + got + "; want " + want
			}
			return why, nil
		}},
		{name: caseFindUnknown, check: func(s *session, b backendUnderTest) (string, error) {
			w := b.build(map[string]calendar.MockCalendarSpec{"werkstatt": {}})
			_, err := s.operate(w, findInput("werkstatt", "fehlt"), "")
			return expectCode(err, "CALENDAR_NOT_FOUND", nil, "", 0)
		}},
		{name: caseFindRecurring, check: func(s *session, b backendUnderTest) (string, error) {
			// A weekly appointment Monday 09:00-10:00, starting the week before: its
			// occurrence on the 25th must block 09:00.
			w := b.build(map[string]calendar.MockCalendarSpec{"werkstatt": {Busy: []calendar.MockBusy{
				{Start: at(18, 9, 0), End: at(18, 10, 0), Weekly: 4},
			}}})
			in := findInput("werkstatt")
			in["to"] = stamp(at(26, 0, 0))
			out, err := s.operate(w, in, "")
			if err != nil {
				return "the operation failed: " + err.Error(), suiteErr(err)
			}
			got, why := slotTimes(s, out)
			want := "25 08:00 werkstatt,25 10:00 werkstatt,25 11:00 werkstatt,25 13:30 werkstatt,25 14:30 werkstatt," +
				"25 15:30 werkstatt,25 16:30 werkstatt"
			if why == "" && got != want {
				why = "offered " + got + "; want " + want
			}
			return why, nil
		}},
	},
	NameReserve: {
		{name: caseReserveFree, check: func(s *session, b backendUnderTest) (string, error) {
			w := b.build(map[string]calendar.MockCalendarSpec{"werkstatt": {}})
			out, err := s.operate(w, reserveInput(9, 0, 10, 0), "")
			if err != nil {
				return "the operation failed for a free slot: " + err.Error(), suiteErr(err)
			}
			evs := w.events("werkstatt")
			booking, _ := out.(map[string]any)
			switch {
			case !s.validOutput(out):
				return "the answer is not what the contract's output schema allows", nil
			case len(evs) != 1:
				return fmt.Sprintf("the calendar holds %d appointments, want the one reserved", len(evs)), nil
			case booking["bookingId"] != evs[0].ID:
				return "the answered bookingId is not the id of the appointment created", nil
			case !evs[0].Start.Equal(at(25, 9, 0)) || !evs[0].End.Equal(at(25, 10, 0)):
				return "the appointment does not take the slot asked for", nil
			case len(evs[0].Attendees) != 0:
				return "the reservation invited somebody before the slot was certain", nil
			}
			return "", nil
		}},
		{name: caseReserveRepeat, check: func(s *session, b backendUnderTest) (string, error) {
			w := b.build(map[string]calendar.MockCalendarSpec{"werkstatt": {}})
			first, err := s.operate(w, reserveInput(9, 0, 10, 0), "delivered-twice")
			if err != nil {
				return "the first delivery failed for a free slot: " + describe(err), suiteErr(err)
			}
			second, err := s.operate(w, reserveInput(9, 0, 10, 0), "delivered-twice")
			switch {
			case err != nil:
				return "the repeated delivery failed (" + describe(err) + "): it took its own reservation for a conflict", suiteErr(err)
			case len(w.events("werkstatt")) != 1:
				return "the repeated delivery created a second appointment", nil
			case !reflect.DeepEqual(first, second):
				return "the repeated delivery answered another booking", nil
			}
			return "", nil
		}},
		{name: caseReserveTaken, check: func(s *session, b backendUnderTest) (string, error) {
			w := b.build(map[string]calendar.MockCalendarSpec{"werkstatt": {Busy: []calendar.MockBusy{{Start: at(25, 9, 30), End: at(25, 9, 45)}}}})
			_, err := s.operate(w, reserveInput(9, 0, 10, 0), "")
			return expectCode(err, "SLOT_TAKEN", w, "werkstatt", 1)
		}},
		{name: caseReserveSequential, check: func(s *session, b backendUnderTest) (string, error) {
			w := b.build(map[string]calendar.MockCalendarSpec{"werkstatt": {}})
			if _, err := s.operate(w, reserveInput(9, 0, 10, 0), "first"); err != nil {
				return "the first reservation failed: " + err.Error(), suiteErr(err)
			}
			_, err := s.operate(w, reserveInput(9, 30, 10, 30), "second")
			if why, e := expectCode(err, "SLOT_TAKEN", nil, "", 0); why != "" || e != nil {
				return "the second: " + why, e
			}
			if evs := w.events("werkstatt"); len(evs) != 1 || evs[0].Key != "first" {
				return "the calendar does not hold exactly the first reservation", nil
			}
			return "", nil
		}},
		{name: caseReserveLockstep, check: func(s *session, b backendUnderTest) (string, error) {
			w := b.build(map[string]calendar.MockCalendarSpec{"werkstatt": {}})
			ls := &lockstep{Backend: w.backend(), created: newBarrier(2), read: newBarrier(2)}
			errs := make(chan error, 2)
			for _, key := range []string{"a", "b"} {
				go func(key string) {
					_, err := s.operateOn(ls, catalog.OpReserveSlot, reserveInput(9, 0, 10, 0), key)
					errs <- err
				}(key)
			}
			var why []string
			for i := 0; i < 2; i++ {
				err := <-errs
				if isSuiteDefect(err) {
					return "", err
				}
				if contractCode(err) != "SLOT_TAKEN" {
					why = append(why, "a reservation ended with "+describeOrSuccess(err)+", not SLOT_TAKEN")
				}
			}
			if n := len(w.events("werkstatt")); n != 0 {
				why = append(why, fmt.Sprintf("the calendar keeps %d of the two", n))
			}
			return strings.Join(why, "; "), nil
		}},
		{name: caseReserveUnknown, check: func(s *session, b backendUnderTest) (string, error) {
			w := b.build(map[string]calendar.MockCalendarSpec{"werkstatt": {}})
			in := reserveInput(9, 0, 10, 0)
			in["calendar"] = "fehlt"
			_, err := s.operate(w, in, "")
			return expectCode(err, "CALENDAR_NOT_FOUND", w, "werkstatt", 0)
		}},
		{name: caseReserveReadOnly, check: func(s *session, b backendUnderTest) (string, error) {
			w := b.build(map[string]calendar.MockCalendarSpec{"werkstatt": {ReadOnly: true}})
			_, err := s.operate(w, reserveInput(9, 0, 10, 0), "")
			return expectCode(err, "CALENDAR_NOT_WRITABLE", w, "werkstatt", 0)
		}},
		{name: caseReserveVisible, notExercised: "every fake sees a create that returned before a read began, by " +
			"construction; only a run against a live provider shows whether its reads do, which is the open question " +
			"the conflict rule rests on"},
	},
	NameConfirm: {
		{name: caseConfirmSets, check: func(s *session, b backendUnderTest) (string, error) {
			w, id, err := reserved(s, b, calendar.MockCalendarSpec{})
			if err != nil {
				return "", err
			}
			out, err := s.operate(w, confirmInput(id), "")
			if err != nil {
				return "the operation failed: " + err.Error(), suiteErr(err)
			}
			evs := w.events("werkstatt")
			switch {
			case !s.validOutput(out):
				return "the answer is not what the contract's output schema allows", nil
			case len(evs) != 1:
				return "the calendar does not hold exactly the one booking", nil
			case evs[0].Subject != "Veloservice – Muster" || evs[0].Body != "E-Bike, Bremsen quietschen":
				return "the subject or body is not what was confirmed", nil
			case len(evs[0].Attendees) != 1 || evs[0].Attendees[0].Email != "kundin@example.ch":
				return "the attendee is not on the booking", nil
			}
			return "", nil
		}},
		{name: caseConfirmRepeat, check: func(s *session, b backendUnderTest) (string, error) {
			w, id, err := reserved(s, b, calendar.MockCalendarSpec{})
			if err != nil {
				return "", err
			}
			for i := 0; i < 2; i++ {
				if _, err := s.operate(w, confirmInput(id), ""); err != nil {
					return fmt.Sprintf("confirmation %d failed: %v", i+1, err), suiteErr(err)
				}
			}
			if evs := w.events("werkstatt"); len(evs) != 1 || len(evs[0].Attendees) != 1 || evs[0].Subject != "Veloservice – Muster" {
				return "the repeated confirmation changed what one confirmation left", nil
			}
			return "", nil
		}},
		{name: caseConfirmGone, check: func(s *session, b backendUnderTest) (string, error) {
			w := b.build(map[string]calendar.MockCalendarSpec{"werkstatt": {}})
			_, err := s.operate(w, confirmInput("no-such-booking"), "")
			return expectCode(err, "BOOKING_NOT_FOUND", w, "werkstatt", 0)
		}},
		{name: caseConfirmAttendees, check: func(s *session, b backendUnderTest) (string, error) {
			w, id, err := reserved(s, b, calendar.MockCalendarSpec{RefuseAttendees: true})
			if err != nil || w == nil {
				return "", err // no world: the provider never refuses attendees
			}
			_, err = s.operate(w, confirmInput(id), "")
			if why, e := expectCode(err, "ATTENDEES_NOT_ALLOWED", w, "werkstatt", 1); why != "" || e != nil {
				return why, e
			}
			if ev := w.events("werkstatt")[0]; ev.Subject != "Veloservice" || ev.Body != "" {
				return "the refused confirmation changed the booking", nil
			}
			return "", nil
		}},
	},
	NameCancel: {
		{name: caseCancelRemoves, check: func(s *session, b backendUnderTest) (string, error) {
			w, id, err := reserved(s, b, calendar.MockCalendarSpec{})
			if err != nil {
				return "", err
			}
			out, err := s.operate(w, map[string]any{"calendar": "werkstatt", "bookingId": id}, "")
			switch {
			case err != nil:
				return "the operation failed: " + err.Error(), suiteErr(err)
			case !s.validOutput(out):
				return "the answer is not what the contract's output schema allows", nil
			case len(w.events("werkstatt")) != 0:
				return "the booking is still in the calendar", nil
			}
			return "", nil
		}},
		{name: caseCancelGone, check: func(s *session, b backendUnderTest) (string, error) {
			w := b.build(map[string]calendar.MockCalendarSpec{"werkstatt": {}})
			if _, err := s.operate(w, map[string]any{"calendar": "werkstatt", "bookingId": "no-such-booking"}, ""); err != nil {
				return "cancelling a booking already gone failed: " + describe(err), suiteErr(err)
			}
			return "", nil
		}},
		{name: caseCancelUnknown, check: func(s *session, b backendUnderTest) (string, error) {
			w := b.build(map[string]calendar.MockCalendarSpec{"werkstatt": {}})
			_, err := s.operate(w, map[string]any{"calendar": "fehlt", "bookingId": "x"}, "")
			return expectCode(err, "CALENDAR_NOT_FOUND", nil, "", 0)
		}},
	},
	NameGet: {
		{name: caseGetExists, check: func(s *session, b backendUnderTest) (string, error) {
			w, id, err := reserved(s, b, calendar.MockCalendarSpec{})
			if err != nil {
				return "", err
			}
			out, err := s.operate(w, map[string]any{"calendar": "werkstatt", "bookingId": id, "timeZone": zone}, "")
			if err != nil {
				return "the operation failed: " + err.Error(), suiteErr(err)
			}
			got, _ := out.(map[string]any)
			switch {
			case !s.validOutput(out):
				return "the answer is not what the contract's output schema allows", nil
			case got["exists"] != true || got["start"] != stamp(at(25, 9, 0)) || got["end"] != stamp(at(25, 10, 0)):
				return "the answer does not say that the booking exists, and when", nil
			}
			return "", nil
		}},
		{name: caseGetGone, check: func(s *session, b backendUnderTest) (string, error) {
			w := b.build(map[string]calendar.MockCalendarSpec{"werkstatt": {}})
			out, err := s.operate(w, map[string]any{"calendar": "werkstatt", "bookingId": "no-such-booking"}, "")
			if err != nil {
				return "the operation failed for a booking gone: " + describe(err), suiteErr(err)
			}
			got, _ := out.(map[string]any)
			if !s.validOutput(out) || got["exists"] != false {
				return "the answer does not say that the booking no longer exists", nil
			}
			return "", nil
		}},
		{name: caseGetUnknown, check: func(s *session, b backendUnderTest) (string, error) {
			w := b.build(map[string]calendar.MockCalendarSpec{"werkstatt": {}})
			_, err := s.operate(w, map[string]any{"calendar": "fehlt", "bookingId": "x"}, "")
			return expectCode(err, "CALENDAR_NOT_FOUND", nil, "", 0)
		}},
	},
}

// suiteErr passes an error on only when it is a defect of the suite; any other failure
// is the case's outcome.
func suiteErr(err error) error {
	if isSuiteDefect(err) {
		return err
	}
	return nil
}

func describeOrSuccess(err error) string {
	if err == nil {
		return "success"
	}
	return describe(err)
}

func reserveInput(h1, m1, h2, m2 int) map[string]any {
	return map[string]any{"calendar": "werkstatt", "start": stamp(at(25, h1, m1)), "end": stamp(at(25, h2, m2)), "subject": "Veloservice", "timeZone": zone}
}

func confirmInput(id string) map[string]any {
	return map[string]any{
		"calendar": "werkstatt", "bookingId": id, "subject": "Veloservice – Muster", "body": "E-Bike, Bremsen quietschen",
		"attendees": []any{map[string]any{"email": "kundin@example.ch", "name": "Kundin"}}, "timeZone": zone,
	}
}

// reserved builds a world whose calendar holds one reservation made by reserve-slot,
// and answers its booking id. Its failure is a suite setup failure.
func reserved(s *session, b backendUnderTest, spec calendar.MockCalendarSpec) (world, string, error) {
	w := b.build(map[string]calendar.MockCalendarSpec{"werkstatt": spec})
	if w == nil {
		return nil, "", nil
	}
	out, err := s.operateOn(w.backend(), catalog.OpReserveSlot, reserveInput(9, 0, 10, 0), "setup")
	if err != nil {
		return nil, "", fmt.Errorf("suite setup: reserve-slot failed: %w", err)
	}
	id, _ := out.(map[string]any)["bookingId"].(string)
	return w, id, nil
}

// barrier releases its n callers together, or each of them after barrierTimeout: an
// operation that never reaches the second step must fail its case, not hang the suite.
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
	select {
	case <-b.done:
	case <-time.After(barrierTimeout):
	}
}

const barrierTimeout = 5 * time.Second

// lockstep holds every reservation after its create until both have created, and after
// its read until both have read: the interleaving in which each read sees the other.
type lockstep struct {
	calendar.Backend
	created, read *barrier
}

func (l *lockstep) Create(ctx context.Context, cal string, ne calendar.NewEvent) (calendar.Event, error) {
	ev, err := l.Backend.Create(ctx, cal, ne)
	l.created.wait()
	return ev, err
}

func (l *lockstep) Events(ctx context.Context, cal string, from, to time.Time) ([]calendar.Event, error) {
	evs, err := l.Backend.Events(ctx, cal, from, to)
	l.read.wait()
	return evs, err
}
