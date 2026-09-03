package openapimock_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pblumer/atlas/connector/rest/openapimock"
)

// loadPetstore is the fixture most tests start from: a small but complete OpenAPI 3.0
// document with refs, a cycle, an allOf, named examples and a non-JSON media type.
func loadPetstore(t *testing.T) *openapimock.Spec {
	t.Helper()
	spec, err := openapimock.LoadFile(filepath.Join("testdata", "petstore.yaml"))
	if err != nil {
		t.Fatalf("LoadFile: %v", err)
	}
	return spec
}

func TestLoadReadsTheDocument(t *testing.T) {
	spec := loadPetstore(t)
	if spec.Title != "Petstore" || spec.Version != "1.4.2" {
		t.Errorf("info = %q %q, want Petstore 1.4.2", spec.Title, spec.Version)
	}
	// The base path comes from the first server URL's path, so a spec written against
	// https://api.example.com/v1 mocks the same paths the real client calls.
	if spec.BasePath != "/v1" {
		t.Errorf("BasePath = %q, want /v1", spec.BasePath)
	}
	want := []string{
		"GET /pets", "POST /pets", "GET /pets/mine", "GET /pets/{petId}",
		"DELETE /pets/{petId}", "GET /reports/{year}-{month}.csv",
	}
	got := map[string]bool{}
	for _, op := range spec.Operations {
		got[op.Method+" "+op.Path] = true
	}
	for _, w := range want {
		if !got[w] {
			t.Errorf("operation %q missing from %v", w, got)
		}
	}
	if len(spec.Operations) != len(want) {
		t.Errorf("got %d operations, want %d", len(spec.Operations), len(want))
	}
}

func TestLoadOrdersOperationsMostSpecificFirst(t *testing.T) {
	spec := loadPetstore(t)
	// /pets/mine must sort ahead of /pets/{petId}, or the literal path is unreachable.
	mine, param := -1, -1
	for i, op := range spec.Operations {
		switch op.Path {
		case "/pets/mine":
			mine = i
		case "/pets/{petId}":
			if param < 0 {
				param = i
			}
		}
	}
	if mine < 0 || param < 0 || mine > param {
		t.Errorf("/pets/mine at %d, /pets/{petId} at %d; want the literal path first", mine, param)
	}
}

// bodyOf returns the compiled body of one operation's response, by status.
func bodyOf(t *testing.T, spec *openapimock.Spec, method, path string, status int) string {
	t.Helper()
	for _, op := range spec.Operations {
		if op.Method != method || op.Path != path {
			continue
		}
		for _, resp := range op.Responses {
			if resp.Status == status {
				return string(resp.Body)
			}
		}
		t.Fatalf("%s %s has no %d response", method, path, status)
	}
	t.Fatalf("no operation %s %s", method, path)
	return ""
}

func TestLoadGeneratesABodyFromTheSchema(t *testing.T) {
	spec := loadPetstore(t)
	// Every property is present, not only the required ones: a mock exists to be read,
	// and a response missing its optional half sends the caller looking for a bug.
	// parent is a $ref back to Pet — the cycle ends in null rather than recursing.
	const want = `{"born":"1970-01-01","id":1,"name":"string","parent":null,"status":"available","tag":"string"}`
	if got := bodyOf(t, spec, "POST", "/pets", 201); got != want {
		t.Errorf("createPet 201 body\n got %s\nwant %s", got, want)
	}
}

func TestLoadHonorsMinItems(t *testing.T) {
	spec := loadPetstore(t)
	var pets []any
	if err := json.Unmarshal([]byte(bodyOf(t, spec, "GET", "/pets", 200)), &pets); err != nil {
		t.Fatalf("listPets body: %v", err)
	}
	if len(pets) != 2 {
		t.Errorf("listPets returned %d items, want 2 (minItems)", len(pets))
	}
}

func TestLoadMergesAllOf(t *testing.T) {
	spec := loadPetstore(t)
	const want = `{"code":0,"message":"string"}`
	if got := bodyOf(t, spec, "POST", "/pets", 400); got != want {
		t.Errorf("createPet 400 body\n got %s\nwant %s", got, want)
	}
}

