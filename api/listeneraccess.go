package api

import (
	"bytes"
	"net/http"
	"sort"
	"strings"

	"github.com/pblumer/atlas/api/httpapi"
	"github.com/pblumer/atlas/compiler"
	"github.com/pblumer/atlas/eventcatalog"
)

// Who may listen to a catalogued event (ADR-0435 §6).
//
// A signal reaches every deployed listener with every variable of the throwing
// instance (ADR-0088), so whoever may deploy a listener on atlas.user.requested
// receives a requester's name, mail address and reason — the trade-off ADR-0431
// accepted while there was one such event. Deploying a process that waits for a
// catalogued event whose payload carries personal data therefore needs the admin
// role. An event without personal data stays open to every modeler: the rule follows
// the data, not the name, and reads the marking each payload field carries in the
// catalogue.
//
// It is a check at the doors, like ADR-0438's mailbox check: it decides whether a
// definition may be deployed, and a listener already deployed keeps running. The
// deploy gate and the Problems panel's validation call the same function, so the two
// cannot disagree. Signals the catalogue does not hold are outside the rule; nothing
// declares which of their variables are personal.
//
// It reads nothing but the model and the caller, so it runs off the run loop (I3).

// ruleSignalListenerAccess is the validation rule a refused listener is reported
// under.
const ruleSignalListenerAccess = "signal.listener-access"

// listenerRefusal is one element the caller may not deploy: it waits for a catalogued
// event whose payload carries personal data. Naming the event and its fields
// discloses nothing; the catalogue is readable by every modeler (ADR-0435 §7).
type listenerRefusal struct {
	element string
	event   string
	fields  []string
	need    string
	have    string
}

// listenerRefusals lists every element of the compiled processes that the caller may
// not deploy, in element order. An administrator — and every caller on a server
// without authentication — gets none.
func (s *Server) listenerRefusals(r *http.Request, deployables []compiler.Deployable) []listenerRefusal {
	if s.isAdmin(r) {
		return nil
	}
	var out []listenerRefusal
	for _, d := range deployables {
		cp := d.Process
		for _, u := range cp.SignalListeners() {
			entry, ok := eventcatalog.Lookup(u.SignalName)
			if !ok || !entry.Has(eventcatalog.Signal) || entry.ListenerRole() != RoleAdmin {
				continue
			}
			out = append(out, listenerRefusal{
				element: cp.ElementBpmnId(u.ElementId),
				event:   entry.Type,
				fields:  entry.PersonalFields(),
				need:    RoleAdmin,
				have:    callerRoles(r),
			})
		}
	}
	return out
}

// listenerAccessBlockingModel parses a model and answers the first element the
// caller may not deploy, or nil. A model that does not compile is not this check's
// business: the deploy refuses it with the compiler's own message.
func (s *Server) listenerAccessBlockingModel(r *http.Request, body []byte) *listenerRefusal {
	if s.isAdmin(r) {
		return nil
	}
	deployables, err := compiler.ParseAll(1, 1, bytes.NewReader(body))
	if err != nil {
		return nil
	}
	return firstListenerRefusal(s.listenerRefusals(r, deployables))
}

func firstListenerRefusal(refs []listenerRefusal) *listenerRefusal {
	if len(refs) == 0 {
		return nil
	}
	return &refs[0]
}

// listenerProblems reports the same refusals as Problems-panel errors, so a modeler
// learns of the rule while drawing the listener rather than from a refused deploy.
func (s *Server) listenerProblems(r *http.Request, body []byte) []compiler.Problem {
	if s.isAdmin(r) {
		return nil
	}
	deployables, err := compiler.ParseAll(1, 1, bytes.NewReader(body))
	if err != nil {
		return nil // the compiler already reported why
	}
	refs := s.listenerRefusals(r, deployables)
	out := make([]compiler.Problem, 0, len(refs))
	for _, ref := range refs {
		out = append(out, compiler.Problem{
			Element:  ref.element,
			Severity: compiler.SeverityError,
			Rule:     ruleSignalListenerAccess,
			Message:  listenerRefusalSentence(ref),
		})
	}
	return out
}

// callerRoles names the caller's roles for a refusal, so the modeler's view and the
// administrator's view of the same draft explain each other.
func callerRoles(r *http.Request) string {
	p := httpapi.PrincipalFrom(r.Context())
	if p == nil || len(p.Roles) == 0 {
		return "none"
	}
	roles := append([]string(nil), p.Roles...)
	sort.Strings(roles)
	return strings.Join(roles, ", ")
}

// listenerRefusalSentence renders a refusal as the one sentence every door and the
// validation give.
func listenerRefusalSentence(ref listenerRefusal) string {
	return "this element listens to " + ref.event + ", which carries personal data (" +
		strings.Join(ref.fields, ", ") + "). Deploying a listener on it needs the role " + ref.need +
		"; your roles: " + ref.have + ". Ask an administrator to deploy it."
}

// listenerRefusalBody is the answer a single-model deploy gives a refused listener.
func listenerRefusalBody(ref *listenerRefusal) map[string]any {
	return map[string]any{
		"error":          "this model listens to an event you may not receive",
		"elementId":      ref.element,
		"event":          ref.event,
		"personalFields": ref.fields,
		"requiredRole":   ref.need,
		"callerRoles":    ref.have,
		"details":        listenerRefusalSentence(*ref),
	}
}

// listenerRefusalResponse answers 403 with that body.
func listenerRefusalResponse(w http.ResponseWriter, ref *listenerRefusal) {
	httpapi.JSON(w, http.StatusForbidden, listenerRefusalBody(ref))
}

// listenerRefusalReason renders a refusal for the doors whose answer is a reason
// string rather than a body of their own.
func listenerRefusalReason(ref *listenerRefusal) string {
	return ref.element + ": " + listenerRefusalSentence(*ref)
}
