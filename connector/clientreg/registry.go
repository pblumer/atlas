// Package clientreg is the connector client registry every connector kind shares: a
// name → client map that a worker resolves a model's connector reference through,
// plus the part that is not a map — *why* a connector an operator did configure is
// missing from it.
//
// A connector that cannot be built (an endpoint naming no host, a credential bundle
// that will not parse, a provider nobody implements) is deliberately left out of the
// registry so its tasks park instead of running wrongly (ADR-0093). The reason used
// to be discarded at the point of skipping, and the parked token then reported "no
// connector registered as X" — which reads as *you never configured it* and sends an
// operator looking for a connector that is sitting right there, disabled or broken.
// So the registry carries the skipped ones too, with their reason, and the incident
// can say what is actually wrong (ADR-0152).
//
// A Registry is read-only once populated and safe for concurrent use by workers; the
// server serializes Replace with the workers that read it on its run-loop goroutine.
package clientreg

import "fmt"

// Registry resolves a connector name to the client of its kind, and remembers the
// configured connectors of that kind it could not build.
type Registry[T any] struct {
	clients  map[string]T
	problems map[string]string
}

// New creates an empty registry.
func New[T any]() *Registry[T] {
	return &Registry[T]{clients: map[string]T{}, problems: map[string]string{}}
}

// Register binds a connector name to its client. Registering the same name again
// replaces the earlier binding (last write wins), so reconfiguration is simple, and
// clears any problem recorded under that name — it is usable now.
func (r *Registry[T]) Register(name string, c T) {
	r.clients[name] = c
	delete(r.problems, name)
}

// Client returns the client bound to name, or the zero value and false if none is.
func (r *Registry[T]) Client(name string) (T, bool) {
	c, ok := r.clients[name]
	return c, ok
}

// Problem returns why a *configured* connector of this kind is not usable, and
// whether one was recorded at all. The distinction is the whole point: no problem and
// no client means nobody ever configured that name; a problem means somebody did and
// it is broken, disabled, or of another kind — which is a different thing for an
// operator to go and do.
func (r *Registry[T]) Problem(name string) (string, bool) {
	why, ok := r.problems[name]
	return why, ok
}

// Replace swaps the whole set of registered connectors at once, so a server can
// rebuild the registry from managed configuration after a change (ADR-0041). A nil
// map clears the registry. It records no problems; ReplaceWith is the form that
// carries them.
func (r *Registry[T]) Replace(clients map[string]T) { r.ReplaceWith(clients, nil) }

// ReplaceWith swaps the registered connectors *and* the reasons the configured ones
// that are missing could not be built, in one step — the two always come from the
// same rebuild pass, and letting them be set separately would allow a registry whose
// clients and explanations disagree.
func (r *Registry[T]) ReplaceWith(clients map[string]T, problems map[string]string) {
	if clients == nil {
		clients = map[string]T{}
	}
	if problems == nil {
		problems = map[string]string{}
	}
	r.clients, r.problems = clients, problems
}

// Problems returns every recorded reason, keyed by connector name. The caller must
// not mutate the result. It backs the operator view that lists a configured
// connector's health beside it.
func (r *Registry[T]) Problems() map[string]string { return r.problems }

// Unresolved is the error a worker returns when a model's connector reference does
// not resolve — the message an operator reads on the parked token. It distinguishes
// the two cases the registry can tell apart: a name nobody configured, and a name
// somebody configured that cannot be used and why. kind prefixes the message with the
// connector kind, as every connector worker's errors do.
func (r *Registry[T]) Unresolved(kind, name string) error {
	if why, ok := r.Problem(name); ok {
		return fmt.Errorf("%s: connector %q is configured but not usable: %s", kind, name, why)
	}
	return fmt.Errorf("%s: no connector registered as %q", kind, name)
}
