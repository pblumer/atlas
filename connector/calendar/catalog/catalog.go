// Package catalog is the calendar Worker's operation table (ADR-0552):
// which values each operation needs, what it returns, the bounds of its list, and the
// technical capability it claims.
//
// It imports nothing of atlas's, for the reason connector/carddav/catalog gives: the
// compiler reads these rows to refuse a model at deploy, and connector/calendar reads
// the same rows to run its operations, so there is one table and nothing to keep in
// step.
//
// # One profile for every backend
//
// Every claim carries the profile free-busy-calendar. ADR-0480 refuses two claims of
// one contract version on one operation, and one operation here serves every provider
// backend, so the profile is the constraint set all of them meet, not the provider. A
// claim stays a candidate until an evidence record verifies it (ADR-0483).
package catalog

import (
	"fmt"
	"slices"
	"sort"
	"strings"
)

// Profile is the technical capability profile every operation claims.
const Profile = "free-busy-calendar"

// ContractVersion is the version of the drafted contracts the operations claim.
const ContractVersion = "1.0.0"

// Result is what an operation hands back to a result variable.
type Result int

const (
	// ResultNone marks an operation that returns nothing worth keeping (a cancel). The
	// compiler refuses a result variable on it.
	ResultNone Result = iota
	// ResultObject is one object: a booking, or the slot list's envelope.
	ResultObject
)

// Bounds holds an operation's maxResults: the value a model gets when it authors none,
// and the most it may author.
type Bounds struct {
	Default int32
	Max     int32
}

// CapabilityClaim is one technical capability the operation declares (ADR-0480). It
// mirrors workertype.CapabilityClaim's three fields instead of importing it, as
// connector/entra does, so the worker binary does not take on the contract
// validator's dependencies.
type CapabilityClaim struct {
	ID      string
	Version string
	Profile string
}

// Idempotency and Effect are ADR-0538's two declarations, as their manifest values.
const (
	IdempotencyNatural = "natural"
	IdempotencyKey     = "key"
	EffectReversible   = "reversible"
)

// Op is one operation a model can author.
type Op struct {
	// Label describes the operation in an error message.
	Label string
	// FindsSlots marks find-free-slots: it takes the calendars, the window, the
	// duration, the grid and the opening hours.
	FindsSlots bool
	// NeedsSlot marks reserve-slot: one calendar, a start and an end.
	NeedsSlot bool
	// NeedsBooking marks the operations on a reserved booking: its calendar and id.
	NeedsBooking bool
	// TakesDetails marks confirm-booking: the final subject, body and attendees.
	TakesDetails bool
	// Create marks the one operation that creates an event, and so needs the job's
	// create key to be safe to repeat.
	Create bool
	// Inputs are the keys the task's input object may carry — exactly the properties of
	// the operation's contract input — and Required those it must. The compiler refuses
	// a key outside Inputs, so a misspelt one is an error at deploy, not a value the
	// Worker ignores.
	Inputs   []string
	Required []string
	// Result is what the operation answers; NeedsResult marks the operations whose
	// answer a process must keep (a read that discards its result is one nothing asked
	// for, and a reservation's bookingId is what confirms and cancels it).
	Result      Result
	NeedsResult bool
	// MaxResults bounds the slot list; the zero value on every other operation.
	MaxResults Bounds
	// Idempotency and Effect are the operation's ADR-0538 declarations.
	Idempotency string
	Effect      string
	// Capabilities are the technical capabilities the operation claims.
	Capabilities []CapabilityClaim
}

// The operation names.
const (
	OpFindFreeSlots  = "find-free-slots"
	OpReserveSlot    = "reserve-slot"
	OpConfirmBooking = "confirm-booking"
	OpCancelBooking  = "cancel-booking"
	OpGetBooking     = "get-booking"
)

// SlotBounds bounds the slot list. A slot list is the earliest free slots of the window;
// a list cut at the bound is still true — every slot on it was free when it was read —
// which is why find-free-slots truncates where a contact listing fails.
var SlotBounds = Bounds{Default: 200, Max: 1000}

func claim(id string) []CapabilityClaim {
	return []CapabilityClaim{{ID: id, Version: ContractVersion, Profile: Profile}}
}

