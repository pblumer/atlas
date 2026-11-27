// Package mcptool is the Worker Type that calls one tool of a remote Model Context
// Protocol server (ADR-draft-mcp-worker): a service task names a configured MCP Worker
// and a tool, and the worker behind it makes the call.
//
// This file is the protocol, written by hand for the reason the model adapters are
// (ADR-0117): a wire format that is a few JSON shapes over HTTP does not need an SDK in
// the engine's dependency list. It is the *client* side and has nothing to do with the
// top-level mcp package, which is Atlas's own MCP server.
//
// # Two protocols under one name
//
// MCP changed shape in its 2026-07-28 revision. Before it, a client opened a session —
// initialize, an Mcp-Session-Id, notifications/initialized — and a server could send
// requests of its own down the response stream. Since it, every request stands alone:
// what the handshake negotiated travels in each request's _meta and in HTTP headers, and
// there is no session to open or close. Servers of both kinds exist and will for years.
//
// So the client speaks the current revision first and falls back, as that revision's own
// backward-compatibility rule describes: a 4xx to a modern request whose body is not one
// of the modern errors means a server of the older kind. Which kind a server is is
// learned once, on tools/list, and remembered for as long as this client lives.
package mcptool

import (
	"bufio"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/pblumer/atlas/limits"
)

// The protocol revisions this client speaks. ModernVersion is sent first; the legacy
// ones are what it offers and accepts in an initialize handshake.
const (
	ModernVersion = "2026-07-28"
	legacyOffered = "2025-11-25"
)

var legacyAccepted = map[string]bool{"2025-11-25": true, "2025-06-18": true, "2025-03-26": true}

// The JSON-RPC error codes the 2026-07-28 revision introduced. Receiving one is how a
// 4xx is known to come from a modern server rather than from an older one that did not
// understand the request at all.
const (
	codeHeaderMismatch     = -32020
	codeMissingCapability  = -32021
	codeUnsupportedVersion = -32022
	codeMethodNotFound     = -32601
)

// The _meta keys every modern request carries in place of a handshake.
const (
	metaProtocolVersion = "io.modelcontextprotocol/protocolVersion"
	metaClientInfo      = "io.modelcontextprotocol/clientInfo"
	metaCapabilities    = "io.modelcontextprotocol/clientCapabilities"
)

// defaultTimeout bounds one HTTP exchange when the caller states none.
const defaultTimeout = 2 * time.Minute

// toolListTTL is how long a server's tool list is trusted when the server names no
// lifetime for it. The list is only read to learn which arguments a tool wants
// mirrored into headers, and a stale answer is corrected on the spot: a server that
// rejects the headers says so, and the list is read again.
const toolListTTL = 5 * time.Minute

// ProtocolError is a JSON-RPC error a server answered with, or an HTTP failure that
// carried none. It is the protocol failing, not the tool: a tool that ran and failed
// answers with a result whose IsError is set.
type ProtocolError struct {
	Status  int // the HTTP status
	Code    int // the JSON-RPC error code, 0 when the body carried none
	Message string
}

func (e *ProtocolError) Error() string {
	if e.Code != 0 {
		return fmt.Sprintf("the MCP server answered %d with error %d: %s", e.Status, e.Code, e.Message)
	}
	return fmt.Sprintf("the MCP server answered %d: %s", e.Status, e.Message)
}

// Tool is one tool as a server lists it. Description and Annotations are the server's
// own words about itself and are never put in front of a model here: what a model
// reads about a tool is the modeller's documentation of the task (ADR-0253), which is
// why a server changing its description changes nothing a process does.
type Tool struct {
	Name         string          `json:"name"`
	Title        string          `json:"title,omitempty"`
	Description  string          `json:"description,omitempty"`
	InputSchema  json.RawMessage `json:"inputSchema,omitempty"`
	OutputSchema json.RawMessage `json:"outputSchema,omitempty"`
	Annotations  struct {
		ReadOnlyHint    *bool `json:"readOnlyHint,omitempty"`
		DestructiveHint *bool `json:"destructiveHint,omitempty"`
		IdempotentHint  *bool `json:"idempotentHint,omitempty"`
	} `json:"annotations"`
}

