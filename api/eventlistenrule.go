package api

import (
	"bytes"
	"fmt"
	"log/slog"
	"net/http"
	"slices"
	"strings"

	"github.com/pblumer/atlas/api/httpapi"
	"github.com/pblumer/atlas/compiler"
	"github.com/pblumer/atlas/eventcatalog"
	"github.com/pblumer/atlas/logging"
)

// Who may listen to atlas's own events (ADR-0435 §6).
//
// A model with a signal start or catch on an event atlas emits receives that event's
// payload, and some payloads are somebody's data: atlas.user.requested carries a
// requester's name and address. Deploying such a listener therefore needs an
// administrator. The rule follows the data, not the name: it reads the catalogue's
// personal-data marking, so an event that carries none stays open to every modeler,
// and a signal the catalogue does not know is outside it.
//
// One check, two callers. The deploy doors refuse with it and the Problems panel's
// validation reports it, so the two cannot disagree. It acts at the doors a caller
// comes through — the deploy, the project deploy and the application import — and not
// in deployModel, so the server's own startup deploys, which have no caller, never
// meet it; restart recovery does not deploy afresh at all.

// ruleEventPersonalListener is the validation rule a finding of this check carries.
const ruleEventPersonalListener = "event.personal-listener"

// personalListener is one element that would receive personal data from an event
// atlas emits.
type personalListener struct {
	Element  string              `json:"elementId"`
	Event    string              `json:"event"`
	Role     compiler.SignalRole `json:"role"`
	Personal []string            `json:"personal"`
}

// personalListenersOf lists the elements of the processes that listen to a
// catalogued signal whose payload carries personal data. lookup is the catalogue's
// (eventcatalog.Lookup); a test passes one of its own.
func personalListenersOf(cps []*compiler.CompiledProcess, lookup func(string) (eventcatalog.Entry, bool)) []personalListener {
	var out []personalListener
	for _, cp := range cps {
		for _, sp := range cp.SignalPoints() {
			if !sp.Receives() {
				continue
			}
			e, ok := lookup(strings.TrimSpace(sp.SignalName))
			if !ok || !e.Has(eventcatalog.Signal) {
				continue
			}
			if personal := e.PersonalFields(); len(personal) > 0 {
				out = append(out, personalListener{Element: sp.Element, Event: e.Type, Role: sp.Role, Personal: personal})
			}
		}
	}
	return out
}

// personalListenersBlocking is the rule's one check: the listeners in a model that
// the caller may not deploy, or nil. An administrator may deploy every one, and so
// may anybody on a server without authentication, where there is nobody to keep
// anything from. A model that does not compile is the deploy's own refusal, with the
// compiler's message, and this check has nothing to say about it.
//
// It reads nothing but the bytes and the caller, so it runs off the run loop (I3).
func (s *Server) personalListenersBlocking(r *http.Request, body []byte) []personalListener {
	if s.isAdmin(r) {
		return nil
	}
	deployables, err := compiler.ParseAll(0, 1, bytes.NewReader(body))
	if err != nil {
		return nil
	}
	cps := make([]*compiler.CompiledProcess, 0, len(deployables))
	for _, d := range deployables {
		cps = append(cps, d.Process)
	}
	return personalListenersOf(cps, eventcatalog.Lookup)
}

// callerRoles names the caller's roles, for a finding that says what the caller has
// beside what the deploy needs.
func callerRoles(r *http.Request) []string {
	p := httpapi.PrincipalFrom(r.Context())
	if p == nil {
		return []string{}
	}
	roles := slices.Clone(p.Roles)
	slices.Sort(roles)
	return roles
}

// rolesText renders a caller's roles for a sentence.
func rolesText(roles []string) string {
	if len(roles) == 0 {
		return "none"
	}
	return strings.Join(roles, ", ")
}

// sentence says what the finding is, in full: the element, the event, the personal
// data it would receive, the role the deploy needs and the role the caller has.
func (l personalListener) sentence(roles []string) string {
	return fmt.Sprintf("%s listens to %s, which carries personal data (%s). Deploying a listener on it "+
		"needs the %s role; you have %s.", l.Element, l.Event, strings.Join(l.Personal, ", "), RoleAdmin, rolesText(roles))
}

// personalListenerProblems renders the check's findings for the Problems panel, as
// errors on their elements: a modeler learns of the rule while modelling, not from a
// refused deploy.
func (s *Server) personalListenerProblems(r *http.Request, body []byte) []compiler.Problem {
	found := s.personalListenersBlocking(r, body)
	if len(found) == 0 {
		return nil
	}
	roles := callerRoles(r)
	out := make([]compiler.Problem, 0, len(found))
	for _, l := range found {
		out = append(out, compiler.Problem{
			Element: l.Element, Severity: compiler.SeverityError, Rule: ruleEventPersonalListener,
			Message: l.sentence(roles),
		})
	}
	return out
}

// personalListenerReason renders a refusal as one sentence, for the doors whose answer
// is a reason string rather than a body of their own.
func personalListenerReason(r *http.Request, found []personalListener) string {
	msg := found[0].sentence(callerRoles(r))
	if n := len(found) - 1; n > 0 {
		msg += fmt.Sprintf(" %d more element(s) listen the same way.", n)
	}
	return msg
}

// personalListenerRefusal answers 403 for a refused deploy. The error is the whole
// sentence, because an MCP client keeps nothing of the body but the error.
func personalListenerRefusal(w http.ResponseWriter, r *http.Request, found []personalListener) {
	auditPersonalListenerRefusal(r, found)
	httpapi.JSON(w, http.StatusForbidden, map[string]any{
		"error":     personalListenerReason(r, found),
		"listeners": found,
		"needs":     RoleAdmin,
		"roles":     callerRoles(r),
	})
}

// auditPersonalListenerRefusal leaves the line every authorization refusal leaves.
func auditPersonalListenerRefusal(r *http.Request, found []personalListener) {
	auditRefusal(r, logging.AuthDenied, "refused: a listener on personal data needs the admin role",
		slog.String("method", r.Method), slog.String("path", r.URL.Path),
		slog.String("event", found[0].Event), slog.String("element", found[0].Element))
}
