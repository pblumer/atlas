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
// Message dispatch is shared with the stdio loop via handleWith; this method is
// transport only. The handler is stateless: it assigns no Mcp-Session-Id and
// requires none, which is sufficient for a tools-only server.
//
// Authentication is the api package's, not this handler's: mount it with
// api.WithMCP and it sits inside the same boundary as every other route, so a
// request without a credential never reaches here. What this method does is the
// other half — it forwards the credential the request arrived with to the Atlas
// API, so a tool call is exactly as privileged as whoever made it. It carries no
// identity of its own to lend (ADR-0196).
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
	resp, reply := s.handleWith(s.client.forCaller(callerFrom(r)), req)
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

// callerFrom lifts the credentials off an incoming MCP request. Both of Atlas's
// are taken, because both can legitimately arrive here: a bearer from a remote
// MCP client, and a session cookie when the signed-in web UI drives a tool
// itself. Neither is inspected — see the caller type.
func callerFrom(r *http.Request) caller {
	return caller{
		authorization: r.Header.Get("Authorization"),
		cookie:        r.Header.Get("Cookie"),
	}
}
