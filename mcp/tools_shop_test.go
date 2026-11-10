package mcp_test

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/pblumer/atlas/api"
	"github.com/pblumer/atlas/mcp"
)

// The shop's customer side over MCP (ADR-draft-mcp-shop-customer-side): each tool is
// the one HTTP operation the portal calls, and the server — not this adapter —
// decides whether an agent may make it.

// TestTheShopToolsReachTheirRoutes: every tool proxies to its route with the order and
// the position escaped into the path, sends what the person gave, and declares itself
// as an adapter's request, which is what lets --mcp-shop=false hold.
func TestTheShopToolsReachTheirRoutes(t *testing.T) {
	var (
		gotMethod, gotPath, gotQuery, gotAdapter string
		gotBody                                  map[string]any
	)
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod, gotPath, gotQuery = r.Method, r.URL.EscapedPath(), r.URL.RawQuery
		gotAdapter = r.Header.Get(mcp.AdapterHeader)
		gotBody = nil
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &gotBody)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer backend.Close()

	for _, tc := range []struct {
		tool   string
		args   map[string]any
		method string
		path   string
		query  string
		body   map[string]any
	}{
		{"atlas_shop_catalogs", map[string]any{}, "GET", "/api/v1/shop/catalogs", "", nil},
		{"atlas_shop_catalog", map[string]any{}, "GET", "/api/v1/shop/catalog", "", nil},
		{"atlas_shop_form_query", map[string]any{"releaseId": "rel 1", "item": "laptop", "field": "owner",
			"search": "ann", "filters": map[string]any{"site": "bern"}, "limit": 5},
			"POST", "/api/v1/shop/releases/rel%201/items/laptop/form-data/owner/query", "",
			map[string]any{"search": "ann", "filters": map[string]any{"site": "bern"}, "limit": float64(5)}},
		{"atlas_place_order", map[string]any{"releaseId": "rel_1", "items": []any{"laptop", "vpn"},
			"variants": map[string]any{"laptop": []any{"14in"}}, "config": map[string]any{"vpn": map[string]any{"cc": "4711"}}},
			"POST", "/api/v1/orders", "",
			map[string]any{"releaseId": "rel_1", "items": []any{"laptop", "vpn"},
				"variants": map[string]any{"laptop": []any{"14in"}}, "config": map[string]any{"vpn": map[string]any{"cc": "4711"}}}},
		{"atlas_list_orders", map[string]any{}, "GET", "/api/v1/orders", "", nil},
		{"atlas_get_order", map[string]any{"orderId": "ord 1"}, "GET", "/api/v1/orders/ord%201", "", nil},
		{"atlas_order_ready_lines", map[string]any{"orderId": "ord 1"}, "GET", "/api/v1/orders/ord%201/next", "", nil},
		{"atlas_shop_tasks", map[string]any{}, "GET", "/api/v1/shop/tasks", "", nil},
		{"atlas_order_line_progress", map[string]any{"orderId": "ord 1", "position": "phone#black"},
			"GET", "/api/v1/shop/orders/ord%201/lines/phone%23black/progress", "", nil},
		{"atlas_cancel_order", map[string]any{"orderId": "ord 1", "reason": "not needed"},
			"POST", "/api/v1/orders/ord%201/cancel", "", map[string]any{"reason": "not needed"}},
		{"atlas_cancel_order_line", map[string]any{"orderId": "ord 1", "item": "phone#black"},
			"POST", "/api/v1/orders/ord%201/lines/phone%23black/cancel", "", map[string]any{}},
		{"atlas_amend_order_line", map[string]any{"orderId": "o", "item": "vpn", "config": map[string]any{"cc": "4712"}},
			"POST", "/api/v1/orders/o/lines/vpn/details", "", map[string]any{"config": map[string]any{"cc": "4712"}}},
		{"atlas_return_order_line", map[string]any{"orderId": "o", "item": "vpn"},
			"POST", "/api/v1/orders/o/lines/vpn/return", "", nil},
		{"atlas_change_order_line", map[string]any{"orderId": "o", "item": "mbx", "changeId": "chg-1",
			"variables": map[string]any{"sizeGB": 100}},
			"POST", "/api/v1/orders/o/lines/mbx/change", "",
			map[string]any{"changeId": "chg-1", "variables": map[string]any{"sizeGB": float64(100)}}},
		{"atlas_list_approvals", map[string]any{"before": 77}, "GET", "/api/v1/approvals", "before=77", nil},
		{"atlas_decide_approvals", map[string]any{"approved": false, "reason": "no budget", "taskKeys": []any{11, "12"}},
			"POST", "/api/v1/approvals/decide", "",
			map[string]any{"approved": false, "reason": "no budget", "taskKeys": []any{float64(11), float64(12)}}},
		{"atlas_catalog_releases", map[string]any{"id": "cat_1", "limit": 1},
			"GET", "/api/v1/catalogs/cat_1/releases", "limit=1", nil},
	} {
		text, isErr := toolText(t, result(t, callToolAgainstBackend(t, backend, tc.tool, tc.args)))
		if isErr {
			t.Errorf("%s = %q, want the route's answer", tc.tool, text)
			continue
		}
		if gotMethod != tc.method || gotPath != tc.path || gotQuery != tc.query {
			t.Errorf("%s sent %s %s?%s, want %s %s?%s", tc.tool, gotMethod, gotPath, gotQuery, tc.method, tc.path, tc.query)
		}
		if gotAdapter == "" {
			t.Errorf("%s did not declare itself with %s", tc.tool, mcp.AdapterHeader)
		}
		if tc.body != nil {
			want, _ := json.Marshal(tc.body)
			got, _ := json.Marshal(gotBody)
			if string(want) != string(got) {
				t.Errorf("%s body = %s, want %s", tc.tool, got, want)
			}
		}
	}
}

