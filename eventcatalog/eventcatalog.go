// Package eventcatalog is the one list of the events Atlas emits (ADR-0435): the
// signals and messages its system processes throw, and the facts its feed delivers.
//
// An entry says what has happened when the event is emitted, where it is emitted,
// which channels carry it and what a receiver may rely on in its payload. The
// catalogue is held equal to what the code emits by drift tests in the packages that
// emit it, so an entry describes a fact and never an intention: a planned event lands
// with the change that throws it, never before.
//
// The package imports nothing of Atlas's own, so the engine, the server and the
// documentation generators may all import it.
package eventcatalog

import "regexp"

// Kind says who produces a fact.
type Kind string

const (
	// Domain is a fact of a system process, thrown by its model or published to it.
	Domain Kind = "domain"
	// Platform is a fact of the engine or the server, derived from a durable record.
	Platform Kind = "platform"
)

// Channel is one way an event reaches a receiver (ADR-0435 §4).
type Channel string

const (
	// Signal reaches every deployed listener inside the engine, and none that is not
	// deployed at the moment of the throw (ADR-0088).
	Signal Channel = "signal"
	// Message is correlated to one receiver inside the engine.
	Message Channel = "message"
	// Feed leaves Atlas as a CloudEvent, durable and ordered (ADR-0429 §5).
	Feed Channel = "feed"
	// Log is an event in logging's catalogue, for operations.
	Log Channel = "log"
)

// Stability says what may change about an entry.
type Stability string

const (
	// Stable entries change additively only: a payload field is never removed.
	Stable Stability = "stable"
	// Experimental entries may change, and a change is called out in the changelog.
	Experimental Stability = "experimental"
)

// Presence says whether a payload field is always there.
type Presence string

const (
	Always   Presence = "always"
	Optional Presence = "optional"
)

// Personal says whether a payload field names or describes a person.
//
// The zero value means nothing. The access rule (ADR-0435 §6) reads this marking, and
// a field nobody looked at must not pass as "not personal": a drift test refuses an
// Unmarked field, so the question is answered in review rather than by a default.
type Personal int

const (
	Unmarked Personal = iota
	PersonalData
	NotPersonal
)

// Field is one payload field a receiver may rely on.
type Field struct {
	Name     string
	Type     string // string, number, boolean, timestamp (RFC 3339) or json
	Presence Presence
	Personal Personal
	Meaning  string
}

// Moment is where an event is emitted.
//
// For a signal, Process and Element name the system process and the throw event. For
// a message the server publishes to a system process, they name the element that
// receives it, and Source names the server code that publishes it. For a platform
// fact, Source names the record or component it is derived from.
type Moment struct {
	Process string
	Element string
	Source  string
}

// Entry is one event Atlas emits.
type Entry struct {
	// Type is the name, used verbatim as the signal, the message or the CloudEvents
	// type. For an entry that describes a shape (Shape is true) it is the pattern the
	// product-declared names follow.
	Type string
	// Shape marks an entry whose names are declared by products, not by Atlas: the
	// catalogue describes their shape once instead of listing each name.
	Shape    bool
	Kind     Kind
	Meaning  string
	Moments  []Moment
	Channels []Channel
	// LogEvent names the event in logging's catalogue, for an entry with the log
	// channel.
	LogEvent string
	Payload  []Field
	// SecretGuard names the test that proves the payload carries no secret. Every
	// entry has one: an event is never secret.
	SecretGuard string
	// Since is the release the event arrived in, as the changelog heads it.
	Since     string
	Stability Stability
}

// Roles a receiver needs. They are spelled as the server spells them; a test in the
// server holds the two equal.
const (
	RoleAdmin      = "admin"
	RoleModeler    = "modeler"
	RoleFeedReader = "feedreader"
)

// Unreleased is the Since of an entry that has not shipped in a release yet.
const Unreleased = "Unreleased"

// nameShape is the form of an Atlas-named event (ADR-0435 §2): atlas.<subject>.<fact>,
// lower case, with a hyphen inside a word.
var nameShape = regexp.MustCompile(`^atlas\.[a-z][a-z0-9]*(-[a-z0-9]+)*\.[a-z][a-z0-9]*(-[a-z0-9]+)*$`)

// WellNamed reports whether name has the form of an Atlas-named event.
func WellNamed(name string) bool { return nameShape.MatchString(name) }

// Lookup returns the entry of an Atlas-named event.
func Lookup(name string) (Entry, bool) {
	for _, e := range Entries {
		if !e.Shape && e.Type == name {
			return e, true
		}
	}
	return Entry{}, false
}

// Has reports whether the entry is carried on channel ch.
func (e Entry) Has(ch Channel) bool {
	for _, c := range e.Channels {
		if c == ch {
			return true
		}
	}
	return false
}

// PersonalFields lists the payload fields marked as personal data, in payload order.
func (e Entry) PersonalFields() []string {
	var out []string
	for _, f := range e.Payload {
		if f.Personal == PersonalData {
			out = append(out, f.Name)
		}
	}
	return out
}

// ListenerRole is the role a deploy needs to listen to this event as a signal: admin
// when the payload carries personal data, modeler otherwise (ADR-0435 §6). The rule
// follows the data, not the name.
func (e Entry) ListenerRole() string {
	if len(e.PersonalFields()) > 0 {
		return RoleAdmin
	}
	return RoleModeler
}

// Access names the role a receiver needs on each channel where Atlas enforces one:
// the signal listener's deploy (ADR-0435 §6) and the feed (ADR-0430). A message is
// correlated to the system process that receives it, and the log is read by whoever
// operates the installation, so neither has a role of its own here.
func (e Entry) Access() map[Channel]string {
	out := map[Channel]string{}
	if e.Has(Signal) {
		out[Signal] = e.ListenerRole()
	}
	if e.Has(Feed) {
		out[Feed] = RoleFeedReader
	}
	return out
}
