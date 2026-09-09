package taskfolder

import (
	"strings"
	"testing"
	"time"

	"github.com/pblumer/atlas/expr"
)

// TestRuleFEELIsGenerated pins the expression each condition shape produces. The
// generated text is what the editor shows the person building the folder, so it
// is part of the contract, not an implementation detail.
func TestRuleFEELIsGenerated(t *testing.T) {
	cases := []struct {
		name string
		cond Condition
		want string
	}{
		{"process is", Condition{Field: FieldProcess, Op: OpIs, Value: "kunden-anfrage"},
			`processId = "kunden-anfrage"`},
		{"process is not", Condition{Field: FieldProcess, Op: OpIsNot, Value: "x"},
			`processId != "x"`},
		{"process is one of", Condition{Field: FieldProcess, Op: OpIsOneOf, Values: []string{"a", "b"}},
			`processId in ("a", "b")`},
		{"task name is", Condition{Field: FieldTaskName, Op: OpIs, Value: "Anfrage sichten"},
			`taskName = "Anfrage sichten"`},
		{"task name contains", Condition{Field: FieldTaskName, Op: OpContains, Value: "Anfrage"},
			`contains(taskName, "Anfrage")`},
		{"task name starts with", Condition{Field: FieldTaskName, Op: OpStartsWith, Value: "An"},
			`starts with(taskName, "An")`},
		{"assignee is me", Condition{Field: FieldAssignee, Op: OpIsMe},
			`assignee = user.name`},
		{"assignee is empty", Condition{Field: FieldAssignee, Op: OpIsEmpty},
			`assignee = null`},
		{"assignee is", Condition{Field: FieldAssignee, Op: OpIs, Value: "nadja"},
			`assignee = "nadja"`},
		{"assignee is not", Condition{Field: FieldAssignee, Op: OpIsNot, Value: "nadja"},
			`assignee != "nadja"`},
		{"group is", Condition{Field: FieldGroup, Op: OpIs, Value: "service-desk"},
			`candidateGroups = "service-desk"`},
		{"group is one of", Condition{Field: FieldGroup, Op: OpIsOneOf, Values: []string{"a", "b"}},
			`candidateGroups in ("a", "b")`},
		{"lane is", Condition{Field: FieldLane, Op: OpIs, Value: "Service Desk"},
			`lane = "Service Desk"`},
		{"lane under", Condition{Field: FieldLane, Op: OpUnder, Value: "Kundenservice"},
			`list contains(lanePath, "Kundenservice")`},
		{"priority at least", Condition{Field: FieldPriority, Op: OpAtLeast, Value: "70"},
			`priority >= 70`},
		{"priority at most", Condition{Field: FieldPriority, Op: OpAtMost, Value: "30"},
			`priority <= 30`},
		{"priority is", Condition{Field: FieldPriority, Op: OpIs, Value: "50"},
			`priority = 50`},
		{"due overdue", Condition{Field: FieldDue, Op: OpOverdue},
			`dueDate != null and dueDate < scanAt`},
		{"due within", Condition{Field: FieldDue, Op: OpWithin, Value: "P3D"},
			`dueDate != null and dueDate < scanAt + duration("P3D")`},
		{"due none", Condition{Field: FieldDue, Op: OpNone}, `dueDate = null`},
		{"due any", Condition{Field: FieldDue, Op: OpAny}, `dueDate != null`},
		{"instance older than days", Condition{Field: FieldInstanceAge, Op: OpOlderThan, Value: "2", Unit: UnitDays},
			`instanceCreatedAt < scanAt - duration("P2D")`},
		{"instance newer than hours", Condition{Field: FieldInstanceAge, Op: OpNewerThan, Value: "4", Unit: UnitHours},
			`instanceCreatedAt > scanAt - duration("PT4H")`},
		{"has form", Condition{Field: FieldForm, Op: OpHas}, `hasForm`},
		{"has no form", Condition{Field: FieldForm, Op: OpHasNot}, `not(hasForm)`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := Rule{Match: MatchAll, Conditions: []Condition{tc.cond}}
			if err := r.Validate(); err != nil {
				t.Fatalf("Validate: %v", err)
			}
			if got := r.FEEL(); got != tc.want {
				t.Errorf("FEEL()\n got %q\nwant %q", got, tc.want)
			}
			if _, err := Compile(r); err != nil {
				t.Errorf("Compile: %v", err)
			}
		})
	}
}

