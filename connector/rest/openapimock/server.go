package openapimock

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"
)

const (
	// maxJournal bounds the journal. A mock left running is unbounded; a view of what
	// it did is not. The newest calls are the ones an operator is looking for, so the
	// oldest go — their sequence numbers are the record that they existed.
	maxJournal = 200
	// maxRecordedBody bounds what one journal entry keeps of a request body.
	maxRecordedBody = 64 << 10
	// inspectionPrefix is where this mock's own endpoints live. It is deliberately
	// unlovely: the paths under it must not collide with the document's, and a
	// document is free to describe /mock/anything.
	inspectionPrefix = "/__mock/"
)

// Call is one request this mock served, as the journal records it.
//
// There is no timestamp, by design: nothing here is durable, and Seq answers the
// question a timestamp would ("in what order, and what did I miss") without inviting
// anyone to treat a mock's clock as evidence.
type Call struct {
	Seq    int    `json:"seq"`
	Method string `json:"method"`
	// Path is what the caller actually asked for, query string included: a REST task
	// that puts its parameters in the URL sent them, and a journal that dropped them
	// would answer "which pet?" with "some pet".
	Path      string          `json:"path"`
	Operation string          `json:"operation,omitempty"`
	Status    int             `json:"status"`
	RequestID string          `json:"requestId,omitempty"`
	Body      json.RawMessage `json:"body,omitempty"`
}

// Report is this mock in the envelope the Console's Mockups view takes
// (ADR-0216): a header the server understands and a payload it
// does not. At is missing on purpose — it is stamped by whoever accepts the report,
// never by the reporter.
type Report struct {
	Kind    string          `json:"kind"`
	Source  string          `json:"source"`
	Target  string          `json:"target"`
	Summary string          `json:"summary"`
	Data    json.RawMessage `json:"data"`
}

// operationReport is one row of the report's operation table: what the document
// describes, and how often it was actually called.
type operationReport struct {
	Method string `json:"method"`
	Path   string `json:"path"`
	ID     string `json:"operationId,omitempty"`
	Calls  int    `json:"calls"`
}

// reportData is the openapi kind's own shape — the payload the envelope carries.
type reportData struct {
	Title      string            `json:"title"`
	Version    string            `json:"version,omitempty"`
	BasePath   string            `json:"basePath,omitempty"`
	Operations []operationReport `json:"operations"`
	Calls      []Call            `json:"calls"`
}

// Server serves a compiled [Spec]. Build one with [New], mount [Server.Handler], and
// read what a run did with [Server.Calls] or [Server.Report]. It is safe for
// concurrent use: one mock serves every worker pointed at it.
type Server struct {
	spec     *Spec
	basePath string
	id       string
	log      io.Writer

	mu     sync.Mutex
	seq    int
	calls  []Call
	counts map[string]int
}

// Option configures a [Server].
type Option func(*Server)

// WithID names this mock in the reports it produces. It defaults to "openapi-mock".
func WithID(id string) Option {
	return func(s *Server) {
		if id = strings.TrimSpace(id); id != "" {
			s.id = id
		}
	}
}

// WithBasePath serves the document's paths under a different prefix than its first
// server URL states. "/" serves them at the root, which is what a task pointed at a
// bare host needs.
func WithBasePath(path string) Option {
	return func(s *Server) { s.basePath = normalizeBasePath(path) }
}

// WithLog writes one line per served call. A mock in a terminal is watched, and the
// line that says which operation answered is most of what watching it is for.
func WithLog(w io.Writer) Option {
	return func(s *Server) { s.log = w }
}

// New builds a server for a compiled document.
func New(spec *Spec, opts ...Option) *Server {
	s := &Server{spec: spec, basePath: spec.BasePath, id: "openapi-mock", counts: map[string]int{}}
	for _, opt := range opts {
		opt(s)
	}
	return s
}

// normalizeBasePath brings a prefix into the one shape the matcher expects: empty, or
// leading slash and no trailing one.
func normalizeBasePath(path string) string {
	path = strings.TrimSpace(path)
	if path == "" || path == "/" {
		return ""
	}
	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}
	return strings.TrimSuffix(path, "/")
}

// Handler returns the HTTP handler: the document's own operations, plus the two
// inspection endpoints under /__mock/.
func (s *Server) Handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, inspectionPrefix) {
			s.serveInspection(w, r)
			return
		}
		s.serve(w, r)
	})
}

// BasePath is the prefix this mock serves the document's paths under, which is the
// document's own unless [WithBasePath] moved it.
func (s *Server) BasePath() string { return s.basePath }

