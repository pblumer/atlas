// Package formlookup serves a task form's remote data sources: a person types into a
// directory field and sees the first matching entries of a directory the server never
// copies (ADR-0473).
//
// The form says what may be searched; this package enforces what the person may
// access; the Worker performs the search; the browser holds one page; and the durable
// workflow records only the value the person finally submits.
//
// **What the request may say** is what a person can know: the term they typed, what
// they picked in the form's other fields, a cursor this server handed them, and a page
// size no larger than the field's. Which Worker, which tenant, which resource, which
// properties are searched and returned — all of that is read from the task's stored
// form and from the provider registry, and a request naming any of it is refused.
//
// **This area owns no state**, like formgen: its collaborators are the server's and
// each observes the single writer itself (I3). What it does hold — a query waiting
// for a Worker Instance — waits on the request's own goroutine, with the run loop
// free, and is never written anywhere: nothing here reaches the log, the processor or
// recovery.
package formlookup

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/pblumer/atlas/api/httpapi"
	"github.com/pblumer/atlas/api/workerquery"
	"github.com/pblumer/atlas/connector/lookup"
)

// The task collaborator's two refusals.
var (
	ErrNoTask    = errors.New("formlookup: no open task with that key")
	ErrForbidden = errors.New("formlookup: the caller may not work on this task")
)

// Task is what a lookup needs of the task it is made from: its form, as the form store
// holds it now.
type Task struct {
	Schema []byte
}

// Worker is a configured Worker as this area judges it. No endpoint and nowhere to put
// a credential.
type Worker struct {
	Name       string `json:"name"`
	WorkerType string `json:"workerType"`
	Enabled    bool   `json:"enabled"`
	// FormLookups is the Worker's own switch: its editors decide whether a form may
	// search through its credential (ADR-0205). Seeing a form, or editing one, is not
	// that permission.
	FormLookups bool `json:"formLookups"`
}

// Config is the server's half of this area.
type Config struct {
	// Task authorizes the caller against a task — the question task completion asks,
	// ADR-0317 — and returns its form. ErrNoTask and ErrForbidden are its refusals.
	Task func(r *http.Request, key uint64) (Task, error)
	// Worker resolves a Worker by name from the Worker store.
	Worker func(name string) (Worker, bool, error)
	// Workers lists the Workers a model author may pick, for the editor.
	Workers func(r *http.Request) ([]Worker, error)
	// Ask puts a query to a Worker Instance and waits for its answer.
	Ask func(ctx context.Context, jobType, connector string, q lookup.Query, timeout time.Duration) (lookup.Page, error)
	// Allow is the per-person rate limit.
	Allow func(key string) bool
	// Principal names the caller, for the rate limit and for the cursor a page is
	// continued with. Empty when the server runs without authentication.
	Principal func(r *http.Request) string
	// Timeout bounds one query.
	Timeout time.Duration
	// MaxBody bounds a request's body: the server's Request budget.
	MaxBody int64
}

// DefaultTimeout is what a person waits for at most. Graph answers a filtered page in
// well under a second; a lookup still waiting after this is not going to be useful.
const DefaultTimeout = 8 * time.Second

// Service serves remote data sources. Build it with [New].
type Service struct {
	cfg    Config
	cursor *sealer
}

// New builds the service.
func New(cfg Config) *Service {
	if cfg.Timeout <= 0 {
		cfg.Timeout = DefaultTimeout
	}
	return &Service{cfg: cfg, cursor: newSealer()}
}

// Item is one option as the form shows it.
type Item struct {
	// Value is what the form submits: the record's identifier.
	Value string `json:"value"`
	// Label is what a person reads; Description the line under it.
	Label       string            `json:"label"`
	Description string            `json:"description,omitempty"`
	Details     map[string]string `json:"details,omitempty"`
}

// queryRequest is the whole of what a lookup request may say.
type queryRequest struct {
	Search  string            `json:"search"`
	Filters map[string]string `json:"filters"`
	Cursor  string            `json:"cursor"`
	Value   string            `json:"value"`
	Limit   int               `json:"limit"`
}

// Codes are the machine-readable codes a lookup or a refused completion answers with,
// beside the human-readable error. A client branches on these and words them itself;
// the console's catalogue carries a message for each (remote.error.<code>), which a
// test holds it to.
var Codes = []string{
	"forbidden", "not_found", "invalid_query", "invalid_cursor", "invalid_configuration",
	"rate_limited", "worker_unavailable", "worker_busy", "provider_throttled",
	"provider_unavailable", "provider_refused", "timeout",
}

// apiError is a refused lookup: a status, a code a client can branch on, a message a
// person can read, and how long to wait where waiting helps.
type apiError struct {
	status int
	code   string
	msg    string
	retry  int
}

