package workersdk_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/pblumer/atlas/workersdk"
	"github.com/pblumer/atlas/workersdk/lookup"
)

// The protocol tests drive the client against a recording stand-in for atlas: what
// matters to a Worker Type built against this package is what crosses the wire — the
// paths, the bodies, the headers — so that is what they read. The end-to-end tests
// against a live engine stay in package worker, which may import the engine.

// recorded is one request the stand-in received.
type recorded struct {
	path   string
	header http.Header
	body   map[string]any
}

// protoStub answers each path with whatever its routes say and records every request.
type protoStub struct {
	mu     sync.Mutex
	reqs   []recorded
	routes map[string]http.HandlerFunc
}

func newProtoStub(t *testing.T, routes map[string]http.HandlerFunc) (*protoStub, *httptest.Server) {
	t.Helper()
	s := &protoStub{routes: routes}
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		var body map[string]any
		_ = json.Unmarshal(raw, &body)
		s.mu.Lock()
		s.reqs = append(s.reqs, recorded{path: r.URL.Path, header: r.Header.Clone(), body: body})
		s.mu.Unlock()
		if h, ok := s.routes[r.URL.Path]; ok {
			h(w, r)
			return
		}
		writeJSON(w, http.StatusOK, `{}`)
	}))
	t.Cleanup(ts.Close)
	return s, ts
}

// to returns the requests sent to path, in order.
func (s *protoStub) to(path string) []recorded {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []recorded
	for _, r := range s.reqs {
		if r.path == path {
			out = append(out, r)
		}
	}
	return out
}

func writeJSON(w http.ResponseWriter, status int, body string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = io.WriteString(w, body)
}

// oneJob answers the first poll with job and every later one with none.
func oneJob(job string) http.HandlerFunc {
	var once sync.Once
	return func(w http.ResponseWriter, _ *http.Request) {
		sent := false
		once.Do(func() {
			writeJSON(w, http.StatusOK, `{"jobs":[`+job+`]}`)
			sent = true
		})
		if !sent {
			writeJSON(w, http.StatusOK, `{"jobs":[]}`)
		}
	}
}