// TestRuleFEELJoinsConditions covers the two match modes and the parenthesising a
// multi-clause condition needs once it sits beside another one.
func TestRuleFEELJoinsConditions(t *testing.T) {
	all := Rule{Match: MatchAll, Conditions: []Condition{
		{Field: FieldProcess, Op: OpIsOneOf, Values: []string{"kunden-anfrage", "service-desk-ticket"}},
		{Field: FieldAssignee, Op: OpIsEmpty},
		{Field: FieldPriority, Op: OpAtLeast, Value: "50"},
	}}
	want := "processId in (\"kunden-anfrage\", \"service-desk-ticket\")\n" +
		"  and assignee = null\n" +
		"  and priority >= 50"
	if got := all.FEEL(); got != want {
		t.Errorf("all:\n got %q\nwant %q", got, want)
	}

	any := Rule{Match: MatchAny, Conditions: []Condition{
		{Field: FieldDue, Op: OpOverdue},
		{Field: FieldPriority, Op: OpAtLeast, Value: "70"},
	}}
	wantAny := "(dueDate != null and dueDate < scanAt)\n  or priority >= 70"
	if got := any.FEEL(); got != wantAny {
		t.Errorf("any:\n got %q\nwant %q", got, wantAny)
	}

	empty := Rule{Match: MatchAll}
	if got := empty.FEEL(); got != "true" {
		t.Errorf("empty rule FEEL() = %q, want %q", got, "true")
	}
}

// TestRuleFEELEscapesValues is the injection test: a value carrying a quote or a
// backslash must stay inside its string literal, so a folder name cannot smuggle
// an expression into the rule it generates.
func TestRuleFEELEscapesValues(t *testing.T) {
	r := Rule{Match: MatchAll, Conditions: []Condition{
		{Field: FieldTaskName, Op: OpIs, Value: `a" or true or "b`},
	}}
	got := r.FEEL()
	if want := `taskName = "a\" or true or \"b"`; got != want {
		t.Fatalf("FEEL() = %q, want %q", got, want)
	}
	m, err := Compile(r)
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	if m.Match(Task{TaskName: "anything"}, User{}, time.Now()) {
		t.Error("an escaped value matched a task it does not name — the quote escaped its literal")
	}
	if !m.Match(Task{TaskName: `a" or true or "b`}, User{}, time.Now()) {
		t.Error("the escaped value did not match the task actually carrying it")
	}
	if _, err := Compile(Rule{Match: MatchAll, Conditions: []Condition{
		{Field: FieldTaskName, Op: OpIs, Value: `back\slash`},
	}}); err != nil {
		t.Errorf("a backslash value did not compile: %v", err)
	}
}

// TestRuleValidateRejects covers every way the editor (or a hand-written request)
// can hand over a rule the server must refuse. A rule that reaches Compile has to
// be known-good: it is compiled once at save time and evaluated per task.
func TestRuleValidateRejects(t *testing.T) {
	cases := []struct {
		name string
		rule Rule
		want string
	}{
		{"unknown match", Rule{Match: "some", Conditions: []Condition{{Field: FieldForm, Op: OpHas}}}, "match"},
		{"unknown field", Rule{Match: MatchAll, Conditions: []Condition{{Field: "colour", Op: OpIs, Value: "red"}}}, "field"},
		{"operator not on field", Rule{Match: MatchAll, Conditions: []Condition{{Field: FieldForm, Op: OpContains, Value: "x"}}}, "operator"},
		{"missing value", Rule{Match: MatchAll, Conditions: []Condition{{Field: FieldProcess, Op: OpIs}}}, "value"},
		{"blank value", Rule{Match: MatchAll, Conditions: []Condition{{Field: FieldProcess, Op: OpIs, Value: "   "}}}, "value"},
		{"empty choice list", Rule{Match: MatchAll, Conditions: []Condition{{Field: FieldProcess, Op: OpIsOneOf}}}, "value"},
		{"blank in choice list", Rule{Match: MatchAll, Conditions: []Condition{{Field: FieldProcess, Op: OpIsOneOf, Values: []string{"a", " "}}}}, "value"},
		{"priority not a number", Rule{Match: MatchAll, Conditions: []Condition{{Field: FieldPriority, Op: OpAtLeast, Value: "hoch"}}}, "number"},
		{"priority out of range", Rule{Match: MatchAll, Conditions: []Condition{{Field: FieldPriority, Op: OpAtLeast, Value: "500"}}}, "0 and 100"},
		{"bad duration", Rule{Match: MatchAll, Conditions: []Condition{{Field: FieldDue, Op: OpWithin, Value: "3 Tage"}}}, "duration"},
		{"bad age unit", Rule{Match: MatchAll, Conditions: []Condition{{Field: FieldInstanceAge, Op: OpOlderThan, Value: "2", Unit: "w"}}}, "unit"},
		{"age not a number", Rule{Match: MatchAll, Conditions: []Condition{{Field: FieldInstanceAge, Op: OpOlderThan, Value: "zwei", Unit: UnitDays}}}, "number"},
		{"control character", Rule{Match: MatchAll, Conditions: []Condition{{Field: FieldTaskName, Op: OpIs, Value: "a\nb"}}}, "control"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.rule.Validate()
			if err == nil {
				t.Fatalf("Validate() accepted %+v", tc.rule)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("Validate() = %q, want it to mention %q", err, tc.want)
			}
		})
	}
}