func TestLoadPrefersTheWrittenExample(t *testing.T) {
	spec := loadPetstore(t)
	// The 404 states an example; a generated one would say message:"string".
	const want = `{"code":404,"message":"no such pet"}`
	if got := bodyOf(t, spec, "GET", "/pets/{petId}", 404); got != want {
		t.Errorf("getPet 404 body\n got %s\nwant %s", got, want)
	}
}

func TestLoadKeepsNamedExamples(t *testing.T) {
	spec := loadPetstore(t)
	for _, op := range spec.Operations {
		if op.ID != "getPet" {
			continue
		}
		for _, resp := range op.Responses {
			if resp.Status != 200 {
				continue
			}
			// With named examples and no single `example`, the first by name is the
			// body — and every one of them stays reachable by name.
			if got := string(resp.Body); !strings.Contains(got, "Fido") {
				t.Errorf("getPet 200 body = %s, want the first named example", got)
			}
			if got := string(resp.Named["rex"]); !strings.Contains(got, "Rex") {
				t.Errorf("named example rex = %s", got)
			}
			if len(resp.Named) != 2 {
				t.Errorf("got %d named examples, want 2", len(resp.Named))
			}
			return
		}
	}
	t.Fatal("getPet 200 not found")
}

func TestLoadGeneratesResponseHeaders(t *testing.T) {
	spec := loadPetstore(t)
	for _, op := range spec.Operations {
		if op.ID != "createPet" {
			continue
		}
		for _, resp := range op.Responses {
			if resp.Status != 201 {
				continue
			}
			if len(resp.Headers) != 1 || resp.Headers[0].Name != "Location" || resp.Headers[0].Value != "/v1/pets/1" {
				t.Errorf("createPet 201 headers = %+v", resp.Headers)
			}
			return
		}
	}
	t.Fatal("createPet 201 not found")
}

func TestLoadKeepsNonJSONMediaTypes(t *testing.T) {
	spec := loadPetstore(t)
	for _, op := range spec.Operations {
		if op.ID != "monthlyReport" {
			continue
		}
		resp := op.Responses[0]
		if resp.Media != "text/csv" {
			t.Errorf("media = %q, want text/csv", resp.Media)
		}
		// A text example is served as written, not as a JSON string.
		if got := string(resp.Body); !strings.HasPrefix(got, "pets,adoptions") {
			t.Errorf("body = %q, want the raw CSV", got)
		}
		return
	}
	t.Fatal("monthlyReport not found")
}

func TestLoadReadsJSONDocuments(t *testing.T) {
	spec, err := openapimock.LoadFile(filepath.Join("testdata", "tickets.json"))
	if err != nil {
		t.Fatalf("LoadFile: %v", err)
	}
	if spec.Title != "Tickets" || spec.BasePath != "" {
		t.Errorf("info = %q, base = %q", spec.Title, spec.BasePath)
	}
	resp := spec.Operations[0].Responses[0]
	// "default" is status 0 internally and answers 200 on the wire.
	if resp.Status != 0 {
		t.Errorf("status = %d, want 0 for the default response", resp.Status)
	}
	const want = `{"id":"00000000-0000-0000-0000-000000000000","opened":"1970-01-01T00:00:00Z","priority":"low"}`
	if got := string(resp.Body); got != want {
		t.Errorf("body\n got %s\nwant %s", got, want)
	}
}

func TestLoadFileReportsAMissingFile(t *testing.T) {
	if _, err := openapimock.LoadFile(filepath.Join("testdata", "nope.yaml")); err == nil {
		t.Fatal("want an error for a missing file")
	}
}

