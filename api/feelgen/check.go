package feelgen

import (
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/pblumer/atlas/expr"
)

// Check is what the engine said about a proposal: whether it compiles into something
// this build can run, what it returns for the example, and whether that is what the
// model claimed it returns.
//
// It is the reason this feature can run on a free model. A model's FEEL is a guess at a
// dialect it learnt from somebody else's documentation; the check is the engine that
// will actually run the expression saying whether the guess was right, before the author
// sees it — and, when it was not, in words the next correction round can act on.
type Check struct {
	// OK is true when the expression compiled, every call in it is one this build
	// can make (ADR-0388), and it evaluated. It says nothing about whether the result
	// is the right one; Matches does.
	OK bool `json:"ok"`
	// Result and Kind are the value as the FEEL evaluate route reports it: the
	// canonical text and its kind's label (expr.ValueKind.Label).
	Result string `json:"result,omitempty"`
	Kind   string `json:"kind,omitempty"`
	Error  string `json:"error,omitempty"`
	// Fault is the kind of failure when OK is false: FaultCompile, FaultCalls or
	// FaultEvaluate. Calls names the callees a FaultCalls refused; it is reported to
	// whoever measures the prompt (Outcome), not to the console, whose author reads
	// the same names in Error.
	Fault string   `json:"fault,omitempty"`
	Calls []string `json:"-"`
	// Inputs are the variables the expression reads, and Missing those of them the
	// example does not bind. A missing input reads as null, which makes the example
	// prove nothing about it.
	Inputs  []string `json:"inputs,omitempty"`
	Missing []string `json:"missing,omitempty"`
	// Expected is what the model said the expression returns, as JSON text, and
	// Matches whether the engine agrees. Both are absent when it said nothing.
	Expected string `json:"expected,omitempty"`
	Matches  *bool  `json:"matches,omitempty"`
}

// Evaluate checks an expression against example variables, and against the result the
// model stated when hasExpected is true. vars is decoded JSON with exact numbers
// (json.Number), the shape the request and [ParseAnswer] both produce.
//
// It is a pure compile and evaluation over the caller's values — no engine state is read
// or written — exactly as the FEEL evaluate route is, and it refuses what the FEEL
// validate route refuses, so the check an answer passed is the one the author's own
// editor would have run.
func Evaluate(expression string, vars map[string]any, expected any, hasExpected bool) Check {
	if strings.TrimSpace(expression) == "" {
		return Check{Error: "the expression is empty", Fault: FaultEmpty}
	}
	compiled, err := expr.CompileAuto(expression)
	if err != nil {
		return Check{Error: "it does not compile: " + err.Error(), Fault: FaultCompile}
	}
	// The faults one by one rather than CheckCallsError's single sentence, because the
	// callee names are worth more apart than joined: they say which foreign functions a
	// model reaches for. The sentence is put together exactly as CheckCallsError does.
	if refused := expr.CheckCalls(expression); len(refused) > 0 {
		c := Check{Fault: FaultCalls}
		msgs := make([]string, len(refused))
		for i, f := range refused {
			msgs[i] = f.Message
			c.Calls = append(c.Calls, f.Name)
		}
		c.Error = strings.Join(msgs, " ")
		return c
	}
	c := Check{Inputs: compiled.Inputs()}
	bindings := make(map[string]expr.Value, len(vars))
	for name, raw := range vars {
		bindings[name] = expr.FromJSON(raw)
	}
	for _, in := range c.Inputs {
		if _, ok := vars[in]; !ok {
			c.Missing = append(c.Missing, in)
		}
	}
	v, err := compiled.Eval(bindings)
	if err != nil {
		c.Error, c.Fault = "it does not evaluate: "+err.Error(), FaultEvaluate
		return c
	}
	kind, b, text := expr.Classify(v)
	switch kind {
	case expr.KindBool:
		text = strconv.FormatBool(b)
	case expr.KindNull:
		text = "null"
	}
	c.OK, c.Result, c.Kind = true, text, kind.Label()
	if hasExpected {
		stated, _ := json.Marshal(expected)
		agrees := matches(v, kind, expected)
		c.Expected, c.Matches = string(stated), &agrees
	}
	return c
}

// comparisons are how a stated result is held against the computed one, in order, each
// with r bound to the result and e to the stated value. The first is FEEL's own
// equality, which is the one that matters: 150 and 150.0 are equal, and so are two
// lists or contexts with equal members. The others exist because JSON has no date or
// duration — a model states "2024-03-02" or "P1D" as text — so a text is also read as
// each temporal type, and as the text of the result, before the two are called
// different.
//
// They are compiled per check rather than once per process: a check is a handful of
// microseconds next to a model call of seconds, and a compiled expression shared
// between concurrent requests would be a property of the FEEL engine this package
// has no business relying on.
var comparisons = []string{
	`r = e`,
	`r = date(e)`,
	`r = date and time(e)`,
	`r = time(e)`,
	`r = duration(e)`,
	`string(r) = e`,
}

// matches reports whether the computed value v (of kind) is the stated one.
//
// Null is settled before any comparison runs, because every text-reading comparison
// turns an unreadable text into null — date("nonsense") is null — and null = null is
// true in FEEL. A null result therefore agrees only with a stated null, and a stated
// null only with a null result.
func matches(v expr.Value, kind expr.ValueKind, expected any) bool {
	if expected == nil || kind == expr.KindNull {
		return expected == nil && kind == expr.KindNull
	}
	_, isText := expected.(string)
	scope := map[string]expr.Value{"r": v, "e": expr.FromJSON(expected)}
	for i, src := range comparisons {
		if i > 0 && !isText {
			break
		}
		cmp, err := expr.Compile(src, "r", "e")
		if err != nil {
			continue
		}
		out, err := cmp.Eval(scope)
		if err != nil {
			continue
		}
		if k, b, _ := expr.Classify(out); k == expr.KindBool && b {
			return true
		}
	}
	return false
}

// settled reports whether a proposal can go to the author as it is, which is when the
// correction rounds stop. A question back is settled: the model has answered, with a
// question. An expression is settled when it runs, binds every input it reads, and —
// when the model said what it returns — returns that.
func settled(p Proposal, c Check) bool {
	if p.Expression == "" {
		return p.Explanation != ""
	}
	return c.OK && len(c.Missing) == 0 && (c.Matches == nil || *c.Matches)
}

// problem is the sentence a correction round puts in front of the model: what the engine
// found, in terms of what the model wrote. It is only ever asked about a proposal that
// is not settled.
func problem(p Proposal, c Check) string {
	switch {
	case p.Expression == "":
		return "Your answer had neither an expression nor an explanation."
	case !c.OK:
		return "The expression does not work in Atlas: " + c.Error
	case len(c.Missing) > 0:
		missing := append([]string(nil), c.Missing...)
		sort.Strings(missing)
		return fmt.Sprintf("The expression reads %s, which your example variables do not set. "+
			"Give every variable it reads a value in \"variables\" (null only where null is the point).",
			strings.Join(missing, ", "))
	default:
		return fmt.Sprintf("With your example variables the expression returns %s (%s), but you said it returns %s. "+
			"One of the two is wrong: correct whichever it is.", c.Result, c.Kind, c.Expected)
	}
}
