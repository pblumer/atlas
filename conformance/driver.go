package conformance

import (
	"fmt"
	"strings"
	"time"

	"github.com/pblumer/atlas/compiler"
	"github.com/pblumer/atlas/engine"
	"github.com/pblumer/atlas/model"
	"github.com/pblumer/atlas/state"
)

// A Step drives a parked instance one action forward during the live run: it
// completes a job, delivers a message, or advances the clock past a timer. Steps
// only touch the live run — replay rebuilds from the log the events they produced,
// so replay equivalence (I4) still holds unchanged. Build steps with the Complete,
// Publish, and Wait constructors so the register reads declaratively.
type Step struct {
	kind        stepKind
	element     string        // Complete: BPMN id of the task whose job to complete
	message     string        // Publish: message name
	correlation string        // Publish: correlation key
	after       time.Duration // Wait: clock jump before firing due timers
	vars        []Var         // Complete outputs / Publish payload
}

type stepKind int

const (
	stepComplete stepKind = iota
	stepPublish
	stepWait
	stepFail
	stepResolve
)

// incidentResolveRetries is how many fresh attempts a Resolve step grants the job
// it re-activates — any positive number lets the job be pulled again.
const incidentResolveRetries = 3

// Var is a string-valued variable carried by a step (a job output or a message
// payload). String values are enough for routing conformance models on results;
// richer kinds can follow.
type Var struct {
	Name string
	Text string
}

// Str names a string variable.
func Str(name, text string) Var { return Var{Name: name, Text: text} }

// Start says how a scenario's instance is born. The zero value is an explicit
// CreateInstance (the default for models with a none start event); message- and
// timer-start events instead spring the instance from a trigger, so no
// CreateInstance is ever called for them.
type Start struct {
	kind        startKind
	message     string
	correlation string
	after       time.Duration
}

type startKind int

const (
	startExplicit startKind = iota // CreateInstance(cp.Key)
	startMessage                   // a message start event: PublishMessage births the instance
	startTimer                     // a timer start event: an armed timer births the instance
)

// MessageStart births the instance by publishing a message to a message start
// event, rather than by an explicit CreateInstance.
func MessageStart(name, correlation string) Start {
	return Start{kind: startMessage, message: name, correlation: correlation}
}

// TimerStart arms the definition's timer start event and advances the clock by
// after so the timer comes due and births the instance.
func TimerStart(after time.Duration) Start {
	return Start{kind: startTimer, after: after}
}

// Complete completes the parked job of the task with the given BPMN id, writing
// any vars as outputs — the driver stands in for the human (user task) or worker
// (service task).
func Complete(element string, vars ...Var) Step {
	return Step{kind: stepComplete, element: element, vars: vars}
}

// Publish delivers a message by (name, correlation key) with an optional payload,
// correlating any waiting subscription.
func Publish(name, correlation string, vars ...Var) Step {
	return Step{kind: stepPublish, message: name, correlation: correlation, vars: vars}
}

// Wait advances the clock by d and fires every timer that has come due — the
// deterministic stand-in for wall-clock time passing.
func Wait(d time.Duration) Step { return Step{kind: stepWait, after: d} }

// Fail fails the parked job of the given task with no retries left, raising an
// incident (message becomes the incident message).
func Fail(element, message string) Step {
	return Step{kind: stepFail, element: element, message: message}
}

// Resolve clears the incident on the given task's element with fresh retries,
// re-activating its job. It fails if no incident is present there.
func Resolve(element string) Step { return Step{kind: stepResolve, element: element} }

func (s Step) describe() string {
	switch s.kind {
	case stepComplete:
		return "complete " + s.element
	case stepPublish:
		return fmt.Sprintf("publish %s/%s", s.message, s.correlation)
	case stepWait:
		return "wait " + s.after.String()
	case stepFail:
		return "fail " + s.element
	case stepResolve:
		return "resolve " + s.element
	default:
		return "unknown"
	}
}

// driver applies steps against a live processor. It is not used on replay.
type driver struct {
	p     *engine.Processor
	store *state.Store
	cp    *compiler.CompiledProcess
	clock *driverClock
}

