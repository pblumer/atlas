package exchange

import (
	"context"

	"github.com/pblumer/atlas/connector/clientreg"
	"github.com/pblumer/atlas/connector/exchange/catalog"
)

// Backend runs one Exchange cmdlet against one configured organization. There are two:
// [OnlineClient] speaks Exchange Online's REST endpoint, [OnPremClient] Exchange
// Server's Remote PowerShell. Everything above this interface — the operation table,
// the checks, the result shapes — is shared, which is what lets one model run against
// either.
type Backend interface {
	// Target says which kind of organization this is, so the operation table can pick
	// the cmdlet that means the same thing there.
	Target() catalog.Target
	// Invoke runs the cmdlet and returns the objects it wrote. limit > 0 lets the
	// backend stop once it holds more than limit objects; the caller decides what that
	// means. Zero reads everything.
	Invoke(ctx context.Context, inv catalog.Invocation, limit int) ([]any, error)
}

// Registry resolves a Worker name to the organization behind it. It is the worker's own
// map: the engine never builds one for this kind.
type Registry = clientreg.Registry[Backend]

// NewRegistry creates an empty registry.
func NewRegistry() *Registry { return clientreg.New[Backend]() }
