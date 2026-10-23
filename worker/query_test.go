package worker_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/pblumer/atlas/api/workerquery"
	"github.com/pblumer/atlas/compiler"
	"github.com/pblumer/atlas/connector/lookup"
	"github.com/pblumer/atlas/worker"
)

// queryServer is the worker half of the query protocol, served by the server's own
// broker, so the worker is exercised against the real lease and fencing rather than a
// stub of them.
func queryServer(t *testing.T) (*workerquery.Broker, *httptest.Server) {
	t.Helper()
	b := workerquery.NewBroker(workerquery.DefaultLimits())
	s := workerquery.NewService(b, 1<<20)
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/v1/worker-queries/activate", s.HandleActivate)
	mux.HandleFunc("POST /api/v1/worker-queries/{id}/answer", s.HandleAnswer)
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return b, srv
}

type askResult struct {
	page lookup.Page
	err  error
}

func askAsync(b *workerquery.Broker, q lookup.Query) <-chan askResult {
	out := make(chan askResult, 1)
	go func() {
		p, err := b.Ask(context.Background(), compiler.EntraJobType, "ucc_entra", q, 20*time.Second)
		out <- askResult{p, err}
	}()
	return out
}

func queryWorker(srv *httptest.Server, fn worker.QueryFunc) *worker.Worker {
	return worker.New(worker.Options{
		Server: srv.URL, ID: "entra-1", Connectors: []string{"ucc_entra"},
		Queries: map[string]worker.QueryFunc{compiler.EntraJobType: fn},
	})
}

// present makes the worker known to the broker with a poll that finds nothing, as an
// idle Instance's poll does.
func presentWorker(t *testing.T, w *worker.Worker) {
	t.Helper()
	if err := w.AnswerQueriesOnce(context.Background(), 0); err != nil {
		t.Fatal(err)
	}
}

func TestAWorkerAnswersAQueryWithItsWorkersCredential(t *testing.T) {
	b, srv := queryServer(t)
	var seen struct {
		connector string
		query     lookup.Query
		deadline  bool
	}
	w := queryWorker(srv, func(ctx context.Context, connector string, q lookup.Query) (lookup.Page, error) {
		seen.connector, seen.query = connector, q
		_, seen.deadline = ctx.Deadline()
		return lookup.Page{Items: []map[string]any{{"id": "00000000-0000-0000-0000-000000000001", "displayName": "Anna"}}, Next: "n"}, nil
	})
	presentWorker(t, w)
	answer := askAsync(b, lookup.Query{Resource: "users", Search: "Ann", SearchFields: []string{"displayName"},
		Properties: []string{"id", "displayName"}})
	if err := w.AnswerQueriesOnce(context.Background(), 20*time.Second); err != nil {
		t.Fatal(err)
	}
	got := <-answer
	if got.err != nil || len(got.page.Items) != 1 || got.page.Next != "n" {
		t.Fatalf("asker got %+v", got)
	}
	if seen.connector != "ucc_entra" || seen.query.Search != "Ann" {
		t.Errorf("handler saw %+v", seen)
	}
	if !seen.deadline {
		t.Error("the handler ran without the asker's deadline")
	}
}

// What a handler fails with reaches the asker as a code and nothing else, because a
// provider's error text carries the request — and the request carries the term.
func TestAWorkerReportsAFailureAsItsCodeOnly(t *testing.T) {
	for _, tc := range []struct {
		err   error
		code  string
		retry int
	}{
		{&lookup.Error{Code: lookup.CodeThrottled, RetryAfter: 6}, lookup.CodeThrottled, 6},
		{lookup.Fail(lookup.CodeRefused), lookup.CodeRefused, 0},
		{errors.New("GET /users?$filter=startswith(displayName,'Geheim') failed"), lookup.CodeUnavailable, 0},
	} {
		b, srv := queryServer(t)
		w := queryWorker(srv, func(context.Context, string, lookup.Query) (lookup.Page, error) { return lookup.Page{}, tc.err })
		presentWorker(t, w)
		answer := askAsync(b, lookup.Query{Resource: "users", Properties: []string{"id"}})
		if err := w.AnswerQueriesOnce(context.Background(), 20*time.Second); err != nil {
			t.Fatal(err)
		}
		got := <-answer
		var le *lookup.Error
		if !errors.As(got.err, &le) || le.Code != tc.code || le.RetryAfter != tc.retry {
			t.Errorf("%v: asker got %v, want %s/%d", tc.err, got.err, tc.code, tc.retry)
		}
	}
}

func TestAWorkerWithOnlyQueriesStillRuns(t *testing.T) {
	_, srv := queryServer(t)
	w := queryWorker(srv, func(context.Context, string, lookup.Query) (lookup.Page, error) { return lookup.Page{}, nil })
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := w.Run(ctx); err != nil {
		t.Fatalf("Run = %v: a worker serving queries has something to serve", err)
	}
}

// The Entra Worker Type answers lookups wherever it serves jobs: a tenant configured for
// a worker is a tenant a form can search, if the server lets it.
func TestTheEntraWorkerServesLookups(t *testing.T) {
	env := map[string]string{
		"ATLAS_ENTRA_CONNECTORS":              "ucc_entra",
		"ATLAS_ENTRA_UCC_ENTRA_TENANT_ID":     "t",
		"ATLAS_ENTRA_UCC_ENTRA_CLIENT_ID":     "c",
		"ATLAS_ENTRA_UCC_ENTRA_CLIENT_SECRET": "s",
	}
	built, err := worker.BuiltinConnectors(func(k string) string { return env[k] }, "entra")
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := built.Queries[compiler.EntraJobType]; !ok {
		t.Fatalf("queries = %v, want %s", built.Queries, compiler.EntraJobType)
	}
	// A query naming a tenant this worker does not hold says so, rather than failing
	// in a way a person cannot tell from a broken directory.
	_, err = built.Queries[compiler.EntraJobType](context.Background(), "elsewhere",
		lookup.Query{Resource: "users", Properties: []string{"id"}})
	var le *lookup.Error
	if !errors.As(err, &le) || le.Code != lookup.CodeUnconfigured {
		t.Fatalf("err = %v, want %s", err, lookup.CodeUnconfigured)
	}
}
