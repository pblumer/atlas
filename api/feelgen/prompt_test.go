package feelgen

import (
	"strings"
	"testing"

	"github.com/pblumer/atlas/expr"
)

// The prompt is a specification of the FEEL this build speaks, written for a reader who
// learnt FEEL from somebody else's documentation. A specification that disagrees with
// the engine teaches the model to be wrong with confidence, so every claim it makes about
// the language is held against the engine here — the same idea as form generation's
// TestTheContractOffersOnlyWhatTheGateAccepts (ADR-0260), applied to a language.

// TestThePromptOffersExactlyTheBuiltins: the function list is the registry's own,
// complete, and nothing the prompt names as unavailable is something this build has.
func TestThePromptOffersExactlyTheBuiltins(t *testing.T) {
	prompt := systemPrompt()
	i := strings.Index(prompt, builtinsHeading)
	if i < 0 {
		t.Fatalf("the prompt has no function list (%q)", builtinsHeading)
	}
	listed := strings.Split(strings.TrimSpace(prompt[i+len(builtinsHeading):]), ", ")
	want := expr.BuiltinNames()
	if strings.Join(listed, "|") != strings.Join(want, "|") {
		t.Errorf("the prompt lists %d functions, the engine has %d:\nprompt: %v\nengine: %v", len(listed), len(want), listed, want)
	}
	have := map[string]bool{}
	for _, n := range want {
		have[n] = true
	}
	for _, name := range unavailable {
		if have[name] {
			t.Errorf("the prompt calls %q unavailable, but this build has it", name)
		}
		if !strings.Contains(prompt, name) {
			t.Errorf("%q is declared unavailable but the prompt never says so", name)
		}
	}
}

// TestThePromptsRulesHoldInThisEngine evaluates every claim the prompt's rules make. A
// rule that stops holding — the engine changed, or the prompt was edited carelessly —
// fails here rather than in front of an author.
func TestThePromptsRulesHoldInThisEngine(t *testing.T) {
	for _, r := range ruleClaims {
		t.Run(r.expr, func(t *testing.T) {
			if !strings.Contains(systemPrompt(), r.quoted) {
				t.Errorf("the claim %q is tested but no longer in the prompt", r.quoted)
			}
			c := Evaluate(r.expr, r.vars, nil, false)
			if !c.OK {
				t.Fatalf("does not evaluate: %s", c.Error)
			}
			if c.Result != r.want {
				t.Errorf("= %s (%s), the prompt says %s", c.Result, c.Kind, r.want)
			}
		})
	}
}

// ruleClaims are the prompt's statements about the language, as expressions and the
// result the prompt says they have. quoted is the prompt text that makes the claim.
var ruleClaims = []struct {
	quoted string
	expr   string
	vars   map[string]any
	want   string
}{
	{`"Nr. " + string(n)`, `"Nr. " + string(n)`, map[string]any{"n": num("7")}, "Nr. 7"},
	{`a string and a number gives null`, `"Nr. " + n`, map[string]any{"n": num("7")}, "null"},
	{`compare lower case(s)`, `"Gold" = "gold"`, nil, "false"},
	{`When c is null, the else branch is taken`, `if x > 1 then "a" else "b"`, map[string]any{"x": nil}, "b"},
	{`xs[1] is the first element`, `xs[1]`, map[string]any{"xs": []any{num("4"), num("5")}}, "4"},
	{`xs[-1] the last`, `xs[-1]`, map[string]any{"xs": []any{num("4"), num("5")}}, "5"},
	{`xs[0] is null`, `xs[0]`, map[string]any{"xs": []any{num("4"), num("5")}}, "null"},
	{`xs[item > 10]`, `xs[item > 10]`, map[string]any{"xs": []any{num("4"), num("15")}}, "[15]"},
	{`orders[amount > 100]`, `orders[amount > 100]`, map[string]any{"orders": []any{map[string]any{"amount": num("50")}, map[string]any{"amount": num("150")}}}, `[{"amount":150}]`},
	{`orders.amount`, `orders.amount`, map[string]any{"orders": []any{map[string]any{"amount": num("50")}, map[string]any{"amount": num("150")}}}, "[50,150]"},
	{`for x in xs return x * 2`, `for x in xs return x * 2`, map[string]any{"xs": []any{num("1"), num("2")}}, "[2,4]"},
	{`some x in xs satisfies x > 10`, `some x in xs satisfies x > 10`, map[string]any{"xs": []any{num("1"), num("20")}}, "true"},
	{`every x in xs satisfies x > 10`, `every x in xs satisfies x > 10`, map[string]any{"xs": []any{num("1"), num("20")}}, "false"},
	{`customer.address.city`, `customer.address.city`, map[string]any{"customer": map[string]any{"address": map[string]any{"city": "Bern"}}}, "Bern"},
	{`A missing field or a missing variable reads as null`, `customer.address.city`, map[string]any{"customer": map[string]any{}}, "null"},
	{`{name: "Bern", count: 3}`, `{name: "Bern", count: 3}`, nil, `{"count":3,"name":"Bern"}`},
	{`true and null is null`, `true and null`, nil, "null"},
	{`false and null is false`, `false and null`, nil, "false"},
	{`arithmetic and comparisons with null give null`, `x > 3`, map[string]any{"x": nil}, "null"},
	{`Instead of is defined(x) write x != null`, `x != null`, map[string]any{"x": nil}, "false"},
	{`instead of get or else(x, d) write if x = null then d else x`, `if x = null then d else x`, map[string]any{"x": nil, "d": num("1")}, "1"},
	{`instead of trim(s) write replace(s, "^\s+|\s+$", "")`, `replace(s, "^\s+|\s+$", "")`, map[string]any{"s": "  a b  "}, "a b"},
	{`of an empty list are null, not 0`, `sum(xs)`, map[string]any{"xs": []any{}}, "null"},
	{`count of an empty list is 0`, `count(xs)`, map[string]any{"xs": []any{}}, "0"},
	{`if count(xs) = 0 then 0 else sum(xs)`, `if count(xs) = 0 then 0 else sum(xs)`, map[string]any{"xs": []any{}}, "0"},
	{`Division by zero is null`, `1 / 0`, nil, "null"},
	{`0.1 + 0.2 = 0.3 is true`, `0.1 + 0.2 = 0.3`, nil, "true"},
	{`decimal(2.345, 2) = 2.34`, `decimal(2.345, 2)`, nil, "2.34"},
	{`round half up(n, scale)`, `round half up(2.345, 2)`, nil, "2.35"},
	{`must be converted with date(x)`, `date(d) < date("2024-04-01")`, map[string]any{"d": "2024-03-01"}, "true"},
	{`A date minus a date is a duration`, `date("2024-03-02") - date("2024-03-01") = duration("P1D")`, nil, "true"},
	{`date + duration("P1D") is a date`, `date("2024-03-01") + duration("P1D") = date("2024-03-02")`, nil, "true"},
	{`years and months duration(from, to)`, `years and months duration(date("1990-05-17"), date("2026-10-02")).years`, nil, "36"},
	{`x between 1 and 10`, `x between 1 and 10`, map[string]any{"x": num("10")}, "true"},
	{`x in [1..10]`, `x in [1..10]`, map[string]any{"x": num("10")}, "true"},
	{`x in (0..1]`, `x in (0..1]`, map[string]any{"x": num("0")}, "false"},
	{`x in ["gold", "silver"]`, `x in ["gold", "silver"]`, map[string]any{"x": "silver"}, "true"},
	{`day of week returns the English name`, `day of week(date("2026-10-02"))`, nil, "Friday"},
}

