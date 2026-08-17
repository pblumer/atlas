package compiler

import (
	"strings"
	"testing"
)

// linkTargets returns the ElementIds a node's outgoing flows point at.
func linkTargets(cp *CompiledProcess, node int32) []int32 {
	var out []int32
	for _, fid := range cp.Outgoing(node) {
		out = append(out, cp.Flow(fid).Target)
	}
	return out
}

// TestParseLinkThrowCatch checks that a link throw and a link catch of the same name compile
// to TypeLinkThrowEvent / TypeLinkCatchEvent and are wired by a synthetic sequence flow from
// the throw to the catch — the goto (ADR-0133). The throw has no real outgoing flow in the XML;
// the only edge leaving it is the synthetic one to the catch.
func TestParseLinkThrowCatch(t *testing.T) {
	const xml = `<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL">
	  <process id="p" isExecutable="true">
	    <startEvent id="s"/>
	    <intermediateThrowEvent id="thr"><linkEventDefinition name="A"/></intermediateThrowEvent>
	    <intermediateCatchEvent id="cat"><linkEventDefinition name="A"/></intermediateCatchEvent>
	    <endEvent id="e"/>
	    <sequenceFlow id="f1" sourceRef="s" targetRef="thr"/>
	    <sequenceFlow id="f2" sourceRef="cat" targetRef="e"/>
	  </process>
	</definitions>`
	cp, err := Parse(1, 1, strings.NewReader(xml))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	thr := nodeByBpmnId(t, cp, "thr")
	if thr.Type != TypeLinkThrowEvent || thr.Type.String() != "LinkThrowEvent" {
		t.Fatalf("throw type = %v (%q), want LinkThrowEvent", thr.Type, thr.Type.String())
	}
	cat := nodeByBpmnId(t, cp, "cat")
	if cat.Type != TypeLinkCatchEvent || cat.Type.String() != "LinkCatchEvent" {
		t.Fatalf("catch type = %v (%q), want LinkCatchEvent", cat.Type, cat.Type.String())
	}
	// The synthetic goto: the throw's only outgoing edge lands on the catch.
	tgts := linkTargets(cp, thr.ElementId)
	if len(tgts) != 1 || tgts[0] != cat.ElementId {
		t.Errorf("link throw outgoing = %v, want [catch=%d] (the synthetic goto)", tgts, cat.ElementId)
	}
	// The catch flows on to the real end event.
	end := nodeByBpmnId(t, cp, "e")
	if cts := linkTargets(cp, cat.ElementId); len(cts) != 1 || cts[0] != end.ElementId {
		t.Errorf("link catch outgoing = %v, want [end=%d]", cts, end.ElementId)
	}
}

// TestParseLinkManyThrowsOneCatch checks that several link throws with the same name all wire
// to the one catch — many gotos, one label (ADR-0133).
func TestParseLinkManyThrowsOneCatch(t *testing.T) {
	const xml = `<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL">
	  <process id="p" isExecutable="true">
	    <startEvent id="s"/>
	    <exclusiveGateway id="g"/>
	    <intermediateThrowEvent id="t1"><linkEventDefinition name="Retry"/></intermediateThrowEvent>
	    <intermediateThrowEvent id="t2"><linkEventDefinition name="Retry"/></intermediateThrowEvent>
	    <intermediateCatchEvent id="cat"><linkEventDefinition name="Retry"/></intermediateCatchEvent>
	    <endEvent id="e"/>
	    <sequenceFlow id="f0" sourceRef="s" targetRef="g"/>
	    <sequenceFlow id="f1" sourceRef="g" targetRef="t1"/>
	    <sequenceFlow id="f2" sourceRef="g" targetRef="t2"/>
	    <sequenceFlow id="f3" sourceRef="cat" targetRef="e"/>
	  </process>
	</definitions>`
	cp, err := Parse(1, 1, strings.NewReader(xml))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	cat := nodeByBpmnId(t, cp, "cat").ElementId
	for _, id := range []string{"t1", "t2"} {
		tgts := linkTargets(cp, nodeByBpmnId(t, cp, id).ElementId)
		if len(tgts) != 1 || tgts[0] != cat {
			t.Errorf("throw %q outgoing = %v, want [catch=%d]", id, tgts, cat)
		}
	}
}

