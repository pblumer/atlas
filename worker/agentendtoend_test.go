package worker_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/pblumer/atlas/worker"
)

// ADR-0254 phase 4: one agent run, whole, over the transport that already existed.
//
// Everything before this proved a piece. Phase 1 resolved a round, phase 2 folded a
// report, phase 3 registered the Worker Type and gave it a provider. This is the claim
// the record actually makes — that an agent needs **no new transport** — and the only
// way to test a claim like that is to run the thing: a real Atlas over HTTP, a real
// `atlas worker` built by BuiltinConnectors, a real Messages-API client, and a model
// endpoint that is a stub only because the test cannot call one for real.
//
// What it walks: park on the container's round job, lease it, decide, report a tool
// call, activate the named activity, run it, drain the scope, come back for the next
// round carrying what the first call returned, and end. Nothing in that sentence is new
// protocol; that is the point.

// stubModel is a Messages-API endpoint that answers from a script and records what it
// was asked. It stands in for a provider, not for the client — the worker really does
// speak HTTP, encode the toolbox and decode the tool calls.
type stubModel struct {
	mu      sync.Mutex
	asked   []stubAsk
	answers []string
}

// stubAsk is one round as it arrived at the endpoint.
type stubAsk struct {
	Tools []struct {
		Name        string         `json:"name"`
		Description string         `json:"description"`
		InputSchema map[string]any `json:"input_schema"`
	} `json:"tools"`
	Messages []struct {
		Role    string `json:"role"`
		Content string `json:"content"`
	} `json:"messages"`
}

func (s *stubModel) start(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var ask stubAsk
		if err := json.Unmarshal(body, &ask); err != nil {
			t.Errorf("the worker sent something that is not JSON: %v", err)
		}
		s.mu.Lock()
		s.asked = append(s.asked, ask)
		n := len(s.asked)
		answer := s.answers[len(s.answers)-1]
		if n <= len(s.answers) {
			answer = s.answers[n-1]
		}
		s.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, answer)
	}))
	t.Cleanup(srv.Close)
	return srv
}

func (s *stubModel) rounds() []stubAsk {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]stubAsk(nil), s.asked...)
}

// TestAnAgentRunsEndToEndOverTheWorkerProtocol is the record's claim, executed.
func TestAnAgentRunsEndToEndOverTheWorkerProtocol(t *testing.T) {
	model := &stubModel{answers: []string{
		// Round one: call the tool, with the argument its <atlas:agentParam> declared.
		`{"stop_reason":"tool_use","content":[{"type":"tool_use","id":"call_1",
		  "name":"zinsen_holen","input":{"url":"https://bank.example/zinsen"}}]}`,
		// Round two: the work is done, so answer. An answer is how a run ends.
		`{"stop_reason":"end_turn","content":[{"type":"text",
		  "text":"Die Bank ist am günstigsten: 1.13 %."}]}`,
	}}
	endpoint := model.start(t)
	atlas := liveAtlasWith(t, agentRoundBPMN, `{"laufzeit":10}`)

	// The worker is built the way `atlas worker --handle agent` builds it: from its own
	// environment, through the registration phase 3 added. Nothing here reaches past
	// BuiltinConnectors to construct a model by hand, because a test that did would not
	// be testing the thing an operator actually runs.
	built, err := worker.BuiltinConnectors(func(k string) string {
		return map[string]string{
			"ATLAS_AGENT_CONNECTORS":            "anthropic_pb",
			"ATLAS_AGENT_ANTHROPIC_PB_ENDPOINT": endpoint.URL,
			"ATLAS_AGENT_ANTHROPIC_PB_API_KEY":  "sk-test",
		}[k]
	}, "agent")
	if err != nil {
		t.Fatalf("BuiltinConnectors: %v", err)
	}
	if len(built.Unconfigured) != 0 {
		t.Fatalf("the agent kind reported itself unconfigured (%v) though a model was configured", built.Unconfigured)
	}

	// The tool itself is an ordinary job-worker task, served by this same worker. That
	// it needs nothing special is the other half of the point: a tool is an activity.
	var toolSawURL any
	built.Handlers["rates"] = worker.ExecFunc(func(_ context.Context, j worker.Job) (map[string]any, error) {
		toolSawURL = j.Variables["url"]
		return map[string]any{"toolCallResult": "1.13 %"}, nil
	})
	built.Handlers["history"] = worker.ExecFunc(func(_ context.Context, _ worker.Job) (map[string]any, error) {
		t.Error("historie_lesen ran; only the tool the agent called may be activated")
		return nil, nil
	})

	w := worker.New(worker.Options{
		Server: atlas.URL, ID: "agent-1", Handlers: built.Handlers,
		// A poll for a type with nothing parked would otherwise block for DefaultWait,
		// and this run polls three types per pass with one of them ever ready.
		Wait: 50 * time.Millisecond, Retry: 10 * time.Millisecond,
	})

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	for i := 0; i < 8 && runningInstances(t, atlas) > 0; i++ {
		if err := w.RunOnce(ctx); err != nil {
			t.Fatalf("RunOnce: %v", err)
		}
	}

	// --- what the model was actually asked -------------------------------------
	rounds := model.rounds()
	if len(rounds) != 2 {
		t.Fatalf("rounds = %d, want 2: one to choose the tool and one to see what it returned", len(rounds))
	}

	// Outbound: the toolbox the engine resolved out of the compiled process — the half
	// a worker cannot build for itself, because it holds no compiled process at all.
	first := rounds[0]
	if len(first.Tools) != 2 {
		t.Fatalf("tools = %+v, want both contained activities", first.Tools)
	}
	byName := map[string]int{}
	for i, tool := range first.Tools {
		byName[tool.Name] = i
	}
	zinsen, ok := byName["zinsen_holen"]
	if !ok {
		t.Fatalf("tools = %+v, want one named for the activity's own BPMN id", first.Tools)
	}
	if got := first.Tools[zinsen].Description; got != "Liest die Zinstabelle einer Bank." {
		t.Errorf("description = %q, want the modeler's own <bpmn:documentation>", got)
	}
	// The declared parameter reached the model as a schema, which is what makes the
	// argument that comes back something the activated activity can be given.
	props, _ := first.Tools[zinsen].InputSchema["properties"].(map[string]any)
	if _, ok := props["url"]; !ok {
		t.Errorf("input_schema = %v, want the declared url parameter", first.Tools[zinsen].InputSchema)
	}
	if len(first.Messages) == 0 || !strings.Contains(first.Messages[0].Content, "Finde den guenstigsten Hypothekarzins.") {
		t.Errorf("first prompt = %+v, want the container's documentation as the goal", first.Messages)
	}
	if strings.Contains(first.Messages[0].Content, "round 2") {
		t.Errorf("first prompt = %q, want it to say nothing has been called yet", first.Messages[0].Content)
	}

	// The round boundary: the second round is a *new* request carrying what the first
	// round's call returned. The worker held nothing in between — the process did, which
	// is the trade ADR-0253 makes and the reason the loop is durable.
	second := rounds[1]
	if len(second.Messages) == 0 || !strings.Contains(second.Messages[0].Content, "1.13 %") {
		t.Errorf("second prompt = %+v, want the first call's result in it: the agent's memory is the instance", second.Messages)
	}

	// --- what the process actually did -----------------------------------------
	if toolSawURL != "https://bank.example/zinsen" {
		t.Errorf("the tool saw url = %#v, want the argument the model chose for it", toolSawURL)
	}
	if running := runningInstances(t, atlas); running != 0 {
		t.Errorf("%d instances still running, want 0 — the agent answered, which is how a run ends", running)
	}
	// And the answer outlived the container, which is what the process was started for
	// (ADR-0068): an agent whose conclusion died with its scope would leave the rest of
	// the model nothing to act on.
	vars := instanceVariables(t, atlas)
	if got, _ := vars["agentAnswer"].(string); !strings.Contains(got, "1.13 %") {
		t.Errorf("agentAnswer = %#v, want the agent's own conclusion on the instance", vars["agentAnswer"])
	}
}

