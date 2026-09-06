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

// The inbound half of ADR-0254: a worker that decided an agent round reports what the
// model chose, and the engine turns that into activations.
//
// This is the second time a completion carries something that is not a variable, and
// it is the riskier of the two. A decision report *describes* work that happened; a
// tool call *causes* work to happen. So what the engine believes a worker about, and
// what it insists on checking itself, is the whole design — and these pin it.

// agentRoundBPMN is an agent-driven ad-hoc subprocess with two tools. Neither is
// connected to anything: they are the container's entry activities, which is what
// makes them the tools the model is offered (ADR-0253).
const agentRoundBPMN = `<?xml version="1.0" encoding="UTF-8"?>
<bpmn:definitions xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL"
                  xmlns:zeebe="http://camunda.org/schema/zeebe/1.0"
                  xmlns:atlas="http://atlas.dev/schema/1.0" id="defs-agent">
  <bpmn:process id="berater" isExecutable="true">
    <bpmn:startEvent id="s"/>
    <bpmn:adHocSubProcess id="agent">
      <bpmn:documentation>Finde den gunstigsten Hypothekarzins.</bpmn:documentation>
      <bpmn:extensionElements>
        <atlas:agentConnector connector="anthropic_pb" resultCollection="toolCallResults" resultElement="=toolCallResult"/>
      </bpmn:extensionElements>
      <bpmn:serviceTask id="zinsen_holen">
        <bpmn:documentation>Liest die Zinstabelle einer Bank.</bpmn:documentation>
        <bpmn:extensionElements>
          <zeebe:taskDefinition type="rates"/>
          <atlas:agentParam name="url" type="string" required="true" description="Die Zinsseite"/>
          <atlas:agentParam name="maxRows" type="number"/>
        </bpmn:extensionElements>
      </bpmn:serviceTask>
      <bpmn:serviceTask id="historie_lesen">
        <bpmn:documentation>Gibt die zuletzt erfassten Satze zuruck.</bpmn:documentation>
        <bpmn:extensionElements>
          <zeebe:taskDefinition type="history"/>
        </bpmn:extensionElements>
      </bpmn:serviceTask>
    </bpmn:adHocSubProcess>
    <bpmn:endEvent id="e"/>
    <bpmn:sequenceFlow id="f1" sourceRef="s" targetRef="agent"/>
    <bpmn:sequenceFlow id="f2" sourceRef="agent" targetRef="e"/>
  </bpmn:process>
</bpmn:definitions>`

// leasedJob is one job as a worker receives it, spelled here so a test can read the
// two things this record is about: the payload the round was resolved into, and the
// variables the activated tool sees.
type leasedJob struct {
	Key        uint64         `json:"jobKey"`
	ElementID  string         `json:"elementId"`
	LeaseToken uint64         `json:"leaseToken"`
	Variables  map[string]any `json:"variables"`
	Connector  *struct {
		Kind   string         `json:"kind"`
		Fields map[string]any `json:"fields"`
	} `json:"connector"`
}

// startAgentRound deploys the process, starts an instance and leases the round job the
// container parked, which is where every test below begins.
func startAgentRound(t *testing.T) (*Server, leasedJob) {
	t.Helper()
	srv, _ := newValidateServer(t)

	code, raw := serveInternal(t, srv, http.MethodPost, "/api/v1/deployments",
		agentRoundBPMN, "application/xml")
	if code != http.StatusOK && code != http.StatusCreated {
		t.Fatalf("deploy: status=%d body=%s", code, raw)
	}
	code, raw = serveInternal(t, srv, http.MethodPost, "/api/v1/processes/1/instances",
		`{"variables":{"laufzeit":10}}`, "application/json")
	if code != http.StatusOK && code != http.StatusCreated {
		t.Fatalf("create instance: status=%d body=%s", code, raw)
	}
	return srv, lease(t, srv, compiler.AgentJobType)
}

