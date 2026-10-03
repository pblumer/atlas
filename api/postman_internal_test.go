package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// The Postman kit under postman/ is published for people to import, and it went
// stale without anybody noticing: when login became the default (ADR-0195) and
// /projects became a deprecated alias of /applications (ADR-0128), nothing in the
// test sweep read the collection, so it kept teaching a server that no longer
// existed. TestThePostmanCollectionReadsItems guards one shape inside it; these
// guard the rest of what can be checked without a running server:
//
//   - every request names a route this server mounts, and not a deprecated one;
//   - every folder and request says what it is for, every request asserts
//     something, and every request carries a saved example;
//   - the models the collection deploys inline are the files beside it.
//
// What needs a server — that each request actually answers what its tests expect —
// is scripts/postman-smoke.sh (make postman-smoke).

// postmanRequest is one request of the collection, flattened out of its folder.
type postmanRequest struct {
	where       string // "Folder / Request", for failure messages
	method      string
	raw         string // the URL as written, {{baseUrl}} and all
	description string
	body        string
	tests       string
	examples    int
}

// postmanCollection reads postman/Atlas.postman_collection.json and returns its
// folders' descriptions (by folder name) and its requests in collection order.
func postmanCollection(t *testing.T) (folders map[string]string, requests []postmanRequest) {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("..", "postman", "Atlas.postman_collection.json"))
	if err != nil {
		t.Fatalf("read the published collection: %v", err)
	}
	type node struct {
		Name        string            `json:"name"`
		Description string            `json:"description"`
		Item        []json.RawMessage `json:"item"`
		Event       []struct {
			Listen string `json:"listen"`
			Script struct {
				Exec []string `json:"exec"`
			} `json:"script"`
		} `json:"event"`
		Request *struct {
			Method      string `json:"method"`
			Description string `json:"description"`
			URL         struct {
				Raw string `json:"raw"`
			} `json:"url"`
			Body struct {
				Raw string `json:"raw"`
			} `json:"body"`
		} `json:"request"`
		Response []json.RawMessage `json:"response"`
	}
	var doc struct {
		Item []json.RawMessage `json:"item"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("decode the collection: %v", err)
	}
	folders = map[string]string{}
	var walk func(items []json.RawMessage, path string)
	walk = func(items []json.RawMessage, path string) {
		for _, it := range items {
			var n node
			if err := json.Unmarshal(it, &n); err != nil {
				t.Fatalf("decode collection item under %q: %v", path, err)
			}
			here := strings.TrimPrefix(path+" / "+n.Name, " / ")
			if n.Request == nil {
				folders[here] = n.Description
				walk(n.Item, here)
				continue
			}
			var tests []string
			for _, e := range n.Event {
				if e.Listen == "test" {
					tests = append(tests, e.Script.Exec...)
				}
			}
			requests = append(requests, postmanRequest{
				where:       here,
				method:      n.Request.Method,
				raw:         n.Request.URL.Raw,
				description: n.Request.Description,
				body:        n.Request.Body.Raw,
				tests:       strings.Join(tests, "\n"),
				examples:    len(n.Response),
			})
		}
	}
	walk(doc.Item, "")
	if len(requests) == 0 {
		t.Fatal("the collection has no requests; either it was gutted or the walk is broken")
	}
	return folders, requests
}

// postmanVariable is a {{name}} reference in a collection URL.
var postmanVariable = regexp.MustCompile(`\{\{[^}]+\}\}`)

// TestThePostmanCollectionCallsServedRoutes resolves every request of the collection
// on the server's own mux — the one Handler serves — so a request whose route was
// renamed, removed or never existed fails here rather than in somebody's Postman. A
// request that reaches a route kept only as a deprecated alias fails too: the
// collection is what newcomers copy, and teaching them the alias is how it outlives
// the release it was kept for.
func TestThePostmanCollectionCallsServedRoutes(t *testing.T) {
	_, requests := postmanCollection(t)
	// An MCP handler of any kind makes the server mount /mcp; the collection calls it.
	srv, _ := newValidateServer(t, WithMCP(http.NotFoundHandler()))
	mux, _ := srv.mountRoutes()
	deprecated := map[string]bool{}
	for _, r := range srv.apiRoutes() {
		if r.op.deprecated {
			deprecated[r.method+" "+r.pattern] = true
		}
	}
	for _, r := range requests {
		path := strings.TrimPrefix(r.raw, "{{baseUrl}}")
		if path == r.raw {
			t.Errorf("%s: URL %q does not start with {{baseUrl}}, so the environment cannot point it at a server", r.where, r.raw)
			continue
		}
		path, _, _ = strings.Cut(path, "?")
		// Every variable in a path stands for a key or an id; any value finds the route.
		path = postmanVariable.ReplaceAllString(path, "1")
		_, pattern := mux.Handler(httptest.NewRequest(r.method, path, nil))
		switch {
		case pattern == "" || pattern == "/" || pattern == "/api/v1/":
			// "/" is the web UI's file server and "/api/v1/" the API's own 404; neither
			// is a route the request was written for.
			t.Errorf("%s: %s %s is not a route this server serves (resolved to %q)", r.where, r.method, path, pattern)
		case deprecated[pattern]:
			t.Errorf("%s: %s is a deprecated alias; call the route that replaced it (see its summary in api/openapi.go)", r.where, pattern)
		}
	}
}

// TestEveryPostmanRequestIsDocumentedAndAsserted holds the collection to what its
// README promises: each folder and request says what it is for, each request
// asserts the status and shape of its answer — a request without a test is one a
// green run says nothing about — and each carries at least one saved example, which
// is what Postman shows somebody who has not sent it yet.
func TestEveryPostmanRequestIsDocumentedAndAsserted(t *testing.T) {
	folders, requests := postmanCollection(t)
	for name, desc := range folders {
		if strings.TrimSpace(desc) == "" {
			t.Errorf("folder %q has no description", name)
		}
	}
	for _, r := range requests {
		if strings.TrimSpace(r.description) == "" {
			t.Errorf("%s has no description", r.where)
		}
		if !strings.Contains(r.tests, "pm.test(") {
			t.Errorf("%s has no pm.test assertion in its Tests script", r.where)
		}
		if r.examples == 0 {
			t.Errorf("%s has no saved example response", r.where)
		}
	}
}

// bpmnProcessID finds the first process id in a BPMN document.
var bpmnProcessID = regexp.MustCompile(`<process\s[^>]*\bid="([^"]+)"`)

