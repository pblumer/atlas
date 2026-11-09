// Package pubinterface is ADR-0373's published process interface: a small,
// versioned, explicitly published contract a process offers another domain, derived
// from a deployed definition rather than typed out beside it, with the right to read
// it (view) and the right to deliver to it (send) granted apart.
//
// It is design-time configuration, like a start grant (ADR-0516) or a release: the
// store is a sidecar (ADR-0282) owned by the API run loop, and nothing here reaches
// the processor, the WAL or applyToState. Deriving a contract reads a compiled
// process, which is immutable and safe to read from any goroutine.
//
// What this package does not do yet, and says so rather than pretending:
//
//   - a commitment (by when an accepted message will be correlated) is refused at
//     publish, because nothing measures it yet and a promise nobody watches reads as
//     kept;
//   - the send grant is answered by [Store.MaySend] for the peer endpoint of
//     ADR-0372, which is not built; until it is, a send grant lets nobody deliver
//     anything;
//   - the observe grant is deliberately absent, as ADR-0373 decided.
package pubinterface

import (
	"regexp"
	"sort"
)

// Entry-point kinds: the element behind a contract-level entry point.
const (
	KindMessageStart      = "message-start"      // a root-scope message start event (ADR-0035)
	KindIntermediateCatch = "intermediate-catch" // a message intermediate catch event (ADR-0020)
	KindReceiveTask       = "receive-task"       // a receive task (ADR-0102)
	KindEventSubprocess   = "event-subprocess"   // a message-triggered event subprocess (ADR-0082)
	KindBoundary          = "boundary"           // a message boundary event (ADR-0040)
)

// Payload bases: where a derived payload description came from.
const (
	// BasisInformationModel: the element writes the message into a data object typed
	// against the information model (ADR-0230); Class names that class.
	BasisInformationModel = "information-model"
	// BasisFields: the element's output mappings read these names. A name may be a
	// field of the message or a variable the instance already holds — the model does
	// not say which, and the descriptor does not guess.
	BasisFields = "fields"
	// BasisUntyped: the model states no shape at all; the message's variables merge
	// into the instance as sent. The descriptor says so rather than inventing one.
	BasisUntyped = "untyped"
)

// Outcome kinds: how a root-scope end event ends an instance.
const (
	OutcomeEnd          = "end"
	OutcomeError        = "error"
	OutcomeEscalation   = "escalation"
	OutcomeMessage      = "message"
	OutcomeSignal       = "signal"
	OutcomeTerminate    = "terminate"
	OutcomeCompensation = "compensation"
)

// Payload is what an entry point expects, as far as the model states it.
type Payload struct {
	Basis  string   `json:"basis"`
	Class  string   `json:"class,omitempty"`
	Fields []string `json:"fields,omitempty"`
}

// EntryPoint is one contract-level way into the published process. Its Name is the
// contract; the element behind it is the binding's business and never appears here.
type EntryPoint struct {
	Name string `json:"name"`
	Kind string `json:"kind"`
	// Starts is true when a message here creates an instance, false when it
	// continues one that is already waiting.
	Starts bool `json:"starts"`
	// CorrelationKey is the FEEL expression a message is correlated by, as authored;
	// empty for a start that states none.
	CorrelationKey string  `json:"correlationKey,omitempty"`
	Payload        Payload `json:"payload"`
}

// Returns are what the publisher promises may come back to a consumer: the error
// and escalation codes that can leave an instance uncaught, and the reply messages
// the publisher chose to name.
type Returns struct {
	Errors      []string `json:"errors"`
	Escalations []string `json:"escalations"`
	Messages    []string `json:"messages"`
}

// Outcome is one terminal outcome an instance can reach, under a name the publisher
// assigned. Several end events may share one outcome; they must share its kind.
type Outcome struct {
	Name string `json:"name"`
	Kind string `json:"kind"`
}

// Contract is the immutable part of a contract version: what a consumer binds to.
// Changing any of it means publishing a new version.
type Contract struct {
	EntryPoints []EntryPoint `json:"entryPoints"`
	Returns     Returns      `json:"returns"`
	Outcomes    []Outcome    `json:"outcomes"`
}

