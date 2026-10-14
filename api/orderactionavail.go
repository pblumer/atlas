package api

import (
	"errors"
	"net/http"

	"github.com/pblumer/atlas/api/httpapi"
	"github.com/pblumer/atlas/api/order"
	"github.com/pblumer/atlas/compiler"
	"github.com/pblumer/atlas/state"
)

// Which actions a held position offers now (ADR-0429 §2).
//
// The portal draws a button per action the caller may ask for, and greys out the
// ones the position cannot take right now. "Now" is the process's business: a
// per-position strand that is busy provisioning a larger mailbox does not wait for a
// second one, and the BPMN model says so by not sitting on that catch. So the answer
// is read from the strand's live element instances, never from a second copy of the
// rule in the catalogue — and never from the message subscriptions, which a
// terminated catch can leave behind until a later correlation clears them.
//
// The read runs off the loop (ADR-0239): it walks one instance's elements, which is
// bounded by what that instance holds, but it is a walk all the same.

// lineActionView is one action the caller may ask of a position.
type lineActionView struct {
	Key      string            `json:"key"`
	Effect   string            `json:"effect"`
	Triggers []string          `json:"triggers"`
	Labels   map[string]string `json:"labels,omitempty"`
	Form     string            `json:"form,omitempty"`
	// Available is whether asking now would reach the process.
	Available bool `json:"available"`
	// Why says why not, when it would not.
	Why string `json:"why,omitempty"`
}

// lineActionsResp is the whole answer for one position.
type lineActionsResp struct {
	Position string           `json:"position"`
	Actions  []lineActionView `json:"actions"`
}

// handleLineActions lists the actions the caller may ask of one position, and which
// of them the position takes now.
func (s *Server) handleLineActions(w http.ResponseWriter, r *http.Request) {
	p := httpapi.PrincipalFrom(r.Context())
	h, status, msg := s.lineFor(p, r.PathValue("id"), r.PathValue("item"))
	if status != http.StatusOK {
		httpapi.Error(w, status, msg)
		return
	}
	line := h.line
	out := lineActionsResp{Position: h.position, Actions: []lineActionView{}}
	for _, a := range line.ActionList() {
		if !a.AskedOfHeld() || !s.mayTrigger(p, h.ord, a) {
			continue
		}
		out.Actions = append(out.Actions, lineActionView{
			Key: a.Key, Effect: a.Effect, Triggers: a.Triggers, Labels: a.Labels, Form: a.Form,
		})
	}
	if len(out.Actions) == 0 {
		httpapi.JSON(w, http.StatusOK, out)
		return
	}
	why := heldRefusal(line)
	var waiting map[string]bool
	if why == "" && line.PerPosition() {
		var err error
		if waiting, err = s.messagesWaitedFor(line.StrandOf()); err != nil {
			if errors.Is(err, errLoopClosing) {
				httpapi.Error(w, http.StatusServiceUnavailable, err.Error())
				return
			}
			httpapi.Error(w, http.StatusInternalServerError, "read the position's instance: "+err.Error())
			return
		}
		if waiting == nil {
			why = "the instance that carried this position is no longer running"
		}
	}
	for i := range out.Actions {
		v := &out.Actions[i]
		switch {
		case why != "":
			v.Why = why
		case line.PerPosition() && !waiting[line.BindingFor(v.Key).Message]:
			v.Why = "the position's process does not take this action now"
		default:
			v.Available = true
		}
	}
	httpapi.JSON(w, http.StatusOK, out)
}

// heldRefusal is why no action can be asked of the line whatever the process does,
// or "" when it is held and has somewhere to send one.
func heldRefusal(l order.Line) string {
	switch {
	case l.Status != order.StatusDone:
		return "the position is " + string(l.Status) + "; an action is asked only of a held right"
	case l.PerPosition() && l.StrandOf() == 0:
		return "no instance carries this position"
	}
	return ""
}

// messagesWaitedFor is the set of message names the instance waits for now: its live
// message catches, receive tasks, armed message boundaries and armed message event
// subprocesses, each read against the version the instance runs. Nil when the
// instance is no longer running.
func (s *Server) messagesWaitedFor(instKey uint64) (map[string]bool, error) {
	var out map[string]bool
	err := s.readOffLoop(func(rv *state.ReadView, defs defIndex) error {
		if _, ok, err := rv.ActiveProcessInstance(instKey); err != nil || !ok {
			return err
		}
		out = map[string]bool{}
		return rv.ElementInstancesOfProcess(instKey, func(elKey uint64) error {
			ei, ok, err := rv.GetElementInstance(elKey)
			if err != nil || !ok {
				return err
			}
			d, ok := defs[ei.ProcessDefKey]
			if !ok || d.cp == nil {
				return nil
			}
			if name := messageOfElement(d.cp, ei.ElementId); name != "" {
				out[name] = true
			}
			return nil
		})
	})
	return out, err
}

// messageOfElement is the message a live element of this type waits for, or "" when
// it waits for none.
func messageOfElement(cp *compiler.CompiledProcess, id int32) string {
	n := cp.Node(id)
	switch n.Type {
	case compiler.TypeMessageCatchEvent:
		return cp.MessageCatch(n.Detail).MessageName
	case compiler.TypeReceiveTask:
		return cp.ReceiveTask(n.Detail).MessageName
	case compiler.TypeBoundaryEvent:
		if b := cp.BoundaryEvent(n.Detail); b.Kind == compiler.BoundaryMessage {
			return b.MessageName
		}
	case compiler.TypeEventSubProcessStart:
		if e := cp.EventSubProcess(n.EventSub); e.Kind == compiler.BoundaryMessage {
			return e.MessageName
		}
	}
	return ""
}