// Calls returns the journal, oldest first.
func (s *Server) Calls() []Call {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]Call, len(s.calls))
	copy(out, s.calls)
	return out
}

// Report is what this mock has served, in the shape the Mockups view renders.
func (s *Server) Report() Report {
	s.mu.Lock()
	defer s.mu.Unlock()
	data := reportData{
		Title:      s.spec.Title,
		Version:    s.spec.Version,
		BasePath:   s.basePath,
		Operations: make([]operationReport, 0, len(s.spec.Operations)),
		Calls:      append([]Call{}, s.calls...),
	}
	for _, op := range s.spec.Operations {
		data.Operations = append(data.Operations, operationReport{
			Method: op.Method,
			Path:   op.Path,
			ID:     op.ID,
			Calls:  s.counts[op.Method+" "+op.Path],
		})
	}
	sort.Slice(data.Operations, func(i, j int) bool {
		if data.Operations[i].Path != data.Operations[j].Path {
			return data.Operations[i].Path < data.Operations[j].Path
		}
		return data.Operations[i].Method < data.Operations[j].Method
	})
	// The payload is this kind's own shape and nothing else has to understand it, so a
	// failure to render it would be a bug here rather than bad input; an empty payload
	// still leaves a legible envelope.
	payload, err := json.Marshal(data)
	if err != nil {
		payload = []byte("{}")
	}
	return Report{
		Kind:   "openapi",
		Source: s.id,
		// Target stays empty for a document that names itself nothing: the envelope's
		// fields are data for whoever renders the card, and a placeholder there would
		// be prose pretending to be one. The summary is the place for prose.
		Target:  strings.TrimSpace(s.spec.Title + " " + s.spec.Version),
		Summary: fmt.Sprintf("%s — %s, %s", s.spec.Name(), plural(len(s.spec.Operations), "operation"), plural(s.seq, "call")),
		Data:    payload,
	}
}

// serveInspection answers this mock's own endpoints. They are not part of what is
// mocked, so they are not journalled either — a view of the calls must not be a call.
func (s *Server) serveInspection(w http.ResponseWriter, r *http.Request) {
	switch strings.TrimPrefix(r.URL.Path, inspectionPrefix) {
	case "calls":
		writeJSON(w, http.StatusOK, s.Calls())
	case "report":
		writeJSON(w, http.StatusOK, s.Report())
	default:
		writeError(w, http.StatusNotFound, fmt.Sprintf("this mock serves %scalls and %sreport", inspectionPrefix, inspectionPrefix))
	}
}

// serve answers one request against the document, and records it either way.
func (s *Server) serve(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(io.LimitReader(r.Body, maxRecordedBody))
	op, status, err := s.answer(w, r)
	if err != nil {
		status = err.status
		writeError(w, status, err.message)
	}
	s.record(r, body, op, status)
}

// refusal is an answer this mock will not give, and why. It is a type rather than a
// bare status because every one of them is a statement to the caller: a mock that
// silently substitutes an answer it can give teaches a model to be wrong (ADR-0181).
type refusal struct {
	status  int
	message string
}

// answer resolves the request to a response and writes it. The returned operation is
// nil when nothing matched.
func (s *Server) answer(w http.ResponseWriter, r *http.Request) (*Operation, int, *refusal) {
	path, ok := s.strip(r.URL.Path)
	if !ok {
		return nil, 0, &refusal{http.StatusNotFound, fmt.Sprintf(
			"no operation matches %s %s: this mock serves paths under %s", r.Method, r.URL.Path, s.basePath)}
	}
	request := splitPath(path)

	var op *Operation
	var allowed []string
	for i := range s.spec.Operations {
		candidate := &s.spec.Operations[i]
		if !matches(candidate.segments, request) {
			continue
		}
		if candidate.Method == r.Method {
			op = candidate
			break
		}
		allowed = append(allowed, candidate.Method)
	}
	switch {
	case op == nil && len(allowed) > 0:
		sort.Strings(allowed)
		w.Header().Set("Allow", strings.Join(allowed, ", "))
		return nil, 0, &refusal{http.StatusMethodNotAllowed, fmt.Sprintf(
			"%s is not described for %s; the document has %s", r.Method, r.URL.Path, strings.Join(allowed, ", "))}
	case op == nil:
		return nil, 0, &refusal{http.StatusNotFound, fmt.Sprintf("no operation matches %s %s", r.Method, r.URL.Path)}
	}

	pref, err := parsePrefer(r.Header.Get("Prefer"))
	if err != nil {
		return op, 0, err
	}
	group, err := chooseStatus(op, pref)
	if err != nil {
		return op, 0, err
	}
	resp, err := chooseMedia(group, r.Header.Get("Accept"))
	if err != nil {
		return op, 0, err
	}
	body := resp.Body
	if pref.example != "" {
		named, ok := resp.Named[pref.example]
		if !ok {
			return op, 0, &refusal{http.StatusBadRequest, fmt.Sprintf(
				"the document has no example %q for this response; it has %s", pref.example, listOr(sortedKeys(resp.Named), "no named examples"))}
		}
		body = named
	}

	for _, header := range resp.Headers {
		w.Header().Set(header.Name, header.Value)
	}
	status := resp.Status
	if status == 0 {
		status = http.StatusOK
	}
	if len(body) > 0 {
		w.Header().Set("Content-Type", resp.Media)
	}
	w.WriteHeader(status)
	if len(body) > 0 {
		_, _ = w.Write(body)
	}
	return op, status, nil
}

