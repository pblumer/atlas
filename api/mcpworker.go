package api

import (
	"fmt"
	"log/slog"
	"os"
	"slices"
	"sort"
	"strings"

	"github.com/pblumer/atlas/connector/mcptool"
	"github.com/pblumer/atlas/logging"
)

// --- The MCP Worker Type on the server (ADR-draft-mcp-worker) ---
//
// A Worker of this type is one Model Context Protocol server. This file holds what only
// the server side knows about it: the list of tools a process may call on it, checked
// where a job is resolved, and the environment a supervised worker is handed.

// mcpToolGrant is one tool an MCP Worker permits, with the two properties ADR-0538 asks
// of every operation, in that record's own words. They are stated here, by whoever
// configures the Worker, because nobody else knows them: the Worker Type cannot know
// what a server's tools do, the server's own hints are not to be trusted, and a modeller
// who could label a write as a read would.
type mcpToolGrant struct {
	// Name is the tool's name as the server lists it.
	Name string `json:"name"`
	// Idempotency is what a second delivery of one call does: natural, key, check or
	// none.
	Idempotency string `json:"idempotency"`
	// Effect is whether a repeated effect can be undone: reversible or irreversible.
	Effect string `json:"effect"`
}

var (
	mcpIdempotencies = []string{"natural", "key", "check", "none"}
	mcpEffects       = []string{"reversible", "irreversible"}
)

// maxMCPTools bounds a Worker's list. It is far above what a reviewed list holds: a
// list this long has stopped being a decision about each tool on it.
const maxMCPTools = 200

// validateMCPConnector checks an MCP server a Console operator is adding. The endpoint
// is required; a credential is not, because a server inside one's own network may ask
// for none. A tool listed without its two properties gets the worst case — a second
// delivery repeats the effect and it cannot be undone — which is what ADR-0538 assumes
// of anything that says nothing about itself.
func validateMCPConnector(p *createConnectorParams) string {
	p.Provider, p.Sender, p.Model = "", "", ""
	if err := mcptool.CheckEndpoint(p.Endpoint); err != nil {
		return "an mcp worker requires an endpoint: the MCP server's URL (https://mcp.example.ch/mcp) — " + err.Error()
	}
	if len(p.Tools) > maxMCPTools {
		return fmt.Sprintf("an mcp worker permits at most %d tools; a longer list is no longer a decision about each of them", maxMCPTools)
	}
	seen := map[string]bool{}
	for i := range p.Tools {
		t := &p.Tools[i]
		t.Name = strings.TrimSpace(t.Name)
		t.Idempotency = strings.ToLower(strings.TrimSpace(t.Idempotency))
		t.Effect = strings.ToLower(strings.TrimSpace(t.Effect))
		switch {
		case t.Name == "":
			return "an mcp worker's permitted tool needs a name: the tool's name as the server lists it"
		case seen[t.Name]:
			return fmt.Sprintf("an mcp worker permits the tool %q twice", t.Name)
		}
		seen[t.Name] = true
		if t.Idempotency == "" {
			t.Idempotency = "none"
		}
		if t.Effect == "" {
			t.Effect = "irreversible"
		}
		if !slices.Contains(mcpIdempotencies, t.Idempotency) {
			return fmt.Sprintf("the tool %q has idempotency %q; want one of %s", t.Name, t.Idempotency, strings.Join(mcpIdempotencies, ", "))
		}
		if !slices.Contains(mcpEffects, t.Effect) {
			return fmt.Sprintf("the tool %q has effect %q; want one of %s", t.Name, t.Effect, strings.Join(mcpEffects, ", "))
		}
	}
	return ""
}

// mcpToolRefusal says why a task's call must not be made, or "" when the Worker permits
// it. It runs on the run loop, where a job is resolved for a worker.
//
// The check is made here and not on the worker because of who holds what. The worker
// has the credential and could call anything the server offers; the list is a record on
// the server, edited by the Worker's editors, and a task is a model somebody deployed.
// Deciding it here means a model cannot reach a tool by naming it, whatever worker
// leases the job.
func (s *Server) mcpToolRefusal(worker, tool string) string {
	recs, err := s.connectors.LoadAll()
	if err != nil {
		return fmt.Sprintf("the Worker %q could not be read, so its list of permitted tools is not known: %v", worker, err)
	}
	for _, c := range recs {
		if c.Kind != connectorKindMCP || c.Name != worker {
			continue
		}
		if !c.Enabled {
			return fmt.Sprintf("the MCP Worker %q is disabled", worker)
		}
		for _, t := range c.Tools {
			if t.Name == tool {
				return ""
			}
		}
		return fmt.Sprintf("the MCP Worker %q does not permit the tool %q; an editor of the Worker adds it to the Worker's list of permitted tools (Console → Workers)", worker, tool)
	}
	return fmt.Sprintf("there is no MCP Worker named %q (Console → Workers)", worker)
}

const (
	mcpEnvPrefix     = "ATLAS_MCP_"
	mcpConnectorsEnv = mcpEnvPrefix + "CONNECTORS"
)

// mcpWorkerEnv renders the environment a supervised worker reads its MCP servers from:
// the names, and per name the endpoint and the credential. The list of permitted tools
// is deliberately not in it — it is checked on the server, so editing it takes effect
// with the next job and restarts nothing.
func (s *Server) mcpWorkerEnv() []string {
	var (
		env       []string
		names     []string
		fromStore bool
	)
	seen := map[string]bool{}
	addName := func(n string) {
		if n = strings.TrimSpace(n); n != "" && !seen[n] {
			seen[n] = true
			names = append(names, n)
		}
	}
	for _, name := range splitConnectorList(os.Getenv(mcpConnectorsEnv)) {
		addName(name)
	}
	s.do(func() {
		recs, err := s.connectors.LoadAll()
		if err != nil {
			logging.Warn(logging.WorkerSupervisorFailed, "could not read the worker store for a supervised mcp worker",
				slog.String("error", err.Error()))
			return
		}
		sort.Slice(recs, func(i, j int) bool { return recs[i].Name < recs[j].Name })
		taken := map[string]string{}
		for _, c := range recs {
			if c.Kind != connectorKindMCP || !c.Enabled {
				continue
			}
			envKey := connectorEnvKey(c.Name)
			if envKey == "" {
				continue
			}
			if first, dup := taken[envKey]; dup {
				logging.Warn(logging.WorkerSupervisorFailed,
					"two mcp workers share one environment name; the second is not handed to the supervised worker",
					slog.String("connector", c.Name), slog.String("collidesWith", first))
				continue
			}
			endpoint := strings.TrimSpace(c.Endpoint)
			if mcptool.CheckEndpoint(endpoint) != nil {
				continue
			}
			taken[envKey] = c.Name
			key := mcpEnvPrefix + envKey + "_"
			env = append(env, key+"URL="+endpoint)
			if ref := strings.TrimSpace(c.CredentialsRef); ref != "" {
				env = append(env, key+"CREDENTIALS="+s.resolveConnectorSecret(ref))
			}
			addName(c.Name)
			fromStore = true
		}
	})
	if !fromStore {
		return nil
	}
	return append(env, mcpConnectorsEnv+"="+strings.Join(names, ","))
}