// runOnce runs one pass of a worker built from opts against ts.
func runOnce(t *testing.T, ts *httptest.Server, opts workersdk.Options) error {
	t.Helper()
	opts.Server = ts.URL
	if opts.ID == "" {
		opts.ID = "w-1"
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	return workersdk.New(opts).RunOnce(ctx)
}

func handling(jobType string, exec workersdk.Exec) map[string]workersdk.Exec {
	return map[string]workersdk.Exec{jobType: exec}
}

// A completed job reports its variables, a decision and an agent's tool calls, under the
// lease it was handed; the poll carries what the worker can reach and its credential.
func TestACompletedJobReportsEverythingItProduced(t *testing.T) {
	stub, ts := newProtoStub(t, map[string]http.HandlerFunc{
		"/api/v1/jobs/activate": oneJob(`{"jobKey":7,"type":"decide","retries":3,"leaseToken":11}`),
	})
	err := runOnce(t, ts, workersdk.Options{
		Token:      "tok",
		Connectors: []string{"temis-prod"},
		Endpoints:  map[string]string{"temis-prod": "https://temis.example:443"},
		Handlers: handling("decide", workersdk.CompletingExecFunc(func(context.Context, workersdk.Job) (workersdk.Outcome, error) {
			return workersdk.Outcome{
				Variables: map[string]any{"approved": true},
				Decision:  &workersdk.DecisionReport{DecisionID: "d1", Outputs: map[string]any{"ok": true}},
				ToolCalls: []workersdk.ToolCallReport{{Tool: "lookup", CallId: "c1"}},
			}, nil
		})),
	})
	if err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	polls := stub.to("/api/v1/jobs/activate")
	if len(polls) != 1 {
		t.Fatalf("polls = %d, want 1", len(polls))
	}
	poll := polls[0]
	if got := poll.header.Get("Authorization"); got != "Bearer tok" {
		t.Errorf("Authorization = %q, want the bearer token", got)
	}
	if poll.body["type"] != "decide" || poll.body["worker"] != "w-1" {
		t.Errorf("poll body = %v, want the type and this worker's id", poll.body)
	}
	if _, ok := poll.body["connectors"]; !ok {
		t.Errorf("poll body = %v, want the Workers this instance holds", poll.body)
	}
	if _, ok := poll.body["endpoints"]; !ok {
		t.Errorf("poll body = %v, want where those Workers connect", poll.body)
	}
	done := stub.to("/api/v1/jobs/7/complete")
	if len(done) != 1 {
		t.Fatalf("completions = %d, want 1", len(done))
	}
	body := done[0].body
	if body["leaseToken"] != float64(11) {
		t.Errorf("leaseToken = %v, want the lease the job was handed", body["leaseToken"])
	}
	for _, key := range []string{"variables", "decision", "toolCalls"} {
		if _, ok := body[key]; !ok {
			t.Errorf("completion body = %v, want %s", body, key)
		}
	}
}

// A job that completes with nothing sends no variables, no decision and no calls: an
// agent reporting no calls has said it is done.
func TestAJobThatProducedNothingCompletesBare(t *testing.T) {
	stub, ts := newProtoStub(t, map[string]http.HandlerFunc{
		"/api/v1/jobs/activate": oneJob(`{"jobKey":8,"type":"noop","retries":1,"leaseToken":2}`),
	})
	if err := runOnce(t, ts, workersdk.Options{Handlers: handling("noop", workersdk.ExecFunc(func(context.Context, workersdk.Job) (map[string]any, error) {
		return nil, nil
	}))}); err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	done := stub.to("/api/v1/jobs/8/complete")
	if len(done) != 1 {
		t.Fatalf("completions = %d, want 1", len(done))
	}
	for _, key := range []string{"variables", "decision", "toolCalls"} {
		if _, ok := done[0].body[key]; ok {
			t.Errorf("completion body = %v, want no %s", done[0].body, key)
		}
	}
}

// A failure spends one attempt and asks the engine to hold the job back as long as the
// server's own runner would; with no attempts left it asks for no delay at all.
func TestAFailedJobSpendsAnAttemptAndWaitsAsTheServerWould(t *testing.T) {
	for _, tc := range []struct {
		name        string
		retries     int
		opts        workersdk.Options
		wantRetries float64
		wantBackoff any // nil: not sent
	}{
		{name: "default policy, two left", retries: 3, wantRetries: 2, wantBackoff: float64(15000)},
		{name: "default policy, one left", retries: 2, wantRetries: 1, wantBackoff: float64(60000)},
		{name: "last attempt", retries: 1, wantRetries: 0},
		{name: "already none", retries: 0, wantRetries: 0},
		{name: "no backoff", retries: 3, opts: workersdk.Options{NoRetryBackoff: true}, wantRetries: 2},
		{name: "own policy", retries: 3, wantRetries: 2, wantBackoff: float64(250),
			opts: workersdk.Options{RetryBackoff: func(int32) int64 { return int64(250 * time.Millisecond) }}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			job := `{"jobKey":9,"type":"call","leaseToken":4,"retries":` + itoa(tc.retries) + `}`
			stub, ts := newProtoStub(t, map[string]http.HandlerFunc{"/api/v1/jobs/activate": oneJob(job)})
			opts := tc.opts
			opts.Handlers = handling("call", workersdk.ExecFunc(func(context.Context, workersdk.Job) (map[string]any, error) {
				return nil, errors.New("target down")
			}))
			if err := runOnce(t, ts, opts); err != nil {
				t.Fatalf("RunOnce: %v", err)
			}
			fails := stub.to("/api/v1/jobs/9/fail")
			if len(fails) != 1 {
				t.Fatalf("failures = %d, want 1", len(fails))
			}
			body := fails[0].body
			if body["retries"] != tc.wantRetries || body["message"] != "target down" {
				t.Errorf("fail body = %v, want retries %v and the handler's message", body, tc.wantRetries)
			}
			if got := body["retryBackoff"]; got != tc.wantBackoff {
				t.Errorf("retryBackoff = %v, want %v", got, tc.wantBackoff)
			}
		})
	}
}

