package api

import (
	"encoding/json"
	"net/http"
	"slices"
	"strings"
	"testing"

	"github.com/pblumer/atlas/compiler"
	"github.com/pblumer/atlas/worker"
)

// The MCP Worker Type on the server (ADR-draft-mcp-worker): a Worker is one MCP server,
// a task calls one of its tools, and which tools a process may call is the Worker's list.

const mcpTaskBPMN = `<?xml version="1.0" encoding="UTF-8"?>
<bpmn:definitions xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL"
                  xmlns:atlas="http://atlas/schema/1.0" id="defs-mcp">
  <bpmn:process id="triage" isExecutable="true">
    <bpmn:startEvent id="s"/>
    <bpmn:serviceTask id="suchen">
      <bpmn:extensionElements>
        <atlas:mcpConnector connector="=server" tool="search_issues" resultVariable="treffer" timeout="PT30S"
          input='{"query":"=meldung","ticket":12345678901234567890,"limit":5}'/>
      </bpmn:extensionElements>
    </bpmn:serviceTask>
    <bpmn:endEvent id="e"/>
    <bpmn:sequenceFlow id="f1" sourceRef="s" targetRef="suchen"/>
    <bpmn:sequenceFlow id="f2" sourceRef="suchen" targetRef="e"/>
  </bpmn:process>
</bpmn:definitions>`

func createMCPWorker(t *testing.T, srv *Server, body string) (id string) {
	t.Helper()
	code, raw := serveInternal(t, srv, http.MethodPost, "/api/v1/connectors", body, "application/json")
	if code != http.StatusOK && code != http.StatusCreated {
		t.Fatalf("create worker: status=%d body=%s", code, raw)
	}
	var created struct {
		ID    string         `json:"id"`
		Tools []mcpToolGrant `json:"tools"`
	}
	if err := json.Unmarshal(raw, &created); err != nil || created.ID == "" {
		t.Fatalf("decode created worker: %v (%s)", err, raw)
	}
	return created.ID
}

func startTriage(t *testing.T, srv *Server, server string) leasedJob {
	t.Helper()
	code, raw := serveInternal(t, srv, http.MethodPost, "/api/v1/processes/1/instances",
		`{"variables":{"server":"`+server+`","meldung":"Portal langsam"}}`, "application/json")
	if code != http.StatusOK && code != http.StatusCreated {
		t.Fatalf("create instance: status=%d body=%s", code, raw)
	}
	return lease(t, srv, compiler.MCPJobType)
}

