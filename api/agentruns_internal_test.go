package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/pblumer/atlas/compiler"
	"github.com/pblumer/atlas/model"
)

// storedAgentRuns reads the facts as the store holds them, which is what a test about
// what was written has to look at; the route is read separately.
func storedAgentRuns(t *testing.T, srv *Server) []model.AgentRunValue {
	t.Helper()
	var out []model.AgentRunValue
	srv.do(func() {
		_ = srv.store.EachAgentRun(func(_ int64, _ uint64, v *model.AgentRunValue) error {
			out = append(out, *v)
			return nil
		})
	})
	return out
}

func agentRunsRoute(t *testing.T, srv *Server, piKey uint64) []agentRunView {
	t.Helper()
	code, raw := serveInternal(t, srv, http.MethodGet, fmt.Sprintf("/api/v1/instances/%d/agent-runs", piKey), "", "")
	if code != http.StatusOK {
		t.Fatalf("agent-runs: status=%d body=%s", code, raw)
	}
	var out []agentRunView
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("decode agent-runs: %v (%s)", err, raw)
	}
	return out
}

// A worker's account of a round is kept with the round and read back from the instance:
// the numbers as reported, the element and the round as the engine states them
// (ADR-0566).
func TestAnAgentRoundsAccountIsKeptAndReadBack(t *testing.T) {
	srv, round := startAgentRound(t)
	code, raw := completeJob(t, srv, round, fmt.Sprintf(`{"worker":"w1","leaseToken":%d,
	  "toolCalls":[{"tool":"zinsen_holen","callId":"call-1","arguments":{"url":"https://bank.example"}}],
	  "agentRun":{"model":"claude-opus-5-5","stopReason":"tool_use","inputTokens":1040,"outputTokens":25,
	              "cacheReadTokens":900,"cacheWriteTokens":100,"durationMs":4200,
	              "system":"S","prompt":"Goal: Finde den Zins","answer":"Ich lese die Tabelle."}}`, round.LeaseToken))
	if code != http.StatusOK && code != http.StatusNoContent {
		t.Fatalf("complete: status=%d body=%s", code, raw)
	}
	stored := storedAgentRuns(t, srv)
	if len(stored) != 1 {
		t.Fatalf("stored %d facts, want the one call", len(stored))
	}
	got := agentRunsRoute(t, srv, stored[0].ProcessInstanceKey)
	if len(got) != 1 {
		t.Fatalf("route answered %d facts: %+v", len(got), got)
	}
	v := got[0]
	if v.Kind != "modelCall" || v.ElementID != "agent" || v.Round != 1 || v.JobKey != fmt.Sprint(round.Key) {
		t.Errorf("identity = %+v, want round 1 of the container, on the leased job", v)
	}
	if v.Model != "claude-opus-5-5" || v.StopReason != "tool_use" || v.InputTokens != 1040 || v.OutputTokens != 25 ||
		v.CacheReadTokens != 900 || v.CacheWriteTokens != 100 || v.DurationMs != 4200 ||
		v.TotalInputTokens != 1040 || v.TotalOutputTokens != 25 || v.Limit != "" {
		t.Errorf("numbers = %+v", v)
	}
	if v.Text != "clear" || v.System != "S" || v.Prompt != "Goal: Finde den Zins" || v.Answer != "Ich lese die Tabelle." {
		t.Errorf("texts = %+v", v)
	}
	if string(v.ToolCalls) != `[{"tool":"zinsen_holen","callId":"call-1"}]` {
		t.Errorf("tool calls = %s, want the names without the arguments", v.ToolCalls)
	}

	// An instance that asked no model has an empty list, not an error.
	if none := agentRunsRoute(t, srv, 1); len(none) != 0 {
		t.Errorf("an unknown instance answered %+v", none)
	}
	if code, _ := serveInternal(t, srv, http.MethodGet, "/api/v1/instances/x/agent-runs", "", ""); code != http.StatusBadRequest {
		t.Errorf("a key that is not a number: status=%d, want 400", code)
	}
}