// TestTheShopToolsNameWhatTheyAreMissing: a required argument is asked for before
// any request leaves the adapter — an order placed with the wrong basket is not
// something to find out about from the server.
func TestTheShopToolsNameWhatTheyAreMissing(t *testing.T) {
	calls := 0
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls++
		w.WriteHeader(http.StatusTeapot)
	}))
	defer backend.Close()

	for _, tc := range []struct {
		tool, missing string
		args          map[string]any
	}{
		{"atlas_place_order", "releaseId", map[string]any{"items": []any{"vpn"}}},
		{"atlas_place_order", "items", map[string]any{"releaseId": "rel_1"}},
		{"atlas_get_order", "orderId", map[string]any{}},
		{"atlas_cancel_order_line", "item", map[string]any{"orderId": "o"}},
		{"atlas_amend_order_line", "config", map[string]any{"orderId": "o", "item": "i"}},
		{"atlas_change_order_line", "changeId", map[string]any{"orderId": "o", "item": "i"}},
		{"atlas_order_line_progress", "position", map[string]any{"orderId": "o"}},
		{"atlas_shop_form_query", "field", map[string]any{"releaseId": "r", "item": "i"}},
		{"atlas_decide_approvals", "approved", map[string]any{"taskKeys": []any{1}}},
		{"atlas_decide_approvals", "taskKeys", map[string]any{"approved": true}},
	} {
		text, isErr := toolText(t, result(t, callToolAgainstBackend(t, backend, tc.tool, tc.args)))
		if !isErr || !strings.Contains(text, tc.missing) {
			t.Errorf("%s without %s = %q (error %v), want it named", tc.tool, tc.missing, text, isErr)
		}
	}
	if calls != 0 {
		t.Fatalf("%d requests left the adapter for calls that were missing arguments", calls)
	}
}

// shopWithOneProduct files a catalogue offering one product nobody must approve and
// publishes it, answering the release an order names.
func shopWithOneProduct(t *testing.T, ts *httptest.Server) string {
	t.Helper()
	text, isErr := callText(t, ts, "atlas_create_catalog", map[string]any{
		"texts": map[string]any{"de": "Test"}, "rank": 1, "languages": []any{"de"},
	})
	if isErr {
		t.Fatalf("create catalogue = %q", text)
	}
	cat := catalogID(t, text)
	if text, isErr := callText(t, ts, "atlas_save_catalog_product", map[string]any{
		"id": "vpn", "homeCatalog": cat, "state": "active", "texts": map[string]any{"de": "VPN"},
		"approval": map[string]any{"kind": "none"}, "provisionProcess": "p", "deprovisionProcess": "d",
	}); isErr {
		t.Fatalf("save = %q", text)
	}
	if text, isErr := callText(t, ts, "atlas_update_catalog", map[string]any{"id": cat, "items": []any{"vpn"}}); isErr {
		t.Fatalf("offer = %q", text)
	}
	if text, isErr := callText(t, ts, "atlas_publish_catalog", map[string]any{"id": cat}); isErr {
		t.Fatalf("publish = %q", text)
	}
	text, isErr = callText(t, ts, "atlas_catalog_releases", map[string]any{"id": cat, "limit": 1})
	var rels []struct {
		ID string `json:"id"`
	}
	if isErr || json.Unmarshal([]byte(text), &rels) != nil || len(rels) != 1 {
		t.Fatalf("newest release = (%q, isErr=%v), want exactly one", text, isErr)
	}
	return rels[0].ID
}

