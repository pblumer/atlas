package interaction

import (
	"errors"
	"slices"
	"strconv"

	"github.com/pblumer/atlas/state"
)

// The message-only replay of ADR-0559 §8: the arrivals the graph counts, listed one by
// one in the order they happened, read from the event log page by page.
//
// It is the graph's own attribution (attribute) applied row group by row group, so a
// replay and the graph read the same log under the same rules and cannot disagree
// about who sent what to whom, or about what the reader may know.

// Event kinds.
const (
	// EventReceived is a message that arrived at a waiting element.
	EventReceived = "received"
	// EventStarted is a message that started an instance.
	EventStarted = "started"
	// EventUnreceived is a send of the center that reached nobody.
	EventUnreceived = "unreceived"
	// EventOutside is what a send of the center reached outside the reader's view:
	// how many, not where (Count, Started).
	EventOutside = "outside"
)

// Event is one thing the replay shows.
type Event struct {
	// Position is the event-log position of the row it was read from.
	Position string `json:"position"`
	At       string `json:"at"`
	Kind     string `json:"kind"`
	From     string `json:"from"`
	// To is empty for an unreceived send.
	To      string `json:"to,omitempty"`
	Message string `json:"message"`
	// Count is how many arrivals the event stands for: one, except for an outside
	// event, which the reader's send row counts as a whole.
	Count int `json:"count"`
	// Started is, for an outside event, how many of Count started an instance.
	Started int `json:"started,omitempty"`
	// CorrelationKey is the key the reader's row carries.
	CorrelationKey string `json:"correlationKey,omitempty"`
}

// ReplayPage is one page of the replay. Events are oldest first, whichever way the
// page was read.
type ReplayPage struct {
	ProcessID    string        `json:"processId"`
	Events       []Event       `json:"events"`
	Participants []Participant `json:"participants"`
	// Next is the cursor for older rows: the oldest position read. Latest is the cursor
	// for newer ones: the newest position read, or the caller's own after.
	Next   string `json:"next"`
	Latest string `json:"latest"`
	// More is whether the page was cut at its limit, so rows in its direction remain
	// unread. Reading Older, it is false once the read reached the window's start or the
	// oldest row the log keeps, which is where the replay's history begins.
	More bool `json:"more"`
	// Rows is how many event-log rows the page read.
	Rows int `json:"rows"`
	// From and To bound the window.
	From string `json:"from"`
	To   string `json:"to"`
}

// Direction is which way a page reads the log.
type Direction int

const (
	// Older reads newest first, from Input.Scan's start backwards: the history.
	Older Direction = iota
	// Newer reads oldest first, after a cursor: what came since.
	Newer
)

// ReplayInput is what Replay reads: the graph's Input, with Scan reading in Direction,
// and how many events a page holds at most. After is the caller's after cursor, echoed
// as Latest when a Newer page finds nothing.
type ReplayInput struct {
	Input
	Direction Direction
	Limit     int
	After     uint64
}

// Replay reads one page of the replay around in.ProcessID.
//
// A page ends at a command's boundary: once Limit events are held, the rows of the
// command being read are finished, so a send and what it reached are never split
// across two pages. ScanLimit bounds the rows a page reads, whatever they yield.
func Replay(in ReplayInput) (ReplayPage, error) {
	b, center, err := newBuilder(in.Input)
	if err != nil {
		return ReplayPage{}, err
	}
	page := ReplayPage{ProcessID: center.ProcessID, Events: []Event{}, From: stamp(in.From), To: stamp(in.Now)}
	var (
		group            []state.EventLogEntry
		cause            uint64
		oldest, youngest uint64
		full             bool
	)
	flush := func() {
		for _, a := range b.attribute(group) {
			page.Events = append(page.Events, toEvent(a))
		}
		group = group[:0]
	}
	if in.Scan != nil {
		err = in.Scan(func(e state.EventLogEntry) error {
			if e.At < in.From {
				if in.Direction == Older {
					return errStop // the window's start: the history begins here
				}
				return nil
			}
			if len(group) > 0 && e.Cause != cause {
				flush()
				if len(page.Events) >= in.Limit || page.Rows >= in.ScanLimit {
					full = true
					return errStop
				}
			}
			page.Rows++
			if oldest == 0 || e.Position < oldest {
				oldest = e.Position
			}
			if e.Position > youngest {
				youngest = e.Position
			}
			cause = e.Cause
			group = append(group, e)
			return nil
		})
		if err != nil && !errors.Is(err, errStop) {
			return ReplayPage{}, err
		}
		if !full {
			flush()
		}
	}
	if in.Direction == Older {
		// Read newest first; answered oldest first. Within one command the rows were
		// read in reverse too, so the whole list is turned.
		slices.Reverse(page.Events)
	}
	page.More = full
	page.Next = strconv.FormatUint(oldest, 10)
	page.Latest = strconv.FormatUint(max(youngest, in.After), 10)
	page.Participants = b.participantList()
	return page, nil
}

func toEvent(a arrival) Event {
	ev := Event{Position: strconv.FormatUint(a.position, 10), At: stamp(a.at), From: a.from, To: a.to,
		Message: a.message, Count: a.n, CorrelationKey: a.key}
	switch {
	case a.unreceived:
		ev.Kind, ev.Count = EventUnreceived, 1
	case a.outside:
		ev.Kind, ev.Started = EventOutside, a.started
	case a.started > 0:
		ev.Kind = EventStarted
	default:
		ev.Kind = EventReceived
	}
	return ev
}
