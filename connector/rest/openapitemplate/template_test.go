package openapitemplate_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/pblumer/atlas/connector/rest/openapimock"
	"github.com/pblumer/atlas/connector/rest/openapitemplate"
)

// load compiles a document for the cases below.
func load(t *testing.T, doc string) *openapimock.Spec {
	t.Helper()
	spec, err := openapimock.Load([]byte(doc))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	return spec
}

const petstore = `
openapi: 3.0.0
info: {title: Swagger Petstore, version: '1.0.11'}
servers: [{url: 'https://petstore3.swagger.io/api/v3'}]
paths:
  /pet/findByStatus:
    get:
      operationId: findPetsByStatus
      summary: Finds pets by status
      responses:
        '200': {description: ok}
  /pet/{petId}:
    get:
      operationId: getPetById
      summary: Find pet by ID
      responses:
        '200': {description: ok}
    delete:
      responses:
        '204': {description: gone}
`

// byID finds one generated package.
func byID(t *testing.T, packages []openapitemplate.Package, id string) openapitemplate.Package {
	t.Helper()
	for _, p := range packages {
		if p.ID == id {
			return p
		}
	}
	var ids []string
	for _, p := range packages {
		ids = append(ids, p.ID)
	}
	t.Fatalf("no package %q; got %v", id, ids)
	return openapitemplate.Package{}
}

// property finds one bound field of a template.
func property(t *testing.T, p openapitemplate.Package, binding string) openapitemplate.Property {
	t.Helper()
	for _, prop := range p.Template.Properties {
		if prop.Binding.Name == binding {
			return prop
		}
	}
	t.Fatalf("package %s has no property bound to %s", p.ID, binding)
	return openapitemplate.Property{}
}

func TestOnePackagePerOperation(t *testing.T) {
	packages := openapitemplate.Packages(load(t, petstore))
	if len(packages) != 3 {
		t.Fatalf("got %d packages, want 3", len(packages))
	}
	// The operationId is the stable name a person recognises; an operation without one
	// is still addressable, by what it is.
	byID(t, packages, "openapi.swagger-petstore.getpetbyid")
	byID(t, packages, "openapi.swagger-petstore.delete-pet-petid")
}

func TestAPathWithoutParametersGetsALiteralURL(t *testing.T) {
	p := byID(t, openapitemplate.Packages(load(t, petstore)), "openapi.swagger-petstore.findpetsbystatus")
	url := property(t, p, "atlas:url")
	if want := "https://petstore3.swagger.io/api/v3/pet/findByStatus"; url.Value != want {
		t.Errorf("url = %q, want %q", url.Value, want)
	}
	if strings.HasPrefix(url.Value, "=") {
		t.Error("a path with nothing to substitute should not be a FEEL expression")
	}
}

func TestAPathParameterBecomesAFEELExpression(t *testing.T) {
	p := byID(t, openapitemplate.Packages(load(t, petstore)), "openapi.swagger-petstore.getpetbyid")
	url := property(t, p, "atlas:url")
	const want = `="https://petstore3.swagger.io/api/v3/pet/" + string(petId)`
	if url.Value != want {
		t.Errorf("url\n got %s\nwant %s", url.Value, want)
	}
	// A field the modeller may edit as an expression, and a description that says
	// which variable the expression is reaching for — the template cannot supply it.
	if url.Feel != "optional" {
		t.Errorf("feel = %q, want optional", url.Feel)
	}
	if !strings.Contains(p.Description, "petId") {
		t.Errorf("description does not name the variable the URL expects: %q", p.Description)
	}
}

func TestSeveralParametersInOneSegment(t *testing.T) {
	spec := load(t, `
openapi: 3.0.0
info: {title: Reports, version: '1'}
servers: [{url: 'https://r.example.com'}]
paths:
  /reports/{year}-{month}.csv:
    get:
      operationId: monthly
      responses:
        '200': {description: ok}
`)
	url := property(t, byID(t, openapitemplate.Packages(spec), "openapi.reports.monthly"), "atlas:url")
	const want = `="https://r.example.com/reports/" + string(year) + "-" + string(month) + ".csv"`
	if url.Value != want {
		t.Errorf("url\n got %s\nwant %s", url.Value, want)
	}
}

func TestTheMethodIsFixedToTheOperations(t *testing.T) {
	packages := openapitemplate.Packages(load(t, petstore))
	method := property(t, byID(t, packages, "openapi.swagger-petstore.delete-pet-petid"), "atlas:method")
	if method.Value != "DELETE" || len(method.Choices) != 1 || method.Choices[0].Value != "DELETE" {
		t.Errorf("method = %+v, want DELETE as the only choice", method)
	}
}

