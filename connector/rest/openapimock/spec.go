// Package openapimock serves a mock REST API from an OpenAPI 3 document, so a process
// with a REST task (ADR-0067) can be run end to end before the API it calls
// exists — or without pointing a draft at the real one (ADR-0217).
//
// It is the generic sibling of the Remedy mock (package
// [github.com/pblumer/atlas/connector/remedy/mock]): that one hand-implements the three
// endpoints one Worker Type calls, this one implements whatever a document describes.
// Run it with `atlas mock-openapi --spec petstore.yaml` and point a REST task's URL at
// the address it prints; nothing else about the model changes.
//
// What it serves is decided at load time, not per request ("compile, don't interpret" —
// the engine's habit, kept here because it moves every schema error to startup where a
// person is watching): every operation is compiled into a matcher and every response
// into the exact bytes it will answer with. A response body is the document's own
// `example` where it states one, the first of its named `examples` where it states
// those, and otherwise a value generated from the schema — deterministically, so two
// runs of a demo produce the same output and a test can assert on it.
//
// A document published as a tree of files — how most large APIs ship — is read as one,
// with every reference resolved against the directory of the file it is written in.
// What may be read is bounded to the document's own directory unless [LoadFileUnder]
// widens it: this mock serves what it reads and authenticates nobody, so the files it
// opens are not the document's decision alone.
//
// A response described only in a media type this mock cannot generate loses its body
// instead of being answered with JSON wearing that media type's label.
//
// It mocks; it does not validate. A request body is recorded, never checked against the
// schema, and a security scheme is not enforced. What it does refuse is everything it
// cannot honestly serve: a `$ref` it cannot resolve or a status it was never given
// (`Prefer: code=418`) is an error, not a quiet 200, because a mock that answers
// anything teaches a model to be wrong (ADR-0181).
//
// It has no authentication, and neither does the journal below: it is a development aid
// that holds invented data and whatever a test run sent it, in the shape of the
// [github.com/pblumer/atlas/connector/remedy/mock] server it follows. Bind it where the
// worker calling it can reach it and no further.
//
// Two endpoints outside the document answer "what did my run actually do":
// `GET /__mock/calls` is the journal of served calls, and `GET /__mock/report` is that
// journal and the operation table in the envelope the Console's Mockups view takes
// (ADR-0216), so this mock reports into the same place as every
// other one rather than becoming a fourth place to look.
package openapimock

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

// methods are the HTTP methods an OpenAPI path item may carry. Every other key of a
// path item (summary, description, servers, parameters, an x- extension) describes
// something this mock does not serve, and is skipped rather than refused.
var methods = []string{"get", "put", "post", "delete", "options", "head", "patch", "trace"}

// Spec is a compiled OpenAPI document: what to serve, ready to serve.
type Spec struct {
	Title   string
	Version string
	// BasePath is the path component of the first server URL, without a trailing
	// slash ("" when there is none). Requests are served under it, so a document
	// written against https://api.example.com/v1 mocks /v1/… exactly as the real API
	// does and a task's URL needs only its host changed.
	BasePath string
	// Operations are ordered most-specific first, which is also the order the matcher
	// walks: /pets/mine must be tried before /pets/{petId} or the literal path is
	// unreachable.
	Operations []Operation
	// Skipped names the responses this mock will not answer with a body because the
	// document describes them only in a media type it cannot generate — "GET /pet/{id}
	// 200 application/xml". It is here to be said out loud at startup: a dropped media
	// type is a small hole in the mock, and a person who is told about it once will not
	// spend an afternoon on the 406 it produces later.
	Skipped []string
	// Files is how many other documents this one was assembled from — 0 for the single
	// file most documents are, and the size of the tree for one published as many.
	Files int
}

// Operation is one method on one path.
type Operation struct {
	Method  string // upper-case, e.g. "GET"
	Path    string // the template as written, e.g. "/pets/{petId}"
	ID      string // operationId, "" when the document states none
	Summary string
	// Responses hold one entry per (status, media type) the document describes,
	// ordered by status and then media type.
	Responses []Response

	segments []segment // the compiled path matcher
}