// TestRuleValidateBounds covers the two size limits: a rule cannot carry an
// unbounded number of conditions, nor a choice list of unbounded length.
func TestRuleValidateBounds(t *testing.T) {
	many := Rule{Match: MatchAll}
	for i := 0; i <= maxConditions; i++ {
		many.Conditions = append(many.Conditions, Condition{Field: FieldForm, Op: OpHas})
	}
	if err := many.Validate(); err == nil {
		t.Error("Validate() accepted more conditions than the limit")
	}
	long := Condition{Field: FieldProcess, Op: OpIsOneOf}
	for i := 0; i <= maxChoices; i++ {
		long.Values = append(long.Values, "p")
	}
	if err := (Rule{Match: MatchAll, Conditions: []Condition{long}}).Validate(); err == nil {
		t.Error("Validate() accepted a longer choice list than the limit")
	}
	if err := (Rule{Match: MatchAll, Conditions: []Condition{
		{Field: FieldTaskName, Op: OpIs, Value: strings.Repeat("x", maxValueLen+1)},
	}}).Validate(); err == nil {
		t.Error("Validate() accepted an over-long value")
	}
}

// TestMatcherMatches drives the compiled rule over tasks — the actual filter, on
// the shapes the editor produces.
func TestMatcherMatches(t *testing.T) {
	now := time.Date(2026, 9, 7, 12, 0, 0, 0, time.UTC)
	base := Task{
		ProcessID: "kunden-anfrage", TaskName: "Anfrage sichten",
		CandidateGroups: "kundenservice", Lane: "Team Lead",
		LanePath: []string{"Kundenservice", "Team Lead"},
		Priority: 75, DueDate: now.Add(-2 * time.Hour).UnixMilli(),
		InstanceCreatedAt: now.Add(-72 * time.Hour).UnixMilli(), HasForm: true,
	}
	me := User{ID: "usr_1", Name: "patrick", Groups: []string{"kundenservice"}}

	cases := []struct {
		name string
		rule Rule
		task Task
		want bool
	}{
		{"process matches", Rule{Match: MatchAll, Conditions: []Condition{
			{Field: FieldProcess, Op: OpIs, Value: "kunden-anfrage"}}}, base, true},
		{"process does not match", Rule{Match: MatchAll, Conditions: []Condition{
			{Field: FieldProcess, Op: OpIs, Value: "onboarding"}}}, base, false},
		{"unassigned", Rule{Match: MatchAll, Conditions: []Condition{
			{Field: FieldAssignee, Op: OpIsEmpty}}}, base, true},
		{"assigned to me", Rule{Match: MatchAll, Conditions: []Condition{
			{Field: FieldAssignee, Op: OpIsMe}}}, withAssignee(base, "patrick"), true},
		{"assigned to someone else", Rule{Match: MatchAll, Conditions: []Condition{
			{Field: FieldAssignee, Op: OpIsMe}}}, withAssignee(base, "nadja"), false},
		{"lane under an outer lane", Rule{Match: MatchAll, Conditions: []Condition{
			{Field: FieldLane, Op: OpUnder, Value: "Kundenservice"}}}, base, true},
		{"overdue", Rule{Match: MatchAll, Conditions: []Condition{
			{Field: FieldDue, Op: OpOverdue}}}, base, true},
		{"no due date is not overdue", Rule{Match: MatchAll, Conditions: []Condition{
			{Field: FieldDue, Op: OpOverdue}}}, withDue(base, 0), false},
		{"no due date is none", Rule{Match: MatchAll, Conditions: []Condition{
			{Field: FieldDue, Op: OpNone}}}, withDue(base, 0), true},
		{"due within a week", Rule{Match: MatchAll, Conditions: []Condition{
			{Field: FieldDue, Op: OpWithin, Value: "P7D"}}}, withDue(base, now.Add(48*time.Hour).UnixMilli()), true},
		{"due beyond a week", Rule{Match: MatchAll, Conditions: []Condition{
			{Field: FieldDue, Op: OpWithin, Value: "P7D"}}}, withDue(base, now.Add(240*time.Hour).UnixMilli()), false},
		{"instance older than two days", Rule{Match: MatchAll, Conditions: []Condition{
			{Field: FieldInstanceAge, Op: OpOlderThan, Value: "2", Unit: UnitDays}}}, base, true},
		{"instance not older than five days", Rule{Match: MatchAll, Conditions: []Condition{
			{Field: FieldInstanceAge, Op: OpOlderThan, Value: "5", Unit: UnitDays}}}, base, false},
		{"has a form", Rule{Match: MatchAll, Conditions: []Condition{
			{Field: FieldForm, Op: OpHas}}}, base, true},
		{"all of three", Rule{Match: MatchAll, Conditions: []Condition{
			{Field: FieldProcess, Op: OpIs, Value: "kunden-anfrage"},
			{Field: FieldAssignee, Op: OpIsEmpty},
			{Field: FieldPriority, Op: OpAtLeast, Value: "50"}}}, base, true},
		{"all of three, one fails", Rule{Match: MatchAll, Conditions: []Condition{
			{Field: FieldProcess, Op: OpIs, Value: "kunden-anfrage"},
			{Field: FieldAssignee, Op: OpIsEmpty},
			{Field: FieldPriority, Op: OpAtLeast, Value: "90"}}}, base, false},
		{"any of two", Rule{Match: MatchAny, Conditions: []Condition{
			{Field: FieldProcess, Op: OpIs, Value: "onboarding"},
			{Field: FieldPriority, Op: OpAtLeast, Value: "70"}}}, base, true},
		{"any of two, neither", Rule{Match: MatchAny, Conditions: []Condition{
			{Field: FieldProcess, Op: OpIs, Value: "onboarding"},
			{Field: FieldPriority, Op: OpAtLeast, Value: "90"}}}, base, false},
		{"no conditions matches everything", Rule{Match: MatchAll}, base, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m, err := Compile(tc.rule)
			if err != nil {
				t.Fatalf("Compile: %v", err)
			}
			if got := m.Match(tc.task, me, now); got != tc.want {
				t.Errorf("Match() = %v, want %v (FEEL: %s)", got, tc.want, tc.rule.FEEL())
			}
		})
	}
}

