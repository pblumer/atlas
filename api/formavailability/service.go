package formavailability

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/pblumer/atlas/api/httpapi"
	"github.com/pblumer/atlas/api/workerquery"
	"github.com/pblumer/atlas/workersdk/lookup"
)

// Worker is a configured Worker as this area judges it: no endpoint and nowhere to put a
// credential.
type Worker struct {
	WorkerType string
	Enabled    bool
	// FormLookups is the Worker's own switch (ADR-0473): its editors decide whether a
	// form may ask anything through its credential. An availability check asks less
	// than a directory search, but through the same credential, so it needs the same
	// consent.
	FormLookups bool
}

// Config is the server's half of this area.
type Config struct {
	// Worker resolves a Worker by name from the Worker store.
	Worker func(name string) (Worker, bool, error)
	// Ask puts a query to a Worker Instance and waits for its answer.
	Ask func(ctx context.Context, jobType, connector string, q lookup.Query, timeout time.Duration) (lookup.Page, error)
	// Allow is the per-person rate limit.
	Allow func(key string) bool
	// Principal names the caller, for the rate limit. Empty when the server runs
	// without authentication.
	Principal func(r *http.Request) string
	// Timeout bounds one query.
	Timeout time.Duration
	// MaxBody bounds a request's body.
	MaxBody int64
}

// DefaultTimeout is what a person waits for at most. The checks run side by side, so a
// field with three sources waits as long as its slowest one.
const DefaultTimeout = 8 * time.Second

// Service answers availability checks. Build it with [New].
type Service struct{ cfg Config }

// New builds the service.
func New(cfg Config) *Service {
	if cfg.Timeout <= 0 {
		cfg.Timeout = DefaultTimeout
	}
	return &Service{cfg: cfg}
}

// apiError is a refused check: a status, a code a client can branch on, a message a
// person can read, and how long to wait where waiting helps. The codes are the
// directory field's (formlookup.Codes) plus invalid_value.
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

// Codes are the machine-readable codes a check answers with beside its message.
var Codes = []string{
	"not_found", "invalid_query", "invalid_value", "invalid_configuration", "forbidden", "rate_limited",
	"worker_unavailable", "worker_busy", "provider_throttled", "provider_unavailable", "provider_refused", "timeout",
}

// request is the whole of what a check may say.
type request struct {
	Value string `json:"value"`
}

// Answer is what a check answers: the value as it was checked, and whether every
// source the field names reports it free.
type Answer struct {
	Value     string `json:"value"`
	Available bool   `json:"available"`
}

// WriteCheck answers a check of one field of a stored form, read by the caller from
// wherever the form is reached — a form id, a task. The request names only the value.
func (s *Service) WriteCheck(w http.ResponseWriter, r *http.Request, schema []byte, fieldKey string) {
	principal := ""
	if s.cfg.Principal != nil {
		principal = s.cfg.Principal(r)
	}
	if s.cfg.Allow != nil && !s.cfg.Allow("availability:"+principal) {
		(&apiError{status: http.StatusTooManyRequests, code: "rate_limited",
			msg: "too many checks in a short time; wait a moment", retry: 1}).write(w)
		return
	}
	form, err := Parse(schema)
	if err != nil {
		(&apiError{status: http.StatusUnprocessableEntity, code: "invalid_configuration",
			msg: "the form's availability check cannot run: " + err.Error()}).write(w)
		return
	}
	f, ok := form.Field(fieldKey)
	if !ok {
		(&apiError{status: http.StatusNotFound, code: "not_found",
			msg: fmt.Sprintf("the form has no availability field %q", fieldKey)}).write(w)
		return
	}
	var req request
	if e := decode(w, r, s.cfg.MaxBody, &req); e != nil {
		e.write(w)
		return
	}
	value := Normalize(req.Value)
	if utf8.RuneCountInString(value) < f.MinLength() {
		(&apiError{status: http.StatusBadRequest, code: "invalid_value",
			msg: fmt.Sprintf("a name needs at least %d characters before it is checked", f.MinLength())}).write(w)
		return
	}
	for _, p := range f.Providers {
		if !p.Valid(value) {
			(&apiError{status: http.StatusBadRequest, code: "invalid_value",
				msg: "this name cannot be used: " + p.ValueFormat}).write(w)
			return
		}
	}
	for _, ck := range f.Source.Checks {
		if e := s.usable(f.Key, ck); e != nil {
			e.write(w)
			return
		}
	}
	available, err := s.ask(r.Context(), f, value)
	if err != nil {
		if errors.Is(err, context.Canceled) && r.Context().Err() != nil {
			return // the person typed on; nobody reads this answer
		}
		failure(err).write(w)
		return
	}
	httpapi.JSON(w, http.StatusOK, Answer{Value: value, Available: available})
}

