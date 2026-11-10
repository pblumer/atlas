package calendar

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/mail"
	"slices"
	"strconv"
	"strings"
	"time"
	// A worker on Windows has no zone database of its own; the job names an IANA zone.
	_ "time/tzdata"

	"github.com/pblumer/atlas/connector/calendar/catalog"
	"github.com/pblumer/atlas/job"
)

// Job is a calendar task with everything already evaluated. There is nowhere here to
// put a credential, which is what makes "the engine holds no calendar credential" a
// property of the type.
type Job struct {
	// Connector names the Worker — the calendar account — the *worker* is configured
	// for. Which provider is behind it is the Worker's configuration, not the model's.
	Connector string `json:"connector"`
	Operation string `json:"operation"`

	// find-free-slots.
	Calendars       []string            `json:"calendars,omitempty"`
	From            string              `json:"from,omitempty"`
	To              string              `json:"to,omitempty"`
	DurationMinutes int32               `json:"durationMinutes,omitempty"`
	GridMinutes     int32               `json:"gridMinutes,omitempty"`
	BufferMinutes   int32               `json:"bufferMinutes,omitempty"`
	MinLeadMinutes  int32               `json:"minLeadMinutes,omitempty"`
	OpeningHours    map[string][]Window `json:"openingHours,omitempty"`
	ClosedDates     []string            `json:"closedDates,omitempty"`
	Require         string              `json:"require,omitempty"`
	MaxResults      int32               `json:"maxResults,omitempty"`

	// The zone every wall time is read and written in; UTC when empty.
	TimeZone string `json:"timeZone,omitempty"`

	// reserve-slot, and the operations on a booking.
	Calendar  string     `json:"calendar,omitempty"`
	Start     string     `json:"start,omitempty"`
	End       string     `json:"end,omitempty"`
	BookingID string     `json:"bookingId,omitempty"`
	Subject   string     `json:"subject,omitempty"`
	Body      string     `json:"body,omitempty"`
	Attendees []Attendee `json:"attendees,omitempty"`

	ResultVariable string `json:"resultVariable,omitempty"`
	// ErrorCodes are the codes an error boundary or error event subprocess of the task's
	// process catches (ADR-0461); they travel because a worker has no compiled process.
	ErrorCodes []string `json:"errorCodes,omitempty"`
	// CreateKey is what makes reserve-slot safe to repeat: derived by the engine from
	// this installation's node id and the job key ([CreateKey]), the same on every
	// delivery of the job and different for every other job. Set on reserve-slot only.
	CreateKey string `json:"createKey,omitempty"`
}

// The BPMN errors an operation ends with when the task's process catches the code.
// Uncaught, each stays a failure with its retries and then an incident. Each is the
// contract code of the same meaning behind the Worker Type's prefix; the conformance
// suites map one to the other.
const (
	// CodeSlotTaken: the conflict rule found the slot taken and removed the
	// reservation. The process leads back to choosing a slot.
	CodeSlotTaken = "CALENDAR_SLOT_TAKEN"
	// CodeCalendarNotFound: the calendar does not exist or the Worker cannot see it.
	CodeCalendarNotFound = "CALENDAR_NOT_FOUND"
	// CodeCalendarNotWritable: the Worker may read the calendar but not write to it.
	CodeCalendarNotWritable = "CALENDAR_NOT_WRITABLE"
	// CodeBookingNotFound: the booked event no longer exists — deleted in the calendar
	// since the process stored its id.
	CodeBookingNotFound = "CALENDAR_BOOKING_NOT_FOUND"
	// CodeAttendeesNotAllowed: the provider refuses attendees from this Worker.
	CodeAttendeesNotAllowed = "CALENDAR_ATTENDEES_NOT_ALLOWED"
)

// Codes lists every code an operation of this worker can throw.
var Codes = []string{CodeSlotTaken, CodeCalendarNotFound, CodeCalendarNotWritable, CodeBookingNotFound, CodeAttendeesNotAllowed}

