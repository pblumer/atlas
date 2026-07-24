package api

import (
	"encoding/json"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// A nil-engine Server suffices: apiRoutes and openapiDoc read only the static
// route table and never touch the processor or store.
func specServer() *Server { return &Server{} }

// TestOpenAPICoversEveryRoute is the drift guard (ADR-0043): the OpenAPI
// document must describe exactly the routes apiRoutes registers — no served
// route missing from the spec, no documented route that isn't served. Because
// both derive from one table, this asserts the invariant holds and fails loudly
// if a future edit ever registers a route outside the table.
func TestOpenAPICoversEveryRoute(t *testing.T) {
	s := specServer()
	doc := s.openapiDoc()
	paths, ok := doc["paths"].(map[string]any)
	if !ok {
		t.Fatalf("paths is %T, want map", doc["paths"])
	}

	// Every route: its pattern is a documented path and its method an operation.
	documented := map[string]bool{}
	for _, r := range s.apiRoutes() {
		item, ok := paths[r.pattern].(map[string]any)
		if !ok {
			t.Fatalf("route %s %s has no documented path", r.method, r.pattern)
		}
		if _, ok := item[strings.ToLower(r.method)]; !ok {
			t.Errorf("route %s %s has no %s operation in the spec", r.method, r.pattern, strings.ToLower(r.method))
		}
		documented[r.method+" "+r.pattern] = true
	}

	// Reverse: every documented operation corresponds to a registered route.
	for pattern, item := range paths {
		ops, ok := item.(map[string]any)
		if !ok {
			t.Fatalf("path %s is %T, want map", pattern, item)
		}
		for method := range ops {
			key := strings.ToUpper(method) + " " + pattern
			if !documented[key] {
				t.Errorf("documented operation %s has no registered route", key)
			}
		}
	}
}

// TestOpenAPIOperationsAreWellFormed checks each operation carries the metadata
// the explorer needs: a non-empty summary and tag, an operationId, every path
// wildcard declared as a required path parameter, and a success plus default
// response.
func TestOpenAPIOperationsAreWellFormed(t *testing.T) {
	s := specServer()
	wildcard := regexp.MustCompile(`\{([^}]+)\}`)
	seenIDs := map[string]bool{}

	for _, r := range s.apiRoutes() {
		if strings.TrimSpace(r.op.summary) == "" {
			t.Errorf("route %s %s has an empty summary", r.method, r.pattern)
		}
		if strings.TrimSpace(r.op.tag) == "" {
			t.Errorf("route %s %s has an empty tag", r.method, r.pattern)
		}
		if r.handler == nil {
			t.Errorf("route %s %s has a nil handler", r.method, r.pattern)
		}

		op := operationDoc(r)

		id, _ := op["operationId"].(string)
		if id == "" {
			t.Errorf("route %s %s has no operationId", r.method, r.pattern)
		}
		if seenIDs[id] {
			t.Errorf("duplicate operationId %q", id)
		}
		seenIDs[id] = true

		// Every {wildcard} in the pattern must appear as a required path param.
		want := map[string]bool{}
		for _, m := range wildcard.FindAllStringSubmatch(r.pattern, -1) {
			want[m[1]] = true
		}
		got := map[string]bool{}
		if params, ok := op["parameters"].([]any); ok {
			for _, p := range params {
				pm := p.(map[string]any)
				if pm["in"] != "path" || pm["required"] != true {
					t.Errorf("route %s %s: path param %v not required/in-path", r.method, r.pattern, pm["name"])
				}
				got[pm["name"].(string)] = true
			}
		}
		for name := range want {
			if !got[name] {
				t.Errorf("route %s %s: wildcard {%s} missing from parameters", r.method, r.pattern, name)
			}
		}

		responses, ok := op["responses"].(map[string]any)
		if !ok {
			t.Fatalf("route %s %s: responses is %T", r.method, r.pattern, op["responses"])
		}
		if _, ok := responses["default"]; !ok {
			t.Errorf("route %s %s: no default error response", r.method, r.pattern)
		}
		status := r.op.status
		if status == 0 {
			status = http.StatusOK
		}
		if _, ok := responses[strconv.Itoa(status)]; !ok {
			t.Errorf("route %s %s: no %d success response", r.method, r.pattern, status)
		}
	}
}

// TestOpenAPIDocIsValidJSONAndVersioned confirms the document marshals and
// carries the top-level fields tools expect.
func TestOpenAPIDocIsValidJSONAndVersioned(t *testing.T) {
	raw, err := json.Marshal(specServer().openapiDoc())
	if err != nil {
		t.Fatalf("marshal openapi doc: %v", err)
	}
	var doc map[string]any
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("unmarshal openapi doc: %v", err)
	}
	if doc["openapi"] != "3.1.0" {
		t.Errorf("openapi = %v, want 3.1.0", doc["openapi"])
	}
	info, ok := doc["info"].(map[string]any)
	if !ok || info["title"] != "Atlas HTTP API" || info["version"] != Version {
		t.Errorf("info = %v, want title=Atlas HTTP API version=%s", doc["info"], Version)
	}
	comps, ok := doc["components"].(map[string]any)
	if !ok {
		t.Fatalf("components missing")
	}
	if schemas, ok := comps["schemas"].(map[string]any); !ok || schemas["Error"] == nil {
		t.Errorf("components.schemas.Error missing")
	}
}

// TestOperationIDStableForNestedPattern pins the id-derivation for a route with
// wildcards, since client generators key off it.
func TestOperationIDStableForNestedPattern(t *testing.T) {
	got := operationID(apiRoute{method: "GET", pattern: "/api/v1/processes/{key}/xml"})
	if got != "get_processes_key_xml" {
		t.Errorf("operationID = %q, want get_processes_key_xml", got)
	}
}

// TestMediaContentDefaultsToPermissiveObject pins the "permissive default"
// promised by ADR-0043: a body without a hand-written schema still renders a
// valid object schema so the document stays valid.
func TestMediaContentDefaultsToPermissiveObject(t *testing.T) {
	content := mediaContent(&bodySpec{mediaType: "application/json"})
	media, ok := content["application/json"].(map[string]any)
	if !ok {
		t.Fatalf("content missing application/json entry: %v", content)
	}
	schema, ok := media["schema"].(map[string]any)
	if !ok || schema["type"] != "object" {
		t.Errorf("default schema = %v, want type=object", media["schema"])
	}
}
