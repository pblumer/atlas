package mcptool

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

// recorded is one request a fake server saw.
type recorded struct {
	Method  string
	Headers http.Header
	Params  map[string]any
	HTTP    string
}

type fakeServer struct {
	mu   sync.Mutex
	seen []recorded
	srv  *httptest.Server
}

func (f *fakeServer) calls(method string) []recorded {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []recorded
	for _, r := range f.seen {
		if r.Method == method {
			out = append(out, r)
		}
	}
	return out
}

func rpcResult(w http.ResponseWriter, id any, result string) {
	w.Header().Set("Content-Type", "application/json")
	_, _ = io.WriteString(w, `{"jsonrpc":"2.0","id":`+jsonOf(id)+`,"result":`+result+`}`)
}

func rpcFail(w http.ResponseWriter, status int, id any, code int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = io.WriteString(w, `{"jsonrpc":"2.0","id":`+jsonOf(id)+`,"error":{"code":`+jsonOf(code)+`,"message":`+jsonOf(msg)+`}}`)
}

func jsonOf(v any) string {
	b, _ := json.Marshal(v)
	return string(b)
}

// newFake starts a server whose handle answers one decoded JSON-RPC request.
func newFake(t *testing.T, handle func(f *fakeServer, w http.ResponseWriter, r *http.Request, req rpcIn)) *fakeServer {
	t.Helper()
	f := &fakeServer{}
	f.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req rpcIn
		if r.Method == http.MethodPost {
			raw, _ := io.ReadAll(r.Body)
			_ = json.Unmarshal(raw, &req)
		}
		f.mu.Lock()
		name := req.Method
		if name == "" && r.Method == http.MethodPost {
			name = "(response)"
		}
		f.seen = append(f.seen, recorded{Method: name, Headers: r.Header.Clone(), Params: req.Params, HTTP: r.Method})
		f.mu.Unlock()
		handle(f, w, r, req)
	}))
	t.Cleanup(f.srv.Close)
	return f
}

type rpcIn struct {
	ID     any            `json:"id"`
	Method string         `json:"method"`
	Params map[string]any `json:"params"`
	Result map[string]any `json:"result"`
}

const weatherTools = `{"resultType":"complete","ttlMs":60000,"tools":[
  {"name":"get_weather","description":"ignored","inputSchema":{"type":"object","properties":{
     "location":{"type":"string"},
     "region":{"type":"string","x-mcp-header":"Region"},
     "opts":{"type":"object","properties":{"days":{"type":"integer","x-mcp-header":"Days"},"metric":{"type":"boolean","x-mcp-header":"Metric"}}}}}},
  {"name":"broken","inputSchema":{"type":"object","properties":{"blob":{"type":"object","x-mcp-header":"Blob"}}}}]}`

// modernServer is a 2026-07-28 server: no handshake, everything in _meta and headers.
func modernServer(t *testing.T, call func(f *fakeServer, w http.ResponseWriter, req rpcIn)) *fakeServer {
	return newFake(t, func(f *fakeServer, w http.ResponseWriter, r *http.Request, req rpcIn) {
		if r.Header.Get("MCP-Protocol-Version") != ModernVersion || r.Header.Get("Mcp-Method") != req.Method {
			rpcFail(w, http.StatusBadRequest, req.ID, codeHeaderMismatch, "headers")
			return
		}
		switch req.Method {
		case "tools/list":
			rpcResult(w, req.ID, weatherTools)
		case "tools/call":
			call(f, w, req)
		default:
			rpcFail(w, http.StatusNotFound, req.ID, codeMethodNotFound, "unknown method")
		}
	})
}

