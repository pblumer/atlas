package formdecision

import (
	"reflect"
	"strings"
	"testing"
)

func TestParseReadsDecisionFields(t *testing.T) {
	schema := `{"type":"default","components":[
	  {"type":"textfield","key":"name"},
	  {"type":"select","key":"kostenstelle","label":"Kostenstelle","validate":{"required":true},
	   "atlasDecision":{"version":1,"decisionId":"Kostenstellen"}},
	  {"type":"radio","key":"farbe","atlasDecision":{"version":1,"decisionId":"Farben"}},
	  {"type":"checklist","key":"tags","disabled":true,"atlasDecision":{"version":1,"decisionId":"Tags"}},
	  {"type":"group","label":"Adresse","path":"adresse.ch","components":[
	    {"type":"taglist","key":"kanton","conditional":{"hide":"=false"},"atlasDecision":{"version":1,"decisionId":"Kantone"}}
	  ]},
	  {"type":"group","label":"ohne Pfad","components":[
	    {"type":"select","key":"land.code","atlasDecision":{"version":1,"decisionId":"Länder"}}
	  ]},
	  {"type":"dynamiclist","path":"positionen","components":[
	    {"type":"select","key":"konto","atlasDecision":{"version":1,"decisionId":"Konten"}}
	  ]}
	]}`
	form, err := Parse([]byte(schema))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	got := map[string]Field{}
	for _, f := range form.Fields {
		got[f.Key] = f
	}
	if len(got) != 6 {
		t.Fatalf("fields = %+v; want six", form.Fields)
	}
	if f := got["kostenstelle"]; f.DecisionID != "Kostenstellen" || !f.Required || f.Multi || f.Label != "Kostenstelle" || f.Type != "select" {
		t.Errorf("kostenstelle = %+v", f)
	}
	if f := got["tags"]; !f.Multi || !f.Disabled {
		t.Errorf("tags = %+v; want multi and disabled", f)
	}
	if f := got["kanton"]; !f.Multi || !f.Conditional || !reflect.DeepEqual(f.Path, []Step{{Name: "adresse"}, {Name: "ch"}, {Name: "kanton"}}) {
		t.Errorf("kanton = %+v", f)
	}
	if f := got["land.code"]; !reflect.DeepEqual(f.Path, []Step{{Name: "land"}, {Name: "code"}}) {
		t.Errorf("a group without a path adds nothing to it, a dotted key nests: %+v", f.Path)
	}
	if f := got["konto"]; !reflect.DeepEqual(f.Path, []Step{{Name: "positionen", Each: true}, {Name: "konto"}}) {
		t.Errorf("a dynamic list repeats its children: %+v", f.Path)
	}
	if ids := form.DecisionIDs(); !reflect.DeepEqual(ids, []string{"Farben", "Kantone", "Konten", "Kostenstellen", "Länder", "Tags"}) {
		t.Errorf("DecisionIDs = %v", ids)
	}
}

func TestParseWithoutDecisionFields(t *testing.T) {
	for _, schema := range []string{`{}`, `{"components":[]}`, `{"components":[{"type":"select","key":"a","values":[{"label":"A","value":"a"}]}]}`} {
		form, err := Parse([]byte(schema))
		if err != nil || len(form.Fields) != 0 {
			t.Errorf("Parse(%s) = %+v, %v; want no fields and no error", schema, form, err)
		}
	}
}

// TestCheckRefuses holds a save to what the block may say. Whether the decision is
// deployed is not among it: that is the server's state, which changes after the form is
// saved, and an import may bring a form before its decision.
func TestCheckRefuses(t *testing.T) {
	cases := map[string]struct{ comp, want string }{
		"not an object":    {`{"type":"select","key":"a","atlasDecision":"Farben"}`, "must be an object"},
		"unknown member":   {`{"type":"select","key":"a","atlasDecision":{"version":1,"decisionId":"F","inputs":{}}}`, "unknown field"},
		"no version":       {`{"type":"select","key":"a","atlasDecision":{"decisionId":"F"}}`, "version is missing"},
		"newer version":    {`{"type":"select","key":"a","atlasDecision":{"version":2,"decisionId":"F"}}`, "newer than this server reads"},
		"negative version": {`{"type":"select","key":"a","atlasDecision":{"version":-1,"decisionId":"F"}}`, "not one this server reads"},
		"no decision":      {`{"type":"select","key":"a","atlasDecision":{"version":1,"decisionId":"  "}}`, "decisionId is missing"},
		"feel":             {`{"type":"select","key":"a","atlasDecision":{"version":1,"decisionId":"=x"}}`, "plain decision name"},
		"control":          {`{"type":"select","key":"a","atlasDecision":{"version":1,"decisionId":"F\u0007"}}`, "is not a decision name"},
		"too long":         {`{"type":"select","key":"a","atlasDecision":{"version":1,"decisionId":"` + strings.Repeat("x", maxDecisionID+1) + `"}}`, "is not a decision name"},
		"no key":           {`{"type":"select","atlasDecision":{"version":1,"decisionId":"F"}}`, "needs a key"},
		"wrong type":       {`{"type":"textfield","key":"a","atlasDecision":{"version":1,"decisionId":"F"}}`, `this one is a "textfield"`},
		"two sources":      {`{"type":"select","key":"a","values":[],"atlasDecision":{"version":1,"decisionId":"F"}}`, "names values as well"},
		"directory too":    {`{"type":"select","key":"a","atlasDataSource":{"version":1},"atlasDecision":{"version":1,"decisionId":"F"}}`, "names atlasDataSource as well"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			err := Check([]byte(`{"components":[` + tc.comp + `]}`))
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("Check = %v; want an error containing %q", err, tc.want)
			}
		})
	}
	if err := Check([]byte(`not json`)); err == nil {
		t.Error("Check accepted a schema that is not JSON")
	}
}

func TestValuesAt(t *testing.T) {
	data := map[string]any{
		"a":          "x",
		"adresse":    map[string]any{"ch": map[string]any{"kanton": []any{"ZH", "BE"}}},
		"positionen": []any{map[string]any{"konto": "1000"}, map[string]any{}, "junk", map[string]any{"konto": nil}, map[string]any{"konto": "2000"}},
	}
	cases := []struct {
		path []Step
		want []any
	}{
		{[]Step{{Name: "a"}}, []any{"x"}},
		{[]Step{{Name: "missing"}}, nil},
		{[]Step{{Name: "a"}, {Name: "deeper"}}, nil},
		{[]Step{{Name: "adresse"}, {Name: "ch"}, {Name: "kanton"}}, []any{[]any{"ZH", "BE"}}},
		{[]Step{{Name: "positionen", Each: true}, {Name: "konto"}}, []any{"1000", "2000"}},
		{[]Step{{Name: "a", Each: true}, {Name: "konto"}}, nil},
	}
	for _, tc := range cases {
		if got := valuesAt(data, tc.path); !reflect.DeepEqual(got, tc.want) {
			t.Errorf("valuesAt(%v) = %#v; want %#v", tc.path, got, tc.want)
		}
	}
}