func (e *apiError) write(w http.ResponseWriter) {
	body := map[string]any{"error": e.msg, "code": e.code}
	if e.retry > 0 {
		w.Header().Set("Retry-After", strconv.Itoa(e.retry))
		body["retryAfter"] = e.retry
	}
	httpapi.JSON(w, e.status, body)
}

func invalid(format string, a ...any) *apiError {
	return &apiError{status: http.StatusBadRequest, code: "invalid_query", msg: fmt.Sprintf(format, a...)}
}

// HandleQuery is POST /api/v1/tasks/{key}/form-data/{field}/query.
func (s *Service) HandleQuery(w http.ResponseWriter, r *http.Request) {
	principal := ""
	if s.cfg.Principal != nil {
		principal = s.cfg.Principal(r)
	}
	if s.cfg.Allow != nil && !s.cfg.Allow("lookup:"+principal) {
		(&apiError{status: http.StatusTooManyRequests, code: "rate_limited",
			msg: "too many searches in a short time; wait a moment", retry: 1}).write(w)
		return
	}
	key, err := strconv.ParseUint(r.PathValue("key"), 10, 64)
	if err != nil {
		(&apiError{status: http.StatusNotFound, code: "not_found", msg: "no open task with that key"}).write(w)
		return
	}
	task, err := s.cfg.Task(r, key)
	switch {
	case errors.Is(err, ErrNoTask):
		(&apiError{status: http.StatusNotFound, code: "not_found", msg: "no open task with that key"}).write(w)
		return
	case errors.Is(err, ErrForbidden):
		(&apiError{status: http.StatusForbidden, code: "forbidden",
			msg: "this task is assigned to somebody else, so its form cannot be searched from here"}).write(w)
		return
	case err != nil:
		httpapi.Error(w, http.StatusInternalServerError, "read task: "+err.Error())
		return
	}
	var req queryRequest
	if e := decode(w, r, s.cfg.MaxBody, &req); e != nil {
		e.write(w)
		return
	}
	form, err := Parse(task.Schema)
	if err != nil {
		(&apiError{status: http.StatusUnprocessableEntity, code: "invalid_configuration",
			msg: "the task's form cannot be searched: " + err.Error()}).write(w)
		return
	}
	fieldKey := r.PathValue("field")
	field, ok := form.Field(fieldKey)
	if !ok {
		(&apiError{status: http.StatusNotFound, code: "not_found",
			msg: fmt.Sprintf("the task's form has no directory field %q", fieldKey)}).write(w)
		return
	}
	if e := s.usable(field); e != nil {
		e.write(w)
		return
	}
	q, e := build(form, field, req)
	if e != nil {
		e.write(w)
		return
	}
	binding := cursorBinding{Task: key, Field: field.Key, Principal: principal, Query: queryHash(field, q)}
	if req.Cursor != "" {
		if q.Value != "" {
			invalid("a value lookup has no pages to continue").write(w)
			return
		}
		next, err := s.cursor.open(req.Cursor, binding)
		if err != nil {
			(&apiError{status: http.StatusBadRequest, code: "invalid_cursor",
				msg: "this page cannot be continued; search again"}).write(w)
			return
		}
		q.Next = next
	}
	if err := field.Provider.Check(q); err != nil {
		invalid("the search breaks the rules of %s", field.Provider.Label).write(w)
		return
	}
	page, err := s.cfg.Ask(r.Context(), field.Provider.JobType, field.Source.Worker, q, s.cfg.Timeout)
	if err != nil {
		if errors.Is(err, context.Canceled) && r.Context().Err() != nil {
			// The person typed on, and the browser withdrew this request. Nobody reads
			// an answer to it.
			return
		}
		failure(err, "the directory could not be searched").write(w)
		return
	}
	out := map[string]any{"items": items(field, page), "nextCursor": nil}
	if page.Next != "" && q.Value == "" {
		out["nextCursor"] = s.cursor.seal(binding, page.Next)
	}
	httpapi.JSON(w, http.StatusOK, out)
}

