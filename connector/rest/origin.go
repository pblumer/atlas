package rest

import (
	"net/url"
	"strings"

	"github.com/pblumer/atlas/compiler"
	"github.com/pblumer/atlas/model"
	"github.com/pblumer/atlas/state"
)

// Origin names the endpoint a REST task calls — scheme, host and port, lower-cased, as
// in "https://api.example.com:8443" — or "" when it cannot say: the url does not
// evaluate, or is not an absolute URL.
//
// It is what the circuit breaker keys a REST task by (ADR-0340, amended 2026-11-11). A
// REST task names no Worker — its endpoint is in the model — so before it had one, every
// REST task on a server shared the one breaker of the REST job type, and one dead host
// held back calls to every other. The path is deliberately not part of it: what goes
// down is a host, and keying by path would let a host's outage trip once per route.
//
// It evaluates only the url, reading only the variables that url's FEEL names, because
// it is asked on the run loop for each candidate of a held type — never in the steady
// state, where no breaker is open and nothing is attributed at all. A literal url costs
// no read.
func Origin(store state.Reader, detail *compiler.ConnectorTaskDetail, ei *model.ElementInstanceValue, elementInstanceKey uint64) string {
	if detail == nil {
		return ""
	}
	if detail.Url.Expr == nil {
		return originOf(detail.Url.Literal)
	}
	names := map[string]bool{}
	for _, n := range detail.Url.Expr.Inputs() {
		names[n] = true
	}
	vars := map[string]model.VariableValue{}
	if len(names) > 0 {
		if err := state.VisibleVariables(store, elementInstanceKey, func(v *model.VariableValue) error {
			if names[v.Name] {
				vars[v.Name] = *v
			}
			return nil
		}); err != nil {
			return ""
		}
	}
	var piKey uint64
	if ei != nil {
		piKey = ei.ProcessInstanceKey
	}
	return originOf(resolveValue(detail.Url, piKey, vars))
}

// originOf reduces an absolute URL to its origin, or "" for anything else.
func originOf(raw string) string {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Scheme == "" || u.Host == "" {
		return ""
	}
	return strings.ToLower(u.Scheme + "://" + u.Host)
}
