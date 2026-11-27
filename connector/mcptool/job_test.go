package mcptool

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"strings"
	"testing"

	"github.com/pblumer/atlas/job"
	"github.com/pblumer/atlas/workertype"
)

func registryFor(t *testing.T, f *fakeServer) *Registry {
	t.Helper()
	c, err := NewClient(f.srv.URL, `{"token":"tok"}`)
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	reg := NewRegistry()
	reg.Register("monitoring", c)
	return reg
}

// A call's result is the task's variable: the structured part when the tool gives one,
// its text otherwise, and nothing when the task names no variable. The credential goes
// with every request as a bearer token.
func TestATasksResultIsItsVariable(t *testing.T) {
	answer := `{"content":[{"type":"text","text":"3 Treffer"}],"structuredContent":{"count":3,"exact":0.10000000000000000001}}`
	f := modernServer(t, func(_ *fakeServer, w http.ResponseWriter, req rpcIn) { rpcResult(w, req.ID, answer) })
	reg := registryFor(t, f)
	base := Job{Connector: "monitoring", Tool: "get_weather", Arguments: map[string]any{"location": "Bern"}}

	withVar := base
	withVar.ResultVariable = "treffer"
	vars, err := Run(context.Background(), withVar, reg)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	got, _ := json.Marshal(vars["treffer"])
	if string(got) != `{"count":3,"exact":0.10000000000000000001}` {
		t.Errorf("variable = %s, want the structured result with its numbers exact", got)
	}
	if h := f.calls("tools/call")[0].Headers; h.Get("Authorization") != "Bearer tok" {
		t.Errorf("the credential was not presented: %v", h)
	}

	answer = `{"content":[{"type":"text","text":"3 Treffer"}]}`
	if vars, err = Run(context.Background(), withVar, reg); err != nil || vars["treffer"] != "3 Treffer" {
		t.Errorf("text result = %v, %v", vars, err)
	}
	if vars, err = Run(context.Background(), base, reg); err != nil || vars != nil {
		t.Errorf("a task that wants no result got %v, %v", vars, err)
	}
	answer = `{"content":[],"structuredContent":"nicht JSON-Objekt"}`
	if vars, err = Run(context.Background(), withVar, reg); err != nil || vars["treffer"] != "nicht JSON-Objekt" {
		t.Errorf("a structured result that is not an object = %v, %v", vars, err)
	}
}

// A tool that ran and reported a failure throws a BPMN error where the process catches
// it, and fails the job where it does not. What the tool said is the message either way.
func TestAToolsOwnFailureIsThrownWhereItIsCaught(t *testing.T) {
	answer := `{"content":[{"type":"text","text":"Kein Zugriff auf das Projekt"}],"isError":true}`
	f := modernServer(t, func(_ *fakeServer, w http.ResponseWriter, req rpcIn) { rpcResult(w, req.ID, answer) })
	reg := registryFor(t, f)
	j := Job{Connector: "monitoring", Tool: "get_weather", ResultVariable: "r"}

	_, err := Run(context.Background(), j, reg)
	var thrown *job.BPMNError
	if err == nil || errors.As(err, &thrown) || !strings.Contains(err.Error(), "Kein Zugriff auf das Projekt") {
		t.Errorf("uncaught: %v, want a failed job quoting the tool", err)
	}
	j.ErrorCodes = []string{"OTHER", CodeToolError}
	if _, err = Run(context.Background(), j, reg); !errors.As(err, &thrown) || thrown.Code != CodeToolError || thrown.Message != "Kein Zugriff auf das Projekt" {
		t.Errorf("caught: %v, want the BPMN error %s", err, CodeToolError)
	}
	answer = `{"content":[],"isError":true}`
	if _, err = Run(context.Background(), j, reg); !errors.As(err, &thrown) || !strings.Contains(thrown.Message, "said nothing") {
		t.Errorf("a silent failure: %v", err)
	}
}

// A call that must not or cannot be made is a failed job that says why, and makes no call.
func TestACallThatMustNotBeMadeIsNot(t *testing.T) {
	f := modernServer(t, func(_ *fakeServer, w http.ResponseWriter, req rpcIn) { rpcResult(w, req.ID, `{"content":[]}`) })
	reg := registryFor(t, f)
	for name, tc := range map[string]struct {
		j    Job
		want string
	}{
		"refused by the list": {Job{Connector: "monitoring", Tool: "delete_all", Refused: `the Worker "monitoring" does not permit the tool "delete_all"`}, "does not permit"},
		"no tool":             {Job{Connector: "monitoring"}, "names no tool"},
		"unknown server":      {Job{Connector: "andere", Tool: "x"}, `holds "monitoring"`},
	} {
		if _, err := Run(context.Background(), tc.j, reg); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: %v, want an error naming %q", name, err, tc.want)
		}
	}
	if n := len(f.calls("tools/call")) + len(f.calls("tools/list")); n != 0 {
		t.Errorf("%d requests were made", n)
	}
	// A bound on the call ends one that does not come back.
	slow := &Client{Endpoint: "http://10.255.255.1:9/mcp"}
	reg.Register("slow", slow)
	if _, err := Run(context.Background(), Job{Connector: "slow", Tool: "x", TimeoutMs: 30}, reg); err == nil {
		t.Error("a call past its timeout answered")
	}
}

func TestAClientIsBuiltFromWhatAWorkerRecordHolds(t *testing.T) {
	for _, endpoint := range []string{"", "mcp.example.ch", "ftp://x/y", "https://"} {
		if _, err := NewClient(endpoint, ""); err == nil {
			t.Errorf("NewClient(%q) accepted an endpoint that is no http(s) URL", endpoint)
		}
	}
	if _, err := NewClient("https://mcp.example.ch/mcp", "{broken"); err == nil {
		t.Error("a bundle that is not JSON was accepted")
	}
	for creds, want := range map[string]string{"": "", "tok": "Bearer tok", `{"token":" tok "}`: "Bearer tok", `{"token":""}`: ""} {
		c, err := NewClient("https://mcp.example.ch/mcp", creds)
		if err != nil {
			t.Fatalf("NewClient(%q): %v", creds, err)
		}
		h := http.Header{}
		if c.Authorize != nil {
			_ = c.Authorize(h)
		}
		if h.Get("Authorization") != want {
			t.Errorf("credentials %q present %q, want %q", creds, h.Get("Authorization"), want)
		}
	}
}

// The manifest declares the one thing the type does, at the worst case: the type cannot
// know what a server's tools do, so what a second delivery does is stated per tool on
// each Worker's list and not here (ADR-0538).
func TestTheManifestDeclaresTheWorstCase(t *testing.T) {
	raw, err := os.ReadFile("manifest.json")
	if err != nil {
		t.Fatal(err)
	}
	m, err := workertype.ParseJSON(raw)
	if err != nil {
		t.Fatalf("ParseJSON: %v", err)
	}
	ops := m.Spec.Operations
	if len(ops) != 1 || ops[0].ID != "call-tool" || ops[0].Idempotency != "none" || ops[0].Effect != "irreversible" {
		t.Errorf("operations = %+v", ops)
	}
}
