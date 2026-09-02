package playground

import (
	"fmt"
	"strings"
	"time"

	"github.com/pblumer/atlas/expr"
	"github.com/pblumer/atlas/model"
)

// maxRuleExamples bounds how many offending cases a rule names. A reader opens
// two or three of them; a run of fifty thousand that broke a rule everywhere
// would otherwise put fifty thousand indices into a response nobody reads.
const maxRuleExamples = 100

// endVariable and durationVariable are the two things the Playground binds into a
// rule's scope on top of the case's own variables.
//
// They shadow a case variable of the same name, deliberately: a rule is written
// against this vocabulary, and silently reading the model's own "end" instead
// would make the rule mean something other than what it says. The names are
// documented rather than obscured, because a rule an author cannot predict is
// worse than one they have to avoid a name for.
const (
	endVariable      = "end"
	durationVariable = "durationSeconds"
)

// Rule is an expectation stated per case rather than per run.
//
// It is the statement the run-wide expectations cannot make. "The median is under
// four hours" is true of a run; "an application under 50 000 from a grade-A
// customer is approved" is true of a *case*, and a run where nine tenths of the
// cases hold it is not nine tenths right — it is wrong for the tenth.
//
// Both halves are FEEL, the language the diagram's own gateways are written in,
// so an author states the rule the way the model states the decision.
type Rule struct {
	// Name labels the rule in the verdict. Empty reads the expressions back.
	Name string
	// When selects the cases the rule speaks about. Empty means every case.
	When string
	// Then is what those cases must show: their variables as the run left them,
	// plus "end" — the BPMN id of the last element the case reached — and
	// "durationSeconds", how long it took in simulated time.
	Then string
}

// Label is how the rule reads in a verdict.
func (r Rule) Label() string {
	if r.Name != "" {
		return r.Name
	}
	if r.When == "" {
		return "every case: " + r.Then
	}
	return r.When + " → " + r.Then
}

// RuleOutcome is what a run did about one rule.
type RuleOutcome struct {
	Rule Rule
	// Cases is how many the run had; Matched how many the When selected.
	Cases, Matched int
	// Satisfied and Violated split the matched, finished cases.
	Satisfied, Violated int
	// Undecided is matched cases that never reached an end, so the Then could not
	// be judged. They are neither: the run has not finished answering for them, and
	// counting them either way would state something the run did not show.
	Undecided int
	// Examples are the first offending cases, by their position in the dataset, so
	// a reader can go and look at one rather than at a number. Bounded; Truncated
	// says the run had more.
	Examples  []int
	Truncated bool
}

// Passed reports whether the run held the rule. Only a violation fails it: an
// undecided case is a case the run did not finish, which is what the completion
// expectation is for — failing here as well would report one problem twice, and
// under a name that does not describe it.
func (o RuleOutcome) Passed() bool { return o.Violated == 0 }

// Got is the outcome as a sentence, for a verdict somebody reads in a build log.
func (o RuleOutcome) Got() string {
	if o.Matched == 0 {
		return fmt.Sprintf("no case of %d matched", o.Cases)
	}
	var b strings.Builder
	fmt.Fprintf(&b, "%d of %d held", o.Satisfied, o.Matched)
	if o.Violated > 0 {
		fmt.Fprintf(&b, ", %d broke it", o.Violated)
	}
	if o.Undecided > 0 {
		fmt.Fprintf(&b, ", %d unfinished", o.Undecided)
	}
	return b.String()
}

// compiledRule is one rule ready to run: both halves compiled once, as invariant
// I5 asks, and evaluated per case with no re-parsing.
type compiledRule struct {
	rule       Rule
	when, then *expr.Compiled
}