var codeOfKind = map[Kind]string{
	KindCalendarNotFound:    CodeCalendarNotFound,
	KindCalendarNotWritable: CodeCalendarNotWritable,
	KindBookingNotFound:     CodeBookingNotFound,
	KindAttendeesNotAllowed: CodeAttendeesNotAllowed,
}

// errSlotTaken is the conflict rule's refusal.
var errSlotTaken = errors.New("calendar: reserve-slot: the slot was taken by another appointment; the reservation was removed")

// thrown is err as the job ends with it: a refusal the process has a path for becomes
// that BPMN error; anything else stays a failure.
func thrown(j Job, err error) error {
	code := ""
	if errors.Is(err, errSlotTaken) {
		code = CodeSlotTaken
	} else {
		code = codeOfKind[kindOf(err)]
	}
	if code != "" && slices.Contains(j.ErrorCodes, code) {
		return &job.BPMNError{Code: code, Message: err.Error()}
	}
	return err
}

// CreateKey derives a reserve-slot job's key from the installation's node id and the
// job key. The node id is what tells two installations' job keys apart: a test server
// and a production server booking into one calendar would otherwise derive the same
// key for different reservations, and the second would take the first's for its own.
func CreateKey(nodeID string, jobKey uint64) string {
	sum := sha256.Sum256([]byte("atlas calendar reserve\x00" + nodeID + "\x00" + strconv.FormatUint(jobKey, 10)))
	return hex.EncodeToString(sum[:16])
}

// Run performs a resolved job through the caller's own registry and returns the
// variables the job completes with. It is the whole of the worker's half.
func Run(ctx context.Context, j Job, reg *Registry) (map[string]any, error) {
	return RunAt(ctx, j, reg, time.Now())
}

// RunAt is [Run] at a given instant: the earliest slot find-free-slots offers, and the
// slot reserve-slot refuses as already begun, are measured from now. The conformance
// suites pass a fixed instant, so a record derived today and verified next year runs
// the same cases.
//
// The Worker lookup comes first: an unconfigured name is the actionable failure. Every
// authored value is checked before the calendar is reached, so a refused job has
// changed nothing.
func RunAt(ctx context.Context, j Job, reg *Registry, now time.Time) (map[string]any, error) {
	b, ok := reg.Client(j.Connector)
	if !ok {
		return nil, reg.Unresolved("calendar", j.Connector)
	}
	op, ok := catalog.Lookup(j.Operation)
	if !ok {
		return nil, fmt.Errorf("calendar: unknown operation %q (want %s)", j.Operation, strings.Join(catalog.Names(), ", "))
	}
	loc, err := location(j.TimeZone)
	if err != nil {
		return nil, err
	}
	value, err := run(ctx, b, j, op, loc, now)
	if err != nil {
		return nil, thrown(j, err)
	}
	if op.Result == catalog.ResultNone || j.ResultVariable == "" {
		return nil, nil
	}
	return map[string]any{j.ResultVariable: value}, nil
}

func location(name string) (*time.Location, error) {
	if strings.TrimSpace(name) == "" {
		return time.UTC, nil
	}
	loc, err := time.LoadLocation(name)
	if err != nil {
		return nil, fmt.Errorf("calendar: %q is not a time zone this worker knows (want an IANA name such as Europe/Zurich)", name)
	}
	return loc, nil
}

