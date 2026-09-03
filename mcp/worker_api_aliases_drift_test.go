package mcp_test

// The Worker-oriented HTTP aliases added by ADR-0203 are design-time
// infrastructure configuration. They intentionally mirror the existing
// worker administration surface and are not MCP capabilities in this slice.
// Keep the omission explicit so the HTTP↔MCP drift guard still forces a conscious
// decision if these routes become agent-facing later.
func init() {
	const reason = "worker infrastructure configuration is admin/UI config, not an agent action"
	mcpOmittedRoutes["GET /api/v1/configured-workers"] = reason
	mcpOmittedRoutes["POST /api/v1/configured-workers"] = reason
	mcpOmittedRoutes["PATCH /api/v1/configured-workers/{id}"] = reason
	mcpOmittedRoutes["DELETE /api/v1/configured-workers/{id}"] = reason
	mcpOmittedRoutes["GET /api/v1/worker-types"] = "server worker-type arrangement; atlas_workers reports runtime placement per job type"
}
