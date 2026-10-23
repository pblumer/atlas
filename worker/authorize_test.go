package worker

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

// askedRequest is one request a fake Atlas server received.
type askedRequest struct {
	method string
	path   string
	header http.Header
	body   map[string]any
}

// askingAtlas stands in for an Atlas server: it records every request it is sent and
// answers each with whatever its answer handler writes.
type askingAtlas struct {
	mu   sync.Mutex
	reqs []askedRequest
}

func newAskingAtlas(t *testing.T, answer http.HandlerFunc) (*httptest.Server, *askingAtlas) {
	t.Helper()
	a := &askingAtlas{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		var body map[string]any
		_ = json.Unmarshal(raw, &body)
		a.mu.Lock()
		a.reqs = append(a.reqs, askedRequest{method: r.Method, path: r.URL.Path, header: r.Header.Clone(), body: body})
		a.mu.Unlock()
		// The answer may read the body too.
		r.Body = io.NopCloser(bytes.NewReader(raw))
		answer(w, r)
	}))
	t.Cleanup(srv.Close)
	return srv, a
}

func (a *askingAtlas) requests() []askedRequest {
	a.mu.Lock()
	defer a.mu.Unlock()
	return append([]askedRequest(nil), a.reqs...)
}

// answerJSON answers every request with status and a JSON body, as the API does.
func answerJSON(status int, body string) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}
}

// askFor builds the question a worker puts within reach of job j's handler, and asks it.
func askFor(t *testing.T, opts Options, j Job) (*Authorization, error) {
	t.Helper()
	w := New(opts)
	return Authorize(w.withAuthorizer(context.Background(), j))
}

const permitWithCredential = `{"enforcement":"enforce","permitted":true,"enforced":true,
  "reasonCode":"PERMIT","decisionKey":99,"job":{"connector":"sp-prod","path":"Drafts/old.docx"},
  "credential":{"type":"bearer","accessToken":"graph-token-1","expiresAt":1700000000}}`

// A handler run outside a worker — a test, or a caller embedding the Run*Job functions —
// has nobody to ask, and that is the legacy answer rather than a refusal.
func TestAHandlerRunOutsideAWorkerHasNobodyToAsk(t *testing.T) {
	auth, err := Authorize(context.Background())
	if err != nil || auth != nil {
		t.Fatalf("Authorize = (%+v, %v), want (nil, nil)", auth, err)
	}
	if auth.Governed() {
		t.Error("the legacy answer reads as governed")
	}
}

// Only an answer from an identity policy governs the action; "off" is the server saying
// this Worker has none, which goes on exactly as before.
func TestOnlyAnAnswerUnderAPolicyIsGoverned(t *testing.T) {
	for _, tc := range []struct {
		name string
		auth *Authorization
		want bool
	}{
		{"no answer", nil, false},
		{"no enforcement stated", &Authorization{}, false},
		{"enforcement off", &Authorization{Enforcement: "off", Permitted: true}, false},
		{"shadow", &Authorization{Enforcement: "shadow"}, true},
		{"enforce", &Authorization{Enforcement: "enforce", Permitted: true, Enforced: true}, true},
	} {
		if got := tc.auth.Governed(); got != tc.want {
			t.Errorf("%s: Governed() = %v, want %v", tc.name, got, tc.want)
		}
	}
}

