package dmn_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pblumer/atlas/dmn"
)

// logicModel is a DMN 1.5 model with one element of each shape the peek reads
// differently: a decision table, a literal expression, a boxed context, and a
// business knowledge model whose function the context invokes.
const logicModel = `<?xml version="1.0" encoding="UTF-8"?>
<definitions xmlns="https://www.omg.org/spec/DMN/20230324/MODEL/" id="defs" name="Pricing" namespace="http://atlas/dmn">
  <inputData id="in_age" name="Age"><variable name="Age" typeRef="number"/></inputData>
  <businessKnowledgeModel id="bkm_double" name="Double">
    <variable name="Double"/>
    <encapsulatedLogic>
      <formalParameter name="x" typeRef="number"/>
      <literalExpression><text>x * 2</text></literalExpression>
    </encapsulatedLogic>
  </businessKnowledgeModel>
  <decision id="Band" name="Band">
    <variable name="Band" typeRef="string"/>
    <informationRequirement id="ir1"><requiredInput href="#in_age"/></informationRequirement>
    <decisionTable id="dt" hitPolicy="FIRST">
      <input id="i1" label="Age"><inputExpression id="ie1" typeRef="number"><text>Age</text></inputExpression></input>
      <output id="o1" name="Band" typeRef="string"/>
      <rule id="r1">
        <inputEntry id="e1"><text>&lt; 18</text></inputEntry>
        <outputEntry id="e2"><text>"young"</text></outputEntry>
      </rule>
      <rule id="r2">
        <inputEntry id="e3"><text>-</text></inputEntry>
        <outputEntry id="e4"><text>"adult"</text></outputEntry>
      </rule>
    </decisionTable>
  </decision>
  <decision id="Base" name="Base">
    <variable name="Base" typeRef="number"/>
    <informationRequirement id="ir2"><requiredInput href="#in_age"/></informationRequirement>
    <literalExpression><text>Age + 10</text></literalExpression>
  </decision>
  <decision id="Price" name="Price">
    <variable name="Price" typeRef="number"/>
    <informationRequirement id="ir3"><requiredDecision href="#Base"/></informationRequirement>
    <knowledgeRequirement id="kr1"><requiredKnowledge href="#bkm_double"/></knowledgeRequirement>
    <context>
      <contextEntry><variable name="doubled"/><literalExpression><text>Double(Base)</text></literalExpression></contextEntry>
      <contextEntry><literalExpression><text>doubled + 1</text></literalExpression></contextEntry>
    </context>
  </decision>
</definitions>`

// TestModelLogic proves the peek's data source: every decision and knowledge model
// comes back with the kind of its logic and temis's view of it, and input data —
// which has none — is left to the graph.
func TestModelLogic(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "pricing.dmn"), []byte(logicModel), 0o644); err != nil {
		t.Fatalf("write model: %v", err)
	}
	v := dmn.NewValidator(dmn.DirResolver{Dir: dir})

	ml, err := v.Logic(context.Background(), "pricing")
	if err != nil {
		t.Fatalf("Logic: %v", err)
	}
	if !ml.Resolved || !ml.Valid || ml.ModelName != "Pricing" {
		t.Fatalf("logic = %+v, want a resolved, valid Pricing model", ml)
	}
	byID := map[string]dmn.ElementLogic{}
	for _, el := range ml.Elements {
		byID[el.ID] = el
	}
	if _, ok := byID["in_age"]; ok {
		t.Errorf("input data listed with logic: %+v", byID["in_age"])
	}
	want := map[string]struct{ element, kind, contains string }{
		"Band":       {"decision", "table", `"hitPolicy":"F"`},
		"Base":       {"decision", "literal", `"text":"Age + 10"`},
		"Price":      {"decision", "context", `"text":"Double(Base)"`},
		"bkm_double": {"businessKnowledgeModel", "literal", `"text":"x * 2"`},
	}
	for id, w := range want {
		el, ok := byID[id]
		if !ok {
			t.Errorf("%s missing from %+v", id, ml.Elements)
			continue
		}
		if el.Element != w.element || el.Kind != w.kind {
			t.Errorf("%s = element %q kind %q, want %q %q", id, el.Element, el.Kind, w.element, w.kind)
		}
		b, err := json.Marshal(el.View)
		if err != nil {
			t.Fatalf("marshal %s view: %v", id, err)
		}
		if !strings.Contains(string(b), w.contains) {
			t.Errorf("%s view = %s, want it to contain %s", id, b, w.contains)
		}
	}
	if p := byID["bkm_double"].Params; len(p) != 1 || p[0].Name != "x" {
		t.Errorf("bkm params = %+v, want the one parameter x", p)
	}
}

