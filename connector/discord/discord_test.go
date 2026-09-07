package discord_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/pblumer/atlas/compiler"
	"github.com/pblumer/atlas/connector/discord"
	"github.com/pblumer/atlas/job"
	"github.com/pblumer/atlas/model"
)

// call is one request the fake Discord recorded.
type call struct {
	method string
	path   string
	query  string
	auth   string
	body   map[string]any
}

// fakeDiscord stands in for Discord's API: it records every request and answers from a
// per-path table, so a test states the API shape it expects rather than a live server's
// behaviour.
type fakeDiscord struct {
	calls   []call
	answers map[string]any // "METHOD /path" → response body (nil → 204 No Content)
	status  map[string]int
	errBody map[string]string
}

func newFakeDiscord(t *testing.T) (*fakeDiscord, *httptest.Server) {
	t.Helper()
	f := &fakeDiscord{answers: map[string]any{}, status: map[string]int{}, errBody: map[string]string{}}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c := call{method: r.Method, path: r.URL.Path, query: r.URL.RawQuery, auth: r.Header.Get("Authorization")}
		if r.Body != nil {
			var body map[string]any
			dec := json.NewDecoder(r.Body)
			dec.UseNumber()
			_ = dec.Decode(&body)
			c.body = body
		}
		f.calls = append(f.calls, c)
		key := r.Method + " " + r.URL.Path
		if code, ok := f.status[key]; ok {
			w.WriteHeader(code)
			body := f.errBody[key]
			if body == "" {
				body = `{"message":"Missing Access","code":50001}`
			}
			_, _ = w.Write([]byte(body))
			return
		}
		ans, ok := f.answers[key]
		if !ok || ans == nil {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(ans)
	}))
	t.Cleanup(srv.Close)
	return f, srv
}

// only returns the single request the fake recorded, failing the test when there was
// not exactly one — the assertion every single-call case wants to make first.
func (f *fakeDiscord) only(t *testing.T) call {
	t.Helper()
	if len(f.calls) != 1 {
		t.Fatalf("requests = %d, want 1: %+v", len(f.calls), f.calls)
	}
	return f.calls[0]
}

func client(base string) discord.Client {
	return discord.NewHTTPClient(discord.Connector{BaseURL: base, BotToken: "tok"})
}

// ---------- the API surface ----------

// Each operation goes to the endpoint and method Discord documents for it. They are
// asserted together because the table is the contract: an operation sent to a plausible
// neighbouring URL is the failure this catches.
func TestOperationsAddressTheirEndpoints(t *testing.T) {
	for _, tc := range []struct {
		op     string
		req    discord.Request
		method string
		path   string
	}{
		{"send-message", discord.Request{Channel: "42", Content: "hallo"}, http.MethodPost, "/channels/42/messages"},
		{"edit-message", discord.Request{Channel: "42", Message: "7", Content: "neu"}, http.MethodPatch, "/channels/42/messages/7"},
		{"delete-message", discord.Request{Channel: "42", Message: "7"}, http.MethodDelete, "/channels/42/messages/7"},
		{"get-message", discord.Request{Channel: "42", Message: "7"}, http.MethodGet, "/channels/42/messages/7"},
		{"list-messages", discord.Request{Channel: "42", MaxResults: 50}, http.MethodGet, "/channels/42/messages"},
		{"create-thread", discord.Request{Channel: "42", Name: "Fall 9"}, http.MethodPost, "/channels/42/threads"},
	} {
		t.Run(tc.op, func(t *testing.T) {
			f, srv := newFakeDiscord(t)
			req := tc.req
			req.Operation = tc.op
			if _, err := client(srv.URL).Do(context.Background(), req); err != nil {
				t.Fatalf("Do: %v", err)
			}
			c := f.only(t)
			if c.method != tc.method || c.path != tc.path {
				t.Errorf("%s %s, want %s %s", c.method, c.path, tc.method, tc.path)
			}
			if c.auth != "Bot tok" {
				t.Errorf("Authorization = %q, want the bot scheme", c.auth)
			}
		})
	}
}

// A thread naming a message hangs under that message; one naming none is standalone and
// carries the type Discord requires there. The two endpoints are the whole difference
// between them, which is why they are one operation.
func TestCreateThreadPicksItsEndpointFromTheMessage(t *testing.T) {
	f, srv := newFakeDiscord(t)
	if _, err := client(srv.URL).Do(context.Background(), discord.Request{
		Operation: "create-thread", Channel: "42", Message: "7", Name: "Fall 9",
	}); err != nil {
		t.Fatalf("Do: %v", err)
	}
	c := f.only(t)
	if c.path != "/channels/42/messages/7/threads" {
		t.Errorf("path = %q, want the thread-from-a-message endpoint", c.path)
	}
	if _, ok := c.body["type"]; ok {
		t.Error("a thread from a message carries a type; the parent decides it")
	}

	f2, srv2 := newFakeDiscord(t)
	if _, err := client(srv2.URL).Do(context.Background(), discord.Request{
		Operation: "create-thread", Channel: "42", Name: "Fall 9",
	}); err != nil {
		t.Fatalf("Do: %v", err)
	}
	c2 := f2.only(t)
	if c2.path != "/channels/42/threads" {
		t.Errorf("path = %q, want the standalone-thread endpoint", c2.path)
	}
	if got, ok := c2.body["type"].(json.Number); !ok || got.String() != "11" {
		t.Errorf("type = %#v, want the public-thread type", c2.body["type"])
	}
	if c2.body["name"] != "Fall 9" {
		t.Errorf("name = %#v, want the authored title", c2.body["name"])
	}
}

