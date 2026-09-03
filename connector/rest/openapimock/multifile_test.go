package openapimock_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pblumer/atlas/connector/rest/openapimock"
)

// A document published as a tree of files is how most large APIs ship — DigitalOcean's
// is one entry file of $refs into a hundred others. The rule that makes it work, and
// the one a naive reader gets wrong, is that a reference resolves relative to the file
// it is written in, not to the entry document.

// writeTree writes files (path → content) under a new temporary directory and returns
// the directory.
func writeTree(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for name, content := range files {
		full := filepath.Join(dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(full), 0o750); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

// splitTree is the shape the real thing has: operations in one subtree, schemas in
// another, and references that climb out of their own directory to reach across.
var splitTree = map[string]string{
	"root.yaml": `
openapi: 3.0.0
info: {title: DO, version: '2.0'}
servers: [{url: 'https://api.example.com/v1'}]
paths:
  /account:
    get:
      $ref: 'resources/account.yml'
  /droplets:
    get:
      $ref: 'resources/droplets/list.yml'
`,
	"resources/account.yml": `
operationId: getAccount
responses:
  '200':
    description: the account
    content:
      application/json:
        schema:
          $ref: '../shared/account.yml#/Account'
`,
	"resources/droplets/list.yml": `
operationId: listDroplets
responses:
  '200':
    $ref: '../../shared/responses.yml#/DropletList'
`,
	"shared/account.yml": `
Account:
  type: object
  properties:
    email: {type: string, example: 'sammy@digitalocean.com'}
`,
	"shared/responses.yml": `
DropletList:
  description: every droplet
  content:
    application/json:
      schema:
        type: array
        items:
          $ref: 'droplet.yml#/Droplet'
`,
	"shared/droplet.yml": `
Droplet:
  type: object
  properties:
    id: {type: integer, example: 3164444}
`,
}

func TestLoadFileFollowsRefsAcrossFiles(t *testing.T) {
	dir := writeTree(t, splitTree)
	spec, err := openapimock.LoadFile(filepath.Join(dir, "root.yaml"))
	if err != nil {
		t.Fatalf("LoadFile: %v", err)
	}
	if len(spec.Operations) != 2 {
		t.Fatalf("got %d operations, want 2", len(spec.Operations))
	}
	if spec.Files != 5 {
		t.Errorf("Files = %d, want the 5 files beside the entry document", spec.Files)
	}

	h := openapimock.New(spec).Handler()
	// A schema one directory up from the operation that refers to it.
	w := do(h, "GET", "/v1/account", "", nil)
	if got := strings.TrimSpace(w.Body.String()); got != `{"email":"sammy@digitalocean.com"}` {
		t.Errorf("GET /v1/account = %s", got)
	}
	// A response two directories up, whose own schema refers to a file beside *it* —
	// the case that only works when each file resolves against its own directory.
	w = do(h, "GET", "/v1/droplets", "", nil)
	if got := strings.TrimSpace(w.Body.String()); got != `[{"id":3164444}]` {
		t.Errorf("GET /v1/droplets = %s", got)
	}
}

func TestARefMayNotClimbOutOfTheSpecRoot(t *testing.T) {
	// The mock has no authentication and serves what it reads, so a document that
	// reaches outside its own tree could put a file from this machine on an open port.
	// The default root is the document's own directory; widening it is deliberate.
	dir := writeTree(t, map[string]string{
		"spec/root.yaml": `
openapi: 3.0.0
info: {title: X, version: '1'}
paths:
  /x:
    get:
      $ref: '../outside/op.yml'
`,
		"outside/op.yml": `
responses:
  '200': {description: ok}
`,
	})
	_, err := openapimock.LoadFile(filepath.Join(dir, "spec", "root.yaml"))
	if err == nil {
		t.Fatal("want a refusal for a ref outside the spec's directory")
	}
	for _, want := range []string{"outside", "--spec-root"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q, want it to mention %q", err, want)
		}
	}
	// Widening the root deliberately is what makes that layout loadable.
	spec, err := openapimock.LoadFileUnder(filepath.Join(dir, "spec", "root.yaml"), dir)
	if err != nil {
		t.Fatalf("LoadFileUnder: %v", err)
	}
	if len(spec.Operations) != 1 {
		t.Errorf("got %d operations under a widened root", len(spec.Operations))
	}
}

func TestTheSamePointerInTwoFilesIsTwoThings(t *testing.T) {
	// Both files define #/Thing. A reader that remembers which refs it is inside by
	// the ref string alone thinks the second one is a cycle and answers null.
	dir := writeTree(t, map[string]string{
		"root.yaml": `
openapi: 3.0.0
info: {title: X, version: '1'}
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
                  a: {$ref: 'a.yml#/Thing'}
                  b: {$ref: 'b.yml#/Thing'}
                  c: {$ref: 'a.yml#/Thing'}
`,
		"a.yml": "Thing: {type: string, example: from-a}\n",
		"b.yml": "Thing: {type: string, example: from-b}\n",
	})
	spec, err := openapimock.LoadFile(filepath.Join(dir, "root.yaml"))
	if err != nil {
		t.Fatalf("LoadFile: %v", err)
	}
	// c refers to a file already read, which is answered from the copy in hand.
	const want = `{"a":"from-a","b":"from-b","c":"from-a"}`
	if got := string(spec.Operations[0].Responses[0].Body); got != want {
		t.Errorf("body = %s, want %s", got, want)
	}
}

func TestACycleAcrossFilesEnds(t *testing.T) {
	dir := writeTree(t, map[string]string{
		"root.yaml": `
openapi: 3.0.0
info: {title: X, version: '1'}
paths:
  /x:
    get:
      responses:
        '200':
          description: ok
          content:
            application/json:
              schema: {$ref: 'a.yml#/Node'}
`,
		"a.yml": "Node:\n  type: object\n  properties:\n    child: {$ref: 'b.yml#/Node'}\n",
		"b.yml": "Node:\n  type: object\n  properties:\n    back: {$ref: 'a.yml#/Node'}\n",
	})
	spec, err := openapimock.LoadFile(filepath.Join(dir, "root.yaml"))
	if err != nil {
		t.Fatalf("LoadFile: %v", err)
	}
	var body map[string]any
	if err := json.Unmarshal(spec.Operations[0].Responses[0].Body, &body); err != nil {
		t.Fatalf("body: %v", err)
	}
	child, _ := body["child"].(map[string]any)
	if child == nil {
		t.Fatalf("body = %v, want the first hop present", body)
	}
	if back, ok := child["back"]; !ok || back != nil {
		t.Errorf("child = %v, want the return hop to stop at null", child)
	}
}

func TestRefusalsAcrossFiles(t *testing.T) {
	cases := map[string]struct {
		files map[string]string
		want  string
	}{
		"a file that is not there": {
			map[string]string{"root.yaml": "openapi: 3.0.0\npaths:\n  /x:\n    get: {$ref: 'gone.yml'}\n"},
			"gone.yml"},
		"a url": {
			map[string]string{"root.yaml": "openapi: 3.0.0\npaths:\n  /x:\n    get: {$ref: 'https://example.com/op.yml'}\n"},
			"files, not URLs"},
		"a file that does not parse": {
			map[string]string{
				"root.yaml": "openapi: 3.0.0\npaths:\n  /x:\n    get: {$ref: 'op.yml'}\n",
				"op.yml":    "responses: [unclosed\n"},
			"parse"},
		"a chain whose second hop is gone": {
			map[string]string{
				"root.yaml": "openapi: 3.0.0\npaths:\n  /x:\n    get: {$ref: '#/components/x'}\ncomponents:\n  x: {$ref: 'gone.yml'}\n"},
			"gone.yml"},
		"a pointer into a file that has no such node": {
			map[string]string{
				"root.yaml": "openapi: 3.0.0\npaths:\n  /x:\n    get: {$ref: 'op.yml#/Nope'}\n",
				"op.yml":    "Other: {}\n"},
			"#/Nope"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			dir := writeTree(t, tc.files)
			_, err := openapimock.LoadFile(filepath.Join(dir, "root.yaml"))
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Errorf("error %v, want it to mention %q", err, tc.want)
			}
		})
	}
}