// A count no call could have is a broken worker's, and is not allowed to wrap the sums.
func TestAnImpossibleTokenCountIsBounded(t *testing.T) {
	srv, round := startAgentRound(t)
	if code, raw := completeJob(t, srv, round, fmt.Sprintf(
		`{"worker":"w1","leaseToken":%d,"agentRun":{"inputTokens":18446744073709551615,"outputTokens":18446744073709551615}}`,
		round.LeaseToken)); code != http.StatusOK && code != http.StatusNoContent {
		t.Fatalf("complete: status=%d body=%s", code, raw)
	}
	stored := storedAgentRuns(t, srv)
	if len(stored) != 1 || stored[0].InputTokens != maxReportedTokens || stored[0].SumOutputTokens != maxReportedTokens {
		t.Errorf("stored = %+v, want the counts bounded", stored)
	}
	if code, _ := completeJob(t, srv, round, `{"worker":"w1","leaseToken":1,"agentRun":"nonsense"}`); code != http.StatusBadRequest {
		t.Errorf("an account that is not an object: status=%d, want 400", code)
	}
}

const boundedAgentBPMN = `<?xml version="1.0" encoding="UTF-8"?>
<bpmn:definitions xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL"
                  xmlns:zeebe="http://camunda.org/schema/zeebe/1.0"
                  xmlns:atlas="http://atlas/schema/1.0" id="defs-bounded">
  <bpmn:process id="bounded" isExecutable="true">
    <bpmn:startEvent id="s"/>
    <bpmn:adHocSubProcess id="agent">
      <bpmn:documentation>Finde den Zins.</bpmn:documentation>
      <bpmn:extensionElements>
        <atlas:agentConnector connector="anthropic_pb" maxIterations="4" timeout="PT30S"/>
      </bpmn:extensionElements>
      <bpmn:serviceTask id="zinsen_holen">
        <bpmn:documentation>Liest die Zinstabelle.</bpmn:documentation>
        <bpmn:extensionElements><zeebe:taskDefinition type="rates"/></bpmn:extensionElements>
      </bpmn:serviceTask>
    </bpmn:adHocSubProcess>
    <bpmn:endEvent id="e"/>
    <bpmn:sequenceFlow id="f1" sourceRef="s" targetRef="agent"/>
    <bpmn:sequenceFlow id="f2" sourceRef="agent" targetRef="e"/>
  </bpmn:process>
</bpmn:definitions>`

func deployAndStartWith(t *testing.T, srv *Server, bpmn, variables string) {
	t.Helper()
	if code, raw := serveInternal(t, srv, http.MethodPost, "/api/v1/deployments", bpmn, "application/xml"); code != http.StatusOK && code != http.StatusCreated {
		t.Fatalf("deploy: status=%d body=%s", code, raw)
	}
	if code, raw := serveInternal(t, srv, http.MethodPost, "/api/v1/processes/1/instances",
		`{"variables":`+variables+`}`, "application/json"); code != http.StatusOK && code != http.StatusCreated {
		t.Fatalf("create instance: status=%d body=%s", code, raw)
	}
}

// The bound a container states on one call reaches the worker with the round.
func TestARoundsAuthoredTimeoutReachesTheWorker(t *testing.T) {
	srv, _ := newValidateServer(t)
	deployAndStartWith(t, srv, boundedAgentBPMN, `{}`)
	round := lease(t, srv, compiler.AgentJobType)
	if round.Connector == nil || round.Connector.Fields["timeoutMs"] != float64(30000) {
		t.Fatalf("payload = %+v, want timeoutMs 30000", round.Connector)
	}
}

