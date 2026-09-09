package taskfolder

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/pblumer/atlas/expr"
)

// The fields a folder rule can ask about, and the FEEL name each binds to. The
// left-hand side is the id the API and the editor speak; the right-hand side is
// the evaluation context below. They are separate on purpose: the id is a stable
// part of the stored rule, the FEEL name is the expression contract, and neither
// should be forced to change because the other did.
const (
	FieldProcess     = "process"
	FieldTaskName    = "taskName"
	FieldAssignee    = "assignee"
	FieldGroup       = "group"
	FieldLane        = "lane"
	FieldPriority    = "priority"
	FieldDue         = "due"
	FieldInstanceAge = "instanceAge"
	FieldForm        = "form"
)

// The operators. Not every operator applies to every field — [Catalog] says which
// pairs exist, and [Rule.Validate] is what enforces it.
const (
	OpIs         = "is"
	OpIsNot      = "isNot"
	OpIsOneOf    = "isOneOf"
	OpContains   = "contains"
	OpStartsWith = "startsWith"
	OpIsMe       = "isMe"
	OpIsEmpty    = "isEmpty"
	OpUnder      = "under"
	OpAtLeast    = "atLeast"
	OpAtMost     = "atMost"
	OpOverdue    = "overdue"
	OpWithin     = "within"
	OpNone       = "none"
	OpAny        = "any"
	OpOlderThan  = "olderThan"
	OpNewerThan  = "newerThan"
	OpHas        = "has"
	OpHasNot     = "hasNot"
)

// The value shapes an operator asks for. This is what the editor reads to decide
// which control to draw — a listbox, a text field, a number, nothing at all — so
// the server describes the form and the client never hard-codes it.
const (
	ValueNone     = "none"     // the operator is complete on its own ("is overdue")
	ValueText     = "text"     // free text the person types (a name fragment)
	ValueChoice   = "choice"   // one entry from a server-supplied list
	ValueChoices  = "choices"  // several entries from that list
	ValueNumber   = "number"   // a plain number (priority)
	ValueDuration = "duration" // an ISO-8601 duration from a fixed set of offers
	ValueCount    = "count"    // a number plus a unit (hours or days)
)

// The units a [ValueCount] value may carry.
const (
	UnitHours = "h"
	UnitDays  = "d"
)

// The limits a stored rule is held to. They are small because a folder is a
// person's sidebar entry, not a query language: a rule that needs more than this
// is one the editor cannot draw legibly either.
const (
	maxConditions = 20
	maxChoices    = 50
	maxValueLen   = 200
)

// OpSpec is one operator as the editor meets it: its id and the value shape it
// needs. The FEEL it produces stays here — the client never builds an expression.
type OpSpec struct {
	ID    string `json:"id"`
	Value string `json:"value"`
	feel  func(c Condition) string
}

// FieldSpec is one field of the editor's first listbox, with the operators its
// second listbox offers and the name of the value list that fills its third.
// Options names a list the API supplies (processes, users, groups, …); it is
// empty for a field whose values are typed or numeric.
type FieldSpec struct {
	ID      string   `json:"id"`
	Options string   `json:"options,omitempty"`
	Ops     []OpSpec `json:"ops"`
}

// The value lists the API fills in for a field. The names are part of the
// /task-folders/fields response, so the editor can pair a field with its list
// without knowing what either contains.
const (
	OptionsProcesses = "processes"
	OptionsTaskNames = "taskNames"
	OptionsUsers     = "users"
	OptionsGroups    = "groups"
	OptionsLanes     = "lanes"
)