// usable checks a source's Worker: it must exist, be of the source's Worker Type, be
// enabled, and allow forms to ask through it.
func (s *Service) usable(key string, ck Check) *apiError {
	if s.cfg.Worker == nil {
		return &apiError{status: http.StatusServiceUnavailable, code: "worker_unavailable", msg: "no Worker store"}
	}
	wk, ok, err := s.cfg.Worker(ck.Worker)
	switch {
	case err != nil:
		return &apiError{status: http.StatusInternalServerError, code: "internal", msg: "read Worker: " + err.Error()}
	case !ok:
		return &apiError{status: http.StatusUnprocessableEntity, code: "invalid_configuration",
			msg: fmt.Sprintf("the field %q names the Worker %q, which is not configured on this server", key, ck.Worker)}
	case wk.WorkerType != ck.WorkerType:
		return &apiError{status: http.StatusUnprocessableEntity, code: "invalid_configuration",
			msg: fmt.Sprintf("the field %q names the Worker %q, which is not a %s Worker", key, ck.Worker, ck.WorkerType)}
	case !wk.Enabled:
		return &apiError{status: http.StatusServiceUnavailable, code: "worker_unavailable",
			msg: fmt.Sprintf("the Worker %q is switched off", ck.Worker)}
	case !wk.FormLookups:
		return &apiError{status: http.StatusForbidden, code: "forbidden",
			msg: fmt.Sprintf("the Worker %q does not allow forms to ask through it; its owner can allow it in the Console", ck.Worker)}
	}
	return nil
}

// ask runs every check side by side and folds the answers: free only when every source
// found nothing. The first failure decides the error, because a name that one source
// could not check is not known to be free.
func (s *Service) ask(ctx context.Context, f Field, value string) (bool, error) {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	type result struct {
		taken bool
		err   error
	}
	results := make([]result, len(f.Source.Checks))
	var wg sync.WaitGroup
	for i, ck := range f.Source.Checks {
		p := f.Providers[i]
		wg.Add(1)
		go func() {
			defer wg.Done()
			page, err := s.cfg.Ask(ctx, p.JobType, ck.Worker, query(ck, p, value), s.cfg.Timeout)
			results[i] = result{taken: err == nil && len(page.Items) > 0, err: err}
		}()
	}
	wg.Wait()
	available := true
	for _, res := range results {
		if res.err != nil {
			return false, res.err
		}
		if res.taken {
			available = false
		}
	}
	return available, nil
}

// failure turns a query's failure into what the person's form is told. Nothing of the
// value travels in it.
func failure(err error) *apiError {
	const what = "the name could not be checked"
	var le *lookup.Error
	switch {
	case errors.Is(err, workerquery.ErrUnavailable):
		return &apiError{status: http.StatusServiceUnavailable, code: "worker_unavailable",
			msg: what + ": no Worker Instance serving this source is running"}
	case errors.Is(err, workerquery.ErrBusy):
		return &apiError{status: http.StatusServiceUnavailable, code: "worker_busy",
			msg: what + ": too many checks are waiting; try again in a moment", retry: 2}
	case errors.Is(err, workerquery.ErrTimeout), errors.Is(err, context.DeadlineExceeded):
		return &apiError{status: http.StatusGatewayTimeout, code: "timeout", msg: what + ": the source did not answer in time"}
	case errors.As(err, &le):
		switch le.Code {
		case lookup.CodeThrottled:
			return &apiError{status: http.StatusServiceUnavailable, code: "provider_throttled",
				msg: what + ": the source asked to be left alone for a moment", retry: le.RetryAfter}
		case lookup.CodeRefused:
			return &apiError{status: http.StatusBadGateway, code: "provider_refused",
				msg: what + ": the source refused the Worker's request; its permissions may be missing"}
		case lookup.CodeUnconfigured:
			return &apiError{status: http.StatusServiceUnavailable, code: "worker_unavailable",
				msg: what + ": the running Worker Instance does not hold this Worker"}
		case lookup.CodeInvalid:
			return &apiError{status: http.StatusUnprocessableEntity, code: "invalid_configuration",
				msg: what + ": the Worker refused the check; the server and the Worker may be different versions"}
		}
	}
	return &apiError{status: http.StatusBadGateway, code: "provider_unavailable", msg: what + ": the source did not answer"}
}

// decode reads a bounded JSON body that names nothing but the value.
func decode(w http.ResponseWriter, r *http.Request, maxBody int64, v any) *apiError {
	if maxBody <= 0 {
		maxBody = 1 << 12
	}
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxBody))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			return &apiError{status: http.StatusRequestEntityTooLarge, code: "invalid_query", msg: "the request is larger than a check can be"}
		}
		return &apiError{status: http.StatusBadRequest, code: "invalid_query", msg: fmt.Sprintf("the request is not a check: %v", err)}
	}
	return nil
}
