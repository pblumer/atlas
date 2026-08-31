package api_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// nodeDescriptor mirrors the wire contract of GET /api/v1/node. Declared here
// rather than imported so the test pins what a *remote correlator* parses, not the
// Go struct behind it — the two are allowed to diverge only over this test's dead
// body, which is the point of a descriptor other servers read.
type nodeDescriptor struct {
	ID          string            `json:"id"`
	Name        string            `json:"name"`
	Environment string            `json:"environment"`
	Labels      map[string]string `json:"labels"`
	Product     string            `json:"product"`
	Version     string            `json:"version"`
	Revision    string            `json:"revision"`
	Partition   int               `json:"partition"`
	Partitions  int               `json:"partitions"`
	Features    []string          `json:"features"`
}

func getNode(t *testing.T, ts *httptest.Server) nodeDescriptor {
	t.Helper()
	code, body := doReq(t, ts, http.MethodGet, "/api/v1/node", "", "")
	if code != http.StatusOK {
		t.Fatalf("GET node status = %d, body = %s", code, body)
	}
	var n nodeDescriptor
	if err := json.Unmarshal(body, &n); err != nil {
		t.Fatalf("decode node: %v (%s)", err, body)
	}
	return n
}

// TestNodeDescriptorIdentifiesTheRuntimeNotTheBinary is the reason this route
// exists at all. /api/v1/info says which binary is running, which two servers
// built from one commit answer identically; correlation needs to know *which
// server* answered, and to get the same answer from it tomorrow.
func TestNodeDescriptorIdentifiesTheRuntimeNotTheBinary(t *testing.T) {
	ts := newTestServer(t)

	node := getNode(t, ts)
	if node.ID == "" {
		t.Fatal("node descriptor carries no id")
	}
	if len(node.ID) != 32 {
		t.Errorf("id = %q, want 32 hex characters", node.ID)
	}
	if node.Product != "Atlas" || node.Version == "" {
		t.Errorf("descriptor = %+v, want a product and a version", node)
	}
	if node.Partitions != 1 {
		t.Errorf("partitions = %d; Atlas drives one partition per process", node.Partitions)
	}

	// The same server answers with the same id. An id that changed between reads
	// would make one node look like an endless series of new ones.
	if again := getNode(t, ts); again.ID != node.ID {
		t.Errorf("two reads gave two ids: %q and %q", node.ID, again.ID)
	}
}

// TestNodeDescriptorNeverLeaksTheEnvironmentOrTheDataDirectory is the disclosure
// bound ADR-0189 §6 sets. This document is fetched by other servers, so anything
// in it is something this operator has handed to whoever holds a status
// credential. Nothing in the assembly reads the process environment or the data
// directory's layout, and this asserts the result rather than the intention.
func TestNodeDescriptorNeverLeaksTheEnvironmentOrTheDataDirectory(t *testing.T) {
	ts := newTestServer(t)

	code, raw := doReq(t, ts, http.MethodGet, "/api/v1/node", "", "")
	if code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", code, raw)
	}
	for _, leak := range []string{"/tmp/", "PATH", "HOME", "vault", "ATLAS_", "password", "secret"} {
		if strings.Contains(string(raw), leak) {
			t.Errorf("descriptor carries %q: %s", leak, raw)
		}
	}
}

// TestNodeFeaturesAreDerivedFromWhatIsMounted keeps the descriptor from
// advertising a capability this build does not serve. A hand-kept feature list is
// a claim that goes stale on the first rename, and to a correlator a false
// advertisement is worse than none: it turns "this node cannot answer that" into
// an error it has to guess the meaning of.
func TestNodeFeaturesAreDerivedFromWhatIsMounted(t *testing.T) {
	ts := newTestServer(t)
	node := getNode(t, ts)

	if len(node.Features) == 0 {
		t.Fatal("descriptor advertises no features at all")
	}
	seen := map[string]bool{}
	for _, f := range node.Features {
		if seen[f] {
			t.Errorf("feature %q is advertised twice", f)
		}
		seen[f] = true
	}
	// Every advertised feature must actually answer. This is the property the
	// derivation buys, checked end to end rather than against the same map that
	// produced the list.
	probes := map[string]string{
		"observations.stats":     "/api/v1/stats",
		"observations.processes": "/api/v1/processes",
		"observations.instances": "/api/v1/instances",
		"panorama.models":        "/api/v1/panorama/models",
		"panorama.mesh":          "/api/v1/panorama/mesh",
	}
	for feature, path := range probes {
		if !seen[feature] {
			t.Errorf("feature %q is not advertised, but this build serves %s", feature, path)
			continue
		}
		if code, body := doReq(t, ts, http.MethodGet, path, "", ""); code != http.StatusOK {
			t.Errorf("feature %q is advertised but %s answers %d: %s", feature, path, code, body)
		}
	}
}

