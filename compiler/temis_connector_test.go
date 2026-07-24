package compiler

import (
	"strings"
	"testing"
)

// twoDecisionBPMN has one central (connector) business rule task and one local
// one, so the compiler's mode selection and the deploy-time gate can be checked
// side by side.
const twoDecisionBPMN = `<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL">
  <process id="p" isExecutable="true">
    <startEvent id="s"/>
    <businessRuleTask id="central">
      <extensionElements>
        <calledDecision decisionId="Risk" resultVariable="risk"/>
        <temisConnector connector="risk-service"/>
        <ioMapping><input source="= amount" target="Amount"/></ioMapping>
      </extensionElements>
    </businessRuleTask>
    <businessRuleTask id="local">
      <extensionElements>
        <calledDecision decisionId="Dish" resultVariable="dish"/>
      </extensionElements>
    </businessRuleTask>
    <endEvent id="e"/>
    <sequenceFlow id="f1" sourceRef="s" targetRef="central"/>
    <sequenceFlow id="f2" sourceRef="central" targetRef="local"/>
    <sequenceFlow id="f3" sourceRef="local" targetRef="e"/>
  </process>
</definitions>`

// TestTemisConnectorParses proves an <atlas:temisConnector> business rule task
// compiles to a central decision — the temis-connector job type and the connector
// name — while a plain business rule task stays local, and that the two modes are
// authored identically otherwise (decision id, result variable, input mappings).
func TestTemisConnectorParses(t *testing.T) {
	cp, err := Parse(1, 1, strings.NewReader(twoDecisionBPMN))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}

	var central, local *BusinessRuleTaskDetail
	for i := range cp.nodes {
		if cp.nodes[i].Type != TypeBusinessRuleTask {
			continue
		}
		d := cp.BusinessRuleTask(cp.nodes[i].Detail)
		switch cp.Intern(d.DecisionId) {
		case "Risk":
			central = d
		case "Dish":
			local = d
		}
	}
	if central == nil || local == nil {
		t.Fatalf("expected both a central and a local business rule task, got central=%v local=%v", central, local)
	}

	// Central: temis-connector job type + the connector name recorded.
	if central.JobType != TemisDecisionJobTypeIndex {
		t.Errorf("central job type = %d, want TemisDecisionJobTypeIndex %d", central.JobType, TemisDecisionJobTypeIndex)
	}
	if got := cp.Intern(central.Connector); got != "risk-service" {
		t.Errorf("central connector = %q, want risk-service", got)
	}
	if cp.Intern(central.ResultVar) != "risk" {
		t.Errorf("central result var = %q, want risk", cp.Intern(central.ResultVar))
	}
	if len(central.InputMappings) != 1 || central.InputMappings[0].Target != "Amount" {
		t.Errorf("central input mappings = %+v, want one Amount mapping", central.InputMappings)
	}

	// Local: DMN job type, no connector.
	if local.JobType != DMNJobTypeIndex {
		t.Errorf("local job type = %d, want DMNJobTypeIndex %d", local.JobType, DMNJobTypeIndex)
	}
	if local.Connector != -1 {
		t.Errorf("local connector = %d, want -1 (local)", local.Connector)
	}

	// The deploy-time local-model gate sees only the local decision; the central
	// one has no local model to resolve (ADR-0050).
	decisions := cp.BusinessRuleDecisions()
	if len(decisions) != 1 || decisions[0] != "Dish" {
		t.Errorf("BusinessRuleDecisions = %v, want only [Dish] (central excluded)", decisions)
	}
}

// TestTemisConnectorEmptyNameRejected fails a temisConnector with no connector
// name at parse (deploy) time rather than at runtime.
func TestTemisConnectorEmptyNameRejected(t *testing.T) {
	const bad = `<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL">
  <process id="p" isExecutable="true">
    <startEvent id="s"/>
    <businessRuleTask id="central">
      <extensionElements>
        <calledDecision decisionId="Risk"/>
        <temisConnector connector=""/>
      </extensionElements>
    </businessRuleTask>
    <sequenceFlow id="f1" sourceRef="s" targetRef="central"/>
  </process>
</definitions>`
	if _, err := Parse(1, 1, strings.NewReader(bad)); err == nil {
		t.Fatal("Parse with an empty temisConnector name: got nil error, want an error")
	}
}
