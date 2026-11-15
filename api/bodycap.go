package api

import (
	"net/http"
	"reflect"

	"github.com/pblumer/atlas/workertype/install"
)

// The ceiling under every route.
//
// Each handler bounds the body it reads with the budget that fits what it reads
// (limits.Limits), and a test holds every handler in this tree to doing so
// (TestNoBodyIsReadWithoutABound). That is the rule. This is what stands behind the
// rule on the day it is broken: one bound on every request body, applied before any
// handler runs, so that a handler which reads with no bound of its own reads a finite
// amount. It is deliberately not a substitute for the handler's budget — it is the
// largest body any route may take, which for an ordinary route is orders of magnitude
// more than it should hold (ADR-draft-body-ceiling-under-every-route).

// bodyCeilingSlack is the room left above the largest budget for what surrounds a
// body of that size on the wire — multipart boundaries and part headers — and for the
// handlers that read one byte past their budget to tell "too large" from "exactly as
// large as allowed". Without it the ceiling would answer for them at the boundary.
const bodyCeilingSlack = 1 << 20

// requestBodyCeiling is the largest request body any route of this server may read.
//
// It is the largest byte budget of the installation, whichever that is: a restore's
// archive by default, but every budget is an operator's to move, so none is assumed
// to be the largest. Every int64 field of limits.Limits is a size in bytes; the
// counts and rates are int32 and are not read. Beside them stands the one body bound
// that is a package format's rule and not a budget, an installed Worker Type
// (workertypeinstall.go).
func (s *Server) requestBodyCeiling() int64 {
	top := int64(install.MaxArtifactBytes + install.MaxManifestBytes)
	v := reflect.ValueOf(s.budgets())
	for i := range v.NumField() {
		if f := v.Field(i); f.Kind() == reflect.Int64 && f.Int() > top {
			top = f.Int()
		}
	}
	return top + bodyCeilingSlack
}

// capBodies bounds the body of every request next serves at ceiling bytes. A read
// past it fails with *http.MaxBytesError, as a handler's own http.MaxBytesReader
// would; a body within it, and a request without one, are served as they were.
func capBodies(next http.Handler, ceiling int64) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.Body = http.MaxBytesReader(w, r.Body, ceiling)
		next.ServeHTTP(w, r)
	})
}

// completionBudget is how large the body completing a job or a task may be.
//
// Not an ordinary command's budget: a completion is where a Worker's answer and a
// person's form arrive as variables. Payload is the largest thing a Worker hands back
// in one piece — it is the ceiling a script's output is held to — and Request is the
// envelope around it, so an answer of exactly that size still fits once it is wrapped
// in {"variables": …}. What each variable may weigh is the engine's rule (Variable),
// applied when the completion is processed; this bounds what the server reads to get
// there.
func (s *Server) completionBudget() int64 {
	b := s.budgets()
	return b.Payload + b.Request
}