// Response is one answer this mock can give: a status, a media type, and the bytes.
//
// Status 0 is the document's `default` response, which answers 200 on the wire — the
// distinction is kept because `Prefer: code=…` addresses what the document states.
type Response struct {
	Status  int
	Media   string // "" when the response has no content
	Body    []byte // served verbatim; JSON for a JSON media type, the raw text otherwise
	Named   map[string][]byte
	Headers []Header
}

// Header is one response header and the value this mock sends for it.
type Header struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

// Name is what this document calls itself, for a banner or a card to say what is being
// mocked. A document that names itself nothing still needs naming to a person.
func (s *Spec) Name() string {
	if name := strings.TrimSpace(s.Title + " " + s.Version); name != "" {
		return name
	}
	return "an untitled API"
}

// LoadFile reads an OpenAPI document from disk and compiles it. JSON and YAML are both
// accepted; the extension is not consulted, the content is.
//
// A document that refers to other files — how most large APIs are published — is
// followed, with each reference resolved against the directory of the file it is
// written in. Those files must live under the document's own directory; use
// [LoadFileUnder] for a layout whose shared parts sit beside it rather than below.
func LoadFile(path string) (*Spec, error) {
	return LoadFileUnder(path, filepath.Dir(path))
}

// LoadFileUnder is [LoadFile] with the directory the document's references may reach
// stated explicitly. Nothing outside root is read: the mock serves what it reads and
// authenticates nobody, so the files it may open are not the document's decision alone.
func LoadFileUnder(path, root string) (*Spec, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(abs)
	if err != nil {
		return nil, err
	}
	spec, err := compile(data, abs, absRoot)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return spec, nil
}

// Load compiles an OpenAPI 3 document held in memory. With no file behind it there is
// no directory to resolve against, so a reference to another file is refused rather
// than read as an empty object.
func Load(data []byte) (*Spec, error) {
	return compile(data, "", "")
}

// compile turns a document into the mock it describes. path and root are empty when the
// document came from bytes rather than from a file.
func compile(data []byte, path, root string) (*Spec, error) {
	doc, err := decode(data)
	if err != nil {
		return nil, err
	}
	if _, ok := doc["swagger"]; ok {
		return nil, fmt.Errorf("this is a Swagger 2.0 document; only OpenAPI 3 documents can be mocked")
	}
	paths, _ := doc["paths"].(map[string]any)
	if len(paths) == 0 {
		return nil, fmt.Errorf("the document has no paths to serve")
	}

	spec := &Spec{}
	if info, ok := doc["info"].(map[string]any); ok {
		spec.Title, _ = info["title"].(string)
		spec.Version, _ = info["version"].(string)
	}
	if spec.BasePath, err = basePath(doc); err != nil {
		return nil, err
	}

	files := &documents{root: root, byPath: map[string]map[string]any{}}
	if path != "" {
		files.byPath[path] = doc
	}
	gen := &generator{doc: doc, path: path, files: files, active: map[string]bool{}}
	for _, route := range sortedKeys(paths) {
		if strings.HasPrefix(route, "x-") {
			continue
		}
		if !strings.HasPrefix(route, "/") {
			return nil, fmt.Errorf("path %q must start with %q", route, "/")
		}
		if err := compilePathItem(spec, gen, route, paths[route]); err != nil {
			return nil, err
		}
	}
	spec.Files = len(files.byPath)
	if spec.Files > 0 {
		spec.Files-- // the entry document is not one of the files it refers to
	}
	if len(spec.Operations) == 0 {
		return nil, fmt.Errorf("the document has no paths to serve")
	}
	sortOperations(spec.Operations)
	return spec, nil
}

