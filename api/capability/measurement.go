package capability

import (
	"fmt"
	"time"
)

// Measuring a capability: what the engine recorded, against what the record declared.
//
// Until this existed, every KPI and SLA on a capability was a declaration and the API
// said so in a field, because nothing computed one. What made that answerable is not
// new storage — it is that the numbers were already there, in the per-element counters
// (ADR-0080) and on the instance records themselves. The measurement
// (benchmarks/results/measurement-381825f.md) settled ADR-0305's open question, and the
// decision it produced is ADR-draft-measuring-a-capability: three
// of the four readings cost microseconds at any volume, and the two that walk
// instances are linear, which is affordable over a window and not over all history.
//
// So this surface has two kinds of number in it, and keeping them apart is the whole
// discipline of the file:
//
//   - **Counted** figures come from maintained counters and are *all-time*. They
//     cannot be windowed, because a counter holds a total and not a series.
//   - **Walked** figures come from reading instances and are *windowed*, because a
//     window is what makes them affordable.
//
// Presenting both in one response without saying which is which would be the kind of
// quiet mixing that makes a dashboard wrong in a way nobody can see. Every figure
// below carries its own basis.

// Window is the span a walked reading covers, in Unix seconds.
//
// It is required rather than optional, and that is the measurement's finding rather
// than a preference: an unwindowed reading over 100 000 finished instances cost 1.24
// seconds, and per-phase duration 2.86. An endpoint that offers a number like that
// with no bound is offering a way to make somebody wait without telling them why.
type Window struct {
	From int64 `json:"from"`
	To   int64 `json:"to"`
}

// maxWindowDays bounds how far back a reading may reach. It is a ceiling on cost
// expressed in the units the caller thinks in: the walk is linear in the instances
// the window contains, and a year of a busy definition is the volume the measurement
// showed taking seconds.
//
// It is deliberately generous. The point is not to make long windows impossible —
// somebody measuring an annual SLA needs one — but to make an unbounded reading
// impossible to ask for by accident.
const maxWindowDays = 400

// ParseWindow reads the window a request asked for: a number of days back from now.
//
// Days rather than a from/to pair, because every question this answers is of the form
// "over the last N days" and a pair invites the two failure modes a single number
// does not have — reversed ends, and a window whose age drifts as the clock moves
// while the caller believes it is fixed.
func ParseWindow(days int, now time.Time) (Window, error) {
	if days <= 0 {
		return Window{}, fmt.Errorf(
			"windowDays is required and must be positive: a measurement over all history is not offered, " +
				"because its cost grows with everything that ever ran (see ADR-0305's measurement)")
	}
	if days > maxWindowDays {
		return Window{}, fmt.Errorf("windowDays is at most %d, which is a ceiling on how much reading one request may ask for", maxWindowDays)
	}
	return Window{From: now.AddDate(0, 0, -days).Unix(), To: now.Unix()}, nil
}

// Days is how many days the window spans, for a response that has to say what it
// measured rather than echo what was asked.
func (w Window) Days() int {
	if w.To <= w.From {
		return 0
	}
	return int((w.To - w.From) / 86400)
}

// ElementCount is one element of a process and a number the engine counted for it.
//
// Name is the element's own label, and today it is always empty: the compiler interns
// an element name only for a user task, so an end event's name is read from the model
// and dropped. The field is here rather than omitted because the method asks authors
// to name their end events distinctly and a reader is meant to read those names — so
// the shape a client codes against should be the one that will carry them. Until it
// does, ElementID is the join key and the process XML the API already serves is where
// the label is.
type ElementCount struct {
	ElementID string `json:"elementId"`
	Name      string `json:"name,omitempty"`
	Count     int64  `json:"count"`
}

// CaseStats is what a walk over finished instances in the window found. Every field
// is streaming — a running count, sum, minimum and maximum — so the walk holds one
// instance at a time however many it reads.
//
// There are no percentiles, and their absence is a decision rather than an omission.
// A percentile needs every value at once, so the memory it costs grows with the
// window, which is the one thing a windowed reading is designed not to do. What a
// percentile is usually wanted *for* here is an SLA — "90% within ten minutes" — and
// that is answerable by counting, which [SLAAttainment] does.
type CaseStats struct {
	// Cases is how many finished instances the window held.
	Cases int64 `json:"cases"`
	// MeanSeconds is the arithmetic mean cycle time, 0 when Cases is 0.
	MeanSeconds float64 `json:"meanSeconds"`
	MinSeconds  int64   `json:"minSeconds"`
	MaxSeconds  int64   `json:"maxSeconds"`
}