// Catalog is every field/operator pair a folder rule may use — the one place the
// editor, the validator and the FEEL generator agree on what exists.
//
// It is a function rather than a package variable so a caller cannot reach in and
// mutate the catalogue the validator is about to consult.
func Catalog() []FieldSpec {
	return []FieldSpec{
		{ID: FieldProcess, Options: OptionsProcesses, Ops: []OpSpec{
			{ID: OpIs, Value: ValueChoice, feel: binary("processId", "=")},
			{ID: OpIsNot, Value: ValueChoice, feel: binary("processId", "!=")},
			{ID: OpIsOneOf, Value: ValueChoices, feel: inList("processId")},
		}},
		{ID: FieldTaskName, Options: OptionsTaskNames, Ops: []OpSpec{
			{ID: OpIs, Value: ValueChoice, feel: binary("taskName", "=")},
			{ID: OpContains, Value: ValueText, feel: call("contains", "taskName")},
			{ID: OpStartsWith, Value: ValueText, feel: call("starts with", "taskName")},
		}},
		{ID: FieldAssignee, Options: OptionsUsers, Ops: []OpSpec{
			// "bin ich" resolves against the viewer, not against a name frozen into
			// the rule — which is what makes one shared folder work for a whole team.
			{ID: OpIsMe, Value: ValueNone, feel: literal("assignee = user.name")},
			{ID: OpIsEmpty, Value: ValueNone, feel: literal("assignee = null")},
			{ID: OpIs, Value: ValueChoice, feel: binary("assignee", "=")},
			{ID: OpIsNot, Value: ValueChoice, feel: binary("assignee", "!=")},
		}},
		{ID: FieldGroup, Options: OptionsGroups, Ops: []OpSpec{
			{ID: OpIs, Value: ValueChoice, feel: binary("candidateGroups", "=")},
			{ID: OpIsOneOf, Value: ValueChoices, feel: inList("candidateGroups")},
		}},
		{ID: FieldLane, Options: OptionsLanes, Ops: []OpSpec{
			{ID: OpIs, Value: ValueChoice, feel: binary("lane", "=")},
			// "liegt unter" asks the whole lane path, so a folder for an outer lane
			// keeps working when a modeller nests a new lane inside it (ADR-0121).
			{ID: OpUnder, Value: ValueChoice, feel: func(c Condition) string {
				return "list contains(lanePath, " + feelString(c.Value) + ")"
			}},
		}},
		{ID: FieldPriority, Ops: []OpSpec{
			{ID: OpAtLeast, Value: ValueNumber, feel: number("priority", ">=")},
			{ID: OpAtMost, Value: ValueNumber, feel: number("priority", "<=")},
			{ID: OpIs, Value: ValueNumber, feel: number("priority", "=")},
		}},
		{ID: FieldDue, Ops: []OpSpec{
			{ID: OpOverdue, Value: ValueNone, feel: literal("dueDate != null and dueDate < scanAt")},
			{ID: OpWithin, Value: ValueDuration, feel: func(c Condition) string {
				return "dueDate != null and dueDate < scanAt + duration(" + feelString(c.Value) + ")"
			}},
			{ID: OpNone, Value: ValueNone, feel: literal("dueDate = null")},
			{ID: OpAny, Value: ValueNone, feel: literal("dueDate != null")},
		}},
		{ID: FieldInstanceAge, Ops: []OpSpec{
			{ID: OpOlderThan, Value: ValueCount, feel: age("<")},
			{ID: OpNewerThan, Value: ValueCount, feel: age(">")},
		}},
		{ID: FieldForm, Ops: []OpSpec{
			{ID: OpHas, Value: ValueNone, feel: literal("hasForm")},
			{ID: OpHasNot, Value: ValueNone, feel: literal("not(hasForm)")},
		}},
	}
}

// The emitters. Each returns the FEEL for one condition; keeping them beside the
// catalogue is what makes "every advertised pair produces a compilable
// expression" a property a test can check by walking the catalogue.
func literal(src string) func(Condition) string { return func(Condition) string { return src } }

func binary(name, op string) func(Condition) string {
	return func(c Condition) string { return name + " " + op + " " + feelString(c.Value) }
}

func number(name, op string) func(Condition) string {
	return func(c Condition) string { return name + " " + op + " " + strings.TrimSpace(c.Value) }
}