// decode reads a document as JSON or YAML. The content decides: a document starting
// with "{" is JSON (and read with UseNumber, so a large integer id survives), anything
// else goes through the YAML parser — which would also accept most JSON, but not JSON
// indented with tabs.
func decode(data []byte) (map[string]any, error) {
	var value any
	if trimmed := strings.TrimLeft(string(data), " \t\r\n"); strings.HasPrefix(trimmed, "{") {
		dec := json.NewDecoder(strings.NewReader(trimmed))
		dec.UseNumber()
		if err := dec.Decode(&value); err != nil {
			return nil, fmt.Errorf("parse the document as JSON: %w", err)
		}
	} else if err := yaml.Unmarshal(data, &value); err != nil {
		return nil, fmt.Errorf("parse the document as YAML: %w", err)
	}
	doc, ok := normalize(value).(map[string]any)
	if !ok {
		if value == nil {
			return nil, fmt.Errorf("the document is empty: no paths to serve")
		}
		return nil, fmt.Errorf("not an OpenAPI document: the top level is not a mapping")
	}
	return doc, nil
}

// normalize turns whatever the YAML decoder produced into the map[string]any /
// []any / scalar shape the rest of this package walks. YAML permits non-string keys;
// a document that uses one is not an OpenAPI document, but rendering the key rather
// than refusing keeps the error where it belongs — at the field that used it.
func normalize(value any) any {
	switch v := value.(type) {
	case map[string]any:
		for key, elem := range v {
			v[key] = normalize(elem)
		}
		return v
	case map[any]any:
		out := make(map[string]any, len(v))
		for key, elem := range v {
			out[fmt.Sprint(key)] = normalize(elem)
		}
		return out
	case []any:
		for i, elem := range v {
			v[i] = normalize(elem)
		}
		return v
	default:
		return value
	}
}

// basePath is the path of the first server URL, trimmed of its trailing slash. A
// document with no servers, or one whose server is a bare host, mocks at the root.
func basePath(doc map[string]any) (string, error) {
	servers, _ := doc["servers"].([]any)
	if len(servers) == 0 {
		return "", nil
	}
	first, _ := servers[0].(map[string]any)
	raw, _ := first["url"].(string)
	if raw == "" {
		return "", nil
	}
	parsed, err := url.Parse(raw)
	if err != nil {
		return "", fmt.Errorf("server url %q: %w", raw, err)
	}
	return strings.TrimSuffix(parsed.Path, "/"), nil
}

// compilePathItem compiles every operation on one path.
//
// A path item, an operation and a response may each be a $ref rather than the thing
// itself — into this document's components, or into another file. Reading the reference
// as an empty object is how a mock quietly stops mocking, so every one of them is
// followed, and the document it leads to stays current for as long as the caller is
// reading inside it.
func compilePathItem(spec *Spec, gen *generator, route string, raw any) error {
	node, leave, err := gen.deref(raw)
	if err != nil {
		return fmt.Errorf("path %q: %w", route, err)
	}
	defer leave()
	item, ok := node.(map[string]any)
	if !ok {
		return fmt.Errorf("path %q: expected a mapping", route)
	}
	segs, err := compilePath(route)
	if err != nil {
		return err
	}
	for _, method := range methods {
		operation, present := item[method]
		if !present {
			continue
		}
		if err := compileOperation(spec, gen, route, method, segs, operation); err != nil {
			return err
		}
	}
	return nil
}

// compileOperation compiles one method on one path into what it answers.
func compileOperation(spec *Spec, gen *generator, route, method string, segs []segment, raw any) error {
	node, leave, err := gen.deref(raw)
	if err != nil {
		return fmt.Errorf("%s %s: %w", strings.ToUpper(method), route, err)
	}
	defer leave()
	opDoc, ok := node.(map[string]any)
	if !ok {
		return fmt.Errorf("%s %s: expected a mapping", method, route)
	}
	op := Operation{Method: strings.ToUpper(method), Path: route, segments: segs}
	op.ID, _ = opDoc["operationId"].(string)
	op.Summary, _ = opDoc["summary"].(string)
	responses, skipped, err := compileResponses(opDoc, gen)
	if err != nil {
		return fmt.Errorf("%s %s: %w", op.Method, route, err)
	}
	op.Responses = responses
	for _, entry := range skipped {
		spec.Skipped = append(spec.Skipped, fmt.Sprintf("%s %s %s", op.Method, route, entry))
	}
	spec.Operations = append(spec.Operations, op)
	return nil
}

