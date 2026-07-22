package mcp_test

import (
	"bufio"
	"encoding/json"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pblumer/atlas/api"
	"github.com/pblumer/atlas/engine"
	"github.com/pblumer/atlas/mcp"
	"github.com/pblumer/atlas/state"
	"github.com/pblumer/atlas/wal"
)

const sampleBPMN = `<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL"
                    xmlns:zeebe="http://camunda.org/schema/zeebe/1.0">
  <process id="order" isExecutable="true">
    <startEvent id="start"/>
    <serviceTask id="task">
      <extensionElements><zeebe:taskDefinition type="payment" retries="5"/></extensionElements>
    </serviceTask>
    <endEvent id="end"/>
    <sequenceFlow id="f1" sourceRef="start" targetRef="task"/>
    <sequenceFlow id="f2" sourceRef="task" targetRef="end"/>
  </process>
</definitions>`

// newAtlas wires a real wal+state+engine behind the API and returns an httptest
// server, mirroring api/server_test.go's setup.
func newAtlas(t *testing.T) *httptest.Server {
	t.Helper()
	dir := t.TempDir()
	wl, err := wal.Open(wal.Options{Dir: filepath.Join(dir, "wal")})
	if err != nil {
		t.Fatalf("wal.Open: %v", err)
	}
	store, err := state.Open(filepath.Join(dir, "state"))
	if err != nil {
		t.Fatalf("state.Open: %v", err)
	}
	proc := engine.New(1, wl, store, nil)
	if err := proc.Recover(); err != nil {
		t.Fatalf("Recover: %v", err)
	}
	srv, err := api.New(proc, store, filepath.Join(dir, "deployments"))
	if err != nil {
		t.Fatalf("api.New: %v", err)
	}
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(func() {
		ts.Close()
		srv.Close()
		_ = store.Close()
		_ = wl.Close()
	})
	return ts
}

// run feeds the given newline-delimited requests through a fresh server and
// returns the responses in order. Notifications produce no response, so the
// count of returned responses may be smaller than the request count.
func run(t *testing.T, ts *httptest.Server, requests ...string) []map[string]any {
	t.Helper()
	srv := mcp.NewServer(mcp.NewClient(ts.URL))
	var out strings.Builder
	in := strings.NewReader(strings.Join(requests, "\n") + "\n")
	if err := srv.Serve(in, &out); err != nil {
		t.Fatalf("Serve: %v", err)
	}
	var resps []map[string]any
	sc := bufio.NewScanner(strings.NewReader(out.String()))
	sc.Buffer(make([]byte, 0, 64<<10), 16<<20)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		var m map[string]any
		if err := json.Unmarshal([]byte(line), &m); err != nil {
			t.Fatalf("decode response %q: %v", line, err)
		}
		resps = append(resps, m)
	}
	return resps
}

// result returns the "result" object of a response, failing on a JSON-RPC error.
func result(t *testing.T, resp map[string]any) map[string]any {
	t.Helper()
	if e, ok := resp["error"]; ok {
		t.Fatalf("unexpected JSON-RPC error: %v", e)
	}
	r, ok := resp["result"].(map[string]any)
	if !ok {
		t.Fatalf("response has no result object: %v", resp)
	}
	return r
}

// toolText returns the concatenated text content of a tools/call result and its
// isError flag.
func toolText(t *testing.T, res map[string]any) (string, bool) {
	t.Helper()
	isErr, _ := res["isError"].(bool)
	content, ok := res["content"].([]any)
	if !ok {
		t.Fatalf("result has no content array: %v", res)
	}
	var b strings.Builder
	for _, c := range content {
		m, _ := c.(map[string]any)
		if s, ok := m["text"].(string); ok {
			b.WriteString(s)
		}
	}
	return b.String(), isErr
}