func itoa(n int) string {
	b, _ := json.Marshal(n)
	return string(b)
}

// A BPMN error is thrown with its code and message and spends no attempt; one without a
// code is the handler's fault and fails the job.
func TestABPMNErrorIsThrownAndACodelessOneFails(t *testing.T) {
	stub, ts := newProtoStub(t, map[string]http.HandlerFunc{
		"/api/v1/jobs/activate": oneJob(`{"jobKey":5,"type":"pay","retries":3,"leaseToken":1},{"jobKey":6,"type":"pay","retries":3,"leaseToken":2}`),
	})
	err := runOnce(t, ts, workersdk.Options{MaxJobs: 2, Handlers: handling("pay", workersdk.ExecFunc(func(_ context.Context, j workersdk.Job) (map[string]any, error) {
		if j.JobKey == 5 {
			return nil, &workersdk.BPMNError{Code: "DECLINED", Message: "card declined"}
		}
		return nil, &workersdk.BPMNError{Code: "  "}
	}))})
	if err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	thrown := stub.to("/api/v1/jobs/5/error")
	if len(thrown) != 1 || thrown[0].body["errorCode"] != "DECLINED" || thrown[0].body["errorMessage"] != "card declined" {
		t.Errorf("thrown = %+v, want DECLINED with its message", thrown)
	}
	if got := len(stub.to("/api/v1/jobs/6/fail")); got != 1 {
		t.Errorf("a code-less BPMN error was failed %d times, want 1", got)
	}
	if got := len(stub.to("/api/v1/jobs/6/error")); got != 0 {
		t.Errorf("a code-less BPMN error was thrown %d times, want 0", got)
	}
	if got := (&workersdk.BPMNError{Code: "X"}).Error(); got != "BPMN error X" {
		t.Errorf("Error() = %q", got)
	}
	if got := (&workersdk.BPMNError{Code: "X", Message: "why"}).Error(); got != "BPMN error X: why" {
		t.Errorf("Error() = %q", got)
	}
}

// A refusal of the poll is an error for RunOnce, worded with the server's reason when it
// gave one and with the status when it did not.
func TestARefusedPollSaysWhy(t *testing.T) {
	for _, tc := range []struct {
		name   string
		answer http.HandlerFunc
		want   string
	}{
		{"with reason", func(w http.ResponseWriter, _ *http.Request) {
			writeJSON(w, http.StatusNotFound, `{"error":"no job type call"}`)
		}, "no job type call"},
		{"plain status", func(w http.ResponseWriter, _ *http.Request) {
			http.Error(w, "gone", http.StatusServiceUnavailable)
		}, "status 503"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, ts := newProtoStub(t, map[string]http.HandlerFunc{"/api/v1/jobs/activate": tc.answer})
			err := runOnce(t, ts, workersdk.Options{Handlers: handling("call", workersdk.ExecFunc(func(context.Context, workersdk.Job) (map[string]any, error) {
				return nil, nil
			}))})
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("RunOnce = %v, want an error naming %q", err, tc.want)
			}
		})
	}
}

// A worker with nothing to serve says so instead of polling forever over nothing.
func TestAWorkerWithNothingToServeRefusesToRun(t *testing.T) {
	w := workersdk.New(workersdk.Options{Server: "http://127.0.0.1:1"})
	if err := w.Run(context.Background()); err == nil {
		t.Error("Run with no types = nil, want an error")
	}
	if err := w.RunOnce(context.Background()); err == nil {
		t.Error("RunOnce with no types = nil, want an error")
	}
}

