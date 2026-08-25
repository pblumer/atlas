package examples_test

// The third rung of the ladder the other two tests in this directory climb.
//
// TestExtensionElementsAreNamespaced catches a model the Modeler cannot read.
// TestShippedModelsCompile catches a model nothing can run. Both passed on a model
// that deployed, ran to completion, reported success — and did nothing. Compiling is
// not running, and running is not working.
//
// So this boots a real Atlas in-process (wal, state, engine, HTTP API), deploys the
// shipped Entra example, runs a real worker against it, and serves the Graph side
// from a stub. Everything between the model and the wire is the production path:
// compile, deploy, instance, job, entra.Resolve on the engine, the resolved detail
// across the wire, the worker's decode, and the connector's query, paging and header.
//
// Two defects were found the first time this was run by hand, and neither was
// visible from inside a package:
//
//   - The example's reconciliation returned nothing. Its FEEL filtered on an outer
//     variable, and inside a filter predicate FEEL resolves every name against the
//     list element — so the name was a missing property rather than an unknown
//     variable, expr.CompileAuto never learned to declare it, and the predicate
//     evaluated over null. No error anywhere.
//   - advancedQuery never reached the worker. The resolved detail is keyed
//     advancedQuery and the Job's JSON tag said advanced, so it decoded to false in
//     silence and the listing ran as a plain query.
//
// Both are asserted below, by outcome rather than by shape.

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/pblumer/atlas/api"
	"github.com/pblumer/atlas/compiler"
	"github.com/pblumer/atlas/connector/entra"
	"github.com/pblumer/atlas/engine"
	"github.com/pblumer/atlas/state"
	"github.com/pblumer/atlas/wal"
	"github.com/pblumer/atlas/worker"
)

// atlasStack is one in-process Atlas behind an httptest server, so the worker polls
// the real HTTP job protocol rather than a stand-in for it.
type atlasStack struct {
	ts    *httptest.Server
	srv   *api.Server
	store *state.Store
	log   *wal.Log
}

func bootAtlas(t *testing.T) *atlasStack {
	t.Helper()
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
	srv, err := api.New(proc, store, dir)
	if err != nil {
		t.Fatalf("api.New: %v", err)
	}
	s := &atlasStack{ts: httptest.NewServer(srv.Handler()), srv: srv, store: store, log: log}
	t.Cleanup(func() {
		s.ts.Close()
		s.srv.Close()
		_ = s.store.Close()
		_ = s.log.Close()
	})
	return s
}

// stubToken stands in for the OAuth2 client-credentials exchange, which has its own
// tests in connector/oauth2 and would otherwise point this test at Microsoft.
type stubToken struct{}

func (stubToken) Token(context.Context) (string, error) { return "stub", nil }

// graphStub answers the Graph calls the connector makes and records what it was
// asked, so the assertions are about what reached the wire.
type graphStub struct {
	srv         *httptest.Server
	listPaths   []string
	consistency []string
	patched     []string
	patchBodies []string
}

func newGraphStub(t *testing.T) *graphStub {
	t.Helper()
	g := &graphStub{}
	mux := http.NewServeMux()
	mux.HandleFunc("/v1.0/users", func(w http.ResponseWriter, r *http.Request) {
		g.consistency = append(g.consistency, r.Header.Get("ConsistencyLevel"))
		g.listPaths = append(g.listPaths, r.URL.RequestURI())
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Query().Get("$skiptoken") != "" {
			fmt.Fprint(w, `{"value":[{"id":"u3","displayName":"Cara","userPrincipalName":"c@contoso.com","mail":"c@contoso.com"}]}`)
			return
		}
		// Graph echoes the query into @odata.nextLink, so a continuation carries
		// $count=true too. Mirroring that keeps the fixture honest about what the
		// connector has to hold on to across a page.
		carry := ""
		if r.URL.Query().Get("$count") != "" {
			carry = "&$count=true"
		}
		// Two pages, so paging is genuinely exercised rather than assumed.
		fmt.Fprintf(w, `{"value":[
		  {"id":"u1","displayName":"Anna","userPrincipalName":"a@contoso.com","mail":"a@contoso.com"},
		  {"id":"u2","displayName":"Bert","userPrincipalName":"b@contoso.com","mail":"b@contoso.com"}
		],"@odata.nextLink":%q}`, g.srv.URL+"/v1.0/users?$skiptoken=PAGE2"+carry)
	})
	mux.HandleFunc("/v1.0/users/", func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		g.patched = append(g.patched, strings.TrimPrefix(r.URL.Path, "/v1.0/users/"))
		g.patchBodies = append(g.patchBodies, string(body))
		w.WriteHeader(http.StatusNoContent) // what Graph answers a PATCH with
	})
	g.srv = httptest.NewServer(mux)
	t.Cleanup(g.srv.Close)
	return g
}

