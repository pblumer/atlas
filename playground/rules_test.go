package playground_test

import (
	"strings"
	"testing"
	"time"

	"github.com/pblumer/atlas/model"
	"github.com/pblumer/atlas/playground"
)

// creditCases builds n cases alternating between a small grade-A application and a
// large one, so a rule about the small ones has both kinds to sort through.
func creditCases(n int) [][]model.VariableValue {
	out := make([][]model.VariableValue, n)
	for i := range out {
		amount, grade := "900", "A"
		if i%2 == 1 {
			amount, grade = "40000", "B"
		}
		out[i] = []model.VariableValue{
			{Name: "amount", Kind: model.VarNumber, Text: amount},
			{Name: "risiko", Kind: model.VarString, Text: grade},
		}
	}
	return out
}

// The statement the run-wide expectations cannot make. "The median is under four
// hours" is true of a run; "a small grade-A application is paid out" is true of a
// case, and a run that holds it nine times in ten is not nine tenths right.
func TestARuleIsJudgedCaseByCase(t *testing.T) {
	sb := openSandbox(t, "exclusive-gateway.bpmn", playground.StubSet{
		Human: &playground.Stub{Min: time.Minute, Max: time.Minute},
	})
	runPlan(t, sb, playground.Plan{Cases: creditCases(10)})

	out, err := sb.JudgeRules([]playground.Rule{
		// The fixture routes amounts over 1000 to review and the rest to the payout,
		// so this holds for every case it selects.
		{Name: "small ones are paid out", When: `amount < 1000 and risiko = "A"`, Then: `end = "paid"`},
		// And this one is wrong on purpose: the large ones are reviewed, not paid.
		{When: "amount > 1000", Then: `end = "paid"`},
	})
	if err != nil {
		t.Fatalf("judge: %v", err)
	}
	if len(out) != 2 {
		t.Fatalf("outcomes = %d, want one per rule", len(out))
	}

	held := out[0]
	if held.Cases != 10 || held.Matched != 5 || held.Satisfied != 5 || held.Violated != 0 {
		t.Errorf("the true rule = %+v, want 5 of 10 matched and all of them held", held)
	}
	if !held.Passed() {
		t.Error("a rule every matching case held did not pass")
	}
	if len(held.Examples) != 0 {
		t.Errorf("a rule nothing broke named %v as offenders", held.Examples)
	}

	broken := out[1]
	if broken.Matched != 5 || broken.Violated != 5 || broken.Satisfied != 0 {
		t.Errorf("the false rule = %+v, want 5 matched and all of them broken", broken)
	}
	if broken.Passed() {
		t.Error("a rule every matching case broke still passed")
	}
	// It names the cases, so a reader can go and look at one rather than at a count.
	// They are the odd-numbered ones: those are the large applications.
	if len(broken.Examples) != 5 {
		t.Fatalf("examples = %v, want the five offending cases", broken.Examples)
	}
	for _, i := range broken.Examples {
		if i%2 != 1 {
			t.Errorf("case %d is named as an offender, but it is one of the small ones", i)
		}
	}
	if broken.Truncated {
		t.Error("five examples were reported as truncated")
	}
}

// A rule with no "when" speaks about every case, which is how "nothing ends in the
// error branch" is stated.
func TestARuleWithNoConditionSpeaksAboutEveryCase(t *testing.T) {
	sb := openSandbox(t, "exclusive-gateway.bpmn", playground.StubSet{
		Human: &playground.Stub{Min: time.Minute, Max: time.Minute},
	})
	runPlan(t, sb, playground.Plan{Cases: creditCases(6)})

	out, err := sb.JudgeRules([]playground.Rule{
		{Then: `end = "paid" or end = "reviewed"`},
		{Then: "durationSeconds < 3600"},
	})
	if err != nil {
		t.Fatalf("judge: %v", err)
	}
	if out[0].Matched != 6 || out[0].Satisfied != 6 {
		t.Errorf("outcome = %+v, want all six matched and holding", out[0])
	}
	// The Playground binds the case's own elapsed time too, so an SLA is a rule
	// rather than a run-wide percentile that hides which case broke it.
	if out[1].Satisfied != 6 {
		t.Errorf("duration rule = %+v, want six cases inside the hour", out[1])
	}
}

