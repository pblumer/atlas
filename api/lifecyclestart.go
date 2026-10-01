package api

import (
	"errors"
	"fmt"

	"github.com/pblumer/atlas/api/catalog"
	"github.com/pblumer/atlas/api/order"
	"github.com/pblumer/atlas/engine"
	"github.com/pblumer/atlas/model"
)

// Starting one operation of a product (ADR-0425).
//
// A product binds either two processes, each started by hand, or one lifecycle
// process whose operations are message start events. Everything that starts an
// operation for a position — the start act the fulfilment and approval models call,
// a return, a recertification, a reconciliation — comes through startBinding, so
// the two forms are one question to every caller: where does this operation start.

// orderTriggerSource is the source the catalogue layer's own triggers are recorded
// under. The directed-trigger route never accepts it from a caller (see
// handleTrigger), so a receipt under it was written by this server.
const orderTriggerSource = "atlas:order"

// errTriggerRefused is a trigger the engine answered without starting anything.
// The message says why in words for the operator who reads the failed job.
type errTriggerRefused struct{ msg string }

func (e errTriggerRefused) Error() string { return e.msg }

// startBinding starts the operation b names and returns the instance that answers.
// A triggered binding goes through the engine's directed trigger with triggerID,
// so a repeated start of the same attempt answers with the first instance; a
// binding started by hand goes through the ordinary create and is refused where
// ADR-0426 refuses it.
func (s *Server) startBinding(b catalog.Binding, triggerID string, vars []model.VariableValue) (uint64, error) {
	if !b.Bound() {
		return 0, errors.New("no process is bound for this operation")
	}
	var (
		key       uint64
		found     bool
		ambiguous string
	)
	s.do(func() {
		if d := s.latestDeploymentOf(b.Process); d != nil {
			key, found = d.Key, true
			if !b.Triggered() {
				ambiguous = untriggeredStartRefusal(d.cp)
			}
		}
	})
	if !found {
		return 0, fmt.Errorf("no deployed process with id %s", b.Process)
	}
	if ambiguous != "" {
		return 0, errors.New(ambiguous)
	}
	if err := s.encipherStartVars(key, vars); err != nil {
		return 0, err
	}
	if !b.Triggered() {
		var instKey uint64
		s.do(func() { s.proc.CreateInstanceReporting(key, &instKey, vars...) })
		if err := s.drive(); err != nil {
			return 0, err
		}
		return instKey, nil
	}
	res, err := s.fireTrigger(key, b.Message, orderTriggerSource, triggerID, vars)
	if err != nil {
		return 0, err
	}
	if msg := triggerRefusal(res, b.Process, b.Message); msg != "" {
		return 0, errTriggerRefused{msg}
	}
	return res.InstanceKey, nil
}

// fireTrigger enqueues one directed trigger and drives it to a durable answer.
func (s *Server) fireTrigger(defKey uint64, message, source, triggerID string, vars []model.VariableValue) (engine.TriggerResult, error) {
	var res engine.TriggerResult
	s.do(func() { s.proc.TriggerStart(defKey, message, source, triggerID, &res, vars...) })
	if err := s.drive(); err != nil {
		return res, err
	}
	return res, nil
}

// triggerRefusal is why a trigger started nothing, or "" when it answered with an
// instance — created now or by an earlier delivery of the same trigger.
func triggerRefusal(res engine.TriggerResult, process, message string) string {
	switch res.Outcome {
	case engine.TriggerCreated, engine.TriggerReplayed:
		return ""
	case engine.TriggerSingletonTaken:
		return "process " + process + " is already running for this key at " + message +
			" (a singleton start); wait for it to finish"
	case engine.TriggerInactive:
		return "process " + process + " is deactivated"
	case engine.TriggerNoSuchStart:
		return "process " + process + " has no message start event " + message +
			" in its newest deployed version"
	}
	return "the trigger was not processed"
}

// positionTriggerID names one attempt at one operation of one position: the same
// attempt delivered twice is one trigger, and a deliberate retry — a return asked
// for again after it failed — is the next attempt and a new one.
func positionTriggerID(orderID, position, op string, attempt int) string {
	return order.AttemptID(orderID, position, op, attempt)
}

// Delivering a later operation of a per-position lifecycle
// (ADR-0428).
//
// A per-position line's provisioning started the instance that carries the right
// for as long as it is held; its return, and any change, is a message that instance
// waits for. It is delivered to that instance and to nothing else, and the answer
// says whether it arrived. Where the line has no such instance any more — it was
// placed before its product was converted, or the instance was cancelled — the
// operation starts at the process's start event for it instead, so every held right
// stays returnable.

// positionCorrelationKey is the key a per-position lifecycle process keys its catch
// events on: the order and the position, which together name exactly one right
// (ADR-0384).
func positionCorrelationKey(orderID, position string) string { return orderID + "/" + position }

// deliverOrStart delivers operation b to the strand instance when there is one and
// it is still running, and starts b's start event when there is none. It returns the
// instance that took the operation.
func (s *Server) deliverOrStart(strand uint64, b catalog.Binding, correlationKey, triggerID string, vars []model.VariableValue) (uint64, error) {
	if strand != 0 && b.Triggered() {
		key, gone, err := s.deliverToStrand(strand, b, correlationKey, triggerID, vars)
		if !gone {
			return key, err
		}
	}
	return s.startBinding(b, triggerID, vars)
}

// deliverChange delivers a change or a service to the strand and to nothing else:
// neither has a start event to fall back to, so a strand that is gone is a refusal.
func (s *Server) deliverChange(strand uint64, b catalog.Binding, correlationKey, triggerID string, vars []model.VariableValue) (uint64, error) {
	key, gone, err := s.deliverToStrand(strand, b, correlationKey, triggerID, vars)
	if gone {
		return 0, errTriggerRefused{fmt.Sprintf("the instance %d that carried this position is "+
			"no longer running; there is nothing to ask", strand)}
	}
	return key, err
}

// deliverToStrand hands b's message to the strand instance and answers with the
// instance that took it, or says the strand is gone.
func (s *Server) deliverToStrand(strand uint64, b catalog.Binding, correlationKey, triggerID string, vars []model.VariableValue) (uint64, bool, error) {
	var res engine.DeliveryResult
	s.do(func() {
		s.proc.DeliverMessage(strand, b.Message, correlationKey, orderTriggerSource, triggerID, &res, vars...)
	})
	if err := s.drive(); err != nil {
		return 0, false, err
	}
	switch res.Outcome {
	case engine.DeliveryDelivered, engine.DeliveryReplayed:
		return res.InstanceKey, false, nil
	case engine.DeliveryNotWaiting:
		return 0, false, errTriggerRefused{fmt.Sprintf("the instance %d that carries this position "+
			"is running but does not wait for %s now; nothing was delivered — retry once "+
			"it reaches a step that listens for it", strand, b.Message)}
	case engine.DeliveryGone:
		return 0, true, nil
	}
	return 0, false, errors.New("the delivery was not processed")
}