// compileResponses turns an operation's responses into the answers this mock can give,
// one per status and media type, ordered by status and then media type.
func compileResponses(opDoc map[string]any, gen *generator) ([]Response, []string, error) {
	responses, _ := opDoc["responses"].(map[string]any)
	var out []Response
	var skipped []string
	for _, key := range sortedKeys(responses) {
		if strings.HasPrefix(key, "x-") {
			continue
		}
		status, err := statusOf(key)
		if err != nil {
			return nil, nil, err
		}
		got, dropped, err := compileResponse(gen, key, status, responses[key])
		if err != nil {
			return nil, nil, err
		}
		out = append(out, got...)
		skipped = append(skipped, dropped...)
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Status != out[j].Status {
			return out[i].Status < out[j].Status
		}
		return out[i].Media < out[j].Media
	})
	return out, skipped, nil
}

// compileResponse compiles one status of one operation: an entry per media type it can
// answer in, and the name of every media type it had to drop.
func compileResponse(gen *generator, key string, status int, raw any) ([]Response, []string, error) {
	node, leave, err := gen.deref(raw)
	if err != nil {
		return nil, nil, fmt.Errorf("response %q: %w", key, err)
	}
	defer leave()
	respDoc, ok := node.(map[string]any)
	if !ok {
		return nil, nil, fmt.Errorf("response %q: expected a mapping", key)
	}
	headers, err := compileHeaders(respDoc, gen)
	if err != nil {
		return nil, nil, err
	}
	content, _ := respDoc["content"].(map[string]any)
	if len(content) == 0 {
		return []Response{{Status: status, Headers: headers}}, nil, nil
	}
	var out []Response
	var skipped []string
	for _, media := range sortedKeys(content) {
		body, named, err := compileBody(content[media], media, gen)
		if errors.Is(err, errUnserveableMedia) {
			// The document describes this response in a shape this mock cannot
			// produce. Answering it with generated JSON under that media type would
			// be a lie the caller cannot see through, so the media type goes and the
			// caller gets a 406 naming what is actually on offer.
			skipped = append(skipped, fmt.Sprintf("%s %s", key, media))
			continue
		}
		if err != nil {
			return nil, nil, fmt.Errorf("response %q, media type %q: %w", key, media, err)
		}
		out = append(out, Response{Status: status, Media: media, Body: body, Named: named, Headers: headers})
	}
	if len(out) == 0 {
		// Every media type went. The status still exists — the operation does answer
		// it — it just answers with nothing.
		out = append(out, Response{Status: status, Headers: headers})
	}
	return out, skipped, nil
}

// compileBody produces the bytes one media type answers with, plus every named example
// kept reachable by name for `Prefer: example=…`.
//
// The order is the document's own: an `example` beats named `examples`, which beat a
// value generated from the schema. A written example is a person's statement of what
// this endpoint returns, and no generator improves on it.
func compileBody(raw any, media string, gen *generator) ([]byte, map[string][]byte, error) {
	mediaDoc, ok := raw.(map[string]any)
	if !ok {
		return nil, nil, fmt.Errorf("expected a mapping")
	}
	named := map[string][]byte{}
	if examples, ok := mediaDoc["examples"].(map[string]any); ok {
		for _, name := range sortedKeys(examples) {
			encoded, ok, err := compileNamedExample(gen, examples[name], media)
			if err != nil {
				return nil, nil, err
			}
			if ok {
				named[name] = encoded
			}
		}
	}
	if example, ok := mediaDoc["example"]; ok {
		body, err := encode(example, media)
		return body, named, err
	}
	if len(named) > 0 {
		return named[sortedKeys(named)[0]], named, nil
	}
	schema, ok := mediaDoc["schema"]
	if !ok {
		return nil, named, nil
	}
	value, err := gen.value(schema, 0)
	if err != nil {
		return nil, nil, err
	}
	body, err := encode(value, media)
	return body, named, err
}

