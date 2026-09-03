package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pblumer/atlas/connector/rest/openapimock"
)

// writeSpec puts an OpenAPI document on disk and returns its path.
func writeSpec(t *testing.T, doc string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "spec.yaml")
	if err := os.WriteFile(path, []byte(doc), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

const twoOperations = `
openapi: 3.0.0
info: {title: Petstore, version: '1.0'}
servers: [{url: 'https://api.example.com/v1'}]
paths:
  /pets:
    get:
      operationId: listPets
      responses:
        '200': {description: ok}
    post:
      operationId: createPet
      responses:
        '201': {description: created}
`

func TestRunMockOpenAPIRequiresADocument(t *testing.T) {
	err := runMockOpenAPI(nil)
	if err == nil || !strings.Contains(err.Error(), "--spec") {
		t.Errorf("err = %v, want it to ask for --spec", err)
	}
}

func TestRunMockOpenAPIRefusesABrokenDocumentBeforeListening(t *testing.T) {
	// The document is compiled before the port is bound, so a broken one fails in the
	// terminal that started it rather than on the first call.
	path := writeSpec(t, "openapi: 3.0.0\n")
	err := runMockOpenAPI([]string{"--spec", path})
	if err == nil || !strings.Contains(err.Error(), "no paths") {
		t.Errorf("err = %v, want the load error", err)
	}
}

func TestMockOpenAPIBannerSaysWhatToDoWithIt(t *testing.T) {
	spec, err := openapimock.Load([]byte(twoOperations))
	if err != nil {
		t.Fatal(err)
	}
	mock := openapimock.New(spec)
	var out bytes.Buffer
	printMockOpenAPIBanner(&out, spec, mock, "petstore.yaml", ":8009", "http://127.0.0.1:8009")
	banner := out.String()
	for _, want := range []string{
		"Petstore 1.0 — 2 operations from petstore.yaml, listening on :8009",
		"GET    http://127.0.0.1:8009/v1/pets",
		"POST   http://127.0.0.1:8009/v1/pets",
		"journal: GET http://127.0.0.1:8009/__mock/calls",
		"report:  GET http://127.0.0.1:8009/__mock/report",
		"point a REST worker task's url at http://127.0.0.1:8009/v1/…",
		"Prefer: code=404",
	} {
		if !strings.Contains(banner, want) {
			t.Errorf("banner missing %q:\n%s", want, banner)
		}
	}
}

func TestMockOpenAPIBannerFollowsAnOverriddenBasePath(t *testing.T) {
	spec, err := openapimock.Load([]byte(twoOperations))
	if err != nil {
		t.Fatal(err)
	}
	mock := openapimock.New(spec, openapimock.WithBasePath("/"))
	var out bytes.Buffer
	printMockOpenAPIBanner(&out, spec, mock, "petstore.yaml", ":8009", "http://127.0.0.1:8009")
	if got := out.String(); !strings.Contains(got, "GET    http://127.0.0.1:8009/pets") {
		t.Errorf("banner does not follow --base-path:\n%s", got)
	}
}

func TestMockOpenAPIBannerCutsALongList(t *testing.T) {
	doc := "openapi: 3.0.0\ninfo: {title: Big, version: '1'}\npaths:\n"
	for i := range 13 {
		doc += "  /p" + string(rune('a'+i)) + ":\n    get:\n      responses:\n        '200': {description: ok}\n"
	}
	spec, err := openapimock.Load([]byte(doc))
	if err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	printMockOpenAPIBanner(&out, spec, openapimock.New(spec), "big.yaml", ":8009", "http://127.0.0.1:8009")
	if got := out.String(); !strings.Contains(got, "… and 3 more") {
		t.Errorf("banner does not cut the list:\n%s", got)
	}
}

func TestRunMockServerReportsAnUnusableAddress(t *testing.T) {
	if err := runMockServer("127.0.0.1:-1", nil); err == nil {
		t.Error("want an error for an address that cannot be bound")
	}
}

func TestMockOpenAPIBannerNamesWhatItCannotGenerate(t *testing.T) {
	// Found against Swagger's own Petstore, which describes every response as JSON and
	// XML: the XML half is dropped, and the person starting the mock is told.
	spec, err := openapimock.Load([]byte(`
openapi: 3.0.0
info: {title: Petstore, version: '1'}
paths:
  /pet:
    get:
      responses:
        '200':
          description: a pet
          content:
            application/json: {schema: {type: object}}
            application/xml: {schema: {type: object}}
`))
	if err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	printMockOpenAPIBanner(&out, spec, openapimock.New(spec), "petstore.yaml", ":8009", "http://127.0.0.1:8009")
	if got := out.String(); !strings.Contains(got, "no body for GET /pet 200 application/xml") {
		t.Errorf("banner does not name the dropped media type:\n%s", got)
	}
}

// writeSplitSpec writes a two-file document and returns the entry file's path.
func writeSplitSpec(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "spec", "resources"), 0o750); err != nil {
		t.Fatal(err)
	}
	files := map[string]string{
		"spec/root.yaml":        "openapi: 3.0.0\ninfo: {title: Split, version: '1'}\npaths:\n  /x:\n    get: {$ref: 'resources/op.yml'}\n  /y:\n    get: {$ref: '../outside.yml'}\n",
		"spec/resources/op.yml": "operationId: getX\nresponses:\n  '200': {description: ok}\n",
		"outside.yml":           "operationId: getY\nresponses:\n  '200': {description: ok}\n",
	}
	for name, content := range files {
		if err := os.WriteFile(filepath.Join(dir, filepath.FromSlash(name)), []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return filepath.Join(dir, "spec", "root.yaml")
}

func TestRunMockOpenAPIKeepsRefsInsideTheSpecsDirectory(t *testing.T) {
	// The default root is the document's own directory. A document reaching past it is
	// refused before the port is bound, and the message says which flag says otherwise.
	err := runMockOpenAPI([]string{"--spec", writeSplitSpec(t)})
	if err == nil || !strings.Contains(err.Error(), "--spec-root") {
		t.Errorf("err = %v, want the refusal to name --spec-root", err)
	}
}

func TestMockOpenAPIBannerCountsTheFilesItRead(t *testing.T) {
	entry := writeSplitSpec(t)
	spec, err := openapimock.LoadFileUnder(entry, filepath.Dir(filepath.Dir(entry)))
	if err != nil {
		t.Fatalf("LoadFileUnder: %v", err)
	}
	var out bytes.Buffer
	printMockOpenAPIBanner(&out, spec, openapimock.New(spec), "root.yaml", ":8009", "http://127.0.0.1:8009")
	if got := out.String(); !strings.Contains(got, "from root.yaml and 2 files") {
		t.Errorf("banner does not say what it read:\n%s", got)
	}
}
