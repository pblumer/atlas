package calendar

import (
	"context"
	"sort"
	"strconv"
	"sync"
	"time"
)

// Mock mode: an in-memory stand-in for a calendar account.
//
// A booking process cannot be tried without a calendar, and the calendar a booking
// needs is the one nobody wants a test writing into. [MockCalendars] is the other half
// of the worker (ADR-0168): the same resolved job, the same [Run], the same slot grid
// and conflict rule — against calendars that live in the worker's memory and are
// thrown away when it stops. The switch is the worker's, not the model's, for the
// reason the SQL mock gives (ADR-0221): the model is byte-identical either way.
//
// Unlike the SQL mock, this one executes rather than answers: a calendar is a list of
// intervals, simple enough to hold faithfully, and the conflict rule is only worth
// trying against a calendar where a reservation one job makes is seen by the next.
//
// # What is faithful
//
//   - A create with a key the calendar already holds returns that event and creates
//     nothing, as Graph's transactionId, Google's client-chosen id and CalDAV's
//     If-None-Match do.
//   - A read sees every create and delete that returned before it started: the
//     read-after-write property the conflict rule rests on. Whether Graph and Google
//     have it is the open question of the record; the mock assumes it, and says so.
//   - A calendar can be unknown, read-only, or refuse attendees, so the refusals a
//     process must handle can be tried.
//
// Nothing here is durable.
type MockCalendars struct {
	mu        sync.Mutex
	calendars map[string]*mockCalendar
	next      int
}

type mockCalendar struct {
	readOnly        bool
	refuseAttendees bool
	events          []*mockEvent
}

type mockEvent struct {
	Event
	subject   string
	body      string
	attendees []Attendee
}

// MockCalendarSpec describes one calendar of the mock.
type MockCalendarSpec struct {
	// ReadOnly makes every write refused as [KindCalendarNotWritable].
	ReadOnly bool `json:"readOnly,omitempty"`
	// RefuseAttendees makes confirm-booking with attendees refused as
	// [KindAttendeesNotAllowed], as a Google service account without domain-wide
	// delegation is refused.
	RefuseAttendees bool `json:"refuseAttendees,omitempty"`
	// Busy are appointments the calendar already holds.
	Busy []MockBusy `json:"busy,omitempty"`
}

// MockBusy is one appointment of a seeded calendar. Free marks one that does not
// block its time.
type MockBusy struct {
	Start time.Time `json:"start"`
	End   time.Time `json:"end"`
	Free  bool      `json:"free,omitempty"`
}

// NewMockCalendars creates the mock with the calendars named. A calendar not named
// does not exist.
func NewMockCalendars(specs map[string]MockCalendarSpec) *MockCalendars {
	m := &MockCalendars{calendars: map[string]*mockCalendar{}}
	names := make([]string, 0, len(specs))
	for name := range specs {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		spec := specs[name]
		c := &mockCalendar{readOnly: spec.ReadOnly, refuseAttendees: spec.RefuseAttendees}
		for _, b := range spec.Busy {
			m.next++
			c.events = append(c.events, &mockEvent{Event: Event{
				ID: "seed-" + strconv.Itoa(m.next), Start: b.Start, End: b.End, Busy: !b.Free,
			}, subject: "Busy"})
		}
		m.calendars[name] = c
	}
	return m
}

func (m *MockCalendars) calendar(name string) (*mockCalendar, error) {
	c, ok := m.calendars[name]
	if !ok {
		return nil, &Error{Kind: KindCalendarNotFound, Message: "the calendar does not exist or this Worker cannot see it"}
	}
	return c, nil
}

func (m *MockCalendars) writable(name string) (*mockCalendar, error) {
	c, err := m.calendar(name)
	if err != nil {
		return nil, err
	}
	if c.readOnly {
		return nil, &Error{Kind: KindCalendarNotWritable, Message: "this Worker may read the calendar but not write to it"}
	}
	return c, nil
}

// Events implements [Backend].
func (m *MockCalendars) Events(_ context.Context, calendar string, from, to time.Time) ([]Event, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	c, err := m.calendar(calendar)
	if err != nil {
		return nil, err
	}
	var out []Event
	for _, e := range c.events {
		if e.overlaps(from, to) {
			out = append(out, e.Event)
		}
	}
	return out, nil
}

// Create implements [Backend].
func (m *MockCalendars) Create(_ context.Context, calendar string, ne NewEvent) (Event, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	c, err := m.writable(calendar)
	if err != nil {
		return Event{}, err
	}
	for _, e := range c.events {
		if ne.Key != "" && e.Key == ne.Key {
			return e.Event, nil
		}
	}
	m.next++
	e := &mockEvent{Event: Event{ID: "event-" + strconv.Itoa(m.next), Key: ne.Key, Start: ne.Start, End: ne.End, Busy: true}, subject: ne.Subject}
	c.events = append(c.events, e)
	return e.Event, nil
}

// Update implements [Backend].
func (m *MockCalendars) Update(_ context.Context, calendar, id string, d Details) (Event, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	c, err := m.writable(calendar)
	if err != nil {
		return Event{}, err
	}
	e := c.find(id)
	if e == nil {
		return Event{}, &Error{Kind: KindBookingNotFound, Message: "the booking does not exist in this calendar"}
	}
	if len(d.Attendees) > 0 && c.refuseAttendees {
		return Event{}, &Error{Kind: KindAttendeesNotAllowed, Message: "this Worker may not invite attendees to this calendar"}
	}
	if d.Subject != "" {
		e.subject = d.Subject
	}
	e.body = d.Body
	e.attendees = append([]Attendee(nil), d.Attendees...)
	return e.Event, nil
}

// Delete implements [Backend].
func (m *MockCalendars) Delete(_ context.Context, calendar, id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	c, err := m.writable(calendar)
	if err != nil {
		return err
	}
	for i, e := range c.events {
		if e.ID == id {
			c.events = append(c.events[:i], c.events[i+1:]...)
			return nil
		}
	}
	return nil
}

// Get implements [Backend].
func (m *MockCalendars) Get(_ context.Context, calendar, id string) (Event, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	c, err := m.calendar(calendar)
	if err != nil {
		return Event{}, false, err
	}
	if e := c.find(id); e != nil {
		return e.Event, true, nil
	}
	return Event{}, false, nil
}

func (c *mockCalendar) find(id string) *mockEvent {
	for _, e := range c.events {
		if e.ID == id {
			return e
		}
	}
	return nil
}

// MockEvent is one event of the mock as a test or the mock worker's journal sees it,
// with what a confirmation set on it.
type MockEvent struct {
	Event
	Subject   string
	Body      string
	Attendees []Attendee
}

// Snapshot returns the events of a calendar in creation order.
func (m *MockCalendars) Snapshot(calendar string) ([]MockEvent, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	c, err := m.calendar(calendar)
	if err != nil {
		return nil, err
	}
	out := make([]MockEvent, 0, len(c.events))
	for _, e := range c.events {
		out = append(out, MockEvent{Event: e.Event, Subject: e.subject, Body: e.body, Attendees: append([]Attendee(nil), e.attendees...)})
	}
	return out, nil
}
