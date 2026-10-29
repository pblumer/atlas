package formdecision

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/pblumer/atlas/dmn"
)

// fakeDecisions is a registry of decision deployments as this area sees one.
type fakeDecisions struct {
	decisions map[string]dmn.DeployedDecision
	results   map[string]map[string]any
	fail      map[string]error
	evals     int
}

func (f *fakeDecisions) config() Config {
	return Config{
		Decision: func(id string) (dmn.DeployedDecision, uint64, bool) {
			d, ok := f.decisions[id]
			return d, 1, ok
		},
		Decisions: func() []dmn.DeployedDecision {
			var out []dmn.DeployedDecision
			for _, id := range []string{"Farben", "Länder", "Preis"} {
				if d, ok := f.decisions[id]; ok {
					out = append(out, d)
				}
			}
			return out
		},
		Evaluate: func(_ context.Context, key uint64, id string) (map[string]any, error) {
			f.evals++
			if key != 1 {
				return nil, errors.New("wrong key")
			}
			if err := f.fail[id]; err != nil {
				return nil, err
			}
			return f.results[id], nil
		},
	}
}

func newFake() *fakeDecisions {
	return &fakeDecisions{
		decisions: map[string]dmn.DeployedDecision{
			"Farben": {ID: "Farben", Name: "Farben", Model: "Stammdaten", Output: dmn.DecisionField{Name: "Farben"}},
			"Länder": {ID: "Länder", Name: "Länder", Model: "Stammdaten", Output: dmn.DecisionField{Name: "Länder"}},
			"Preis":  {ID: "Preis", Name: "Preis", Model: "Preise", Inputs: []dmn.DecisionField{{Name: "Menge"}}, Output: dmn.DecisionField{Name: "Preis"}},
			"Kaputt": {ID: "Kaputt", Name: "Kaputt", Output: dmn.DecisionField{Name: "Kaputt"}},
			"Text":   {ID: "Text", Name: "Text", Output: dmn.DecisionField{Name: "Text"}},
		},
		results: map[string]map[string]any{
			"Farben": {"Farben": []any{"rot", map[string]any{"value": "gr", "label": "grün"}, nil}},
			"Länder": {"Länder": []any{"CH", "DE"}},
			"Text":   {"Text": "kein Verzeichnis"},
		},
		fail: map[string]error{"Kaputt": errors.New("dmn: evaluate: boom")},
	}
}

func TestOptions(t *testing.T) {
	f := newFake()
	s := New(f.config())
	opts, skipped, e := s.Options(context.Background(), "Farben")
	if e != nil {
		t.Fatalf("Options: %v", e)
	}
	if len(opts) != 2 || opts[1].Label != "grün" || skipped != 1 {
		t.Errorf("Options = %+v, skipped %d", opts, skipped)
	}

	for id, want := range map[string]struct {
		status int
		code   string
	}{
		"Unbekannt": {http.StatusNotFound, "not_deployed"},
		"Preis":     {http.StatusUnprocessableEntity, "takes_inputs"},
		"Kaputt":    {http.StatusUnprocessableEntity, "evaluation_failed"},
		"Text":      {http.StatusUnprocessableEntity, "not_a_list"},
	} {
		_, _, e := s.Options(context.Background(), id)
		if e == nil || e.Status != want.status || e.Code != want.code {
			t.Errorf("Options(%s) = %+v; want %d %s", id, e, want.status, want.code)
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
		} `json:"decisions"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	var ids []string
	for _, d := range body.Decisions {
		ids = append(ids, d.ID)
	}
	// A decision that takes inputs cannot give a form its options, so it is not offered.
	if strings.Join(ids, ",") != "Farben,Länder" {
		t.Errorf("listed %v; want Farben and Länder", ids)
	}
	if body.Decisions[0].Model != "Stammdaten" {
		t.Errorf("model = %q", body.Decisions[0].Model)
	}
}

func TestHandleOptions(t *testing.T) {
	s := New(newFake().config())
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/form-data/decisions/{decisionId}/options", s.HandleOptions)

	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/form-data/decisions/L%C3%A4nder/options", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body)
	}
	var body struct {
		DecisionID string   `json:"decisionId"`
		Options    []Option `json:"options"`
		Skipped    int      `json:"skipped"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.DecisionID != "Länder" || len(body.Options) != 2 || body.Options[0].Value != "CH" || body.Options[0].Label != "CH" {
		t.Errorf("body = %+v", body)
	}

	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/form-data/decisions/Preis/options", nil))
	var refusal map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &refusal)
	if rec.Code != http.StatusUnprocessableEntity || refusal["code"] != "takes_inputs" || !strings.Contains(refusal["error"].(string), "Menge") {
		t.Errorf("a decision with inputs: %d %v", rec.Code, refusal)
	}
}

