package dmn_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pblumer/atlas/dmn"
)

// brokenModel is valid XML but its decision's FEEL literal does not compile, so
// temis reports an error diagnostic — the "resolved but invalid" case.
const brokenModel = `<?xml version="1.0" encoding="UTF-8"?>
<definitions xmlns="https://www.omg.org/spec/DMN/20191111/MODEL/" id="d" name="broken" namespace="http://atlas/dmn">
  <decision id="Bad" name="Bad">
    <literalExpression id="le"><text>1 +</text></literalExpression>
  </decision>
</definitions>`

func writeModel(t *testing.T, dir, name, xml string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(xml), 0o644); err != nil {
		t.Fatalf("write %s: %v", name, err)
	}
}

func TestValidatorValidModel(t *testing.T) {
	dir := t.TempDir()
	writeModel(t, dir, "dish.dmn", dishModel)
	v := dmn.NewValidator(dmn.DirResolver{Dir: dir})

	res, err := v.Validate(context.Background(), "dish")
	if err != nil {
		t.Fatalf("Validate: %v", err)
	}
	if !res.Resolved || !res.Valid {
		t.Fatalf("res = %+v, want resolved and valid", res)
	}
	if res.ModelName != "dish" {
		t.Errorf("ModelName = %q, want dish", res.ModelName)
	}
	found := false
	for _, d := range res.Decisions {
		if d == "Dish" {
			found = true
		}
	}
	if !found {
		t.Errorf("Decisions = %v, want it to include Dish", res.Decisions)
	}
}

func TestValidatorInvalidModel(t *testing.T) {
	dir := t.TempDir()
	writeModel(t, dir, "broken.dmn", brokenModel)
	v := dmn.NewValidator(dmn.DirResolver{Dir: dir})

	res, err := v.Validate(context.Background(), "broken")
	if err != nil {
		t.Fatalf("Validate: %v", err)
	}
	if !res.Resolved {
		t.Fatalf("res = %+v, want resolved (the XML was found)", res)
	}
	if res.Valid {
		t.Fatalf("res = %+v, want invalid (bad FEEL)", res)
	}
	if res.Message == "" {
		t.Error("want a non-empty diagnostic message for an invalid model")
	}
}

func TestValidatorUnresolved(t *testing.T) {
	v := dmn.NewValidator(dmn.DirResolver{Dir: t.TempDir()})
	res, err := v.Validate(context.Background(), "missing")
	if err != nil {
		t.Fatalf("Validate: %v", err)
	}
	if res.Resolved || res.Valid || res.Message == "" {
		t.Fatalf("res = %+v, want unresolved with a message", res)
	}
}

func TestValidatorInfraErrorPropagates(t *testing.T) {
	dir := t.TempDir()
	// A directory where the model file is expected makes the resolver fail with a
	// real I/O error, which Validate must propagate (not swallow as "unresolved").
	if err := os.MkdirAll(filepath.Join(dir, "busy.dmn"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	v := dmn.NewValidator(dmn.DirResolver{Dir: dir})
	if _, err := v.Validate(context.Background(), "busy"); err == nil {
		t.Fatal("Validate over a broken source: want an error")
	}
}

// serviceModel is a model whose decision service publishes what it is asked for,
// parameterised by the output decisions it names — none of them, for the case this
// is here to refuse.
func serviceModel(outputs string) string {
	return `<?xml version="1.0" encoding="UTF-8"?>
<definitions xmlns="https://www.omg.org/spec/DMN/20230324/MODEL/" id="d" name="Rating" namespace="http://atlas/dmn/service">
  <inputData id="in1" name="Amount"><variable id="v1" name="amount" typeRef="number"/></inputData>
  <decision id="dec1" name="Risk">
    <variable id="v2" name="risk" typeRef="string"/>
    <informationRequirement id="ir1"><requiredInput href="#in1"/></informationRequirement>
    <literalExpression id="le1"><text>if amount &gt; 100 then "high" else "low"</text></literalExpression>
  </decision>
  <decisionService id="svc1" name="Rating Service">
` + outputs + `    <encapsulatedDecision href="#dec1"/>
  </decisionService>
</definitions>`
}

// TestValidateXMLRefusesAServicePublishingNothing: a decision service with no output
// decision is not a half-finished model that still half works. temis compiles it,
// Atlas lists it, the picker offers it, a business rule task calls it — and the
// answer is empty. The failure is silence, so it is refused at the gate, where
// somebody is still holding the model.
func TestValidateXMLRefusesAServicePublishingNothing(t *testing.T) {
	v := dmn.NewValidator(dmn.DirResolver{Dir: t.TempDir()})

	res := v.ValidateXML(context.Background(), []byte(serviceModel("")))

	if res.Valid {
		t.Fatal("a decision service returning nothing was accepted; it compiles and answers with nothing, which is the whole problem")
	}
	if !res.Resolved {
		t.Error("the bytes were in hand, so the model resolved; only its content is wrong")
	}
	// The message has to name the service: a model may declare several, and "one of
	// them is broken" sends the author looking.
	if !strings.Contains(res.Message, `"Rating Service"`) {
		t.Errorf("message = %q, want it to name the service it is about", res.Message)
	}
	if !strings.Contains(res.Message, "output decision") {
		t.Errorf("message = %q, want it to say what is missing", res.Message)
	}
}

// TestValidateXMLAcceptsAServiceThatPublishes is the other half, and the one that
// keeps the guard from being a ban on decision services: the same model, with the
// one thing DMN asks for, passes.
func TestValidateXMLAcceptsAServiceThatPublishes(t *testing.T) {
	v := dmn.NewValidator(dmn.DirResolver{Dir: t.TempDir()})

	res := v.ValidateXML(context.Background(), []byte(serviceModel("    <outputDecision href=\"#dec1\"/>\n")))

	if !res.Valid {
		t.Fatalf("a service naming its output decision was refused: %s", res.Message)
	}
	if len(res.Services) != 1 || res.Services[0] != "Rating Service" {
		t.Errorf("services = %v, want the one the model declares", res.Services)
	}
}

// TestValidateRefusesAStoredServicePublishingNothing: the gate is not only the way
// in. A model stored before this rule existed — or edited in the folder behind
// Atlas's back — is refused when a deploy asks whether it is sound, so a service
// that answers with nothing cannot be shipped by a route that skips the upload.
func TestValidateRefusesAStoredServicePublishingNothing(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "rating.dmn"), []byte(serviceModel("")), 0o644); err != nil {
		t.Fatalf("write model: %v", err)
	}
	v := dmn.NewValidator(dmn.DirResolver{Dir: dir})

	res, err := v.Validate(context.Background(), "rating")

	if err != nil {
		t.Fatalf("Validate: %v", err)
	}
	if res.Valid {
		t.Fatal("a stored service returning nothing passed the deploy preflight")
	}
	if !strings.Contains(res.Message, `"Rating Service"`) {
		t.Errorf("message = %q, want it to name the service", res.Message)
	}
}
