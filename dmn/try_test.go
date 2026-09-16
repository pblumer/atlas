package dmn_test

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/pblumer/atlas/dmn"
)

// Trying a decision before anything is deployed (ADR-0326).
//
// Try is a pure function of the request: nothing is keyed, stored, registered or
// cleaned up, and the registry deployed processes are bound to is neither read nor
// written. That is the property the whole feature rests on — an author trying a
// table must not be able to move something that is running — and it is the one a
// future change is most likely to break by reaching for a convenience.
//
// The three not-OK answers are the other half. A model that does not compile, a
// decision the model does not provide and an evaluation that errored are the
// normal output of authoring, not a caller's mistake, so each comes back as a
// result to render with a message saying which. A caller that could not tell them
// apart would have to guess whether to fix the XML, the id or the inputs.

// greeting is one decision over one input, with an output nobody has to decode.
const greeting = `<?xml version="1.0" encoding="UTF-8"?>
<definitions xmlns="https://www.omg.org/spec/DMN/20230324/MODEL/" id="d" name="Begruessung" namespace="http://atlas/dmn">
  <inputData id="n" name="Alter"/>
  <decision id="Einstufung" name="Einstufung">
    <informationRequirement><requiredInput href="#n"/></informationRequirement>
    <literalExpression id="l"><text>if Alter >= 18 then "erwachsen" else "minderjaehrig"</text></literalExpression>
  </decision>
</definitions>`

// ambiguous is the authoring mistake this surface most exists to catch: a UNIQUE
// table whose rules overlap. It compiles — the overlap is a property of the inputs,
// not of the model — and fails when it is run, which is the whole reason an author
// wants to run one before deploying it.
const ambiguous = `<?xml version="1.0" encoding="UTF-8"?>
<definitions xmlns="https://www.omg.org/spec/DMN/20230324/MODEL/" id="d2" name="Stufen" namespace="http://atlas/dmn">
  <inputData id="n2" name="Alter"/>
  <decision id="Stufe" name="Stufe">
    <informationRequirement><requiredInput href="#n2"/></informationRequirement>
    <decisionTable id="dt" hitPolicy="UNIQUE">
      <input id="i1"><inputExpression id="ie1" typeRef="number"><text>Alter</text></inputExpression></input>
      <output id="o1" name="Stufe" typeRef="string"/>
      <rule id="r1"><inputEntry id="e1"><text>&gt;= 0</text></inputEntry><outputEntry id="v1"><text>"a"</text></outputEntry></rule>
      <rule id="r2"><inputEntry id="e2"><text>&gt;= 10</text></inputEntry><outputEntry id="v2"><text>"b"</text></outputEntry></rule>
    </decisionTable>
  </decision>
</definitions>`

func tryIt(t *testing.T, src, decision string, inputs map[string]any) dmn.Trial {
	t.Helper()
	v := dmn.NewValidator(dmn.DirResolver{Dir: t.TempDir()})
	return v.Try(context.Background(), []byte(src), decision, inputs)
}

// TestTryingADecisionAnswersWithWhatItProduced.
func TestTryingADecisionAnswersWithWhatItProduced(t *testing.T) {
	got := tryIt(t, greeting, "Einstufung", map[string]any{"Alter": 21})
	if !got.OK {
		t.Fatalf("a decision that runs came back not ok: %q", got.Message)
	}
	if got.DecisionID != "Einstufung" {
		t.Errorf("decisionId = %q", got.DecisionID)
	}
	if got.Outputs["Einstufung"] != "erwachsen" {
		t.Errorf("outputs = %#v, want the decision's own answer", got.Outputs)
	}
	// The trace is what makes this worth more than a result: an author asking
	// whether a table does what they meant is asking which rule fired.
	if len(got.Trace) == 0 {
		t.Error("nothing came back saying how the answer was reached")
	}
	// And the model is described alongside, because the panel's form is built from
	// temis's own view rather than from XML a client re-parsed.
	if got.ModelName != "Begruessung" || len(got.Decisions) != 1 {
		t.Errorf("model = %q with %d decisions", got.ModelName, len(got.Decisions))
	}
}

// TestDescribingIsTheSameQuestionAskedWithoutADecision.
//
// Naming no decision evaluates nothing and describes the model. It is one question
// asked twice over rather than two modes: the panel has to know what it may run,
// and what that wants, before it can ask for a run.
func TestDescribingIsTheSameQuestionAskedWithoutADecision(t *testing.T) {
	got := tryIt(t, greeting, "", nil)
	if !got.OK {
		t.Fatalf("describing a model that compiles came back not ok: %q", got.Message)
	}
	if got.DecisionID != "" || got.Outputs != nil || len(got.Trace) != 0 {
		t.Errorf("naming no decision ran one anyway: %+v", got)
	}
	if got.ModelName != "Begruessung" || len(got.Decisions) != 1 || got.Decisions[0].ID != "Einstufung" {
		t.Errorf("the model was not described: %+v", got)
	}
}

// TestTheThreeWaysThisCanFailAreToldApart.
//
// Each is a result with a message rather than an error, and each message has to
// say which thing to fix: the XML, the id, or the inputs.
func TestTheThreeWaysThisCanFailAreToldApart(t *testing.T) {
	for _, c := range []struct {
		name     string
		src      string
		decision string
		inputs   map[string]any
		says     string
	}{
		{"a model that is not a model", "not xml at all", "Einstufung", nil, ""},
		{"a model that does not compile", strings.Replace(greeting,
			`if Alter >= 18 then "erwachsen" else "minderjaehrig"`, `if then`, 1), "Einstufung", nil, ""},
		{"a decision the model does not provide", greeting, "Gibtsnicht", nil, "no decision called Gibtsnicht"},
		{"a table whose rules overlap where it said they would not", ambiguous, "Stufe",
			map[string]any{"Alter": 21}, ""},
	} {
		got := tryIt(t, c.src, c.decision, c.inputs)
		if got.OK {
			t.Errorf("%s: came back ok", c.name)
			continue
		}
		if strings.TrimSpace(got.Message) == "" {
			t.Errorf("%s: came back not ok with nothing said", c.name)
		}
		if c.says != "" && !strings.Contains(got.Message, c.says) {
			t.Errorf("%s: message = %q, want it to say %q", c.name, got.Message, c.says)
		}
		// Decisions is an array on every answer, never null: the panel renders it
		// without asking whether it is there.
		if got.Decisions == nil {
			t.Errorf("%s: Decisions is nil rather than empty", c.name)
		}
	}
}

// TestTryingADecisionRegistersNothing.
//
// The property the feature rests on. A model tried here must not become resolvable
// — the next decision that names it by id has to fail to find it, exactly as it
// would have before anybody pressed try.
func TestTryingADecisionRegistersNothing(t *testing.T) {
	dir := t.TempDir()
	v := dmn.NewValidator(dmn.DirResolver{Dir: dir})
	if got := v.Try(context.Background(), []byte(greeting), "Einstufung",
		map[string]any{"Alter": 7}); !got.OK {
		t.Fatalf("try: %q", got.Message)
	}
	entries, err := readDirNames(dir)
	if err != nil {
		t.Fatalf("read dir: %v", err)
	}
	if len(entries) != 0 {
		t.Errorf("trying a decision left %v behind", entries)
	}
}

// readDirNames is what "nothing was stored" is checked against: the resolver's own
// directory, which is where a model would land if trying one ever deployed it.
func readDirNames(dir string) ([]string, error) {
	f, err := os.Open(dir)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	return f.Readdirnames(-1)
}