// Ops is the table, by operation name.
var Ops = map[string]Op{
	OpFindFreeSlots: {
		Label: "find the free slots of one or more calendars", FindsSlots: true,
		Inputs: []string{"calendars", "from", "to", "durationMinutes", "gridMinutes", "bufferMinutes", "minLeadMinutes",
			"timeZone", "openingHours", "closedDates", "require", "maxResults"},
		Required: []string{"calendars", "from", "to", "durationMinutes", "timeZone", "openingHours"},
		Result:   ResultObject, NeedsResult: true, MaxResults: SlotBounds,
		Idempotency: IdempotencyNatural, Effect: EffectReversible,
		Capabilities: claim("io.atlas.calendar.slot.find"),
	},
	OpReserveSlot: {
		Label: "reserve one slot in a calendar", NeedsSlot: true, Create: true,
		Inputs:   []string{"calendar", "start", "end", "subject", "timeZone"},
		Required: []string{"calendar", "start", "end"},
		Result:   ResultObject, NeedsResult: true,
		Idempotency: IdempotencyKey, Effect: EffectReversible,
		Capabilities: claim("io.atlas.calendar.booking.reserve"),
	},
	OpConfirmBooking: {
		Label: "confirm a reserved booking", NeedsBooking: true, TakesDetails: true,
		Inputs:      []string{"calendar", "bookingId", "subject", "body", "attendees", "timeZone"},
		Required:    []string{"calendar", "bookingId"},
		Result:      ResultObject,
		Idempotency: IdempotencyNatural, Effect: EffectReversible,
		Capabilities: claim("io.atlas.calendar.booking.confirm"),
	},
	OpCancelBooking: {
		Label: "cancel a booking", NeedsBooking: true,
		Inputs:      []string{"calendar", "bookingId"},
		Required:    []string{"calendar", "bookingId"},
		Result:      ResultNone,
		Idempotency: IdempotencyNatural, Effect: EffectReversible,
		Capabilities: claim("io.atlas.calendar.booking.cancel"),
	},
	OpGetBooking: {
		Label: "read a booking", NeedsBooking: true,
		Inputs:   []string{"calendar", "bookingId", "timeZone"},
		Required: []string{"calendar", "bookingId"},
		Result:   ResultObject, NeedsResult: true,
		Idempotency: IdempotencyNatural, Effect: EffectReversible,
		Capabilities: claim("io.atlas.calendar.booking.get"),
	},
}

// Lookup returns the operation of that name.
func Lookup(name string) (Op, bool) {
	op, ok := Ops[name]
	return op, ok
}

// Names lists the operations, sorted.
func Names() []string {
	out := make([]string, 0, len(Ops))
	for n := range Ops {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}

// Weekdays are the keys of an opening-hours object, Monday first.
var Weekdays = []string{"mon", "tue", "wed", "thu", "fri", "sat", "sun"}

// Window bounds the authored values of find-free-slots. They are bounds on a request a
// public page triggers, so each is a ceiling on work a visitor can cause: the days read
// from a provider, the calendars asked, the slots generated.
const (
	// MaxWindowDays is the longest window find-free-slots reads. Microsoft Graph's
	// getSchedule takes at most 62 days; calendarView has no documented ceiling, but one
	// bound for every backend keeps a model portable.
	MaxWindowDays = 62
	// MaxCalendars is the most calendars one find-free-slots reads.
	MaxCalendars = 20
	// MinMinutes and MaxMinutes bound a slot's duration and the grid.
	MinMinutes = 5
	MaxMinutes = 24 * 60
	// MaxBufferMinutes bounds the buffer kept free before and after a slot.
	MaxBufferMinutes = 240
	// MaxWindowsPerDay bounds the opening intervals of one weekday.
	MaxWindowsPerDay = 8
	// MaxClosedDates bounds the dates a shop is closed on.
	MaxClosedDates = 400
	// MaxAttendees bounds the attendees confirm-booking adds.
	MaxAttendees = 20
	// MaxSubjectLength and MaxBodyLength bound the text of an event.
	MaxSubjectLength = 255
	MaxBodyLength    = 8000
)

// Require says which calendars must be free for a slot to be offered.
const (
	// RequireAny offers a slot when any calendar is free in it: "any free mechanic",
	// or one of several equal rooms.
	RequireAny = "any"
	// RequireAll offers a slot only when every calendar is free in it: a person and a
	// room together.
	RequireAll = "all"
)

// TakesResult reports whether the operation writes a result variable at all.
func (o Op) TakesResult() bool { return o.Result != ResultNone }

// CheckInputKeys reports the first key of an input object the operation does not take,
// and the first it needs that is missing.
func CheckInputKeys(name string, keys []string) error {
	op, ok := Ops[name]
	if !ok {
		return fmt.Errorf("calendar: unknown operation %q", name)
	}
	have := map[string]bool{}
	for _, k := range keys {
		if !slices.Contains(op.Inputs, k) {
			return fmt.Errorf("calendar: operation %q does not take %q in its input (it takes %s)", name, k, strings.Join(op.Inputs, ", "))
		}
		have[k] = true
	}
	for _, k := range op.Required {
		if !have[k] {
			return fmt.Errorf("calendar: operation %q needs %q in its input", name, k)
		}
	}
	return nil
}