const personalAiTaskBPMN = `<?xml version="1.0" encoding="UTF-8"?>
<bpmn:definitions xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL"
                  xmlns:atlas="http://atlas/schema/1.0" id="defs-personal-ai">
  <bpmn:process id="brief" isExecutable="true" atlas:personal="name" atlas:dataSubject="kunde">
    <bpmn:startEvent id="s"/>
    <bpmn:serviceTask id="schreiben">
      <bpmn:extensionElements>
        <atlas:agentConnector connector="anthropic_pb" prompt="Schreibe einen Gruss" resultVariable="gruss"/>
      </bpmn:extensionElements>
    </bpmn:serviceTask>
    <bpmn:endEvent id="e"/>
    <bpmn:sequenceFlow id="f1" sourceRef="s" targetRef="schreiben"/>
    <bpmn:sequenceFlow id="f2" sourceRef="schreiben" targetRef="e"/>
  </bpmn:process>
</bpmn:definitions>`

const personalAccount = `"agentRun":{"model":"m","inputTokens":7,"outputTokens":3,"system":"S",
  "prompt":"Schreibe an Frau Muster","answer":"Liebe Frau Muster"}`

// Where a process declares personal data, the texts of a model call are stored under the
// data subject's key, so erasing the subject reaches them — and the numbers stay
// (ADR-0314, ADR-0566).
func TestAPersonalProcessKeepsACallsTextsUnderTheSubjectsKey(t *testing.T) {
	srv := newVaultServer(t)
	deployAndStartWith(t, srv, personalAiTaskBPMN, `{"kunde":"k-1","name":"Muster"}`)
	task := lease(t, srv, compiler.AiTaskJobType)
	if code, raw := completeJob(t, srv, task, fmt.Sprintf(`{"worker":"w1","leaseToken":%d,"variables":{"gruss":"Hallo"},%s}`,
		task.LeaseToken, personalAccount)); code != http.StatusOK && code != http.StatusNoContent {
		t.Fatalf("complete: status=%d body=%s", code, raw)
	}
	stored := storedAgentRuns(t, srv)
	if len(stored) != 1 || stored[0].TextState != model.AgentRunTextEnciphered {
		t.Fatalf("stored = %+v, want one enciphered fact", stored)
	}
	if strings.Contains(stored[0].Prompt+stored[0].Answer, "Muster") {
		t.Fatalf("the stored texts are readable: %q / %q", stored[0].Prompt, stored[0].Answer)
	}
	if stored[0].System != "S" || stored[0].InputTokens != 7 {
		t.Errorf("stored = %+v, want the standing instruction and the numbers kept", stored[0])
	}

	got := agentRunsRoute(t, srv, stored[0].ProcessInstanceKey)
	if len(got) != 1 || got[0].Text != "clear" || got[0].Prompt != "Schreibe an Frau Muster" || got[0].Answer != "Liebe Frau Muster" {
		t.Fatalf("route = %+v, want the texts opened for a reader", got)
	}

	if err := srv.vault.Erase("k-1"); err != nil {
		t.Fatalf("Erase: %v", err)
	}
	got = agentRunsRoute(t, srv, stored[0].ProcessInstanceKey)
	if len(got) != 1 || got[0].Text != "erased" || got[0].Prompt != "" || got[0].Answer != "" || got[0].InputTokens != 7 {
		t.Errorf("after the erasure the route = %+v, want the texts gone and the numbers left", got)
	}
}

// And where they cannot be enciphered — no data subject is known for the instance — they
// are not stored at all, rather than stored readable.
func TestAPersonalProcessWithoutASubjectWithholdsACallsTexts(t *testing.T) {
	srv := newVaultServer(t)
	deployAndStartWith(t, srv, personalAiTaskBPMN, `{}`)
	task := lease(t, srv, compiler.AiTaskJobType)
	if code, raw := completeJob(t, srv, task, fmt.Sprintf(`{"worker":"w1","leaseToken":%d,"variables":{"gruss":"Hallo"},%s}`,
		task.LeaseToken, personalAccount)); code != http.StatusOK && code != http.StatusNoContent {
		t.Fatalf("complete: status=%d body=%s", code, raw)
	}
	stored := storedAgentRuns(t, srv)
	if len(stored) != 1 || stored[0].TextState != model.AgentRunTextWithheld || stored[0].Prompt != "" || stored[0].Answer != "" {
		t.Fatalf("stored = %+v, want the texts withheld", stored)
	}
	got := agentRunsRoute(t, srv, stored[0].ProcessInstanceKey)
	if len(got) != 1 || got[0].Text != "withheld" || got[0].OutputTokens != 3 {
		t.Errorf("route = %+v, want the texts withheld and the numbers left", got)
	}
}