// TestTheShopIsRefusedOverMCPWhereTheServerSwitchedItOff: a server started with
// --mcp-shop=false refuses the shop to an agent and says which setting did it, and
// the portal — the same route without an adapter in front — goes on working.
func TestTheShopIsRefusedOverMCPWhereTheServerSwitchedItOff(t *testing.T) {
	ts := newAtlasWith(t, api.WithoutMCPShop())
	release := shopWithOneProduct(t, ts)

	for _, tc := range []struct {
		tool string
		args map[string]any
	}{
		{"atlas_shop_catalogs", map[string]any{}},
		{"atlas_list_orders", map[string]any{}},
		{"atlas_place_order", map[string]any{"releaseId": release, "items": []any{"vpn"}}},
		{"atlas_return_order_line", map[string]any{"orderId": "o", "item": "vpn"}},
		{"atlas_ask_order_line_action", map[string]any{"orderId": "o", "item": "vpn", "action": "reset",
			"commandId": "c1", "trigger": "customer"}},
	} {
		text, isErr := callText(t, ts, tc.tool, tc.args)
		if !isErr || !strings.Contains(text, "--mcp-shop=false") {
			t.Errorf("%s on a server without the shop over MCP = (%q, isErr=%v), want a refusal naming the setting", tc.tool, text, isErr)
		}
	}
	// An operator's action is not the shop's: it reaches the act, which has no such order.
	text, isErr := callText(t, ts, "atlas_ask_order_line_action", map[string]any{"orderId": "o", "item": "vpn",
		"action": "reset", "commandId": "c1", "trigger": "operator"})
	if !isErr || strings.Contains(text, "--mcp-shop") || !strings.Contains(text, "404") {
		t.Errorf("an operator's action = (%q, isErr=%v), want the act's own 404", text, isErr)
	}
	// The portal — the same route without an adapter in front — is answered, and the
	// refused call ordered nothing. (Placing from the portal needs a signed-in person;
	// api/mcpshop_transport_test.go places one on both sides of the switch.)
	res, err := http.Get(ts.URL + "/api/v1/orders")
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	raw, _ := io.ReadAll(res.Body)
	if res.StatusCode != http.StatusOK || strings.TrimSpace(string(raw)) != "[]" {
		t.Fatalf("the portal's own orders = %d %s, want 200 and nothing placed", res.StatusCode, raw)
	}
	// And /api/v1/info says so, for an agent that asks first.
	if text, isErr := callText(t, ts, "atlas_info", map[string]any{}); isErr || !strings.Contains(text, `"mcpShop":false`) {
		t.Fatalf("info = (%q, isErr=%v), want mcpShop false", text, isErr)
	}
}

// TestACustomersActionReachesTheServerWhenTheShopIsOffered: with the shop offered
// over MCP — the default — the action tool takes trigger customer to the act, whose
// own checks answer (here: there is no such order).
func TestACustomersActionReachesTheServerWhenTheShopIsOffered(t *testing.T) {
	ts := newAtlas(t)
	text, isErr := callText(t, ts, "atlas_ask_order_line_action", map[string]any{"orderId": "nope", "item": "vpn",
		"action": "reset", "commandId": "c1", "trigger": "customer"})
	if !isErr || strings.Contains(text, "--mcp-shop") || !strings.Contains(text, "no order nope") {
		t.Fatalf("a customer's action = (%q, isErr=%v), want the act's own answer", text, isErr)
	}
	if text, isErr := callText(t, ts, "atlas_info", map[string]any{}); isErr || !strings.Contains(text, `"mcpShop":true`) {
		t.Fatalf("info = (%q, isErr=%v), want mcpShop true", text, isErr)
	}
}
