// Package openapitemplate turns an OpenAPI document into element-template packages —
// one per operation — in the shape [github.com/pblumer/atlas/api]'s repository catalog
// already uses (ADR-0027 chose the bpmn.io element-templates schema; ADR-0081 wraps one
// in a package with an id, a version and an author).
//
// It is the reader of [github.com/pblumer/atlas/connector/rest/openapimock] pointed the
// other way. That package makes a document answer like the API it describes; this one
// makes it configure the task that calls it, so the same file serves both halves of
// trying an integration out.
//
// # What a generated package can and cannot do today
//
// It cannot be applied to a task: the applier is ADR-0212, and that record is Proposed
// with its implementation not started. It cannot be loaded into a running server
// either — the catalog is compiled in (`//go:embed repository_catalog/*.json`) and
// installing takes a catalog id, not a file. What a generated package is, today, is the
// artifact those two things are specified to consume, and a file somebody can commit
// into the catalog for an API worth shipping with Atlas. Saying that here beats letting
// the next reader discover it.
//
// # What it fills in, and what it leaves alone
//
// Method is fixed to the operation's. The URL is the document's first server plus the
// operation's path — literal where the path has nothing to substitute, and a FEEL
// expression where it does, naming each path parameter as a variable the process is
// expected to hold. Everything a document cannot decide for a person is left as the
// REST task's own empty field: headers, authentication, the credential reference. A
// document's `securitySchemes` are deliberately not mapped onto Atlas's auth types —
// the useful ones need a token endpoint and a client id that live on the server, and
// guessing them into a template would put a wrong answer in front of somebody.
package openapitemplate

import (
	"fmt"
	"strings"

	"github.com/pblumer/atlas/connector/rest/openapimock"
)

// templateSchema is the element-templates schema the bundled catalog declares. It is
// repeated rather than shared because the catalog's copy is data in a JSON file.
const templateSchema = "https://unpkg.com/@camunda/element-templates-json-schema/resources/schema.json"

// engineCompat matches what the bundled packages state.
const engineCompat = ">=0.9"

// Package is one repository package: an element template plus what the catalog needs to
// list, version and attribute it. The JSON tags are the catalog's own field names.
type Package struct {
	ID           string   `json:"id"`
	Version      string   `json:"version"`
	Kind         string   `json:"kind"`
	Title        string   `json:"title"`
	Author       string   `json:"author"`
	Description  string   `json:"description"`
	EngineCompat string   `json:"engineCompat"`
	Template     Template `json:"template"`

	slug string // the operation's own part of the id, for a file name
}

// Template is the element template itself.
type Template struct {
	Schema     string     `json:"$schema"`
	Name       string     `json:"name"`
	ID         string     `json:"id"`
	AppliesTo  []string   `json:"appliesTo"`
	Properties []Property `json:"properties"`
}

// Property is one field of a template, bound to an attribute of the extension element
// the task carries.
type Property struct {
	Label   string   `json:"label"`
	Type    string   `json:"type"`
	Value   string   `json:"value,omitempty"`
	Feel    string   `json:"feel,omitempty"`
	Binding Binding  `json:"binding"`
	Choices []Choice `json:"choices,omitempty"`
}

// Binding says which attribute a property writes.
type Binding struct {
	Type string `json:"type"`
	Name string `json:"name"`
}

// Choice is one option of a Dropdown.
type Choice struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

// Filename is the file this package belongs in, matching the catalog's one-package-
// per-file layout.
func (p Package) Filename() string { return p.slug + ".json" }

// Packages generates one package per operation, in the order the document compiled to.
func Packages(spec *openapimock.Spec) []Package {
	api := slug(spec.Title)
	if api == "" {
		api = "api"
	}
	out := make([]Package, 0, len(spec.Operations))
	for _, op := range spec.Operations {
		out = append(out, packageFor(spec, api, op))
	}
	return out
}

// packageFor builds the package for one operation.
func packageFor(spec *openapimock.Spec, api string, op openapimock.Operation) Package {
	name := op.ID
	if name == "" {
		// An operation without an operationId is still addressable by what it is,
		// which is also what a person would call it.
		name = op.Method + " " + op.Path
	}
	opSlug := slug(name)
	id := "openapi." + api + "." + opSlug

	title := strings.TrimSpace(spec.Title + " · " + label(op))
	version := spec.Version
	if strings.TrimSpace(version) == "" {
		version = "0.0.0"
	}

	return Package{
		ID:           id,
		Version:      version,
		Kind:         "connector",
		Title:        title,
		Author:       "generated from " + strings.TrimSpace(spec.Title+" "+spec.Version),
		Description:  describe(spec, op),
		EngineCompat: engineCompat,
		Template: Template{
			Schema:     templateSchema,
			Name:       title,
			ID:         id,
			AppliesTo:  []string{"bpmn:ServiceTask"},
			Properties: properties(spec, op),
		},
		slug: opSlug,
	}
}

