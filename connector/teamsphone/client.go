package teamsphone

import (
	"context"
	"fmt"
	"time"

	"github.com/pblumer/atlas/connector/clientreg"
)

// Client performs one prepared operation against one tenant and answers with the
// operation's result object. [Pwsh] runs the shipped PowerShell; [MockTenant] answers
// from memory.
type Client interface {
	Do(ctx context.Context, req Request) (map[string]any, error)
}

// Registry resolves a worker name to the tenant behind it. It is the worker's own map:
// the engine never holds one for this kind.
type Registry = clientreg.Registry[Client]

// NewRegistry creates an empty registry.
func NewRegistry() *Registry { return clientreg.New[Client]() }

// dateLayout is the one date form a holiday is authored in.
const dateLayout = "2006-01-02"

// isoDate reads a YYYY-MM-DD date. A date with a time is refused rather than cut: a
// holiday is whole days, and a time would leave the reader guessing which day it meant.
func isoDate(s string) (time.Time, error) {
	t, err := time.Parse(dateLayout, s)
	if err != nil {
		return time.Time{}, fmt.Errorf("%q is not a date of the form YYYY-MM-DD", s)
	}
	return t, nil
}