// strip removes the base path, reporting whether the request was under it at all.
func (s *Server) strip(path string) (string, bool) {
	if s.basePath == "" {
		return path, true
	}
	if path == s.basePath {
		return "/", true
	}
	if rest, ok := strings.CutPrefix(path, s.basePath+"/"); ok {
		return "/" + rest, true
	}
	return "", false
}

// chooseStatus picks the responses a request will be answered from: what `Prefer:
// code=…` asked for, or — with no preference — the lowest success the document
// describes, which is the answer the caller was written against.
func chooseStatus(op *Operation, pref preference) ([]Response, *refusal) {
	var group []Response
	if pref.hasCode {
		for _, resp := range op.Responses {
			if resp.Status == pref.code {
				group = append(group, resp)
			}
		}
		if len(group) == 0 {
			// A `default` response is what the document says every other code
			// returns, so it can honour the request; nothing else can.
			for _, resp := range op.Responses {
				if resp.Status == 0 {
					group = append(group, Response{Status: pref.code, Media: resp.Media, Body: resp.Body, Named: resp.Named, Headers: resp.Headers})
				}
			}
		}
		if len(group) == 0 {
			return nil, &refusal{http.StatusBadRequest, fmt.Sprintf(
				"the document does not describe a %d response for this operation; it describes %s", pref.code, listOr(statusNames(op), "none"))}
		}
		return group, nil
	}
	best := -1
	for _, resp := range op.Responses {
		if best == -1 || preferStatus(resp.Status, best) {
			best = resp.Status
		}
	}
	for _, resp := range op.Responses {
		if resp.Status == best {
			group = append(group, resp)
		}
	}
	if len(group) == 0 {
		// An operation with no responses at all is legal in OpenAPI 3.1 and means
		// exactly this: it answers, and says nothing.
		group = append(group, Response{Status: http.StatusNoContent})
	}
	return group, nil
}

// preferStatus reports whether one status is the better answer to a request that asked
// for none: a success first — that is what the caller was written against — then the
// document's `default`, then the lowest of whatever is left, so an operation that
// describes only failures answers the likeliest one.
func preferStatus(candidate, best int) bool {
	rank := func(status int) int {
		switch {
		case status >= 200 && status < 300:
			return 0
		case status == 0:
			return 1
		default:
			return 2
		}
	}
	if rank(candidate) != rank(best) {
		return rank(candidate) < rank(best)
	}
	return candidate < best
}

// chooseMedia picks the media type to answer with: what the caller accepts, and
// otherwise JSON where there is JSON.
func chooseMedia(group []Response, accept string) (Response, *refusal) {
	if len(group) == 1 && group[0].Media == "" {
		return group[0], nil // no content is acceptable to everybody
	}
	wanted := parseAccept(accept)
	if len(wanted) == 0 {
		return preferred(group), nil
	}
	for _, want := range wanted {
		if want == "*/*" {
			return preferred(group), nil
		}
		for _, resp := range group {
			if mediaMatches(want, resp.Media) {
				return resp, nil
			}
		}
	}
	var have []string
	for _, resp := range group {
		have = append(have, resp.Media)
	}
	return Response{}, &refusal{http.StatusNotAcceptable, fmt.Sprintf(
		"this operation answers %s, which %q does not accept", listOr(have, "no content"), accept)}
}

// preferred is the media type to answer with when the caller did not say: JSON if the
// operation has it, otherwise the first the document lists.
func preferred(group []Response) Response {
	for _, resp := range group {
		if resp.Media == "application/json" {
			return resp
		}
	}
	for _, resp := range group {
		if isJSON(resp.Media) {
			return resp
		}
	}
	return group[0]
}

