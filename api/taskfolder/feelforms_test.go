package taskfolder

import (
	"testing"
	"time"

	"github.com/pblumer/atlas/expr"
)

// TestFEELFormsCompileAndEvaluate pins the exact expression forms the generator
// emits. It is the first test written for this package: every clause below is a
// shape the UI can produce, and a form the FEEL engine rejects is a folder nobody
// can save.
func TestFEELFormsCompileAndEvaluate(t *testing.T) {
	now := time.Date(2026, 9, 7, 12, 0, 0, 0, time.UTC)
	vars := map[string]expr.Value{
		"scanAt":            expr.DateTime(now),
		"processId":         expr.String("kunden-anfrage"),
		"taskName":          expr.String("Anfrage sichten"),
		"assignee":          expr.Null,
		"candidateGroups":   expr.String("kundenservice"),
		"lane":              expr.String("Kundenservice"),
		"lanePath":          expr.FromJSON([]any{"Kundenservice", "Team Lead"}),
		"priority":          expr.Number(75),
		"dueDate":           expr.DateTime(now.Add(-2 * time.Hour)),
		"hasForm":           expr.Bool(true),
		"instanceCreatedAt": expr.DateTime(now.Add(-72 * time.Hour)),
		"user":              expr.FromJSON(map[string]any{"id": "usr_1", "name": "patrick"}),
	}
	cases := []struct {
		src  string
		want bool
	}{
		{`processId = "kunden-anfrage"`, true},
		{`processId != "kunden-anfrage"`, false},
		{`processId in ("kunden-anfrage", "service-desk-ticket")`, true},
		{`processId in ("a", "b")`, false},
		{`taskName = "Anfrage sichten"`, true},
		{`contains(taskName, "sichten")`, true},
		{`starts with(taskName, "Anfrage")`, true},
		{`assignee = null`, true},
		{`assignee = "patrick"`, false},
		{`assignee = user.name`, false},
		{`candidateGroups in ("kundenservice", "service-desk")`, true},
		{`lane = "Kundenservice"`, true},
		{`list contains(lanePath, "Team Lead")`, true},
		{`"Team Lead" in lanePath`, true},
		{`priority >= 70`, true},
		{`priority <= 50`, false},
		{`dueDate != null and dueDate < scanAt`, true},
		{`dueDate != null and dueDate < scanAt + duration("P3D")`, true},
		{`dueDate = null`, false},
		{`hasForm`, true},
		{`not(hasForm)`, false},
		{`instanceCreatedAt < scanAt - duration("P2D")`, true},
	}
	for _, tc := range cases {
		c, err := expr.CompileAuto(tc.src)
		if err != nil {
			t.Errorf("compile %q: %v", tc.src, err)
			continue
		}
		v, err := c.Eval(vars)
		if err != nil {
			t.Errorf("eval %q: %v", tc.src, err)
			continue
		}
		kind, b, text := expr.Classify(v)
		if kind != expr.KindBool {
			t.Errorf("%q: got kind %v (%q), want boolean", tc.src, kind, text)
			continue
		}
		if b != tc.want {
			t.Errorf("%q = %v, want %v", tc.src, b, tc.want)
		}
	}
}
