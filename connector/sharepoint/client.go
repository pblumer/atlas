// Package sharepoint integrates Microsoft SharePoint as a server-registered Atlas
// worker: a BPMN SharePoint task creates a list item in a
// model-authored site and list through a configured provider via the job path
// (ADR-0141), mirroring how the mail package delegates a send to a registry-managed
// provider (ADR-0079). The integration inherits the job protocol's durability and
// non-blocking properties (ADR-0007):
//
//   - A task creates a job carrying the reserved [compiler.SharePointJobType].
//     The processor never performs the outbound call itself, so it stays
//     allocation-free (invariant I1) and free of any HTTP dependency.
//   - The in-process [Handler] — a job worker — pulls those jobs, creates the item
//     off the processor goroutine and after fsync (invariant I2, never inside
//     applyToState / I4), and completes the job (writing the created item's JSON into
//     the task's result variable), which drives the token onward.
//   - The Graph base and OAuth credential live in a server-side [Registry] keyed by
//     worker name, so a model refers to a provider by name only and never carries
//     an endpoint or a secret (ADR-0036/0041). Only the target (site, list, item
//     fields) is authored in the model, like a REST task's endpoint (ADR-0067).
//
// The transport is Microsoft Graph ([GraphClient]), authenticated with an OAuth2
// bearer token acquired app-only (client-credentials) or via a pre-obtained refresh
// token (ADR-0141), reusing the same grant shapes as the native mail providers
// (ADR-0093).
//
// Delivery is at-least-once: a crash between "Graph created the item" and "job
// completed" replays the create, which — unlike an idempotent mail Message-ID — can
// produce a duplicate list item. De-duplication of created items is a follow-up
// (ADR-0141).
package sharepoint

import (
	"context"

	"github.com/pblumer/atlas/connector/clientreg"
)

// ItemRequest is one list-item creation a SharePoint task performs. Site
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
// interface so the worker is testable without a live server and so a worker name
// binds to exactly one provider. CreateItem returns the created item as decoded JSON
// (the shape Graph returns), which the worker writes into the task's result variable.
type Client interface {
	CreateItem(ctx context.Context, req ItemRequest) (any, error)
}

// Registry resolves a worker name to the [Client] for this kind. Workers are
// registered at the server from managed configuration (endpoint plus credentials), so
// a model refers to a worker by name only (ADR-0036/0041).
//
// It is the shared [clientreg.Registry], which also carries *why* a configured
// worker is missing from it — the difference between "never configured" and
// "configured and broken", which is what a parked token has to be able to say
// (ADR-0158).
type Registry = clientreg.Registry[Client]

// NewRegistry creates an empty worker registry.
func NewRegistry() *Registry { return clientreg.New[Client]() }