func TestLoadRefusesWhatItCannotServe(t *testing.T) {
	// A mock that guesses is worse than one that refuses: every case here would
	// otherwise become a wrong answer at request time, far from its cause.
	cases := map[string]struct{ doc, want string }{
		"empty":                  {"", "no paths"},
		"not a document":         {"[1,2,3]", "not an OpenAPI document"},
		"broken yaml":            {"openapi: 3.0.0\npaths:\n  - : :\n", "parse"},
		"swagger 2":              {"swagger: \"2.0\"\npaths: {}\n", "OpenAPI 3"},
		"no paths":               {"openapi: 3.0.0\ninfo: {title: x, version: '1'}\n", "no paths"},
		"relative path":          {"openapi: 3.0.0\npaths:\n  pets:\n    get: {responses: {}}\n", "must start with"},
		"unknown ref":            {"openapi: 3.0.0\npaths:\n  /x:\n    get:\n      responses:\n        '200':\n          content:\n            application/json:\n              schema: {$ref: '#/components/schemas/Nope'}\n", "#/components/schemas/Nope"},
		"remote ref":             {"openapi: 3.0.0\npaths:\n  /x:\n    get:\n      responses:\n        '200':\n          content:\n            application/json:\n              schema: {$ref: 'other.yaml#/Pet'}\n", "only local"},
		"bad status":             {"openapi: 3.0.0\npaths:\n  /x:\n    get:\n      responses:\n        okay: {description: x}\n", "status"},
		"operation is not a map": {"openapi: 3.0.0\npaths:\n  /x:\n    get: 7\n", "get"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := openapimock.Load([]byte(tc.doc))
			if err == nil {
				t.Fatalf("want an error mentioning %q", tc.want)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("error %q, want it to mention %q", err, tc.want)
			}
		})
	}
}

func TestLoadAcceptsAServerWithNoPath(t *testing.T) {
	spec, err := openapimock.Load([]byte("openapi: 3.0.0\nservers: [{url: 'https://api.example.com'}]\npaths:\n  /x:\n    get:\n      responses:\n        '200': {description: ok}\n"))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if spec.BasePath != "" {
		t.Errorf("BasePath = %q, want empty", spec.BasePath)
	}
	// A response with no content is a real thing: it answers with no body.
	if got := spec.Operations[0].Responses[0]; got.Media != "" || len(got.Body) != 0 {
		t.Errorf("response = %+v, want an empty body", got)
	}
}

func TestLoadAcceptsARelativeServerURL(t *testing.T) {
	spec, err := openapimock.Load([]byte("openapi: 3.0.0\nservers: [{url: '/api/v2/'}]\npaths:\n  /x:\n    get:\n      responses:\n        '200': {description: ok}\n"))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	// The trailing slash goes: a base path is a prefix, never a segment of its own.
	if spec.BasePath != "/api/v2" {
		t.Errorf("BasePath = %q, want /api/v2", spec.BasePath)
	}
}