// SLAAttainment is a declared SLA measured against what ran.
//
// It is reported only for an SLA that carries a machine-readable threshold. A
// threshold written as prose — "within five business days" — is not something Atlas
// can turn into a number without guessing what a business day is here, and a guessed
// SLA is worse than an unmeasured one: it would be a number somebody acts on that
// nobody authored.
type SLAAttainment struct {
	Name   string `json:"name"`
	Metric string `json:"metric"`
	// ThresholdSeconds is what was compared against, echoed so the figure can be
	// checked without fetching the record.
	ThresholdSeconds int64 `json:"thresholdSeconds"`
	// Within and Cases are the counts: Within is how many finished cases in the
	// window met the threshold, Cases how many there were.
	Within int64 `json:"within"`
	Cases  int64 `json:"cases"`
	// Share is Within/Cases, 0 when there were no cases. A rate over no cases is not
	// 100% and not 0%; the reader is meant to look at Cases, which is why it is here.
	Share float64 `json:"share"`
}

// NotMeasured is one declared figure Atlas did not compute, and why.
//
// A declaration nothing measured is the normal case and stays the normal case: the
// point of the record is that somebody can write down what a capability is held to
// before anything can check it. What this type prevents is the reader having to infer
// from an absence whether the number is missing, zero, or uncomputable.
type NotMeasured struct {
	Kind   string `json:"kind"` // "kpi" or "sla"
	Name   string `json:"name"`
	Reason string `json:"reason"`
}

// Reasons a declared figure is not measured.
const (
	// ReasonProseThreshold: the SLA's threshold is free text with no machine-readable
	// counterpart on the record.
	ReasonProseThreshold = "its threshold is prose; add thresholdSeconds to have it measured"
	// ReasonKPIsAreDirections: a KPI is a direction with a goal, and Atlas has no way
	// to know which recorded number it refers to. The cycle time and outcome counts in
	// the same response are what a reader compares it against by hand.
	ReasonKPIsAreDirections = "a KPI names a goal in the business's own words; Atlas reports what it recorded beside it rather than guessing which figure the goal means"
)

// ProcessMeasurement is what one realising process recorded. A capability realised by
// several processes gets one of these each rather than a sum: the processes are
// different implementations of the same capability, and adding their cycle times
// together would produce a number describing nothing.
type ProcessMeasurement struct {
	ProcessID string `json:"processId"`
	Name      string `json:"name,omitempty"`
	// Restricted marks a realisation this caller may not see. Its figures are absent
	// rather than zero, for the reason the gap report has the same distinction: zero
	// is a finding and "you may not look" is not.
	Restricted bool `json:"restricted,omitempty"`
	// Deployed is false for a realisation this server does not currently run, which is
	// a fact about the installation rather than about the capability.
	Deployed bool `json:"deployed"`

	// Outcomes and Cancellations are counted, therefore all-time.
	Outcomes      []ElementCount `json:"outcomes"`
	Cancellations []ElementCount `json:"cancellations,omitempty"`
	// Cases is walked, therefore windowed.
	Cases CaseStats `json:"cases"`
}

// Measurement is the whole answer for one capability.
type Measurement struct {
	Key  string `json:"key"`
	Name string `json:"name"`
	// Window is what the walked figures cover. The counted ones do not.
	Window     Window `json:"window"`
	WindowDays int    `json:"windowDays"`
	// CountedBasis and WalkedBasis say, in the response itself, which figures rest on
	// what. A client rendering this does not have to have read the ADR to label its
	// own axes honestly.
	CountedBasis string `json:"countedBasis"`
	WalkedBasis  string `json:"walkedBasis"`

	Processes []ProcessMeasurement `json:"processes"`
	// SLAs are the declared commitments that could be measured; NotMeasured names
	// every declared figure that could not, with the reason.
	SLAs        []SLAAttainment `json:"slas"`
	NotMeasured []NotMeasured   `json:"notMeasured"`
	// Unrealizable is true when the capability names no realisation this server runs,
	// so there is nothing to measure and that is a fact about the map rather than a
	// failure of the reading.
	Unrealizable bool `json:"unrealizable,omitempty"`
}

// The two basis sentences, as constants so the response and the documentation cannot
// drift apart.
const (
	CountedBasis = "counted from maintained per-element counters: all-time totals for this process definition, not restricted to the window"
	WalkedBasis  = "walked over the finished instances inside the window"
)