func TestADocumentWithNoFileBehindItStillRefusesFileRefs(t *testing.T) {
	// Load takes bytes, so there is no directory to resolve against. Saying so beats
	// serving an operation that answers nothing.
	_, err := openapimock.Load([]byte("openapi: 3.0.0\npaths:\n  /x:\n    get: {$ref: 'other.yml'}\n"))
	if err == nil || !strings.Contains(err.Error(), "not read from a file") {
		t.Errorf("error %v, want it to say there is no file to resolve against", err)
	}
}

func TestATreeOfFilesIsBounded(t *testing.T) {
	// A generated or symlinked tree should not read the disk forever.
	files := map[string]string{}
	var props strings.Builder
	for i := range 300 {
		files[fmt.Sprintf("f%d.yml", i)] = fmt.Sprintf("Thing: {type: string, example: v%d}\n", i)
		fmt.Fprintf(&props, "                  p%d: {$ref: 'f%d.yml#/Thing'}\n", i, i)
	}
	files["root.yaml"] = "openapi: 3.0.0\npaths:\n  /x:\n    get:\n      responses:\n        '200':\n          description: ok\n          content:\n            application/json:\n              schema:\n                type: object\n                properties:\n" + props.String()
	dir := writeTree(t, files)
	_, err := openapimock.LoadFile(filepath.Join(dir, "root.yaml"))
	if err == nil || !strings.Contains(err.Error(), "too many files") {
		t.Errorf("error %v, want the file count bounded", err)
	}
}

func TestTheServerStillAnswersFromASplitDocument(t *testing.T) {
	dir := writeTree(t, splitTree)
	spec, err := openapimock.LoadFile(filepath.Join(dir, "root.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	srv := openapimock.New(spec)
	if w := do(srv.Handler(), "GET", "/v1/droplets", "", nil); w.Code != http.StatusOK {
		t.Errorf("status = %d", w.Code)
	}
	if got := srv.Calls()[0].Operation; got != "listDroplets" {
		t.Errorf("journal names %q", got)
	}
}