func TestLoadFileReadsAWholeSpecFromDisk(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "spec.yml")
	if err := os.WriteFile(path, []byte("openapi: 3.0.0\ninfo: {title: T, version: '9'}\npaths:\n  /ping:\n    get:\n      responses:\n        '200': {description: pong}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	spec, err := openapimock.LoadFile(path)
	if err != nil {
		t.Fatalf("LoadFile: %v", err)
	}
	if spec.Title != "T" || len(spec.Operations) != 1 {
		t.Errorf("spec = %+v", spec)
	}
}

func TestLoadSkipsWhatItDoesNotServe(t *testing.T) {
	// A real document carries more than operations: extensions, shared parameters, a
	// path-level summary, tags. None of it is servable and none of it is an error.
	spec, err := openapimock.Load([]byte(`
openapi: 3.0.0
info: {title: T, version: '1'}
tags: [one, two]
x-internal: {note: ignored}
paths:
  x-vendor: {get: {responses: {}}}
  /described:
    summary: no operations here
    parameters: [{name: id, in: query}]
  /x:
    get:
      x-owner: platform
      responses:
        x-note: ignored
        '200': {description: ok}
`))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(spec.Operations) != 1 || spec.Operations[0].Path != "/x" {
		t.Errorf("operations = %+v, want only GET /x", spec.Operations)
	}
}

func TestLoadRefusesADocumentWithNothingToServe(t *testing.T) {
	_, err := openapimock.Load([]byte("openapi: 3.0.0\npaths:\n  x-vendor: {get: {responses: {}}}\n"))
	if err == nil || !strings.Contains(err.Error(), "no paths") {
		t.Errorf("err = %v, want it to refuse a document with no operations", err)
	}
}

func TestLoadReportsWhereTheProblemIs(t *testing.T) {
	cases := map[string]struct{ doc, want string }{
		"a path item that is not a mapping": {
			"openapi: 3.0.0\npaths:\n  /x: oops\n", `path "/x"`},
		"an unbalanced path template": {
			"openapi: 3.0.0\npaths:\n  /pets/{id:\n    get: {responses: {}}\n", "unbalanced"},
		"a response that is not a mapping": {
			"openapi: 3.0.0\npaths:\n  /x:\n    get:\n      responses:\n        '200': oops\n", `response "200"`},
		"a media type that is not a mapping": {
			"openapi: 3.0.0\npaths:\n  /x:\n    get:\n      responses:\n        '200':\n          content:\n            application/json: oops\n", "application/json"},
		"a path item that refs nowhere": {
			"openapi: 3.0.0\npaths:\n  /x: {$ref: '#/components/pathItems/Gone'}\n", "#/components/pathItems/Gone"},
		"a response that refs nowhere": {
			"openapi: 3.0.0\npaths:\n  /x:\n    get:\n      responses:\n        '200': {$ref: '#/components/responses/Gone'}\n", "#/components/responses/Gone"},
		"references that point in a circle": {
			"openapi: 3.0.0\npaths:\n  /x:\n    get:\n      responses:\n        '200': {$ref: '#/components/responses/A'}\ncomponents:\n  responses:\n    A: {$ref: '#/components/responses/B'}\n    B: {$ref: '#/components/responses/A'}\n", "circle"},
		"a server url that is not a url": {
			"openapi: 3.0.0\nservers: [{url: '://nope'}]\npaths:\n  /x:\n    get:\n      responses:\n        '200': {description: ok}\n", "server url"},
		"broken JSON": {
			`{"openapi": "3.0.0", "paths": }`, "JSON"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := openapimock.Load([]byte(tc.doc))
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Errorf("err = %v, want it to mention %q", err, tc.want)
			}
		})
	}
}

func TestLoadFileNamesTheFileInAnError(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "broken.yaml")
	if err := os.WriteFile(path, []byte("openapi: 3.0.0\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := openapimock.LoadFile(path)
	if err == nil || !strings.Contains(err.Error(), "broken.yaml") {
		t.Errorf("err = %v, want the file named", err)
	}
}

func TestLoadTakesExamplesAsTheyCome(t *testing.T) {
	// Named examples may be $refs, may carry no value, and may be scalars. A document
	// is somebody else's work: read what is usable and ignore the rest.
	spec, err := openapimock.Load([]byte(`
openapi: 3.0.0
info: {title: T, version: '1'}
servers: ['not a mapping']
paths:
  /x:
    get:
      responses:
        '200':
          description: ok
          headers:
            X-Skipped: not a mapping
            X-Empty: {schema: {type: 'null'}}
            X-Kept: {schema: {type: integer, minimum: 3}}
          content:
            application/json:
              examples:
                shared: {$ref: '#/components/examples/One'}
                noValue: {description: nothing to serve}
                scalar: 7
components:
  examples:
    One: {value: {id: 1}}
`))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if spec.BasePath != "" {
		t.Errorf("BasePath = %q, want empty for a malformed server entry", spec.BasePath)
	}
	resp := spec.Operations[0].Responses[0]
	if len(resp.Named) != 1 || string(resp.Named["shared"]) != `{"id":1}` {
		t.Errorf("named examples = %v", resp.Named)
	}
	if string(resp.Body) != `{"id":1}` {
		t.Errorf("body = %s, want the only usable example", resp.Body)
	}
	if len(resp.Headers) != 1 || resp.Headers[0] != (openapimock.Header{Name: "X-Kept", Value: "3"}) {
		t.Errorf("headers = %+v, want only the one with a value", resp.Headers)
	}
}

func TestLoadNormalizesNonStringKeys(t *testing.T) {
	// YAML allows keys JSON does not. Rendering them keeps the document loadable
	// instead of failing on a field nothing will ever look at.
	spec, err := openapimock.Load([]byte(`
openapi: 3.0.0
info: {title: T, version: '1'}
paths:
  /x:
    get:
      responses:
        '200':
          description: ok
          content:
            application/json:
              schema:
                type: object
                properties:
                  ? 1
                  : {type: string}
`))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got := string(spec.Operations[0].Responses[0].Body); got != `{"1":"string"}` {
		t.Errorf("body = %s", got)
	}
}

func TestLoadRefusesABadRefWhereverItHides(t *testing.T) {
	// The refusal has to reach the surface from every place a schema can sit, or a
	// document with one broken ref serves nulls from somewhere in the middle of it.
	const head = "openapi: 3.0.0\npaths:\n  /x:\n    get:\n      responses:\n        '200':\n"
	cases := map[string]string{
		"in a property":      head + "          content:\n            application/json:\n              schema:\n                type: object\n                properties:\n                  p: {$ref: '#/nope'}\n",
		"in array items":     head + "          content:\n            application/json:\n              schema:\n                type: array\n                items: {$ref: '#/nope'}\n",
		"in an allOf member": head + "          content:\n            application/json:\n              schema:\n                allOf: [{$ref: '#/nope'}]\n",
		"in a header schema": head + "          headers:\n            X-Bad: {schema: {$ref: '#/nope'}}\n",
		"in a named example": head + "          content:\n            application/json:\n              examples:\n                one: {$ref: '#/nope'}\n",
	}
	for name, doc := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := openapimock.Load([]byte(doc)); err == nil {
				t.Error("want an error naming the ref")
			}
		})
	}
}

