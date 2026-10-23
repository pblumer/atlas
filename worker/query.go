package worker

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/pblumer/atlas/connector/lookup"
	"github.com/pblumer/atlas/logging"
)

// QueryFunc answers one form lookup for the Worker named connector: a page of records
// or the *lookup.Error it failed with (ADR-0473). Any other
// error is reported as the provider being unavailable — and only as that, because an
// error's text is the one place a search term could leave this process.
type QueryFunc func(ctx context.Context, connector string, q lookup.Query) (lookup.Page, error)

// leasedQuery is one query as the server hands it over.
type leasedQuery struct {
	ID         string          `json:"id"`
	LeaseToken string          `json:"leaseToken"`
	Type       string          `json:"type"`
	Connector  string          `json:"connector"`
	Deadline   int64           `json:"deadline"`
	Query      json.RawMessage `json:"query"`
}

// serveQueries keeps one job type's query places filled until ctx is cancelled — the
// shape of serve, for a message kind that is answered rather than completed.
func (w *Worker) serveQueries(ctx context.Context, jobType string) {
	places := make(chan struct{}, w.opts.MaxQueries)
	var running sync.WaitGroup
	defer running.Wait()
	failing := false
	for ctx.Err() == nil {
		select {
		case places <- struct{}{}:
		case <-ctx.Done():
			return
		}
		free := 1
	claim:
		for free < cap(places) {
			select {
			case places <- struct{}{}:
				free++
			default:
				break claim
			}
		}
		qs, err := w.activateQueries(ctx, jobType, free, w.opts.Wait)
		for i := len(qs); i < free; i++ {
			<-places
		}
		for i, q := range qs {
			if i >= free {
				places <- struct{}{}
			}
			running.Add(1)
			go func() {
				defer running.Done()
				defer func() { <-places }()
				w.answerQuery(ctx, q)
			}()
		}
		if err == nil || ctx.Err() != nil {
			failing = false
			continue
		}
		// Said once per run of failures rather than per poll: a server older than the
		// query protocol answers every poll 404, and a worker that logged each would
		// bury the one line that says why its tenants are not searchable.
		if !failing {
			logging.Warn(logging.WorkerPollFailed, "a query poll failed; retrying",
				slog.String("worker", w.opts.ID), slog.String("type", jobType),
				slog.String("error", err.Error()))
			failing = true
		}
		select {
		case <-ctx.Done():
		case <-time.After(w.opts.Retry):
		}
	}
}

// AnswerQueriesOnce polls every query type once, waiting up to wait for a query, and
// answers whatever it is given before it returns. It is what a test drives; a zero wait
// is an idle poll, which makes this worker present for its Workers.
func (w *Worker) AnswerQueriesOnce(ctx context.Context, wait time.Duration) error {
	for _, jobType := range w.queryTypes {
		qs, err := w.activateQueries(ctx, jobType, w.opts.MaxQueries, wait)
		if err != nil {
			return err
		}
		var wg sync.WaitGroup
		for _, q := range qs {
			wg.Add(1)
			go func() {
				defer wg.Done()
				w.answerQuery(ctx, q)
			}()
		}
		wg.Wait()
	}
	return nil
}

func (w *Worker) activateQueries(ctx context.Context, jobType string, max int, wait time.Duration) ([]leasedQuery, error) {
	body := map[string]any{
		"type": jobType, "worker": w.opts.ID, "maxQueries": max, "waitMs": wait.Milliseconds(),
		// Sent on every poll: it is what makes this Instance present for these Workers,
		// and what keeps a query for a tenant it does not hold from reaching it.
		"connectors": w.opts.Connectors,
	}
	var out struct {
		Queries []leasedQuery `json:"queries"`
	}
	if err := w.call(ctx, "/api/v1/worker-queries/activate", body, &out); err != nil {
		return nil, err
	}
	return out.Queries, nil
}

// answerQuery runs one query under its asker's deadline and reports the outcome. A
// report that is refused needs nothing done: the asker has gone, or never will hear.
func (w *Worker) answerQuery(ctx context.Context, q leasedQuery) {
	fn := w.opts.Queries[q.Type]
	body := map[string]any{"worker": w.opts.ID, "leaseToken": q.LeaseToken}
	var lq lookup.Query
	switch {
	case fn == nil:
		body["error"] = lookup.Fail(lookup.CodeUnconfigured)
	case json.Unmarshal(q.Query, &lq) != nil:
		body["error"] = lookup.Fail(lookup.CodeInvalid)
	default:
		qctx, cancel := context.WithDeadline(ctx, time.UnixMilli(q.Deadline))
		page, err := fn(qctx, q.Connector, lq)
		cancel()
		var le *lookup.Error
		switch {
		case err == nil:
			if page.Items == nil {
				page.Items = []map[string]any{}
			}
			body["page"] = page
		case errors.As(err, &le):
			body["error"] = &lookup.Error{Code: le.Code, RetryAfter: le.RetryAfter}
		default:
			body["error"] = lookup.Fail(lookup.CodeUnavailable)
		}
	}
	if err := w.call(ctx, "/api/v1/worker-queries/"+q.ID+"/answer", body, nil); err != nil &&
		!strings.Contains(err.Error(), "no longer waiting") {
		logging.Warn(logging.WorkerPollFailed, "a query answer was not taken",
			slog.String("worker", w.opts.ID), slog.String("type", q.Type),
			slog.String("error", err.Error()))
	}
}
