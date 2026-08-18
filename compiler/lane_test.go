package compiler

import (
	"strings"
	"testing"
)

// TestParseLanes checks that a two-lane process records each flow node's lane (ADR-0121): a lane
// is organizational metadata with no execution effect, so the tokens still flow, but every node
// carries the lane it was drawn in.
func TestParseLanes(t *testing.T) {
	const xml = `<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL"
	             xmlns:zeebe="http://camunda.org/schema/zeebe/1.0">
	  <process id="p" isExecutable="true">
	    <laneSet id="ls">
	      <lane id="l_sales" name="Sales">
	        <flowNodeRef>s</flowNodeRef>
	        <flowNodeRef>capture</flowNodeRef>
	      </lane>
	      <lane id="l_finance" name="Finance">
	        <flowNodeRef>approve</flowNodeRef>
	        <flowNodeRef>e</flowNodeRef>
	      </lane>
	    </laneSet>
	    <startEvent id="s"/>
	    <userTask id="capture" name="Capture order"/>
	    <userTask id="approve" name="Approve"/>
	    <endEvent id="e"/>
	    <sequenceFlow id="f1" sourceRef="s" targetRef="capture"/>
	    <sequenceFlow id="f2" sourceRef="capture" targetRef="approve"/>
	    <sequenceFlow id="f3" sourceRef="approve" targetRef="e"/>
	  </process>
	</definitions>`
	cp, err := Parse(1, 1, strings.NewReader(xml))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	want := map[string]string{"s": "Sales", "capture": "Sales", "approve": "Finance", "e": "Finance"}
	for id, laneName := range want {
		n := nodeByBpmnId(t, cp, id)
		lane := cp.NodeLane(n.ElementId)
		if lane < 0 {
			t.Errorf("node %q is in no lane, want %q", id, laneName)
			continue
		}
		if got := cp.Intern(cp.Lane(lane).Name); got != laneName {
			t.Errorf("node %q lane = %q, want %q", id, got, laneName)
		}
		if path := cp.LanePath(n.ElementId); len(path) != 1 || path[0] != laneName {
			t.Errorf("node %q lane path = %v, want [%q]", id, path, laneName)
		}
	}
}

// TestParseNestedLanes checks that a node in a nested <childLaneSet> maps to its leaf lane, and
// its lane path runs outermost-to-leaf (ADR-0121).
func TestParseNestedLanes(t *testing.T) {
	const xml = `<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL">
	  <process id="p" isExecutable="true">
	    <laneSet id="ls">
	      <lane id="l_ops" name="Operations">
	        <childLaneSet id="cls">
	          <lane id="l_review" name="Review">
	            <flowNodeRef>check</flowNodeRef>
	          </lane>
	        </childLaneSet>
	      </lane>
	    </laneSet>
	    <startEvent id="s"/>
	    <userTask id="check" name="Check"/>
	    <endEvent id="e"/>
	    <sequenceFlow id="f1" sourceRef="s" targetRef="check"/>
	    <sequenceFlow id="f2" sourceRef="check" targetRef="e"/>
	  </process>
	</definitions>`
	cp, err := Parse(1, 1, strings.NewReader(xml))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	check := nodeByBpmnId(t, cp, "check")
	leaf := cp.NodeLane(check.ElementId)
	if leaf < 0 || cp.Intern(cp.Lane(leaf).Name) != "Review" {
		t.Fatalf("check leaf lane = %v, want Review", leaf)
	}
	if path := cp.LanePath(check.ElementId); len(path) != 2 || path[0] != "Operations" || path[1] != "Review" {
		t.Errorf("check lane path = %v, want [Operations Review]", path)
	}
	// A node in no lane stays at -1.
	if l := cp.NodeLane(nodeByBpmnId(t, cp, "s").ElementId); l != -1 {
		t.Errorf("start-event lane = %d, want -1 (in no lane)", l)
	}
}

// TestParseLaneUnknownFlowNodeRef rejects a lane whose flowNodeRef names no flow node (ADR-0121).
func TestParseLaneUnknownFlowNodeRef(t *testing.T) {
	const xml = `<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL">
	  <process id="p" isExecutable="true">
	    <laneSet id="ls"><lane id="l" name="L"><flowNodeRef>ghost</flowNodeRef></lane></laneSet>
	    <startEvent id="s"/><endEvent id="e"/>
	    <sequenceFlow id="f" sourceRef="s" targetRef="e"/>
	  </process>
	</definitions>`
	_, err := Parse(1, 1, strings.NewReader(xml))
	if err == nil {
		t.Fatal("Parse: want an error for a lane referencing an unknown flow node, got nil")
	}
	for _, want := range []string{"lane", "ghost", "unknown flow node"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q should mention %q", err.Error(), want)
		}
	}
}

// TestParseLaneDoubleAssignment rejects a flow node claimed by two lanes — a node belongs to at
// most one lane (ADR-0121).
func TestParseLaneDoubleAssignment(t *testing.T) {
	const xml = `<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL">
	  <process id="p" isExecutable="true">
	    <laneSet id="ls">
	      <lane id="l1" name="One"><flowNodeRef>t</flowNodeRef></lane>
	      <lane id="l2" name="Two"><flowNodeRef>t</flowNodeRef></lane>
	    </laneSet>
	    <startEvent id="s"/>
	    <userTask id="t" name="T"/>
	    <endEvent id="e"/>
	    <sequenceFlow id="f1" sourceRef="s" targetRef="t"/>
	    <sequenceFlow id="f2" sourceRef="t" targetRef="e"/>
	  </process>
	</definitions>`
	_, err := Parse(1, 1, strings.NewReader(xml))
	if err == nil {
		t.Fatal("Parse: want an error for a flow node in two lanes, got nil")
	}
	if !strings.Contains(err.Error(), "two lanes") {
		t.Errorf("error %q should mention %q", err.Error(), "two lanes")
	}
}