func TestTheResultVariableIsNamedAfterTheOperation(t *testing.T) {
	p := byID(t, openapitemplate.Packages(load(t, petstore)), "openapi.swagger-petstore.getpetbyid")
	if got := property(t, p, "atlas:resultVariable").Value; got != "getPetByIdResult" {
		t.Errorf("result variable = %q", got)
	}
}

func TestAServerWithoutAHostSaysSo(t *testing.T) {
	// Swagger's own Petstore declares its server as "/api/v3" — a path, no host. The
	// generated URL is then as uncallable as one with no server at all, and the reason
	// a person would not spot it is that the field looks filled in.
	spec := load(t, "openapi: 3.0.0\ninfo: {title: P, version: '1'}\nservers: [{url: '/api/v3'}]\npaths:\n  /pet:\n    get: {operationId: getPet, responses: {'200': {description: ok}}}\n")
	p := openapitemplate.Packages(spec)[0]
	if got := property(t, p, "atlas:url").Value; got != "/api/v3/pet" {
		t.Errorf("url = %q, want the document's own relative address", got)
	}
	if !strings.Contains(p.Description, "no host") {
		t.Errorf("description = %q, want it to say the address carries no host", p.Description)
	}
}

func TestADocumentWithNoServerSaysSo(t *testing.T) {
	// Without a server the URL is a path, which no task can call. Generating it anyway
	// and saying nothing would hand somebody a template that fails at run time.
	spec := load(t, "openapi: 3.0.0\ninfo: {title: X, version: '1'}\npaths:\n  /x:\n    get: {operationId: getX, responses: {'200': {description: ok}}}\n")
	p := openapitemplate.Packages(spec)[0]
	if !strings.Contains(p.Description, "no host") {
		t.Errorf("description = %q, want it to say the address carries no host", p.Description)
	}
	if got := property(t, p, "atlas:url").Value; got != "/x" {
		t.Errorf("url = %q, want the bare path", got)
	}
}

func TestThePackageIsShapedLikeTheBundledCatalog(t *testing.T) {
	p := byID(t, openapitemplate.Packages(load(t, petstore)), "openapi.swagger-petstore.getpetbyid")
	encoded, err := json.Marshal(p)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var got map[string]any
	if err := json.Unmarshal(encoded, &got); err != nil {
		t.Fatal(err)
	}
	// The keys api/repository.go requires of every package it serves, and the ones the
	// bundled catalog carries beside them.
	for _, key := range []string{"id", "version", "kind", "title", "author", "description", "engineCompat", "template"} {
		if _, ok := got[key]; !ok {
			t.Errorf("package has no %q", key)
		}
	}
	if got["kind"] != "connector" {
		t.Errorf("kind = %v, want connector (api/repository.go accepts connector, service-task, script-task)", got["kind"])
	}
	if got["version"] != "1.0.11" {
		t.Errorf("version = %v, want the document's", got["version"])
	}
	tmpl, _ := got["template"].(map[string]any)
	if tmpl["$schema"] != "https://unpkg.com/@camunda/element-templates-json-schema/resources/schema.json" {
		t.Errorf("$schema = %v", tmpl["$schema"])
	}
	applies, _ := tmpl["appliesTo"].([]any)
	if len(applies) != 1 || applies[0] != "bpmn:ServiceTask" {
		t.Errorf("appliesTo = %v", applies)
	}
}

func TestFilenamesAreStableAndDistinct(t *testing.T) {
	seen := map[string]bool{}
	for _, p := range openapitemplate.Packages(load(t, petstore)) {
		name := p.Filename()
		if !strings.HasSuffix(name, ".json") || strings.ContainsAny(name, "/\\ ") {
			t.Errorf("filename %q is not a plain json file name", name)
		}
		if seen[name] {
			t.Errorf("filename %q is used twice", name)
		}
		seen[name] = true
	}
}

func TestADocumentThatNamesItselfNothing(t *testing.T) {
	// A document with no title and no version still has to produce a package the
	// catalog would accept: id, version and title are all required there.
	spec := load(t, "openapi: 3.0.0\npaths:\n  /x:\n    get: {operationId: getX, responses: {'200': {description: ok}}}\n")
	p := openapitemplate.Packages(spec)[0]
	if p.ID != "openapi.api.getx" {
		t.Errorf("id = %q, want a package id without a name to build on", p.ID)
	}
	if p.Version != "0.0.0" {
		t.Errorf("version = %q, want a stand-in the catalog accepts", p.Version)
	}
	if strings.TrimSpace(p.Title) == "" {
		t.Error("title is blank, which the catalog refuses")
	}
}