// ElementMap ties one contract name to the element(s) behind it in the bound
// definition. MessageName is the message an entry point's element waits for: an
// internal name, which the contract never shows, and which a refactoring may change.
type ElementMap struct {
	Name        string   `json:"name"`
	ElementIDs  []string `json:"elementIds"`
	MessageName string   `json:"messageName,omitempty"`
}

// Binding is the mutable half of a contract version: which deployed definition
// realizes it now, and through which elements. Rebinding to a newer definition is
// allowed exactly when that definition still realizes the same contract.
type Binding struct {
	ProcessID         string       `json:"processId"`
	DefinitionKey     uint64       `json:"definitionKey"`
	DefinitionVersion int32        `json:"definitionVersion"`
	EntryPoints       []ElementMap `json:"entryPoints"`
	Outcomes          []ElementMap `json:"outcomes"`
}

// Audience is who a grant names: user ids and group ids of this server's directory,
// never display names.
type Audience struct {
	UserIDs  []string `json:"userIds"`
	GroupIDs []string `json:"groupIds"`
}

// Grants are an interface's two rights, kept apart on purpose (ADR-0373): seeing a
// contract never lets anybody drive work into the publishing domain. Public means
// "viewable without a grant" and never implies send.
type Grants struct {
	Public bool     `json:"public"`
	View   Audience `json:"view"`
	Send   Audience `json:"send"`
}

// Principal is the caller a grant is checked against.
type Principal struct {
	UserID   string
	GroupIDs []string
}

func (a Audience) admits(p Principal) bool {
	if p.UserID == "" {
		return false
	}
	for _, id := range a.UserIDs {
		if id == p.UserID {
			return true
		}
	}
	for _, g := range a.GroupIDs {
		for _, m := range p.GroupIDs {
			if m != "" && m == g {
				return true
			}
		}
	}
	return false
}