func TestInitializeHandshake(t *testing.T) {
	ts := newAtlas(t)
	resps := run(t, ts,
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-03-26","capabilities":{},"clientInfo":{"name":"test","version":"0"}}}`,
		`{"jsonrpc":"2.0","method":"notifications/initialized"}`,
	)
	// The notification produces no response.
	if len(resps) != 1 {
		t.Fatalf("got %d responses, want 1 (notification must not be answered)", len(resps))
	}
	res := result(t, resps[0])
	if got := res["protocolVersion"]; got != "2025-03-26" {
		t.Fatalf("protocolVersion = %v, want echo of 2025-03-26", got)
	}
	caps, ok := res["capabilities"].(map[string]any)
	if !ok || caps["tools"] == nil {
		t.Fatalf("capabilities missing tools: %v", res)
	}
	info, ok := res["serverInfo"].(map[string]any)
	if !ok || info["name"] != "atlas-mcp" {
		t.Fatalf("serverInfo = %v, want name atlas-mcp", res["serverInfo"])
	}
}

func TestInitializeUnknownVersionFallsBack(t *testing.T) {
	ts := newAtlas(t)
	resps := run(t, ts,
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"1999-01-01"}}`,
	)
	res := result(t, resps[0])
	if got := res["protocolVersion"]; got != "2025-06-18" {
		t.Fatalf("protocolVersion = %v, want default 2025-06-18 for unknown request", got)
	}
}

func TestToolsListExposesAtlasTools(t *testing.T) {
	ts := newAtlas(t)
	resps := run(t, ts, `{"jsonrpc":"2.0","id":2,"method":"tools/list"}`)
	res := result(t, resps[0])
	tools, ok := res["tools"].([]any)
	if !ok || len(tools) == 0 {
		t.Fatalf("tools/list returned no tools: %v", res)
	}
	names := map[string]bool{}
	for _, tl := range tools {
		m := tl.(map[string]any)
		names[m["name"].(string)] = true
		if _, ok := m["inputSchema"].(map[string]any); !ok {
			t.Fatalf("tool %v missing inputSchema", m["name"])
		}
	}
	for _, want := range []string{
		"atlas_info", "atlas_deploy", "atlas_list_processes", "atlas_get_process_xml",
		"atlas_process_runtime", "atlas_create_instance", "atlas_list_instances", "atlas_stats",
	} {
		if !names[want] {
			t.Fatalf("tools/list missing %q; got %v", want, names)
		}
	}
}

// TestDeployRunInspectViaTools exercises the full lifecycle through MCP tool
// calls against a real engine: deploy, list, start an instance, and read the
// runtime overlay.
func TestDeployRunInspectViaTools(t *testing.T) {
	ts := newAtlas(t)

	deployReq := map[string]any{
		"jsonrpc": "2.0", "id": 1, "method": "tools/call",
		"params": map[string]any{
			"name":      "atlas_deploy",
			"arguments": map[string]any{"xml": sampleBPMN},
		},
	}
	dep, _ := json.Marshal(deployReq)
	resps := run(t, ts, string(dep))
	text, isErr := toolText(t, result(t, resps[0]))
	if isErr {
		t.Fatalf("deploy reported error: %s", text)
	}
	var deployed struct {
		Key       uint64 `json:"key"`
		ProcessID string `json:"processId"`
		Version   int32  `json:"version"`
	}
	if err := json.Unmarshal([]byte(text), &deployed); err != nil {
		t.Fatalf("decode deploy text %q: %v", text, err)
	}
	if deployed.ProcessID != "order" || deployed.Version != 1 {
		t.Fatalf("deploy = %+v, want order/1", deployed)
	}

	// Start an instance of the freshly deployed definition.
	callInstance := `{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"atlas_create_instance","arguments":{"key":` +
		itoa(deployed.Key) + `}}}`
	// Read the runtime overlay.
	callRuntime := `{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"atlas_process_runtime","arguments":{"key":` +
		itoa(deployed.Key) + `}}}`
	resps = run(t, ts,
		string(dep), // redeploy on the fresh server this run wires up
		callInstance,
		callRuntime,
	)
	// resps[0] deploy, resps[1] instance, resps[2] runtime.
	instText, isErr := toolText(t, result(t, resps[1]))
	if isErr {
		t.Fatalf("create_instance error: %s", instText)
	}
	if !strings.Contains(instText, `"activeProcessInstances":1`) {
		t.Fatalf("create_instance stats = %s, want 1 active process instance", instText)
	}
	rtText, isErr := toolText(t, result(t, resps[2]))
	if isErr {
		t.Fatalf("process_runtime error: %s", rtText)
	}
	if !strings.Contains(rtText, `"elementId":"task"`) || !strings.Contains(rtText, `"tokens":1`) {
		t.Fatalf("runtime = %s, want a token parked on service task \"task\"", rtText)
	}
}

func TestDeployInvalidModelIsToolError(t *testing.T) {
	ts := newAtlas(t)
	const bad = `<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL"><process id="p"><endEvent id="e"/></process></definitions>`
	req := map[string]any{
		"jsonrpc": "2.0", "id": 1, "method": "tools/call",
		"params": map[string]any{"name": "atlas_deploy", "arguments": map[string]any{"xml": bad}},
	}
	b, _ := json.Marshal(req)
	resps := run(t, ts, string(b))
	text, isErr := toolText(t, result(t, resps[0]))
	if !isErr {
		t.Fatalf("invalid model should be a tool error, got success: %s", text)
	}
	if !strings.Contains(text, "atlas server error") {
		t.Fatalf("tool error text = %q, want the server's rejection", text)
	}
}

