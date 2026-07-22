package mcp

import (
	"encoding/json"
	"io"
	"net/http"
)

// ServeHTTP makes Server an http.Handler implementing the MCP "Streamable HTTP"
// transport, so the same tool surface reachable over stdio (Serve) can be
// mounted at a path such as /mcp and reached by a remote MCP client — for
// example a claude.ai custom connector.
//
// Message dispatch is shared with the stdio loop via handle; this method is
// transport only. The handler is stateless: it assigns no Mcp-Session-Id and
// requires none, which is sufficient for a tools-only server.
//
// Security: this endpoint performs NO authentication. Anything that can reach it
// can deploy and run processes. Do not expose it publicly without authentication
// in front of it (e.g. at a reverse proxy).
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodPost:
		s.serveHTTPPost(w, r)
	case http.MethodDelete:
		// Stateless: there is no session to tear down. Acknowledge per the
		// transport's session-termination semantics.
		w.WriteHeader(http.StatusNoContent)
	default:
		// We do not offer a server-initiated SSE stream, so GET (and anything
		// else) is not allowed; the spec permits 405 here.
		w.Header().Set("Allow", "POST, DELETE")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

// serveHTTPPost handles one JSON-RPC message posted to the endpoint. A request
// (with id) yields a single application/json JSON-RPC response; a notification
// (no id) is acknowledged with 202 Accepted and an empty body.
func (s *Server) serveHTTPPost(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(io.LimitReader(r.Body, maxLine))
	if err != nil {
		writeRPC(w, errorResponse(nil, codeParseError, "read body: "+err.Error()))
		return
	}
	var req rpcRequest
	if err := json.Unmarshal(body, &req); err != nil {
		writeRPC(w, errorResponse(nil, codeParseError, "parse error"))
		return
	}
	resp, reply := s.handle(req)
	if !reply {
		w.WriteHeader(http.StatusAccepted)
		return
	}
	writeRPC(w, resp)
}

// writeRPC encodes a JSON-RPC response as application/json. Protocol-level
// errors are still delivered with HTTP 200 and a JSON-RPC error body, the
// convention for JSON-RPC over HTTP.
func writeRPC(w http.ResponseWriter, resp rpcResponse) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(resp)
}