var (
	idPattern   = regexp.MustCompile(`^[a-z0-9](?:[a-z0-9-]{0,62}[a-z0-9])?$`)
	namePattern = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_.-]{0,127}$`)
)

// ValidID reports whether s is a usable interface id: a lower-case slug such as
// "kreditpruefung", at most 64 characters.
func ValidID(s string) bool { return idPattern.MatchString(s) }

// ValidName reports whether s is a usable contract-level name for an entry point or
// an outcome: a letter, then letters, digits, '_', '.' or '-', at most 128.
func ValidName(s string) bool { return namePattern.MatchString(s) }

// canonical sorts everything a contract holds, so two contracts that say the same
// thing compare equal and are stored in one order.
func (c *Contract) canonical() {
	sort.Slice(c.EntryPoints, func(i, j int) bool { return c.EntryPoints[i].Name < c.EntryPoints[j].Name })
	sort.Slice(c.Outcomes, func(i, j int) bool { return c.Outcomes[i].Name < c.Outcomes[j].Name })
	for _, s := range []*[]string{&c.Returns.Errors, &c.Returns.Escalations, &c.Returns.Messages} {
		if *s == nil {
			*s = []string{}
		}
		sort.Strings(*s)
	}
	if c.EntryPoints == nil {
		c.EntryPoints = []EntryPoint{}
	}
	if c.Outcomes == nil {
		c.Outcomes = []Outcome{}
	}
}

// Equal reports whether two contracts promise the same thing.
func (c Contract) Equal(o Contract) bool {
	c.canonical()
	o.canonical()
	if len(c.EntryPoints) != len(o.EntryPoints) || len(c.Outcomes) != len(o.Outcomes) ||
		!sameStrings(c.Returns.Errors, o.Returns.Errors) ||
		!sameStrings(c.Returns.Escalations, o.Returns.Escalations) ||
		!sameStrings(c.Returns.Messages, o.Returns.Messages) {
		return false
	}
	for i := range c.EntryPoints {
		a, b := c.EntryPoints[i], o.EntryPoints[i]
		if a.Name != b.Name || a.Kind != b.Kind || a.Starts != b.Starts || a.CorrelationKey != b.CorrelationKey ||
			a.Payload.Basis != b.Payload.Basis || a.Payload.Class != b.Payload.Class ||
			!sameStrings(a.Payload.Fields, b.Payload.Fields) {
			return false
		}
	}
	for i := range c.Outcomes {
		if c.Outcomes[i] != o.Outcomes[i] {
			return false
		}
	}
	return true
}

// Differences names, in plain words, what separates two contracts — what a
// publisher reads when a rebinding is refused.
func (c Contract) Differences(o Contract) []string {
	c.canonical()
	o.canonical()
	var out []string
	mine := map[string]EntryPoint{}
	for _, e := range c.EntryPoints {
		mine[e.Name] = e
	}
	theirs := map[string]EntryPoint{}
	for _, e := range o.EntryPoints {
		theirs[e.Name] = e
		a, ok := mine[e.Name]
		if !ok {
			out = append(out, "entry point "+e.Name+" is new")
			continue
		}
		if a.Kind != e.Kind || a.Starts != e.Starts {
			out = append(out, "entry point "+e.Name+" changes its kind")
		}
		if a.CorrelationKey != e.CorrelationKey {
			out = append(out, "entry point "+e.Name+" changes its correlation key")
		}
		if a.Payload.Basis != e.Payload.Basis || a.Payload.Class != e.Payload.Class ||
			!sameStrings(a.Payload.Fields, e.Payload.Fields) {
			out = append(out, "entry point "+e.Name+" changes its payload")
		}
	}
	for _, e := range c.EntryPoints {
		if _, ok := theirs[e.Name]; !ok {
			out = append(out, "entry point "+e.Name+" is gone")
		}
	}
	if !sameStrings(c.Returns.Errors, o.Returns.Errors) {
		out = append(out, "the errors that may come back change")
	}
	if !sameStrings(c.Returns.Escalations, o.Returns.Escalations) {
		out = append(out, "the escalations that may come back change")
	}
	if !sameStrings(c.Returns.Messages, o.Returns.Messages) {
		out = append(out, "the reply messages change")
	}
	if len(c.Outcomes) != len(o.Outcomes) {
		out = append(out, "the terminal outcomes change")
	} else {
		for i := range c.Outcomes {
			if c.Outcomes[i] != o.Outcomes[i] {
				out = append(out, "the terminal outcomes change")
				break
			}
		}
	}
	return out
}

func sameStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func cloneStrings(s []string) []string {
	if s == nil {
		return []string{}
	}
	return append([]string{}, s...)
}

func (c Contract) clone() Contract {
	out := Contract{
		EntryPoints: make([]EntryPoint, len(c.EntryPoints)),
		Outcomes:    append([]Outcome{}, c.Outcomes...),
		Returns: Returns{
			Errors:      cloneStrings(c.Returns.Errors),
			Escalations: cloneStrings(c.Returns.Escalations),
			Messages:    cloneStrings(c.Returns.Messages),
		},
	}
	for i, e := range c.EntryPoints {
		e.Payload.Fields = append([]string(nil), e.Payload.Fields...)
		out.EntryPoints[i] = e
	}
	return out
}

func cloneMaps(ms []ElementMap) []ElementMap {
	out := make([]ElementMap, len(ms))
	for i, m := range ms {
		m.ElementIDs = cloneStrings(m.ElementIDs)
		out[i] = m
	}
	return out
}

func (b Binding) clone() Binding {
	b.EntryPoints = cloneMaps(b.EntryPoints)
	b.Outcomes = cloneMaps(b.Outcomes)
	return b
}

func (a Audience) clone() Audience {
	return Audience{UserIDs: cloneStrings(a.UserIDs), GroupIDs: cloneStrings(a.GroupIDs)}
}

func (g Grants) clone() Grants {
	return Grants{Public: g.Public, View: g.View.clone(), Send: g.Send.clone()}
}