// A task is resolved for a worker with its arguments evaluated and nothing of the
// server; a tool the Worker's list does not permit travels as a refusal, and so does a
// Worker that does not exist or is switched off. Editing the list takes effect with the
// next job.
func TestAnMcpTaskIsResolvedAgainstItsWorkersList(t *testing.T) {
	srv, _ := newValidateServer(t)
	id := createMCPWorker(t, srv, `{"name":"monitoring","kind":"mcp","endpoint":"https://mcp.example.ch/mcp",
	  "tools":[{"name":"search_issues","idempotency":"natural","effect":"reversible"},{"name":"create_incident"}]}`)
	if code, raw := serveInternal(t, srv, http.MethodPost, "/api/v1/deployments", mcpTaskBPMN, "application/xml"); code != http.StatusOK && code != http.StatusCreated {
		t.Fatalf("deploy: status=%d body=%s", code, raw)
	}

	job := startTriage(t, srv, "monitoring")
	if job.Connector == nil || job.Connector.Kind != "mcp" {
		t.Fatalf("payload = %+v, want an mcp task", job.Connector)
	}
	f := job.Connector.Fields
	args, _ := f["arguments"].(map[string]any)
	if f["connector"] != "monitoring" || f["tool"] != "search_issues" || f["resultVariable"] != "treffer" ||
		f["timeoutMs"] != float64(30000) || f["refused"] != nil || args["query"] != "Portal langsam" || args["limit"] != float64(5) {
		t.Errorf("fields = %v", f)
	}
	for k := range f {
		if k == "endpoint" || k == "credentialsRef" || k == "token" {
			t.Errorf("the payload carries %q; where the server is and its credential are the worker's", k)
		}
	}
	// A long identifier reaches the worker as it was written, not rounded on the way —
	// read off the bytes the server sent, since decoding them here would round it too.
	if code, raw := serveInternal(t, srv, http.MethodPost, "/api/v1/processes/1/instances",
		`{"variables":{"server":"monitoring","meldung":"x"}}`, "application/json"); code != http.StatusOK && code != http.StatusCreated {
		t.Fatalf("create instance: status=%d body=%s", code, raw)
	}
	code, raw := serveInternal(t, srv, http.MethodPost, "/api/v1/jobs/activate", `{"type":"io.atlas.mcp","worker":"w1","leaseMs":60000}`, "application/json")
	if code != http.StatusOK || !strings.Contains(string(raw), "12345678901234567890") {
		t.Errorf("activate: status=%d, body does not carry the identifier exactly: %s", code, raw)
	}

	// A task whose Worker expression names nobody cannot be resolved; it is leased
	// without a task, and the worker fails it saying so.
	if code, raw := serveInternal(t, srv, http.MethodPost, "/api/v1/processes/1/instances",
		`{"variables":{"meldung":"x"}}`, "application/json"); code != http.StatusOK && code != http.StatusCreated {
		t.Fatalf("create instance: status=%d body=%s", code, raw)
	}
	if unresolved := lease(t, srv, compiler.MCPJobType); unresolved.Connector != nil {
		t.Errorf("a task that names no Worker was resolved: %+v", unresolved.Connector)
	}

	// The list is edited: the tool is no longer permitted.
	if code, raw := serveInternal(t, srv, http.MethodPatch, "/api/v1/connectors/"+id, `{"tools":[{"name":"create_incident"}]}`, "application/json"); code != http.StatusOK {
		t.Fatalf("patch tools: status=%d body=%s", code, raw)
	}
	refused, _ := startTriage(t, srv, "monitoring").Connector.Fields["refused"].(string)
	if !strings.Contains(refused, `does not permit the tool "search_issues"`) || !strings.Contains(refused, `"monitoring"`) {
		t.Errorf("after the tool left the list: refused = %q", refused)
	}
	// A Worker that does not exist.
	if r, _ := startTriage(t, srv, "andere").Connector.Fields["refused"].(string); !strings.Contains(r, `no MCP Worker named "andere"`) {
		t.Errorf("an unknown Worker: refused = %q", r)
	}
	// And one that is switched off.
	if code, raw := serveInternal(t, srv, http.MethodPatch, "/api/v1/connectors/"+id, `{"enabled":false,"tools":[{"name":"search_issues"}]}`, "application/json"); code != http.StatusOK {
		t.Fatalf("disable: status=%d body=%s", code, raw)
	}
	if r, _ := startTriage(t, srv, "monitoring").Connector.Fields["refused"].(string); !strings.Contains(r, "is disabled") {
		t.Errorf("a disabled Worker: refused = %q", r)
	}
}

// What a Worker's list may hold: named tools, each once, with the two properties
// ADR-0538 asks of every operation — the worst case for one that states neither.
func TestAnMcpWorkersListIsValidated(t *testing.T) {
	ok := createConnectorParams{Endpoint: "https://mcp.example.ch/mcp", Model: "leftover",
		Tools: []mcpToolGrant{{Name: " search "}, {Name: "write", Idempotency: "KEY", Effect: "Reversible"}}}
	if msg := validateMCPConnector(&ok); msg != "" {
		t.Fatalf("a valid Worker was refused: %s", msg)
	}
	if ok.Model != "" || ok.Tools[0] != (mcpToolGrant{Name: "search", Idempotency: "none", Effect: "irreversible"}) ||
		ok.Tools[1] != (mcpToolGrant{Name: "write", Idempotency: "key", Effect: "reversible"}) {
		t.Errorf("normalised = %+v (model %q)", ok.Tools, ok.Model)
	}
	many := make([]mcpToolGrant, maxMCPTools+1)
	for i := range many {
		many[i].Name = "t" + strings.Repeat("x", i%7) + string(rune('a'+i%26)) + strings.Repeat("y", i/26)
	}
	for name, tc := range map[string]struct {
		p    createConnectorParams
		want string
	}{
		"no endpoint":           {createConnectorParams{}, "requires an endpoint"},
		"not a URL":             {createConnectorParams{Endpoint: "mcp.example.ch"}, "not an http or https URL"},
		"a nameless tool":       {createConnectorParams{Endpoint: "https://x/mcp", Tools: []mcpToolGrant{{}}}, "needs a name"},
		"a tool twice":          {createConnectorParams{Endpoint: "https://x/mcp", Tools: []mcpToolGrant{{Name: "a"}, {Name: "a"}}}, `"a" twice`},
		"bad idempotency":       {createConnectorParams{Endpoint: "https://x/mcp", Tools: []mcpToolGrant{{Name: "a", Idempotency: "sometimes"}}}, `idempotency "sometimes"`},
		"bad effect":            {createConnectorParams{Endpoint: "https://x/mcp", Tools: []mcpToolGrant{{Name: "a", Effect: "harmless"}}}, `effect "harmless"`},
		"too long to be a list": {createConnectorParams{Endpoint: "https://x/mcp", Tools: many}, "at most"},
	} {
		if msg := validateMCPConnector(&tc.p); !strings.Contains(msg, tc.want) {
			t.Errorf("%s: %q, want a refusal naming %q", name, msg, tc.want)
		}
	}
	// The same rules hold when a Worker is created over HTTP.
	srv, _ := newValidateServer(t)
	if code, raw := serveInternal(t, srv, http.MethodPost, "/api/v1/connectors",
		`{"name":"m","kind":"mcp","endpoint":"https://x/mcp","tools":[{"name":"a","effect":"harmless"}]}`, "application/json"); code != http.StatusBadRequest {
		t.Errorf("a list with an unknown effect: status=%d body=%s", code, raw)
	}
}