// A failing poll is retried, not fatal: the server may be restarting. Run ends only when
// its context does.
func TestRunRetriesAFailingPollUntilCancelled(t *testing.T) {
	stub, ts := newProtoStub(t, map[string]http.HandlerFunc{
		"/api/v1/jobs/activate": func(w http.ResponseWriter, _ *http.Request) {
			writeJSON(w, http.StatusInternalServerError, `{"error":"restarting"}`)
		},
		"/api/v1/worker-queries/activate": func(w http.ResponseWriter, _ *http.Request) {
			writeJSON(w, http.StatusNotFound, `{"error":"older server"}`)
		},
	})
	logs := &syncBuffer{}
	w := workersdk.New(workersdk.Options{
		Server: ts.URL, ID: "w-1", Retry: time.Millisecond, Wait: time.Millisecond,
		Logger:   slog.New(slog.NewJSONHandler(logs, nil)),
		Handlers: handling("call", workersdk.ExecFunc(func(context.Context, workersdk.Job) (map[string]any, error) { return nil, nil })),
		Queries: map[string]workersdk.QueryFunc{"call": func(context.Context, string, lookup.Query) (lookup.Page, error) {
			return lookup.Page{}, nil
		}},
	})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- w.Run(ctx) }()
	waitUntil(t, "three failed job polls and three failed query polls", func() bool {
		return len(stub.to("/api/v1/jobs/activate")) >= 3 && len(stub.to("/api/v1/worker-queries/activate")) >= 3
	})
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Errorf("Run = %v, want nil once cancelled", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("Run did not return after cancellation")
	}
	// The retried polls are said on the logger the Worker Type gave, under the event
	// name an operator alerts on.
	if out := logs.String(); !strings.Contains(out, `"event":"`+workersdk.EventPollFailed+`"`) ||
		!strings.Contains(out, "a job poll failed") || !strings.Contains(out, "a query poll failed") {
		t.Errorf("log = %s, want both failed polls under %s", out, workersdk.EventPollFailed)
	}
}

// syncBuffer is a bytes.Buffer safe for the worker's goroutines to write to.
type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// Run keeps serving jobs and queries until cancelled, and finishes the work it started.
func TestRunServesJobsAndQueriesUntilCancelled(t *testing.T) {
	var queried sync.Once
	stub, ts := newProtoStub(t, map[string]http.HandlerFunc{
		"/api/v1/jobs/activate": oneJob(`{"jobKey":3,"type":"call","retries":1,"leaseToken":9}`),
		"/api/v1/worker-queries/activate": func(w http.ResponseWriter, _ *http.Request) {
			sent := false
			queried.Do(func() {
				writeJSON(w, http.StatusOK, `{"queries":[{"id":"q1","leaseToken":"l1","type":"call","connector":"c","deadline":`+
					itoa(int(time.Now().Add(time.Minute).UnixMilli()))+`,"query":{"resource":"users","properties":["id"]}}]}`)
				sent = true
			})
			if !sent {
				time.Sleep(time.Millisecond)
				writeJSON(w, http.StatusOK, `{"queries":[]}`)
			}
		},
	})
	w := workersdk.New(workersdk.Options{
		Server: ts.URL, ID: "w-1", Wait: time.Millisecond, Retry: time.Millisecond,
		Handlers: handling("call", workersdk.ExecFunc(func(context.Context, workersdk.Job) (map[string]any, error) {
			return map[string]any{"ok": true}, nil
		})),
		Queries: map[string]workersdk.QueryFunc{"call": func(context.Context, string, lookup.Query) (lookup.Page, error) {
			return lookup.Page{Items: []map[string]any{{"id": "u1"}}}, nil
		}},
	})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- w.Run(ctx) }()
	waitUntil(t, "the job completed and the query answered", func() bool {
		return len(stub.to("/api/v1/jobs/3/complete")) == 1 && len(stub.to("/api/v1/worker-queries/q1/answer")) == 1
	})
	cancel()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("Run did not return after cancellation")
	}
	if page, ok := stub.to("/api/v1/worker-queries/q1/answer")[0].body["page"].(map[string]any); !ok || page["items"] == nil {
		t.Errorf("answer = %v, want the page", stub.to("/api/v1/worker-queries/q1/answer")[0].body)
	}
}