func TestLoadHandlesValuesJSONCannotHold(t *testing.T) {
	// YAML has values JSON does not: .nan is the reachable one. A body that cannot be
	// rendered is refused at load; a header falls back to printing the value, because
	// a header is a string either way.
	_, err := openapimock.Load([]byte("openapi: 3.0.0\npaths:\n  /x:\n    get:\n      responses:\n        '200':\n          content:\n            application/json:\n              example: .nan\n"))
	if err == nil || !strings.Contains(err.Error(), "render the example") {
		t.Errorf("err = %v, want a refusal to render the body", err)
	}
	_, err = openapimock.Load([]byte("openapi: 3.0.0\npaths:\n  /x:\n    get:\n      responses:\n        '200':\n          content:\n            application/json:\n              examples:\n                one: {value: .nan}\n"))
	if err == nil || !strings.Contains(err.Error(), "render the example") {
		t.Errorf("err = %v, want a refusal to render a named example", err)
	}
	spec, err := openapimock.Load([]byte("openapi: 3.0.0\npaths:\n  /x:\n    get:\n      responses:\n        '200':\n          headers:\n            X-Odd: {example: .nan}\n"))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got := spec.Operations[0].Responses[0].Headers[0].Value; got != "NaN" {
		t.Errorf("header value = %q", got)
	}
}

func TestLoadOrdersMediaTypesWithinAStatus(t *testing.T) {
	spec, err := openapimock.Load([]byte("openapi: 3.0.0\npaths:\n  /x:\n    get:\n      responses:\n        '200':\n          content:\n            text/plain: {example: hello}\n            application/json: {example: {a: 1}}\n"))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	got := []string{spec.Operations[0].Responses[0].Media, spec.Operations[0].Responses[1].Media}
	want := []string{"application/json", "text/plain"}
	if got[0] != want[0] || got[1] != want[1] {
		t.Errorf("media order = %v, want %v", got, want)
	}
}

func TestLoadAcceptsAMediaTypeThatDescribesNothing(t *testing.T) {
	// A content entry with neither example nor schema is a real thing in a
	// half-written document: the operation answers, with nothing in the body.
	spec, err := openapimock.Load([]byte("openapi: 3.0.0\npaths:\n  /x:\n    get:\n      responses:\n        '200':\n          content:\n            application/json: {}\n"))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got := spec.Operations[0].Responses[0]; len(got.Body) != 0 || got.Media != "application/json" {
		t.Errorf("response = %+v, want an empty body", got)
	}
}

// The cases below come from running the mock against real published documents —
// Swagger's own Petstore, Asana, Stripe, DocuSign, Kubernetes and DigitalOcean. Two of
// them served something wrong rather than refusing, which is the one thing this mock
// must not do (ADR-0217).