// The worker asks about the job it leased, under its own name and lease, and presents
// the broker key it was handed — the one thing that lets the server release a person's
// token to this process. The answer arrives whole: the decision, the parameters it
// covers and the credential to act with.
func TestAWorkerAsksWithItsBrokerKey(t *testing.T) {
	srv, atlas := newAskingAtlas(t, answerJSON(http.StatusOK, permitWithCredential))

	auth, err := askFor(t, Options{Server: srv.URL, ID: "sharepoint-1", Token: "supervised", BrokerKey: "bk-1"},
		Job{JobKey: 42, LeaseToken: 7})
	if err != nil {
		t.Fatalf("Authorize: %v", err)
	}

	reqs := atlas.requests()
	if len(reqs) != 1 {
		t.Fatalf("the server was asked %d times, want once: %+v", len(reqs), reqs)
	}
	got := reqs[0]
	if got.method != http.MethodPost || got.path != "/api/v1/jobs/42/authorize" {
		t.Errorf("asked %s %s, want POST /api/v1/jobs/42/authorize", got.method, got.path)
	}
	if keys := got.header.Values("X-Atlas-Broker-Key"); len(keys) != 1 || keys[0] != "bk-1" {
		t.Errorf("X-Atlas-Broker-Key = %q, want exactly the configured key", keys)
	}
	if bearer := got.header.Get("Authorization"); bearer != "Bearer supervised" {
		t.Errorf("Authorization = %q, want the worker's own token beside the broker key", bearer)
	}
	if got.body["worker"] != "sharepoint-1" || got.body["leaseToken"] != float64(7) {
		t.Errorf("body = %v, want the worker's name and the job's lease token", got.body)
	}

	if !auth.Governed() || !auth.Permitted || !auth.Enforced {
		t.Errorf("answer = %+v, want a governed, enforced permit", auth)
	}
	if auth.ReasonCode != "PERMIT" || auth.DecisionKey != 99 {
		t.Errorf("reason %q, decision %d; want PERMIT and 99", auth.ReasonCode, auth.DecisionKey)
	}
	if auth.Job["path"] != "Drafts/old.docx" {
		t.Errorf("job = %v, want the parameters the decision covers", auth.Job)
	}
	if auth.Credential == nil || auth.Credential.AccessToken != "graph-token-1" ||
		auth.Credential.Type != "bearer" || auth.Credential.ExpiresAt != 1700000000 {
		t.Errorf("credential = %+v, want the brokered token", auth.Credential)
	}
}

// A worker the server brokers nothing for was handed no key, and asks without one —
// an empty header would be a key, of a kind.
func TestAWorkerWithoutABrokerKeyAsksWithoutOne(t *testing.T) {
	srv, atlas := newAskingAtlas(t, answerJSON(http.StatusOK, `{"enforcement":"off","permitted":true}`))

	auth, err := askFor(t, Options{Server: srv.URL, ID: "sharepoint-1"}, Job{JobKey: 5, LeaseToken: 1})
	if err != nil {
		t.Fatalf("Authorize: %v", err)
	}
	reqs := atlas.requests()
	if len(reqs) != 1 {
		t.Fatalf("the server was asked %d times, want once", len(reqs))
	}
	if keys := reqs[0].header.Values("X-Atlas-Broker-Key"); len(keys) != 0 {
		t.Errorf("X-Atlas-Broker-Key = %q on a worker that has none", keys)
	}
	if reqs[0].header.Get("Authorization") != "" {
		t.Errorf("Authorization = %q on a worker without a token", reqs[0].header.Get("Authorization"))
	}
	if auth == nil || auth.Governed() {
		t.Errorf("answer = %+v, want the server's ungoverned answer", auth)
	}
}

// A server built before the step exists answers the mux's plain-text 404: nothing
// governs this Worker there, so that is the legacy answer and not a failure.
func TestAServerWithoutTheAuthorizeStepIsTheLegacyAnswer(t *testing.T) {
	srv, atlas := newAskingAtlas(t, http.NotFound)

	auth, err := askFor(t, Options{Server: srv.URL, ID: "sharepoint-1", BrokerKey: "bk-1"}, Job{JobKey: 42, LeaseToken: 7})
	if err != nil || auth != nil {
		t.Fatalf("Authorize = (%+v, %v), want (nil, nil) from a server without the step", auth, err)
	}
	if len(atlas.requests()) != 1 {
		t.Error("the worker did not ask")
	}
}

