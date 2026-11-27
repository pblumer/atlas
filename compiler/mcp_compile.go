package compiler

import (
	"encoding/json"
	"fmt"
	"strings"
)

// compileMcpConnectorTask compiles an <atlas:mcpConnector> service task: one call to one
// tool of the MCP server its Worker is (ADR-draft-mcp-worker).
//
// What is checked here is what the model alone can get wrong: a Worker and a tool have
// to be named, the arguments have to be an object, and a timeout has to be a duration.
// Whether the tool exists on the server, and whether the Worker permits it, are facts of
// an installation and not of a model — the first is the server's to answer when called,
// the second is checked against the Worker's list where the job is resolved.
func compileMcpConnectorTask(b *Builder, st xmlServiceTask, retries int32) (int32, error) {
	cn := st.Mcp
	fail := func(format string, args ...any) (int32, error) {
		return 0, fmt.Errorf("compiler: MCP task %q "+format, append([]any{st.Id}, args...)...)
	}
	if strings.TrimSpace(cn.Connector) == "" {
		return fail("needs a connector attribute naming the MCP Worker — the server — it calls")
	}
	workerExpr, err := connectorValue(b.gate(), st.Id, "MCP worker", "connector", cn.Connector)
	if err != nil {
		return 0, err
	}
	tool := strings.TrimSpace(cn.Tool)
	if tool == "" {
		return fail("needs a tool: the name of the one tool of that server this task calls")
	}
	cfg := McpConfig{
		Worker:     strings.TrimSpace(cn.Connector),
		WorkerExpr: workerExpr,
		Tool:       tool,
		ResultVar:  strings.TrimSpace(cn.ResultVariable),
		Retries:    retries,
	}
	if raw := strings.TrimSpace(cn.Timeout); raw != "" {
		nanos, err := parseISO8601Duration(raw)
		if err != nil {
			return fail("has an invalid timeout %q (want an ISO-8601 duration such as PT30S): %v", raw, err)
		}
		if nanos < int64(MinAgentTimeout) || nanos > int64(MaxAgentTimeout) {
			return fail("has a timeout %q outside %s to %s", raw, MinAgentTimeout, MaxAgentTimeout)
		}
		cfg.TimeoutNanos = nanos
	}
	if strings.TrimSpace(cn.Input) == "" {
		return b.AddMcpConnectorTask(cfg), nil // a tool that takes no arguments
	}
	dec := json.NewDecoder(strings.NewReader(cn.Input))
	dec.UseNumber()
	var raw any
	if err := dec.Decode(&raw); err != nil {
		return fail("has an input that is not JSON: %v", err)
	}
	input, ok := raw.(map[string]any)
	if !ok {
		return fail("input must be a JSON object of the tool's arguments, not a %T", raw)
	}
	feel, err := jsonTemplateToFeel(input)
	if err != nil {
		return fail("input: %v", err)
	}
	e, err := b.gate().compileFEEL(feel)
	if err != nil {
		return fail("input did not compile (a value's =expression may be malformed): %v", err)
	}
	cfg.Input = RestExpr{Expr: e}
	return b.AddMcpConnectorTask(cfg), nil
}
