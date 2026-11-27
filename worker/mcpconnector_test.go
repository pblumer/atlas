package worker

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// mcpServer answers the two requests a call to a current-revision MCP server makes.
func mcpServer(t *testing.T, result string, seen *[]map[string]any) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		var req struct {
			ID     any            `json:"id"`
			Method string         `json:"method"`
			Params map[string]any `json:"params"`
		}
		_ = json.Unmarshal(raw, &req)
		id, _ := json.Marshal(req.ID)
		w.Header().Set("Content-Type", "application/json")
		if req.Method == "tools/list" {
			_, _ = io.WriteString(w, `{"jsonrpc":"2.0","id":`+string(id)+`,"result":{"tools":[{"name":"search_issues","inputSchema":{"type":"object"}}]}}`)
			return
		}
		*seen = append(*seen, map[string]any{"auth": r.Header.Get("Authorization"), "params": req.Params})
		_, _ = io.WriteString(w, `{"jsonrpc":"2.0","id":`+string(id)+`,"result":`+result+`}`)
	}))
	t.Cleanup(srv.Close)
	return srv
}

// A leased MCP task is worked with what the worker alone holds: the server's address
// and its credential, read from the worker's own environment (ADR-draft-mcp-worker).
func TestALeasedMcpTaskIsWorkedFromTheWorkersEnvironment(t *testing.T) {
	var seen []map[string]any
	srv := mcpServer(t, `{"content":[{"type":"text","text":"2 Treffer"}],"structuredContent":{"count":2}}`, &seen)
	env := map[string]string{
		"ATLAS_MCP_CONNECTORS":             "monitoring",
		"ATLAS_MCP_MONITORING_URL":         srv.URL,
		"ATLAS_MCP_MONITORING_CREDENTIALS": `{"token":"mcp_tok"}`,
	}
	reg, names, err := mcpRegistryFromEnv(func(k string) string { return env[k] })
	if err != nil || len(names) != 1 || names[0] != "monitoring" {
		t.Fatalf("registry = %v, %v", names, err)
	}
	job := Job{Connector: &ConnectorPayload{Kind: "mcp", Fields: map[string]any{
		"connector": "monitoring", "tool": "search_issues", "resultVariable": "treffer",
		"arguments": map[string]any{"query": "Portal langsam", "ticket": json.Number("12345678901234567890")},
	}}}
	vars, err := RunMCPJob(context.Background(), job, reg)
	if err != nil {
		t.Fatalf("RunMCPJob: %v", err)
	}
	if got, _ := json.Marshal(vars["treffer"]); string(got) != `{"count":2}` {
		t.Errorf("variable = %s, want the tool's structured result", got)
	}
	if len(seen) != 1 || seen[0]["auth"] != "Bearer mcp_tok" {
		t.Fatalf("calls = %v, want one with the credential", seen)
	}
	sent, _ := json.Marshal(seen[0]["params"].(map[string]any)["arguments"])
	if !strings.Contains(string(sent), `"query":"Portal langsam"`) {
		t.Errorf("arguments sent = %s", sent)
	}

	// A refusal the server stated when it resolved the task is honoured: no call is made.
	seen = nil
	job.Connector.Fields["refused"] = `the MCP Worker "monitoring" does not permit the tool "search_issues"`
	if _, err := RunMCPJob(context.Background(), job, reg); err == nil || !strings.Contains(err.Error(), "does not permit") {
		t.Errorf("a refused task: %v", err)
	}
	if len(seen) != 0 {
		t.Error("a refused task was called anyway")
	}
}

func TestAnMcpJobThatCannotBeWorkedSaysWhy(t *testing.T) {
	reg, _, err := mcpRegistryFromEnv(func(string) string { return "" })
	if reg != nil || err != nil {
		t.Fatalf("an unconfigured worker = %v, %v, want nothing to serve and no error", reg, err)
	}
	env := map[string]string{"ATLAS_MCP_CONNECTORS": "kaputt", "ATLAS_MCP_KAPUTT_URL": "ftp://x"}
	if _, _, err := mcpRegistryFromEnv(func(k string) string { return env[k] }); err == nil || !strings.Contains(err.Error(), `"kaputt" is not usable`) {
		t.Errorf("an unusable server: %v", err)
	}
	if _, err := RunMCPJob(context.Background(), Job{}, nil); err == nil || !strings.Contains(err.Error(), "no resolved task") {
		t.Errorf("a job without its task: %v", err)
	}
	unknown := Job{Connector: &ConnectorPayload{Kind: "mcp", Fields: map[string]any{"connector": "m", "tool": "t", "endpoint": "https://evil"}}}
	if _, err := RunMCPJob(context.Background(), unknown, nil); err == nil || !strings.Contains(err.Error(), "cannot read the resolved task") {
		t.Errorf("a task carrying a field this worker does not know: %v", err)
	}
}