// A model endpoint that refuses does not quietly end the run. The round fails, the
// engine retries it, and when the attempts are gone the token parks on an incident an
// operator can see — the same machinery every other kind's failure uses, which is what
// stops an unreachable provider from looking like an agent that decided it was done.
func TestAnUnreachableModelParksTheRunOnAnIncident(t *testing.T) {
	refusing := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = io.WriteString(w, `{"error":{"message":"invalid x-api-key"}}`)
	}))
	t.Cleanup(refusing.Close)
	atlas := liveAtlasWith(t, agentRoundBPMN, `{}`)

	built, err := worker.BuiltinConnectors(func(k string) string {
		return map[string]string{
			"ATLAS_AGENT_CONNECTORS":            "anthropic_pb",
			"ATLAS_AGENT_ANTHROPIC_PB_ENDPOINT": refusing.URL,
			"ATLAS_AGENT_ANTHROPIC_PB_API_KEY":  "wrong",
		}[k]
	}, "agent")
	if err != nil {
		t.Fatalf("BuiltinConnectors: %v", err)
	}
	w := worker.New(worker.Options{
		Server: atlas.URL, ID: "agent-1", Handlers: built.Handlers,
		Wait: 50 * time.Millisecond, Retry: 10 * time.Millisecond,
	})

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	// A round gets three attempts (agentRoundRetries); spend them all.
	for i := 0; i < 6; i++ {
		if err := w.RunOnce(ctx); err != nil {
			t.Fatalf("RunOnce: %v", err)
		}
	}

	var rt struct {
		Incidents []struct {
			ElementID string `json:"elementId"`
			Message   string `json:"message"`
		} `json:"incidents"`
	}
	if err := json.Unmarshal(get(t, atlas, "/api/v1/processes/1/runtime"), &rt); err != nil {
		t.Fatalf("decode runtime: %v", err)
	}
	if len(rt.Incidents) == 0 {
		t.Fatal("a model endpoint that refuses left no incident; the run would look finished")
	}
	// The status is in the message because it is what decides what the operator does:
	// 401 is a credential, 429 is capacity, 400 is us.
	if !strings.Contains(rt.Incidents[0].Message, "401") {
		t.Errorf("incident = %q, want the refusal's status in it", rt.Incidents[0].Message)
	}
	if running := runningInstances(t, atlas); running != 1 {
		t.Errorf("instances running = %d, want the one still parked on its failed round", running)
	}
}