// properties are the fields of the generated template: what the document decided,
// filled in; what it cannot decide, left empty for the person configuring the task.
func properties(spec *openapimock.Spec, op openapimock.Operation) []Property {
	result := ""
	if op.ID != "" {
		result = op.ID + "Result"
	}
	return []Property{
		{
			Label: "Method", Type: "Dropdown", Value: op.Method,
			Binding: Binding{Type: "property", Name: "atlas:method"},
			// One choice, because the operation is one method. A dropdown that
			// offered the others would offer a request this template does not describe.
			Choices: []Choice{{Name: op.Method, Value: op.Method}},
		},
		{
			Label: "URL", Type: "String", Value: urlValue(spec, op), Feel: "optional",
			Binding: Binding{Type: "property", Name: "atlas:url"},
		},
		{Label: "Headers", Type: "String", Feel: "optional", Binding: Binding{Type: "property", Name: "atlas:headers"}},
		{
			Label: "Authentication", Type: "Dropdown", Value: "none",
			Binding: Binding{Type: "property", Name: "atlas:authType"},
			Choices: []Choice{
				{Name: "None", Value: "none"},
				{Name: "Basic (server credential)", Value: "basic"},
				{Name: "Bearer (server credential)", Value: "bearer"},
			},
		},
		{Label: "Credential reference", Type: "String", Binding: Binding{Type: "property", Name: "atlas:credentialsRef"}},
		{Label: "Result variable", Type: "String", Value: result, Binding: Binding{Type: "property", Name: "atlas:resultVariable"}},
	}
}

// urlValue is the address this operation is called at: the document's first server plus
// the operation's path. A path with parameters becomes a FEEL expression that reads each
// one from a variable, because the document knows where the value goes and only the
// process knows what it is.
func urlValue(spec *openapimock.Spec, op openapimock.Operation) string {
	parts := splitTemplate(spec.ServerURL + op.Path)
	var expression []string
	literal := true
	for _, part := range parts {
		if part.param {
			literal = false
			expression = append(expression, "string("+part.text+")")
			continue
		}
		expression = append(expression, quote(part.text))
	}
	if literal {
		return spec.ServerURL + op.Path
	}
	return "=" + strings.Join(expression, " + ")
}

// pathPart is one piece of a path template: a run of literal text, or a {parameter}.
type pathPart struct {
	text  string
	param bool
}

// splitTemplate splits a path template into its literal runs and its parameters.
//
// A brace that never closes is literal text. The document's own reader refuses such a
// path before it can reach here, so this is a guard on a Spec somebody built by hand
// rather than a case a document produces — but inventing a parameter out of it would
// put a variable in the URL that nothing will ever set.
func splitTemplate(s string) []pathPart {
	var out []pathPart
	rest := s
	for {
		open := strings.IndexByte(rest, '{')
		if open < 0 {
			break
		}
		closing := strings.IndexByte(rest[open:], '}')
		if closing < 0 {
			break
		}
		closing += open
		if open > 0 {
			out = append(out, pathPart{text: rest[:open]})
		}
		out = append(out, pathPart{text: rest[open+1 : closing], param: true})
		rest = rest[closing+1:]
	}
	if rest != "" {
		out = append(out, pathPart{text: rest})
	}
	return out
}

// pathParameters lists the {names} a path template carries, in order.
func pathParameters(path string) []string {
	var out []string
	for _, part := range splitTemplate(path) {
		if part.param {
			out = append(out, part.text)
		}
	}
	return out
}

// describe says what the operation is, where it goes, and what the template could not
// fill in — which is the half a person needs before they can use it.
func describe(spec *openapimock.Spec, op openapimock.Operation) string {
	var b strings.Builder
	if summary := strings.TrimSpace(op.Summary); summary != "" {
		b.WriteString(summary)
		b.WriteString(" — ")
	}
	fmt.Fprintf(&b, "%s %s", op.Method, op.Path)
	if params := pathParameters(op.Path); len(params) > 0 {
		fmt.Fprintf(&b, ". The URL is an expression over %s, which the process must hold by the time the task runs",
			listOf(params))
	}
	// A server may be absent, and it may also be relative — Swagger's own Petstore
	// declares "/api/v3". Both leave a URL no task can call, and the second is the one
	// nobody notices, because the field looks filled in.
	if !strings.Contains(spec.ServerURL, "://") {
		if spec.ServerURL == "" {
			b.WriteString(". The document names no server, so the URL carries no host: point it at one before the task can call anything")
		} else {
			fmt.Fprintf(&b, ". The document's server is %q, so the URL carries no host: point it at one before the task can call anything", spec.ServerURL)
		}
	}
	b.WriteString(".")
	return b.String()
}

// label names the operation for a human: its summary where it has one, its operationId
// otherwise, and what it is when it has neither.
func label(op openapimock.Operation) string {
	if summary := strings.TrimSpace(op.Summary); summary != "" {
		return summary
	}
	if op.ID != "" {
		return op.ID
	}
	return op.Method + " " + op.Path
}

// listOf renders names as prose: "a", "a and b", "a, b and c".
func listOf(names []string) string {
	if len(names) < 2 {
		return strings.Join(names, "")
	}
	return strings.Join(names[:len(names)-1], ", ") + " and " + names[len(names)-1]
}

// quote renders a literal as a FEEL string.
func quote(s string) string {
	return `"` + strings.ReplaceAll(s, `"`, `\"`) + `"`
}

// slug reduces a name to the lowercase, hyphen-separated form an id and a file name
// are made of.
func slug(s string) string {
	var b strings.Builder
	dash := false
	for _, r := range strings.ToLower(s) {
		switch {
		case (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9'):
			b.WriteRune(r)
			dash = false
		default:
			if !dash && b.Len() > 0 {
				b.WriteByte('-')
				dash = true
			}
		}
	}
	return strings.TrimRight(b.String(), "-")
}