// A modern server is called without a handshake: the version, the client and its (empty)
// capabilities ride in _meta, the method and the tool in headers, and an argument the
// tool wants mirrored is sent as a header too — a string as it is, an integer in
// decimal, a boolean in lower case, and a value that is not plain ASCII in Base64.
func TestAModernServerIsCalledWithoutAHandshake(t *testing.T) {
	f := modernServer(t, func(_ *fakeServer, w http.ResponseWriter, req rpcIn) {
		rpcResult(w, req.ID, `{"resultType":"complete","content":[{"type":"text","text":"sonnig"},{"type":"image","data":"x"},{"type":"text","text":"12 Grad"}],
		  "structuredContent":{"temp":12}}`)
	})
	c := &Client{Endpoint: f.srv.URL, ClientVersion: "9.9", Authorize: func(h http.Header) error {
		h.Set("Authorization", "Bearer tok")
		return nil
	}}
	res, err := c.CallTool(context.Background(), "get_weather", map[string]any{
		"location": "Bern", "region": "Zürich", "opts": map[string]any{"days": json.Number("3"), "metric": true},
	})
	if err != nil {
		t.Fatalf("CallTool: %v", err)
	}
	if res.Text != "sonnig\n12 Grad" || string(res.Structured) != `{"temp":12}` || res.IsError || len(res.Other) != 1 || res.Other[0] != "image" {
		t.Errorf("result = %+v", res)
	}
	if len(f.calls("initialize")) != 0 {
		t.Error("a modern server was sent an initialize")
	}
	calls := f.calls("tools/call")
	if len(calls) != 1 {
		t.Fatalf("%d tools/call requests, want 1", len(calls))
	}
	h := calls[0].Headers
	if h.Get("Mcp-Name") != "get_weather" || h.Get("Authorization") != "Bearer tok" ||
		!strings.Contains(h.Get("Accept"), "application/json") || !strings.Contains(h.Get("Accept"), "text/event-stream") {
		t.Errorf("headers = %v", h)
	}
	if h.Get("Mcp-Param-Days") != "3" || h.Get("Mcp-Param-Metric") != "true" || h.Get("Mcp-Param-Region") != "=?base64?WsO8cmljaA==?=" {
		t.Errorf("mirrored headers = Days %q, Metric %q, Region %q", h.Get("Mcp-Param-Days"), h.Get("Mcp-Param-Metric"), h.Get("Mcp-Param-Region"))
	}
	meta, _ := calls[0].Params["_meta"].(map[string]any)
	info, _ := meta[metaClientInfo].(map[string]any)
	if meta[metaProtocolVersion] != ModernVersion || info["name"] != "atlas" || info["version"] != "9.9" || meta[metaCapabilities] == nil {
		t.Errorf("_meta = %v", meta)
	}
	// The argument stays in the body as well: the server checks the two against each other.
	if args, _ := calls[0].Params["arguments"].(map[string]any); args["region"] != "Zürich" {
		t.Errorf("arguments = %v", calls[0].Params["arguments"])
	}

	// The list is read once and then trusted for as long as the server said.
	if _, err := c.CallTool(context.Background(), "get_weather", map[string]any{"location": "Chur"}); err != nil {
		t.Fatalf("second CallTool: %v", err)
	}
	if n := len(f.calls("tools/list")); n != 1 {
		t.Errorf("%d tools/list requests, want the one", n)
	}
	// An argument that is absent has no header.
	if second := f.calls("tools/call")[1].Headers; second.Get("Mcp-Param-Region") != "" {
		t.Errorf("an absent argument was mirrored: %v", second)
	}
	// And once that time is up it is read again.
	c.now = func() time.Time { return time.Now().Add(2 * time.Minute) }
	if _, err := c.CallTool(context.Background(), "get_weather", nil); err != nil {
		t.Fatalf("third CallTool: %v", err)
	}
	if n := len(f.calls("tools/list")); n != 2 {
		t.Errorf("%d tools/list requests after the list ran out, want 2", n)
	}
}

// A tool that is not there, and one whose annotation cannot be honoured, are not called.
func TestAToolThatCannotBeCalledIsNot(t *testing.T) {
	f := modernServer(t, func(_ *fakeServer, w http.ResponseWriter, req rpcIn) { rpcResult(w, req.ID, `{"content":[]}`) })
	c := &Client{Endpoint: f.srv.URL}
	if _, err := c.CallTool(context.Background(), "nope", nil); err == nil || !strings.Contains(err.Error(), `no tool named "nope"`) {
		t.Errorf("an unlisted tool: %v", err)
	}
	if _, err := c.CallTool(context.Background(), "broken", nil); err == nil || !strings.Contains(err.Error(), "not a string, an integer or a boolean") {
		t.Errorf("a tool with an annotation on an object: %v", err)
	}
	if n := len(f.calls("tools/call")); n != 0 {
		t.Errorf("%d calls were made", n)
	}
}

