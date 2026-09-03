// Package openapimock serves a mock REST API from an OpenAPI 3 document, so a process
// with a REST connector task (ADR-0067) can be run end to end before the API it calls
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
// It reads the one document it is given: a `$ref` into another file is refused at load
// rather than half-served, because a document published as a tree of files (a common
// shape for a large API) would otherwise become a mock whose every operation answers
// nothing. For the same reason a response described only in a media type this mock
// cannot generate loses its body instead of being answered with JSON wearing that
// media type's label.
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
func LoadFile(path string) (*Spec, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	spec, err := Load(data)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return spec, nil
}

// Load compiles an OpenAPI 3 document held in memory.
func Load(data []byte) (*Spec, error) {
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

	gen := &generator{doc: doc, active: map[string]bool{}}
	for _, path := range sortedKeys(paths) {
		if strings.HasPrefix(path, "x-") {
			continue
		}
		if !strings.HasPrefix(path, "/") {
			return nil, fmt.Errorf("path %q must start with %q", path, "/")
		}
		// A path item, an operation and a response may each be a $ref into components
		// rather than the thing itself; a large document routinely factors all three
		// out. Reading the reference as an empty object is how a mock quietly stops
		// mocking, so every one of them is followed here.
		node, err := gen.deref(paths[path])
		if err != nil {
			return nil, fmt.Errorf("path %q: %w", path, err)
		}
		item, ok := node.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("path %q: expected a mapping", path)
		}
		segs, err := compilePath(path)
		if err != nil {
			return nil, err
		}
		for _, method := range methods {
			raw, present := item[method]
			if !present {
				continue
			}
			resolved, err := gen.deref(raw)
			if err != nil {
				return nil, fmt.Errorf("%s %s: %w", strings.ToUpper(method), path, err)
			}
			opDoc, ok := resolved.(map[string]any)
			if !ok {
				return nil, fmt.Errorf("%s %s: expected a mapping", method, path)
			}
			op := Operation{
				Method:   strings.ToUpper(method),
				Path:     path,
				segments: segs,
			}
			op.ID, _ = opDoc["operationId"].(string)
			op.Summary, _ = opDoc["summary"].(string)
			responses, skipped, err := compileResponses(opDoc, gen)
			if err != nil {
				return nil, fmt.Errorf("%s %s: %w", strings.ToUpper(method), path, err)
			}
			op.Responses = responses
			for _, entry := range skipped {
				spec.Skipped = append(spec.Skipped, fmt.Sprintf("%s %s %s", op.Method, path, entry))
			}
			spec.Operations = append(spec.Operations, op)
		}
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
		node, err := gen.deref(responses[key])
		if err != nil {
			return nil, nil, fmt.Errorf("response %q: %w", key, err)
		}
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
			out = append(out, Response{Status: status, Headers: headers})
			continue
		}
		served := 0
		for _, media := range sortedKeys(content) {
			body, named, err := compileBody(content[media], media, gen)
			if errors.Is(err, errUnserveableMedia) {
				// The document describes this response in a shape this mock cannot
				// produce. Answering it with generated JSON under that media type
				// would be a lie the caller cannot see through, so the media type
				// goes and the caller gets a 406 naming what is actually on offer.
				skipped = append(skipped, fmt.Sprintf("%s %s", key, media))
				continue
			}
			if err != nil {
				return nil, nil, fmt.Errorf("response %q, media type %q: %w", key, media, err)
			}
			out = append(out, Response{Status: status, Media: media, Body: body, Named: named, Headers: headers})
			served++
		}
		if served == 0 {
			// Every media type went. The status still exists — the operation does
			// answer it — it just answers with nothing.
			out = append(out, Response{Status: status, Headers: headers})
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Status != out[j].Status {
			return out[i].Status < out[j].Status
		}
		return out[i].Media < out[j].Media
	})
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
			entry, err := gen.deref(examples[name])
			if err != nil {
				return nil, nil, err
			}
			doc, ok := entry.(map[string]any)
			if !ok {
				continue
			}
			value, ok := doc["value"]
			if !ok {
				continue
			}
			encoded, err := encode(value, media)
			if err != nil {
				return nil, nil, err
			}
			named[name] = encoded
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