// compileNamedExample renders one entry of a media type's `examples`, which may itself
// be a reference. An entry that states no value is not an error: it describes the
// example in prose, and there is nothing to serve.
func compileNamedExample(gen *generator, raw any, media string) ([]byte, bool, error) {
	node, leave, err := gen.deref(raw)
	if err != nil {
		return nil, false, err
	}
	defer leave()
	doc, ok := node.(map[string]any)
	if !ok {
		return nil, false, nil
	}
	value, ok := doc["value"]
	if !ok {
		return nil, false, nil
	}
	encoded, err := encode(value, media)
	if err != nil {
		return nil, false, err
	}
	return encoded, true, nil
}

// compileHeaders generates the response headers the document describes, sorted by name.
func compileHeaders(respDoc map[string]any, gen *generator) ([]Header, error) {
	headers, _ := respDoc["headers"].(map[string]any)
	var out []Header
	for _, name := range sortedKeys(headers) {
		doc, ok := headers[name].(map[string]any)
		if !ok {
			continue
		}
		value, ok := doc["example"]
		if !ok {
			var err error
			if value, err = gen.value(doc["schema"], 0); err != nil {
				return nil, err
			}
		}
		if value == nil {
			continue
		}
		out = append(out, Header{Name: name, Value: scalar(value)})
	}
	return out, nil
}

// errUnserveableMedia says a response cannot be rendered in the media type the document
// gives it. It is not a bad document: this mock generates JSON and copies text through,
// and an XML response described only by a schema is simply outside what it can make.
var errUnserveableMedia = errors.New("this mock cannot generate that media type")

// encode renders a value for a media type: JSON for a JSON one, and for anything else
// the string as written — a text/csv example is CSV, not a quoted JSON string.
//
// Anything else is refused. Generated JSON served under application/xml is the kind of
// wrong answer a caller cannot see through, and a mock that gives one teaches a model
// to be wrong (ADR-0217).
func encode(value any, media string) ([]byte, error) {
	if !isJSON(media) {
		text, ok := value.(string)
		if !ok {
			return nil, fmt.Errorf("%w: %s", errUnserveableMedia, media)
		}
		return []byte(text), nil
	}
	body, err := json.Marshal(value)
	if err != nil {
		return nil, fmt.Errorf("render the example: %w", err)
	}
	return body, nil
}

// isJSON reports whether a media type carries JSON — application/json, and the +json
// structured syntax suffix every problem/hal/vnd flavour uses.
func isJSON(media string) bool {
	base, _, _ := strings.Cut(media, ";")
	base = strings.TrimSpace(base)
	return base == "application/json" || strings.HasSuffix(base, "+json")
}

// scalar renders a generated value as a header value.
func scalar(value any) string {
	switch v := value.(type) {
	case string:
		return v
	case nil:
		return ""
	default:
		body, err := json.Marshal(v)
		if err != nil {
			return fmt.Sprint(v)
		}
		return string(body)
	}
}

// statusOf reads a response key: a status code, an OpenAPI "4XX" range (served as its
// first code), or `default` — which is status 0 here and 200 on the wire.
func statusOf(key string) (int, error) {
	if key == "default" {
		return 0, nil
	}
	if len(key) == 3 && strings.EqualFold(key[1:], "XX") {
		if lead, err := strconv.Atoi(key[:1]); err == nil && lead >= 1 && lead <= 5 {
			return lead * 100, nil
		}
	}
	code, err := strconv.Atoi(key)
	if err != nil || code < 100 || code > 599 {
		return 0, fmt.Errorf("response %q is not a status code or %q", key, "default")
	}
	return code, nil
}

// sortedKeys returns a map's keys in a stable order, so a document always compiles to
// the same mock — the demo that ran yesterday runs the same today.
func sortedKeys[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for key := range m {
		out = append(out, key)
	}
	sort.Strings(out)
	return out
}
