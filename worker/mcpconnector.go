package worker

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"

	"github.com/pblumer/atlas/connector/mcptool"
)

// mcpEnvPrefix is where an MCP worker's servers live: ATLAS_MCP_CONNECTORS names them,
// and per name ATLAS_MCP_<NAME>_URL is the server's endpoint and
// ATLAS_MCP_<NAME>_CREDENTIALS its token, or a bundle {"token": "…"}, or nothing for a
// server that asks for none (ADR-0568).
const mcpEnvPrefix = "ATLAS_MCP_"

// mcpRegistryFromEnv builds the worker's MCP servers from its environment. It returns
// nil when none is configured, which is not an error: the kind is supervised by default
// and simply has nothing to serve until an operator adds a Worker.
func mcpRegistryFromEnv(env func(string) string) (*mcptool.Registry, []string, error) {
	names := splitAndTrim(env(mcpEnvPrefix + "CONNECTORS"))
	if len(names) == 0 {
		return nil, nil, nil
	}
	reg := mcptool.NewRegistry()
	for _, name := range names {
		key := mcpEnvPrefix + envFold(name) + "_"
		c, err := mcptool.NewClient(env(key+"URL"), env(key+"CREDENTIALS"))
		if err != nil {
			return nil, nil, fmt.Errorf("worker: mcp worker %q is not usable: %w (set %sURL to the MCP server's endpoint and, if it asks for one, %sCREDENTIALS to its token)", name, err, key, key)
		}
		reg.Register(name, c)
	}
	return reg, names, nil
}

// RunMCPJob calls the tool a leased MCP task stands for. The task arrives resolved —
// Worker, tool, arguments — and the worker adds what only it has: the server's endpoint
// and the credential.
func RunMCPJob(ctx context.Context, j Job, reg *mcptool.Registry) (map[string]any, error) {
	if j.Connector == nil {
		return nil, fmt.Errorf("mcp: the job carried no resolved task; the server could not evaluate its arguments (are the variables its =expressions read set?) or does not resolve MCP tasks")
	}
	raw, err := json.Marshal(j.Connector.Fields)
	if err != nil {
		return nil, err
	}
	var task mcptool.Job
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	dec.UseNumber()
	if err := dec.Decode(&task); err != nil {
		return nil, fmt.Errorf("mcp: cannot read the resolved task: %w", err)
	}
	return mcptool.Run(ctx, task, reg)
}