// mediaMatches reports whether an Accept entry covers a media type.
func mediaMatches(want, have string) bool {
	if want == have {
		return true
	}
	prefix, ok := strings.CutSuffix(want, "/*")
	return ok && strings.HasPrefix(have, prefix+"/")
}

// parseAccept reads an Accept header into its media types, in the order written. The
// q-values are ignored: a mock has one or two shapes per response, and preference
// ordering between them is not a fidelity anybody needs from a mockup.
func parseAccept(header string) []string {
	var out []string
	for _, entry := range strings.Split(header, ",") {
		media, _, _ := strings.Cut(entry, ";")
		if media = strings.TrimSpace(media); media != "" {
			out = append(out, media)
		}
	}
	return out
}

// preference is what a Prefer header asked this mock to do (RFC 7240).
type preference struct {
	code    int
	hasCode bool
	example string
}

// parsePrefer reads `Prefer: code=404, example=rex`.
//
// The RFC says an unhonoured preference is ignored, and this mock ignores every
// preference it does not know — but not one it knows and cannot serve. A test written
// against the 418 path that quietly receives a 200 passes, and the model it was
// judging is wrong in production instead.
func parsePrefer(header string) (preference, *refusal) {
	var pref preference
	for _, entry := range strings.Split(header, ",") {
		key, value, ok := strings.Cut(strings.TrimSpace(entry), "=")
		if !ok {
			continue
		}
		value = strings.Trim(strings.TrimSpace(value), `"`)
		switch strings.ToLower(strings.TrimSpace(key)) {
		case "code":
			code, err := strconv.Atoi(value)
			if err != nil {
				return pref, &refusal{http.StatusBadRequest, fmt.Sprintf("Prefer code=%q is not a status code", value)}
			}
			pref.code, pref.hasCode = code, true
		case "example":
			pref.example = value
		}
	}
	return pref, nil
}

// statusNames lists the statuses an operation describes, for a refusal to name.
func statusNames(op *Operation) []string {
	seen := map[int]bool{}
	var out []string
	for _, resp := range op.Responses {
		if seen[resp.Status] {
			continue
		}
		seen[resp.Status] = true
		if resp.Status == 0 {
			out = append(out, "default")
			continue
		}
		out = append(out, strconv.Itoa(resp.Status))
	}
	sort.Strings(out)
	return out
}

// record appends one call to the journal and counts it against its operation.
func (s *Server) record(r *http.Request, body []byte, op *Operation, status int) {
	call := Call{
		Method:    r.Method,
		Path:      r.URL.RequestURI(),
		Status:    status,
		RequestID: r.Header.Get("X-Request-ID"),
		Body:      recordedBody(body),
	}
	var key string
	if op != nil {
		if call.Operation = op.ID; call.Operation == "" {
			call.Operation = op.Method + " " + op.Path
		}
		key = op.Method + " " + op.Path
	}

	s.mu.Lock()
	s.seq++
	call.Seq = s.seq
	if key != "" {
		s.counts[key]++
	}
	s.calls = append(s.calls, call)
	if len(s.calls) > maxJournal {
		s.calls = append(s.calls[:0], s.calls[len(s.calls)-maxJournal:]...)
	}
	// Logging under the lock keeps the lines in journal order, and keeps two workers
	// calling at once from interleaving half a line each.
	if s.log != nil {
		line := fmt.Sprintf("%s %s → %d", call.Method, call.Path, call.Status)
		if call.Operation != "" {
			line += " " + call.Operation
		}
		fmt.Fprintln(s.log, line)
	}
	s.mu.Unlock()
}

// recordedBody keeps a request body in a form the journal can serve back as JSON: as
// itself when it is JSON, and as a JSON string when it is not.
func recordedBody(body []byte) json.RawMessage {
	if len(body) == 0 {
		return nil
	}
	if json.Valid(body) {
		return json.RawMessage(body)
	}
	// Marshalling a string cannot fail, so there is nothing to handle here.
	quoted, _ := json.Marshal(string(body))
	return quoted
}

// writeJSON writes a value as the body of a response.
func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

// writeError answers with the reason this mock would not serve the request. The shape
// is the mock's own — the document describes what the API answers, and this is not
// the API answering.
func writeError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]string{"error": message})
}

// listOr renders a list for a message, falling back to a phrase when it is empty.
func listOr(items []string, empty string) string {
	if len(items) == 0 {
		return empty
	}
	return strings.Join(items, ", ")
}

// plural renders a count with its noun: "1 call", "6 operations".
func plural(n int, noun string) string {
	if n == 1 {
		return fmt.Sprintf("%d %s", n, noun)
	}
	return fmt.Sprintf("%d %ss", n, noun)
}
