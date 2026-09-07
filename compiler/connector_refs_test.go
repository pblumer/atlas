package compiler

import (
	"strings"
	"testing"
)

// nodeIndexOf finds a compiled node by the BPMN element id the author wrote, which is
// how every caller outside the compiler addresses an element.
func nodeIndexOf(t *testing.T, cp *CompiledProcess, bpmnID string) int32 {
	t.Helper()
	for i := range cp.nodes {
		if cp.ElementBpmnId(int32(i)) == bpmnID {
			return int32(i)
		}
	}
	t.Fatalf("no node with BPMN id %q", bpmnID)
	return -1
}

// TestConnectorRefsEnumeratesBothShapes covers the two ways a model names a
// server-registered worker — a task and a *central* business rule task —
// and the elements that name none. A deploy checks every reference in a model against
// the worker store this way (ADR-0158).
func TestConnectorRefsEnumeratesBothShapes(t *testing.T) {
	mail, err := Parse(1, 1, strings.NewReader(mailConnectorBPMN))
	if err != nil {
		t.Fatalf("Parse mail: %v", err)
	}
	refs := mail.ConnectorRefs()
	if len(refs) != 1 {
		t.Fatalf("ConnectorRefs = %+v, want exactly the one mail task", refs)
	}
	if refs[0].ElementId != "t" || refs[0].Connector != "office365" || refs[0].JobType != MailJobTypeIndex {
		t.Errorf("ref = %+v, want element t → office365 on the mail job type", refs[0])
	}

	decisions, err := Parse(2, 1, strings.NewReader(twoDecisionBPMN))
	if err != nil {
		t.Fatalf("Parse decisions: %v", err)
	}
	refs = decisions.ConnectorRefs()
	if len(refs) != 1 {
		t.Fatalf("ConnectorRefs = %+v, want only the central decision", refs)
	}
	if refs[0].ElementId != "central" || refs[0].Connector != "risk-service" || refs[0].JobType != TemisDecisionJobTypeIndex {
		t.Errorf("ref = %+v, want element central → risk-service on the temis job type", refs[0])
	}
}

// TestNodeConnectorRefPerElement covers the per-element question an *incident* asks:
// a token is parked here, which worker is it stuck on (ADR-0160)? Every element
// that names no worker — a local decision, a start event, an index that is not a
// node at all — has to answer "none" rather than a wrong or partial reference.
func TestNodeConnectorRefPerElement(t *testing.T) {
	cp, err := Parse(1, 1, strings.NewReader(twoDecisionBPMN))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	ref, ok := cp.NodeConnectorRef(nodeIndexOf(t, cp, "central"))
	if !ok || ref.Connector != "risk-service" || ref.JobType != TemisDecisionJobTypeIndex {
		t.Errorf("central = %+v (ok=%v), want the risk-service reference", ref, ok)
	}
	// A business rule task evaluating a local decision is the same node type carrying
	// no worker — the case that would make a naive "it's a decision, so it has a
	// worker" answer wrong.
	if ref, ok := cp.NodeConnectorRef(nodeIndexOf(t, cp, "local")); ok {
		t.Errorf("local decision = %+v, want no worker reference", ref)
	}
	if ref, ok := cp.NodeConnectorRef(nodeIndexOf(t, cp, "s")); ok {
		t.Errorf("start event = %+v, want no worker reference", ref)
	}
	for _, id := range []int32{-1, int32(len(cp.nodes))} {
		if ref, ok := cp.NodeConnectorRef(id); ok {
			t.Errorf("node %d = %+v, want no reference for an index out of range", id, ref)
		}
	}
}

// A third shape, and the one that was missed: an agent-driven ad-hoc subprocess names
// its Worker on the *container*, not on a task (ADR-0253). Being absent here was not
// cosmetic — this enumeration is what a deploy warns from when nothing answers to a
// name (ADR-0158) and what a delete refuses from while a deployed model still depends
// on one (ADR-0163). Without it an agent Worker could be deleted out from under a
// running process, silently, and a model naming a Worker nobody had created deployed
// without a word.
func TestConnectorRefsEnumeratesAnAgentContainer(t *testing.T) {
	cp, err := Parse(1, 1, strings.NewReader(agentAdHoc(twoToolAdHoc)))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	refs := cp.ConnectorRefs()
	if len(refs) != 1 {
		t.Fatalf("ConnectorRefs = %+v, want exactly the agent container", refs)
	}
	if refs[0].ElementId != "adhoc" || refs[0].Connector != "anthropic_pb" || refs[0].JobType != AgentJobTypeIndex {
		t.Errorf("ref = %+v, want element adhoc → anthropic_pb on the agent job type", refs[0])
	}
	// The tools themselves name no Worker: they are ordinary activities, and a job
	// type is not a Worker reference.
	if _, ok := cp.NodeConnectorRef(nodeIndexOf(t, cp, "zinsen_holen")); ok {
		t.Error("a tool was reported as naming a Worker; only the container does")
	}
}

// A plain ad-hoc subprocess names none, so nothing about ADR-0143's containers changes.
func TestAPlainAdHocNamesNoWorker(t *testing.T) {
	cp, err := Parse(1, 1, strings.NewReader(agentAdHoc(`<adHocSubProcess id="adhoc">
	  <serviceTask id="frei"><extensionElements><zeebe:taskDefinition type="free"/></extensionElements></serviceTask>
	</adHocSubProcess>`)))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if refs := cp.ConnectorRefs(); len(refs) != 0 {
		t.Errorf("ConnectorRefs = %+v, want none: a plain ad-hoc names no Worker", refs)
	}
}