// usable checks the field's Worker: it must exist, be of the source's Worker Type, be
// enabled, and allow forms to search it.
func (s *Service) usable(f Field) *apiError {
	wk, ok, err := s.cfg.Worker(f.Source.Worker)
	switch {
	case err != nil:
		return &apiError{status: http.StatusInternalServerError, code: "internal", msg: "read Worker: " + err.Error()}
	case !ok:
		return &apiError{status: http.StatusUnprocessableEntity, code: "invalid_configuration",
			msg: fmt.Sprintf("the field %q names the Worker %q, which is not configured on this server", f.Key, f.Source.Worker)}
	case wk.WorkerType != f.Provider.WorkerType:
		return &apiError{status: http.StatusUnprocessableEntity, code: "invalid_configuration",
			msg: fmt.Sprintf("the field %q names the Worker %q, which is not a %s Worker", f.Key, f.Source.Worker, f.Provider.WorkerType)}
	case !wk.Enabled:
		return &apiError{status: http.StatusServiceUnavailable, code: "worker_unavailable",
			msg: fmt.Sprintf("the Worker %q is switched off", f.Source.Worker)}
	case !wk.FormLookups:
		return &apiError{status: http.StatusForbidden, code: "forbidden",
			msg: fmt.Sprintf("the Worker %q does not allow forms to search through it; its owner can allow it in the Console", f.Source.Worker)}
	}
	return nil
}

// build turns a request into the query the field allows, refusing anything the field
// does not.
func build(form *Form, f Field, req queryRequest) (lookup.Query, *apiError) {
	q := lookup.Query{
		Resource:   f.Provider.Resource,
		ActiveOnly: f.ActiveOnly(),
		Properties: f.Properties(),
		PageSize:   f.PageSize(),
	}
	if req.Limit < 0 || req.Limit > f.PageSize() {
		return q, invalid("limit %d is outside 1..%d", req.Limit, f.PageSize())
	}
	if req.Limit > 0 {
		q.PageSize = req.Limit
	}
	bound := map[string]bool{}
	for _, fl := range f.Source.Filters {
		if fl.Field != "" {
			bound[fl.Property] = true
		}
	}
	for p := range req.Filters {
		if !bound[p] {
			return q, invalid("%q is not a filter this field takes from the form", p)
		}
	}
	filters, e := filtersOf(form, f, func(prop string) string { return req.Filters[prop] }, invalid)
	if e != nil {
		return q, e
	}
	q.Filters = filters
	if req.Value != "" {
		if req.Search != "" {
			return q, invalid("a request is a search or a value lookup, not both")
		}
		if !f.Provider.ValidValue(req.Value) {
			return q, invalid("the value is not %s", f.Provider.ValueFormat)
		}
		q.Value = req.Value
		return q, nil
	}
	if !lookup.CleanText(req.Search) {
		return q, invalid("the search term contains control characters")
	}
	term := lookup.NormalizeSearch(req.Search)
	if n := utf8.RuneCountInString(term); n > lookup.MaxSearchLength {
		return q, invalid("the search term is longer than %d characters", lookup.MaxSearchLength)
	} else if n < f.MinSearchLength() {
		return q, invalid("type at least %d characters to search", f.MinSearchLength())
	}
	q.Search = term
	if term != "" {
		q.SearchFields = f.SearchFields()
	}
	return q, nil
}

// filtersOf resolves the field's filters: fixed ones as the form states them, bound
// ones from value (the request's, or the submission's). A bound value is held to its
// source field's fixed options when it has some; an empty one leaves the search
// unnarrowed, unless the filter is required.
func filtersOf(form *Form, f Field, value func(prop string) string, refuse func(string, ...any) *apiError) ([]lookup.Filter, *apiError) {
	var out []lookup.Filter
	for _, fl := range f.Source.Filters {
		if fl.Value != "" {
			out = append(out, lookup.Filter{Property: fl.Property, Value: fl.Value})
			continue
		}
		v := strings.TrimSpace(value(fl.Property))
		if v == "" {
			if fl.Required {
				return nil, refuse("choose %s first", fieldName(form, fl.Field))
			}
			continue
		}
		if utf8.RuneCountInString(v) > lookup.MaxValueLength || !lookup.CleanText(v) {
			return nil, refuse("the value of %s cannot be used as a filter", fieldName(form, fl.Field))
		}
		if opts, ok := form.StaticOptions(fl.Field); ok && !slices.Contains(opts, v) {
			return nil, refuse("%q is not one of the options of %s", v, fieldName(form, fl.Field))
		}
		out = append(out, lookup.Filter{Property: fl.Property, Value: v})
	}
	return out, nil
}

// fieldName is how an error names a field of the form: by its label when it has one.
func fieldName(form *Form, key string) string {
	if l := form.labels[key]; l != "" {
		return l
	}
	return key
}

