package worker_test

import (
	"context"
	"testing"
	"time"

	"github.com/pblumer/atlas/compiler"
	"github.com/pblumer/atlas/worker"
)

// agentRoundBPMN parks on an agent-driven ad-hoc subprocess: the container itself
// carries the round job, and its two unconnected activities are the tools the model is
// offered (ADR-0253). Nothing here activates on entry — which is the difference from a
// plain ad-hoc, and the reason a worker gets to decide what runs.
const agentRoundBPMN = `<?xml version="1.0" encoding="UTF-8"?>
<bpmn:definitions xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL"
                  xmlns:zeebe="http://camunda.org/schema/zeebe/1.0"
                  xmlns:atlas="http://atlas.dev/schema/1.0" id="defs-agent">
  <bpmn:process id="berater" isExecutable="true">
    <bpmn:startEvent id="s"/>
    <bpmn:adHocSubProcess id="agent">
      <bpmn:documentation>Finde den guenstigsten Hypothekarzins.</bpmn:documentation>
      <bpmn:extensionElements>
        <atlas:agentConnector connector="anthropic_pb" resultCollection="toolCallResults" resultElement="=toolCallResult"/>
      </bpmn:extensionElements>
      <bpmn:serviceTask id="zinsen_holen">
        <bpmn:documentation>Liest die Zinstabelle einer Bank.</bpmn:documentation>
        <bpmn:extensionElements>
          <zeebe:taskDefinition type="rates"/>
          <atlas:agentParam name="url" type="string" required="true" description="Die Zinsseite"/>
        </bpmn:extensionElements>
      </bpmn:serviceTask>
      <bpmn:serviceTask id="historie_lesen">
        <bpmn:extensionElements><zeebe:taskDefinition type="history"/></bpmn:extensionElements>
      </bpmn:serviceTask>
    </bpmn:adHocSubProcess>
    <bpmn:endEvent id="e"/>
    <bpmn:sequenceFlow id="f1" sourceRef="s" targetRef="agent"/>
    <bpmn:sequenceFlow id="f2" sourceRef="agent" targetRef="e"/>
  </bpmn:process>
</bpmn:definitions>`

// TestAWorkerDecidesAnAgentRoundOverTheOrdinaryProtocol is what ADR-0254 claims and
// this phase has to make true: an agent round decided outside the engine, over the
// worker protocol that exists today — no streaming, no new transport, nothing but one
// more field on the lease and one more on the completion.
//
// It runs both halves against a real Atlas. Outbound, the leased round carries the
// toolbox the worker could not have built: element ids, the modeler's documentation,
// the declared parameters. Inbound, the worker answers with a choice of activities
// rather than variables, and the engine runs the one it named.
//
// The model is a script here on purpose. What is under test is the protocol, not
// inference — connector/agent's own tests are where a decision is made.
func TestAWorkerDecidesAnAgentRoundOverTheOrdinaryProtocol(t *testing.T) {
	ts := liveAtlasWith(t, agentRoundBPMN, `{"laufzeit":10}`)

	var (
		rounds     int
		goal       string
		offered    []string
		results    []any
		toolSawURL any
	)
	w := worker.New(worker.Options{
		Server: ts.URL,
		ID:     "agent-1",
		// A poll for a type with nothing parked blocks for DefaultWait otherwise, and
		// this run polls three types per pass with only one of them ever ready.
		Wait:  50 * time.Millisecond,
		Retry: 10 * time.Millisecond,
		Handlers: map[string]worker.Exec{
			// The round: what a model would be asked, and what it answered.
			compiler.AgentJobType: worker.CompletingExecFunc(func(_ context.Context, j worker.Job) (worker.Outcome, error) {
				rounds++
				if j.Connector == nil || j.Connector.Kind != "agent" {
					t.Errorf("round %d: payload = %#v, want an agent payload", rounds, j.Connector)
					return worker.Outcome{}, nil
				}
				goal, _ = j.Connector.Fields["goal"].(string)
				offered = nil
				for _, raw := range asSlice(j.Connector.Fields["tools"]) {
					if tool, ok := raw.(map[string]any); ok {
						name, _ := tool["name"].(string)
						offered = append(offered, name)
					}
				}
				results = asSlice(j.Connector.Fields["results"])
				if rounds == 1 {
					return worker.Outcome{ToolCalls: []worker.ToolCallReport{{
						Tool:      "zinsen_holen",
						CallId:    "call_1",
						Arguments: map[string]any{"url": "https://bank.example/zinsen"},
					}}}, nil
				}
				// Round two: nothing more to call. An empty report is the ending.
				return worker.Outcome{Variables: map[string]any{"agentAnswer": "1.13%"}}, nil
			}),
			// The tool the agent chose, doing its work like any other job-worker task.
			"rates": worker.ExecFunc(func(_ context.Context, j worker.Job) (map[string]any, error) {
				toolSawURL = j.Variables["url"]
				return map[string]any{"toolCallResult": "1.13%"}, nil
			}),
			"history": worker.ExecFunc(func(_ context.Context, _ worker.Job) (map[string]any, error) {
				t.Error("historie_lesen ran; only the tool the agent called may be activated")
				return nil, nil
			}),
		},
	})

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	// Each pass leases what is parked right now; the rounds are what move the
	// container along, so a handful of passes is the whole run.
	for i := 0; i < 6 && runningInstances(t, ts) > 0; i++ {
		if err := w.RunOnce(ctx); err != nil {
			t.Fatalf("RunOnce: %v", err)
		}
	}

	if rounds < 2 {
		t.Fatalf("rounds = %d, want at least 2: the tool's result has to come back to the agent", rounds)
	}
	// Outbound: the toolbox the engine resolved, which is the half a worker cannot do
	// for itself (ADR-0168).
	if goal != "Finde den guenstigsten Hypothekarzins." {
		t.Errorf("goal = %q, want the container's own documentation", goal)
	}
	if len(offered) != 2 {
		t.Errorf("tools = %v, want both contained activities", offered)
	}
	// Inbound: the named tool ran, and with the argument the agent supplied.
	if toolSawURL != "https://bank.example/zinsen" {
		t.Errorf("the tool saw url = %#v, want the argument the agent chose for it", toolSawURL)
	}
	// And the next round was told what the call returned — the agent's memory of its
	// own run, which is why a round can build on the one before it.
	if len(results) == 0 {
		t.Errorf("the second round carried no results; the agent cannot build on a call it cannot see")
	}
	if running := runningInstances(t, ts); running != 0 {
		t.Errorf("%d instances still running, want 0 — reporting no calls is how an agent finishes", running)
	}
}

func asSlice(v any) []any {
	out, _ := v.([]any)
	return out
}
