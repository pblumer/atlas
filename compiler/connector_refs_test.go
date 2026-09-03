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
