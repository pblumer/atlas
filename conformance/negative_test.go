package conformance

import (
	"bytes"
	"testing"

	"github.com/pblumer/atlas/compiler"
)

// TestNegativeModels asserts the adversarial half of the collection: every
// registered negative model is well-formed XML but structurally invalid, so the
// compiler must reject it. "Garbage refused at deploy" is as much a correctness
// property as "valid model runs".
func TestNegativeModels(t *testing.T) {
	for _, nm := range NegativeModels {
		nm := nm
		t.Run(nm.Name, func(t *testing.T) {
			xml, err := nm.load()
			if err != nil {
				t.Fatalf("load: %v", err)
			}
			if _, err := compiler.Parse(defKey, 1, bytes.NewReader(xml)); err == nil {
				t.Errorf("model compiled but must be rejected: %s", nm.Reason)
			}
		})
	}
}
