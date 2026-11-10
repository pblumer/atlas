package api

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/pblumer/atlas/mcp"
)

// The adapter declares itself under one name and the server reads another only if
// the two are spelled alike; a drift would leave --mcp-shop=false refusing nothing.
func TestMCPAdapterHeaderMatchesTheAdapter(t *testing.T) {
	if mcpAdapterHeader != mcp.AdapterHeader {
		t.Errorf("the server reads %q and the adapter declares %q; --mcp-shop=false would refuse nothing",
			mcpAdapterHeader, mcp.AdapterHeader)
	}
}

// Every route the switch names is one the route table serves, so renaming a route
// cannot quietly take it out from under --mcp-shop=false.
func TestMCPShopRoutesAreRoutes(t *testing.T) {
	srv := newServerWithOptions(t)
	served := map[string]bool{}
	for _, r := range srv.apiRoutes() {
		served[r.method+" "+r.pattern] = true
	}
	for route := range mcpShopRoutes {
		if !served[route] {
			t.Errorf("mcpShopRoutes names %s, which the route table does not serve", route)
		}
	}
}

func mcpShopCall(t *testing.T, h http.Handler, method, path, body string, viaMCP bool) (int, string) {
	t.Helper()
	var rd io.Reader
	if body != "" {
		rd = strings.NewReader(body)
	}
	req := httptest.NewRequest(method, path, rd)
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	if viaMCP {
		req.Header.Set(mcpAdapterHeader, "1")
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec.Code, rec.Body.String()
}

// Switched off, a shop route refuses a request that came through MCP and names the
// setting, while the same request from the portal is answered as it always was.
// Switched on — the default — the adapter's request is answered like the portal's.
func TestMCPShopSwitchRefusesAgentsAndKeepsThePortal(t *testing.T) {
	off := newServerWithOptions(t, WithoutMCPShop()).Handler()

	code, body := mcpShopCall(t, off, "GET", "/api/v1/orders", "", true)
	if code != http.StatusForbidden || !strings.Contains(body, "--mcp-shop=false") ||
		!strings.Contains(body, "ATLAS_MCP_SHOP=false") {
		t.Fatalf("orders over MCP, switched off = %d %s, want 403 naming the setting", code, body)
	}
	if code, body = mcpShopCall(t, off, "GET", "/api/v1/orders", "", false); code != http.StatusOK {
		t.Fatalf("orders from the portal, switched off = %d %s, want 200", code, body)
	}
	if code, body = mcpShopCall(t, off, "POST", "/api/v1/orders/o1/lines/i1/return", "", true); code != http.StatusForbidden ||
		!strings.Contains(body, "--mcp-shop=false") {
		t.Fatalf("a return over MCP, switched off = %d %s, want 403 naming the setting", code, body)
	}
	// A route the switch does not name is not touched: an operator's tool keeps working.
	if code, body = mcpShopCall(t, off, "GET", "/api/v1/processes", "", true); code != http.StatusOK {
		t.Fatalf("a process list over MCP, switched off = %d %s, want 200", code, body)
	}

	on := newServerWithOptions(t).Handler()
	if code, body = mcpShopCall(t, on, "GET", "/api/v1/orders", "", true); code != http.StatusOK {
		t.Fatalf("orders over MCP, switched on = %d %s, want 200", code, body)
	}
}

// The action act is the shop's only for its customer half: switched off, a customer
// trigger — or none, which asks as whoever the caller is — is refused over MCP, and
// an operator's or the system's action goes on to the act's own checks.
func TestMCPShopSwitchRefusesOnlyTheCustomersActions(t *testing.T) {
	off := newServerWithOptions(t, WithoutMCPShop()).Handler()
	path := "/api/v1/orders/nope/lines/i1/actions/reset"
	for _, body := range []string{
		`{"commandId":"c1","trigger":"customer"}`,
		`{"commandId":"c1"}`,
	} {
		code, got := mcpShopCall(t, off, "POST", path, body, true)
		if code != http.StatusForbidden || !strings.Contains(got, "--mcp-shop=false") {
			t.Errorf("%s over MCP, switched off = %d %s, want 403 naming the setting", body, code, got)
		}
	}
	for _, trigger := range []string{"operator", "system"} {
		code, got := mcpShopCall(t, off, "POST", path, `{"commandId":"c1","trigger":"`+trigger+`"}`, true)
		if strings.Contains(got, "--mcp-shop") || code != http.StatusNotFound {
			t.Errorf("trigger %s over MCP, switched off = %d %s, want the act's own 404 for no order", trigger, code, got)
		}
	}
	// From the portal the customer's action reaches the act whatever the switch says.
	if code, got := mcpShopCall(t, off, "POST", path, `{"commandId":"c1","trigger":"customer"}`, false); code != http.StatusNotFound {
		t.Errorf("a customer action from the portal, switched off = %d %s, want the act's own 404", code, got)
	}
	// Switched on, an agent's customer action reaches the act too.
	on := newServerWithOptions(t).Handler()
	if code, got := mcpShopCall(t, on, "POST", path, `{"commandId":"c1","trigger":"customer"}`, true); code != http.StatusNotFound {
		t.Errorf("a customer action over MCP, switched on = %d %s, want the act's own 404", code, got)
	}
}

// A request the /mcp transport stamped is an MCP request even if it did not
// declare itself, so the switch holds on the transport's verified marker too.
func TestViaMCPReadsTheTransportsMarker(t *testing.T) {
	s := &Server{internalToken: "the-internal-token"}
	stamped := httptest.NewRequest("GET", "/api/v1/orders", nil)
	stamped.Header.Set(mcpTransportHeader, "the-internal-token")
	if !s.viaMCP(stamped) {
		t.Error("a request the transport stamped was not read as an MCP request")
	}
	if s.viaMCP(httptest.NewRequest("GET", "/api/v1/orders", nil)) {
		t.Error("a portal request was read as an MCP request")
	}
}

// /api/v1/info says whether the shop is offered over MCP, so an agent can tell the
// person before a call is refused.
func TestInfoSaysWhetherTheShopIsOfferedOverMCP(t *testing.T) {
	for _, tc := range []struct {
		opts []Option
		want string
	}{
		{nil, `"mcpShop":true`},
		{[]Option{WithoutMCPShop()}, `"mcpShop":false`},
	} {
		_, body := mcpShopCall(t, newServerWithOptions(t, tc.opts...).Handler(), "GET", "/api/v1/info", "", false)
		if !strings.Contains(body, tc.want) {
			t.Errorf("info = %s, want %s", body, tc.want)
		}
	}
}