// serveEntra runs a real worker for io.atlas.entra until the test ends. The registry
// is built here rather than from the environment because the environment form pins
// the token endpoint at login.microsoftonline.com; RunEntraJob takes a registry the
// caller owns precisely so an embedder can supply its own.
func serveEntra(t *testing.T, atlasURL string, stub *graphStub) {
	t.Helper()
	reg := entra.NewRegistry()
	reg.Register("contoso", entra.NewGraphClient(stubToken{}, stub.srv.URL+"/v1.0", http.DefaultClient))
	w := worker.New(worker.Options{
		Server:     atlasURL,
		ID:         "examples-entra-integration",
		Connectors: []string{"contoso"},
		Lease:      30 * time.Second,
		Wait:       200 * time.Millisecond,
		Retry:      50 * time.Millisecond,
		MaxJobs:    5,
		Handlers: map[string]worker.Exec{
			compiler.EntraJobType: worker.ExecFunc(func(ctx context.Context, j worker.Job) (map[string]any, error) {
				return worker.RunEntraJob(ctx, j, reg)
			}),
		},
	})
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go func() { _ = w.Run(ctx) }()
}

// instView is the slice of an instance listing this test reads.
type instView struct {
	Key       float64 `json:"key"`
	State     string  `json:"state"`
	Variables []struct {
		Name  string `json:"name"`
		Value string `json:"value"`
	} `json:"variables"`
}

func httpJSON(t *testing.T, method, url, contentType string, body []byte, out any) {
	t.Helper()
	var rdr io.Reader
	if body != nil {
		rdr = strings.NewReader(string(body))
	}
	req, err := http.NewRequest(method, url, rdr)
	if err != nil {
		t.Fatal(err)
	}
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode/100 != 2 {
		t.Fatalf("%s %s -> %d: %s", method, url, resp.StatusCode, raw)
	}
	if out != nil {
		if err := json.Unmarshal(raw, out); err != nil {
			t.Fatalf("%s %s: decode %s: %v", method, url, raw, err)
		}
	}
}

