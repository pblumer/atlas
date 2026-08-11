// Package sharepoint integrates Microsoft SharePoint as a server-registered Atlas
// connector: a BPMN SharePoint connector task creates a list item in a
// model-authored site and list through a configured provider via the job path
// (ADR-0105), mirroring how the mail package delegates a send to a registry-managed
// provider (ADR-0079). The integration inherits the job protocol's durability and
// non-blocking properties (ADR-0007):
//
//   - A connector task creates a job carrying the reserved [compiler.SharePointJobType].
//     The processor never performs the outbound call itself, so it stays
//     allocation-free (invariant I1) and free of any HTTP dependency.
//   - The in-process [Handler] — a job worker — pulls those jobs, creates the item
//     off the processor goroutine and after fsync (invariant I2, never inside
//     applyToState / I4), and completes the job (writing the created item's JSON into
//     the task's result variable), which drives the token onward.
//   - The Graph base and OAuth credential live in a server-side [Registry] keyed by
//     connector name, so a model refers to a provider by name only and never carries
//     an endpoint or a secret (ADR-0036/0041). Only the target (site, list, item
//     fields) is authored in the model, like a REST task's endpoint (ADR-0067).
//
// The transport is Microsoft Graph ([GraphClient]), authenticated with an OAuth2
// bearer token acquired app-only (client-credentials) or via a pre-obtained refresh
// token (ADR-0105), reusing the same grant shapes as the native mail providers
// (ADR-0093).
//
// Delivery is at-least-once: a crash between "Graph created the item" and "job
// completed" replays the create, which — unlike an idempotent mail Message-ID — can
// produce a duplicate list item. De-duplication of created items is a follow-up
// (ADR-0105).
package sharepoint

import "context"

// ItemRequest is one list-item creation a SharePoint connector task performs. Site
// and List address the target list (a Graph site id and a list name or id); Fields
// are the item's column values, already resolved from the model's literal-or-FEEL
// values by the worker. RequestID is the job key, carried for tracing and any future
// idempotency support.
type ItemRequest struct {
	Site      string
	List      string
	Fields    map[string]string
	RequestID string
}

// Client creates a list item through one configured SharePoint provider. It is an
// interface so the worker is testable without a live server and so a connector name
// binds to exactly one provider. CreateItem returns the created item as decoded JSON
// (the shape Graph returns), which the worker writes into the task's result variable.
type Client interface {
	CreateItem(ctx context.Context, req ItemRequest) (any, error)
}

// Registry resolves a connector name to the [Client] for its SharePoint provider.
// Connectors are registered at the server from managed configuration (Graph base plus
// credentials), so a model refers to a connector by name only (ADR-0036/0041). A
// Registry is read-only once populated and safe for concurrent use by workers.
type Registry struct {
	clients map[string]Client
}

// NewRegistry creates an empty connector registry.
func NewRegistry() *Registry { return &Registry{clients: map[string]Client{}} }

// Register binds a connector name to its client. Registering the same name again
// replaces the earlier binding (last write wins), so reconfiguration is simple.
func (r *Registry) Register(name string, c Client) { r.clients[name] = c }

// Client returns the client bound to name, or nil and false if none is registered.
func (r *Registry) Client(name string) (Client, bool) {
	c, ok := r.clients[name]
	return c, ok
}

// Replace swaps the whole set of registered connectors at once, so a server can
// rebuild the registry from managed configuration after a change (ADR-0041). The
// caller must serialize Replace with the workers that read the registry — the Atlas
// server does both on its run-loop goroutine — so no lock is needed. A nil map clears
// the registry.
func (r *Registry) Replace(clients map[string]Client) {
	if clients == nil {
		clients = map[string]Client{}
	}
	r.clients = clients
}