// compileRules prepares the rules, refusing one that cannot be compiled. A rule
// with a typo in it has to fail at the point it is stated, not silently match
// nothing for the rest of the run.
func compileRules(rules []Rule) ([]compiledRule, error) {
	out := make([]compiledRule, 0, len(rules))
	for i, r := range rules {
		if strings.TrimSpace(r.Then) == "" {
			return nil, fmt.Errorf("playground: rule %d (%s) says nothing the case has to show", i+1, r.Label())
		}
		c := compiledRule{rule: r}
		var err error
		if strings.TrimSpace(r.When) != "" {
			if c.when, err = expr.CompileAuto(r.When); err != nil {
				return nil, fmt.Errorf("playground: rule %d selects cases with %q, which is not an expression: %w", i+1, r.When, err)
			}
		}
		if c.then, err = expr.CompileAuto(r.Then); err != nil {
			return nil, fmt.Errorf("playground: rule %d expects %q, which is not an expression: %w", i+1, r.Then, err)
		}
		out = append(out, c)
	}
	return out, nil
}

// JudgeRules measures every case against the rules, in one pass over the run.
//
// It is a pass of its own rather than part of [Sandbox.Report] because it costs
// what the report deliberately does not: the report reads each case's record,
// this reads its variables too. A run with no rules should not pay for that.
func (s *Sandbox) JudgeRules(rules []Rule) ([]RuleOutcome, error) {
	compiled, err := compileRules(rules)
	if err != nil {
		return nil, err
	}
	keys, err := s.caseKeyList()
	if err != nil {
		return nil, err
	}
	out := make([]RuleOutcome, len(compiled))
	for i := range compiled {
		out[i].Rule = compiled[i].rule
		out[i].Cases = len(keys)
	}
	if len(compiled) == 0 {
		return out, nil
	}

	// One scope, cleared per case: a rule pass over fifty thousand cases should not
	// leave fifty thousand maps behind for the collector.
	scope := make(map[string]expr.Value, 8)
	for i, key := range keys {
		pi, ok, err := s.store.ProcessInstance(key)
		if err != nil {
			return nil, fmt.Errorf("playground: read case %d: %w", key, err)
		}
		if !ok {
			continue
		}
		clear(scope)
		if err := s.store.VariablesOfScope(key, func(v *model.VariableValue) error {
			scope[v.Name] = expr.FromStored(exprKind(v.Kind), v.Bool, v.Text)
			return nil
		}); err != nil {
			return nil, fmt.Errorf("playground: read case variables: %w", err)
		}
		end, err := s.lastElement(key, s.byKey[pi.ProcessDefKey])
		if err != nil {
			return nil, err
		}
		done := pi.State == model.PICompleted
		scope[endVariable] = expr.String(end)
		scope[durationVariable] = expr.Number(int64(time.Duration(pi.CompletedAt-pi.CreatedAt) / time.Second))
		for j := range compiled {
			compiled[j].judge(scope, i, done, &out[j])
		}
	}
	return out, nil
}

// judge measures one case against one rule.
func (c compiledRule) judge(scope map[string]expr.Value, index int, done bool, out *RuleOutcome) {
	if c.when != nil {
		// A When that does not evaluate to true does not select the case. That is the
		// same reading a sequence-flow condition gets, and it is why a rule naming a
		// variable no case carries selects nothing rather than failing everything.
		v, err := c.when.Eval(scope)
		if err != nil || !expr.IsTrue(v) {
			return
		}
	}
	out.Matched++
	if !done {
		out.Undecided++
		return
	}
	v, err := c.then.Eval(scope)
	if err == nil && expr.IsTrue(v) {
		out.Satisfied++
		return
	}
	out.Violated++
	if len(out.Examples) < maxRuleExamples {
		out.Examples = append(out.Examples, index)
		return
	}
	out.Truncated = true
}

// exprKind maps a stored variable's kind onto the FEEL side. It mirrors the
// engine's own conversion; the two are a dozen lines and exporting one across a
// package boundary for this would tie the sandbox to the processor's internals.
func exprKind(k model.VarKind) expr.ValueKind {
	switch k {
	case model.VarBool:
		return expr.KindBool
	case model.VarNumber:
		return expr.KindNumber
	case model.VarString:
		return expr.KindString
	case model.VarJSON:
		return expr.KindJSON
	default:
		return expr.KindNull
	}
}