// A list pages with the cap the compiler applied and, when the model authored one, an
// exclusive lower bound. An unauthored after is left off entirely: an empty one is not
// "from the beginning" to Discord but a malformed snowflake.
func TestListPagesWithLimitAndAfter(t *testing.T) {
	f, srv := newFakeDiscord(t)
	f.answers["GET /channels/42/messages"] = []any{}
	if _, err := client(srv.URL).Do(context.Background(), discord.Request{
		Operation: "list-messages", Channel: "42", MaxResults: 25, After: "100",
	}); err != nil {
		t.Fatalf("Do: %v", err)
	}
	if got := f.only(t).query; got != "after=100&limit=25" {
		t.Errorf("query = %q, want the cap and the lower bound", got)
	}

	f2, srv2 := newFakeDiscord(t)
	f2.answers["GET /channels/42/messages"] = []any{}
	if _, err := client(srv2.URL).Do(context.Background(), discord.Request{
		Operation: "list-messages", Channel: "42", MaxResults: 25,
	}); err != nil {
		t.Fatalf("Do: %v", err)
	}
	if got := f2.only(t).query; got != "limit=25" {
		t.Errorf("query = %q, want no after at all", got)
	}
}

// The job key rides along as a created message's nonce, so a duplicate produced by an
// at-least-once replay is recognizable as one. An edit carries none: a nonce identifies
// a creation, and an edit already addresses its message by id.
func TestNonceRidesOnACreationOnly(t *testing.T) {
	f, srv := newFakeDiscord(t)
	c := client(srv.URL)
	if _, err := c.Do(context.Background(), discord.Request{
		Operation: "send-message", Channel: "42", Content: "hallo", Nonce: "9",
	}); err != nil {
		t.Fatalf("Do: %v", err)
	}
	if got := f.calls[0].body["nonce"]; got != "9" {
		t.Errorf("nonce = %#v, want the job key", got)
	}
	if _, err := c.Do(context.Background(), discord.Request{
		Operation: "edit-message", Channel: "42", Message: "7", Content: "neu", Nonce: "9",
	}); err != nil {
		t.Fatalf("Do: %v", err)
	}
	if _, ok := f.calls[1].body["nonce"]; ok {
		t.Error("an edit carries a nonce")
	}
}

// Model-authored fields are merged last, so a model can override what the worker
// composed rather than be blocked by it — which is how a task sends an embed instead of
// plain content.
func TestFieldsAreMergedLast(t *testing.T) {
	f, srv := newFakeDiscord(t)
	if _, err := client(srv.URL).Do(context.Background(), discord.Request{
		Operation: "send-message", Channel: "42", Content: "komponiert",
		Fields: map[string]any{
			"content":          "überschrieben",
			"allowed_mentions": map[string]any{"parse": []any{}},
		},
	}); err != nil {
		t.Fatalf("Do: %v", err)
	}
	body := f.only(t).body
	if body["content"] != "überschrieben" {
		t.Errorf("content = %#v, want the model's own value to win", body["content"])
	}
	if _, ok := body["allowed_mentions"].(map[string]any); !ok {
		t.Errorf("allowed_mentions = %#v, want the object shape preserved", body["allowed_mentions"])
	}
}

// A snowflake that arrived with whitespace addresses nothing; trimming it here keeps a
// stray space in a form field from reading as a deleted message. Content is left
// exactly as the model composed it.
func TestIdentifiersAreTrimmedAndContentIsNot(t *testing.T) {
	f, srv := newFakeDiscord(t)
	if _, err := client(srv.URL).Do(context.Background(), discord.Request{
		Operation: "edit-message", Channel: " 42 ", Message: " 7 ", Content: "  Rand  ",
	}); err != nil {
		t.Fatalf("Do: %v", err)
	}
	c := f.only(t)
	if c.path != "/channels/42/messages/7" {
		t.Errorf("path = %q, want the trimmed ids", c.path)
	}
	if c.body["content"] != "  Rand  " {
		t.Errorf("content = %#v, want the authored text untouched", c.body["content"])
	}
}

// A delete answers 204, which decodes to nil — what tells the worker there is nothing
// to write into a result variable.
func TestNoContentDecodesToNil(t *testing.T) {
	_, srv := newFakeDiscord(t)
	got, err := client(srv.URL).Do(context.Background(), discord.Request{
		Operation: "delete-message", Channel: "42", Message: "7",
	})
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	if got != nil {
		t.Errorf("result = %#v, want nil for a 204", got)
	}
}

// Discord's own error envelope is what an operator needs to see; "HTTP 403" is not —
// code 50001 is "Missing Access", which points at the bot's channel permissions rather
// than at its token.
func TestErrorSurfacesDiscordsMessage(t *testing.T) {
	f, srv := newFakeDiscord(t)
	f.status["POST /channels/42/messages"] = http.StatusForbidden
	_, err := client(srv.URL).Do(context.Background(), discord.Request{
		Operation: "send-message", Channel: "42", Content: "hallo",
	})
	if err == nil {
		t.Fatal("Do accepted a rejected send")
	}
	if !strings.Contains(err.Error(), "Missing Access") || !strings.Contains(err.Error(), "50001") {
		t.Errorf("error = %v, want Discord's own message and code in it", err)
	}
}

