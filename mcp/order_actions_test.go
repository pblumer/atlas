package mcp_test

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestAnAgentAsksAnActionAsOperatorOrSystemOnly: the action tool proxies to the action
// act with the trigger it names, and refuses a customer's action before any request
// leaves the adapter — what a person asks of what they hold is theirs to ask
// (ADR-0429, maintainers' decision of 2026-10-01). The server checks the same.
func TestAnAgentAsksAnActionAsOperatorOrSystemOnly(t *testing.T) {
	var (
		calls          int
		gotMethod, got string
		gotBody        map[string]any
	)
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		gotMethod, got = r.Method, r.URL.EscapedPath()
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &gotBody)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"action":"quota-alert","instanceKey":7,"process":"mbx-strand"}`))
	}))
	defer backend.Close()

	args := func(trigger string) map[string]any {
		return map[string]any{
			"orderId": "ord 1", "item": "mailbox", "action": "quota-alert",
			"commandId": "obs-42", "trigger": trigger, "reason": "above nine tenths",
			"variables": map[string]any{"usedGB": 91},
		}
	}

	text, isErr := toolText(t, result(t, callToolAgainstBackend(t, backend, "atlas_ask_order_line_action", args("customer"))))
	if !isErr || !strings.Contains(text, "operator or system") || calls != 0 {
		t.Fatalf("a customer trigger = %q (error %v, %d requests), want refused in the adapter", text, isErr, calls)
	}

	text, isErr = toolText(t, result(t, callToolAgainstBackend(t, backend, "atlas_ask_order_line_action", args("system"))))
	if isErr || !strings.Contains(text, "quota-alert") {
		t.Fatalf("a system trigger = %q (error %v), want the act's answer", text, isErr)
	}
	if gotMethod != http.MethodPost || got != "/api/v1/orders/ord%201/lines/mailbox/actions/quota-alert" {
		t.Fatalf("request = %s %s, want POST to the action act", gotMethod, got)
	}
	if gotBody["trigger"] != "system" || gotBody["commandId"] != "obs-42" || gotBody["reason"] != "above nine tenths" {
		t.Fatalf("body = %v, want the trigger, the command id and the reason", gotBody)
	}
	if vars, _ := gotBody["variables"].(map[string]any); vars["usedGB"] != float64(91) {
		t.Fatalf("variables = %v, want what the action needs", gotBody["variables"])
	}
}

// TestAnAgentReadsWhatAPositionOffers: the read tool proxies to the availability
// route with the order and the position escaped into the path, and changes nothing.
func TestAnAgentReadsWhatAPositionOffers(t *testing.T) {
	var gotMethod, got string
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod, got = r.Method, r.URL.EscapedPath()
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"position":"phone#black","actions":[{"key":"reset","available":false,"why":"not now"}]}`))
	}))
	defer backend.Close()

	text, isErr := toolText(t, result(t, callToolAgainstBackend(t, backend, "atlas_order_line_actions",
		map[string]any{"orderId": "ord 1", "item": "phone#black"})))
	if isErr || !strings.Contains(text, `"why":"not now"`) {
		t.Fatalf("read = %q (error %v), want the availability answer", text, isErr)
	}
	if gotMethod != http.MethodGet || got != "/api/v1/orders/ord%201/lines/phone%23black/actions" {
		t.Fatalf("request = %s %s, want GET of the position's actions", gotMethod, got)
	}
}

// TestTheActionToolsNameWhatTheyAreMissing: each required argument is asked for by
// name, before any request leaves the adapter.
func TestTheActionToolsNameWhatTheyAreMissing(t *testing.T) {
	calls := 0
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls++
		w.WriteHeader(http.StatusTeapot)
	}))
	defer backend.Close()

	full := map[string]any{"orderId": "o", "item": "i", "action": "a", "commandId": "c", "trigger": "operator"}
	for _, missing := range []string{"orderId", "item", "action", "commandId", "trigger"} {
		args := map[string]any{}
		for k, v := range full {
			if k != missing {
				args[k] = v
			}
		}
		text, isErr := toolText(t, result(t, callToolAgainstBackend(t, backend, "atlas_ask_order_line_action", args)))
		if !isErr || !strings.Contains(text, missing) {
			t.Errorf("ask without %s = %q (error %v), want it named", missing, text, isErr)
		}
	}
	for _, missing := range []string{"orderId", "item"} {
		args := map[string]any{"orderId": "o", "item": "i"}
		delete(args, missing)
		text, isErr := toolText(t, result(t, callToolAgainstBackend(t, backend, "atlas_order_line_actions", args)))
		if !isErr || !strings.Contains(text, missing) {
			t.Errorf("read without %s = %q (error %v), want it named", missing, text, isErr)
		}
	}
	if calls != 0 {
		t.Fatalf("%d requests left the adapter for calls that were missing arguments", calls)
	}
}