// runToCompletion deploys a model, starts an instance, and waits for it to finish,
// returning its final variables by name.
func runToCompletion(t *testing.T, base string, bpmn []byte, vars map[string]any) map[string]string {
	t.Helper()
	var before []instView
	httpJSON(t, "GET", base+"/api/v1/instances?limit=200", "", nil, &before)
	seen := map[float64]bool{}
	for _, i := range before {
		seen[i.Key] = true
	}

	var dep struct {
		Key float64 `json:"key"`
	}
	httpJSON(t, "POST", base+"/api/v1/deployments", "application/xml", bpmn, &dep)

	body, _ := json.Marshal(map[string]any{"variables": vars})
	httpJSON(t, "POST", fmt.Sprintf("%s/api/v1/processes/%.0f/instances", base, dep.Key), "application/json", body, nil)

	deadline := time.Now().Add(60 * time.Second)
	var mine instView
	for time.Now().Before(deadline) {
		var now []instView
		httpJSON(t, "GET", base+"/api/v1/instances?limit=200", "", nil, &now)
		for _, i := range now {
			if !seen[i.Key] {
				mine = i
			}
		}
		if mine.Key != 0 && mine.State != "active" {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if mine.Key == 0 {
		t.Fatal("the started instance never appeared")
	}
	if mine.State != "completed" {
		t.Fatalf("instance state = %q, want completed", mine.State)
	}
	out := map[string]string{}
	for _, v := range mine.Variables {
		out[v.Name] = v.Value
	}
	return out
}

// The shipped example, run for real: two pages listed, the reconciliation actually
// reconciling, and the orphans actually disabled.
func TestEntraExampleActuallyRuns(t *testing.T) {
	atlas := bootAtlas(t)
	stub := newGraphStub(t)
	serveEntra(t, atlas.ts.URL, stub)

	bpmn, err := os.ReadFile("entra-konten-rezertifizierung.bpmn")
	if err != nil {
		t.Fatal(err)
	}
	vars := runToCompletion(t, atlas.ts.URL, bpmn, map[string]any{
		"bereich":             "IT",
		"aktiveMitarbeitende": []string{"a@contoso.com"},
	})

	// The listing arrived as one result, not one page.
	if len(stub.listPaths) != 2 {
		t.Errorf("made %d listing requests, want 2 — the continuation must be followed", len(stub.listPaths))
	}
	if n := strings.Count(vars["konten"], `"id"`); n != 3 {
		t.Errorf("konten = %s, want all three users from both pages", vars["konten"])
	}
	// The authored filter reached Graph as Graph's own parameter.
	if !strings.Contains(stub.listPaths[0], "%24filter=department") && !strings.Contains(stub.listPaths[0], "$filter=department") {
		t.Errorf("first request = %q, want the authored department filter", stub.listPaths[0])
	}

	// The reconciliation reconciled. This is the assertion the model once failed
	// while reporting success: zuSperren was empty because the FEEL predicate
	// evaluated over an unbound name, and the process disabled nobody.
	for _, want := range []string{"u2", "u3"} {
		if !strings.Contains(vars["zuSperren"], `"`+want+`"`) {
			t.Errorf("zuSperren = %s, want the orphaned account %s", vars["zuSperren"], want)
		}
	}
	if strings.Contains(vars["zuSperren"], `"u1"`) {
		t.Errorf("zuSperren = %s, want the still-employed account left alone", vars["zuSperren"])
	}

	// And the disable really happened, once per orphan, with the body Graph takes.
	if len(stub.patched) != 2 {
		t.Errorf("disabled %v, want exactly the two orphans", stub.patched)
	}
	for _, b := range stub.patchBodies {
		if !strings.Contains(b, `"accountEnabled":false`) {
			t.Errorf("PATCH body = %s, want accountEnabled false", b)
		}
	}
	// A plain listing stays strongly consistent.
	for _, c := range stub.consistency {
		if c != "" {
			t.Errorf("a plain listing sent ConsistencyLevel %q, want none", c)
		}
	}
}

// advancedQueryBPMN opts into an advanced query, so the header can be watched
// travelling the whole way from an authored attribute to the wire — the hop where it
// was once dropped in silence.
const advancedQueryBPMN = `<?xml version="1.0" encoding="UTF-8"?>
<bpmn:definitions xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL"
                  xmlns:atlas="http://atlas/schema/1.0"
                  id="defs_entra_advanced" targetNamespace="http://atlas/examples">
  <bpmn:process id="entra-advanced-query-probe" name="Entra advanced query" isExecutable="true">
    <bpmn:startEvent id="s"/>
    <bpmn:serviceTask id="t">
      <bpmn:extensionElements>
        <atlas:entraConnector connector="contoso" operation="list-users"
                              filter="endsWith(mail,'@contoso.com')"
                              advancedQuery="true" resultVariable="treffer"/>
      </bpmn:extensionElements>
    </bpmn:serviceTask>
    <bpmn:endEvent id="e"/>
    <bpmn:sequenceFlow id="f1" sourceRef="s" targetRef="t"/>
    <bpmn:sequenceFlow id="f2" sourceRef="t" targetRef="e"/>
  </bpmn:process>
</bpmn:definitions>`

func TestEntraAdvancedQueryReachesTheWire(t *testing.T) {
	atlas := bootAtlas(t)
	stub := newGraphStub(t)
	serveEntra(t, atlas.ts.URL, stub)

	vars := runToCompletion(t, atlas.ts.URL, []byte(advancedQueryBPMN), map[string]any{})
	if n := strings.Count(vars["treffer"], `"id"`); n != 3 {
		t.Errorf("treffer = %s, want both pages", vars["treffer"])
	}
	// Every request, not just the first: Graph rejects a continuation fetched
	// without the header that made the query legal.
	if len(stub.consistency) != 2 {
		t.Fatalf("made %d requests, want 2", len(stub.consistency))
	}
	for i, c := range stub.consistency {
		if c != "eventual" {
			t.Errorf("request %d sent ConsistencyLevel %q, want eventual", i, c)
		}
	}
	// $count=true is the other half Graph insists on, and the connector builds it
	// into the request it makes.
	if !strings.Contains(stub.listPaths[0], "count=true") {
		t.Errorf("first request = %q, want $count=true alongside the header", stub.listPaths[0])
	}
}