// Each way a query ends crosses the wire as a page or a bare code — never a provider's
// own message, which could carry the person's search term.
func TestAQueryIsAnsweredWithAPageOrACodeOnly(t *testing.T) {
	deadline := itoa(int(time.Now().Add(time.Minute).UnixMilli()))
	query := func(id, typ, raw string) string {
		return `{"id":"` + id + `","leaseToken":"l-` + id + `","type":"` + typ + `","connector":"c","deadline":` + deadline + `,"query":` + raw + `}`
	}
	valid := `{"resource":"users","properties":["id"]}`
	var once sync.Once
	stub, ts := newProtoStub(t, map[string]http.HandlerFunc{
		"/api/v1/worker-queries/activate": func(w http.ResponseWriter, _ *http.Request) {
			sent := false
			once.Do(func() {
				writeJSON(w, http.StatusOK, `{"queries":[`+strings.Join([]string{
					query("empty", "dir", valid),
					query("rejected", "dir", valid),
					query("gone", "dir", valid),
					query("garbled", "dir", `"not an object"`),
					query("unknown", "other", valid),
				}, ",")+`]}`)
				sent = true
			})
			if !sent {
				writeJSON(w, http.StatusOK, `{"queries":[]}`)
			}
		},
		// The asker has gone: a refused answer needs nothing done.
		"/api/v1/worker-queries/gone/answer": func(w http.ResponseWriter, _ *http.Request) {
			writeJSON(w, http.StatusGone, `{"error":"no longer waiting"}`)
		},
		"/api/v1/worker-queries/rejected/answer": func(w http.ResponseWriter, _ *http.Request) {
			writeJSON(w, http.StatusInternalServerError, `{"error":"something else"}`)
		},
	})
	w := workersdk.New(workersdk.Options{
		Server: ts.URL, ID: "w-1", MaxQueries: 8, Connectors: []string{"c"},
		Queries: map[string]workersdk.QueryFunc{"dir": func(_ context.Context, _ string, q lookup.Query) (lookup.Page, error) {
			return lookup.Page{}, nil
		}},
	})
	// "gone" and "rejected" are answered like "empty"; their answers are refused, which
	// the worker takes without stopping — the asker has gone, or the refusal is logged.
	if err := w.AnswerQueriesOnce(context.Background(), 0); err != nil {
		t.Fatalf("AnswerQueriesOnce: %v", err)
	}
	if page, ok := stub.to("/api/v1/worker-queries/empty/answer")[0].body["page"].(map[string]any); !ok {
		t.Errorf("empty answer = %v, want a page", stub.to("/api/v1/worker-queries/empty/answer")[0].body)
	} else if items, ok := page["items"].([]any); !ok || len(items) != 0 {
		t.Errorf("items = %v, want an empty list rather than null", page["items"])
	}
	if code := answerCode(stub, "garbled"); code != lookup.CodeInvalid {
		t.Errorf("garbled query answered %q, want %q", code, lookup.CodeInvalid)
	}
	if code := answerCode(stub, "unknown"); code != lookup.CodeUnconfigured {
		t.Errorf("query of a type this worker does not answer got %q, want %q", code, lookup.CodeUnconfigured)
	}
}