// TestThePostmanSampleModelsAreTheFilesBesideIt keeps the two copies of each sample
// model in step. The collection deploys its models inline, because a request body is
// what Postman can send without a file picker; the .bpmn files beside it are what
// people open in the Modeler and what the README links. Every .bpmn file must be
// deployed by some request, and every request deploying a model that has a file
// must send that file byte for byte.
func TestThePostmanSampleModelsAreTheFilesBesideIt(t *testing.T) {
	_, requests := postmanCollection(t)
	files, err := filepath.Glob(filepath.Join("..", "postman", "*.bpmn"))
	if err != nil {
		t.Fatalf("glob: %v", err)
	}
	if len(files) == 0 {
		t.Fatal("no .bpmn files under postman/; the kit ships its sample models there")
	}
	type sample struct{ file, content string }
	byID := map[string]sample{}
	for _, f := range files {
		content, err := os.ReadFile(f)
		if err != nil {
			t.Fatalf("read %s: %v", f, err)
		}
		m := bpmnProcessID.FindSubmatch(content)
		if m == nil {
			t.Fatalf("%s has no <process id>", f)
		}
		byID[string(m[1])] = sample{filepath.Base(f), string(content)}
	}
	sent := map[string]bool{}
	for _, r := range requests {
		m := bpmnProcessID.FindStringSubmatch(r.body)
		if m == nil {
			continue
		}
		want, ok := byID[m[1]]
		if !ok {
			continue
		}
		sent[m[1]] = true
		if r.body != want.content {
			t.Errorf("%s sends the model %q, but its body is not postman/%s; copy the file into the request", r.where, m[1], want.file)
		}
	}
	for id, s := range byID {
		if !sent[id] {
			t.Errorf("postman/%s is deployed by no request in the collection", s.file)
		}
	}
}