func TestUnknownToolIsInvalidParams(t *testing.T) {
	ts := newAtlas(t)
	resps := run(t, ts, `{"jsonrpc":"2.0","id":7,"method":"tools/call","params":{"name":"nope","arguments":{}}}`)
	if _, ok := resps[0]["error"].(map[string]any); !ok {
		t.Fatalf("unknown tool should yield a JSON-RPC error, got %v", resps[0])
	}
}

func TestMissingRequiredArgumentIsToolError(t *testing.T) {
	ts := newAtlas(t)
	resps := run(t, ts, `{"jsonrpc":"2.0","id":8,"method":"tools/call","params":{"name":"atlas_deploy","arguments":{}}}`)
	text, isErr := toolText(t, result(t, resps[0]))
	if !isErr || !strings.Contains(text, "missing required argument: xml") {
		t.Fatalf("missing arg = (%q, isErr=%v), want a missing-argument tool error", text, isErr)
	}
}

func TestUnknownMethodIsMethodNotFound(t *testing.T) {
	ts := newAtlas(t)
	resps := run(t, ts, `{"jsonrpc":"2.0","id":9,"method":"does/not/exist"}`)
	e, ok := resps[0]["error"].(map[string]any)
	if !ok {
		t.Fatalf("unknown method should yield an error, got %v", resps[0])
	}
	if code, _ := e["code"].(float64); int(code) != -32601 {
		t.Fatalf("error code = %v, want -32601 (method not found)", e["code"])
	}
}

func TestPing(t *testing.T) {
	ts := newAtlas(t)
	resps := run(t, ts, `{"jsonrpc":"2.0","id":"p","method":"ping"}`)
	res := result(t, resps[0])
	if len(res) != 0 {
		t.Fatalf("ping result = %v, want empty object", res)
	}
}

func TestParseErrorGetsNullIDError(t *testing.T) {
	ts := newAtlas(t)
	resps := run(t, ts, `{not json`)
	e, ok := resps[0]["error"].(map[string]any)
	if !ok {
		t.Fatalf("malformed line should yield a parse error, got %v", resps[0])
	}
	if code, _ := e["code"].(float64); int(code) != -32700 {
		t.Fatalf("error code = %v, want -32700 (parse error)", e["code"])
	}
	if resps[0]["id"] != nil {
		t.Fatalf("parse-error id = %v, want null", resps[0]["id"])
	}
}

// itoa is a tiny uint64→string helper for building request literals.
func itoa(u uint64) string {
	return strings.TrimSpace(jsonNumber(u))
}

func jsonNumber(u uint64) string {
	b, _ := json.Marshal(u)
	return string(b)
}