// From its second round on, a round is resolved with the run as it happened: the model's
// own earlier turn, what its call returned, the round's real number, and only the context
// that changed (ADR-0567).
func TestASecondRoundIsResolvedWithTheRunsTranscript(t *testing.T) {
	srv, round := startAgentRound(t)
	if round.Connector.Fields["history"] != nil || round.Connector.Fields["round"] != float64(1) {
		t.Fatalf("round 1 payload = %v, want no history", round.Connector.Fields)
	}
	turn := `[{"type":"tool_use","id":"toolu_1","name":"zinsen_holen","input":{"url":"https://bank.example"}}]`
	body, _ := json.Marshal(map[string]any{
		"worker": "w1", "leaseToken": round.LeaseToken,
		"toolCalls": []map[string]any{{"tool": "zinsen_holen", "callId": "toolu_1", "arguments": map[string]any{"url": "https://bank.example"}}},
		"agentRun": map[string]any{"prompt": "Goal: Finde den Zins", "wire": "messages", "turn": turn,
			"context": map[string]string{"laufzeit": "10"}},
	})
	if code, raw := completeJob(t, srv, round, string(body)); code != http.StatusOK && code != http.StatusNoContent {
		t.Fatalf("complete round 1: status=%d body=%s", code, raw)
	}
	tool := lease(t, srv, "rates")
	if code, raw := completeJob(t, srv, tool, fmt.Sprintf(`{"worker":"w1","leaseToken":%d,"variables":{"toolCallResult":"1.45 %%"}}`,
		tool.LeaseToken)); code != http.StatusOK && code != http.StatusNoContent {
		t.Fatalf("complete tool: status=%d body=%s", code, raw)
	}

	second := lease(t, srv, compiler.AgentJobType)
	fields := second.Connector.Fields
	history, _ := fields["history"].([]any)
	if fields["round"] != float64(2) || len(history) != 1 {
		t.Fatalf("round 2 payload = %v, want round 2 with one past round", fields)
	}
	past := history[0].(map[string]any)
	results, _ := past["results"].([]any)
	if past["wire"] != "messages" || past["turn"] != turn || past["said"] != "Goal: Finde den Zins" || len(results) != 1 {
		t.Fatalf("past round = %v", past)
	}
	if r := results[0].(map[string]any); r["callId"] != "toolu_1" || r["tool"] != "zinsen_holen" || r["text"] != "1.45 %" {
		t.Errorf("result = %v, want the call's own, by id", r)
	}
	if fields["changed"] != nil {
		t.Errorf("changed = %v, want nothing: no context is authored on this container", fields["changed"])
	}

	// The record holds the turn and what the tool returned, and the route shows the
	// instance's facts in the order they happened.
	stored := storedAgentRuns(t, srv)
	got := agentRunsRoute(t, srv, stored[0].ProcessInstanceKey)
	if len(got) != 2 || got[0].Kind != "modelCall" || got[1].Kind != "toolResult" || got[1].CallID != "toolu_1" ||
		got[1].Answer != "1.45 %" || got[1].ElementID != "zinsen_holen" || got[1].Round != 1 {
		t.Errorf("route = %+v", got)
	}
}

