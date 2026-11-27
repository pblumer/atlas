package compiler

import (
	"strings"
	"testing"
	"time"
)

// limitedAdHoc is twoToolAdHoc's container with the given attributes on its connector.
func limitedAdHoc(attrs string) string {
	return agentAdHoc(strings.Replace(twoToolAdHoc, `connector="anthropic_pb"`, `connector="anthropic_pb" `+attrs, 1))
}

func aiTask(attrs string) string {
	return agentAdHoc(`<serviceTask id="adhoc">
	  <extensionElements>
	    <atlas:agentConnector connector="anthropic_pb" prompt="Fasse zusammen" resultVariable="antwort" ` + attrs + `/>
	  </extensionElements>
	</serviceTask>`)
}

// The three bounds ADR-0117 named are read off the container, and a container that
// states no round bound still has one (ADR-draft-agent-run-record).
func TestAgentLimitsCompileOntoTheContainer(t *testing.T) {
	cp, err := Parse(1, 1, strings.NewReader(limitedAdHoc(`maxIterations="6" tokenBudget="40000" timeout="PT90S" maxContextTokens="30000"`)))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	l := cp.AdHoc(nodeByBpmnId(t, cp, "adhoc").Detail).AgentLimits
	if l.ContextTokens() != 30000 {
		t.Errorf("context bound = %d, want the stated 30000", l.ContextTokens())
	}
	if l.MaxRounds != 6 || l.TokenBudget != 40000 || l.TimeoutNanos != int64(90*time.Second) || l.Rounds() != 6 {
		t.Errorf("limits = %+v (rounds %d), want 6 rounds, 40000 tokens, 90s", l, l.Rounds())
	}

	cp, err = Parse(1, 1, strings.NewReader(agentAdHoc(twoToolAdHoc)))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	l = cp.AdHoc(nodeByBpmnId(t, cp, "adhoc").Detail).AgentLimits
	if l.ContextTokens() != SafeAgentContextTokens {
		t.Errorf("unstated context bound = %d, want %d", l.ContextTokens(), SafeAgentContextTokens)
	}
	if l.MaxRounds != 0 || l.TokenBudget != 0 || l.TimeoutNanos != 0 || l.Rounds() != SafeAgentRounds {
		t.Errorf("unstated limits = %+v (rounds %d), want nothing stated and the safe ceiling %d", l, l.Rounds(), SafeAgentRounds)
	}
}

func TestAnAiTaskCarriesItsTimeout(t *testing.T) {
	cp, err := Parse(1, 1, strings.NewReader(aiTask(`timeout="PT20S"`)))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if got := cp.ConnectorTask(nodeByBpmnId(t, cp, "adhoc").Detail).AgentLimits.TimeoutNanos; got != int64(20*time.Second) {
		t.Errorf("timeout = %d, want 20s", got)
	}
}

// A bound that cannot be honoured refuses the deploy on the element that states it —
// and does not stop the same model from being loaded again, because a definition
// deployed before these attributes were read may carry one (ADR-0177).
func TestAnUnusableAgentLimitRefusesTheDeployButNotTheReload(t *testing.T) {
	for name, tc := range map[string]struct{ xml, want string }{
		"not a number":          {limitedAdHoc(`maxIterations="viele"`), `invalid maxIterations "viele"`},
		"zero rounds":           {limitedAdHoc(`maxIterations="0"`), `invalid maxIterations "0"`},
		"rounds too large":      {limitedAdHoc(`maxIterations="9999999999"`), `too large`},
		"negative budget":       {limitedAdHoc(`tokenBudget="-5"`), `invalid tokenBudget "-5"`},
		"timeout not ISO":       {limitedAdHoc(`timeout="90"`), `invalid timeout "90"`},
		"timeout too short":     {limitedAdHoc(`timeout="PT0.1S"`), `timeout`},
		"timeout too long":      {limitedAdHoc(`timeout="P2D"`), `outside`},
		"rounds on an ai task":  {aiTask(`maxIterations="3"`), `sets maxIterations, which only an agent-driven ad-hoc subprocess reads`},
		"budget on an ai task":  {aiTask(`tokenBudget="3"`), `sets tokenBudget, which only an agent-driven ad-hoc subprocess reads`},
		"context on an ai task": {aiTask(`maxContextTokens="3"`), `sets maxContextTokens, which only an agent-driven ad-hoc subprocess reads`},
		"context not a number":  {limitedAdHoc(`maxContextTokens="viel"`), `invalid maxContextTokens "viel"`},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := Parse(1, 1, strings.NewReader(tc.xml))
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("Parse error = %v, want one naming %q", err, tc.want)
			}
			cp, problems, err := ReloadNamed(1, 1, strings.NewReader(tc.xml), "p")
			if err != nil || cp == nil {
				t.Fatalf("ReloadNamed = %v, %v: a stored model must still load", cp, err)
			}
			found := false
			for _, p := range problems {
				found = found || (p.Rule == RuleAgentLimit && p.Element == "adhoc" && p.Severity == SeverityError)
			}
			if !found {
				t.Errorf("reload reported %+v, want an %s error on the element", problems, RuleAgentLimit)
			}
		})
	}
}