// TestMatcherJudgesAgainstTheGivenMoment pins the parameter Match takes. The
// generated FEEL used to call now(), which reads the real clock, so the moment
// the caller passed was accepted and ignored: a scan judged each row against its
// own instant, and a test could anchor whatever date it liked without that date
// deciding anything. Both moments below are far from any wall clock, and each
// one alone decides the answer.
func TestMatcherJudgesAgainstTheGivenMoment(t *testing.T) {
	due := time.Date(2020, 1, 1, 12, 0, 0, 0, time.UTC)
	task := Task{DueDate: due.UnixMilli(), InstanceCreatedAt: due.UnixMilli()}
	me := User{ID: "usr_1", Name: "patrick"}
	overdue, err := Compile(Rule{Match: MatchAll, Conditions: []Condition{
		{Field: FieldDue, Op: OpOverdue}}})
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	if got := overdue.Match(task, me, due.Add(-time.Hour)); got {
		t.Error("a task due an hour later reads as overdue; the moment passed to Match is being ignored")
	}
	if got := overdue.Match(task, me, due.Add(time.Hour)); !got {
		t.Error("a task due an hour earlier does not read as overdue")
	}

	// The same for an age window, where the old form drifted with the clock: the
	// instance is one day old at the first moment and eight at the second.
	age, err := Compile(Rule{Match: MatchAll, Conditions: []Condition{
		{Field: FieldInstanceAge, Op: OpOlderThan, Value: "5", Unit: UnitDays}}})
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	if got := age.Match(task, me, due.AddDate(0, 0, 1)); got {
		t.Error("an instance one day old reads as older than five days")
	}
	if got := age.Match(task, me, due.AddDate(0, 0, 8)); !got {
		t.Error("an instance eight days old does not read as older than five days")
	}
}

