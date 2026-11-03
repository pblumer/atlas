package api_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/pblumer/atlas/compiler"
	"github.com/pblumer/atlas/connector/entra"
	"github.com/pblumer/atlas/connector/exchange"
	"github.com/pblumer/atlas/connector/lookup"
	"github.com/pblumer/atlas/connector/sharepoint"
	"github.com/pblumer/atlas/worker"
)

// A name field's live check (ADR-draft-name-availability-check), end to end: a stored form names an Entra and a
// SharePoint Worker, a Worker Instance holding both answers through a fake Graph and the
// SharePoint mockup, and a signed-in person typing a name learns whether any of the
// three sources holds it — and nothing else.

const siteOrderForm = `{"components":[
  {"type":"textfield","key":"titel","label":"Titel"},
  {"type":"textfield","key":"url","label":"URL","atlasAvailability":{"version":1,"checks":[
    {"workerType":"entra","worker":"ucc_entra","resource":"groups"},
    {"workerType":"entra","worker":"ucc_entra","resource":"deletedGroups"},
    {"workerType":"exchange","worker":"ucc_exchange","resource":"recipients"},
    {"workerType":"sharepoint","worker":"ucc_sharepoint","resource":"sites","host":"contoso.sharepoint.com"}
  ]}}
]}`

// aliasGraph answers a group collection filtered by mailNickname: one group for an alias
// it holds, none otherwise. Live groups and deleted ones are kept apart, as Graph keeps
// them.
type aliasGraph struct {
	live, deleted map[string]bool
}

func (g aliasGraph) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	held := g.live
	if strings.Contains(r.URL.Path, "/directory/deletedItems/") {
		held = g.deleted
	}
	filter := r.URL.Query().Get("$filter")
	alias := strings.TrimSuffix(strings.TrimPrefix(filter, "mailNickname eq '"), "'")
	w.Header().Set("Content-Type", "application/json")
	if !strings.HasPrefix(filter, "mailNickname eq '") || !held[alias] {
		_, _ = w.Write([]byte(`{"value":[]}`))
		return
	}
	_ = json.NewEncoder(w).Encode(map[string]any{"value": []any{map[string]any{"id": "g-1", "displayName": alias, "mailNickname": alias}}})
}

