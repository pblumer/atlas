package api_test

import (
	"testing"

	"github.com/pblumer/atlas/api"
)

// TestABuiltInWorkerTypeIsFoundByTheIDEvidenceNames: conformance evidence names a
// Worker Type by its ID (ADR-0483), so build tooling looks it up by that ID and gets
// back the version and the kind the runtime knows it as. An ID Atlas does not ship
// finds nothing, and neither does a bare kind.
func TestABuiltInWorkerTypeIsFoundByTheIDEvidenceNames(t *testing.T) {
	got, ok := api.LookupBuiltInWorkerType("atlas.entra")
	if !ok {
		t.Fatal("atlas.entra is not found")
	}
	if want := (api.BuiltInWorkerType{ID: "atlas.entra", Version: "1.0.0", ConnectorKind: "entra"}); got != want {
		t.Fatalf("atlas.entra = %+v, want %+v", got, want)
	}
	for _, id := range []string{"entra", "atlas.nope", ""} {
		if got, ok := api.LookupBuiltInWorkerType(id); ok {
			t.Errorf("%q found %+v", id, got)
		}
	}
}
