package api_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// sourceRow is one message source as the listing answers it.
type sourceRow struct {
	MessageName string   `json:"messageName"`
	SourceKind  string   `json:"sourceKind"`
	Enabled     bool     `json:"enabled"`
	ProductID   string   `json:"productId"`
	ProductName string   `json:"productName"`
	Action      string   `json:"action"`
	Effect      string   `json:"effect"`
	Triggers    []string `json:"triggers"`
	ProcessID   string   `json:"processId"`
	ElementID   string   `json:"elementId"`
	Element     string   `json:"element"`
}

func sourcesFor(t *testing.T, c *http.Client, ts *httptest.Server) []sourceRow {
	t.Helper()
	code, body := cReq(t, c, ts, "GET", "/api/v1/message-sources", "")
	var rows []sourceRow
	if code != http.StatusOK || json.Unmarshal(body, &rows) != nil {
		t.Fatalf("message sources: %d (%s)", code, body)
	}
	return rows
}

// TestAMessageNameIsListedWithWhereItComesFrom: beside the Worker events, the listing
// names each product action's message — its product, key, effect and triggers, and the
// process the product binds it to — and every message a deployed process waits for, at
// a start or a catch. A product action is a maintainer's picture of the catalogue, so a
// modeller who maintains no catalogue is not shown it; the processes are deployed for
// every modeller to see.
func TestAMessageNameIsListedWithWhereItComesFrom(t *testing.T) {
	ts, admin, _, _, _, _ := aToolHeldForRita(t, `["customer","operator"]`)

	byKind := func(rows []sourceRow) map[string]map[string]sourceRow {
		out := map[string]map[string]sourceRow{}
		for _, r := range rows {
			if out[r.SourceKind] == nil {
				out[r.SourceKind] = map[string]sourceRow{}
			}
			key := r.MessageName + "@" + r.ElementID
			out[r.SourceKind][key] = r
		}
		return out
	}
	got := byKind(sourcesFor(t, admin, ts))

	reset, ok := got["product-action"]["tool.reset@"]
	if !ok || reset.ProductID != "tool" || reset.ProductName != "Werkzeug" || reset.Action != "password-reset" ||
		reset.Effect != "service" || len(reset.Triggers) != 1 || reset.Triggers[0] != "customer" ||
		reset.ProcessID != "tool-strand" || !reset.Enabled {
		t.Fatalf("the reset as a product action = %+v (present %v)", reset, ok)
	}
	if _, ok := got["product-action"]["tool.provision@"]; !ok {
		t.Fatal("the provision's message is not listed among the product's actions")
	}

	start, ok := got["process"]["tool.provision@Provision"]
	if !ok || start.ProcessID != "tool-strand" || start.Element != "start" || !start.Enabled {
		t.Fatalf("the strand's message start = %+v (present %v)", start, ok)
	}
	catch, ok := got["process"]["tool.reset@AskReset"]
	if !ok || catch.Element != "catch" {
		t.Fatalf("the strand's catch for the reset = %+v (present %v)", catch, ok)
	}

	createUserWithRoles(t, admin, ts.URL, "mona", `["modeler","user"]`)
	mona := signInAs(t, ts.URL, "mona", "a-password-that-is-long")
	theirs := byKind(sourcesFor(t, mona, ts))
	if n := len(theirs["product-action"]); n != 0 {
		t.Fatalf("a modeller who maintains no catalogue was shown %d product action(s)", n)
	}
	if _, ok := theirs["process"]["tool.reset@AskReset"]; !ok {
		t.Fatal("a modeller was not shown where a deployed process waits")
	}
}