// queryHash identifies a search, so a cursor continues exactly the one it was issued
// for: the same term, filters, properties and page size, through the same Worker.
func queryHash(f Field, q lookup.Query) string {
	q.Next = ""
	raw, _ := json.Marshal(struct {
		Worker string       `json:"w"`
		Query  lookup.Query `json:"q"`
	}{f.Source.Worker, q})
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

// items turns a page into options. A record without a valid value is not offered —
// whatever a Worker answered, nothing reaches the person that the form could not
// submit — and nothing the field does not show is passed on.
func items(f Field, page lookup.Page) []Item {
	out := make([]Item, 0, len(page.Items))
	for _, rec := range page.Items {
		value, _ := rec[f.Provider.ValueProperty].(string)
		if !f.Provider.ValidValue(value) {
			continue
		}
		it := Item{Value: value, Label: text(rec[f.LabelProperty()])}
		fallback := text(rec[f.Provider.Fallback])
		if it.Label == "" {
			it.Label = fallback
		}
		if it.Label == "" {
			it.Label = value
		}
		it.Description = text(rec[f.DescriptionProperty()])
		if it.Description == "" {
			it.Description = fallback
		}
		if it.Description == it.Label {
			it.Description = ""
		}
		for _, d := range f.Source.Option.Details {
			if v := text(rec[d]); v != "" {
				if it.Details == nil {
					it.Details = map[string]string{}
				}
				it.Details[d] = v
			}
		}
		out = append(out, it)
	}
	return out
}

// text is a property as a person reads it. An object or a list is not something an
// option shows.
func text(v any) string {
	switch t := v.(type) {
	case string:
		return strings.TrimSpace(t)
	case json.Number, float64, bool, int:
		return fmt.Sprint(t)
	}
	return ""
}

// failure words a query that got no page. The person reads the message; the code is
// what a client branches on; the provider's own words never appear, because they carry
// the request and the request carries the term.
func failure(err error, what string) *apiError {
	var le *lookup.Error
	switch {
	case errors.Is(err, workerquery.ErrUnavailable):
		return &apiError{status: http.StatusServiceUnavailable, code: "worker_unavailable",
			msg: what + ": no Worker Instance serving this directory is running"}
	case errors.Is(err, workerquery.ErrBusy):
		return &apiError{status: http.StatusServiceUnavailable, code: "worker_busy",
			msg: what + ": too many searches are waiting; try again in a moment", retry: 2}
	case errors.Is(err, workerquery.ErrTimeout), errors.Is(err, context.DeadlineExceeded):
		return &apiError{status: http.StatusGatewayTimeout, code: "timeout", msg: what + ": the directory did not answer in time"}
	case errors.As(err, &le):
		switch le.Code {
		case lookup.CodeThrottled:
			return &apiError{status: http.StatusServiceUnavailable, code: "provider_throttled",
				msg: what + ": the directory asked to be left alone for a moment", retry: le.RetryAfter}
		case lookup.CodeRefused:
			return &apiError{status: http.StatusBadGateway, code: "provider_refused",
				msg: what + ": the directory refused the Worker's request; its permissions may be missing"}
		case lookup.CodeUnconfigured:
			return &apiError{status: http.StatusServiceUnavailable, code: "worker_unavailable",
				msg: what + ": the running Worker Instance does not hold this Worker"}
		case lookup.CodeInvalid:
			return &apiError{status: http.StatusUnprocessableEntity, code: "invalid_configuration",
				msg: what + ": the Worker refused the field's search; the server and the Worker may be different versions"}
		}
	}
	return &apiError{status: http.StatusBadGateway, code: "provider_unavailable", msg: what + ": the directory did not answer"}
}

// decode reads a bounded JSON body that names nothing a lookup request may not say.
func decode(w http.ResponseWriter, r *http.Request, maxBody int64, v any) *apiError {
	if maxBody <= 0 {
		maxBody = 1 << 16
	}
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxBody))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			return &apiError{status: http.StatusRequestEntityTooLarge, code: "invalid_query", msg: "the request is larger than a search can be"}
		}
		return invalid("the request is not a search: %v", err)
	}
	return nil
}

// HandleSources is GET /api/v1/form-data/sources: what a model author can configure — the
// sources this server offers, and the Workers a field can name, each saying whether it
// lets forms search it.
func (s *Service) HandleSources(w http.ResponseWriter, r *http.Request) {
	workers := []Worker{}
	if s.cfg.Workers != nil {
		all, err := s.cfg.Workers(r)
		if err != nil {
			httpapi.Error(w, http.StatusInternalServerError, "list Workers: "+err.Error())
			return
		}
		for _, wk := range all {
			for _, p := range providers {
				if p.WorkerType == wk.WorkerType {
					workers = append(workers, wk)
					break
				}
			}
		}
	}
	sort.Slice(workers, func(i, j int) bool { return workers[i].Name < workers[j].Name })
	httpapi.JSON(w, http.StatusOK, map[string]any{"providers": Providers(), "workers": workers})
}