// A server that says the headers do not match has changed what it wants mirrored: the
// list is read again and the call retried, once.
func TestAHeaderMismatchRereadsTheListAndRetriesOnce(t *testing.T) {
	attempts := 0
	f := modernServer(t, func(_ *fakeServer, w http.ResponseWriter, req rpcIn) {
		attempts++
		if attempts == 1 {
			rpcFail(w, http.StatusBadRequest, req.ID, codeHeaderMismatch, "HeaderMismatch")
			return
		}
		rpcResult(w, req.ID, `{"content":[{"type":"text","text":"ok"}]}`)
	})
	c := &Client{Endpoint: f.srv.URL}
	res, err := c.CallTool(context.Background(), "get_weather", nil)
	if err != nil || res.Text != "ok" {
		t.Fatalf("CallTool = %+v, %v", res, err)
	}
	if attempts != 2 || len(f.calls("tools/list")) != 2 {
		t.Errorf("%d attempts and %d list reads, want 2 and 2", attempts, len(f.calls("tools/list")))
	}

	// A server that keeps saying so is not retried for ever.
	always := modernServer(t, func(_ *fakeServer, w http.ResponseWriter, req rpcIn) {
		rpcFail(w, http.StatusBadRequest, req.ID, codeHeaderMismatch, "HeaderMismatch")
	})
	var perr *ProtocolError
	if _, err := (&Client{Endpoint: always.srv.URL}).CallTool(context.Background(), "get_weather", nil); !errors.As(err, &perr) || perr.Code != codeHeaderMismatch {
		t.Errorf("a persistent mismatch: %v", err)
	}
	if n := len(always.calls("tools/call")); n != 2 {
		t.Errorf("%d calls, want the call and one retry", n)
	}
}

