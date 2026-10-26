package workerquery

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/pblumer/atlas/api/httpapi"
	"github.com/pblumer/atlas/connector/lookup"
)

// MaxPollWait bounds one long poll, as maxJobPollWait bounds a job poll. It stays
// below [Limits.PresentFor], so an Instance inside a poll is always present.
const MaxPollWait = 50 * time.Second

// maxRetryAdvice bounds the wait an Instance may pass on. A provider asking for more
// than an hour is not advice a person waiting at a form can use.
const maxRetryAdvice = 3600

// maxNextLength bounds a continuation. Graph's are a few hundred characters; this is
// only there so an answer cannot make the server seal an arbitrary blob.
const maxNextLength = 8192

// Service serves the worker half of the protocol: the poll and the answer. The asker's
// half is [Broker.Ask], called by whichever area of the server has a query to put.
type Service struct {
	broker *Broker
	// maxBody bounds an answer's body: the server's Request budget, supplied by the
	// server so it is the one an operator configured (ADR-0291).
	maxBody int64
}

// NewService serves broker over HTTP.
func NewService(broker *Broker, maxBody int64) *Service {
	return &Service{broker: broker, maxBody: maxBody}
}

// HandleActivate is POST /api/v1/worker-queries/activate: a Worker Instance's poll for
// queries, the query twin of POST /api/v1/jobs/activate.
//
// Body: {"type": "io.atlas.entra", "worker": "w1", "connectors": ["ucc_entra"],
// "maxQueries": 4, "waitMs": 30000}. The connectors are the Workers the Instance holds
// credentials for; only queries naming one of them are handed out, and reporting them
// is also what makes the Instance present for those Workers.
func (s *Service) HandleActivate(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Type       string   `json:"type"`
		Worker     string   `json:"worker"`
		Connectors []string `json:"connectors"`
		MaxQueries int      `json:"maxQueries"`
		WaitMs     int64    `json:"waitMs"`
	}
	if !decode(w, r, s.maxBody, &body) {
		return
	}
	body.Type, body.Worker = strings.TrimSpace(body.Type), strings.TrimSpace(body.Worker)
	if body.Type == "" || body.Worker == "" {
		httpapi.Error(w, http.StatusBadRequest, "type and worker are required: a query is leased by job type, to a named Worker Instance")
		return
	}
	wait := time.Duration(body.WaitMs) * time.Millisecond
	if wait > MaxPollWait {
		wait = MaxPollWait
	}
	got := s.broker.Lease(r.Context(), body.Type, body.Worker, body.Connectors, body.MaxQueries, wait)
	if got == nil {
		got = []Leased{}
	}
	httpapi.JSON(w, http.StatusOK, map[string]any{"queries": got})
}

// HandleAnswer is POST /api/v1/worker-queries/{id}/answer: the page a Worker Instance
// read, or the code it failed with, fenced by the lease it was handed.
//
// Body: {"worker": "w1", "leaseToken": "…", "page": {"items": [...], "next": "…"}} or
// {"worker": "w1", "leaseToken": "…", "error": {"code": "provider_throttled",
// "retryAfter": 10}}.
func (s *Service) HandleAnswer(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Worker     string        `json:"worker"`
		LeaseToken string        `json:"leaseToken"`
		Page       *lookup.Page  `json:"page"`
		Error      *lookup.Error `json:"error"`
	}
	if !decode(w, r, s.maxBody, &body) {
		return
	}
	if (body.Page == nil) == (body.Error == nil) {
		httpapi.Error(w, http.StatusBadRequest, "an answer carries exactly one of page and error")
		return
	}
	if body.Page != nil {
		if len(body.Page.Items) > lookup.MaxPageSize {
			httpapi.Error(w, http.StatusBadRequest, "a page carries more records than any query may ask for")
			return
		}
		if len(body.Page.Next) > maxNextLength {
			httpapi.Error(w, http.StatusBadRequest, "the continuation is longer than any provider's")
			return
		}
		if body.Page.Items == nil {
			body.Page.Items = []map[string]any{}
		}
	}
	if body.Error != nil {
		if !knownCode(body.Error.Code) {
			httpapi.Error(w, http.StatusBadRequest, "unknown lookup error code: "+body.Error.Code)
			return
		}
		if body.Error.RetryAfter < 0 || body.Error.RetryAfter > maxRetryAdvice {
			body.Error.RetryAfter = 0
		}
	}
	err := s.broker.Answer(r.PathValue("id"), strings.TrimSpace(body.Worker), body.LeaseToken,
		Answer{Page: body.Page, Err: body.Error})
	switch {
	case errors.Is(err, ErrGone):
		httpapi.Error(w, http.StatusNotFound, "the query is no longer waiting: its asker has gone or it was answered")
	case errors.Is(err, ErrFenced):
		httpapi.Error(w, http.StatusConflict, "the answer does not hold this query's lease")
	case err != nil:
		httpapi.Error(w, http.StatusInternalServerError, err.Error())
	default:
		httpapi.JSON(w, http.StatusOK, map[string]any{"id": r.PathValue("id")})
	}
}

// knownCode reports whether code is one a Worker Type may answer with.
func knownCode(code string) bool {
	switch code {
	case lookup.CodeInvalid, lookup.CodeUnconfigured, lookup.CodeThrottled, lookup.CodeUnavailable, lookup.CodeRefused:
		return true
	}
	return false
}

// decode reads a bounded JSON body that names nothing it does not expect.
func decode(w http.ResponseWriter, r *http.Request, maxBody int64, v any) bool {
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxBody))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			httpapi.Error(w, http.StatusRequestEntityTooLarge, "the body is larger than this server accepts")
			return false
		}
		httpapi.Error(w, http.StatusBadRequest, "invalid JSON body: "+err.Error())
		return false
	}
	return true
}