// lease pulls exactly one job of a type, the way a worker does.
func lease(t *testing.T, srv *Server, jobType string) leasedJob {
	t.Helper()
	body := fmt.Sprintf(`{"type":%q,"worker":"w1","leaseMs":60000}`, jobType)
	code, raw := serveInternal(t, srv, http.MethodPost, "/api/v1/jobs/activate", body, "application/json")
	if code != http.StatusOK {
		t.Fatalf("lease %s: status=%d body=%s", jobType, code, raw)
	}
	var out struct {
		Jobs []leasedJob `json:"jobs"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("decode lease: %v (%s)", err, raw)
	}
	if len(out.Jobs) != 1 {
		t.Fatalf("leased %d jobs of type %s, want 1; body=%s", len(out.Jobs), jobType, raw)
	}
	return out.Jobs[0]
}

func completeJob(t *testing.T, srv *Server, j leasedJob, body string) (int, []byte) {
	t.Helper()
	return serveInternal(t, srv, http.MethodPost,
		fmt.Sprintf("/api/v1/jobs/%d/complete", j.Key), body, "application/json")
}

func incidents(t *testing.T, srv *Server) []*model.IncidentValue {
	t.Helper()
	var found []*model.IncidentValue
	srv.do(func() {
		_ = srv.store.Incidents(func(_ uint64, v *model.IncidentValue) error {
			found = append(found, v)
			return nil
		})
	})
	return found
}

// The end-to-end proof of this record: a round decided out of process, reported back,
// and turned into the activation it names — with the arguments the model supplied
// landing in that activity's own scope.
//
// This is the test the record exists for. Everything before it was already true of a
// round decided in the engine; a worker being able to say "run this next" is new, and
// a report that completed the job without activating anything would look exactly like
// an agent that decided it was finished.
func TestAToolCallReportedByAWorkerActivatesThatTool(t *testing.T) {
	srv, round := startAgentRound(t)

	// The outbound half (phase 1) is what the worker was handed: the container's
	// toolbox, resolved from the compiled process it does not have.
	if round.Connector == nil || round.Connector.Kind != "agent" {
		t.Fatalf("payload = %#v, want an agent payload", round.Connector)
	}
	if got := round.Connector.Fields["goal"]; got != "Finde den gunstigsten Hypothekarzins." {
		t.Errorf("goal = %#v, want the container's own documentation", got)
	}

	// What a worker sends after asking a model: one call, by name, with arguments.
	body := fmt.Sprintf(`{"worker":"w1","leaseToken":%d,
		"toolCalls":[{"tool":"zinsen_holen","callId":"call_1",
		              "arguments":{"url":"https://bank.example/zinsen","maxRows":25}}]}`, round.LeaseToken)
	if code, raw := completeJob(t, srv, round, body); code != http.StatusOK && code != http.StatusNoContent {
		t.Fatalf("complete round: status=%d body=%s", code, raw)
	}

	if got := incidents(t, srv); len(got) != 0 {
		t.Fatalf("incidents = %+v, want none: the tool is one the container offers", got)
	}

	// The activation itself: the named tool is running, and it is the only one.
	tool := lease(t, srv, "rates")
	if tool.ElementID != "zinsen_holen" {
		t.Errorf("activated %q, want zinsen_holen", tool.ElementID)
	}
	// Arguments are believed, and this is what believing them means: they are written
	// into the activated activity's own scope, so the tool sees what the model chose
	// for it and nothing else does.
	if got := tool.Variables["url"]; got != "https://bank.example/zinsen" {
		t.Errorf("url = %#v, want the argument the model supplied", got)
	}
	if got := fmt.Sprint(tool.Variables["maxRows"]); got != "25" {
		t.Errorf("maxRows = %#v, want 25 — a number argument keeps its exact form", tool.Variables["maxRows"])
	}
	// The other tool was offered, not called. Activating everything on offer is what
	// a plain ad-hoc does; an agent-driven one activates what was chosen (ADR-0253).
	code, raw := serveInternal(t, srv, http.MethodPost, "/api/v1/jobs/activate",
		`{"type":"history","worker":"w1","leaseMs":60000}`, "application/json")
	if code != http.StatusOK {
		t.Fatalf("lease history: status=%d body=%s", code, raw)
	}
	var other struct {
		Jobs []leasedJob `json:"jobs"`
	}
	if err := json.Unmarshal(raw, &other); err != nil {
		t.Fatalf("decode lease: %v (%s)", err, raw)
	}
	if len(other.Jobs) != 0 {
		t.Errorf("historie_lesen is running as well; only the tool the model called may be activated")
	}
}

// A worker naming a tool the container does not carry must change nothing — this is
// where ADR-0253's governance argument would be undone from the worker side. The
// report adds no trust: the name is resolved against the compiled index, and an
// unknown one raises the incident ADR-0253 already defines rather than running
// something the model was never offered.
//
// Dropping it quietly would be worse than the incident. The agent would go on
// believing it took a step that never happened.
func TestAReportedToolTheContainerDoesNotOfferRaisesAnIncident(t *testing.T) {
	srv, round := startAgentRound(t)

	body := fmt.Sprintf(`{"worker":"w1","leaseToken":%d,
		"toolCalls":[{"tool":"geld_ueberweisen","callId":"call_1",
		              "arguments":{"iban":"CH00","betrag":50000}}]}`, round.LeaseToken)
	if code, raw := completeJob(t, srv, round, body); code != http.StatusOK && code != http.StatusNoContent {
		t.Fatalf("complete round: status=%d body=%s", code, raw)
	}

	got := incidents(t, srv)
	if len(got) != 1 {
		t.Fatalf("incidents = %d, want 1: a worker naming a tool the model does not carry must stop visibly", len(got))
	}
	// The message names what *is* on offer, because the operator reading it has to be
	// able to tell a typo from a worker talking to the wrong process.
	for _, want := range []string{"geld_ueberweisen", "zinsen_holen", "historie_lesen"} {
		if !strings.Contains(got[0].Message, want) {
			t.Errorf("incident message = %q, want it to name %q", got[0].Message, want)
		}
	}
	// And nothing ran.
	code, raw := serveInternal(t, srv, http.MethodPost, "/api/v1/jobs/activate",
		`{"type":"rates","worker":"w1","leaseMs":60000}`, "application/json")
	if code != http.StatusOK {
		t.Fatalf("lease rates: status=%d body=%s", code, raw)
	}
	var after struct {
		Jobs []leasedJob `json:"jobs"`
	}
	if err := json.Unmarshal(raw, &after); err != nil {
		t.Fatalf("decode lease: %v (%s)", err, raw)
	}
	if len(after.Jobs) != 0 {
		t.Errorf("a tool ran on a report the container's index refused")
	}
}

// No calls is an answer, not an omission: the agent saying it is done. The container
// completes through the ordinary path and the instance finishes — the same meaning an
// empty list has for a round decided in the engine, which is what lets one Worker Type
// serve both sides.
func TestAnEmptyToolCallReportEndsTheRun(t *testing.T) {
	srv, round := startAgentRound(t)

	body := fmt.Sprintf(`{"worker":"w1","leaseToken":%d,
		"variables":{"agentAnswer":"Die Bank am guenstigsten, 1.13%%."}}`, round.LeaseToken)
	if code, raw := completeJob(t, srv, round, body); code != http.StatusOK && code != http.StatusNoContent {
		t.Fatalf("complete round: status=%d body=%s", code, raw)
	}

	if got := incidents(t, srv); len(got) != 0 {
		t.Fatalf("incidents = %+v, want none: reporting no calls is how an agent finishes", got)
	}
	var active int
	srv.do(func() {
		n, err := srv.store.ActiveProcessInstanceCount()
		if err != nil {
			t.Errorf("count instances: %v", err)
		}
		active = n
	})
	if active != 0 {
		t.Errorf("active instances = %d, want 0: an agent reporting no calls ends its container and the process", active)
	}
}

// A report on a job that is not an agent round is dropped, not refused — the third
// rule lifted verbatim from decisionFromReport. The work was done and the variables
// are good; refusing would turn a worker sending a field the engine does not want into
// a failed job, which is a worse answer than ignoring it.
func TestAToolCallReportOnANonAgentJobIsIgnored(t *testing.T) {
	reps := []toolCallReport{{Tool: "zinsen_holen", CallId: "call_1"}}
	for _, tc := range []struct {
		name    string
		jobType int32
	}{
		{"a REST call", compiler.RestJobTypeIndex},
		{"a user task", compiler.UserTaskJobTypeIndex},
		{"a central decision", compiler.TemisDecisionJobTypeIndex},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := toolCallsFromReport(&model.JobValue{JobType: tc.jobType}, reps)
			if err != nil {
				t.Fatalf("toolCallsFromReport: %v — a report the engine does not want is dropped, not an error", err)
			}
			if got != nil {
				t.Errorf("tool calls = %#v, want none: %s did not run an agent round", got, tc.name)
			}
		})
	}
}

// The fold itself, on the one job type that may carry it. Names travel as the model
// said them — trimmed, but never resolved here — because resolving them is the
// engine's job and doing it twice is how the two would drift apart.
func TestToolCallsAreFoldedForAnAgentRound(t *testing.T) {
	jv := &model.JobValue{JobType: compiler.AgentJobTypeIndex}

	t.Run("nothing reported folds nothing", func(t *testing.T) {
		got, err := toolCallsFromReport(jv, nil)
		if err != nil || got != nil {
			t.Errorf("toolCallsFromReport(nil) = %#v, %v; want none and no error", got, err)
		}
	})

	t.Run("the arguments become typed variables", func(t *testing.T) {
		reps, err := parseToolCallReports([]byte(`{"toolCalls":[
			{"tool":" zinsen_holen ","callId":" call_1 ",
			 "arguments":{"url":"https://bank.example","maxRows":25,"nurAktuelle":true,
			              "filter":{"kanton":"ZH"}}}]}`))
		if err != nil {
			t.Fatalf("parseToolCallReports: %v", err)
		}
		got, err := toolCallsFromReport(jv, reps)
		if err != nil {
			t.Fatalf("toolCallsFromReport: %v", err)
		}
		if len(got) != 1 {
			t.Fatalf("tool calls = %d, want 1", len(got))
		}
		// Trimmed, because a name with a stray space is a name the compiled index
		// would refuse for a reason that has nothing to do with the model's choice.
		if got[0].Tool != "zinsen_holen" || got[0].CallId != "call_1" {
			t.Errorf("call = %+v, want the trimmed name and id", got[0])
		}
		byName := map[string]model.VariableValue{}
		for _, v := range got[0].Arguments {
			byName[v.Name] = v
		}
		for _, want := range []struct {
			name string
			kind model.VarKind
			text string
		}{
			{"url", model.VarString, "https://bank.example"},
			// The exact textual form, not a float64 round trip: an argument is a
			// variable, and FEEL's decimals are decided at the parse (ADR-0037).
			{"maxRows", model.VarNumber, "25"},
			{"nurAktuelle", model.VarBool, ""},
			{"filter", model.VarJSON, `{"kanton":"ZH"}`},
		} {
			got := byName[want.name]
			if got.Kind != want.kind {
				t.Errorf("%s: kind = %v, want %v", want.name, got.Kind, want.kind)
			}
			if want.text != "" && got.Text != want.text {
				t.Errorf("%s: text = %q, want %q", want.name, got.Text, want.text)
			}
		}
		if !byName["nurAktuelle"].Bool {
			t.Errorf("nurAktuelle = false, want true")
		}
	})
}