// A rule about an outcome cannot be decided for a case that has no outcome yet.
// Counting those as violations would fail the rule for a reason the completion
// expectation already reports, under a name that does not describe it.
func TestAnUnfinishedCaseLeavesARuleUndecided(t *testing.T) {
	// Nothing answers the user tasks, so every case parks at one.
	sb := openSandbox(t, "exclusive-gateway.bpmn", playground.StubSet{})
	runPlan(t, sb, playground.Plan{Cases: creditCases(4)})

	out, err := sb.JudgeRules([]playground.Rule{{Then: `end = "paid"`}})
	if err != nil {
		t.Fatalf("judge: %v", err)
	}
	got := out[0]
	if got.Matched != 4 || got.Undecided != 4 || got.Violated != 0 || got.Satisfied != 0 {
		t.Errorf("outcome = %+v, want four matched and all four undecided", got)
	}
	if !got.Passed() {
		t.Error("a rule failed on cases the run never finished; that is what the completion check is for")
	}
	if !strings.Contains(got.Got(), "unfinished") {
		t.Errorf("the outcome reads %q, which does not say the cases were unfinished", got.Got())
	}
}

// A condition naming a variable no case carries selects nothing rather than
// failing everything — the reading a sequence-flow condition already gets. The
// count says so, which is what stops it going quiet.
func TestAConditionNothingMatchesSelectsNothing(t *testing.T) {
	sb := openSandbox(t, "exclusive-gateway.bpmn", playground.StubSet{
		Human: &playground.Stub{Min: time.Minute, Max: time.Minute},
	})
	runPlan(t, sb, playground.Plan{Cases: creditCases(4)})

	out, err := sb.JudgeRules([]playground.Rule{{When: `waehrung = "CHF"`, Then: `end = "nowhere"`}})
	if err != nil {
		t.Fatalf("judge: %v", err)
	}
	if out[0].Matched != 0 || out[0].Violated != 0 {
		t.Errorf("outcome = %+v, want nothing matched", out[0])
	}
	if !strings.Contains(out[0].Got(), "no case of 4 matched") {
		t.Errorf("the outcome reads %q, which does not say it matched nothing", out[0].Got())
	}
}

// A rule that will not compile is refused where it is written. The alternative is
// a rule that silently matches nothing for the rest of the run — the one thing an
// assertion must never do.
func TestARuleThatWillNotCompileIsRefused(t *testing.T) {
	sb := openSandbox(t, "exclusive-gateway.bpmn", playground.StubSet{
		Human: &playground.Stub{Min: time.Minute, Max: time.Minute},
	})
	runPlan(t, sb, playground.Plan{Cases: creditCases(2)})

	for _, tc := range []struct {
		name string
		rule playground.Rule
		says string
	}{
		{"a condition that is not an expression", playground.Rule{When: "amount <", Then: "true"}, "not an expression"},
		{"an expectation that is not an expression", playground.Rule{Then: "end = "}, "not an expression"},
		{"an expectation that is empty", playground.Rule{When: "true"}, "says nothing"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := sb.JudgeRules([]playground.Rule{tc.rule})
			if err == nil {
				t.Fatal("a rule that cannot be evaluated was accepted")
			}
			if !strings.Contains(err.Error(), tc.says) {
				t.Errorf("error = %q, want it to say %q", err, tc.says)
			}
		})
	}
}

// The offending cases are a sample, not the run: a rule broken everywhere in fifty
// thousand cases must not put fifty thousand indices into a response nobody reads.
func TestTheOffendingCasesAreASample(t *testing.T) {
	sb := openSandbox(t, "exclusive-gateway.bpmn", playground.StubSet{
		Human: &playground.Stub{Min: time.Second, Max: time.Second},
	})
	runPlan(t, sb, playground.Plan{Cases: creditCases(220)})

	out, err := sb.JudgeRules([]playground.Rule{{Then: `end = "nowhere"`}})
	if err != nil {
		t.Fatalf("judge: %v", err)
	}
	got := out[0]
	if got.Violated != 220 {
		t.Fatalf("violated = %d, want every case", got.Violated)
	}
	if len(got.Examples) != 100 {
		t.Errorf("examples = %d, want the sample bounded at 100", len(got.Examples))
	}
	if !got.Truncated {
		t.Error("a bounded sample did not say the run had more")
	}
}