// Result is what a tool call came back with.
type Result struct {
	// Text is the result's text blocks, joined by a newline.
	Text string
	// Structured is the result's structuredContent as JSON, nil when it carried none.
	Structured json.RawMessage
	// IsError says the tool ran and reported a failure of its own.
	IsError bool
	// Other names the kinds of content this client does not carry into a process —
	// image, audio, resource, resource_link — once per block.
	Other []string
}

// Client calls the tools of one MCP server. It is safe for concurrent use: a worker
// holds one per configured MCP Worker and works many jobs at once.
type Client struct {
	// Endpoint is the server's single MCP endpoint.
	Endpoint string
	// Authorize adds the credential to a request. nil sends none.
	Authorize func(http.Header) error
	// HTTP is the client to use; nil uses one with Timeout.
	HTTP *http.Client
	// Timeout bounds one exchange when HTTP is nil; 0 uses two minutes.
	Timeout time.Duration
	// ClientVersion is what this client says its version is.
	ClientVersion string

	mu      sync.Mutex
	nextID  int64
	era     era
	tools   map[string]toolHeaders
	expires time.Time
	now     func() time.Time // injected by tests
}

type era uint8

const (
	eraUnknown era = iota
	eraModern
	eraLegacy
)

// headerParam is one argument a tool wants mirrored into an HTTP header.
type headerParam struct {
	path   []string // the argument's place: a chain of property names
	header string   // the header's name after "Mcp-Param-"
	kind   string   // "string", "integer" or "boolean"
}

// toolHeaders is what the list said about one tool's mirrored arguments. invalid holds
// the reason a tool must not be called: the revision obliges a client to leave out a
// tool whose annotation it cannot honour.
type toolHeaders struct {
	params  []headerParam
	invalid string
}

type rpcRequest struct {
	JSONRPC string `json:"jsonrpc"`
	ID      *int64 `json:"id,omitempty"`
	Method  string `json:"method"`
	Params  any    `json:"params,omitempty"`
}

type rpcError struct {
	Code    int             `json:"code"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data,omitempty"`
}

type rpcMessage struct {
	ID     json.RawMessage `json:"id,omitempty"`
	Method string          `json:"method,omitempty"`
	Result json.RawMessage `json:"result,omitempty"`
	Error  *rpcError       `json:"error,omitempty"`
}

// exchange is one answered POST.
type exchange struct {
	status  int
	header  http.Header
	message rpcMessage // the JSON-RPC response to our request, when there was one
	found   bool
}

func (c *Client) clock() time.Time {
	if c.now != nil {
		return c.now()
	}
	return time.Now()
}

func (c *Client) id() int64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.nextID++
	return c.nextID
}

func (c *Client) meta() map[string]any {
	version := c.ClientVersion
	if version == "" {
		version = "dev"
	}
	return map[string]any{
		metaProtocolVersion: ModernVersion,
		metaClientInfo:      map[string]string{"name": "atlas", "version": version},
		// No capability is declared, so a server may ask this client for nothing: not
		// for a model's answer, not for a person's input, not for its roots.
		metaCapabilities: map[string]any{},
	}
}

