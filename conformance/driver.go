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
)

// Var is a string-valued variable carried by a step (a job output or a message
// payload). String values are enough for routing conformance models on results;
// richer kinds can follow.
type Var struct {
	Name string
	Text string
}

// Str names a string variable.
func Str(name, text string) Var { return Var{Name: name, Text: text} }

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

func (s Step) describe() string {
	switch s.kind {
	case stepComplete:
		return "complete " + s.element
	case stepPublish:
		return fmt.Sprintf("publish %s/%s", s.message, s.correlation)
	case stepWait:
		return "wait " + s.after.String()
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
		return d.p.TickTimers()
	default:
		return fmt.Errorf("unknown step kind %d", s.kind)
	}
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
