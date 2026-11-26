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
	cp, err := Parse(1, 1, strings.NewReader(limitedAdHoc(`maxIterations="6" tokenBudget="40000" timeout="PT90S"`)))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	l := cp.AdHoc(nodeByBpmnId(t, cp, "adhoc").Detail).AgentLimits
	if l.MaxRounds != 6 || l.TokenBudget != 40000 || l.TimeoutNanos != int64(90*time.Second) || l.Rounds() != 6 {
		t.Errorf("limits = %+v (rounds %d), want 6 rounds, 40000 tokens, 90s", l, l.Rounds())
	}

	cp, err = Parse(1, 1, strings.NewReader(agentAdHoc(twoToolAdHoc)))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	l = cp.AdHoc(nodeByBpmnId(t, cp, "adhoc").Detail).AgentLimits
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
		"not a number":         {limitedAdHoc(`maxIterations="viele"`), `invalid maxIterations "viele"`},
		"zero rounds":          {limitedAdHoc(`maxIterations="0"`), `invalid maxIterations "0"`},
		"rounds too large":     {limitedAdHoc(`maxIterations="9999999999"`), `too large`},
		"negative budget":      {limitedAdHoc(`tokenBudget="-5"`), `invalid tokenBudget "-5"`},
		"timeout not ISO":      {limitedAdHoc(`timeout="90"`), `invalid timeout "90"`},
		"timeout too short":    {limitedAdHoc(`timeout="PT0.1S"`), `timeout`},
		"timeout too long":     {limitedAdHoc(`timeout="P2D"`), `outside`},
		"rounds on an ai task": {aiTask(`maxIterations="3"`), `sets maxIterations, which only an agent-driven ad-hoc subprocess reads`},
		"budget on an ai task": {aiTask(`tokenBudget="3"`), `sets tokenBudget, which only an agent-driven ad-hoc subprocess reads`},
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
