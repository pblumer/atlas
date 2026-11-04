package api

import (
	"reflect"
	"testing"

	"github.com/pblumer/atlas/model"
)

func vars(pairs ...model.VariableValue) []model.VariableValue { return pairs }

func str(name, v string) model.VariableValue {
	return model.VariableValue{Name: name, Kind: model.VarString, Text: v}
}
func num(name, v string) model.VariableValue {
	return model.VariableValue{Name: name, Kind: model.VarNumber, Text: v}
}
func js(name, v string) model.VariableValue {
	return model.VariableValue{Name: name, Kind: model.VarJSON, Text: v}
}
func boolean(name string, v bool) model.VariableValue {
	return model.VariableValue{Name: name, Kind: model.VarBool, Bool: v}
}

const constraintSchema = `{"components":[
  {"type":"textfield","key":"reason","label":"Reason","validate":{"required":true,"minLength":3}},
  {"type":"textarea","key":"detail","conditional":{"hide":"=reason = \"x\""},"validate":{"required":true}},
  {"type":"group","path":"sub","conditional":{"hide":"=false"},"components":[
    {"type":"textfield","key":"inner","validate":{"required":true}}
  ]},
  {"type":"number","key":"count","serializeToString":true,"validate":{"min":0}},
  {"type":"checklist","key":"tags","values":[{"label":"A","value":"a"},{"label":"One","value":1}]},
  {"type":"taglist","key":"dyn","valuesKey":"options"},
  {"type":"select","key":"pick","atlasDecision":{"version":1,"decisionId":"D"}},
  {"type":"datetime","key":"when"},
  {"type":"dynamiclist","path":"rows","isRepeating":true,"components":[
    {"type":"textfield","key":"cell","validate":{"required":true}}
  ]},
  {"type":"textfield","key":"shown","disabled":true,"validate":{"required":true}},
  {"type":"mailview","key":"mail"},
  {"type":"text","text":"no key"}
]}`

func TestStartFormRulesReadWhatTheFormDeclares(t *testing.T) {
	rules := startFormRules([]byte(constraintSchema))
	paths := map[string]formRule{}
	for _, r := range rules {
		paths[joinPath(r.path)] = r
	}
	for _, want := range []string{"reason", "detail", "sub.inner", "count", "tags", "dyn", "pick", "when"} {
		if _, ok := paths[want]; !ok {
			t.Errorf("no rule for %s", want)
		}
	}
	for _, absent := range []string{"rows.cell", "shown", "mail"} {
		if _, ok := paths[absent]; ok {
			t.Errorf("a rule for %s, which this check leaves to the shape check or the browser", absent)
		}
	}
	if !paths["reason"].required || paths["detail"].required || paths["sub.inner"].required {
		t.Errorf("required: reason=%v detail=%v sub.inner=%v; a field shown under a condition is never required here",
			paths["reason"].required, paths["detail"].required, paths["sub.inner"].required)
	}
	if paths["dyn"].options != nil || paths["pick"].options != nil || len(paths["tags"].options) != 2 {
		t.Errorf("options: dyn=%v pick=%v tags=%v; only a list the schema states is closed",
			paths["dyn"].options, paths["pick"].options, paths["tags"].options)
	}
	if rules := startFormRules([]byte(`{not json`)); rules != nil {
		t.Errorf("an unparseable schema declares %v", rules)
	}
}

func joinPath(p []string) string {
	out := ""
	for i, s := range p {
		if i > 0 {
			out += "."
		}
		out += s
	}
	return out
}

func TestStartFormProblemsHoldTheDeclaredConstraints(t *testing.T) {
	rules := startFormRules([]byte(constraintSchema))
	cases := map[string]struct {
		vars []model.VariableValue
		want []string
	}{
		"all fine": {vars(str("reason", "because"), str("count", "3"), js("tags", `["a",1]`), js("dyn", `["anything"]`),
			str("pick", "whatever"), str("when", "2026-11-02")), nil},
		"missing required":           {vars(), []string{"reason is required"}},
		"too short":                  {vars(str("reason", "no")), []string{"reason must be at least 3 characters"}},
		"number as text accepted":    {vars(str("reason", "ok!"), str("count", "-1")), []string{"count must be at least 0"}},
		"text that is no number":     {vars(str("reason", "ok!"), str("count", "many")), []string{"count must be a number"}},
		"bool for a number":          {vars(str("reason", "ok!"), boolean("count", true)), []string{"count must be a number"}},
		"checklist off the list":     {vars(str("reason", "ok!"), js("tags", `["b"]`)), []string{"tags holds a value that is not one of its options"}},
		"checklist not a list":       {vars(str("reason", "ok!"), str("tags", "a")), []string{"tags must be a list of its options"}},
		"checklist of objects":       {vars(str("reason", "ok!"), js("dyn", `[{"x":1}]`)), []string{"dyn holds a value that is not one of its options"}},
		"select needs a scalar":      {vars(str("reason", "ok!"), js("pick", `{"a":1}`)), []string{"pick must be one of its options"}},
		"datetime must be text":      {vars(str("reason", "ok!"), num("when", "5")), []string{"when must be text"}},
		"text for a text field only": {vars(js("reason", `["x"]`)), []string{"reason must be text"}},
		"null is empty":              {vars(model.VariableValue{Name: "reason", Kind: model.VarNull}), []string{"reason is required"}},
		"broken JSON is absent":      {vars(js("reason", `{`)), []string{"reason is required"}},
	}
	for name, c := range cases {
		if got := startFormProblems(rules, c.vars); !reflect.DeepEqual(got, c.want) {
			t.Errorf("%s = %v, want %v", name, got, c.want)
		}
	}
	nested := startFormRules([]byte(`{"components":[{"type":"group","path":"a","components":[{"type":"number","key":"b.c","validate":{"required":true,"max":2}}]},
	  {"type":"checkbox","key":"ok"},{"type":"radio","key":"r","values":[{"value":"x"}]}]}`))
	for name, c := range map[string]struct {
		vars []model.VariableValue
		want []string
	}{
		"nested missing":        {vars(js("a", `{}`)), []string{"a.b.c is required"}},
		"nested through scalar": {vars(str("a", "flat")), []string{"a.b.c is required"}},
		"nested above max":      {vars(js("a", `{"b":{"c":3}}`)), []string{"a.b.c must be at most 2"}},
		"checkbox text":         {vars(js("a", `{"b":{"c":1}}`), str("ok", "yes")), []string{"ok must be true or false"}},
		"radio off the list":    {vars(js("a", `{"b":{"c":1}}`), str("r", "y")), []string{"r is not one of its options"}},
		"radio on the list":     {vars(js("a", `{"b":{"c":1}}`), str("r", "x"), boolean("ok", false)), nil},
	} {
		if got := startFormProblems(nested, c.vars); !reflect.DeepEqual(got, c.want) {
			t.Errorf("%s = %v, want %v", name, got, c.want)
		}
	}
}
