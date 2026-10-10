package api

import (
	"strings"
	"testing"

	"github.com/pblumer/atlas/compiler"
)

// An event-based gateway marked instantiate="true" or eventGatewayType="Parallel" is
// refused at deploy (compiler.RuleEventGatewayKind, #804). The Modeler says so while the
// author is still drawing, as it does for a conditional start: unsupportedReason puts the
// ⚠ badge on the element and a warning in the Problems bar.

// TestModelerWarnsOfAnEventGatewayTheCompilerRefuses keeps the Modeler's warning in step
// with the compiler's rule: the check exists, reads both attributes, and unsupportedReason
// reaches it.
func TestModelerWarnsOfAnEventGatewayTheCompilerRefuses(t *testing.T) {
	if compiler.RuleEventGatewayKind == "" {
		t.Fatal("compiler.RuleEventGatewayKind is empty; the rule this test pairs with is gone")
	}
	src := modelerSource(t)

	const decl = "function eventGatewayReason(bo) {"
	start := strings.Index(src, decl)
	if start < 0 {
		t.Fatal("editor.js has no eventGatewayReason; an instantiating or parallel event gateway would draw without a warning")
	}
	end := strings.Index(src[start:], "\n}\n")
	if end < 0 {
		t.Fatal("eventGatewayReason has no end in editor.js")
	}
	body := src[start : start+end]
	for _, want := range []string{`"bpmn:EventBasedGateway"`, "instantiate", "eventGatewayType", `"parallel"`} {
		if !strings.Contains(body, want) {
			t.Errorf("eventGatewayReason does not mention %s; it must flag both kinds the compiler refuses", want)
		}
	}

	const reason = "function unsupportedReason(bo) {"
	rs := strings.Index(src, reason)
	if rs < 0 {
		t.Fatal("editor.js has no unsupportedReason")
	}
	re := strings.Index(src[rs:], "\n}\n")
	if re < 0 || !strings.Contains(src[rs:rs+re], "eventGatewayReason(bo)") {
		t.Error("unsupportedReason does not call eventGatewayReason; the badge and the Problems warning would never show")
	}
}