// legacyServer is a 2025-11-25 server: a session opened by initialize, and a tools/call
// answered on an event stream after a notification and a ping of its own.
func legacyServer(t *testing.T, version string) *fakeServer {
	return newFake(t, func(f *fakeServer, w http.ResponseWriter, r *http.Request, req rpcIn) {
		if r.Method == http.MethodDelete {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		sid := r.Header.Get("Mcp-Session-Id")
		switch {
		case req.Method == "initialize":
			w.Header().Set("Mcp-Session-Id", "sess-1")
			rpcResult(w, req.ID, `{"protocolVersion":`+jsonOf(version)+`,"capabilities":{"tools":{}},"serverInfo":{"name":"s","version":"1"}}`)
		case sid != "sess-1":
			// What an older server says to a request it has no session for — a JSON-RPC
			// error, but none of the modern ones.
			rpcFail(w, http.StatusBadRequest, nil, -32000, "Bad Request: Server not initialized")
		case req.Method == "notifications/initialized", req.Method == "":
			w.WriteHeader(http.StatusAccepted)
		case req.Method == "tools/list":
			if req.Params["cursor"] == nil {
				rpcResult(w, req.ID, `{"tools":[{"name":"a","inputSchema":{"type":"object"}}],"nextCursor":"p2"}`)
			} else {
				rpcResult(w, req.ID, `{"tools":[{"name":"b","inputSchema":{"type":"object"}}]}`)
			}
		case req.Method == "tools/call":
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = io.WriteString(w, ": priming\nid: 1\ndata:\n\n"+
				"data: {\"jsonrpc\":\"2.0\",\"method\":\"notifications/progress\",\"params\":{}}\n\n"+
				"data: {\"jsonrpc\":\"2.0\",\"id\":\"srv-1\",\"method\":\"ping\"}\n\n"+
				"data: {\"jsonrpc\":\"2.0\",\"id\":\"srv-2\",\"method\":\"sampling/createMessage\",\"params\":{}}\n\n"+
				"event: message\ndata: {\"jsonrpc\":\"2.0\",\"id\":"+jsonOf(req.ID)+",\n"+
				"data: \"result\":{\"content\":[{\"type\":\"text\",\"text\":\"alt\"}],\"isError\":true}}\n\n")
		}
	})
}

// A server of the older kind is recognised by how it refuses a modern request, and is
// then spoken to its own way: a session is opened, its id and the agreed version are
// sent with everything after, and the session is ended when the call is done. On the
// stream a notification is passed over, a ping is answered, and anything else the
// server asks is told this client does not do that.
func TestAnOlderServerIsFallenBackTo(t *testing.T) {
	f := legacyServer(t, "2025-06-18")
	c := &Client{Endpoint: f.srv.URL}
	res, err := c.CallTool(context.Background(), "a", map[string]any{"q": 1})
	if err != nil {
		t.Fatalf("CallTool: %v", err)
	}
	// A tool that ran and failed is a result, not an error.
	if !res.IsError || res.Text != "alt" {
		t.Errorf("result = %+v, want the tool's own failure", res)
	}
	call := f.calls("tools/call")
	if len(call) != 1 || call[0].Headers.Get("Mcp-Session-Id") != "sess-1" || call[0].Headers.Get("MCP-Protocol-Version") != "2025-06-18" {
		t.Fatalf("tools/call = %+v", call)
	}
	if _, has := call[0].Params["_meta"]; has || call[0].Headers.Get("Mcp-Method") != "" {
		t.Error("an older server was sent the modern request shape")
	}
	if len(f.calls("notifications/initialized")) == 0 {
		t.Error("no notifications/initialized after initialize")
	}
	var replies, deletes int
	f.mu.Lock()
	for _, r := range f.seen {
		if r.Method == "(response)" {
			replies++
		}
		if r.HTTP == http.MethodDelete {
			deletes++
		}
	}
	f.mu.Unlock()
	if replies != 2 || deletes == 0 {
		t.Errorf("%d replies to the server's own requests and %d session ends, want 2 and at least 1", replies, deletes)
	}

	// Which kind the server is was learned once: the next call asks no modern question.
	before := len(f.calls("tools/list"))
	if _, err := c.CallTool(context.Background(), "a", nil); err != nil {
		t.Fatalf("second CallTool: %v", err)
	}
	if after := len(f.calls("tools/list")); after != before {
		t.Errorf("a known older server was probed again (%d list requests, then %d)", before, after)
	}
	// Its list is paged through like any other.
	tools, err := c.ListTools(context.Background())
	if err != nil || len(tools) != 2 || tools[1].Name != "b" {
		t.Errorf("ListTools = %+v, %v", tools, err)
	}
}

func TestAnOlderServerOfAnUnknownVersionIsRefused(t *testing.T) {
	f := legacyServer(t, "2024-11-05")
	if _, err := (&Client{Endpoint: f.srv.URL}).CallTool(context.Background(), "a", nil); err == nil || !strings.Contains(err.Error(), `"2024-11-05"`) {
		t.Errorf("an unknown version: %v", err)
	}
}

// What is not a tool's own failure is an error that says what the server said.
func TestProtocolFailuresAreErrors(t *testing.T) {
	for name, tc := range map[string]struct {
		answer func(w http.ResponseWriter, req rpcIn)
		want   string
	}{
		"a JSON-RPC error": {func(w http.ResponseWriter, req rpcIn) { rpcFail(w, 400, req.ID, -32602, "Unknown argument") }, "error -32602: Unknown argument"},
		"a gateway's page": {func(w http.ResponseWriter, _ rpcIn) {
			w.WriteHeader(502)
			_, _ = io.WriteString(w, "<html>Bad Gateway</html>")
		}, "502: <html>Bad Gateway</html>"},
		"an empty answer": {func(w http.ResponseWriter, _ rpcIn) {}, "no answer to the request"},
		"input required": {func(w http.ResponseWriter, req rpcIn) {
			rpcResult(w, req.ID, `{"resultType":"input_required","requestState":"x"}`)
		}, "asks for input"},
		"an unknown result type":  {func(w http.ResponseWriter, req rpcIn) { rpcResult(w, req.ID, `{"resultType":"later"}`) }, `result type "later"`},
		"a result that is no map": {func(w http.ResponseWriter, req rpcIn) { rpcResult(w, req.ID, `"nonsense"`) }, "decode the tool result"},
	} {
		t.Run(name, func(t *testing.T) {
			f := modernServer(t, func(_ *fakeServer, w http.ResponseWriter, req rpcIn) { tc.answer(w, req) })
			_, err := (&Client{Endpoint: f.srv.URL}).CallTool(context.Background(), "get_weather", nil)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Errorf("error = %v, want one naming %q", err, tc.want)
			}
		})
	}
	// A server that cannot be reached at all.
	if _, err := (&Client{Endpoint: "http://127.0.0.1:1", Timeout: time.Second}).CallTool(context.Background(), "x", nil); err == nil {
		t.Error("an unreachable server answered")
	}
	// A credential that cannot be presented stops the request before it is sent.
	f := modernServer(t, func(_ *fakeServer, w http.ResponseWriter, req rpcIn) { rpcResult(w, req.ID, `{}`) })
	refuse := &Client{Endpoint: f.srv.URL, Authorize: func(http.Header) error { return errors.New("no key") }}
	if _, err := refuse.CallTool(context.Background(), "get_weather", nil); err == nil || !strings.Contains(err.Error(), "no key") {
		t.Errorf("an unusable credential: %v", err)
	}
}

