package mcp

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
)

// serverName and serverVersion identify this MCP server to clients in the
// initialize handshake.
const (
	serverName    = "atlas-mcp"
	serverVersion = "0.1.0-dev"

	// defaultProtocolVersion is the MCP revision we advertise when a client does
	// not request one. We echo the client's requested version when we support it
	// (see negotiateVersion) so newer clients interoperate cleanly.
	defaultProtocolVersion = "2025-06-18"

	// maxLine bounds a single JSON-RPC message. MCP stdio framing is one JSON
	// object per line; a BPMN model embedded in a deploy call is the largest
	// realistic payload, so this mirrors the api package's 4 MiB body cap with
	// generous headroom for JSON escaping.
	maxLine = 16 << 20
)

// supportedProtocolVersions lists MCP revisions this server can speak. The set
// is ordered newest-first only for readability; membership is what matters.
var supportedProtocolVersions = map[string]bool{
	"2024-11-05": true,
	"2025-03-26": true,
	"2025-06-18": true,
}

// JSON-RPC 2.0 error codes we use (subset of the spec).
const (
	codeParseError     = -32700
	codeInvalidRequest = -32600
	codeMethodNotFound = -32601
	codeInvalidParams  = -32602
	codeInternalError  = -32603
)

type rpcRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

// isNotification reports whether the message is a JSON-RPC notification (no id
// member), which must never be answered.
func (r rpcRequest) isNotification() bool { return len(r.ID) == 0 }

type rpcResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  any             `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    any    `json:"data,omitempty"`
}

// Server is a Model Context Protocol server that exposes the Atlas API as MCP
// tools over a stdio JSON-RPC 2.0 transport. It is deliberately dependency-free
// and hand-written, in keeping with the rest of the repository.
type Server struct {
	client *Client
	tools  []Tool
	index  map[string]Tool
}

// NewServer builds an MCP server that proxies tool calls to the Atlas server
// reachable through client.
func NewServer(client *Client) *Server {
	s := &Server{client: client, index: map[string]Tool{}}
	s.tools = defaultTools()
	for _, t := range s.tools {
		s.index[t.Name] = t
	}
	return s
}

// Serve runs the JSON-RPC loop, reading newline-delimited messages from in and
// writing responses to out, until in reaches EOF. It returns the first read
// error, or nil on a clean EOF. Diagnostics must never be written to out (that
// is the protocol channel); callers should log to stderr.
func (s *Server) Serve(in io.Reader, out io.Writer) error {
	scanner := bufio.NewScanner(in)
	scanner.Buffer(make([]byte, 0, 64<<10), maxLine)
	enc := json.NewEncoder(out)

	for scanner.Scan() {
		line := bytes.TrimSpace(scanner.Bytes())
		if len(line) == 0 {
			continue
		}
		var req rpcRequest
		if err := json.Unmarshal(line, &req); err != nil {
			// A malformed line can't carry a reliable id; reply with null id per
			// the JSON-RPC spec.
			_ = enc.Encode(errorResponse(nil, codeParseError, "parse error"))
			continue
		}
		resp, reply := s.handle(req)
		if reply {
			if err := enc.Encode(resp); err != nil {
				return err
			}
		}
	}
	return scanner.Err()
}

// handle dispatches one request. The bool result is false for notifications and
// anything else that must not be answered.
func (s *Server) handle(req rpcRequest) (rpcResponse, bool) {
	switch req.Method {
	case "initialize":
		return s.handleInitialize(req), true
	case "notifications/initialized", "notifications/cancelled":
		// Notifications carry no id and get no response.
		return rpcResponse{}, false
	case "ping":
		return okResponse(req.ID, map[string]any{}), true
	case "tools/list":
		return s.handleToolsList(req), true
	case "tools/call":
		return s.handleToolsCall(req), true
	default:
		if req.isNotification() {
			return rpcResponse{}, false
		}
		return errorResponse(req.ID, codeMethodNotFound, "method not found: "+req.Method), true
	}
}

func (s *Server) handleInitialize(req rpcRequest) rpcResponse {
	var params struct {
		ProtocolVersion string `json:"protocolVersion"`
	}
	if len(req.Params) > 0 {
		_ = json.Unmarshal(req.Params, &params)
	}
	return okResponse(req.ID, map[string]any{
		"protocolVersion": negotiateVersion(params.ProtocolVersion),
		"capabilities": map[string]any{
			"tools": map[string]any{},
		},
		"serverInfo": map[string]any{
			"name":    serverName,
			"version": serverVersion,
		},
	})
}

// negotiateVersion echoes the client's requested protocol version when we
// support it, otherwise offers our default. This is the behaviour the MCP spec
// prescribes for version negotiation.
func negotiateVersion(requested string) string {
	if requested != "" && supportedProtocolVersions[requested] {
		return requested
	}
	return defaultProtocolVersion
}

func (s *Server) handleToolsList(req rpcRequest) rpcResponse {
	list := make([]map[string]any, 0, len(s.tools))
	for _, t := range s.tools {
		list = append(list, map[string]any{
			"name":        t.Name,
			"description": t.Description,
			"inputSchema": t.InputSchema,
		})
	}
	return okResponse(req.ID, map[string]any{"tools": list})
}

func (s *Server) handleToolsCall(req rpcRequest) rpcResponse {
	var params struct {
		Name      string         `json:"name"`
		Arguments map[string]any `json:"arguments"`
	}
	if err := json.Unmarshal(req.Params, &params); err != nil {
		return errorResponse(req.ID, codeInvalidParams, "invalid tools/call params: "+err.Error())
	}
	tool, ok := s.index[params.Name]
	if !ok {
		return errorResponse(req.ID, codeInvalidParams, "unknown tool: "+params.Name)
	}
	if params.Arguments == nil {
		params.Arguments = map[string]any{}
	}

	// A tool's own failure (bad argument, server rejection) is reported as a
	// tool result with isError:true, not a protocol error — that is the MCP
	// contract, and it lets the model see and react to the message.
	text, err := tool.Handler(s.client, params.Arguments)
	if err != nil {
		return okResponse(req.ID, toolResult(err.Error(), true))
	}
	return okResponse(req.ID, toolResult(text, false))
}

// toolResult builds the MCP CallToolResult shape.
func toolResult(text string, isError bool) map[string]any {
	return map[string]any{
		"content": []map[string]any{
			{"type": "text", "text": text},
		},
		"isError": isError,
	}
}

func okResponse(id json.RawMessage, result any) rpcResponse {
	return rpcResponse{JSONRPC: "2.0", ID: normalizeID(id), Result: result}
}

func errorResponse(id json.RawMessage, code int, msg string) rpcResponse {
	return rpcResponse{JSONRPC: "2.0", ID: normalizeID(id), Error: &rpcError{Code: code, Message: msg}}
}

// normalizeID ensures the response id is valid JSON: a request without an id
// (should not reach a response path, but be safe) serializes as null.
func normalizeID(id json.RawMessage) json.RawMessage {
	if len(id) == 0 {
		return json.RawMessage("null")
	}
	return id
}

// argString extracts a required string argument.
func argString(args map[string]any, name string) (string, error) {
	v, ok := args[name]
	if !ok {
		return "", fmt.Errorf("missing required argument: %s", name)
	}
	s, ok := v.(string)
	if !ok {
		return "", fmt.Errorf("argument %q must be a string", name)
	}
	if s == "" {
		return "", fmt.Errorf("argument %q must not be empty", name)
	}
	return s, nil
}

// argUint extracts a required unsigned-integer argument. JSON numbers decode to
// float64, but clients occasionally send the value as a string, so both are
// accepted.
func argUint(args map[string]any, name string) (uint64, error) {
	v, ok := args[name]
	if !ok {
		return 0, fmt.Errorf("missing required argument: %s", name)
	}
	switch n := v.(type) {
	case float64:
		if n < 0 || n != float64(uint64(n)) {
			return 0, fmt.Errorf("argument %q must be a non-negative integer", name)
		}
		return uint64(n), nil
	case json.Number:
		u, err := parseUint(n.String())
		if err != nil {
			return 0, fmt.Errorf("argument %q must be a non-negative integer", name)
		}
		return u, nil
	case string:
		u, err := parseUint(n)
		if err != nil {
			return 0, fmt.Errorf("argument %q must be a non-negative integer", name)
		}
		return u, nil
	default:
		return 0, fmt.Errorf("argument %q must be an integer", name)
	}
}
