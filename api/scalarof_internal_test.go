package api

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/pblumer/atlas/model"
)

// scalarOf turns a stored data-object value back into the Go value that marshals to
// the canonical JSON the object graph decodes. It had no test.
//
// The case worth pinning is the number: it is carried as `json.Number`, not float64,
// so a value keeps the exact text it was stored with. Through a float an order total
// of 10.10 comes back as 10.1, and an identifier long enough to exceed float64's
// integer precision comes back as a different identifier.
func TestScalarOfKeepsANumbersExactText(t *testing.T) {
	for _, text := range []string{"10.10", "0.1", "9007199254740993", "1e3"} {
		got := scalarOf(&model.DataObjectValue{Kind: model.VarNumber, Text: text})
		b, err := json.Marshal(got)
		if err != nil {
			t.Fatalf("marshal %q: %v", text, err)
		}
		if string(b) != text {
			t.Errorf("%q marshalled back as %s; a number must keep its own text", text, b)
		}
	}
}

func TestScalarOfCarriesTheOtherKinds(t *testing.T) {
	if got := scalarOf(&model.DataObjectValue{Kind: model.VarBool, Bool: true}); got != true {
		t.Errorf("bool = %#v, want true", got)
	}
	if got := scalarOf(&model.DataObjectValue{Kind: model.VarString, Text: "MT-1998"}); got != "MT-1998" {
		t.Errorf("string = %#v, want the text", got)
	}
	// Anything else is nil rather than a guess: a JSON-kinded value is decoded by the
	// caller, and inventing a scalar for it would put the wrong shape in the graph.
	if got := scalarOf(&model.DataObjectValue{Kind: model.VarJSON, Text: `{"a":1}`}); got != nil {
		t.Errorf("json kind = %#v, want nil so the caller decodes it", got)
	}
}

// The refusal for a reading above its budget. It is asserted because the *reason* is
// the point: this endpoint reads absence as a finding, so a truncated reading would
// report everybody missing from the truncated half as having lost their access.
func TestReconcileTooManyObservationsSaysWhyItRefusesWhole(t *testing.T) {
	msg := reconcileTooManyObservations(5000, 1000)
	for _, want := range []string{"5000", "1000", "ATLAS_LIMIT_RECONCILE_OBSERVATIONS",
		"refused whole rather than truncated", "Split the run by reference"} {
		if !strings.Contains(msg, want) {
			t.Errorf("refusal does not mention %q: %s", want, msg)
		}
	}
}