func call(fn, name string) func(Condition) string {
	return func(c Condition) string { return fn + "(" + name + ", " + feelString(c.Value) + ")" }
}

func inList(name string) func(Condition) string {
	return func(c Condition) string {
		quoted := make([]string, 0, len(c.Values))
		for _, v := range c.Values {
			quoted = append(quoted, feelString(v))
		}
		return name + " in (" + strings.Join(quoted, ", ") + ")"
	}
}

// age compares the instance's start against a moment that many hours or days ago.
// "older than" is the earlier instant, hence `<`.
func age(op string) func(Condition) string {
	return func(c Condition) string {
		return "instanceCreatedAt " + op + " scanAt - duration(" + feelString(isoDuration(c)) + ")"
	}
}

// isoDuration renders a count/unit pair as the ISO-8601 duration FEEL parses.
func isoDuration(c Condition) string {
	n := strings.TrimSpace(c.Value)
	if c.Unit == UnitHours {
		return "PT" + n + "H"
	}
	return "P" + n + "D"
}

// feelString renders a value as a FEEL string literal. Escaping here rather than
// rejecting the characters is what keeps a value with an apostrophe or a quote in
// its name usable, while making it impossible for one to close the literal and
// continue as expression text.
func feelString(s string) string {
	var b strings.Builder
	b.Grow(len(s) + 2)
	b.WriteByte('"')
	for _, r := range s {
		switch r {
		case '\\':
			b.WriteString(`\\`)
		case '"':
			b.WriteString(`\"`)
		default:
			b.WriteRune(r)
		}
	}
	b.WriteByte('"')
	return b.String()
}

// lookup finds a field/operator pair in the catalogue.
func lookup(field, op string) (FieldSpec, OpSpec, error) {
	for _, f := range Catalog() {
		if f.ID != field {
			continue
		}
		for _, o := range f.Ops {
			if o.ID == op {
				return f, o, nil
			}
		}
		return f, OpSpec{}, fmt.Errorf("operator %q does not apply to field %q", op, field)
	}
	return FieldSpec{}, OpSpec{}, fmt.Errorf("unknown field %q", field)
}

// durationPattern is the ISO-8601 subset a "due within" offer may carry: whole
// days, whole hours, or both. Anything else is refused rather than passed to the
// FEEL parser, so a bad value fails at save time with a message about durations.
var durationPattern = regexp.MustCompile(`^P(?:[1-9][0-9]{0,3}D)?(?:T[1-9][0-9]{0,3}H)?$`)

// Validate reports whether a rule is one the server will store and compile. It is
// the only gate: [Compile] runs it first, so nothing unvalidated reaches FEEL.
func (r Rule) Validate() error {
	if r.Match != MatchAll && r.Match != MatchAny {
		return fmt.Errorf("match must be %q or %q, got %q", MatchAll, MatchAny, r.Match)
	}
	if len(r.Conditions) > maxConditions {
		return fmt.Errorf("a folder may carry at most %d conditions, got %d", maxConditions, len(r.Conditions))
	}
	for i, c := range r.Conditions {
		if err := c.validate(); err != nil {
			return fmt.Errorf("condition %d: %w", i+1, err)
		}
	}
	return nil
}