const structuredAiTaskBPMN = `<?xml version="1.0" encoding="UTF-8"?>
<bpmn:definitions xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL"
                  xmlns:atlas="http://atlas/schema/1.0" id="defs-structured">
  <bpmn:process id="tragbarkeit" isExecutable="true">
    <bpmn:startEvent id="s"/>
    <bpmn:serviceTask id="pruefen">
      <bpmn:extensionElements>
        <atlas:agentConnector connector="anthropic_pb" prompt="Ist das tragbar?" resultVariable="urteil"/>
        <atlas:agentOutput name="entscheid" type="string" required="true" values="ja,nein"/>
      </bpmn:extensionElements>
    </bpmn:serviceTask>
    <bpmn:endEvent id="e"/>
    <bpmn:sequenceFlow id="f1" sourceRef="s" targetRef="pruefen"/>
    <bpmn:sequenceFlow id="f2" sourceRef="pruefen" targetRef="e"/>
  </bpmn:process>
</bpmn:definitions>`

// The fields an answer must carry reach the worker with the task.
func TestDeclaredAnswerFieldsReachTheWorker(t *testing.T) {
	srv, _ := newValidateServer(t)
	deployAndStartWith(t, srv, structuredAiTaskBPMN, `{}`)
	task := lease(t, srv, compiler.AiTaskJobType)
	outputs, _ := task.Connector.Fields["outputs"].([]any)
	if len(outputs) != 1 {
		t.Fatalf("payload = %v, want the declared field", task.Connector.Fields)
	}
	if o := outputs[0].(map[string]any); o["name"] != "entscheid" || o["type"] != "string" || o["required"] != true ||
		len(o["values"].([]any)) != 2 {
		t.Errorf("output = %v", o)
	}
}

// A sealed turn is opened for the round it is replayed in, and a run whose key is gone
// is counted but no longer replayed.
func TestASealedTurnIsOpenedForReplayAndLostWithItsKey(t *testing.T) {
	srv := newVaultServer(t)
	clear := &model.AgentRunValue{Prompt: "p", Turn: "t", Context: `{"a":"b"}`}
	if p, tu, c, ok := srv.openAgentRunForReplay(clear); !ok || p != "p" || tu != "t" || c != `{"a":"b"}` {
		t.Errorf("a clear record = %q %q %q %v", p, tu, c, ok)
	}
	sealed := &model.AgentRunValue{Prompt: "Frau Muster", Turn: `[{"type":"text"}]`, Context: `{"name":"Muster"}`}
	srv.sealAgentRunTexts(sealed, "k-9")
	if sealed.TextState != model.AgentRunTextEnciphered || strings.Contains(sealed.Turn+sealed.Context, "Muster") {
		t.Fatalf("sealed = %+v, want the turn and the context enciphered too", sealed)
	}
	if p, tu, c, ok := srv.openAgentRunForReplay(sealed); !ok || p != "Frau Muster" || tu != `[{"type":"text"}]` || c != `{"name":"Muster"}` {
		t.Errorf("opened = %q %q %q %v", p, tu, c, ok)
	}
	if err := srv.vault.Erase("k-9"); err != nil {
		t.Fatalf("Erase: %v", err)
	}
	if _, _, _, ok := srv.openAgentRunForReplay(sealed); ok {
		t.Error("a run whose key was destroyed is still replayable")
	}
	withheld := &model.AgentRunValue{Prompt: "p", Turn: "t"}
	srv.sealAgentRunTexts(withheld, "")
	if _, _, _, ok := srv.openAgentRunForReplay(withheld); ok || withheld.Turn != "" {
		t.Errorf("a withheld run = %+v, replayable=%v", withheld, ok)
	}
}