// TestMatcherNeedsInstance covers the read a scan may skip: the instance's start
// time costs an extra store lookup per task, so it is fetched only for a rule
// that actually asks about it. The answer comes from the compiled expression's
// own input list, not from a second reading of the rule.
func TestMatcherNeedsInstance(t *testing.T) {
	withAge, err := Compile(Rule{Match: MatchAll, Conditions: []Condition{
		{Field: FieldInstanceAge, Op: OpOlderThan, Value: "2", Unit: UnitDays}}})
	if err != nil {
		t.Fatal(err)
	}
	if !withAge.NeedsInstance() {
		t.Error("a rule reading instanceCreatedAt reports it does not need the instance")
	}
	without, err := Compile(Rule{Match: MatchAll, Conditions: []Condition{
		{Field: FieldProcess, Op: OpIs, Value: "p"}}})
	if err != nil {
		t.Fatal(err)
	}
	if without.NeedsInstance() {
		t.Error("a rule that never mentions the instance reports it needs one")
	}
}

// TestCompileRejectsInvalidRule proves the two guards are one: Compile validates
// first, so no unvalidated rule can reach the FEEL compiler.
func TestCompileRejectsInvalidRule(t *testing.T) {
	if _, err := Compile(Rule{Match: "sideways"}); err == nil {
		t.Error("Compile accepted a rule Validate rejects")
	}
}

// TestCatalogIsSelfConsistent holds the catalogue the editor is built from
// against the generator: every advertised field/operator pair must validate and
// produce an expression that compiles, or the UI offers a row nobody can save.
func TestCatalogIsSelfConsistent(t *testing.T) {
	sample := map[string]Condition{
		ValueText:     {Value: "beispiel"},
		ValueChoice:   {Value: "beispiel"},
		ValueChoices:  {Values: []string{"a", "b"}},
		ValueNumber:   {Value: "50"},
		ValueDuration: {Value: "P3D"},
		ValueCount:    {Value: "2", Unit: UnitDays},
		ValueNone:     {},
	}
	for _, f := range Catalog() {
		for _, op := range f.Ops {
			c, ok := sample[op.Value]
			if !ok {
				t.Fatalf("%s/%s advertises unknown value shape %q", f.ID, op.ID, op.Value)
			}
			c.Field, c.Op = f.ID, op.ID
			r := Rule{Match: MatchAll, Conditions: []Condition{c}}
			if err := r.Validate(); err != nil {
				t.Errorf("%s/%s: Validate: %v", f.ID, op.ID, err)
				continue
			}
			if _, err := Compile(r); err != nil {
				t.Errorf("%s/%s: Compile(%s): %v", f.ID, op.ID, r.FEEL(), err)
			}
		}
	}
}

// TestMatchIsFalseWhenTheExpressionIsNotBoolean guards the filter's fail-closed
// reading: anything a rule evaluates to that is not FEEL true excludes the task,
// rather than being reported as a match nobody asked for.
func TestMatchIsFalseWhenTheExpressionIsNotBoolean(t *testing.T) {
	c, err := expr.CompileAuto(`"not a boolean"`)
	if err != nil {
		t.Fatal(err)
	}
	m := &Matcher{c: c}
	if m.Match(Task{}, User{}, time.Now()) {
		t.Error("a non-boolean rule counted as a match")
	}
}

func withAssignee(t Task, who string) Task { t.Assignee = who; return t }
func withDue(t Task, due int64) Task       { t.DueDate = due; return t }