// post sends one JSON-RPC message and reads the answer to it. A notification has no id
// and no answer. extra are the headers the protocol asks for beside the fixed ones.
func (c *Client) post(ctx context.Context, method string, params any, notification bool, extra map[string]string) (exchange, error) {
	req := rpcRequest{JSONRPC: "2.0", Method: method, Params: params}
	var id int64
	if !notification {
		id = c.id()
		req.ID = &id
	}
	body, err := json.Marshal(req)
	if err != nil {
		return exchange{}, fmt.Errorf("mcp: encode %s: %w", method, err)
	}
	hreq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.Endpoint, bytes.NewReader(body))
	if err != nil {
		return exchange{}, err
	}
	hreq.Header.Set("Content-Type", "application/json")
	// A server may answer either way and a client must take both.
	hreq.Header.Set("Accept", "application/json, text/event-stream")
	for k, v := range extra {
		hreq.Header.Set(k, v)
	}
	if c.Authorize != nil {
		if err := c.Authorize(hreq.Header); err != nil {
			return exchange{}, err
		}
	}
	resp, err := c.http().Do(hreq)
	if err != nil {
		return exchange{}, err
	}
	defer func() { _ = resp.Body.Close() }()
	ex := exchange{status: resp.StatusCode, header: resp.Header}
	limited := io.LimitReader(resp.Body, limits.Default().Payload)
	if notification {
		_, _ = io.Copy(io.Discard, limited)
		return ex, nil
	}
	if strings.HasPrefix(resp.Header.Get("Content-Type"), "text/event-stream") {
		ex.message, ex.found, err = c.readStream(ctx, limited, id, extra)
		return ex, err
	}
	raw, err := io.ReadAll(limited)
	if err != nil {
		return ex, err
	}
	if len(bytes.TrimSpace(raw)) == 0 {
		return ex, nil
	}
	var msg rpcMessage
	if json.Unmarshal(raw, &msg) == nil && (msg.Result != nil || msg.Error != nil) {
		ex.message, ex.found = msg, true
		return ex, nil
	}
	// Not JSON-RPC at all: a gateway's page, a proxy's refusal. Kept as the message so
	// an operator reads what actually came back.
	ex.message.Error = &rpcError{Message: snippet(raw)}
	return ex, nil
}

func (c *Client) http() *http.Client {
	if c.HTTP != nil {
		return c.HTTP
	}
	timeout := c.Timeout
	if timeout <= 0 {
		timeout = defaultTimeout
	}
	return &http.Client{Timeout: timeout}
}

// readStream reads a server-sent event stream until the response to request id arrives.
//
// A server of the older kind may send messages of its own first. Notifications are
// passed over. A request is answered, because a server waiting on one would never send
// what this call is waiting for: a ping gets its empty result, and anything else is
// told this client does not do that — it declared no capability, so nothing else may
// be asked of it.
func (c *Client) readStream(ctx context.Context, r io.Reader, id int64, session map[string]string) (rpcMessage, bool, error) {
	want := strconv.FormatInt(id, 10)
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 64<<10), int(limits.Default().Payload))
	var data strings.Builder
	flush := func() (rpcMessage, bool) {
		defer data.Reset()
		if data.Len() == 0 {
			return rpcMessage{}, false
		}
		var msg rpcMessage
		if json.Unmarshal([]byte(data.String()), &msg) != nil {
			return rpcMessage{}, false
		}
		answered := msg.Result != nil || msg.Error != nil
		if answered && strings.TrimSpace(string(msg.ID)) == want {
			return msg, true
		}
		if msg.Method != "" && len(msg.ID) > 0 && !answered {
			c.answerServerRequest(ctx, msg, session)
		}
		return rpcMessage{}, false
	}
	for sc.Scan() {
		line := sc.Text()
		switch {
		case line == "":
			if msg, ok := flush(); ok {
				return msg, true, nil
			}
		case strings.HasPrefix(line, ":"):
			// a comment
		case strings.HasPrefix(line, "data:"):
			if data.Len() > 0 {
				data.WriteByte('\n')
			}
			data.WriteString(strings.TrimPrefix(strings.TrimPrefix(line, "data:"), " "))
		}
	}
	if msg, ok := flush(); ok {
		return msg, true, nil
	}
	if err := sc.Err(); err != nil {
		return rpcMessage{}, false, err
	}
	return rpcMessage{}, false, nil
}

// answerServerRequest replies to a request a legacy server sent down the stream. The
// reply is a POST of its own; that it fails changes nothing this call can do about it.
func (c *Client) answerServerRequest(ctx context.Context, req rpcMessage, session map[string]string) {
	reply := map[string]any{"jsonrpc": "2.0", "id": req.ID}
	if req.Method == "ping" {
		reply["result"] = map[string]any{}
	} else {
		reply["error"] = rpcError{Code: codeMethodNotFound, Message: "this client does not support " + req.Method}
	}
	body, err := json.Marshal(reply)
	if err != nil {
		return
	}
	hreq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.Endpoint, bytes.NewReader(body))
	if err != nil {
		return
	}
	hreq.Header.Set("Content-Type", "application/json")
	hreq.Header.Set("Accept", "application/json, text/event-stream")
	for k, v := range session {
		hreq.Header.Set(k, v)
	}
	if c.Authorize != nil && c.Authorize(hreq.Header) != nil {
		return
	}
	if resp, err := c.http().Do(hreq); err == nil {
		_ = resp.Body.Close()
	}
}