func TestLoadFollowsRefsToPathItemsOperationsAndResponses(t *testing.T) {
	// A large document routinely factors its operations and responses out into
	// components and refers to them. Reading the reference as an empty object leaves an
	// operation that answers nothing, which is how a mock silently stops mocking.
	spec, err := openapimock.Load([]byte(`
openapi: 3.1.0
info: {title: Refs, version: '1'}
paths:
  /pets:
    $ref: '#/components/pathItems/Pets'
  /pets/{id}:
    get:
      $ref: '#/components/operations/GetPet'
components:
  pathItems:
    Pets:
      get:
        operationId: listPets
        responses:
          '200':
            $ref: '#/components/responses/PetList'
  operations:
    GetPet:
      operationId: getPet
      responses:
        '200':
          description: one pet
          content:
            application/json:
              example: {id: 7}
  responses:
    PetList:
      description: every pet
      content:
        application/json:
          example: [{id: 7}]
`))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(spec.Operations) != 2 {
		t.Fatalf("got %d operations, want 2: %+v", len(spec.Operations), spec.Operations)
	}
	for _, op := range spec.Operations {
		if len(op.Responses) != 1 || len(op.Responses[0].Body) == 0 {
			t.Errorf("%s %s answers nothing: %+v", op.Method, op.Path, op.Responses)
		}
	}
}

func TestLoadRefusesADocumentSplitAcrossFiles(t *testing.T) {
	// DigitalOcean's published document is one file of $refs into a tree of others.
	// Loading it and serving 290 operations that answer nothing is worse than refusing:
	// the mock looks like it works.
	_, err := openapimock.Load([]byte(`
openapi: 3.0.0
info: {title: DO, version: '2.0'}
paths:
  /v2/droplets:
    get:
      $ref: 'resources/droplets/droplets_list.yml'
`))
	if err == nil {
		t.Fatal("want a refusal for a multi-file document")
	}
	for _, want := range []string{"resources/droplets/droplets_list.yml", "one file"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q, want it to mention %q", err, want)
		}
	}
}

func TestLoadWillNotLabelGeneratedJSONAsSomethingElse(t *testing.T) {
	// The Petstore describes its responses as both JSON and XML. This mock generates
	// JSON, so serving those bytes under application/xml would be a lie the caller
	// cannot see through — the media type is dropped instead, and the document's own
	// text examples still travel as themselves.
	spec, err := openapimock.Load([]byte(`
openapi: 3.0.0
info: {title: Petstore, version: '1'}
paths:
  /pet/{id}:
    get:
      operationId: getPet
      responses:
        '200':
          description: a pet
          content:
            application/json: {schema: {type: object, properties: {id: {type: integer}}}}
            application/xml: {schema: {type: object, properties: {id: {type: integer}}}}
            text/plain: {schema: {type: string}}
            text/csv: {example: "id\n7\n"}
`))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	var media []string
	for _, resp := range spec.Operations[0].Responses {
		media = append(media, resp.Media)
	}
	want := []string{"application/json", "text/csv", "text/plain"}
	if strings.Join(media, ",") != strings.Join(want, ",") {
		t.Errorf("media types = %v, want %v (xml cannot be generated)", media, want)
	}
	// What was dropped is said out loud, so a person starting the mock is told rather
	// than left to notice a 406 later.
	if len(spec.Skipped) != 1 || !strings.Contains(spec.Skipped[0], "application/xml") {
		t.Errorf("Skipped = %v, want the xml response named", spec.Skipped)
	}
}

func TestAStatusWhoseOnlyMediaTypeIsUnserveableStillAnswers(t *testing.T) {
	spec, err := openapimock.Load([]byte(`
openapi: 3.0.0
info: {title: X, version: '1'}
paths:
  /x:
    get:
      responses:
        '200':
          description: xml only
          content:
            application/xml: {schema: {type: object}}
`))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	resp := spec.Operations[0].Responses[0]
	if resp.Status != 200 || resp.Media != "" || len(resp.Body) != 0 {
		t.Errorf("response = %+v, want a 200 with no body", resp)
	}
}