// A bound a call reached is on its fact, and the route names it — the round bound and
// the token budget each in its own word.
func TestTheRouteNamesTheBoundACallReached(t *testing.T) {
	for want, attrs := range map[string]string{
		"rounds": `maxIterations="1"`,
		"tokens": `maxIterations="9" tokenBudget="10"`,
	} {
		t.Run(want, func(t *testing.T) {
			srv, _ := newValidateServer(t)
			deployAndStartWith(t, srv, strings.Replace(boundedAgentBPMN, `maxIterations="4"`, attrs, 1), `{}`)
			round := lease(t, srv, compiler.AgentJobType)
			if code, raw := completeJob(t, srv, round, fmt.Sprintf(`{"worker":"w1","leaseToken":%d,
			  "toolCalls":[{"tool":"zinsen_holen","callId":"c1"}],"agentRun":{"inputTokens":20,"outputTokens":5}}`, round.LeaseToken)); code != http.StatusOK && code != http.StatusNoContent {
				t.Fatalf("complete: status=%d body=%s", code, raw)
			}
			stored := storedAgentRuns(t, srv)
			got := agentRunsRoute(t, srv, stored[0].ProcessInstanceKey)
			if len(got) != 1 || got[0].Limit != want {
				t.Fatalf("route = %+v, want the bound %q", got, want)
			}
			// The tool finishes, the scope drains, and the container parks instead of
			// asking again.
			tool := lease(t, srv, "rates")
			if code, raw := completeJob(t, srv, tool, fmt.Sprintf(`{"worker":"w1","leaseToken":%d}`, tool.LeaseToken)); code != http.StatusOK && code != http.StatusNoContent {
				t.Fatalf("complete tool: status=%d body=%s", code, raw)
			}
			parked := false
			for _, inc := range incidents(t, srv) {
				parked = parked || inc.Reason == model.IncidentAgentLimit
			}
			if !parked {
				t.Error("the container did not park at its bound")
			}
			// It is listed as an agent's incident, and a filter on that kind finds it.
			code, raw := serveInternal(t, srv, http.MethodGet, "/api/v1/incidents?type=agent", "", "")
			if code != http.StatusOK || !strings.Contains(string(raw), `"type":"agent"`) || !strings.Contains(string(raw), "1 round") {
				t.Errorf("incidents?type=agent: status=%d body=%s", code, raw)
			}
		})
	}
}

// An account of a model call on a job that asked no model is not kept: a worker of
// another kind cannot write agent history by sending one.
func TestAnAccountOnAJobThatAskedNoModelIsNotKept(t *testing.T) {
	srv, round := startAgentRound(t)
	if code, raw := completeJob(t, srv, round, fmt.Sprintf(`{"worker":"w1","leaseToken":%d,
	  "toolCalls":[{"tool":"zinsen_holen","callId":"c1"}]}`, round.LeaseToken)); code != http.StatusOK && code != http.StatusNoContent {
		t.Fatalf("complete round: status=%d body=%s", code, raw)
	}
	tool := lease(t, srv, "rates")
	if code, raw := completeJob(t, srv, tool, fmt.Sprintf(`{"worker":"w1","leaseToken":%d,
	  "agentRun":{"model":"forged","inputTokens":999,"prompt":"forged"}}`, tool.LeaseToken)); code != http.StatusOK && code != http.StatusNoContent {
		t.Fatalf("complete tool: status=%d body=%s", code, raw)
	}
	for _, f := range storedAgentRuns(t, srv) {
		if f.Model == "forged" || f.InputTokens == 999 {
			t.Errorf("a tool's worker wrote a model call: %+v", f)
		}
	}
	if run := srv.agentRunFromReport(1<<40, &agentRunReport{Model: "m"}); run != nil {
		t.Errorf("an account for a job that does not exist = %+v", run)
	}
	// A stored text that is not an envelope, or a server with no vault, opens nothing.
	if text, ok := srv.openAgentRunText(agentRunSealPrompt, "plain text"); ok || text != "" {
		t.Errorf("a text that is no envelope opened as %q", text)
	}
	if text, ok := srv.openAgentRunText(agentRunSealPrompt, ""); !ok || text != "" {
		t.Errorf("an empty text = %q, %v, want it to be simply empty", text, ok)
	}
}
