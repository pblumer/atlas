package mcp_test

import (
	"bufio"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/pblumer/atlas/api"
	"github.com/pblumer/atlas/mcp"
)

// The adapter's half of the catalogue opt-out
// (ADR-0434). The server stops serving the area;
// an agent connected to it must not be offered eighteen tools that each answer
// "no such endpoint", which reads as a broken server rather than a decision.

// catalogueOperations reads, off the served OpenAPI document, every operation of the
// area: the ones tagged Catalogue or Order. Read rather than listed, so a tool added
// to the area tomorrow is held to the switch without editing this file.
func catalogueOperations(t *testing.T, atlasURL string) map[string]bool {
	t.Helper()
	resp, err := http.Get(atlasURL + "/api/v1/openapi.json")
	if err != nil {
		t.Fatalf("fetch openapi.json: %v", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read openapi.json: %v", err)
	}
	var doc struct {
		Paths map[string]map[string]struct {
			Tags []string `json:"tags"`
		} `json:"paths"`
	}
	if err := json.Unmarshal(body, &doc); err != nil {
		t.Fatalf("decode openapi.json: %v", err)
	}
	area := map[string]bool{}
	for pattern, methods := range doc.Paths {
		for method, op := range methods {
			for _, tag := range op.Tags {
				if tag == "Catalogue" || tag == "Order" {
					area[strings.ToUpper(method)+" "+pattern] = true
				}
			}
		}
	}
	if len(area) == 0 {
		t.Fatal("the default server documents no Catalogue or Order operation; the tags this test reads have gone stale")
	}
	return area
}

// serveWith feeds requests through an already-built adapter, for a test about how
// it was built.
func serveWith(t *testing.T, srv *mcp.Server, requests ...string) []map[string]any {
	t.Helper()
	var out strings.Builder
	if err := srv.Serve(strings.NewReader(strings.Join(requests, "\n")+"\n"), &out); err != nil {
		t.Fatalf("Serve: %v", err)
	}
	var resps []map[string]any
	sc := bufio.NewScanner(strings.NewReader(out.String()))
	sc.Buffer(make([]byte, 0, 64<<10), 16<<20)
	for sc.Scan() {
		if line := strings.TrimSpace(sc.Text()); line != "" {
			var m map[string]any
			if err := json.Unmarshal([]byte(line), &m); err != nil {
				t.Fatalf("decode response %q: %v", line, err)
			}
			resps = append(resps, m)
		}
	}
	return resps
}

// TestWithoutCatalogueOffersNoToolOfTheArea: every tool whose route the switch
// removes is gone from tools/list and from tools/call, and every other tool is still
// there. The tool→route classification is the drift guard's, so a catalogue tool
// that forgets to say so fails here.
func TestWithoutCatalogueOffersNoToolOfTheArea(t *testing.T) {
	atlas := newAtlas(t)
	area := catalogueOperations(t, atlas.URL)

	srv := mcp.NewServer(mcp.NewClient(atlas.URL), mcp.WithoutCatalogue())
	resps := serveWith(t, srv,
		`{"jsonrpc":"2.0","id":1,"method":"tools/list"}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"atlas_list_catalogs","arguments":{}}}`)
	if len(resps) != 2 {
		t.Fatalf("got %d responses, want 2", len(resps))
	}

	listed := map[string]bool{}
	for _, name := range listedToolNames(t, resps[0]) {
		listed[name] = true
	}
	hidden := 0
	for name, route := range mcpToolRoutes {
		switch {
		case area[route] && listed[name]:
			t.Errorf("%s (%s) is still offered with the catalogue off", name, route)
		case area[route]:
			hidden++
		case !listed[name]:
			t.Errorf("%s (%s) is no longer offered with the catalogue off, and it is not part of the catalogue", name, route)
		}
	}
	if hidden == 0 {
		t.Fatal("no tool of the area was hidden; the classification this test reads has gone stale")
	}

	e, ok := resps[1]["error"].(map[string]any)
	if !ok || !strings.Contains(e["message"].(string), "unknown tool") {
		t.Errorf("calling a tool of the switched-off area: %v, want an unknown-tool error", resps[1])
	}
}

// TestTheAdapterAsksWhetherTheCatalogueIsOffered: the stdio adapter is a separate
// process and cannot read the server's flags, so it asks. An older server that does
// not say is read as offering it — the adapter then lists what it always listed, and
// the server still refuses whatever it does not serve.
func TestTheAdapterAsksWhetherTheCatalogueIsOffered(t *testing.T) {
	for _, tc := range []struct {
		name string
		srv  *httptest.Server
		want bool
	}{
		{"on", newAtlas(t), true},
		{"off", newAtlasWith(t, api.WithoutCatalogue()), false},
	} {
		got, err := mcp.NewClient(tc.srv.URL).CatalogueOffered()
		if err != nil {
			t.Fatalf("%s: CatalogueOffered: %v", tc.name, err)
		}
		if got != tc.want {
			t.Errorf("%s: CatalogueOffered = %v, want %v", tc.name, got, tc.want)
		}
	}

	older := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"product":"Atlas","version":"0.1.0","docs":true}`)
	}))
	t.Cleanup(older.Close)
	if got, err := mcp.NewClient(older.URL).CatalogueOffered(); err != nil || !got {
		t.Errorf("a server that does not say: CatalogueOffered = %v, %v; want true, nil", got, err)
	}

	gone := httptest.NewServer(http.NotFoundHandler())
	gone.Close()
	if _, err := mcp.NewClient(gone.URL).CatalogueOffered(); err == nil {
		t.Error("an unreachable server answered; the caller cannot tell that from a decision")
	}
}
