package dmn_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pblumer/atlas/dmn"
)

// The depth suite: every model-facing entry point, pointed at testdata/layered-service.dmn.
//
// It exists because of a defect that survived a full green suite. A check scoped
// to a decision's *directly* declared inputs looked complete against every fixture
// here, and was blind to anything built on top of one — which is the shape a
// well-factored DRG has at the top, and exactly where a business rule task points
// (ADR-0419). Every fixture was single-table, so nothing asked the question.
//
// The rule this suite encodes: a check that reads a DMN model is not finished
// until it has been run against a model with a decision that declares no input of
// its own. Adding a model-facing entry point means adding a case here.
const layeredFixture = "testdata/layered-service.dmn"

func layeredXML(t *testing.T) []byte {
	t.Helper()
	b, err := os.ReadFile(layeredFixture)
	if err != nil {
		t.Fatalf("read %s: %v", layeredFixture, err)
	}
	return b
}

// layeredValidator serves the fixture under the handle "layered", so the
// modelRef-taking entry points can be exercised the way the server calls them.
func layeredValidator(t *testing.T) *dmn.Validator {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "layered.dmn"), layeredXML(t), 0o600); err != nil {
		t.Fatalf("seed model dir: %v", err)
	}
	return dmn.NewValidator(dmn.DirResolver{Dir: dir})
}

func layeredRegistry(t *testing.T) *dmn.Registry {
	t.Helper()
	reg := dmn.NewRegistry()
	if err := reg.Deploy(1, layeredXML(t)); err != nil {
		t.Fatalf("deploy the depth fixture: %v", err)
	}
	return reg
}

// The fixture's defining property, asserted rather than assumed: the top decision
// declares no input of its own. If a model change ever gave it one, every
// depth assertion below would still pass while testing nothing, so the property is
// checked first and separately.
func TestTheDepthFixtureHasADecisionWithNoInputsOfItsOwn(t *testing.T) {
	src := string(layeredXML(t))
	top := src[strings.Index(src, `<decision id="dec_urteil"`):]
	top = top[:strings.Index(top, "</decision>")]
	if strings.Contains(top, "requiredInput") {
		t.Fatal("Gesamturteil declares a direct input — the fixture no longer has depth")
	}
	if strings.Count(top, "requiredDecision") != 3 {
		t.Fatalf("Gesamturteil should require exactly the three decisions under it")
	}
}

// Evaluation, at every level and through the service, with the types the caller
// actually sends: JSON numbers, strings, booleans and an ISO date.
func TestTheDepthFixtureEvaluatesAtEveryLevel(t *testing.T) {
	reg := layeredRegistry(t)
	// risikoKlasse "B" falls to "hoch", which decides the composed answer on its
	// own. The date is deliberately not load-bearing here: it is a known gap
	// through a composed decision, and that gap has its own test below rather than
	// being allowed to make this one flap.
	in := map[string]any{
		"antragBetrag": 30000,
		"kundeSeit":    "2018-05-01",
		"risikoKlasse": "B",
		"aktiv":        true,
	}
	for _, c := range []struct{ target, key, want string }{
		{"Tragbar", "tragbar", "true"},
		{"Treue", "treu", "true"},
		{"Risiko", "risiko", "hoch"},
		{"Gesamturteil", "urteil", "abgelehnt"},
		{"Pruefung", "urteil", "abgelehnt"},
	} {
		out, err := reg.Evaluate(context.Background(), 1, c.target, in)
		if err != nil {
			t.Errorf("Evaluate %s: %v", c.target, err)
			continue
		}
		if got := strings.TrimSpace(strings.Trim(toText(out[c.key]), `"`)); got != c.want {
			t.Errorf("%s -> %v, want %v", c.target, out[c.key], c.want)
		}
	}
}

func toText(v any) string {
	switch x := v.(type) {
	case string:
		return x
	case bool:
		if x {
			return "true"
		}
		return "false"
	default:
		return ""
	}
}

