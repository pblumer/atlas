package formdecision

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/pblumer/atlas/dmn"
)

// fakeDecisions is a registry of decision deployments as this area sees one. Mitglieder
// answers the members of a group; Gruppen the groups of a country; the rest are what
// their names say.
type fakeDecisions struct {
	decisions map[string]dmn.DeployedDecision
	evals     []string
}

var members = map[string][]any{
	"IT":       {"anna", map[string]any{"value": "ben", "label": "Ben"}},
	"Vertrieb": {"carla"},
	"Geheim":   {"direktion"},
}

func (f *fakeDecisions) config() Config {
	return Config{
		Decision: func(id string) (dmn.DeployedDecision, uint64, bool) {
			d, ok := f.decisions[id]
			return d, 1, ok
		},
		Decisions: func() []dmn.DeployedDecision {
			var out []dmn.DeployedDecision
			for _, id := range []string{"Farben", "Gruppen", "Mitglieder"} {
				out = append(out, f.decisions[id])
			}
			return out
		},
		Evaluate: func(_ context.Context, key uint64, id string, in map[string]any) (map[string]any, error) {
			b, _ := json.Marshal(in)
			f.evals = append(f.evals, id+string(b))
			if key != 1 {
				return nil, errors.New("wrong key")
			}
			switch id {
			case "Farben":
				return map[string]any{"Farben": []any{"rot", map[string]any{"value": "gr", "label": "grün"}, nil}}, nil
			case "Länder":
				return map[string]any{"Länder": []any{"CH", "DE"}}, nil
			case "Gruppen":
				if in["land"] == "CH" {
					return map[string]any{"Gruppen": []any{"IT", "Vertrieb"}}, nil
				}
				return map[string]any{"Gruppen": []any{"IT"}}, nil
			case "Mitglieder":
				g, _ := in["gruppe"].(string)
				if _, isFloat := in["limit"].(float64); in["limit"] != nil && !isFloat {
					return nil, fmt.Errorf("limit arrived as %T, not as a number", in["limit"])
				}
				return map[string]any{"Mitglieder": members[g]}, nil
			case "Kaputt":
				return nil, errors.New("dmn: evaluate: boom")
			case "Text":
				return map[string]any{"Text": "kein Verzeichnis"}, nil
			}
			return nil, errors.New("unknown")
		},
	}
}

func newFake() *fakeDecisions {
	d := func(id string, inputs ...string) dmn.DeployedDecision {
		var in []dmn.DecisionField
		for _, n := range inputs {
			in = append(in, dmn.DecisionField{Name: n, Type: "string"})
		}
		return dmn.DeployedDecision{ID: id, Name: id, Model: "Stammdaten", Inputs: in, Output: dmn.DecisionField{Name: id}}
	}
	return &fakeDecisions{decisions: map[string]dmn.DeployedDecision{
		"Farben":     d("Farben"),
		"Länder":     d("Länder"),
		"Gruppen":    d("Gruppen", "land"),
		"Mitglieder": d("Mitglieder", "gruppe"),
		"Kaputt":     d("Kaputt"),
		"Text":       d("Text"),
	}}
}

func TestOptions(t *testing.T) {
	f := newFake()
	s := New(f.config())
	opts, skipped, e := s.Options(context.Background(), "Farben", nil)
	if e != nil {
		t.Fatalf("Options: %v", e)
	}
	if len(opts) != 2 || opts[1].Label != "grün" || skipped != 1 {
		t.Errorf("Options = %+v, skipped %d", opts, skipped)
	}
	opts, _, e = s.Options(context.Background(), "Mitglieder", map[string]any{"gruppe": "IT", "limit": json.Number("3")})
	if e != nil || len(opts) != 2 || opts[1].Label != "Ben" {
		t.Fatalf("Options with an input = %+v, %v", opts, e)
	}

	for _, tc := range []struct {
		id     string
		inputs map[string]any
		status int
		code   string
		says   string
	}{
		{"Unbekannt", nil, http.StatusNotFound, "not_deployed", "publish it"},
		{"Mitglieder", nil, http.StatusUnprocessableEntity, "unbound_input", `"gruppe"`},
		{"Kaputt", nil, http.StatusUnprocessableEntity, "evaluation_failed", "boom"},
		{"Text", nil, http.StatusUnprocessableEntity, "not_a_list", "a string"},
	} {
		_, _, e := s.Options(context.Background(), tc.id, tc.inputs)
		if e == nil || e.Status != tc.status || e.Code != tc.code || !strings.Contains(e.Message, tc.says) {
			t.Errorf("Options(%s) = %+v; want %d %s saying %q", tc.id, e, tc.status, tc.code, tc.says)
		}
	}
}

