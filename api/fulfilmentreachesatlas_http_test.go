package api_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/pblumer/atlas/api"
	"github.com/pblumer/atlas/engine"
	"github.com/pblumer/atlas/state"
	"github.com/pblumer/atlas/wal"
)

// The orchestration does its work by calling Atlas, so it has to reach Atlas.
//
// Every step the fulfilment model takes is an HTTP call to this server's own API:
// it asks which positions may start and it starts them. For four releases those
// calls were authored as plain service tasks of a job type named "rest", carrying
// their target and method in task headers — and a leased job carries no task
// headers, and nothing serves a job type called "rest". The tokens parked. No
// retry was spent, no incident was raised, and on the installation that reported
// it fourteen orders stood at "Wartet" with nothing anywhere to say why.
//
// This is the end-to-end proof that they are calls now: a real server, a real
// order, and an orchestration that gets past its first step without anybody
// leasing anything by hand.

// TestTheOrchestrationReachesAtlasAndMovesOn.
func TestTheOrchestrationReachesAtlasAndMovesOn(t *testing.T) {
	ts, admin := aServerThatCanCallItself(t)

	// A catalogue with one service, published, and an order against it.
	code, body := cReq(t, admin, ts, "POST", "/api/v1/catalogs",
		`{"rank":1,"languages":["de"],"texts":{"de":"Arbeitsplatz"}}`)
	if code != http.StatusCreated {
		t.Fatalf("create catalogue: %d (%s)", code, body)
	}
	cat := idOf(t, body)
	if code, b := cReq(t, admin, ts, "POST", "/api/v1/catalog-products",
		`{"id":"vpn","homeCatalog":"`+cat+`","state":"active","texts":{"de":"VPN"},`+
			`"approval":{"kind":"none"},"provisionProcess":"prov","deprovisionProcess":"deprov"}`,
	); code != http.StatusOK {
		t.Fatalf("save the product: %d (%s)", code, b)
	}
	if code, b := cReq(t, admin, ts, "PATCH", "/api/v1/catalogs/"+cat,
		`{"items":["vpn"]}`); code != http.StatusOK {
		t.Fatalf("offer it: %d (%s)", code, b)
	}
	code, body = cReq(t, admin, ts, "POST", "/api/v1/catalogs/"+cat+"/releases", "")
	if code != http.StatusCreated {
		t.Fatalf("publish: %d (%s)", code, body)
	}
	rel := idOf(t, body)
	code, body = cReq(t, admin, ts, "POST", "/api/v1/orders",
		`{"releaseId":"`+rel+`","items":["vpn"]}`)
	if code != http.StatusCreated {
		t.Fatalf("place the order: %d (%s)", code, body)
	}
	ord := idOf(t, body)

	// The orchestration's first step is the call. It is done when the answer is on
	// the instance: `bereit` is what GET /orders/{id}/next writes back, and nothing
	// else in the model produces it.
	//
	// Polled rather than awaited, because the job runs on the engine's own loop and
	// the call is a real round trip to this server. A failure here is the whole
	// point of the test, so the wait is generous and what it saw is printed.
	deadline := time.Now().Add(20 * time.Second)
	for {
		vars, state := orchestrationOf(t, admin, ts, ord)
		if _, answered := vars["bereit"]; answered {
			return // the call went out, came back, and its answer is on the instance
		}
		if time.Now().After(deadline) {
			t.Fatalf("the orchestration never got past its first call: state=%q variables=%v\n"+
				"That step is GET /api/v1/orders/{id}/next against this server's own API. "+
				"Nothing else in the model writes `bereit`, so an instance without it is an "+
				"instance whose first request never landed.", state, variableNames(vars))
		}
		time.Sleep(200 * time.Millisecond)
	}
}