// result turns an exchange into the JSON-RPC result, or the error it stands for.
func (ex exchange) result() (json.RawMessage, error) {
	if ex.found && ex.message.Error == nil && ex.status < 300 {
		return ex.message.Result, nil
	}
	perr := &ProtocolError{Status: ex.status}
	switch {
	case ex.message.Error != nil:
		perr.Code, perr.Message = ex.message.Error.Code, ex.message.Error.Message
	case ex.status < 300:
		perr.Message = "the response carried no answer to the request"
	default:
		perr.Message = http.StatusText(ex.status)
	}
	return nil, perr
}

// modern reports whether a failed exchange is a modern server's own refusal — as
// against an older server that did not understand a modern request at all.
func (ex exchange) modern() bool {
	if ex.message.Error == nil {
		return false
	}
	switch ex.message.Error.Code {
	case codeHeaderMismatch, codeMissingCapability, codeUnsupportedVersion:
		return true
	}
	return false
}

// --- the modern revision ----------------------------------------------------------

func (c *Client) modernRequest(ctx context.Context, method, name string, params map[string]any, extra map[string]string) (exchange, error) {
	if params == nil {
		params = map[string]any{}
	}
	params["_meta"] = c.meta()
	headers := map[string]string{"MCP-Protocol-Version": ModernVersion, "Mcp-Method": method}
	if name != "" {
		headers["Mcp-Name"] = headerValue(name)
	}
	for k, v := range extra {
		headers[k] = v
	}
	return c.post(ctx, method, params, false, headers)
}

type toolList struct {
	Tools      []Tool `json:"tools"`
	NextCursor string `json:"nextCursor"`
	TTLMs      int64  `json:"ttlMs"`
}

// listModern pages through a modern server's tools. legacy is true when the server
// turned out to be of the older kind, which is this function's other job: tools/list is
// where the two are told apart, because it changes nothing and has to be asked anyway.
func (c *Client) listModern(ctx context.Context) (tools []Tool, ttl time.Duration, legacy bool, err error) {
	cursor := ""
	for {
		params := map[string]any{}
		if cursor != "" {
			params["cursor"] = cursor
		}
		ex, err := c.modernRequest(ctx, "tools/list", "", params, nil)
		if err != nil {
			return nil, 0, false, err
		}
		raw, rerr := ex.result()
		if rerr != nil {
			if ex.status >= 400 && ex.status < 500 && !ex.modern() && len(tools) == 0 {
				return nil, 0, true, nil
			}
			return nil, 0, false, rerr
		}
		var page toolList
		if err := json.Unmarshal(raw, &page); err != nil {
			return nil, 0, false, fmt.Errorf("mcp: decode the tool list: %w", err)
		}
		tools = append(tools, page.Tools...)
		if page.TTLMs > 0 {
			ttl = time.Duration(page.TTLMs) * time.Millisecond
		}
		if page.NextCursor == "" {
			return tools, ttl, false, nil
		}
		cursor = page.NextCursor
	}
}

// --- the older revisions ----------------------------------------------------------