func TestHandleList(t *testing.T) {
	s := New(newFake().config())
	rec := httptest.NewRecorder()
	s.HandleList(rec, httptest.NewRequest(http.MethodGet, "/api/v1/form-data/decisions", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body)
	}
	var body struct {
		Decisions []struct {
			ID, Name, Model string
			Inputs          []struct{ Name, Type string }
		} `json:"decisions"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if len(body.Decisions) != 3 || body.Decisions[0].ID != "Farben" || body.Decisions[0].Inputs == nil {
		t.Fatalf("listed %+v; want the three decisions, an empty list of inputs for one that takes none", body.Decisions)
	}
	// A decision that takes inputs is offered with them: the panel binds each one.
	if in := body.Decisions[2].Inputs; len(in) != 1 || in[0].Name != "gruppe" || in[0].Type != "string" {
		t.Errorf("Mitglieder's inputs = %+v", in)
	}
	if !strings.Contains(rec.Body.String(), `"inputs":[]`) {
		t.Errorf("a decision without inputs lists them as null: %s", rec.Body)
	}
}

func TestHandleEvaluate(t *testing.T) {
	s := New(newFake().config())
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/v1/form-data/decisions/{decisionId}/options", s.HandleEvaluate)
	post := func(path, body string) (int, map[string]any) {
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, path, strings.NewReader(body)))
		var out map[string]any
		_ = json.Unmarshal(rec.Body.Bytes(), &out)
		return rec.Code, out
	}

	code, out := post("/api/v1/form-data/decisions/L%C3%A4nder/options", "")
	if code != http.StatusOK || out["decisionId"] != "Länder" || len(out["options"].([]any)) != 2 {
		t.Fatalf("an empty body: %d %v", code, out)
	}
	code, out = post("/api/v1/form-data/decisions/Mitglieder/options", `{"inputs":{"gruppe":"Vertrieb"}}`)
	if code != http.StatusOK || len(out["options"].([]any)) != 1 {
		t.Fatalf("with an input: %d %v", code, out)
	}
	code, out = post("/api/v1/form-data/decisions/Mitglieder/options", `{}`)
	if code != http.StatusUnprocessableEntity || out["code"] != "unbound_input" {
		t.Errorf("without the input: %d %v", code, out)
	}
	code, out = post("/api/v1/form-data/decisions/Mitglieder/options", `{"inputs":{},"decisionId":"x"}`)
	if code != http.StatusBadRequest || out["code"] != "invalid_query" {
		t.Errorf("an unknown member: %d %v", code, out)
	}
	code, out = post("/api/v1/form-data/decisions/Mitglieder/options", strings.Repeat(" ", maxRequestBytes+1))
	if code != http.StatusBadRequest || !strings.Contains(fmt.Sprint(out["error"]), "larger") {
		t.Errorf("an oversized body: %d %v", code, out)
	}
}

// dependentForm chooses a country, then a group of that country, then members of the
// group; a fixed-input field beside them; and a dynamic list of rows, each with a group
// and the members of that row's group, plus a field that reads the country outside the
// rows.
const dependentForm = `{"components":[
  {"type":"select","key":"land","label":"Land","values":[{"label":"Schweiz","value":"CH"},{"label":"Deutschland","value":"DE"}]},
  {"type":"select","key":"gruppe","label":"Gruppe","atlasDecision":{"version":1,"decisionId":"Gruppen","inputs":[{"name":"land","field":"land"}]}},
  {"type":"checklist","key":"mitglieder","label":"Mitglieder","atlasDecision":{"version":1,"decisionId":"Mitglieder","inputs":[{"name":"gruppe","field":"gruppe"}]}},
  {"type":"textfield","key":"frei"},
  {"type":"radio","key":"frei_mitglied","atlasDecision":{"version":1,"decisionId":"Mitglieder","inputs":[{"name":"gruppe","field":"frei"}]}},
  {"type":"select","key":"fest","atlasDecision":{"version":1,"decisionId":"Mitglieder","inputs":[{"name":"gruppe","value":"Vertrieb"},{"name":"limit","value":3}]}},
  {"type":"select","key":"aus","disabled":true,"defaultValue":"IT","values":[{"label":"IT","value":"IT"},{"label":"Geheim","value":"Geheim"}]},
  {"type":"select","key":"von_aus","atlasDecision":{"version":1,"decisionId":"Mitglieder","inputs":[{"name":"gruppe","field":"aus"}]}},
  {"type":"dynamiclist","path":"zeilen","components":[
    {"type":"select","key":"zgruppe","values":[{"label":"IT","value":"IT"},{"label":"Vertrieb","value":"Vertrieb"}]},
    {"type":"select","key":"zmitglied","atlasDecision":{"version":1,"decisionId":"Mitglieder","inputs":[{"name":"gruppe","field":"zgruppe"}]}},
    {"type":"select","key":"zgruppe_land","atlasDecision":{"version":1,"decisionId":"Gruppen","inputs":[{"name":"land","field":"land"}]}}
  ]}
]}`

func TestFieldOptions(t *testing.T) {
	f := newFake()
	s := New(f.config())
	ctx := context.Background()
	ask := func(key string, values map[string]any) ([]Option, *Error) {
		opts, _, e := s.FieldOptions(ctx, []byte(dependentForm), key, values)
		return opts, e
	}

	// A choice in one field decides what the next one offers.
	opts, e := ask("gruppe", map[string]any{"land": "CH"})
	if e != nil || len(opts) != 2 {
		t.Fatalf("groups of CH = %+v, %v", opts, e)
	}
	opts, e = ask("mitglieder", map[string]any{"gruppe": "IT", "land": "CH"})
	if e != nil || len(opts) != 2 || opts[0].Value != "anna" {
		t.Fatalf("members of IT = %+v, %v", opts, e)
	}

	// Nothing chosen yet: nothing offered, and the decision is not asked.
	before := len(f.evals)
	if opts, e := ask("mitglieder", map[string]any{}); e != nil || opts == nil || len(opts) != 0 {
		t.Errorf("before a group is chosen = %#v, %v; want an empty list", opts, e)
	}
	if len(f.evals) != before {
		t.Errorf("the decision was asked without its input: %v", f.evals[before:])
	}

	// A fixed input needs no request, and arrives as the number it is.
	if opts, e := ask("fest", nil); e != nil || len(opts) != 1 || opts[0].Value != "carla" {
		t.Errorf("fixed inputs = %+v, %v", opts, e)
	}

	// The values a request may name are those the field depends on — here the group and,
	// before it, the country — and a value must be one the field it comes from offers.
	for _, tc := range []struct {
		key    string
		values map[string]any
		says   string
	}{
		{"mitglieder", map[string]any{"gruppe": "IT", "frei": "x"}, `"frei" is not a field`},
		{"gruppe", map[string]any{"land": "FR"}, `"FR" is not one of the choices the field "land" offers`},
		{"mitglieder", map[string]any{"gruppe": "Geheim", "land": "CH"}, `"Geheim" is not one of the choices the field "gruppe" offers`},
		{"mitglieder", map[string]any{"gruppe": "Vertrieb", "land": "DE"}, `"Vertrieb" is not one of the choices the field "gruppe" offers`},
		{"von_aus", map[string]any{"aus": "Verwaltung"}, `"Verwaltung" is not one of the choices`},
		{"gruppe", map[string]any{"land": strings.Repeat("x", maxValueBytes)}, "larger than"},
	} {
		if _, e := ask(tc.key, tc.values); e == nil || e.Status != http.StatusBadRequest || e.Code != "invalid_query" || !strings.Contains(e.Message, tc.says) {
			t.Errorf("FieldOptions(%s, %v) = %+v; want 400 invalid_query saying %q", tc.key, tc.values, e, tc.says)
		}
	}
	// A field a person types into fixes nothing: its value is the person's.
	if opts, e := ask("frei_mitglied", map[string]any{"frei": "Vertrieb"}); e != nil || len(opts) != 1 {
		t.Errorf("bound to a text field = %+v, %v", opts, e)
	}
	// A list submitted by a checklist is held element by element.
	if _, e := ask("von_aus", map[string]any{"aus": []any{"IT", "Geheim"}}); e != nil {
		t.Errorf("a list of offered values: %v", e)
	}

	if _, e := ask("land", nil); e == nil || e.Status != http.StatusNotFound || e.Code != "not_found" {
		t.Errorf("a field without a decision: %+v", e)
	}
	if _, _, e := s.FieldOptions(ctx, []byte(`{"components":[{"type":"select","atlasDecision":{}}]}`), "x", nil); e == nil || e.Code != "invalid_configuration" {
		t.Errorf("a form the save would refuse: %+v", e)
	}
}

func TestWriteFieldOptions(t *testing.T) {
	s := New(newFake().config())
	call := func(body string) (int, map[string]any) {
		rec := httptest.NewRecorder()
		s.WriteFieldOptions(rec, httptest.NewRequest(http.MethodPost, "/", strings.NewReader(body)), []byte(dependentForm), "gruppe")
		var out map[string]any
		_ = json.Unmarshal(rec.Body.Bytes(), &out)
		return rec.Code, out
	}
	if code, out := call(`{"values":{"land":"CH"}}`); code != http.StatusOK || out["field"] != "gruppe" || len(out["options"].([]any)) != 2 {
		t.Fatalf("answer = %d %v", code, out)
	}
	if code, out := call(`{"values":{"land":"CH"},"decisionId":"Geheim"}`); code != http.StatusBadRequest || out["code"] != "invalid_query" {
		t.Errorf("a request naming the decision: %d %v", code, out)
	}
	if code, out := call(`{"values":{"land":"FR"}}`); code != http.StatusBadRequest || out["code"] != "invalid_query" {
		t.Errorf("a value the bound field does not offer: %d %v", code, out)
	}
}

func TestResolve(t *testing.T) {
	f := newFake()
	s := New(f.config())
	schema := []byte(`{"components":[
	  {"type":"select","key":"farbe","atlasDecision":{"version":1,"decisionId":"Farben"}},
	  {"type":"radio","key":"farbe2","atlasDecision":{"version":1,"decisionId":"Farben"}},
	  {"type":"select","key":"kaputt","atlasDecision":{"version":1,"decisionId":"Kaputt"}},
	  {"type":"select","key":"fest","atlasDecision":{"version":1,"decisionId":"Mitglieder","inputs":[{"name":"gruppe","value":"Vertrieb"}]}},
	  {"type":"textfield","key":"g"},
	  {"type":"select","key":"bound","atlasDecision":{"version":1,"decisionId":"Mitglieder","inputs":[{"name":"gruppe","field":"g"}]}},
	  {"type":"number","key":"n","defaultValue":12.50}
	]}`)
	out := s.Resolve(context.Background(), schema)
	var doc struct {
		Components []map[string]any `json:"components"`
	}
	if err := json.Unmarshal(out, &doc); err != nil {
		t.Fatalf("resolved schema is not JSON: %v\n%s", err, out)
	}
	for _, i := range []int{0, 1, 3} {
		c := doc.Components[i]
		if _, still := c[Member]; still || c["values"] == nil {
			t.Errorf("component %d was not written in: %v", i, c)
		}
	}
	// A decision that cannot answer leaves the field as it was, and so does one bound
	// to another field: the visitor's choice there is not known yet.
	for _, i := range []int{2, 5} {
		if _, still := doc.Components[i][Member]; !still || doc.Components[i]["values"] != nil {
			t.Errorf("component %d was rewritten: %v", i, doc.Components[i])
		}
	}
	if !strings.Contains(string(out), "12.50") {
		t.Errorf("a number elsewhere in the form changed its spelling: %s", out)
	}

	for _, unchanged := range [][]byte{
		[]byte(`{"components":[{"type":"textfield","key":"a"}]}`),
		[]byte(`{"components":[{"type":"textfield","key":"a","atlasDecision":{"version":1,"decisionId":"Farben"}}]}`),
		[]byte(`{"components":[{"type":"select","key":"a","atlasDecision":{"version":1,"decisionId":"Kaputt"}}]}`),
	} {
		if got := s.Resolve(context.Background(), unchanged); string(got) != string(unchanged) {
			t.Errorf("rewritten: %s", got)
		}
	}
}

func TestCheckSubmission(t *testing.T) {
	s := New(newFake().config())
	ctx := context.Background()
	schema := []byte(dependentForm)
	ok := []map[string]any{
		{},
		{"land": "CH", "gruppe": "Vertrieb", "mitglieder": []any{"carla"}},
		{"land": "DE", "gruppe": "IT", "mitglieder": []any{"anna", "ben"}},
		{"fest": "carla", "frei": "IT", "frei_mitglied": "ben"},
		// The disabled group is not submitted: its value is what the form was opened with.
		{"von_aus": "anna"},
		{"land": "CH", "zeilen": []any{
			map[string]any{"zgruppe": "IT", "zmitglied": "ben", "zgruppe_land": "Vertrieb"},
			map[string]any{"zgruppe": "Vertrieb", "zmitglied": "carla"},
			map[string]any{},
		}},
	}
	for _, vars := range ok {
		if e := s.CheckSubmission(ctx, schema, vars, nil); e != nil {
			t.Errorf("CheckSubmission(%v) = %v; want accepted", vars, e)
		}
	}
	refused := []struct {
		vars map[string]any
		want string
	}{
		{map[string]any{"land": "CH", "gruppe": "Geheim"}, `Gruppe: "Geheim" is not one of the options`},
		{map[string]any{"land": "DE", "gruppe": "IT", "mitglieder": []any{"anna", "carla"}}, `Mitglieder: "carla"`},
		{map[string]any{"mitglieder": []any{"anna"}}, `Mitglieder: "anna"`},
		{map[string]any{"land": "DE", "gruppe": "Vertrieb", "mitglieder": []any{"carla"}}, `Gruppe: "Vertrieb"`},
		{map[string]any{"land": "FR", "gruppe": "IT"}, `Gruppe cannot be checked: "FR" is not one of the choices`},
		{map[string]any{"mitglieder": "anna", "gruppe": "IT", "land": "CH"}, "Mitglieder takes a list"},
		// Each row is held to the list its own row's group gives.
		{map[string]any{"zeilen": []any{map[string]any{"zgruppe": "IT", "zmitglied": "carla"}}}, `zmitglied: "carla"`},
	}
	for _, tc := range refused {
		e := s.CheckSubmission(ctx, schema, tc.vars, nil)
		if e == nil || e.Status != http.StatusBadRequest || e.Code != "invalid_value" || !strings.Contains(e.Message, tc.want) {
			t.Errorf("CheckSubmission(%v) = %+v; want a 400 containing %q", tc.vars, e, tc.want)
		}
	}

	// A bound field that was not submitted is read from what the form was opened with,
	// before its default value — and that is asked once.
	asked := 0
	known := func() map[string]any { asked++; return map[string]any{"aus": "Geheim"} }
	if e := s.CheckSubmission(ctx, schema, map[string]any{"von_aus": "direktion"}, known); e != nil {
		t.Errorf("with the opened-with value: %v", e)
	}
	if e := s.CheckSubmission(ctx, schema, map[string]any{"von_aus": "anna", "mitglieder": []any{}}, known); e == nil {
		t.Error("the opened-with value did not take precedence over the default")
	}
	if asked != 2 {
		t.Errorf("known was called %d times over two checks; want once each", asked)
	}

	gone := []byte(`{"components":[{"type":"select","key":"x","atlasDecision":{"version":1,"decisionId":"Unbekannt"}}]}`)
	if e := s.CheckSubmission(ctx, gone, map[string]any{"x": "a"}, nil); e == nil || e.Status != http.StatusUnprocessableEntity || e.Code != "not_deployed" {
		t.Errorf("a field whose decision is gone: %+v", e)
	}
	if e := s.CheckSubmission(ctx, gone, map[string]any{}, nil); e != nil {
		t.Errorf("an empty field needs no list: %+v", e)
	}
	if e := s.CheckSubmission(ctx, []byte(`{"components":[{"type":"select","atlasDecision":{}}]}`), nil, nil); e == nil || e.Code != "invalid_configuration" {
		t.Errorf("a form the save check would refuse: %+v", e)
	}
	disabled := []byte(`{"components":[{"type":"select","key":"x","disabled":true,"atlasDecision":{"version":1,"decisionId":"Kaputt"}}]}`)
	if e := s.CheckSubmission(ctx, disabled, map[string]any{"x": "irgendwas"}, nil); e != nil {
		t.Errorf("a disabled field is not submitted by form-js, so not checked: %+v", e)
	}
}

// TestResultOf finds the decision's own result in what an evaluation returns, whichever
// name temis keyed it by.
func TestResultOf(t *testing.T) {
	d := dmn.DeployedDecision{ID: "id", Name: "Name", Output: dmn.DecisionField{Name: "out"}}
	cases := []struct {
		out  map[string]any
		want any
	}{
		{map[string]any{"out": "a", "Name": "b"}, "a"},
		{map[string]any{"Name": "b", "x": 1}, "b"},
		{map[string]any{"id": "c", "x": 1}, "c"},
		{map[string]any{"anything": "d"}, "d"},
	}
	for _, tc := range cases {
		if got := resultOf(d, tc.out); got != tc.want {
			t.Errorf("resultOf(%v) = %v; want %v", tc.out, got, tc.want)
		}
	}
	several := map[string]any{"a": 1, "b": 2}
	if got, ok := resultOf(d, several).(map[string]any); !ok || len(got) != 2 {
		t.Errorf("resultOf(several) = %v; want the context itself", got)
	}
}

func TestQuote(t *testing.T) {
	for v, want := range map[any]string{"a": `"a"`, json.Number("2"): "2", true: "true"} {
		if got := quote(v); got != want {
			t.Errorf("quote(%#v) = %s; want %s", v, got, want)
		}
	}
	if got := quote(map[string]any{"f": func() {}}); got == "" {
		t.Error("an unmarshalable value is still named")
	}
}

func TestErrorWrite(t *testing.T) {
	rec := httptest.NewRecorder()
	(&Error{Status: http.StatusBadRequest, Code: "invalid_value", Message: "nope"}).Write(rec)
	if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), `"code":"invalid_value"`) || !strings.Contains(rec.Body.String(), `"error":"nope"`) {
		t.Errorf("Write = %d %s", rec.Code, rec.Body)
	}
	if (&Error{Message: "m"}).Error() != "m" {
		t.Error("Error() is not the message")
	}
}