const answerFields = `<atlas:agentOutput name="entscheid" type="string" required="true" values="ja, nein" description="Ob es tragbar ist"/>
	    <atlas:agentOutput name="quote" type="number"/>`

// The fields an answer must carry are read off the element that hosts the agent — an ai
// task or a container — in the order they were written, and do not become tool
// parameters of anything (ADR-draft-agent-transcript-and-answer).
func TestAnswerFieldsCompileOntoTheirHost(t *testing.T) {
	task := strings.Replace(aiTask(``), `</extensionElements>`, answerFields+`</extensionElements>`, 1)
	container := agentAdHoc(strings.Replace(twoToolAdHoc, `<extensionElements>`, `<extensionElements>`+answerFields, 1))
	for name, xml := range map[string]string{"ai task": task, "container": container} {
		cp, err := Parse(1, 1, strings.NewReader(xml))
		if err != nil {
			t.Fatalf("%s: Parse: %v", name, err)
		}
		node := nodeByBpmnId(t, cp, "adhoc")
		var outs []AgentOutput
		if node.Type == TypeAdHocSubProcess {
			d := cp.AdHoc(node.Detail)
			outs = d.AgentOutputs
			for _, tool := range d.Tools {
				for _, p := range tool.Params {
					if cp.Intern(p.Name) == "entscheid" {
						t.Errorf("%s: an answer field became a parameter of a tool", name)
					}
				}
			}
		} else {
			outs = cp.ConnectorTask(node.Detail).AgentOutputs
		}
		if len(outs) != 2 || cp.Intern(outs[0].Name) != "entscheid" || cp.Intern(outs[0].Type) != "string" || !outs[0].Required ||
			len(outs[0].Values) != 2 || cp.Intern(outs[0].Values[1]) != "nein" || cp.Intern(outs[0].Description) != "Ob es tragbar ist" ||
			cp.Intern(outs[1].Name) != "quote" || outs[1].Required || outs[1].Description != -1 {
			t.Errorf("%s: outputs = %+v", name, outs)
		}
	}
}

func TestAnUnusableAnswerFieldRefusesTheDeployButNotTheReload(t *testing.T) {
	for name, tc := range map[string]struct{ field, want string }{
		"no name":         {`<atlas:agentOutput type="string"/>`, `agentOutput without a name`},
		"twice":           {`<atlas:agentOutput name="a" type="string"/><atlas:agentOutput name="a" type="number"/>`, `"a" twice`},
		"not a scalar":    {`<atlas:agentOutput name="a" type="object"/>`, `has type "object"`},
		"values on a num": {`<atlas:agentOutput name="a" type="number" values="1,2"/>`, `only a string field can`},
	} {
		t.Run(name, func(t *testing.T) {
			xml := strings.Replace(aiTask(``), `</extensionElements>`, tc.field+`</extensionElements>`, 1)
			if _, err := Parse(1, 1, strings.NewReader(xml)); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("Parse error = %v, want one naming %q", err, tc.want)
			}
			cp, problems, err := ReloadNamed(1, 1, strings.NewReader(xml), "p")
			if err != nil || cp == nil {
				t.Fatalf("ReloadNamed = %v, %v: a stored model must still load", cp, err)
			}
			found := false
			for _, p := range problems {
				found = found || (p.Rule == RuleAgentOutput && p.Element == "adhoc")
			}
			if !found {
				t.Errorf("reload reported %+v, want an %s error", problems, RuleAgentOutput)
			}
		})
	}
}