// What the server hands a supervised worker is what the worker builds its servers from:
// the address and the credential — and not the list, which the server keeps and checks.
func TestSupervisedMcpEnvBuildsTheWorker(t *testing.T) {
	srv, _ := newValidateServer(t, WithSupervisedWorkers("http://s", nil, nil))
	save := func(id, name, endpoint, ref string, enabled bool) {
		if err := srv.connectors.Save(connector{ID: id, Name: name, Kind: connectorKindMCP, Endpoint: endpoint,
			CredentialsRef: ref, Enabled: enabled, CreatedAt: 1, Tools: []mcpToolGrant{{Name: "search"}}}); err != nil {
			t.Fatalf("Save %s: %v", name, err)
		}
	}
	save("1", "monitoring", "https://mcp.example.ch/mcp", "monitoring-mcp", true)
	save("2", "intern", "http://intern.example.ch/mcp", "", true) // asks for no credential
	save("3", "aus", "https://aus.example.ch/mcp", "", false)
	save("4", "kaputt", "ftp://x", "", true)
	save("5", "monitorinG", "https://zwei.example.ch/mcp", "", true) // folds to "monitoring"'s environment name, and sorts first
	t.Setenv("ATLAS_CONNECTOR_MONITORING_MCP_TOKEN", `{"token":"mcp_0123"}`)

	env := envOf(t, srv.mcpWorkerEnv())
	built, err := worker.BuiltinConnectors(func(k string) string { return env[k] }, connectorKindMCP)
	if err != nil {
		t.Fatalf("a worker could not be configured from what the engine handed it: %v", err)
	}
	// Two Workers whose names fold to one environment name: only the first is handed over.
	if !slices.Contains(built.Names, "monitorinG") || !slices.Contains(built.Names, "intern") || len(built.Names) != 2 {
		t.Errorf("the worker holds %v, want intern and the first of the two that share a name", built.Names)
	}
	if env["ATLAS_MCP_MONITORING_URL"] != "https://zwei.example.ch/mcp" || env["ATLAS_MCP_INTERN_URL"] != "http://intern.example.ch/mcp" {
		t.Errorf("handed = %v", env)
	}

	// With a credential reference, what the vault holds is handed over as it is.
	creds, _ := newValidateServer(t, WithSupervisedWorkers("http://s", nil, nil))
	if err := creds.connectors.Save(connector{ID: "1", Name: "monitoring", Kind: connectorKindMCP, Endpoint: "https://mcp.example.ch/mcp",
		CredentialsRef: "monitoring-mcp", Enabled: true, CreatedAt: 1}); err != nil {
		t.Fatalf("Save: %v", err)
	}
	if got := envOf(t, creds.mcpWorkerEnv())["ATLAS_MCP_MONITORING_CREDENTIALS"]; got != `{"token":"mcp_0123"}` {
		t.Errorf("handed credential = %q", got)
	}
	if _, has := env["ATLAS_MCP_INTERN_CREDENTIALS"]; has {
		t.Error("a server that asks for no credential was handed one")
	}
	for k, v := range env {
		if strings.Contains(k, "TOOLS") || strings.Contains(v, "search") {
			t.Errorf("the list of permitted tools was handed to the worker: %s=%s", k, v)
		}
	}
	// The kind is supervised without a flag, and with no Worker there is nothing to hand.
	if !slices.Contains(DefaultSupervisedWorkerOnlyKinds(), connectorKindMCP) {
		t.Error("the mcp kind is not supervised by default")
	}
	empty, _ := newValidateServer(t, WithSupervisedWorkers("http://s", nil, nil))
	if env := empty.mcpWorkerEnv(); env != nil {
		t.Errorf("no Worker rendered %v", env)
	}
}