// legacySession opens a session and returns the headers every later request of it
// carries: the protocol version the two agreed on, and the session id if the server
// assigned one.
func (c *Client) legacySession(ctx context.Context) (map[string]string, error) {
	version := c.ClientVersion
	if version == "" {
		version = "dev"
	}
	ex, err := c.post(ctx, "initialize", map[string]any{
		"protocolVersion": legacyOffered,
		"capabilities":    map[string]any{},
		"clientInfo":      map[string]string{"name": "atlas", "version": version},
	}, false, nil)
	if err != nil {
		return nil, err
	}
	raw, err := ex.result()
	if err != nil {
		return nil, err
	}
	var init struct {
		ProtocolVersion string `json:"protocolVersion"`
	}
	if err := json.Unmarshal(raw, &init); err != nil {
		return nil, fmt.Errorf("mcp: decode the initialize result: %w", err)
	}
	if !legacyAccepted[init.ProtocolVersion] {
		return nil, fmt.Errorf("mcp: the server speaks protocol version %q, which this client does not", init.ProtocolVersion)
	}
	session := map[string]string{"MCP-Protocol-Version": init.ProtocolVersion}
	if sid := ex.header.Get("Mcp-Session-Id"); sid != "" {
		session["Mcp-Session-Id"] = sid
	}
	if _, err := c.post(ctx, "notifications/initialized", nil, true, session); err != nil {
		return nil, err
	}
	return session, nil
}

// endLegacySession says the session is over. A server may refuse to be told; either way
// the call it was opened for is done.
func (c *Client) endLegacySession(ctx context.Context, session map[string]string) {
	if session["Mcp-Session-Id"] == "" {
		return
	}
	hreq, err := http.NewRequestWithContext(ctx, http.MethodDelete, c.Endpoint, nil)
	if err != nil {
		return
	}
	for k, v := range session {
		hreq.Header.Set(k, v)
	}
	if c.Authorize != nil && c.Authorize(hreq.Header) != nil {
		return
	}
	if resp, err := c.http().Do(hreq); err == nil {
		_ = resp.Body.Close()
	}
}

func (c *Client) listLegacy(ctx context.Context) ([]Tool, error) {
	session, err := c.legacySession(ctx)
	if err != nil {
		return nil, err
	}
	defer c.endLegacySession(ctx, session)
	var tools []Tool
	cursor := ""
	for {
		var params any
		if cursor != "" {
			params = map[string]any{"cursor": cursor}
		}
		ex, err := c.post(ctx, "tools/list", params, false, session)
		if err != nil {
			return nil, err
		}
		raw, err := ex.result()
		if err != nil {
			return nil, err
		}
		var page toolList
		if err := json.Unmarshal(raw, &page); err != nil {
			return nil, fmt.Errorf("mcp: decode the tool list: %w", err)
		}
		tools = append(tools, page.Tools...)
		if page.NextCursor == "" {
			return tools, nil
		}
		cursor = page.NextCursor
	}
}

// --- what a caller uses -----------------------------------------------------------

// ListTools returns the tools the server offers, in the server's order.
func (c *Client) ListTools(ctx context.Context) ([]Tool, error) {
	c.mu.Lock()
	known := c.era
	c.mu.Unlock()
	if known == eraLegacy {
		return c.listLegacy(ctx)
	}
	tools, ttl, legacy, err := c.listModern(ctx)
	if err != nil {
		return nil, err
	}
	if legacy {
		tools, err := c.listLegacy(ctx)
		if err != nil {
			return nil, err
		}
		c.mu.Lock()
		c.era = eraLegacy
		c.mu.Unlock()
		return tools, nil
	}
	c.remember(tools, ttl)
	return tools, nil
}

// remember keeps what a modern server's list said about mirrored arguments.
func (c *Client) remember(tools []Tool, ttl time.Duration) {
	if ttl <= 0 {
		ttl = toolListTTL
	}
	index := make(map[string]toolHeaders, len(tools))
	for _, t := range tools {
		index[t.Name] = mirroredArguments(t.InputSchema)
	}
	c.mu.Lock()
	c.era, c.tools, c.expires = eraModern, index, c.clock().Add(ttl)
	c.mu.Unlock()
}