func TestHeaderValuesAndMirroredArguments(t *testing.T) {
	for in, want := range map[string]string{
		"Bern": "Bern", "a b": "a b", " lead": "=?base64?IGxlYWQ=?=", "=?base64?x?=": "=?base64?PT9iYXNlNjQ/eD89?=",
		"Zürich": "=?base64?WsO8cmljaA==?=", "": "",
	} {
		if got := headerValue(in); got != want {
			t.Errorf("headerValue(%q) = %q, want %q", in, got, want)
		}
	}
	args := map[string]any{"s": "x", "b": false, "f": float64(4), "i": 5, "l": int64(6), "n": nil, "o": map[string]any{"k": "v"}}
	for name, want := range map[string]string{"s": "x", "b": "false", "f": "4", "i": "5", "l": "6"} {
		if got, ok := mirroredValue(args, headerParam{path: []string{name}}); !ok || got != want {
			t.Errorf("mirroredValue(%s) = %q, %v, want %q", name, got, ok, want)
		}
	}
	for _, path := range [][]string{{"n"}, {"missing"}, {"o"}, {"s", "deeper"}} {
		if got, ok := mirroredValue(args, headerParam{path: path}); ok {
			t.Errorf("mirroredValue(%v) = %q, want no header", path, got)
		}
	}
	for schema, want := range map[string]string{
		`{"properties":{"a":{"type":"string","x-mcp-header":"bad name"}}}`:                                             "not a header name",
		`{"properties":{"a":{"type":"string","x-mcp-header":""}}}`:                                                     "not a header name",
		`{"properties":{"a":{"type":"string","x-mcp-header":"X"},"b":{"type":"string","x-mcp-header":"x"}}}`:           "two of its arguments",
		`{"properties":{"a":{"type":"number","x-mcp-header":"X"}}}`:                                                    "not a string, an integer or a boolean",
		`{"properties":{"a":{"type":"string","x-mcp-header":"X"}},"items":{"type":"string","x-mcp-header":"ignored"}}`: "",
		`not json`: "",
	} {
		if got := mirroredArguments(json.RawMessage(schema)).invalid; (want == "") != (got == "") || !strings.Contains(got, want) {
			t.Errorf("mirroredArguments(%s) invalid = %q, want %q", schema, got, want)
		}
	}
	if e := (&ProtocolError{Status: 500, Message: "x"}).Error(); e != "the MCP server answered 500: x" {
		t.Errorf("error text = %q", e)
	}
}