func (c Condition) validate() error {
	_, op, err := lookup(c.Field, c.Op)
	if err != nil {
		return err
	}
	switch op.Value {
	case ValueNone:
		return nil
	case ValueChoices:
		if len(c.Values) == 0 {
			return fmt.Errorf("operator %q needs at least one value", c.Op)
		}
		if len(c.Values) > maxChoices {
			return fmt.Errorf("operator %q accepts at most %d values, got %d", c.Op, maxChoices, len(c.Values))
		}
		for _, v := range c.Values {
			if err := checkText(v); err != nil {
				return err
			}
		}
		return nil
	case ValueText, ValueChoice:
		return checkText(c.Value)
	case ValueNumber:
		n, err := strconv.Atoi(strings.TrimSpace(c.Value))
		if err != nil {
			return fmt.Errorf("value %q is not a number", c.Value)
		}
		if n < 0 || n > 100 {
			return fmt.Errorf("priority must be between 0 and 100, got %d", n)
		}
		return nil
	case ValueDuration:
		if !durationPattern.MatchString(strings.TrimSpace(c.Value)) {
			return fmt.Errorf("value %q is not a duration this field accepts (e.g. P3D, PT8H)", c.Value)
		}
		return nil
	case ValueCount:
		if c.Unit != UnitHours && c.Unit != UnitDays {
			return fmt.Errorf("unit must be %q or %q, got %q", UnitHours, UnitDays, c.Unit)
		}
		n, err := strconv.Atoi(strings.TrimSpace(c.Value))
		if err != nil {
			return fmt.Errorf("value %q is not a number", c.Value)
		}
		if n < 1 || n > 9999 {
			return fmt.Errorf("value must be between 1 and 9999, got %d", n)
		}
		return nil
	}
	return fmt.Errorf("operator %q has no value shape", c.Op)
}

// checkText holds a free or chosen value to what a single-line control can carry.
// Control characters are refused rather than escaped: none of them can reach the
// server from the editor, so one arriving means the request was not built by it.
func checkText(v string) error {
	if strings.TrimSpace(v) == "" {
		return fmt.Errorf("a value is required")
	}
	if len([]rune(v)) > maxValueLen {
		return fmt.Errorf("a value may be at most %d characters", maxValueLen)
	}
	for _, r := range v {
		if unicode.IsControl(r) {
			return fmt.Errorf("a value may not contain control characters")
		}
	}
	return nil
}

// FEEL renders the rule as the expression that decides membership — the text the
// editor shows beneath the conditions, and the text the server compiles.
//
// A rule with no conditions is `true`: a folder someone has started but not
// narrowed shows everything, which is what the empty editor looks like it means.
func (r Rule) FEEL() string {
	if len(r.Conditions) == 0 {
		return "true"
	}
	joiner := "and"
	if r.Match == MatchAny {
		joiner = "or"
	}
	parts := make([]string, 0, len(r.Conditions))
	for _, c := range r.Conditions {
		src := "true"
		if _, op, err := lookup(c.Field, c.Op); err == nil && op.feel != nil {
			src = op.feel(c)
		}
		// A condition that is itself a conjunction needs brackets once it sits
		// beside another one, or `or` would bind looser than the reader expects.
		if len(r.Conditions) > 1 && strings.Contains(src, " and ") {
			src = "(" + src + ")"
		}
		parts = append(parts, src)
	}
	return strings.Join(parts, "\n  "+joiner+" ")
}

// Matcher is a rule compiled once and evaluated per task. Compiling at save time
// rather than per request is invariant 5 (ADR-0008): the parse, type-check and
// lowering happen when a person presses Save, and a listing only evaluates.
type Matcher struct {
	c         *expr.Compiled
	needsInst bool
}

// Compile validates a rule and compiles the expression it generates. The returned
// matcher is immutable and safe to evaluate concurrently, like every other
// compiled FEEL in Atlas.
func Compile(r Rule) (*Matcher, error) {
	if err := r.Validate(); err != nil {
		return nil, err
	}
	c, err := expr.CompileAuto(r.FEEL())
	if err != nil {
		return nil, fmt.Errorf("the rule does not compile: %w", err)
	}
	m := &Matcher{c: c}
	for _, in := range c.Inputs() {
		if in == "instanceCreatedAt" {
			m.needsInst = true
		}
	}
	return m, nil
}

// Source returns the FEEL the matcher was compiled from.
func (m *Matcher) Source() string { return m.c.Source() }

// NeedsInstance reports whether the rule asks about the process instance behind
// the task. A scan reads the instance only when it does — that read is one more
// store lookup per task, and most rules never mention it. The answer comes from
// the compiled expression's own input list, so it cannot disagree with the
// expression actually being evaluated.
func (m *Matcher) NeedsInstance() bool { return m.needsInst }

