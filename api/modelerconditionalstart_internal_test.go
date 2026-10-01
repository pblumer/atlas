package api

import (
	"strings"
	"testing"

	"github.com/pblumer/atlas/compiler"
)

// A process-level conditional start event is refused at deploy
// (compiler.RuleConditionalStart). The Modeler says so earlier, while the author is
// still drawing: unsupportedReason puts the ⚠ badge on the element and a warning in the
// Problems bar, and the server's finding on the same element replaces the warning once
// validation answers.
//
// The two halves live in different languages and nothing links them, which is how the
// silence this rule ends came about in the first place: the compiler dropped the
// condition and the Modeler had no entry for it. So the pairing is checked here.

// TestModelerWarnsOfAConditionalStartTheCompilerRefuses keeps the Modeler's warning in
// step with the compiler's rule: the check exists, unsupportedReason reaches it, and it
// spares an event subprocess, whose conditional start is its trigger and runs.
func TestModelerWarnsOfAConditionalStartTheCompilerRefuses(t *testing.T) {
	if compiler.RuleConditionalStart == "" {
		t.Fatal("compiler.RuleConditionalStart is empty; the rule this test pairs with is gone")
	}
	src := modelerSource(t)

	const decl = "function conditionalStartReason(bo) {"
	start := strings.Index(src, decl)
	if start < 0 {
		t.Fatal("editor.js has no conditionalStartReason; a process-level conditional start would draw without a warning again")
	}
	end := strings.Index(src[start:], "\n}\n")
	if end < 0 {
		t.Fatal("conditionalStartReason has no end in editor.js")
	}
	body := src[start : start+end]
	for _, want := range []string{`"bpmn:StartEvent"`, `"bpmn:ConditionalEventDefinition"`, "triggeredByEvent"} {
		if !strings.Contains(body, want) {
			t.Errorf("conditionalStartReason does not mention %s; it must flag a conditional start outside an event subprocess and only that", want)
		}
	}

	const reason = "function unsupportedReason(bo) {"
	rs := strings.Index(src, reason)
	if rs < 0 {
		t.Fatal("editor.js has no unsupportedReason")
	}
	re := strings.Index(src[rs:], "\n}\n")
	if re < 0 || !strings.Contains(src[rs:rs+re], "conditionalStartReason(bo)") {
		t.Error("unsupportedReason does not call conditionalStartReason; the badge and the Problems warning would never show")
	}
}