// TestTheExamplesPassTheirOwnCheck: the worked examples are what the model imitates
// most closely, so each one is put through exactly the check a real answer gets.
func TestTheExamplesPassTheirOwnCheck(t *testing.T) {
	if len(examples) < 2 {
		t.Fatalf("only %d examples", len(examples))
	}
	for _, ex := range examples {
		t.Run(ex.request, func(t *testing.T) {
			if !strings.Contains(systemPrompt(), ex.answer) {
				t.Error("the example is not in the prompt")
			}
			p, err := ParseAnswer(ex.answer, 1<<16)
			if err != nil {
				t.Fatalf("ParseAnswer: %v", err)
			}
			c := Evaluate(p.Expression, p.Variables, p.Expected, p.HasExpected)
			if !settled(p, c) {
				t.Errorf("the example fails its own check: %+v", c)
			}
			if !p.HasExpected || len(p.Variables) == 0 || p.Explanation == "" {
				t.Errorf("the example does not show the whole contract: %+v", p)
			}
		})
	}
}

// TestGoalPromptCarriesTheConversationAndTheEditor: everything the author can see is
// something the model is told — the conversation, and what is in the editor now, which
// the author may have changed by hand since the last answer.
func TestGoalPromptCarriesTheConversationAndTheEditor(t *testing.T) {
	goal := goalPrompt([]Turn{
		{Role: "user", Content: "Rabatt von 10 %"},
		{Role: "assistant", Content: `{"expression":"total * 0.1"}`},
		{Role: "user", Content: "Nur über 1000"},
	}, "total * 0.15", `{"total": 1200}`, "Bedingung eines Gateways")
	for _, want := range []string{
		"Author: Rabatt von 10 %",
		`You: {"expression":"total * 0.1"}`,
		"Author: Nur über 1000",
		"total * 0.15",
		`{"total": 1200}`,
		"Bedingung eines Gateways",
	} {
		if !strings.Contains(goal, want) {
			t.Errorf("the goal prompt lacks %q:\n%s", want, goal)
		}
	}
	// The last message is the one to answer, so it is the last thing in the transcript.
	if strings.Index(goal, "Nur über 1000") < strings.Index(goal, "Rabatt von 10 %") {
		t.Error("the conversation is out of order")
	}

	// With nothing in the editor and nowhere named, those sections are absent rather
	// than present and empty: an empty heading is a question the model tries to answer.
	bare := goalPrompt([]Turn{{Role: "user", Content: "x > 1"}}, "", "", "")
	for _, absent := range []string{editorHeading, variablesHeading, targetHeading} {
		if strings.Contains(bare, absent) {
			t.Errorf("an empty section %q is in the prompt", absent)
		}
	}
}

// TestRepairPromptShowsTheFailedAnswerAndTheVerdict: a correction round sees what it
// wrote and what the engine said about it, and is asked for the whole answer again.
func TestRepairPromptShowsTheFailedAnswerAndTheVerdict(t *testing.T) {
	got := repairPrompt("BASE", `{"expression":"trim(x)"}`, "write `replace(x, …)`")
	for _, want := range []string{"BASE", `{"expression":"trim(x)"}`, "write `replace(x, …)`", "whole JSON object"} {
		if !strings.Contains(got, want) {
			t.Errorf("the repair prompt lacks %q:\n%s", want, got)
		}
	}
}