// The depth assertion itself: a wrongly-typed leaf input is refused when the task
// names the decision three levels above it, where that decision declares no input
// of its own (ADR-0419). This is the case the single-table fixtures could not pose.
func TestADepthFixtureLeafIsCheckedFromTheTop(t *testing.T) {
	reg := layeredRegistry(t)
	base := map[string]any{
		"antragBetrag": 30000,
		"kundeSeit":    "2018-05-01",
		"risikoKlasse": "A",
		"aktiv":        true,
	}
	for _, c := range []struct {
		input    string
		bad      any
		wantType string
	}{
		{"antragBetrag", "30000", "number"},
		{"risikoKlasse", 1, "string"},
		{"aktiv", "ja", "boolean"},
		{"kundeSeit", "irgendwann", "date"},
	} {
		in := map[string]any{}
		for k, v := range base {
			in[k] = v
		}
		in[c.input] = c.bad
		_, err := reg.Evaluate(context.Background(), 1, "Gesamturteil", in)
		if err == nil {
			t.Errorf("%s = %v: no error, want the job refused from the top decision", c.input, c.bad)
			continue
		}
		if !strings.Contains(err.Error(), c.input) || !strings.Contains(err.Error(), c.wantType) {
			t.Errorf("%s: error = %q, want it to name the input and %q", c.input, err, c.wantType)
		}
	}
}

// A declared date reaches a decision as a date when that decision declares it
// (ADR-0419, temis ADR-0040).
func TestTheDepthFixtureComparesADateAsADate(t *testing.T) {
	reg := layeredRegistry(t)
	in := func(seit string) map[string]any {
		return map[string]any{"antragBetrag": 30000, "kundeSeit": seit, "risikoKlasse": "B", "aktiv": true}
	}
	for _, c := range []struct{ seit, want string }{{"2018-05-01", "true"}, {"2024-05-01", "false"}} {
		out, err := reg.Evaluate(context.Background(), 1, "Treue", in(c.seit))
		if err != nil {
			t.Fatalf("Evaluate Treue(%s): %v", c.seit, err)
		}
		if got := toText(out["treu"]); got != c.want {
			t.Errorf("Treue(%s) = %v, want %v — the date was compared as a string", c.seit, out["treu"], c.want)
		}
	}
}

// A declared type reaches a decision through the whole requirements cone, not
// only the decision that declares it (temis ADR-0041).
//
// This fixture found the gap this test now guards. temis converted an input by
// the schema of the decision being evaluated, and that schema was its *directly*
// declared inputs; `Gesamturteil` declares none, so nothing was converted.
// `kundeSeit` reached `Treue` as the text it was sent as, `kundeSeit < date(...)`
// was null, and `Treue` contributed null to a table that cannot tell null from
// false. The same decision evaluated on its own was right, which is what made it
// so hard to see: the wrong answer appeared two levels above its cause.
//
// It was the same defect ADR-0419 fixed on the checking side, on the conversion
// side — both scoped to direct inputs where the reachable ones are what a caller
// sends. Asserted here from the top, because that is where a business rule task
// points.
func TestADeclaredDateIsConvertedThroughAComposedDecision(t *testing.T) {
	reg := layeredRegistry(t)
	in := func(seit string) map[string]any {
		return map[string]any{"antragBetrag": 30000, "kundeSeit": seit, "risikoKlasse": "A", "aktiv": true}
	}

	// The premise: the date decides the answer here. tragbar and risiko are the
	// same in both rows, so only Treue can move the result — and Treue reads
	// nothing but the date.
	for _, c := range []struct{ seit, want string }{
		{"2018-05-01", "bewilligt"},         // treu -> rule 2
		{"2024-05-01", "manuelle Pruefung"}, // not treu -> the catch-all
	} {
		got, err := reg.Evaluate(context.Background(), 1, "Gesamturteil", in(c.seit))
		if err != nil {
			t.Fatalf("Evaluate Gesamturteil(%s): %v", c.seit, err)
		}
		if got["urteil"] != c.want {
			t.Errorf("Gesamturteil(kundeSeit=%s) = %v, want %q — the date reached Treue as text",
				c.seit, got["urteil"], c.want)
		}

		// And through the decision service over the same decision. Its working set
		// is built from its output decisions, which was empty for exactly the
		// services that encapsulate something — the shape a service has.
		svc, err := reg.Evaluate(context.Background(), 1, "Pruefung", in(c.seit))
		if err != nil {
			t.Fatalf("Evaluate Pruefung(%s): %v", c.seit, err)
		}
		if svc["urteil"] != c.want {
			t.Errorf("Pruefung(kundeSeit=%s) = %v, want %q — the date reached the service as text",
				c.seit, svc["urteil"], c.want)
		}
	}
}