// TestParseLinkInsideSubprocess checks that a link pair inside a subprocess resolves within
// that scope — the throw wires to the catch drawn in the same subprocess (ADR-0133).
func TestParseLinkInsideSubprocess(t *testing.T) {
	const xml = `<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL">
	  <process id="p" isExecutable="true">
	    <startEvent id="s"/>
	    <subProcess id="sub">
	      <startEvent id="ss"/>
	      <intermediateThrowEvent id="thr"><linkEventDefinition name="X"/></intermediateThrowEvent>
	      <intermediateCatchEvent id="cat"><linkEventDefinition name="X"/></intermediateCatchEvent>
	      <endEvent id="se"/>
	      <sequenceFlow id="i1" sourceRef="ss" targetRef="thr"/>
	      <sequenceFlow id="i2" sourceRef="cat" targetRef="se"/>
	    </subProcess>
	    <endEvent id="e"/>
	    <sequenceFlow id="f1" sourceRef="s" targetRef="sub"/>
	    <sequenceFlow id="f2" sourceRef="sub" targetRef="e"/>
	  </process>
	</definitions>`
	cp, err := Parse(1, 1, strings.NewReader(xml))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	cat := nodeByBpmnId(t, cp, "cat").ElementId
	tgts := linkTargets(cp, nodeByBpmnId(t, cp, "thr").ElementId)
	if len(tgts) != 1 || tgts[0] != cat {
		t.Errorf("subprocess link throw outgoing = %v, want [catch=%d]", tgts, cat)
	}
}

// TestParseLinkErrors rejects an unmatched link throw and a duplicate link catch name
// (ADR-0133).
func TestParseLinkErrors(t *testing.T) {
	wrap := func(body string) string {
		return `<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL">
		  <process id="p" isExecutable="true">` + body + `</process></definitions>`
	}
	cases := map[string]struct{ body, want string }{
		"unmatched throw": {
			body: `<startEvent id="s"/><intermediateThrowEvent id="thr"><linkEventDefinition name="Nope"/></intermediateThrowEvent>
			        <sequenceFlow id="f" sourceRef="s" targetRef="thr"/>`,
			want: "no matching link catch",
		},
		"duplicate catch": {
			body: `<startEvent id="s"/>
			        <intermediateCatchEvent id="c1"><linkEventDefinition name="Dup"/></intermediateCatchEvent>
			        <intermediateCatchEvent id="c2"><linkEventDefinition name="Dup"/></intermediateCatchEvent>
			        <endEvent id="e1"/><endEvent id="e2"/>
			        <sequenceFlow id="f1" sourceRef="c1" targetRef="e1"/><sequenceFlow id="f2" sourceRef="c2" targetRef="e2"/>
			        <intermediateThrowEvent id="thr"><linkEventDefinition name="Dup"/></intermediateThrowEvent>
			        <sequenceFlow id="f0" sourceRef="s" targetRef="thr"/>`,
			want: "duplicate link catch name",
		},
	}
	for name, tc := range cases {
		_, err := Parse(1, 1, strings.NewReader(wrap(tc.body)))
		if err == nil {
			t.Errorf("%s: Parse succeeded, want an error", name)
			continue
		}
		if !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: error %q should contain %q", name, err.Error(), tc.want)
		}
	}
}

// TestParseLinkAndMessageNamesCoexist proves a link name and a message of the same name do not
// collide — the name spaces are separate (ADR-0133).
func TestParseLinkAndMessageNamesCoexist(t *testing.T) {
	const xml = `<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL"
	             xmlns:zeebe="http://camunda.org/schema/zeebe/1.0">
	  <message id="Msg" name="Retry"><extensionElements><zeebe:subscription correlationKey="=k"/></extensionElements></message>
	  <process id="p" isExecutable="true">
	    <startEvent id="s"/>
	    <intermediateThrowEvent id="thr"><linkEventDefinition name="Retry"/></intermediateThrowEvent>
	    <intermediateCatchEvent id="cat"><linkEventDefinition name="Retry"/></intermediateCatchEvent>
	    <intermediateCatchEvent id="mc"><messageEventDefinition messageRef="Msg"/></intermediateCatchEvent>
	    <endEvent id="e"/>
	    <sequenceFlow id="f1" sourceRef="s" targetRef="thr"/>
	    <sequenceFlow id="f2" sourceRef="cat" targetRef="mc"/>
	    <sequenceFlow id="f3" sourceRef="mc" targetRef="e"/>
	  </process>
	</definitions>`
	cp, err := Parse(1, 1, strings.NewReader(xml))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	cat := nodeByBpmnId(t, cp, "cat").ElementId
	if tgts := linkTargets(cp, nodeByBpmnId(t, cp, "thr").ElementId); len(tgts) != 1 || tgts[0] != cat {
		t.Errorf("link throw outgoing = %v, want [catch=%d] (link name is not a message name)", tgts, cat)
	}
	if nodeByBpmnId(t, cp, "mc").Type != TypeMessageCatchEvent {
		t.Errorf("message catch of the same name should stay a message catch")
	}
}