// aServerThatCanCallItself is an auth-enforcing server that knows its own address
// and holds a token for it.
//
// The listener comes first on purpose: the server has to be told where it is
// before it starts serving, and httptest's started server only reveals that
// afterwards. This is also exactly the ordering the real binary has — it binds,
// then hands the address to the engine and to its children.
func aServerThatCanCallItself(t *testing.T) (*httptest.Server, *http.Client) {
	t.Helper()
	t.Setenv("ATLAS_ADMIN_USERNAME", "root")
	t.Setenv("ATLAS_ADMIN_PASSWORD", "rootpassword")

	ts := httptest.NewUnstartedServer(nil)
	self := "http://" + ts.Listener.Addr().String()

	dir := t.TempDir()
	log, err := wal.Open(wal.Options{Dir: filepath.Join(dir, "wal")})
	if err != nil {
		t.Fatalf("wal.Open: %v", err)
	}
	store, err := state.Open(filepath.Join(dir, "state"))
	if err != nil {
		t.Fatalf("state.Open: %v", err)
	}
	proc := engine.New(1, log, store, nil)
	if err := proc.Recover(); err != nil {
		t.Fatalf("Recover: %v", err)
	}
	srv, err := api.New(proc, store, dir,
		api.WithAuth(), api.WithSystemProcesses(), api.WithSelfURL(self))
	if err != nil {
		t.Fatalf("api.New: %v", err)
	}
	ts.Config.Handler = srv.Handler()
	ts.Start()
	t.Cleanup(func() {
		ts.Close()
		srv.Close()
		_ = store.Close()
		_ = log.Close()
	})

	admin := newClient(t)
	if login(t, admin, ts, "root", "rootpassword") != http.StatusOK {
		t.Fatal("admin login failed")
	}
	// The credential the models name. The calls are Atlas's own API and the API
	// wants a caller, so the secret reference in the model resolves to a real token
	// (ADR-0041) — the same step an operator takes, done here in the environment.
	code, body := cReq(t, admin, ts, "POST", "/api/v1/api-tokens",
		`{"name":"atlas","scope":"full","expiresInDays":1}`)
	if code != http.StatusCreated && code != http.StatusOK {
		t.Fatalf("mint the token: %d (%s)", code, body)
	}
	var minted struct {
		Secret string `json:"secret"`
		Token  string `json:"token"`
	}
	if err := json.Unmarshal(body, &minted); err != nil {
		t.Fatalf("decode the token: %v (%s)", err, body)
	}
	secret := minted.Secret
	if secret == "" {
		secret = minted.Token
	}
	if secret == "" {
		t.Fatalf("the minted token carries no secret: %s", body)
	}
	t.Setenv("ATLAS_CONNECTOR_ATLAS_TOKEN", secret)
	return ts, admin
}

// orchestrationOf returns the fulfilment instance's variables and state for one
// order, or an empty map while there is none.
//
// Two reads, because the search answers with only the variables that matched the
// query — asking it for the whole set is how a check here would pass on an
// instance carrying nothing but the id it was found by.
func orchestrationOf(t *testing.T, c *http.Client, ts *httptest.Server, orderID string) (map[string]string, string) {
	t.Helper()
	code, body := cReq(t, c, ts, "GET",
		fmt.Sprintf("/api/v1/instances/search?q=%s", "orderId%3D"+orderID), "")
	if code != http.StatusOK {
		return map[string]string{}, ""
	}
	var page struct {
		Items []struct {
			Key       uint64 `json:"key"`
			ProcessID string `json:"processId"`
			State     string `json:"state"`
		} `json:"items"`
	}
	if err := json.Unmarshal(body, &page); err != nil {
		return map[string]string{}, ""
	}
	for _, i := range page.Items {
		if i.ProcessID != "atlas-auftrag-erfuellung" {
			continue
		}
		code, raw := cReq(t, c, ts, "GET", fmt.Sprintf("/api/v1/instances/%d/variables", i.Key), "")
		if code != http.StatusOK {
			return map[string]string{}, i.State
		}
		var vars map[string]any
		if err := json.Unmarshal(raw, &vars); err != nil {
			return map[string]string{}, i.State
		}
		out := map[string]string{}
		for k, v := range vars {
			out[k] = fmt.Sprint(v)
		}
		return out, i.State
	}
	return map[string]string{}, ""
}

func variableNames(m map[string]string) string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return strings.Join(out, ", ")
}
