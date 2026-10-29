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
		"unknown member":   {`{"type":"select","key":"a","atlasDecision":{"version":1,"decisionId":"F","expression":"=x"}}`, "unknown field"},
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

// TestCheckRefusesBindings holds an input to what it may say: the name of an input of
// the decision, and exactly one of a field of the same form it is bound to and a fixed
// value.
func TestCheckRefusesBindings(t *testing.T) {
	form := func(inputs string, extra ...string) string {
		comps := append([]string{
			`{"type":"select","key":"gruppe","values":[{"label":"IT","value":"IT"}]}`,
			`{"type":"select","key":"m","atlasDecision":{"version":1,"decisionId":"Mitglieder","inputs":` + inputs + `}}`,
		}, extra...)
		return `{"components":[` + strings.Join(comps, ",") + `]}`
	}
	cases := map[string]struct{ schema, want string }{
		"not a list":    {form(`{"gruppe":"gruppe"}`), "cannot unmarshal"},
		"no name":       {form(`[{"field":"gruppe"}]`), "is not the name of a decision input"},
		"neither":       {form(`[{"name":"gruppe"}]`), "needs exactly one of field"},
		"both":          {form(`[{"name":"gruppe","field":"gruppe","value":"IT"}]`), "needs exactly one of field"},
		"twice":         {form(`[{"name":"gruppe","field":"gruppe"},{"name":"gruppe","value":"IT"}]`), `"gruppe" is given twice`},
		"itself":        {form(`[{"name":"gruppe","field":"m"}]`), "bound to the field itself"},
		"no such field": {form(`[{"name":"gruppe","field":"abteilung"}]`), `"abteilung", which is not a field of this form`},
		"ambiguous":     {form(`[{"name":"gruppe","field":"gruppe"}]`, `{"type":"group","path":"x","components":[{"type":"textfield","key":"gruppe"}]}`), "several fields of this form are called"},
		"fixed null":    {form(`[{"name":"gruppe","value":null}]`), ""},
		"too large":     {form(`[{"name":"gruppe","value":"` + strings.Repeat("x", maxValueBytes) + `"}]`), "larger than"},
		"too many":      {form(`[` + strings.TrimSuffix(strings.Repeat(`{"name":"g","value":1},`, maxInputs+1), ",") + `]`), "binds at most"},
		"other row": {`{"components":[
		  {"type":"dynamiclist","path":"zeilen","components":[{"type":"textfield","key":"z"}]},
		  {"type":"select","key":"m","atlasDecision":{"version":1,"decisionId":"M","inputs":[{"name":"g","field":"z"}]}}]}`,
			"repeats in a dynamic list this field is not in"},
		"cycle": {`{"components":[
		  {"type":"select","key":"a","atlasDecision":{"version":1,"decisionId":"M","inputs":[{"name":"g","field":"b"}]}},
		  {"type":"select","key":"b","atlasDecision":{"version":1,"decisionId":"M","inputs":[{"name":"g","field":"a"}]}}]}`,
			"depend on each other"},
		"duplicate key": {`{"components":[
		  {"type":"select","key":"a","atlasDecision":{"version":1,"decisionId":"M"}},
		  {"type":"group","path":"x","components":[{"type":"textfield","key":"a"}]}]}`,
			"another field of this form has the key"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			err := Check([]byte(tc.schema))
			if tc.want == "" {
				if err != nil {
					t.Fatalf("Check = %v; want accepted", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("Check = %v; want an error containing %q", err, tc.want)
			}
		})
	}
}

func TestBindingsAreRead(t *testing.T) {
	form, err := Parse([]byte(`{"components":[
	  {"type":"select","key":"land","values":[{"label":"CH","value":"CH"}],"defaultValue":"CH"},
	  {"type":"select","key":"gruppe","atlasDecision":{"version":1,"decisionId":"Gruppen","inputs":[{"name":"land","field":"land"},{"name":"limit","value":3}]}},
	  {"type":"select","key":"m","atlasDecision":{"version":1,"decisionId":"M","inputs":[{"name":"gruppe","field":"gruppe"}]}},
	  {"type":"dynamiclist","path":"zeilen","components":[
	    {"type":"textfield","key":"z"},
	    {"type":"select","key":"zm","atlasDecision":{"version":1,"decisionId":"M","inputs":[{"name":"g","field":"z"},{"name":"l","field":"land"}]}}
	  ]}
	]}`))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	g, _ := form.Field("gruppe")
	if len(g.Inputs) != 2 || g.Inputs[0] != (Binding{Name: "land", Field: "land"}) || g.Inputs[1].Value != float64(3) || !g.Inputs[1].Fixed() || !g.BoundFields() {
		t.Errorf("gruppe's inputs = %+v", g.Inputs)
	}
	m, _ := form.Field("m")
	if got := form.closure(m); !reflect.DeepEqual(got, []string{"gruppe", "land"}) {
		t.Errorf("closure(m) = %v; want the group and the country it depends on", got)
	}
	if k := form.keys["land"]; !k.HasStatic || len(k.Static) != 1 || !k.HasDefault || k.Default != "CH" {
		t.Errorf("the country as a bound field = %+v", k)
	}
	if k := form.keys["gruppe"]; !k.Decision {
		t.Errorf("gruppe is not marked a decision field: %+v", k)
	}
	if zm, _ := form.Field("zm"); len(zm.Inputs) != 2 {
		t.Errorf("a row's field bound to its row and to a field outside the rows = %+v", zm.Inputs)
	}
	if f, _ := form.Field("land"); f.Key != "" {
		t.Error("a field without a decision was found as one")
	}
}

func TestOccurrencesAndValueAt(t *testing.T) {
	data := map[string]any{
		"land": "CH",
		"zeilen": []any{
			map[string]any{"z": "IT", "zm": "anna", "innen": []any{map[string]any{"x": 1}, map[string]any{"x": 2}}},
			map[string]any{"z": "Vertrieb"},
			map[string]any{"z": "HR", "zm": nil},
		},
	}
	zm := []Step{{Name: "zeilen", Each: true}, {Name: "zm"}}
	if got := occurrences(data, zm); len(got) != 1 || got[0].Value != "anna" || !reflect.DeepEqual(got[0].Rows, []int{0}) {
		t.Errorf("occurrences(zm) = %+v", got)
	}
	x := []Step{{Name: "zeilen", Each: true}, {Name: "innen", Each: true}, {Name: "x"}}
	if got := occurrences(data, x); len(got) != 2 || !reflect.DeepEqual(got[1].Rows, []int{0, 1}) {
		t.Errorf("occurrences(x) = %+v", got)
	}
	z := []Step{{Name: "zeilen", Each: true}, {Name: "z"}}
	for rows, want := range map[int]any{0: "IT", 1: "Vertrieb", 2: "HR"} {
		if v, ok := valueAt(data, z, []int{rows}); !ok || v != want {
			t.Errorf("valueAt(z, %d) = %v, %v", rows, v, ok)
		}
	}
	if v, ok := valueAt(data, []Step{{Name: "land"}}, nil); !ok || v != "CH" {
		t.Errorf("valueAt(land) = %v, %v", v, ok)
	}
	if v, ok := valueAt(data, zm, []int{2}); !ok || v != nil {
		t.Errorf("a null is held: %v, %v", v, ok)
	}
	for _, tc := range []struct {
		path []Step
		rows []int
	}{
		{zm, []int{1}},
		{z, []int{3}},
		{z, nil},
		{[]Step{{Name: "land"}, {Name: "x"}}, nil},
		{[]Step{{Name: "land", Each: true}}, []int{0}},
	} {
		if v, ok := valueAt(data, tc.path, tc.rows); ok {
			t.Errorf("valueAt(%v, %v) = %v; want absent", tc.path, tc.rows, v)
		}
	}
}