// begin births the instance according to the scenario's Start and runs it to its
// first idle point. For a timer start it arms the timer and jumps the clock so
// TickTimers fires it.
func (d *driver) begin(s Start) error {
	switch s.kind {
	case startExplicit:
		d.p.CreateInstance(d.cp.Key)
		return d.p.RunUntilIdle()
	case startMessage:
		d.p.PublishMessage(s.message, s.correlation)
		return d.p.RunUntilIdle()
	case startTimer:
		// Arm and drain so the start timer is durably indexed before we jump the
		// clock; only then can TickTimers find it due. Firing the timer creates the
		// instance, whose first token movement is a followup, so drain once more to
		// carry the newborn instance to its first idle point.
		d.p.ArmStartTimers(d.cp.Key)
		if err := d.p.RunUntilIdle(); err != nil {
			return err
		}
		d.clock.advance(int64(s.after))
		if err := d.p.TickTimers(); err != nil {
			return err
		}
		return d.p.RunUntilIdle()
	default:
		return fmt.Errorf("unknown start kind %d", s.kind)
	}
}

func (d *driver) apply(s Step) error {
	switch s.kind {
	case stepComplete:
		jobKey, err := d.jobForElement(s.element)
		if err != nil {
			return err
		}
		d.p.CompleteJob(jobKey, toVars(s.vars)...)
		return d.p.RunUntilIdle()
	case stepPublish:
		d.p.PublishMessage(s.message, s.correlation, toVars(s.vars)...)
		return d.p.RunUntilIdle()
	case stepWait:
		d.clock.advance(int64(s.after))
		if err := d.p.TickTimers(); err != nil {
			return err
		}
		return d.p.RunUntilIdle()
	case stepFail:
		jobKey, err := d.jobForElement(s.element)
		if err != nil {
			return err
		}
		d.p.FailJob(jobKey, 0, s.message, 0)
		return d.p.RunUntilIdle()
	case stepResolve:
		return d.resolveIncident(s.element)
	default:
		return fmt.Errorf("unknown step kind %d", s.kind)
	}
}

// resolveIncident clears the incident raised on the given task's element, granting
// fresh retries so its job can be pulled again. It errors if no incident is there,
// making a Resolve step self-verifying: the incident must actually have been raised.
func (d *driver) resolveIncident(bpmnID string) error {
	var elKey uint64
	found := false
	err := d.store.Incidents(func(k uint64, v *model.IncidentValue) error {
		if d.cp.ElementBpmnId(v.ElementId) == bpmnID {
			elKey = k
			found = true
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("scan incidents: %w", err)
	}
	if !found {
		return fmt.Errorf("no incident on element %q to resolve", bpmnID)
	}
	d.p.ResolveIncident(elKey, incidentResolveRetries)
	return d.p.RunUntilIdle()
}

// jobForElement resolves the single activatable job created by the task with the
// given BPMN id. A job carries its element-instance key, not the element index, so
// the lookup is two hops: job → element instance → compiled element id. It errors
// on zero or multiple matches so a mis-authored step fails loudly rather than
// driving the wrong token.
func (d *driver) jobForElement(bpmnID string) (uint64, error) {
	var jobKey uint64
	matches := 0
	err := d.store.AllActivatableJobs(func(k uint64) error {
		jv, ok, err := d.store.GetJob(k)
		if err != nil || !ok {
			return err
		}
		ei, ok, err := d.store.GetElementInstance(jv.ElementInstanceKey)
		if err != nil || !ok {
			return err
		}
		if d.cp.ElementBpmnId(ei.ElementId) == bpmnID {
			jobKey = k
			matches++
		}
		return nil
	})
	if err != nil {
		return 0, fmt.Errorf("scan jobs: %w", err)
	}
	switch {
	case matches == 0:
		return 0, fmt.Errorf("no activatable job for element %q (parked elsewhere?)", bpmnID)
	case matches > 1:
		return 0, fmt.Errorf("%d activatable jobs for element %q; ambiguous", matches, bpmnID)
	}
	return jobKey, nil
}

func toVars(vs []Var) []model.VariableValue {
	if len(vs) == 0 {
		return nil
	}
	out := make([]model.VariableValue, len(vs))
	for i, v := range vs {
		out[i] = model.VariableValue{Name: v.Name, Kind: model.VarString, Text: v.Text}
	}
	return out
}

// stepList renders a scenario's driver for an error message.
func stepList(steps []Step) string {
	if len(steps) == 0 {
		return "(none)"
	}
	parts := make([]string, len(steps))
	for i, s := range steps {
		parts[i] = s.describe()
	}
	return strings.Join(parts, ", ")
}