// The API's own 404 is JSON: it says the job is not there (or not this worker's), which
// is a server that has the step answering — not one without it. Read as the legacy
// answer, it would let a governed action go ahead ungoverned.
func TestAJobTheServerDoesNotKnowIsNotMistakenForAMissingStep(t *testing.T) {
	srv, _ := newAskingAtlas(t, answerJSON(http.StatusNotFound, `{"error":"no job 42"}`))

	auth, err := askFor(t, Options{Server: srv.URL, ID: "sharepoint-1"}, Job{JobKey: 42, LeaseToken: 7})
	if err == nil {
		t.Fatalf("Authorize = %+v, nil; want the server's 404 as an error", auth)
	}
	if auth != nil {
		t.Errorf("an error came with an answer: %+v", auth)
	}
	if !strings.Contains(err.Error(), "no job 42") {
		t.Errorf("error = %v, want the server's reason", err)
	}
	var se *statusError
	if !errors.As(err, &se) || se.status != http.StatusNotFound || se.noRoute {
		t.Errorf("error = %#v, want a 404 from a route that exists", err)
	}
}

// Any other refusal of the question — a plain-text one included, since only a 404 is
// the missing route — is an error the handler stops on, not a reason to act.
func TestAQuestionTheServerDoesNotAnswerIsAnError(t *testing.T) {
	for _, tc := range []struct {
		name   string
		answer http.HandlerFunc
		want   string
	}{
		{"plain-text forbidden", func(w http.ResponseWriter, _ *http.Request) {
			http.Error(w, "forbidden", http.StatusForbidden)
		}, "status 403"},
		{"lease fenced out", answerJSON(http.StatusConflict, `{"error":"the lease is held by another worker"}`), "held by another worker"},
		{"server failure", answerJSON(http.StatusInternalServerError, `{"error":"store closed"}`), "store closed"},
		{"an answer that is not JSON", func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte("permitted"))
		}, "invalid character"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv, _ := newAskingAtlas(t, tc.answer)
			auth, err := askFor(t, Options{Server: srv.URL, ID: "sharepoint-1"}, Job{JobKey: 42, LeaseToken: 7})
			if err == nil || auth != nil {
				t.Fatalf("Authorize = (%+v, %v), want an error and no answer", auth, err)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("error = %v, want it to mention %q", err, tc.want)
			}
		})
	}
}

// A refused action is settled by the server in the same batch as its decision, so the
// worker reports nothing for it: a fail or complete would only be fenced out, or read
// as a second outcome. The handler learns of the refusal by asking, through the worker.
func TestAWorkerReportsNothingForARefusedAction(t *testing.T) {
	srv, atlas := newAskingAtlas(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/activate"):
			_, _ = w.Write([]byte(`{"jobs":[{"jobKey":42,"type":"governed","retries":3,"leaseToken":7}]}`))
		case strings.HasSuffix(r.URL.Path, "/authorize"):
			_, _ = w.Write([]byte(`{"enforcement":"enforce","permitted":false,"enforced":true,"reasonCode":"NO_CONNECTION"}`))
		default:
			_, _ = w.Write([]byte(`{}`))
		}
	})
	acted := false
	w := New(Options{Server: srv.URL, ID: "governed-1", Handlers: map[string]Exec{
		"governed": ExecFunc(func(ctx context.Context, _ Job) (map[string]any, error) {
			auth, err := Authorize(ctx)
			if err != nil {
				return nil, err
			}
			if auth.Governed() && auth.Enforced && !auth.Permitted {
				return nil, Refused()
			}
			acted = true
			return nil, nil
		}),
	}})
	if err := w.RunOnce(context.Background()); err != nil {
		t.Fatalf("RunOnce: %v", err)
	}

	if acted {
		t.Error("the handler acted on a refusal")
	}
	var paths []string
	for _, r := range atlas.requests() {
		paths = append(paths, r.path)
	}
	want := []string{"/api/v1/jobs/activate", "/api/v1/jobs/42/authorize"}
	if strings.Join(paths, " ") != strings.Join(want, " ") {
		t.Errorf("the worker called %v, want %v and no report", paths, want)
	}
	if !errors.Is(Refused(), errPolicyRefused) {
		t.Error("Refused() is not the error the worker reads as settled")
	}
}