// A rule reads back as what it says when it has no name, so a verdict in a build
// log names the statement rather than "rule 2".
func TestARuleWithoutANameReadsAsItsOwnStatement(t *testing.T) {
	for _, tc := range []struct {
		rule playground.Rule
		want string
	}{
		{playground.Rule{Name: "small ones pay out", When: "a", Then: "b"}, "small ones pay out"},
		{playground.Rule{When: "amount < 1000", Then: `end = "paid"`}, `amount < 1000 → end = "paid"`},
		{playground.Rule{Then: "durationSeconds < 60"}, "every case: durationSeconds < 60"},
	} {
		if got := tc.rule.Label(); got != tc.want {
			t.Errorf("label = %q, want %q", got, tc.want)
		}
	}
}

// A run with no rules is judged as before and pays nothing for the pass it does
// not need.
func TestARunWithNoRulesIsJudgedAsBefore(t *testing.T) {
	sb := openSandbox(t, "exclusive-gateway.bpmn", playground.StubSet{
		Human: &playground.Stub{Min: time.Minute, Max: time.Minute},
	})
	runPlan(t, sb, playground.Plan{Cases: creditCases(2)})

	out, err := sb.JudgeRules(nil)
	if err != nil || len(out) != 0 {
		t.Errorf("outcomes = %v, err %v; want nothing judged", out, err)
	}
	rep, err := sb.Report()
	if err != nil {
		t.Fatalf("report: %v", err)
	}
	if v := (playground.Expectations{MinCompleted: 2}).Judge(rep, out); !v.Passed || len(v.Checks) != 1 {
		t.Errorf("verdict = %+v, want the one check the expectations asked for", v)
	}
}

// The rules join the verdict as checks, so one thing decides whether a build goes
// red — a panel showing a passing verdict beside a broken rule would be two
// answers to one question.
func TestABrokenRuleFailsTheVerdict(t *testing.T) {
	sb := openSandbox(t, "exclusive-gateway.bpmn", playground.StubSet{
		Human: &playground.Stub{Min: time.Minute, Max: time.Minute},
	})
	runPlan(t, sb, playground.Plan{Cases: creditCases(4)})

	out, err := sb.JudgeRules([]playground.Rule{{Name: "everything pays out", Then: `end = "paid"`}})
	if err != nil {
		t.Fatalf("judge: %v", err)
	}
	rep, err := sb.Report()
	if err != nil {
		t.Fatalf("report: %v", err)
	}
	v := (playground.Expectations{MinCompleted: 4}).Judge(rep, out)
	if v.Passed {
		t.Error("the verdict passed with a broken rule in it")
	}
	last := v.Checks[len(v.Checks)-1]
	if last.Name != "everything pays out" || last.Passed {
		t.Errorf("the rule's check = %+v, want it named and failed", last)
	}
	if !strings.Contains(last.Got, "broke it") {
		t.Errorf("the check reads %q, which does not say how many broke it", last.Got)
	}
}

// A rule reads the case's variables as the values they are, not as the text the
// results table renders them into: a number compares as a number, a flag as a
// boolean, and a structured value by its members. Reading them back as strings
// would make every numeric rule quietly false.
func TestARuleSeesTheVariablesAsTheirOwnKinds(t *testing.T) {
	sb := openSandbox(t, "exclusive-gateway.bpmn", playground.StubSet{
		Human: &playground.Stub{Min: time.Minute, Max: time.Minute},
	})
	runPlan(t, sb, playground.Plan{Cases: [][]model.VariableValue{{
		{Name: "amount", Kind: model.VarNumber, Text: "900"},
		{Name: "express", Kind: model.VarBool, Bool: true},
		{Name: "kunde", Kind: model.VarString, Text: "A"},
		{Name: "adresse", Kind: model.VarJSON, Text: `{"ort":"Bern"}`},
		{Name: "notiz", Kind: model.VarNull},
	}}})

	out, err := sb.JudgeRules([]playground.Rule{
		{Name: "number", When: "amount < 1000", Then: "amount + 100 = 1000"},
		{Name: "boolean", When: "express", Then: `end = "paid"`},
		{Name: "string", When: `kunde = "A"`, Then: `end != ""`},
		{Name: "structured", When: `adresse.ort = "Bern"`, Then: "true"},
		{Name: "null", When: "notiz = null", Then: "true"},
	})
	if err != nil {
		t.Fatalf("judge: %v", err)
	}
	for _, o := range out {
		if o.Matched != 1 || o.Satisfied != 1 {
			t.Errorf("the %s rule = %+v, want the case matched and holding", o.Rule.Name, o)
		}
	}
}
