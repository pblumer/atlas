package api_test

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/pblumer/atlas/api"
)

// The shop over the /mcp transport, signed in as a person
// (ADR-0550): the agent orders, finds and withdraws the order
// as that person, and a server started with --mcp-shop=false refuses exactly that to
// the transport while the same person keeps ordering in the portal.

func shopCall(tool string, args map[string]any) string {
	raw, _ := json.Marshal(map[string]any{
		"jsonrpc": "2.0", "id": 1, "method": "tools/call",
		"params": map[string]any{"name": tool, "arguments": args},
	})
	return string(raw)
}

func TestAnAgentOrdersAsThePersonOverTheTransport(t *testing.T) {
	ts := newMCPServer(t)
	root := mcpClient(t)
	if code := login(t, root, ts, "root", "rootpassword"); code != http.StatusOK {
		t.Fatalf("login: got %d", code)
	}
	release := anExcludingCatalogue(t, root, ts)

	_, resp := rpc(t, root, ts, shopCall("atlas_place_order", map[string]any{
		"releaseId": release, "items": []any{"create-supplier"},
	}))
	text, isErr := toolText(t, resp)
	if isErr {
		t.Fatalf("place over MCP = %q", text)
	}
	var placed struct {
		ID      string `json:"id"`
		Orderer string `json:"orderer"`
	}
	if err := json.Unmarshal([]byte(text), &placed); err != nil || placed.ID == "" || placed.Orderer == "" {
		t.Fatalf("place answered %q, want an order naming its orderer", text)
	}

	_, resp = rpc(t, root, ts, shopCall("atlas_list_orders", map[string]any{}))
	if text, isErr = toolText(t, resp); isErr || !strings.Contains(text, placed.ID) {
		t.Fatalf("own orders over MCP = (%q, isErr=%v), want the placed order", text, isErr)
	}
	_, resp = rpc(t, root, ts, shopCall("atlas_get_order", map[string]any{"orderId": placed.ID}))
	if text, isErr = toolText(t, resp); isErr || !strings.Contains(text, "create-supplier") {
		t.Fatalf("order over MCP = (%q, isErr=%v), want its line", text, isErr)
	}
	_, resp = rpc(t, root, ts, shopCall("atlas_shop_tasks", map[string]any{}))
	if text, isErr = toolText(t, resp); isErr {
		t.Fatalf("shop tasks over MCP = %q", text)
	}
	_, resp = rpc(t, root, ts, shopCall("atlas_cancel_order", map[string]any{"orderId": placed.ID, "reason": "a test"}))
	if text, isErr = toolText(t, resp); isErr || !strings.Contains(text, placed.ID) {
		t.Fatalf("withdraw over MCP = (%q, isErr=%v)", text, isErr)
	}
}

func TestTheTransportIsRefusedTheShopWhereTheServerSwitchedItOff(t *testing.T) {
	ts := newMCPServer(t, api.WithoutMCPShop())
	root := mcpClient(t)
	if code := login(t, root, ts, "root", "rootpassword"); code != http.StatusOK {
		t.Fatalf("login: got %d", code)
	}
	release := anExcludingCatalogue(t, root, ts)

	_, resp := rpc(t, root, ts, shopCall("atlas_place_order", map[string]any{
		"releaseId": release, "items": []any{"create-supplier"},
	}))
	if text, isErr := toolText(t, resp); !isErr || !strings.Contains(text, "--mcp-shop=false") {
		t.Fatalf("place over MCP, switched off = (%q, isErr=%v), want a refusal naming the setting", text, isErr)
	}
	_, resp = rpc(t, root, ts, shopCall("atlas_list_orders", map[string]any{}))
	if text, isErr := toolText(t, resp); !isErr || !strings.Contains(text, "ATLAS_MCP_SHOP=false") {
		t.Fatalf("orders over MCP, switched off = (%q, isErr=%v), want a refusal naming the setting", text, isErr)
	}
	// A tool that is not the shop's is untouched by the switch.
	_, resp = rpc(t, root, ts, listProcessesCall)
	if text, isErr := toolText(t, resp); isErr {
		t.Fatalf("process list over MCP, switched off = %q", text)
	}

	// The portal: the same person, the same routes, no adapter in front.
	if code, body := placeOrder(t, root, ts, release, "create-supplier"); code != http.StatusCreated {
		t.Fatalf("place in the portal, switched off = %d %s, want 201", code, body)
	}
	code, body := cReq(t, root, ts, "GET", "/api/v1/orders", "")
	if code != http.StatusOK || strings.Count(string(body), `"orderer"`) != 1 {
		t.Fatalf("orders in the portal, switched off = %d %s, want the one portal order", code, body)
	}
}