func run(ctx context.Context, b Backend, j Job, op catalog.Op, loc *time.Location, now time.Time) (any, error) {
	switch {
	case op.FindsSlots:
		req, err := slotRequestOf(j, loc, now)
		if err != nil {
			return nil, err
		}
		return findFreeSlots(ctx, b, req)
	case op.NeedsSlot:
		cal, start, end, err := slotOf(j, now)
		if err != nil {
			return nil, err
		}
		if strings.TrimSpace(j.CreateKey) == "" {
			return nil, fmt.Errorf("calendar: reserve-slot: the job carries no create key, so a repeated delivery could book twice; it was not built by the engine")
		}
		subject := strings.TrimSpace(j.Subject)
		if subject == "" {
			subject = "Reserved"
		}
		if len(subject) > catalog.MaxSubjectLength {
			return nil, fmt.Errorf("calendar: reserve-slot: the subject is longer than %d characters", catalog.MaxSubjectLength)
		}
		ev, err := reserve(ctx, b, cal, NewEvent{Key: j.CreateKey, Subject: subject, Start: start, End: end})
		if err != nil {
			return nil, err
		}
		return bookingOf(cal, ev, loc), nil
	case op.NeedsBooking:
		cal, id, err := bookingRef(j)
		if err != nil {
			return nil, err
		}
		switch j.Operation {
		case catalog.OpConfirmBooking:
			d, err := detailsOf(j)
			if err != nil {
				return nil, err
			}
			ev, err := b.Update(ctx, cal, id, d)
			if err != nil {
				return nil, err
			}
			return bookingOf(cal, ev, loc), nil
		case catalog.OpCancelBooking:
			err := b.Delete(ctx, cal, id)
			if kindOf(err) == KindBookingNotFound {
				return nil, nil // already gone is what the task asked for
			}
			return nil, err
		case catalog.OpGetBooking:
			ev, ok, err := b.Get(ctx, cal, id)
			if err != nil {
				return nil, err
			}
			if !ok {
				return map[string]any{"bookingId": id, "calendar": cal, "exists": false}, nil
			}
			out := bookingOf(cal, ev, loc)
			out["exists"] = true
			return out, nil
		}
	}
	return nil, fmt.Errorf("calendar: operation %q is in the table but not implemented", j.Operation)
}

// reserve is the conflict rule (ADR-draft-calendar-worker-type, *The conflict rule*):
// create the placeholder, read the slot, and give the slot up when anything else
// takes any of its time.
//
// Why at most one of two competing reservations survives: each read starts after its
// own create returned, so the later of the two reads starts after both creates
// returned and — on a provider whose reads see every create that returned before they
// started — sees the other placeholder and gives up. Both may give up; neither then
// gets the slot, which costs a retry and never a double booking. A tie-break that let
// one of them keep the slot would not be safe: when only one sees the other, it can
// keep both.
//
// A repeated delivery runs the same steps: the create key finds the placeholder the
// first delivery made, and the rule runs on it again. A delete that fails leaves the
// job to be retried, which deletes again.
func reserve(ctx context.Context, b Backend, cal string, ne NewEvent) (Event, error) {
	ev, err := b.Create(ctx, cal, ne)
	if err != nil {
		return Event{}, err
	}
	others, err := b.Events(ctx, cal, ne.Start, ne.End)
	if err != nil {
		return Event{}, err
	}
	for _, o := range others {
		if o.ID == ev.ID || (o.Key != "" && o.Key == ne.Key) || !o.Busy || !o.overlaps(ne.Start, ne.End) {
			continue
		}
		if err := b.Delete(ctx, cal, ev.ID); err != nil && kindOf(err) != KindBookingNotFound {
			return Event{}, fmt.Errorf("calendar: reserve-slot: the slot is taken, and removing the reservation failed; the job is retried: %w", err)
		}
		return Event{}, errSlotTaken
	}
	return ev, nil
}

// findFreeSlots reads each calendar once over the window widened by the buffer and
// lays the grid over what it read.
func findFreeSlots(ctx context.Context, b Backend, r *slotRequest) (map[string]any, error) {
	busy := make(map[string][]Event, len(r.calendars))
	for _, cal := range r.calendars {
		evs, err := b.Events(ctx, cal, r.from.Add(-r.buffer), r.to.Add(r.buffer))
		if err != nil {
			return nil, err
		}
		busy[cal] = evs
	}
	slots, truncated := r.freeSlots(busy)
	list := make([]any, 0, len(slots))
	for _, s := range slots {
		local := s.Start.In(r.loc)
		cals := make([]any, len(s.Calendars))
		for i, c := range s.Calendars {
			cals[i] = c
		}
		list = append(list, map[string]any{
			"start":     local.Format(time.RFC3339),
			"end":       s.End.In(r.loc).Format(time.RFC3339),
			"date":      local.Format(time.DateOnly),
			"time":      local.Format("15:04"),
			"calendars": cals,
		})
	}
	return map[string]any{"slots": list, "truncated": truncated}, nil
}

