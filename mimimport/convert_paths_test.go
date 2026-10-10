package mimimport

import (
	"strings"
	"testing"
)

// TestASecondUnconditionalBranchIsFlaggedNotGuessed: WF runs the first branch that
// has no condition, so a second one is unreachable as written. The converter keeps
// both routes — an empty branch going straight to the join — and flags the second
// for a person to decide, rather than picking one silently.
func TestASecondUnconditionalBranchIsFlaggedNotGuessed(t *testing.T) {
	src := `<SequentialWorkflow>
	  <IfElseActivity Description="pick">
	    <IfElseBranchActivity Description="first"/>
	    <IfElseBranchActivity Description="second"><NotificationActivity Description="Tell"/></IfElseBranchActivity>
	  </IfElseActivity>
	</SequentialWorkflow>`
	res, err := Convert(strings.NewReader(src), "Two")
	if err != nil {
		t.Fatalf("Convert: %v", err)
	}
	validate(t, res.BPMN)

	var flagged []Note
	for _, n := range res.Report.Notes {
		if strings.Contains(n.Detail, "an earlier branch is already the default") {
			flagged = append(flagged, n)
		}
	}
	if len(flagged) != 1 || flagged[0].Status != StatusManualReview || flagged[0].Kind != "conditionExpression" {
		t.Fatalf("notes = %+v, want exactly the second branch flagged for review", res.Report.Notes)
	}
	// The empty first branch is the default and still a route: split straight to join.
	if !strings.Contains(string(res.BPMN), "_join") {
		t.Errorf("no join gateway in the converted model:\n%s", res.BPMN)
	}
}

// TestInputThatIsNotXMLIsRefused: an empty file or plain text has no root, and the
// converter says so instead of producing an empty process.
func TestInputThatIsNotXMLIsRefused(t *testing.T) {
	if _, err := Convert(strings.NewReader("just some text"), "X"); err == nil {
		t.Fatal("plain text converted into a process")
	}
}