// A body that is not Discord's envelope — a proxy's error page — is still reported, and
// bounded, because it is itself the answer to "why is this failing".
func TestErrorFallsBackToTheRawBody(t *testing.T) {
	f, srv := newFakeDiscord(t)
	f.status["POST /channels/42/messages"] = http.StatusBadGateway
	f.errBody["POST /channels/42/messages"] = "<html>" + strings.Repeat("x", 500) + "</html>"
	_, err := client(srv.URL).Do(context.Background(), discord.Request{
		Operation: "send-message", Channel: "42", Content: "hallo",
	})
	if err == nil || !strings.Contains(err.Error(), "<html>") {
		t.Fatalf("error = %v, want the proxy's page in it", err)
	}
	if len(err.Error()) > 400 {
		t.Errorf("error is %d bytes; an incident message is not a place for a whole page", len(err.Error()))
	}
}

// An empty error body says so rather than trailing off after the colon.
func TestErrorWithNoBody(t *testing.T) {
	f, srv := newFakeDiscord(t)
	f.status["DELETE /channels/42/messages/7"] = http.StatusNotFound
	f.errBody["DELETE /channels/42/messages/7"] = " "
	_, err := client(srv.URL).Do(context.Background(), discord.Request{
		Operation: "delete-message", Channel: "42", Message: "7",
	})
	if err == nil || !strings.Contains(err.Error(), "no response body") {
		t.Fatalf("error = %v, want it to say the body was empty", err)
	}
}

// A 429 is reported with the wait Discord asked for, and not slept out: the worker may
// run on the run-loop goroutine, where waiting would stall the engine for every other
// request. The job stays pending for the ordinary retry path instead.
func TestRateLimitReportsTheRetryAfter(t *testing.T) {
	f, srv := newFakeDiscord(t)
	f.status["POST /channels/42/messages"] = http.StatusTooManyRequests
	f.errBody["POST /channels/42/messages"] = `{"message":"You are being rate limited.","retry_after":1.25}`
	_, err := client(srv.URL).Do(context.Background(), discord.Request{
		Operation: "send-message", Channel: "42", Content: "hallo",
	})
	if err == nil || !strings.Contains(err.Error(), "rate limited") {
		t.Fatalf("error = %v, want it to name the rate limit", err)
	}
	if !strings.Contains(err.Error(), "1.25") {
		t.Errorf("error = %v, want the retry_after in it", err)
	}
}

// A 429 whose body carries no retry_after still reports the rate limit rather than a
// bare status.
func TestRateLimitWithoutARetryAfter(t *testing.T) {
	f, srv := newFakeDiscord(t)
	f.status["POST /channels/42/messages"] = http.StatusTooManyRequests
	f.errBody["POST /channels/42/messages"] = `not json`
	_, err := client(srv.URL).Do(context.Background(), discord.Request{
		Operation: "send-message", Channel: "42", Content: "hallo",
	})
	if err == nil || !strings.Contains(err.Error(), "rate limited") {
		t.Fatalf("error = %v, want it to name the rate limit", err)
	}
}

// An operation nothing implements is refused rather than sent somewhere plausible.
func TestUnknownOperation(t *testing.T) {
	_, srv := newFakeDiscord(t)
	_, err := client(srv.URL).Do(context.Background(), discord.Request{Operation: "explode", Channel: "42"})
	if err == nil || !strings.Contains(err.Error(), "explode") {
		t.Fatalf("error = %v, want the unknown operation named", err)
	}
}

// The compiler guarantees the attribute is authored; it cannot guarantee what a FEEL
// expression evaluates to. An id that resolved to nothing is caught where the fix — the
// expression — is still in view, rather than spliced into a path and answered with a
// 404 naming a URL the author never wrote.
func TestEmptyIdentifiersAreRefused(t *testing.T) {
	_, srv := newFakeDiscord(t)
	c := client(srv.URL)
	if _, err := c.Do(context.Background(), discord.Request{Operation: "send-message", Channel: "  ", Content: "x"}); err == nil {
		t.Error("a send with no channel was accepted")
	}
	if _, err := c.Do(context.Background(), discord.Request{Operation: "edit-message", Channel: "42", Message: "", Content: "x"}); err == nil {
		t.Error("an edit with no message id was accepted")
	}
}

// A worker built with no token refuses rather than calling Discord unauthenticated and
// being told nothing useful by a 401.
func TestCallNeedsAToken(t *testing.T) {
	_, srv := newFakeDiscord(t)
	c := discord.NewHTTPClient(discord.Connector{BaseURL: srv.URL, BotToken: "  "})
	if _, err := c.Do(context.Background(), discord.Request{Operation: "send-message", Channel: "42", Content: "x"}); err == nil {
		t.Fatal("a client with no token called Discord")
	}
}

// The API base defaults to Discord's own, so a Worker record needs an endpoint only
// behind a proxy.
func TestBaseURLDefaults(t *testing.T) {
	c, err := discord.NewProviderClient(discord.ProviderConfig{Secret: `{"botToken":"tok"}`})
	if err != nil {
		t.Fatalf("NewProviderClient: %v", err)
	}
	hc, ok := c.(*discord.HTTPClient)
	if !ok {
		t.Fatalf("client = %T, want the HTTP client", c)
	}
	if got := hc.BaseURL(); got != discord.DefaultBaseURL {
		t.Errorf("BaseURL = %q, want %q", got, discord.DefaultBaseURL)
	}
}

