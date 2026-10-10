package feelgen

import (
	"strings"
	"testing"
)

// What a model writes is not under anybody's control, and the free models an
// installation is likely to point this at are the least disciplined about it. These tests
// pin how much of that this package forgives: the contract in a fence, the contract with
// prose around it, a bare code block, and plain talk — and what it does not forgive, an
// answer too long to be one expression.

func TestParseAnswerReadsTheContract(t *testing.T) {
	for name, answer := range map[string]string{
		"bare":       `{"expression":"x > 1","explanation":"Grösser als eins.","variables":{"x":2},"expected":true}`,
		"fenced":     "```json\n{\"expression\":\"x > 1\",\"explanation\":\"Grösser als eins.\",\"variables\":{\"x\":2},\"expected\":true}\n```",
		"with prose": "Hier ist die Expression:\n{\"expression\":\"x > 1\",\"explanation\":\"Grösser als eins.\",\"variables\":{\"x\":2},\"expected\":true}\nViel Erfolg!",
	} {
		t.Run(name, func(t *testing.T) {
			p, err := ParseAnswer(answer, 1<<16)
			if err != nil {
				t.Fatalf("ParseAnswer: %v", err)
			}
			if p.Expression != "x > 1" || p.Explanation != "Grösser als eins." {
				t.Errorf("got %+v", p)
			}
			if !p.HasExpected || p.Expected != true {
				t.Errorf("expected = %v (stated %v), want true", p.Expected, p.HasExpected)
			}
			if p.Variables["x"] == nil {
				t.Errorf("variables = %v, want x", p.Variables)
			}
		})
	}
}

// TestParseAnswerKeepsNumbersExact: an example of 0.1 must reach the engine as the
// decimal 0.1, not as the float nearest to it.
func TestParseAnswerKeepsNumbersExact(t *testing.T) {
	p, err := ParseAnswer(`{"expression":"a + b","variables":{"a":0.1,"b":0.2},"expected":0.3}`, 1<<16)
	if err != nil {
		t.Fatalf("ParseAnswer: %v", err)
	}
	c := Evaluate(p.Expression, p.Variables, p.Expected, p.HasExpected)
	if !c.OK || c.Result != "0.3" || c.Matches == nil || !*c.Matches {
		t.Errorf("check = %+v, want 0.3 matching the stated 0.3", c)
	}
}

// TestParseAnswerStripsTheExpressionMarker: Zeebe writes an expression field as
// "= …", and models trained on its documentation do too. The marker belongs to the
// field, not to the expression — an fx field adds its own when the author applies it.
func TestParseAnswerStripsTheExpressionMarker(t *testing.T) {
	p, err := ParseAnswer(`{"expression":"  = x > 1 ","explanation":"e"}`, 1<<16)
	if err != nil {
		t.Fatalf("ParseAnswer: %v", err)
	}
	if p.Expression != "x > 1" {
		t.Errorf("expression = %q, want the marker gone", p.Expression)
	}
}

// TestParseAnswerFallsBackToACodeBlock: a model that ignores the contract but puts its
// expression in a code block has still answered the question.
func TestParseAnswerFallsBackToACodeBlock(t *testing.T) {
	answer := "Verwende diese Expression:\n\n```feel\nsum(positions.amount)\n```\n\nSie summiert alle Beträge."
	p, err := ParseAnswer(answer, 1<<16)
	if err != nil {
		t.Fatalf("ParseAnswer: %v", err)
	}
	if p.Expression != "sum(positions.amount)" {
		t.Errorf("expression = %q", p.Expression)
	}
	if !strings.Contains(p.Explanation, "Verwende diese Expression") || !strings.Contains(p.Explanation, "summiert") {
		t.Errorf("explanation = %q, want the prose around the block", p.Explanation)
	}
	if p.HasExpected || p.Variables != nil {
		t.Errorf("a code block states no example: %+v", p)
	}
}

// TestParseAnswerDoesNotMistakeAContextForTheContract: a FEEL context literal has
// braces too. It is not JSON, so it must not be read as a broken contract and dropped.
func TestParseAnswerDoesNotMistakeAContextForTheContract(t *testing.T) {
	p, err := ParseAnswer("```feel\n{name: kunde.name, total: sum(items.price)}\n```", 1<<16)
	if err != nil {
		t.Fatalf("ParseAnswer: %v", err)
	}
	if p.Expression != "{name: kunde.name, total: sum(items.price)}" {
		t.Errorf("expression = %q", p.Expression)
	}
}

// TestParseAnswerProseIsTalk: no contract and no code block is the model talking — a
// question back, or a refusal. It is shown as what it is, with no expression to check.
func TestParseAnswerProseIsTalk(t *testing.T) {
	p, err := ParseAnswer("Meinst du mit Bestellwert den Betrag inklusive MWST?", 1<<16)
	if err != nil {
		t.Fatalf("ParseAnswer: %v", err)
	}
	if p.Expression != "" || !strings.HasPrefix(p.Explanation, "Meinst du") {
		t.Errorf("got %+v", p)
	}
}

// TestParseAnswerAQuestionInTheContract: the contract's own way of asking is an empty
// expression with the question in the explanation.
func TestParseAnswerAQuestionInTheContract(t *testing.T) {
	p, err := ParseAnswer(`{"expression":"","explanation":"Welche Variable hält den Betrag?"}`, 1<<16)
	if err != nil {
		t.Fatalf("ParseAnswer: %v", err)
	}
	if p.Expression != "" || p.Explanation != "Welche Variable hält den Betrag?" {
		t.Errorf("got %+v", p)
	}
}

// TestParseAnswerExpectedNullIsStated: "expected": null is a claim — the expression
// returns null for these inputs — and must not read as no claim at all.
func TestParseAnswerExpectedNullIsStated(t *testing.T) {
	p, err := ParseAnswer(`{"expression":"sum(xs)","variables":{"xs":[]},"expected":null}`, 1<<16)
	if err != nil {
		t.Fatalf("ParseAnswer: %v", err)
	}
	if !p.HasExpected || p.Expected != nil {
		t.Errorf("expected = %v (stated %v), want a stated null", p.Expected, p.HasExpected)
	}
}

// TestParseAnswerIgnoresVariablesThatAreNotAnObject: variables are named, so anything
// but an object cannot bind; the author's own test variables answer instead.
func TestParseAnswerIgnoresVariablesThatAreNotAnObject(t *testing.T) {
	p, err := ParseAnswer(`{"expression":"1","variables":[1,2]}`, 1<<16)
	if err != nil {
		t.Fatalf("ParseAnswer: %v", err)
	}
	if p.Variables != nil {
		t.Errorf("variables = %v, want none", p.Variables)
	}
}

func TestParseAnswerRefusesWhatCannotBeAnAnswer(t *testing.T) {
	if _, err := ParseAnswer(strings.Repeat("x", 200), 100); err == nil {
		t.Error("an answer over the budget was accepted")
	}
	if _, err := ParseAnswer("   ", 100); err == nil {
		t.Error("an empty answer was accepted")
	}
}

// TestParseAnswerAnEmptyBlockIsNotAnExpression: a fence with nothing in it is
// formatting, not an answer, and the prose around it is what the model said.
func TestParseAnswerAnEmptyBlockIsNotAnExpression(t *testing.T) {
	p, err := ParseAnswer("Das geht nicht.\n```\n```", 1<<16)
	if err != nil {
		t.Fatalf("ParseAnswer: %v", err)
	}
	if p.Expression != "" || !strings.HasPrefix(p.Explanation, "Das geht nicht.") {
		t.Errorf("got %+v", p)
	}
}