// TestParseNoLanes: a process without any laneSet compiles, and every node's lane is -1 (ADR-0121).
func TestParseNoLanes(t *testing.T) {
	const xml = `<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL">
	  <process id="p" isExecutable="true">
	    <startEvent id="s"/><endEvent id="e"/>
	    <sequenceFlow id="f" sourceRef="s" targetRef="e"/>
	  </process>
	</definitions>`
	cp, err := Parse(1, 1, strings.NewReader(xml))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	for _, id := range []string{"s", "e"} {
		if l := cp.NodeLane(nodeByBpmnId(t, cp, id).ElementId); l != -1 {
			t.Errorf("node %q lane = %d, want -1 (no lanes)", id, l)
		}
		if path := cp.LanePath(nodeByBpmnId(t, cp, id).ElementId); len(path) != 0 {
			t.Errorf("node %q lane path = %v, want empty", id, path)
		}
	}
}

// TestParseLaneErrorsAtEveryNestingLevel checks that a bad flowNodeRef is caught
// wherever the lane sits (ADR-0121). The lane walk recurses through childLaneSets
// and into subprocess scopes, and an error found down there has to travel back
// out — a laneSet nested two levels deep must not be able to smuggle a broken
// reference into a deployable process.
func TestParseLaneErrorsAtEveryNestingLevel(t *testing.T) {
	tests := []struct {
		name, laneXML string
	}{
		{
			"nested childLaneSet",
			`<laneSet id="ls">
			   <lane id="l_outer" name="Outer">
			     <childLaneSet id="cls"><lane id="l_inner" name="Inner"><flowNodeRef>ghost</flowNodeRef></lane></childLaneSet>
			   </lane>
			 </laneSet>`,
		},
		{
			"laneSet inside a subprocess",
			`<subProcess id="sub">
			   <laneSet id="ls_sub"><lane id="l_sub" name="Sub"><flowNodeRef>ghost</flowNodeRef></lane></laneSet>
			   <startEvent id="sub_s"/>
			 </subProcess>`,
		},
		{
			"laneSet inside an ad-hoc subprocess",
			`<adHocSubProcess id="adhoc">
			   <laneSet id="ls_ad"><lane id="l_ad" name="Ad"><flowNodeRef>ghost</flowNodeRef></lane></laneSet>
			   <task id="ad_t"/>
			 </adHocSubProcess>`,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			xml := `<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL">
			  <process id="p" isExecutable="true">
			    ` + tc.laneXML + `
			    <startEvent id="s"/><endEvent id="e"/>
			    <sequenceFlow id="f" sourceRef="s" targetRef="e"/>
			  </process>
			</definitions>`
			_, err := Parse(1, 1, strings.NewReader(xml))
			if err == nil {
				t.Fatal("Parse: want an error for the unknown flow node, got nil")
			}
			if !strings.Contains(err.Error(), "ghost") {
				t.Errorf("error %q should name the unresolved reference", err.Error())
			}
		})
	}
}

// TestParseLaneUnnamedIsIdentifiedById checks the label a lane gets in an error
// when it has no name. A model drawn without lane captions would otherwise report
// an empty name, leaving nothing to search the XML for.
func TestParseLaneUnnamedIsIdentifiedById(t *testing.T) {
	const xml = `<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL">
	  <process id="p" isExecutable="true">
	    <laneSet id="ls"><lane id="l_nameless"><flowNodeRef>ghost</flowNodeRef></lane></laneSet>
	    <startEvent id="s"/><endEvent id="e"/>
	    <sequenceFlow id="f" sourceRef="s" targetRef="e"/>
	  </process>
	</definitions>`
	_, err := Parse(1, 1, strings.NewReader(xml))
	if err == nil {
		t.Fatal("Parse: want an error, got nil")
	}
	if !strings.Contains(err.Error(), "l_nameless") {
		t.Errorf("error %q should fall back to the lane's id", err.Error())
	}
}

// TestParseLaneBlankFlowNodeRefIgnored checks that an empty <flowNodeRef/> is
// skipped rather than treated as a reference to a node named "". Round-tripping a
// model through a modeler can leave one behind, and it names nothing, so it
// cannot be an unknown-node error.
func TestParseLaneBlankFlowNodeRefIgnored(t *testing.T) {
	const xml = `<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL">
	  <process id="p" isExecutable="true">
	    <laneSet id="ls">
	      <lane id="l" name="L">
	        <flowNodeRef></flowNodeRef>
	        <flowNodeRef>   </flowNodeRef>
	        <flowNodeRef>s</flowNodeRef>
	      </lane>
	    </laneSet>
	    <startEvent id="s"/><endEvent id="e"/>
	    <sequenceFlow id="f" sourceRef="s" targetRef="e"/>
	  </process>
	</definitions>`
	cp, err := Parse(1, 1, strings.NewReader(xml))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	s := nodeByBpmnId(t, cp, "s")
	if lane := cp.NodeLane(s.ElementId); lane < 0 || cp.Intern(cp.Lane(lane).Name) != "L" {
		t.Errorf("start event lane = %d, want the real reference to still resolve to L", lane)
	}
}
