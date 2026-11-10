package calendar

import (
	"context"
	"errors"
	"time"

	"github.com/pblumer/atlas/connector/clientreg"
)

// Event is one appointment, or one occurrence of a recurring one, as a backend reads
// it. It carries only what finding slots and checking a reservation need: no subject,
// no attendees, no body of anybody else's appointment.
type Event struct {
	// ID is the provider's identifier of the event (of the occurrence, for a recurring
	// event). It is what a booking is addressed by afterwards.
	ID string
	// Key is the create key atlas wrote on the event, and empty on every event atlas
	// did not create.
	Key string
	// Start and End bound the event. End is exclusive.
	Start, End time.Time
	// Busy says whether the event blocks its time. The contracts fix the rule for every
	// backend: an event is busy unless it is marked free or transparent, or cancelled;
	// tentative, out of office and working elsewhere are busy.
	Busy bool
}

// overlaps reports whether the event takes any time of [start, end).
func (e Event) overlaps(start, end time.Time) bool {
	return e.Start.Before(end) && start.Before(e.End)
}

// NewEvent is the placeholder reserve-slot creates: a busy event with a neutral
// subject, no attendees and the create key that makes the create safe to repeat.
type NewEvent struct {
	Key     string
	Subject string
	Start   time.Time
	End     time.Time
}

// Details are what confirm-booking sets on a reserved event.
type Details struct {
	Subject   string
	Body      string
	Attendees []Attendee
}

// Attendee is one person confirm-booking invites.
type Attendee struct {
	Email string `json:"email"`
	Name  string `json:"name,omitempty"`
}

// Backend is one provider's calendar: Microsoft Graph, Google Calendar, CalDAV,
// Exchange Web Services, or the mock. Everything above it — the slot grid, the
// conflict rule, the checks, the result shapes — is shared, which is what lets one
// model run against any of them.
//
// A backend reports the failures a process can act on as an [*Error] of a known
// [Kind]; anything else is a failure the job retries.
type Backend interface {
	// Events reads the events of a calendar that overlap [from, to), with recurring
	// events expanded into their occurrences.
	Events(ctx context.Context, calendar string, from, to time.Time) ([]Event, error)
	// Create creates the placeholder, or — when the calendar already holds an event
	// with the same key — returns that event and creates nothing.
	Create(ctx context.Context, calendar string, ev NewEvent) (Event, error)
	// Update sets the details of an existing event.
	Update(ctx context.Context, calendar, id string, d Details) (Event, error)
	// Delete removes an event. An event already gone is success.
	Delete(ctx context.Context, calendar, id string) error
	// Get reads one event; false when it does not exist.
	Get(ctx context.Context, calendar, id string) (Event, bool, error)
}

// Kind is a failure a process can tell apart and act on.
type Kind int

const (
	// KindCalendarNotFound: the calendar does not exist, or the Worker cannot see it.
	KindCalendarNotFound Kind = iota + 1
	// KindCalendarNotWritable: the Worker may read the calendar but not write to it.
	KindCalendarNotWritable
	// KindBookingNotFound: the event the process booked no longer exists.
	KindBookingNotFound
	// KindAttendeesNotAllowed: the provider refuses attendees from this Worker's
	// identity (a Google service account without domain-wide delegation).
	KindAttendeesNotAllowed
)

// Error is a failure of a known kind. Message is for the operator and names no
// personal data.
type Error struct {
	Kind    Kind
	Message string
}

func (e *Error) Error() string { return "calendar: " + e.Message }

// kindOf is err's kind, or 0 when it is not a failure of a known kind.
func kindOf(err error) Kind {
	var e *Error
	if errors.As(err, &e) {
		return e.Kind
	}
	return 0
}

// Registry resolves a Worker name to the calendar backend behind it. It is the
// worker's own map: the engine never builds one for this kind.
type Registry = clientreg.Registry[Backend]

// NewRegistry creates an empty registry.
func NewRegistry() *Registry { return clientreg.New[Backend]() }