// headersFor returns what a modern server's tool wants mirrored, reading the list when
// it is not known or has run out. refresh reads it regardless.
func (c *Client) headersFor(ctx context.Context, tool string, refresh bool) (toolHeaders, era, error) {
	c.mu.Lock()
	known, fresh := c.era, c.tools != nil && c.clock().Before(c.expires)
	th, listed := c.tools[tool]
	c.mu.Unlock()
	if known == eraLegacy {
		return toolHeaders{}, eraLegacy, nil
	}
	if known == eraModern && fresh && listed && !refresh {
		return th, eraModern, nil
	}
	if _, err := c.ListTools(ctx); err != nil {
		return toolHeaders{}, eraUnknown, err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.era == eraLegacy {
		return toolHeaders{}, eraLegacy, nil
	}
	th, listed = c.tools[tool]
	if !listed {
		return toolHeaders{}, eraModern, fmt.Errorf("mcp: the server offers no tool named %q", tool)
	}
	return th, eraModern, nil
}

// CallTool calls one tool with the given arguments.
//
// An error is the protocol or the transport failing. A tool that ran and reported its
// own failure is a Result with IsError set and no error: the two are different facts,
// and a process treats them differently.
func (c *Client) CallTool(ctx context.Context, tool string, arguments map[string]any) (Result, error) {
	if arguments == nil {
		arguments = map[string]any{}
	}
	th, kind, err := c.headersFor(ctx, tool, false)
	if err != nil {
		return Result{}, err
	}
	if kind == eraLegacy {
		return c.callLegacy(ctx, tool, arguments)
	}
	ex, err := c.callModern(ctx, tool, arguments, th)
	if err != nil {
		return Result{}, err
	}
	if ex.message.Error != nil && ex.message.Error.Code == codeHeaderMismatch {
		// The server's idea of which arguments belong in headers has moved on from
		// the list this client holds. The revision says what to do: read the list
		// again and retry — once, because a second mismatch is not staleness.
		if th, _, err = c.headersFor(ctx, tool, true); err != nil {
			return Result{}, err
		}
		if ex, err = c.callModern(ctx, tool, arguments, th); err != nil {
			return Result{}, err
		}
	}
	raw, err := ex.result()
	if err != nil {
		return Result{}, err
	}
	return decodeResult(raw)
}

func (c *Client) callModern(ctx context.Context, tool string, arguments map[string]any, th toolHeaders) (exchange, error) {
	if th.invalid != "" {
		return exchange{}, fmt.Errorf("mcp: the tool %q cannot be called: %s", tool, th.invalid)
	}
	extra := map[string]string{}
	for _, p := range th.params {
		if v, ok := mirroredValue(arguments, p); ok {
			extra["Mcp-Param-"+p.header] = v
		}
	}
	return c.modernRequest(ctx, "tools/call", tool, map[string]any{"name": tool, "arguments": arguments}, extra)
}

func (c *Client) callLegacy(ctx context.Context, tool string, arguments map[string]any) (Result, error) {
	session, err := c.legacySession(ctx)
	if err != nil {
		return Result{}, err
	}
	defer c.endLegacySession(ctx, session)
	ex, err := c.post(ctx, "tools/call", map[string]any{"name": tool, "arguments": arguments}, false, session)
	if err != nil {
		return Result{}, err
	}
	raw, err := ex.result()
	if err != nil {
		return Result{}, err
	}
	return decodeResult(raw)
}

// ErrInputRequired is a modern server answering a call with a request for more input —
// or with nothing but an invitation to ask again. This client declares no capability to
// supply input, so it can do neither more nor less than say so.
var ErrInputRequired = errors.New("mcp: the server did not complete the call and asks for input this client does not supply")

func decodeResult(raw json.RawMessage) (Result, error) {
	var r struct {
		ResultType string `json:"resultType"`
		Content    []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
		StructuredContent json.RawMessage `json:"structuredContent"`
		IsError           bool            `json:"isError"`
	}
	if err := json.Unmarshal(raw, &r); err != nil {
		return Result{}, fmt.Errorf("mcp: decode the tool result: %w", err)
	}
	switch r.ResultType {
	case "", "complete":
	case "input_required":
		return Result{}, ErrInputRequired
	default:
		return Result{}, fmt.Errorf("mcp: the server answered with result type %q, which this client does not know", r.ResultType)
	}
	out := Result{IsError: r.IsError}
	var texts []string
	for _, block := range r.Content {
		if block.Type == "text" {
			texts = append(texts, block.Text)
			continue
		}
		out.Other = append(out.Other, block.Type)
	}
	out.Text = strings.Join(texts, "\n")
	if s := bytes.TrimSpace(r.StructuredContent); len(s) > 0 && string(s) != "null" {
		out.Structured = json.RawMessage(s)
	}
	return out, nil
}

// --- arguments mirrored into headers ----------------------------------------------

// mirroredArguments reads a tool's input schema for the arguments it wants repeated as
// HTTP headers (x-mcp-header). The annotation counts only on a property reached through
// property names alone, and only on a string, an integer or a boolean. A tool carrying
// one this client cannot honour is marked invalid: the revision obliges a client to
// leave such a tool out rather than call it wrongly.
func mirroredArguments(schema json.RawMessage) toolHeaders {
	var th toolHeaders
	seen := map[string]bool{}
	var walk func(raw json.RawMessage, path []string)
	walk = func(raw json.RawMessage, path []string) {
		var node struct {
			Type       json.RawMessage            `json:"type"`
			Header     *string                    `json:"x-mcp-header"`
			Properties map[string]json.RawMessage `json:"properties"`
		}
		if len(raw) == 0 || json.Unmarshal(raw, &node) != nil {
			return
		}
		if node.Header != nil && len(path) > 0 {
			var kind string
			_ = json.Unmarshal(node.Type, &kind)
			name := *node.Header
			switch {
			case !isToken(name):
				th.invalid = fmt.Sprintf("its argument %q names the header %q, which is not a header name", strings.Join(path, "."), name)
			case seen[strings.ToLower(name)]:
				th.invalid = fmt.Sprintf("two of its arguments name the header %q", name)
			case kind != "string" && kind != "integer" && kind != "boolean":
				th.invalid = fmt.Sprintf("its argument %q is to be sent as a header but is not a string, an integer or a boolean", strings.Join(path, "."))
			default:
				seen[strings.ToLower(name)] = true
				th.params = append(th.params, headerParam{path: append([]string(nil), path...), header: name, kind: kind})
			}
		}
		for name, child := range node.Properties {
			walk(child, append(path, name))
		}
	}
	walk(schema, nil)
	return th
}

// isToken reports whether s is an HTTP field-name token.
func isToken(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
		case strings.ContainsRune("!#$%&'*+-.^_`|~", r):
		default:
			return false
		}
	}
	return true
}