func TestANameFieldSaysWhetherTheNameIsFree(t *testing.T) {
	st := openLookupStack(t, t.TempDir())
	ts := st.ts
	admin := newClient(t)
	if login(t, admin, ts, "root", "rootpassword") != http.StatusOK {
		t.Fatal("admin login failed")
	}
	anna := twoUsers(t, ts, admin, "anna", "mallory")[0]
	for _, body := range []string{
		`{"name":"ucc_entra","kind":"entra","credentialsRef":"entra-ucc","formLookups":true}`,
		`{"name":"ucc_sharepoint","kind":"sharepoint","credentialsRef":"sp-ucc","formLookups":true}`,
		`{"name":"ucc_exchange","kind":"exchange","credentialsRef":"exo-ucc","formLookups":true}`,
	} {
		if code, b := cReq(t, admin, ts, "POST", "/api/v1/connectors", body); code != http.StatusOK && code != http.StatusCreated {
			t.Fatalf("create a Worker: %d (%s)", code, b)
		}
	}

	// The save holds the block to what this server can check.
	bad := strings.Replace(siteOrderForm, `"host":"contoso.sharepoint.com"`, `"host":"https://contoso.sharepoint.com/sites"`, 1)
	body, _ := json.Marshal(map[string]any{"id": "sp-bestellung", "name": "Team Site", "schema": json.RawMessage(bad)})
	if code, b := cReq(t, admin, ts, "POST", "/api/v1/forms", string(body)); code != http.StatusBadRequest || !strings.Contains(string(b), "SharePoint host") {
		t.Fatalf("saving a block with a URL for a host: %d %s", code, b)
	}
	body, _ = json.Marshal(map[string]any{"id": "sp-bestellung", "name": "Team Site", "schema": json.RawMessage(siteOrderForm)})
	if code, b := cReq(t, admin, ts, "POST", "/api/v1/forms", string(body)); code != http.StatusOK && code != http.StatusCreated {
		t.Fatalf("save the form: %d %s", code, b)
	}

	// The Worker Instance: Entra through a fake Graph, SharePoint through the mockup,
	// where somebody already stored a file in the site dev-huda.
	gs := httptest.NewServer(aliasGraph{live: map[string]bool{"team-x": true}, deleted: map[string]bool{"weg": true}})
	t.Cleanup(gs.Close)
	entraReg := entra.NewRegistry()
	entraReg.Register("ucc_entra", entra.NewGraphClient(graphToken{}, gs.URL+"/v1.0", gs.Client()))
	site := sharepoint.NewMockSites(time.Now).Site("ucc_sharepoint")
	if _, err := site.Do(context.Background(), sharepoint.Request{Operation: "upload-file", Site: "contoso.sharepoint.com:/sites/dev-huda", Path: "a.txt", Content: "x"}); err != nil {
		t.Fatal(err)
	}
	// Exchange Online, holding a shared mailbox whose alias is fp-fin.
	exo := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			CmdletInput struct {
				Parameters map[string]any
			}
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		w.Header().Set("Content-Type", "application/json")
		if f, _ := body.CmdletInput.Parameters["Filter"].(string); strings.Contains(f, "'fp-fin'") {
			_, _ = w.Write([]byte(`{"value":[{"Alias":"fp-fin"}]}`))
			return
		}
		_, _ = w.Write([]byte(`{"value":[]}`))
	}))
	t.Cleanup(exo.Close)
	exReg := exchange.NewRegistry()
	exReg.Register("ucc_exchange", exchange.NewOnlineClient(exchange.OnlineConfig{
		Tokens: graphToken{}, BaseURL: exo.URL, Organization: "contoso.onmicrosoft.com", HTTPClient: exo.Client(),
	}))
	spReg := sharepoint.NewRegistry()
	spReg.Register("ucc_sharepoint", site)

	code, b := cReq(t, admin, ts, "POST", "/api/v1/api-tokens", `{"name":"m365-worker","scope":"worker","expiresInDays":1}`)
	if code != http.StatusOK && code != http.StatusCreated {
		t.Fatalf("mint the worker token: %d (%s)", code, b)
	}
	var minted struct{ Secret, Token string }
	_ = json.Unmarshal(b, &minted)
	token := minted.Secret
	if token == "" {
		token = minted.Token
	}
	wk := worker.New(worker.Options{
		Server: ts.URL, ID: "m365", Token: token, Connectors: []string{"ucc_entra", "ucc_sharepoint", "ucc_exchange"},
		Wait: 2 * time.Second, Retry: 50 * time.Millisecond,
		Handlers: map[string]worker.Exec{},
		Queries: map[string]worker.QueryFunc{
			compiler.EntraJobType: func(ctx context.Context, c string, q lookup.Query) (lookup.Page, error) {
				return entra.RunLookup(ctx, c, q, entraReg)
			},
			compiler.SharePointOpsJobType: func(ctx context.Context, c string, q lookup.Query) (lookup.Page, error) {
				return sharepoint.RunLookup(ctx, c, q, spReg)
			},
			compiler.ExchangeJobType: func(ctx context.Context, c string, q lookup.Query) (lookup.Page, error) {
				return exchange.RunLookup(ctx, c, q, exReg)
			},
		},
	})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { defer close(done); _ = wk.Run(ctx) }()
	t.Cleanup(func() { cancel(); <-done })

	ask := func(c *http.Client, value string) (int, map[string]any) {
		t.Helper()
		code, raw := cReq(t, c, ts, "POST", "/api/v1/forms/sp-bestellung/fields/url/availability", `{"value":"`+value+`"}`)
		var out map[string]any
		_ = json.Unmarshal(raw, &out)
		return code, out
	}
	deadline := time.Now().Add(20 * time.Second)
	for {
		code, out := ask(anna, "frei")
		if code == http.StatusOK {
			if out["available"] != true || out["value"] != "frei" || len(out) != 2 {
				t.Fatalf("a free name: %v", out)
			}
			break
		}
		if out["code"] != "worker_unavailable" || time.Now().After(deadline) {
			t.Fatalf("the worker never answered: %d %v", code, out)
		}
		time.Sleep(20 * time.Millisecond)
	}

	for value, why := range map[string]string{
		"Dev-HuDa": "a site is at /sites/dev-huda",
		"team-x":   "a group has the alias",
		"weg":      "a deleted group still holds the alias",
		"fp-fin":   "a shared mailbox has the alias",
	} {
		if code, out := ask(anna, value); code != http.StatusOK || out["available"] != false {
			t.Errorf("%s (%s): %d %v, want taken", value, why, code, out)
		}
	}
	if code, out := ask(anna, "a/b"); code != http.StatusBadRequest || out["code"] != "invalid_value" {
		t.Errorf("a name with a slash: %d %v", code, out)
	}
	if code, out := ask(anna, "frei"); code != http.StatusOK || out["available"] != true {
		t.Errorf("asking again: %d %v", code, out)
	}

	// Nobody signed in reaches it, and a closed Worker closes it.
	if code, _ := ask(newClient(t), "frei"); code != http.StatusForbidden && code != http.StatusUnauthorized {
		t.Errorf("anonymous: %d, want refused", code)
	}
	id := connectorID(t, admin, ts, "ucc_sharepoint")
	if code, b := cReq(t, admin, ts, "PATCH", "/api/v1/connectors/"+id, `{"formLookups":false}`); code != http.StatusOK {
		t.Fatalf("close the Worker: %d %s", code, b)
	}
	if code, out := ask(anna, "frei"); code != http.StatusForbidden || out["code"] != "forbidden" {
		t.Errorf("a closed Worker: %d %v", code, out)
	}
}
