package dmn_test

import (
	"context"
	"reflect"
	"testing"

	"github.com/pblumer/atlas/dmn"
)

// colorsModel is a decision whose logic is a boxed list (DMN §10.3.2.7): no inputs,
// one value per entry — the shape a form's options are read from.
const colorsModel = `<?xml version="1.0" encoding="UTF-8"?>
<definitions xmlns="https://www.omg.org/spec/DMN/20191111/MODEL/" id="colordefs" name="Farben" namespace="http://atlas/dmn">
  <decision id="colors" name="Farben">
    <variable name="Farben"/>
    <list id="colorsList">
      <literalExpression><text>"rot"</text></literalExpression>
      <literalExpression><text>{value: "gr", label: "grün"}</text></literalExpression>
    </list>
  </decision>
</definitions>`

const colorsModelV2 = `<?xml version="1.0" encoding="UTF-8"?>
<definitions xmlns="https://www.omg.org/spec/DMN/20191111/MODEL/" id="colordefs" name="Farben" namespace="http://atlas/dmn">
  <decision id="colors" name="Farben">
    <variable name="Farben"/>
    <list id="colorsList">
      <literalExpression><text>"blau"</text></literalExpression>
    </list>
  </decision>
</definitions>`

// TestLatestDeployedDecision is what a form's options source reads: the newest
// decision *deployment* providing a decision (ADR-0319), described with its inputs,
// and the key to evaluate it under. A model bundled with a process is not a decision
// in its own right and is not offered, whatever the legacy latest pointer says.
func TestLatestDeployedDecision(t *testing.T) {
	reg := dmn.NewRegistry()

	if _, _, ok := reg.LatestDeployedDecision("Farben"); ok {
		t.Fatal("an empty registry offered a decision")
	}
	if got := reg.LatestDeployedDecisions(); len(got) != 0 {
		t.Fatalf("an empty registry listed %+v", got)
	}

	// Bundled with a process: reachable under the process's key, but not offered.
	if err := reg.Deploy(7, []byte(colorsModel)); err != nil {
		t.Fatalf("Deploy: %v", err)
	}
	if d, key, ok := reg.LatestDeployedDecision("Farben"); ok {
		t.Fatalf("a bundled model was offered as a decision deployment: %+v under %d", d, key)
	}

	if err := reg.DeployDecision(101, []byte(colorsModel)); err != nil {
		t.Fatalf("DeployDecision: %v", err)
	}
	if err := reg.DeployDecision(102, []byte(dishModel)); err != nil {
		t.Fatalf("DeployDecision dish: %v", err)
	}
	d, key, ok := reg.LatestDeployedDecision("Farben")
	if !ok || key != 101 {
		t.Fatalf("LatestDeployedDecision = %+v, %d, %v; want key 101", d, key, ok)
	}
	if d.ID != "Farben" || d.Model != "Farben" || len(d.Inputs) != 0 || d.Output.Name != "Farben" {
		t.Errorf("described as %+v; want Farben, no inputs, output Farben", d)
	}
	dish, _, ok := reg.LatestDeployedDecision("Dish")
	if !ok || len(dish.Inputs) != 1 || dish.Inputs[0].Name != "Season" {
		t.Errorf("Dish described as %+v, %v; want its one input Season", dish, ok)
	}

	list := reg.LatestDeployedDecisions()
	var ids []string
	for _, x := range list {
		ids = append(ids, x.ID)
	}
	if !reflect.DeepEqual(ids, []string{"Dish", "Farben"}) {
		t.Errorf("LatestDeployedDecisions ids = %v; want [Dish Farben], sorted", ids)
	}

	// A newer version is what is offered and evaluated.
	if err := reg.DeployDecision(103, []byte(colorsModelV2)); err != nil {
		t.Fatalf("DeployDecision v2: %v", err)
	}
	if _, key, _ := reg.LatestDeployedDecision("Farben"); key != 103 {
		t.Fatalf("after v2 the key is %d; want 103", key)
	}
	out, err := reg.Evaluate(context.Background(), 103, "Farben", nil)
	if err != nil {
		t.Fatalf("Evaluate v2: %v", err)
	}
	if want := []any{"blau"}; !reflect.DeepEqual(out["Farben"], want) {
		t.Errorf("v2 evaluates to %#v; want %#v", out["Farben"], want)
	}

	// Removing the newest version falls back to the one before, as a restart would.
	reg.UndeployDecision(103)
	if _, key, ok := reg.LatestDeployedDecision("Farben"); !ok || key != 101 {
		t.Fatalf("after undeploying v2: key %d, %v; want 101", key, ok)
	}
}