// A query function's own error crosses the wire as its lookup code, and any other error
// as the provider being unavailable, with nothing of its text.
func TestAQueryFunctionsErrorTravelsAsItsCodeAlone(t *testing.T) {
	deadline := itoa(int(time.Now().Add(time.Minute).UnixMilli()))
	var once sync.Once
	stub, ts := newProtoStub(t, map[string]http.HandlerFunc{
		"/api/v1/worker-queries/activate": func(w http.ResponseWriter, _ *http.Request) {
			sent := false
			once.Do(func() {
				writeJSON(w, http.StatusOK, `{"queries":[`+
					`{"id":"slow","leaseToken":"a","type":"dir","connector":"throttle","deadline":`+deadline+`,"query":{"resource":"users"}},`+
					`{"id":"down","leaseToken":"b","type":"dir","connector":"secret search term","deadline":`+deadline+`,"query":{"resource":"users"}}]}`)
				sent = true
			})
			if !sent {
				writeJSON(w, http.StatusOK, `{"queries":[]}`)
			}
		},
	})
	w := workersdk.New(workersdk.Options{
		Server: ts.URL, ID: "w-1",
		Queries: map[string]workersdk.QueryFunc{"dir": func(_ context.Context, connector string, _ lookup.Query) (lookup.Page, error) {
			if connector == "throttle" {
				return lookup.Page{}, &lookup.Error{Code: lookup.CodeThrottled, RetryAfter: 30}
			}
			return lookup.Page{}, errors.New("provider said: " + connector)
		}},
	})
	if err := w.AnswerQueriesOnce(context.Background(), 0); err != nil {
		t.Fatalf("AnswerQueriesOnce: %v", err)
	}
	if code := answerCode(stub, "slow"); code != lookup.CodeThrottled {
		t.Errorf("throttled answer = %q, want %q", code, lookup.CodeThrottled)
	}
	down := stub.to("/api/v1/worker-queries/down/answer")
	if code := answerCode(stub, "down"); code != lookup.CodeUnavailable {
		t.Errorf("failed answer = %q, want %q", code, lookup.CodeUnavailable)
	}
	raw, _ := json.Marshal(down[0].body)
	if strings.Contains(string(raw), "secret search term") {
		t.Errorf("answer %s carries the provider's text", raw)
	}
}

// A refused query poll is an error for AnswerQueriesOnce.
func TestARefusedQueryPollIsAnError(t *testing.T) {
	_, ts := newProtoStub(t, map[string]http.HandlerFunc{
		"/api/v1/worker-queries/activate": func(w http.ResponseWriter, _ *http.Request) {
			writeJSON(w, http.StatusForbidden, `{"error":"not this worker"}`)
		},
	})
	w := workersdk.New(workersdk.Options{Server: ts.URL, ID: "w-1",
		Queries: map[string]workersdk.QueryFunc{"dir": func(context.Context, string, lookup.Query) (lookup.Page, error) {
			return lookup.Page{}, nil
		}}})
	if err := w.AnswerQueriesOnce(context.Background(), 0); err == nil || !strings.Contains(err.Error(), "not this worker") {
		t.Errorf("AnswerQueriesOnce = %v, want the server's refusal", err)
	}
}

func answerCode(stub *protoStub, id string) string {
	answers := stub.to("/api/v1/worker-queries/" + id + "/answer")
	if len(answers) != 1 {
		return "no answer"
	}
	e, ok := answers[0].body["error"].(map[string]any)
	if !ok {
		return "no error"
	}
	code, _ := e["code"].(string)
	return code
}

// The server's retry policy, restated: one retry left waits a minute, more wait fifteen
// seconds, none raises the incident at once.
func TestDefaultRetryBackoff(t *testing.T) {
	for retries, want := range map[int32]time.Duration{-1: 0, 0: 0, 1: time.Minute, 2: 15 * time.Second, 9: 15 * time.Second} {
		if got := time.Duration(workersdk.DefaultRetryBackoff(retries)); got != want {
			t.Errorf("DefaultRetryBackoff(%d) = %v, want %v", retries, got, want)
		}
	}
}