func bookingOf(cal string, ev Event, loc *time.Location) map[string]any {
	return map[string]any{
		"bookingId": ev.ID,
		"calendar":  cal,
		"start":     ev.Start.In(loc).Format(time.RFC3339),
		"end":       ev.End.In(loc).Format(time.RFC3339),
	}
}

func parseInstant(what, s string) (time.Time, error) {
	t, err := time.Parse(time.RFC3339, strings.TrimSpace(s))
	if err != nil {
		return time.Time{}, fmt.Errorf("%s %q is not a date and time with an offset (RFC 3339, e.g. 2026-11-12T09:00:00+01:00)", what, s)
	}
	return t, nil
}

func checkMinutes(what string, v int32, lo, hi int) error {
	if int(v) < lo || int(v) > hi {
		return fmt.Errorf("calendar: find-free-slots: %s is %d minutes; it must be between %d and %d", what, v, lo, hi)
	}
	return nil
}

// slotRequestOf checks find-free-slots' values and builds the request.
func slotRequestOf(j Job, loc *time.Location, now time.Time) (*slotRequest, error) {
	cals, err := calendarList(j.Calendars)
	if err != nil {
		return nil, err
	}
	from, err := parseInstant("calendar: find-free-slots: from", j.From)
	if err != nil {
		return nil, err
	}
	to, err := parseInstant("calendar: find-free-slots: to", j.To)
	if err != nil {
		return nil, err
	}
	if !from.Before(to) {
		return nil, fmt.Errorf("calendar: find-free-slots: from is not before to")
	}
	if to.Sub(from) > catalog.MaxWindowDays*24*time.Hour {
		return nil, fmt.Errorf("calendar: find-free-slots: the window is longer than %d days", catalog.MaxWindowDays)
	}
	if err := checkMinutes("the duration", j.DurationMinutes, catalog.MinMinutes, catalog.MaxMinutes); err != nil {
		return nil, err
	}
	grid := j.GridMinutes
	if grid == 0 {
		grid = j.DurationMinutes
	}
	if err := checkMinutes("the grid", grid, catalog.MinMinutes, catalog.MaxMinutes); err != nil {
		return nil, err
	}
	if err := checkMinutes("the buffer", j.BufferMinutes, 0, catalog.MaxBufferMinutes); err != nil {
		return nil, err
	}
	if j.MinLeadMinutes < 0 {
		return nil, fmt.Errorf("calendar: find-free-slots: the lead time is negative")
	}
	opening, err := parseOpening(j.OpeningHours)
	if err != nil {
		return nil, fmt.Errorf("calendar: find-free-slots: %w", err)
	}
	if len(j.ClosedDates) > catalog.MaxClosedDates {
		return nil, fmt.Errorf("calendar: find-free-slots: %d closed dates; at most %d are allowed", len(j.ClosedDates), catalog.MaxClosedDates)
	}
	closed := make(map[string]bool, len(j.ClosedDates))
	for _, d := range j.ClosedDates {
		if _, err := time.Parse(time.DateOnly, d); err != nil {
			return nil, fmt.Errorf("calendar: find-free-slots: closed date %q is not a date in the form YYYY-MM-DD", d)
		}
		closed[d] = true
	}
	require := j.Require
	switch require {
	case "":
		require = catalog.RequireAny
	case catalog.RequireAny, catalog.RequireAll:
	default:
		return nil, fmt.Errorf("calendar: find-free-slots: require is %q; want %q or %q", j.Require, catalog.RequireAny, catalog.RequireAll)
	}
	limit := int(j.MaxResults)
	if limit <= 0 || limit > int(catalog.SlotBounds.Max) {
		limit = int(catalog.SlotBounds.Default)
	}
	return &slotRequest{
		calendars: cals, from: from, to: to,
		duration: time.Duration(j.DurationMinutes) * time.Minute,
		grid:     time.Duration(grid) * time.Minute,
		buffer:   time.Duration(j.BufferMinutes) * time.Minute,
		earliest: now.Add(time.Duration(j.MinLeadMinutes) * time.Minute),
		loc:      loc, opening: opening, closed: closed, require: require, max: limit,
	}, nil
}