// Task is one open user task as a rule sees it: the evaluation context, and the
// public contract of the expression a folder generates.
type Task struct {
	ProcessID       string
	ProcessName     string
	TaskName        string
	ElementID       string
	Assignee        string
	CandidateGroups string
	Lane            string
	LanePath        []string
	Priority        int32
	// DueDate is the task's due instant in Unix milliseconds, 0 when it has none.
	DueDate int64
	// InstanceCreatedAt is when the process instance carrying the task started, in
	// Unix milliseconds, 0 when it was not read (see [Matcher.NeedsInstance]).
	InstanceCreatedAt int64
	HasForm           bool
}

// User is who is asking. It is what the `user` context in a rule resolves
// against, so one shared folder ("assigned to me") answers differently for each
// viewer.
//
// ID and Name are deliberately two fields. A task's assignee is a *username* —
// that is what claiming writes onto the job (ADR-0042) — while a folder is owned
// by an account id, which survives a rename. Binding one to the other would make
// "assigned to me" compare a username against an account id and quietly match
// nothing, which is why `user.name` and not `user.id` is what an assignee
// condition generates.
type User struct {
	ID     string
	Name   string
	Groups []string
}

// Match reports whether one task belongs in the folder.
//
// Anything that is not FEEL true excludes the task: a null from comparing an
// absent field, a rule that somehow evaluates to a string, an evaluation error.
// A filter that fails open would quietly put foreign work in somebody's queue,
// which is the worse of the two failures.
func (m *Matcher) Match(t Task, u User, now time.Time) bool {
	v, err := m.c.Eval(m.bindings(t, u, now))
	if err != nil {
		return false
	}
	kind, b, _ := expr.Classify(v)
	return kind == expr.KindBool && b
}

// bindings builds the evaluation scope for one task. An empty string binds as
// FEEL null rather than as "", so `assignee = null` means "nobody has claimed it"
// instead of "somebody is called the empty string".
//
// scanAt is the moment the whole scan is judged against, bound as a value
// rather than left to FEEL's now(), which reads the clock again for every row
// and would let a task on the edge of a window fall on either side depending on
// when in the scan it was reached (see the note on expr.DateTime). It is not
// called "now": FEEL resolves that name to the builtin function even where a
// binding of that name exists, so `dueDate < now` compares an instant against a
// function and yields null instead of a boolean.
func (m *Matcher) bindings(t Task, u User, now time.Time) map[string]expr.Value {
	lane := make([]any, 0, len(t.LanePath))
	for _, l := range t.LanePath {
		lane = append(lane, l)
	}
	groups := make([]any, 0, len(u.Groups))
	for _, g := range u.Groups {
		groups = append(groups, g)
	}
	return map[string]expr.Value{
		"scanAt":            expr.DateTime(now),
		"processId":         strOrNull(t.ProcessID),
		"processName":       strOrNull(t.ProcessName),
		"taskName":          strOrNull(t.TaskName),
		"elementId":         strOrNull(t.ElementID),
		"assignee":          strOrNull(t.Assignee),
		"candidateGroups":   strOrNull(t.CandidateGroups),
		"lane":              strOrNull(t.Lane),
		"lanePath":          expr.FromJSON(lane),
		"priority":          expr.Number(int64(t.Priority)),
		"dueDate":           msOrNull(t.DueDate),
		"instanceCreatedAt": msOrNull(t.InstanceCreatedAt),
		"hasForm":           expr.Bool(t.HasForm),
		"user":              expr.FromJSON(map[string]any{"id": u.ID, "name": u.Name, "groups": groups}),
	}
}

func strOrNull(s string) expr.Value {
	if s == "" {
		return expr.Null
	}
	return expr.String(s)
}

func msOrNull(ms int64) expr.Value {
	if ms == 0 {
		return expr.Null
	}
	return expr.DateTime(time.UnixMilli(ms).UTC())
}