// mirroredValue finds an argument and renders it as its header's value. An argument
// that is absent or null has no header.
func mirroredValue(arguments map[string]any, p headerParam) (string, bool) {
	var v any = arguments
	for _, name := range p.path {
		obj, ok := v.(map[string]any)
		if !ok {
			return "", false
		}
		if v, ok = obj[name]; !ok || v == nil {
			return "", false
		}
	}
	switch x := v.(type) {
	case string:
		return headerValue(x), true
	case bool:
		return strconv.FormatBool(x), true
	case json.Number:
		return x.String(), true
	case float64:
		return strconv.FormatFloat(x, 'f', -1, 64), true
	case int:
		return strconv.Itoa(x), true
	case int64:
		return strconv.FormatInt(x, 10), true
	}
	return "", false
}

// headerValue is a string as a header carries it: itself when it is plain printable
// ASCII with no space at either end, and otherwise the Base64 of its UTF-8 bytes
// between the revision's sentinels — which is also how a value that happens to look
// like such a sentinel is told apart from one.
func headerValue(s string) string {
	plain := s == strings.TrimSpace(s) && !strings.HasPrefix(s, "=?base64?")
	for i := 0; plain && i < len(s); i++ {
		plain = s[i] >= 0x20 && s[i] < 0x7f
	}
	if plain {
		return s
	}
	return "=?base64?" + base64.StdEncoding.EncodeToString([]byte(s)) + "?="
}

// snippet keeps an error message readable when a server answers with a page.
func snippet(b []byte) string {
	const max = 300
	s := strings.TrimSpace(string(b))
	if len(s) > max {
		return s[:max] + "…"
	}
	return s
}