// TestModelLogicNotServed keeps the peek's two silences apart from a failure: a
// missing model and an invalid one each come back as a message with no elements,
// and only a resolver that cannot answer is an error.
func TestModelLogicNotServed(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "broken.dmn"),
		[]byte(`<definitions xmlns="https://www.omg.org/spec/DMN/20230324/MODEL/" id="d" name="broken"><decision id="Bad" name="Bad"><literalExpression id="le"><text>1 +</text></literalExpression></decision></definitions>`), 0o644); err != nil {
		t.Fatalf("write model: %v", err)
	}
	v := dmn.NewValidator(dmn.DirResolver{Dir: dir})
	if err := os.WriteFile(filepath.Join(dir, "garbage.dmn"), []byte(`this is not a model`), 0o644); err != nil {
		t.Fatalf("write model: %v", err)
	}
	for _, ref := range []string{"missing", "broken", "garbage"} {
		ml, err := v.Logic(context.Background(), ref)
		if err != nil {
			t.Fatalf("Logic(%s): %v", ref, err)
		}
		if ml.Valid || ml.Message == "" || ml.Elements == nil || len(ml.Elements) != 0 {
			t.Errorf("Logic(%s) = %+v, want not valid, a message and an empty element list", ref, ml)
		}
	}
	if _, err := dmn.NewValidator(errResolver{}).Logic(context.Background(), "x"); err == nil {
		t.Error("Logic with a failing resolver = nil error, want an error")
	}
}

// shapesModel holds the elements whose logic is read differently from the plain
// cases above: a knowledge model whose body is a boxed context, one whose body is a
// nested function definition, and a decision whose logic is one — the two kinds temis
// names but serves no view for.
const shapesModel = `<?xml version="1.0" encoding="UTF-8"?>
<definitions xmlns="https://www.omg.org/spec/DMN/20230324/MODEL/" id="defs" name="Shapes" namespace="http://atlas/dmn">
  <businessKnowledgeModel id="bkm_ctx" name="Boxed">
    <variable name="Boxed"/>
    <encapsulatedLogic>
      <formalParameter name="x" typeRef="number"/>
      <context>
        <contextEntry><variable name="y"/><literalExpression><text>x + 1</text></literalExpression></contextEntry>
        <contextEntry><literalExpression><text>y * 2</text></literalExpression></contextEntry>
      </context>
    </encapsulatedLogic>
  </businessKnowledgeModel>
  <businessKnowledgeModel id="bkm_fn" name="Curried">
    <variable name="Curried"/>
    <encapsulatedLogic>
      <formalParameter name="a"/>
      <functionDefinition><formalParameter name="b"/><literalExpression><text>a + b</text></literalExpression></functionDefinition>
    </encapsulatedLogic>
  </businessKnowledgeModel>
  <decision id="Fn" name="Fn">
    <variable name="Fn"/>
    <functionDefinition><formalParameter name="n"/><literalExpression><text>n * 3</text></literalExpression></functionDefinition>
  </decision>
</definitions>`

// TestModelLogicShapes covers a knowledge model whose body is itself a boxed
// expression (drawn as that kind), and the function definitions temis names but
// serves no view for: a knowledge model's keeps its kind, so the peek can say what it
// is, and a decision's comes back without one.
func TestModelLogicShapes(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "shapes.dmn"), []byte(shapesModel), 0o644); err != nil {
		t.Fatalf("write model: %v", err)
	}
	ml, err := dmn.NewValidator(dmn.DirResolver{Dir: dir}).Logic(context.Background(), "shapes")
	if err != nil {
		t.Fatalf("Logic: %v", err)
	}
	if !ml.Valid {
		t.Fatalf("logic = %+v, want a valid model", ml)
	}
	byID := map[string]dmn.ElementLogic{}
	for _, el := range ml.Elements {
		byID[el.ID] = el
	}
	if el := byID["bkm_ctx"]; el.Kind != "context" || el.View == nil || len(el.Params) != 1 {
		t.Errorf("boxed knowledge model = %+v, want a context view with one parameter", el)
	}
	if el := byID["bkm_fn"]; el.Kind != "function" || el.View != nil || len(el.Params) != 1 {
		t.Errorf("knowledge model with a nested function = %+v, want kind function and no view", el)
	}
	if el, ok := byID["Fn"]; !ok || el.Kind != "" || el.View != nil {
		t.Errorf("decision whose logic is a function = %+v (listed %v), want it listed with no kind", el, ok)
	}
}