func calendarList(in []string) ([]string, error) {
	if len(in) == 0 {
		return nil, fmt.Errorf("calendar: find-free-slots: no calendar is named")
	}
	if len(in) > catalog.MaxCalendars {
		return nil, fmt.Errorf("calendar: find-free-slots: %d calendars are named; at most %d are allowed", len(in), catalog.MaxCalendars)
	}
	out := make([]string, 0, len(in))
	for _, c := range in {
		c = strings.TrimSpace(c)
		if c == "" {
			return nil, fmt.Errorf("calendar: find-free-slots: a calendar evaluated to nothing; set the variable it reads before this task")
		}
		if slices.Contains(out, c) {
			return nil, fmt.Errorf("calendar: find-free-slots: the calendar %q is named twice", c)
		}
		out = append(out, c)
	}
	return out, nil
}

// slotOf checks reserve-slot's calendar and slot.
func slotOf(j Job, now time.Time) (string, time.Time, time.Time, error) {
	cal := strings.TrimSpace(j.Calendar)
	if cal == "" {
		return "", time.Time{}, time.Time{}, fmt.Errorf("calendar: reserve-slot: the calendar evaluated to nothing; set the variable it reads before this task")
	}
	start, err := parseInstant("calendar: reserve-slot: start", j.Start)
	if err != nil {
		return "", time.Time{}, time.Time{}, err
	}
	end, err := parseInstant("calendar: reserve-slot: end", j.End)
	if err != nil {
		return "", time.Time{}, time.Time{}, err
	}
	if !start.Before(end) {
		return "", time.Time{}, time.Time{}, fmt.Errorf("calendar: reserve-slot: start is not before end")
	}
	if end.Sub(start) > catalog.MaxMinutes*time.Minute {
		return "", time.Time{}, time.Time{}, fmt.Errorf("calendar: reserve-slot: the slot is longer than a day")
	}
	if start.Before(now) {
		return "", time.Time{}, time.Time{}, fmt.Errorf("calendar: reserve-slot: the slot has already begun")
	}
	return cal, start, end, nil
}

func bookingRef(j Job) (string, string, error) {
	cal, id := strings.TrimSpace(j.Calendar), strings.TrimSpace(j.BookingID)
	if cal == "" {
		return "", "", fmt.Errorf("calendar: %s: the calendar evaluated to nothing; set the variable it reads before this task", j.Operation)
	}
	if id == "" {
		return "", "", fmt.Errorf("calendar: %s: the booking id evaluated to nothing; store reserve-slot's bookingId before this task", j.Operation)
	}
	return cal, id, nil
}

func detailsOf(j Job) (Details, error) {
	d := Details{Subject: strings.TrimSpace(j.Subject), Body: j.Body}
	if len(d.Subject) > catalog.MaxSubjectLength {
		return Details{}, fmt.Errorf("calendar: confirm-booking: the subject is longer than %d characters", catalog.MaxSubjectLength)
	}
	if len(d.Body) > catalog.MaxBodyLength {
		return Details{}, fmt.Errorf("calendar: confirm-booking: the body is longer than %d characters", catalog.MaxBodyLength)
	}
	if len(j.Attendees) > catalog.MaxAttendees {
		return Details{}, fmt.Errorf("calendar: confirm-booking: %d attendees; at most %d are allowed", len(j.Attendees), catalog.MaxAttendees)
	}
	for _, a := range j.Attendees {
		addr, err := mail.ParseAddress(a.Email)
		if err != nil || addr.Address != a.Email {
			return Details{}, fmt.Errorf("calendar: confirm-booking: an attendee's e-mail address is not one address")
		}
		d.Attendees = append(d.Attendees, a)
	}
	return d, nil
}
