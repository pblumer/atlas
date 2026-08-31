package api

import (
	"net/http/httptest"
	"testing"

	"github.com/pblumer/atlas/mcp"
)

// The marker that says "this API request is a tool call" is spelled in two places:
// here, where it is stamped and checked, and in the adapter that forwards it. The
// two packages cannot share a constant — the dependency runs one way, this one takes
// an http.Handler and does not know the other exists (WithMCP) — so what keeps them
// from drifting is this.
//
// A drift is silent and total: every tool call would go back to being refused by the
// scope it is supposed to be exempt from, and nothing else would change.
func TestMCPTransportHeaderMatchesTheAdapter(t *testing.T) {
	if mcpTransportHeader != mcp.TransportHeader {
		t.Errorf("the boundary stamps %q and the adapter forwards %q; a tool call cannot say what it is",
			mcpTransportHeader, mcp.TransportHeader)
	}
}

// viaMCPTransport is a secret comparison, so its two refusals are worth pinning
// where an HTTP round trip would only show them indirectly: a value that is not the
// token, and — the one that matters — a server that has no token at all, where the
// absent header must not read as a match.
func TestViaMCPTransportRefusesWhatItCannotVerify(t *testing.T) {
	withToken := &Server{internalToken: "the-internal-token"}
	stamped := httptest.NewRequest("GET", "/api/v1/processes", nil)
	stamped.Header.Set(mcpTransportHeader, "the-internal-token")
	if !withToken.viaMCPTransport(stamped) {
		t.Error("a request carrying this server's own token was not recognised")
	}

	forged := httptest.NewRequest("GET", "/api/v1/processes", nil)
	forged.Header.Set(mcpTransportHeader, "a guess")
	if withToken.viaMCPTransport(forged) {
		t.Error("a guessed marker was accepted — the header would be a way past the scope")
	}
	if withToken.viaMCPTransport(httptest.NewRequest("GET", "/api/v1/processes", nil)) {
		t.Error("a request with no marker was accepted")
	}

	// Authentication off: no token to prove anything with, so nothing proves
	// anything — including the empty header every request already carries.
	noToken := &Server{}
	if noToken.viaMCPTransport(httptest.NewRequest("GET", "/api/v1/processes", nil)) {
		t.Error("an unset internal token matched an absent header")
	}

	// And the scope check reads the same way round: the exception is the mcp scope's
	// alone, so a marker on a worker's request buys it nothing.
	if withToken.scopeMayReach(apiScopeWorker, stamped) {
		t.Error("the marker widened the worker scope; it is the mcp scope's exception only")
	}
	if !withToken.scopeMayReach(apiScopeMCP, stamped) {
		t.Error("a marked tool call was refused by the mcp scope")
	}
}