// RunHandler asks a handler that can report more for the wider outcome and keeps a plain
// one to its variables.
func TestRunHandlerPicksTheHandlersShape(t *testing.T) {
	plain := workersdk.ExecFunc(func(context.Context, workersdk.Job) (map[string]any, error) {
		return map[string]any{"a": 1}, nil
	})
	out, err := workersdk.RunHandler(context.Background(), plain, workersdk.Job{})
	if err != nil || out.Variables["a"] != 1 || out.Decision != nil {
		t.Errorf("plain = %+v, %v", out, err)
	}
	completing := workersdk.CompletingExecFunc(func(context.Context, workersdk.Job) (workersdk.Outcome, error) {
		return workersdk.Outcome{Variables: map[string]any{"b": 2}, Decision: &workersdk.DecisionReport{DecisionID: "d"}}, nil
	})
	out, err = workersdk.RunHandler(context.Background(), completing, workersdk.Job{})
	if err != nil || out.Decision == nil {
		t.Errorf("completing = %+v, %v", out, err)
	}
	vars, err := completing.Run(context.Background(), workersdk.Job{})
	if err != nil || vars["b"] != 2 {
		t.Errorf("CompletingExecFunc.Run = %v, %v; want its variables", vars, err)
	}
}

// CmdExec hands the job to a command as JSON on stdin and completes with the JSON object
// it prints; a non-zero exit fails the job with the command's stderr.
func TestCmdExecRunsACommandPerJob(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the commands below are POSIX shell")
	}
	job := workersdk.Job{JobKey: 1, Type: "echo", Variables: map[string]any{"to": "a@example.com"}}
	for _, tc := range []struct {
		name    string
		script  string
		want    map[string]any
		wantErr string
	}{
		{name: "variables", script: `read -r line; printf '{"seen": %s}' "$(printf '%s' "$line" | wc -c | tr -d ' ')"`},
		{name: "env reaches it", script: `printf '{"x":"%s"}' "$EXTRA"`, want: map[string]any{"x": "yes"}},
		{name: "no output", script: `cat >/dev/null`},
		{name: "not JSON", script: `echo hello`, wantErr: "not a JSON object"},
		{name: "fails with stderr", script: `echo boom >&2; exit 3`, wantErr: "boom"},
		{name: "fails silently", script: `exit 4`, wantErr: "exit status 4"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			vars, err := workersdk.CmdExec{Name: "sh", Args: []string{"-c", tc.script}, Env: []string{"EXTRA=yes"}}.Run(context.Background(), job)
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("Run = %v, %v; want an error naming %q", vars, err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("Run: %v", err)
			}
			for k, v := range tc.want {
				if vars[k] != v {
					t.Errorf("vars[%q] = %v, want %v", k, vars[k], v)
				}
			}
			if tc.name == "variables" {
				if _, ok := vars["seen"]; !ok {
					t.Errorf("vars = %v, want what the command printed", vars)
				}
			}
			if tc.name == "no output" && vars != nil {
				t.Errorf("vars = %v, want none", vars)
			}
		})
	}
}

// A worker built without its own client gets one bounded by a poll plus a lease, and
// RunOnce with a worker given every default still polls.
func TestNewFillsInTheDefaults(t *testing.T) {
	stub, ts := newProtoStub(t, map[string]http.HandlerFunc{
		"/api/v1/jobs/activate": func(w http.ResponseWriter, _ *http.Request) { writeJSON(w, http.StatusOK, `{"jobs":[]}`) },
	})
	if err := runOnce(t, ts, workersdk.Options{Handlers: handling("call", workersdk.ExecFunc(func(context.Context, workersdk.Job) (map[string]any, error) {
		return nil, nil
	}))}); err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	poll := stub.to("/api/v1/jobs/activate")[0].body
	if poll["leaseMs"] != float64(workersdk.DefaultLease.Milliseconds()) || poll["waitMs"] != float64(workersdk.DefaultWait.Milliseconds()) ||
		poll["maxJobs"] != float64(workersdk.DefaultMaxJobs) {
		t.Errorf("poll = %v, want the defaults", poll)
	}
}

// waitUntil polls cond until it holds or twenty seconds pass.
func waitUntil(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(2 * time.Millisecond)
	}
}
