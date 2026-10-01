package dmn_test

import (
	"context"
	"strings"
	"testing"

	"github.com/pblumer/atlas/dmn"
)

// TestEveryServicePublishingNothingIsNamed: the refusal names each empty service by
// whatever the document gives it — its name, else its id — and still refuses one that
// gives neither, because it is the document that is wrong.
func TestEveryServicePublishingNothingIsNamed(t *testing.T) {
	model := `<?xml version="1.0" encoding="UTF-8"?>
<definitions xmlns="https://www.omg.org/spec/DMN/20230324/MODEL/" id="d" name="Rating" namespace="http://atlas/dmn/service">
  <inputData id="in1" name="Amount"><variable id="v1" name="amount" typeRef="number"/></inputData>
  <decision id="dec1" name="Risk">
    <variable id="v2" name="risk" typeRef="string"/>
    <informationRequirement id="ir1"><requiredInput href="#in1"/></informationRequirement>
    <literalExpression id="le1"><text>if amount &gt; 100 then "high" else "low"</text></literalExpression>
  </decision>
  <decisionService id="svc_by_id"><encapsulatedDecision href="#dec1"/></decisionService>
  <decisionService><encapsulatedDecision href="#dec1"/></decisionService>
</definitions>`
	res := dmn.NewValidator(dmn.DirResolver{Dir: t.TempDir()}).ValidateXML(context.Background(), []byte(model))
	if res.Valid {
		t.Fatal("two decision services returning nothing were accepted")
	}
	if !strings.Contains(res.Message, `the decision services "svc_by_id", "(unnamed)" return nothing`) {
		t.Fatalf("message = %q, want both services named, in the plural", res.Message)
	}
}