// A worker whose bundle is missing or unusable is refused at build time, so its tasks
// park with a reason instead of calling Discord unauthenticated.
func TestProviderClientNeedsAUsableBundle(t *testing.T) {
	for _, tc := range []struct{ name, secret string }{
		{"no credential", ""},
		{"malformed bundle", "{"},
		{"no botToken", `{"token":"tok"}`},
		{"blank botToken", `{"botToken":"   "}`},
		{"only the scheme", `{"botToken":"Bot "}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := discord.NewProviderClient(discord.ProviderConfig{Secret: tc.secret}); err == nil {
				t.Fatalf("%s was accepted", tc.name)
			}
		})
	}
	if _, err := discord.NewProviderClient(discord.ProviderConfig{Secret: `{"botToken":"tok"}`}); err != nil {
		t.Fatalf("a usable bundle was rejected: %v", err)
	}
}

// A token pasted with the scheme still on it is the one paste mistake this worker can
// fix rather than report: the client composes "Bot ", so leaving the prefix would send
// it twice and be answered by a 401 that explains nothing.
func TestProviderClientStripsAPastedScheme(t *testing.T) {
	f, srv := newFakeDiscord(t)
	c, err := discord.NewProviderClient(discord.ProviderConfig{Endpoint: srv.URL, Secret: `{"botToken":"Bot tok"}`})
	if err != nil {
		t.Fatalf("NewProviderClient: %v", err)
	}
	if _, err := c.Do(context.Background(), discord.Request{Operation: "send-message", Channel: "42", Content: "x"}); err != nil {
		t.Fatalf("Do: %v", err)
	}
	if got := f.only(t).auth; got != "Bot tok" {
		t.Errorf("Authorization = %q, want the scheme sent exactly once", got)
	}
}

// A real token that happens to begin with the letters "Bot" is not a scheme, and
// mangling it would turn a working credential into a 401 nobody could explain. Only
// "Bot" followed by whitespace is the prefix.
func TestProviderClientKeepsATokenBeginningWithBot(t *testing.T) {
	f, srv := newFakeDiscord(t)
	c, err := discord.NewProviderClient(discord.ProviderConfig{Endpoint: srv.URL, Secret: `{"botToken":"Botanik123"}`})
	if err != nil {
		t.Fatalf("NewProviderClient: %v", err)
	}
	if _, err := c.Do(context.Background(), discord.Request{Operation: "send-message", Channel: "42", Content: "x"}); err != nil {
		t.Fatalf("Do: %v", err)
	}
	if got := f.only(t).auth; got != "Bot Botanik123" {
		t.Errorf("Authorization = %q, want the token kept whole", got)
	}
}

// An endpoint override is used as given, with a trailing slash trimmed so a path is not
// appended to a double one.
func TestEndpointOverrideIsTrimmed(t *testing.T) {
	c, err := discord.NewProviderClient(discord.ProviderConfig{Endpoint: " https://proxy.intern/api/v10/ ", Secret: `{"botToken":"tok"}`})
	if err != nil {
		t.Fatalf("NewProviderClient: %v", err)
	}
	hc, ok := c.(*discord.HTTPClient)
	if !ok {
		t.Fatalf("client = %T, want the HTTP client", c)
	}
	if got := hc.BaseURL(); got != "https://proxy.intern/api/v10" {
		t.Errorf("BaseURL = %q, want it trimmed", got)
	}
}

// ---------- the operation table ----------

// Every operation names itself for an error message, and OpNames is sorted so a message
// listing them reads the same way twice.
func TestOpNamesAreSortedAndComplete(t *testing.T) {
	names := discord.OpNames()
	if len(names) != len(discord.Ops) {
		t.Fatalf("OpNames = %d entries, Ops = %d", len(names), len(discord.Ops))
	}
	for i := 1; i < len(names); i++ {
		if names[i-1] >= names[i] {
			t.Fatalf("OpNames is not sorted: %q before %q", names[i-1], names[i])
		}
	}
	for _, n := range names {
		if discord.Ops[n].Label == "" {
			t.Errorf("Ops[%q] has no label for an error message to use", n)
		}
	}
}

// TestDiscordOpsMatchTheConnector is the drift guard between this package's [Ops] table
// and the compiler's own copy of the operation rules.
//
// The compiler cannot import this package — connector/discord imports compiler, so the
// dependency only runs one way — which is why the rules exist twice. The check is
// therefore behavioural: for every operation, a model supplying exactly what Ops says is
// required must compile, and a model missing any one of those values must not.
func TestDiscordOpsMatchTheConnector(t *testing.T) {
	attrsFor := func(op string, spec discord.Op, omit string) string {
		parts := []string{`connector="team"`, `operation="` + op + `"`}
		add := func(name, attr, value string) {
			if omit != name {
				parts = append(parts, attr+`="`+value+`"`)
			}
		}
		if spec.NeedsChannel {
			add("channel", "channel", "42")
		}
		if spec.NeedsMessage {
			add("message", "messageId", "7")
		}
		if spec.NeedsContent {
			add("content", "content", "hallo")
		}
		if spec.NeedsName {
			add("name", "name", "Fall 9")
		}
		if spec.NeedsResult {
			add("result", "resultVariable", "ergebnis")
		}
		return strings.Join(parts, " ")
	}
	compile := func(attrs string) error {
		bpmn := `<bpmn:definitions xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL"
                  xmlns:atlas="http://atlas.dev/schema/1.0">
  <bpmn:process id="p">
    <bpmn:startEvent id="s"/>
    <bpmn:serviceTask id="t"><bpmn:extensionElements><atlas:discordConnector ` + attrs + `/></bpmn:extensionElements></bpmn:serviceTask>
    <bpmn:endEvent id="e"/>
    <bpmn:sequenceFlow id="f1" sourceRef="s" targetRef="t"/>
    <bpmn:sequenceFlow id="f2" sourceRef="t" targetRef="e"/>
  </bpmn:process>
</bpmn:definitions>`
		_, err := compiler.Parse(1, 1, strings.NewReader(bpmn))
		return err
	}
	for op, spec := range discord.Ops {
		t.Run(op, func(t *testing.T) {
			if err := compile(attrsFor(op, spec, "")); err != nil {
				t.Fatalf("the compiler rejects a model that satisfies Ops[%q]: %v", op, err)
			}
			required := map[string]bool{
				"channel": spec.NeedsChannel, "message": spec.NeedsMessage,
				"content": spec.NeedsContent, "name": spec.NeedsName, "result": spec.NeedsResult,
			}
			for omit, need := range required {
				if !need {
					continue
				}
				if err := compile(attrsFor(op, spec, omit)); err == nil {
					t.Errorf("the compiler accepts %q without its required %s, which Ops says it needs", op, omit)
				}
			}
		})
	}
}

// ---------- the worker ----------

// fakeReader is the slice of the state store the handler reads: one element instance
// and the variables its scope sees.
type fakeReader struct {
	ei   *model.ElementInstanceValue
	vars []model.VariableValue
	err  error // what the store fails with, for the paths that must not swallow it
	// varsErr fails the variable read specifically, which is a different point in the
	// handler from a failed element-instance read and must not be swallowed either.
	varsErr error
}

func (f *fakeReader) GetElementInstance(uint64) (*model.ElementInstanceValue, bool, error) {
	if f.err != nil {
		return nil, false, f.err
	}
	return f.ei, f.ei != nil, nil
}

func (f *fakeReader) VariablesOfScope(scope uint64, fn func(*model.VariableValue) error) error {
	if f.varsErr != nil {
		return f.varsErr
	}
	if f.ei == nil || scope != f.ei.ProcessInstanceKey {
		return nil
	}
	for i := range f.vars {
		if err := fn(&f.vars[i]); err != nil {
			return err
		}
	}
	return nil
}

// recordingClient captures what the worker resolved and answers with a canned result.
type recordingClient struct {
	reqs   []discord.Request
	result any
	err    error
}

func (r *recordingClient) Do(_ context.Context, req discord.Request) (any, error) {
	r.reqs = append(r.reqs, req)
	return r.result, r.err
}

// workerFixture compiles a one-task process and returns the pieces a handler needs.
func workerFixture(t *testing.T, inner string, vars ...model.VariableValue) (*fakeReader, func(uint64) *compiler.CompiledProcess) {
	t.Helper()
	bpmn := `<bpmn:definitions xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL"
                  xmlns:atlas="http://atlas.dev/schema/1.0">
  <bpmn:process id="p">
    <bpmn:startEvent id="s"/>
    <bpmn:serviceTask id="t"><bpmn:extensionElements>` + inner + `</bpmn:extensionElements></bpmn:serviceTask>
    <bpmn:endEvent id="e"/>
    <bpmn:sequenceFlow id="f1" sourceRef="s" targetRef="t"/>
    <bpmn:sequenceFlow id="f2" sourceRef="t" targetRef="e"/>
  </bpmn:process>
</bpmn:definitions>`
	cp, err := compiler.Parse(7, 1, strings.NewReader(bpmn))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	task := cp.Flow(cp.Outgoing(cp.StartEvents()[0])[0]).Target
	rd := &fakeReader{
		// FlowScopeKey is the process-instance scope: the chain the handler walks to
		// read the variables the task sees (ADR-0068) ends there.
		ei:   &model.ElementInstanceValue{ProcessInstanceKey: 500, ProcessDefKey: 7, ElementId: task, FlowScopeKey: 500},
		vars: vars,
	}
	return rd, func(uint64) *compiler.CompiledProcess { return cp }
}

// The worker evaluates the task's FEEL values over the variables it sees, sends what
// they resolved to, and writes what Discord returned into the result variable.
func TestHandlerResolvesAndWritesBack(t *testing.T) {
	rd, lookup := workerFixture(t,
		`<atlas:discordConnector connector="team" operation="send-message" channel="=kanal"
		    content="=text" resultVariable="nachricht"><atlas:discordField name="embeds" value="=anhang"/></atlas:discordConnector>`,
		model.VariableValue{Name: "kanal", Kind: model.VarString, Text: "42"},
		model.VariableValue{Name: "text", Kind: model.VarString, Text: "Antrag genehmigt"},
		model.VariableValue{Name: "anhang", Kind: model.VarJSON, Text: `[{"title":"Antrag"}]`},
	)
	c := &recordingClient{result: map[string]any{"id": "999"}}
	reg := discord.NewRegistry()
	reg.Register("team", c)

	out, err := discord.Handler(rd, lookup, reg)(job.Job{Key: 9, ElementInstanceKey: 42})
	if err != nil {
		t.Fatalf("handler: %v", err)
	}
	if len(c.reqs) != 1 {
		t.Fatalf("requests = %d, want 1", len(c.reqs))
	}
	req := c.reqs[0]
	if req.Operation != "send-message" || req.Channel != "42" || req.Content != "Antrag genehmigt" {
		t.Errorf("request = %+v, want the resolved channel and content", req)
	}
	embeds, _ := req.Fields["embeds"].([]any)
	if len(embeds) != 1 {
		t.Errorf("embeds = %#v, want the FEEL list sent as a JSON list", req.Fields["embeds"])
	}
	if req.Nonce != "9" {
		t.Errorf("nonce = %q, want the job key", req.Nonce)
	}
	if len(out) != 1 || out[0].Name != "nachricht" || out[0].Kind != model.VarJSON {
		t.Fatalf("outputs = %+v, want the created message in \"nachricht\"", out)
	}
}

// A body property's JSON shape follows its FEEL value's kind, not the look of its text:
// a boolean stays a boolean (tts), a number stays a number, an object stays an object,
// and content that happens to begin with "{" stays a string.
func TestFieldsKeepTheirFeelShape(t *testing.T) {
	rd, lookup := workerFixture(t,
		`<atlas:discordConnector connector="team" operation="send-message" channel="42" content="x">
			<atlas:discordField name="tts" value="=laut"/>
			<atlas:discordField name="flags" value="=marken"/>
			<atlas:discordField name="allowed_mentions" value="=erwaehnungen"/>
			<atlas:discordField name="fehlt" value="=gibtEsNicht"/>
			<atlas:discordField name="literal" value="{kein json}"/>
			<atlas:discordField name="spur" value="=processInstanceKey"/>
		 </atlas:discordConnector>`,
		model.VariableValue{Name: "laut", Kind: model.VarBool, Bool: true},
		model.VariableValue{Name: "marken", Kind: model.VarNumber, Text: "4"},
		model.VariableValue{Name: "erwaehnungen", Kind: model.VarJSON, Text: `{"parse":[]}`},
	)
	c := &recordingClient{}
	reg := discord.NewRegistry()
	reg.Register("team", c)
	if _, err := discord.Handler(rd, lookup, reg)(job.Job{Key: 4, ElementInstanceKey: 42}); err != nil {
		t.Fatalf("handler: %v", err)
	}
	fields := c.reqs[0].Fields
	if fields["tts"] != true {
		t.Errorf("tts = %#v, want the boolean true", fields["tts"])
	}
	if got, ok := fields["flags"].(json.Number); !ok || got.String() != "4" {
		t.Errorf("flags = %#v, want the JSON number 4", fields["flags"])
	}
	if _, ok := fields["allowed_mentions"].(map[string]any); !ok {
		t.Errorf("allowed_mentions = %#v, want an object", fields["allowed_mentions"])
	}
	if fields["fehlt"] != nil {
		t.Errorf("fehlt = %#v, want null for a variable that is not set", fields["fehlt"])
	}
	if fields["literal"] != "{kein json}" {
		t.Errorf("literal = %#v, want the literal kept a string", fields["literal"])
	}
	if fields["spur"] != "500" {
		t.Errorf("spur = %#v, want the process instance key bound", fields["spur"])
	}
}

// An operation Discord answers with nothing writes no variable, so a delete stays
// distinguishable from a read that found nothing.
func TestHandlerWritesNothingWithoutAResult(t *testing.T) {
	rd, lookup := workerFixture(t,
		`<atlas:discordConnector connector="team" operation="delete-message" channel="42" messageId="7"/>`)
	reg := discord.NewRegistry()
	reg.Register("team", &recordingClient{})
	out, err := discord.Handler(rd, lookup, reg)(job.Job{Key: 1, ElementInstanceKey: 42})
	if err != nil {
		t.Fatalf("handler: %v", err)
	}
	if len(out) != 0 {
		t.Errorf("outputs = %+v, want none", out)
	}
}

// A worker name the registry does not hold fails the job with the registry's own
// reason, so a parked token can say whether it was never configured or is broken.
func TestHandlerUnresolvedWorker(t *testing.T) {
	rd, lookup := workerFixture(t,
		`<atlas:discordConnector connector="team" operation="get-message" channel="42" messageId="7" resultVariable="n"/>`)
	_, err := discord.Handler(rd, lookup, discord.NewRegistry())(job.Job{Key: 1, ElementInstanceKey: 42})
	if err == nil || !strings.Contains(err.Error(), "team") {
		t.Fatalf("error = %v, want it to name the unresolved worker", err)
	}
}

// A vanished element instance is not an error: the job outlived what it belonged to.
func TestHandlerMissingElementInstance(t *testing.T) {
	rd, lookup := workerFixture(t,
		`<atlas:discordConnector connector="team" operation="get-message" channel="42" messageId="7" resultVariable="n"/>`)
	rd.ei = nil
	out, err := discord.Handler(rd, lookup, discord.NewRegistry())(job.Job{Key: 1, ElementInstanceKey: 42})
	if err != nil || out != nil {
		t.Fatalf("handler = %+v, %v; want nothing to do", out, err)
	}
}

// A compiled process the lookup cannot resolve fails the job rather than completing a
// token on a call that was never made.
func TestHandlerMissingCompiledProcess(t *testing.T) {
	rd, _ := workerFixture(t,
		`<atlas:discordConnector connector="team" operation="get-message" channel="42" messageId="7" resultVariable="n"/>`)
	_, err := discord.Handler(rd, func(uint64) *compiler.CompiledProcess { return nil }, discord.NewRegistry())(job.Job{Key: 1, ElementInstanceKey: 42})
	if err == nil {
		t.Fatal("handler accepted a job with no compiled process")
	}
}

// A failing client fails the job, which is what leaves it pending for the retry path
// instead of moving the token on.
func TestHandlerPropagatesTheClientError(t *testing.T) {
	rd, lookup := workerFixture(t,
		`<atlas:discordConnector connector="team" operation="send-message" channel="42" content="x"/>`)
	reg := discord.NewRegistry()
	reg.Register("team", &recordingClient{err: fmt.Errorf("kaputt")})
	if _, err := discord.Handler(rd, lookup, reg)(job.Job{Key: 1, ElementInstanceKey: 42}); err == nil {
		t.Fatal("handler swallowed the client's error")
	}
}

// What Discord returned keeps its own kind on the way into the result variable: a
// scalar stays a scalar, so a model comparing the answer to a number does not have to
// unwrap a JSON document first.
func TestResultVariableKeepsItsKind(t *testing.T) {
	for _, tc := range []struct {
		name   string
		result any
		want   model.VarKind
	}{
		{"string", "fertig", model.VarString},
		{"number", float64(3), model.VarNumber},
		{"bool", true, model.VarBool},
		{"object", map[string]any{"id": "9"}, model.VarJSON},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rd, lookup := workerFixture(t,
				`<atlas:discordConnector connector="team" operation="get-message" channel="42" messageId="7" resultVariable="n"/>`)
			reg := discord.NewRegistry()
			reg.Register("team", &recordingClient{result: tc.result})
			out, err := discord.Handler(rd, lookup, reg)(job.Job{Key: 1, ElementInstanceKey: 42})
			if err != nil {
				t.Fatalf("handler: %v", err)
			}
			if len(out) != 1 || out[0].Kind != tc.want {
				t.Fatalf("outputs = %+v, want one %v", out, tc.want)
			}
		})
	}
}

// An authored value resolves from a variable of any kind, coerced to the string an id
// or a message body has to be. A channel id held as a number is the common case: it came
// out of a JSON payload, and Discord takes it in the path either way. A JSON variable
// resolves to its serialized form, which is what makes a structured value usable in a
// message body without a field.
//
// A boolean is the one kind that resolves to nothing, because expr.Classify carries a
// boolean in its own return rather than in the text. That is the shared contract of
// every literal-or-FEEL worker value in this repository — the REST, SharePoint and Jira
// workers resolve one the same way — so it is pinned here rather than worked around:
// a model wanting the word needs "=if genehmigt then \"ja\" else \"nein\"".
func TestValuesResolveFromEveryVariableKind(t *testing.T) {
	rd, lookup := workerFixture(t,
		`<atlas:discordConnector connector="team" operation="send-message" channel="=kanal" content="=text">
			<atlas:discordField name="daten" value="=struktur"/>
			<atlas:discordField name="flagge" value="=jaNein"/>
		 </atlas:discordConnector>`,
		model.VariableValue{Name: "kanal", Kind: model.VarNumber, Text: "42"},
		model.VariableValue{Name: "text", Kind: model.VarJSON, Text: `{"a":1}`},
		model.VariableValue{Name: "struktur", Kind: model.VarString, Text: "roh"},
		model.VariableValue{Name: "jaNein", Kind: model.VarBool, Bool: true},
	)
	c := &recordingClient{}
	reg := discord.NewRegistry()
	reg.Register("team", c)
	if _, err := discord.Handler(rd, lookup, reg)(job.Job{Key: 1, ElementInstanceKey: 42}); err != nil {
		t.Fatalf("handler: %v", err)
	}
	req := c.reqs[0]
	if req.Channel != "42" {
		t.Errorf("channel = %q, want the number coerced to its string form", req.Channel)
	}
	if req.Content != `{"a":1}` {
		t.Errorf("content = %q, want the JSON value serialized", req.Content)
	}
	if req.Fields["daten"] != "roh" {
		t.Errorf("daten = %#v, want the string kept", req.Fields["daten"])
	}
	if req.Fields["flagge"] != true {
		t.Errorf("flagge = %#v, want a field to keep the boolean a value slot loses", req.Fields["flagge"])
	}
}

// The boolean rule above, stated where a reader looking for it will find it: a boolean
// in a string-valued slot resolves to the empty string, so a message body authored
// straight from a flag is empty rather than "true".
func TestABooleanInAValueSlotResolvesToNothing(t *testing.T) {
	rd, lookup := workerFixture(t,
		`<atlas:discordConnector connector="team" operation="send-message" channel="42" content="=genehmigt"/>`,
		model.VariableValue{Name: "genehmigt", Kind: model.VarBool, Bool: true},
	)
	c := &recordingClient{}
	reg := discord.NewRegistry()
	reg.Register("team", c)
	if _, err := discord.Handler(rd, lookup, reg)(job.Job{Key: 1, ElementInstanceKey: 42}); err != nil {
		t.Fatalf("handler: %v", err)
	}
	if c.reqs[0].Content != "" {
		t.Errorf("content = %q, want the empty string a boolean resolves to", c.reqs[0].Content)
	}
}

// A list with neither a cap nor a lower bound sends no query string at all, rather than
// a bare "?" — the shape a caller building a registry by hand can still produce.
func TestListWithoutLimitOrAfter(t *testing.T) {
	f, srv := newFakeDiscord(t)
	f.answers["GET /channels/42/messages"] = []any{}
	if _, err := client(srv.URL).Do(context.Background(), discord.Request{
		Operation: "list-messages", Channel: "42",
	}); err != nil {
		t.Fatalf("Do: %v", err)
	}
	if got := f.only(t).query; got != "" {
		t.Errorf("query = %q, want none", got)
	}
}

// A store that fails fails the job: the handler must not read a failure as "the element
// instance is gone" and complete a token on a call it never made.
func TestHandlerPropagatesTheStoreError(t *testing.T) {
	rd, lookup := workerFixture(t,
		`<atlas:discordConnector connector="team" operation="get-message" channel="42" messageId="7" resultVariable="n"/>`)
	rd.err = fmt.Errorf("store kaputt")
	if _, err := discord.Handler(rd, lookup, discord.NewRegistry())(job.Job{Key: 1, ElementInstanceKey: 42}); err == nil {
		t.Fatal("handler swallowed the store's error")
	}
}

// Resolve is reached by the offload path too, where a detail the caller failed to find
// arrives as nil. It says so rather than resolving a job of empty values.
func TestResolveWithoutADetail(t *testing.T) {
	rd, lookup := workerFixture(t,
		`<atlas:discordConnector connector="team" operation="send-message" channel="42" content="x"/>`)
	if _, err := discord.Resolve(rd, lookup(7), nil, rd.ei, 42, 1); err == nil {
		t.Fatal("Resolve accepted a task with no detail")
	}
}

// A JSON body that is not Discord's envelope falls back to the raw text: a bare object
// with no message is as unhelpful as no body, and hiding it would hide the one clue.
func TestErrorWithJSONButNoMessage(t *testing.T) {
	f, srv := newFakeDiscord(t)
	f.status["GET /channels/42/messages/7"] = http.StatusInternalServerError
	f.errBody["GET /channels/42/messages/7"] = `{"unerwartet":true}`
	_, err := client(srv.URL).Do(context.Background(), discord.Request{
		Operation: "get-message", Channel: "42", Message: "7",
	})
	if err == nil || !strings.Contains(err.Error(), "unerwartet") {
		t.Fatalf("error = %v, want the raw body in it", err)
	}
}

// Discord's envelope without a numeric code still reports its message, which is the
// half an operator reads.
func TestErrorWithMessageButNoCode(t *testing.T) {
	f, srv := newFakeDiscord(t)
	f.status["GET /channels/42/messages/7"] = http.StatusUnauthorized
	f.errBody["GET /channels/42/messages/7"] = `{"message":"401: Unauthorized"}`
	_, err := client(srv.URL).Do(context.Background(), discord.Request{
		Operation: "get-message", Channel: "42", Message: "7",
	})
	if err == nil || !strings.Contains(err.Error(), "401: Unauthorized") {
		t.Fatalf("error = %v, want Discord's message in it", err)
	}
}

// A variable read that fails fails the job. It is a different point in the handler from
// a failed element-instance read — the task was found, its scope was not — and completing
// a token on values that could not be read would send an empty message.
func TestHandlerPropagatesTheVariableReadError(t *testing.T) {
	rd, lookup := workerFixture(t,
		`<atlas:discordConnector connector="team" operation="send-message" channel="42" content="=text"/>`)
	rd.varsErr = fmt.Errorf("scope kaputt")
	reg := discord.NewRegistry()
	reg.Register("team", &recordingClient{})
	if _, err := discord.Handler(rd, lookup, reg)(job.Job{Key: 1, ElementInstanceKey: 42}); err == nil {
		t.Fatal("handler swallowed the variable read error")
	}
}

// An element that is not a worker task fails the job rather than resolving an empty
// one — the shape a job outliving a redeploy onto a changed model would have.
func TestHandlerElementIsNotAWorkerTask(t *testing.T) {
	rd, lookup := workerFixture(t,
		`<atlas:discordConnector connector="team" operation="send-message" channel="42" content="x"/>`)
	rd.ei.ElementId = lookup(7).StartEvents()[0]
	if _, err := discord.Handler(rd, lookup, discord.NewRegistry())(job.Job{Key: 1, ElementInstanceKey: 42}); err == nil {
		t.Fatal("handler resolved a start event as a Discord task")
	}
}

// A FEEL value with no variable inputs still resolves — the "=" form is a value, not a
// requirement that something be read.
func TestConstantFeelValueResolves(t *testing.T) {
	rd, lookup := workerFixture(t,
		`<atlas:discordConnector connector="team" operation="send-message" channel="42" content="=&quot;fest&quot;"/>`)
	c := &recordingClient{}
	reg := discord.NewRegistry()
	reg.Register("team", c)
	if _, err := discord.Handler(rd, lookup, reg)(job.Job{Key: 1, ElementInstanceKey: 42}); err != nil {
		t.Fatalf("handler: %v", err)
	}
	if c.reqs[0].Content != "fest" {
		t.Errorf("content = %q, want the constant expression's value", c.reqs[0].Content)
	}
}

// A variable stored as null binds as null: a field reading one is sent as JSON null,
// which is how a model clears a property rather than omitting it.
func TestNullVariableBindsAsNull(t *testing.T) {
	rd, lookup := workerFixture(t,
		`<atlas:discordConnector connector="team" operation="send-message" channel="42" content="x">
			<atlas:discordField name="leer" value="=nichts"/>
		 </atlas:discordConnector>`,
		model.VariableValue{Name: "nichts", Kind: model.VarNull},
	)
	c := &recordingClient{}
	reg := discord.NewRegistry()
	reg.Register("team", c)
	if _, err := discord.Handler(rd, lookup, reg)(job.Job{Key: 1, ElementInstanceKey: 42}); err != nil {
		t.Fatalf("handler: %v", err)
	}
	if c.reqs[0].Fields["leer"] != nil {
		t.Errorf("leer = %#v, want JSON null", c.reqs[0].Fields["leer"])
	}
}

// A transport failure is returned, which leaves the job pending for the retry path
// rather than completing a token on a call that never reached Discord.
func TestTransportFailureIsReturned(t *testing.T) {
	_, srv := newFakeDiscord(t)
	base := srv.URL
	srv.Close() // nothing is listening now
	_, err := client(base).Do(context.Background(), discord.Request{
		Operation: "send-message", Channel: "42", Content: "x",
	})
	if err == nil {
		t.Fatal("Do reported success with nothing listening")
	}
	if !strings.Contains(err.Error(), "send-message") {
		t.Errorf("error = %v, want the operation named in it", err)
	}
}
