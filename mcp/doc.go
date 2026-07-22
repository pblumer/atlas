// Package mcp is Atlas's Model Context Protocol server: it lets an AI agent
// (Claude Desktop, Claude Code, or any MCP client) drive a running Atlas server
// through tools — deploy a BPMN model, start an instance, and inspect live
// runtime state.
//
// # Shape: a stdio adapter over the HTTP API
//
// The server speaks JSON-RPC 2.0 over stdio (newline-delimited, one message per
// line — the MCP stdio transport) and translates each tool call into an HTTP
// request against a running Atlas server. It holds no engine state of its own.
//
// This is deliberate. The engine is a single-writer partition (invariant I3):
// exactly one goroutine may touch a partition's processor and state, a
// discipline the api package already enforces behind its HTTP surface. By
// proxying to that surface rather than embedding the engine, the MCP server can
// never violate an engine invariant — it only ever makes HTTP calls. It is a
// pure adapter, and an AI agent sees the same deployments and instances a human
// sees in the web UI.
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
//	atlas serve --addr :8080            # in one process: the engine + HTTP API
//	atlas mcp --server http://localhost:8080   # in another: the MCP adapter on stdio
//
// The MCP process is short-lived and per-agent: an MCP client spawns it, speaks
// the protocol on its stdin/stdout, and tears it down. Diagnostics go to stderr;
// stdout carries protocol traffic only.
package mcp