func TestResolve(t *testing.T) {
	f := newFake()
	s := New(f.config())
	schema := []byte(`{"components":[
	  {"type":"select","key":"farbe","atlasDecision":{"version":1,"decisionId":"Farben"}},
	  {"type":"radio","key":"farbe2","atlasDecision":{"version":1,"decisionId":"Farben"}},
	  {"type":"select","key":"preis","atlasDecision":{"version":1,"decisionId":"Preis"}},
	  {"type":"number","key":"n","defaultValue":12.50}
	]}`)
	out := s.Resolve(context.Background(), schema)
	var doc struct {
		Components []map[string]any `json:"components"`
	}
	if err := json.Unmarshal(out, &doc); err != nil {
		t.Fatalf("resolved schema is not JSON: %v\n%s", err, out)
	}
	for _, i := range []int{0, 1} {
		c := doc.Components[i]
		if _, still := c[Member]; still {
			t.Errorf("component %d still names the decision: %v", i, c)
		}
		vals, _ := c["values"].([]any)
		if len(vals) != 2 {
			t.Errorf("component %d values = %v; want the decision's two options", i, c["values"])
		}
	}
	// A decision that cannot answer leaves the field as it was: the viewer says the
	// options could not be loaded, rather than offering nothing as though that were
	// the list.
	if _, still := doc.Components[2][Member]; !still || doc.Components[2]["values"] != nil {
		t.Errorf("an unanswerable field was rewritten: %v", doc.Components[2])
	}
	if !strings.Contains(string(out), "12.50") {
		t.Errorf("a number elsewhere in the form changed its spelling: %s", out)
	}
	// Farben once for both of its fields; Preis is refused for its inputs before
	// anything is evaluated.
	if f.evals != 1 {
		t.Errorf("evaluated %d times; want once", f.evals)
	}

	plain := []byte(`{"components":[{"type":"textfield","key":"a"}]}`)
	if got := s.Resolve(context.Background(), plain); string(got) != string(plain) {
		t.Errorf("a form without a decision field was rewritten: %s", got)
	}
	broken := []byte(`{"components":[{"type":"textfield","key":"a","atlasDecision":{"version":1,"decisionId":"Farben"}}]}`)
	if got := s.Resolve(context.Background(), broken); string(got) != string(broken) {
		t.Errorf("a form the save check refuses was rewritten: %s", got)
	}
}

func TestCheckSubmission(t *testing.T) {
	s := New(newFake().config())
	schema := []byte(`{"components":[
	  {"type":"select","key":"farbe","label":"Farbe","atlasDecision":{"version":1,"decisionId":"Farben"}},
	  {"type":"checklist","key":"laender","atlasDecision":{"version":1,"decisionId":"Länder"}},
	  {"type":"select","key":"aus","disabled":true,"atlasDecision":{"version":1,"decisionId":"Kaputt"}},
	  {"type":"dynamiclist","path":"pos","components":[
	    {"type":"select","key":"land","atlasDecision":{"version":1,"decisionId":"Länder"}}
	  ]}
	]}`)
	ok := []map[string]any{
		{},
		{"farbe": "rot", "laender": []any{"CH", "DE"}},
		{"farbe": "gr", "laender": []any{}},
		{"farbe": nil, "laender": nil, "aus": "irgendwas"},
		{"farbe": "", "pos": []any{map[string]any{"land": "DE"}, map[string]any{}}},
	}
	for _, vars := range ok {
		if e := s.CheckSubmission(context.Background(), schema, vars); e != nil {
			t.Errorf("CheckSubmission(%v) = %v; want accepted", vars, e)
		}
	}
	refused := []struct {
		vars map[string]any
		want string
	}{
		{map[string]any{"farbe": "grün"}, `Farbe: "grün" is not one of the options`},
		{map[string]any{"laender": []any{"CH", "FR"}}, `laender: "FR" is not one of the options`},
		{map[string]any{"laender": "CH"}, "laender takes a list"},
		{map[string]any{"pos": []any{map[string]any{"land": "IT"}}}, `land: "IT"`},
	}
	for _, tc := range refused {
		e := s.CheckSubmission(context.Background(), schema, tc.vars)
		if e == nil || e.Status != http.StatusBadRequest || e.Code != "invalid_value" || !strings.Contains(e.Message, tc.want) {
			t.Errorf("CheckSubmission(%v) = %+v; want a 400 containing %q", tc.vars, e, tc.want)
		}
	}

	// A value whose list cannot be read now cannot be checked, and is not completed
	// unchecked.
	gone := []byte(`{"components":[{"type":"select","key":"x","atlasDecision":{"version":1,"decisionId":"Unbekannt"}}]}`)
	if e := s.CheckSubmission(context.Background(), gone, map[string]any{"x": "a"}); e == nil || e.Status != http.StatusUnprocessableEntity || e.Code != "not_deployed" {
		t.Errorf("a field whose decision is gone: %+v", e)
	}
	if e := s.CheckSubmission(context.Background(), gone, map[string]any{}); e != nil {
		t.Errorf("an empty field needs no list: %+v", e)
	}
	if e := s.CheckSubmission(context.Background(), []byte(`{"components":[{"type":"select","atlasDecision":{}}]}`), nil); e == nil || e.Code != "invalid_configuration" {
		t.Errorf("a form the save check would refuse: %+v", e)
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
	// Several results, none of them the decision's: a context, which is not a list.
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
