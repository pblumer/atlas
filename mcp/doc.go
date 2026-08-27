// Package mcp is Atlas's Model Context Protocol server: it lets an AI agent
// drive a running Atlas server through tools — deploy a BPMN model, manage
// design-time projects and artifacts, start an instance, complete human tasks,
// and inspect live runtime state.
//
// # Shape: an adapter over the HTTP API, on two transports
//
// The server speaks JSON-RPC 2.0 and translates each tool call into an HTTP
// request against a running Atlas server; it holds no engine state of its own.
// Two transports share one dispatch path:
//
//   - stdio (Serve) — newline-delimited JSON, one message per line. This is the
//     MCP stdio transport a local client (Claude Desktop, Claude Code) spawns.
//   - Streamable HTTP (ServeHTTP) — the remote transport. Mount it at a path
//     such as /mcp and a remote client can reach the same tools. Mounted through
//     api.WithMCP it sits inside the Atlas server's own access boundary, so under
//     --auth a request without a credential is refused there; the transport then
//     forwards whatever authenticated the caller to the API, so a tool call is
//     exactly as privileged as whoever made it and no more
//     (ADR-draft-authenticated-mcp-transport).
//
// This is deliberate. The engine is a single-writer partition (invariant I3):
// exactly one goroutine may touch a partition's processor and state, a
// discipline the api package already enforces behind its HTTP surface. By
// proxying to that surface rather than embedding the engine, the MCP server can
// never violate an engine invariant — it only ever makes HTTP calls. It is a
// pure adapter, and an AI agent sees the same deployments and instances a human
// sees in the web UI.
//
// # Project deletion
//
// A design-time project is a grouping folder, not an execution aggregate
// (ADR-0034). atlas_delete_project therefore calls the public project DELETE
// endpoint and removes only that folder. Drafts and decision references tagged
// with its id remain available as ungrouped artifacts; deployed definitions and
// process instances are unaffected. The API operation is idempotent and enforces
// the project owner role when authentication is enabled.
//
// # No new dependencies
//
// The protocol is implemented by hand (see server.go), matching the
// repository's preference for small, self-contained code over pulled-in SDKs.
// The only surface area is the four MCP methods a tools-only server needs:
// initialize, tools/list, tools/call, and ping.
//
// # Running it
//
// Remote (Streamable HTTP) — atlas serve mounts the transport at /mcp:
//
//	atlas serve --addr :8080            # engine + HTTP API + UI + /mcp
//
// Local (stdio) — a per-agent, short-lived adapter an MCP client spawns:
//
//	atlas mcp --server http://localhost:8080
//	atlas mcp --server https://atlas.example.com --token "$ATLAS_TOKEN"
//
// The token is what a stdio adapter authenticates with against a server running
// --auth, and is needed there: unlike the HTTP transport, which forwards each
// request's own caller, a stdio adapter is one process with one identity for its
// whole life. Without it every tool call comes back 401.
//
// For stdio, diagnostics go to stderr; stdout carries protocol traffic only.
package mcp