// TestOperatorNamesTheNodeAndItSurvives is what makes the identity usable by a
// person: an id nobody can recognise is correlatable but not readable, and the
// name is what an architect binds a model element to.
func TestOperatorNamesTheNodeAndItSurvives(t *testing.T) {
	ts := newTestServer(t)
	before := getNode(t, ts)

	code, body := doReq(t, ts, http.MethodPut, "/api/v1/node",
		`{"name":"Zurich primary","environment":"production","labels":{"region":"ch-zh","tier":"1"}}`,
		"application/json")
	if code != http.StatusOK {
		t.Fatalf("PUT node status = %d, body = %s", code, body)
	}

	node := getNode(t, ts)
	if node.Name != "Zurich primary" || node.Environment != "production" {
		t.Fatalf("named node = %+v", node)
	}
	if node.Labels["region"] != "ch-zh" || len(node.Labels) != 2 {
		t.Errorf("labels = %v, want both operator labels", node.Labels)
	}
	// Naming a node does not re-identify it: everything already correlated against
	// the old id must keep pointing at this server.
	if node.ID != before.ID {
		t.Errorf("naming the node changed its id: %q -> %q", before.ID, node.ID)
	}

	// A field left out is left alone. Setting an environment must not silently
	// clear the name somebody set last week.
	if code, body = doReq(t, ts, http.MethodPut, "/api/v1/node",
		`{"environment":"staging"}`, "application/json"); code != http.StatusOK {
		t.Fatalf("partial update status = %d, body = %s", code, body)
	}
	if after := getNode(t, ts); after.Name != "Zurich primary" || after.Environment != "staging" {
		t.Errorf("partial update = %+v, want the name kept and the environment changed", after)
	}

	// An empty label map clears them — otherwise a mistyped label could never be
	// removed, which is the failure mode of "absent means unchanged" applied to
	// everything without exception.
	if code, body = doReq(t, ts, http.MethodPut, "/api/v1/node", `{"labels":{}}`, "application/json"); code != http.StatusOK {
		t.Fatalf("clear labels status = %d, body = %s", code, body)
	}
	if after := getNode(t, ts); len(after.Labels) != 0 {
		t.Errorf("labels = %v after clearing", after.Labels)
	}
}

// TestNodeUpdateIsBounded keeps a document other servers fetch from growing
// without limit, and refuses the shapes that would make it unreadable.
func TestNodeUpdateIsBounded(t *testing.T) {
	ts := newTestServer(t)

	long := strings.Repeat("x", 400)
	for name, body := range map[string]string{
		"a name past the bound":         `{"name":"` + long + `"}`,
		"an environment past the bound": `{"environment":"` + long + `"}`,
		"a label value past the bound":  `{"labels":{"k":"` + long + `"}}`,
		"an empty label key":            `{"labels":{"  ":"v"}}`,
		"not json at all":               `{`,
	} {
		if code, got := doReq(t, ts, http.MethodPut, "/api/v1/node", body, "application/json"); code != http.StatusBadRequest {
			t.Errorf("%s: status = %d, want 400; body = %s", name, code, got)
		}
	}
	// Refusing a bad update leaves the node as it was, rather than half-written.
	if node := getNode(t, ts); node.Name != "" || len(node.Labels) != 0 {
		t.Errorf("a refused update changed the node: %+v", node)
	}
}