// The describing surfaces: what the picker offers, and what a mapping row is
// checked against. The service's inputs must be the four leaves — not the
// output decision's own (empty) set.
func TestTheDepthFixtureDescribesItsServiceBoundary(t *testing.T) {
	_, infos, err := layeredValidator(t).Describe(context.Background(), "layered")
	if err != nil {
		t.Fatalf("describe: %v", err)
	}
	byID := map[string]dmn.DecisionInfo{}
	for _, i := range infos {
		byID[i.ID] = i
	}
	svc, ok := byID["Pruefung"]
	if !ok {
		t.Fatal("the decision service is not described")
	}
	if !svc.Service {
		t.Error("Pruefung is not marked as a service")
	}
	var names []string
	for _, f := range svc.Inputs {
		names = append(names, f.Name+":"+f.Type)
	}
	want := "antragBetrag:number,kundeSeit:date,risikoKlasse:string,aktiv:boolean"
	if got := strings.Join(names, ","); got != want {
		t.Errorf("service inputs = %q, want %q", got, want)
	}
	if len(byID["Gesamturteil"].Inputs) != 4 {
		t.Errorf("the composed decision should describe its four reachable inputs, got %+v", byID["Gesamturteil"].Inputs)
	}
}

// The drawing surface, on a model that carries no DMNDI: every element is placed,
// so a reader of a generated model sees the same picture the editor would.
func TestTheDepthFixtureDrawsWithoutADiagram(t *testing.T) {
	g, err := layeredValidator(t).Graph(context.Background(), "layered")
	if err != nil {
		t.Fatalf("graph: %v", err)
	}
	if !g.Resolved || !g.Valid {
		t.Fatalf("graph did not resolve: %+v", g.Message)
	}
	if len(g.Nodes) != 8 {
		t.Errorf("nodes = %d, want 8 (four inputs, four decisions)", len(g.Nodes))
	}
	for _, n := range g.Nodes {
		if n.Width <= 0 || n.Height <= 0 {
			t.Errorf("node %q has no bounds — diagram generation did not reach it", n.ID)
		}
	}
	if len(g.Edges) != 7 {
		t.Errorf("edges = %d, want 7 (four from the inputs, three into the top)", len(g.Edges))
	}
}

// The try-a-decision surface offers the composed decision and the service, not
// only the ones with inputs of their own.
func TestTheDepthFixtureCanBeTriedAtEveryLevel(t *testing.T) {
	v := layeredValidator(t)
	src := layeredXML(t)
	for _, target := range []string{"Tragbar", "Treue", "Risiko", "Gesamturteil", "Pruefung"} {
		tr := v.Try(context.Background(), src, target, map[string]any{
			"antragBetrag": 30000, "kundeSeit": "2018-05-01", "risikoKlasse": "A", "aktiv": true,
		})
		if !tr.OK {
			t.Errorf("Try %s did not run: %s", target, tr.Message)
			continue
		}
		if tr.DecisionID != target || len(tr.Outputs) == 0 {
			t.Errorf("Try %s produced no outputs (decisionId=%q)", target, tr.DecisionID)
		}
	}
}

// REMAINING GAP, narrowed by temis ADR-0041 rather than closed: a decision
// service converts its inputs by declared type now, but a wrongly-typed one is
// still not refused there.
//
// The two halves moved apart. Conversion is temis's and reaches a service,
// because its working set is built from its output decisions' cones (the test
// above proves it). Refusal is Atlas's, sits in evalDecision, and a service goes
// through evalService — where there is nothing to ask, because CompiledService
// publishes no input schema. temis ADR-0041 names that as what it does not
// decide.
//
// So the same wrong value is refused when the task names the decision and
// answers silently when it names the service over it. That asymmetry is the gap,
// and it is asserted rather than left to be rediscovered: when a service can be
// validated, this test fails and should become the assertion that it is refused.
func TestRemainingGapAServiceDoesNotRefuseAWronglyTypedInput(t *testing.T) {
	reg := layeredRegistry(t)
	bad := map[string]any{"antragBetrag": "30000", "kundeSeit": "2018-05-01", "risikoKlasse": "A", "aktiv": true}

	if _, err := reg.Evaluate(context.Background(), 1, "Gesamturteil", bad); err == nil {
		t.Fatal("the decision no longer refuses a wrongly-typed input — the premise of this gap is gone")
	}

	out, err := reg.Evaluate(context.Background(), 1, "Pruefung", bad)
	if err != nil {
		t.Fatalf("the service now refuses it too (%v).\n"+
			"That is the gap closing: turn this test into the assertion that the service "+
			"refuses, and narrow ADR-0419's open question accordingly.", err)
	}
	if out["urteil"] == nil {
		t.Fatalf("Pruefung returned nothing at all — this gap is about a silent answer, not an empty one")
	}
}
