package atlasreport

import (
	"os"
	"slices"
	"testing"

	"github.com/pblumer/atlas/compiler"
	"github.com/pblumer/atlas/connector/atlasreport/catalog"
	"github.com/pblumer/atlas/workertype"
)

// The manifest is this Worker Type's declaration of what a second delivery of each of
// its operations does (ADR-0538). It is written by hand beside an operation table it
// must not drift from, so the two are held together here: every operation the Worker
// runs is declared, as the catalog declares it, and nothing it does not run.
func TestTheManifestDeclaresEveryOperationTheWorkerRuns(t *testing.T) {
	raw, err := os.ReadFile("manifest.json")
	if err != nil {
		t.Fatal(err)
	}
	m, err := workertype.ParseJSON(raw)
	if err != nil {
		t.Fatalf("manifest.json: %v", err)
	}
	if !slices.Equal(m.Spec.JobTypes, []string{compiler.AtlasReportJobType}) {
		t.Errorf("jobTypes = %v, want only %s", m.Spec.JobTypes, compiler.AtlasReportJobType)
	}
	var declared []string
	for _, op := range m.Spec.Operations {
		declared = append(declared, op.ID)
		row, ok := catalog.Lookup(op.ID)
		if !ok {
			t.Errorf("the manifest declares %s, which the catalog does not run", op.ID)
			continue
		}
		if string(op.Idempotency) != row.Idempotency || string(op.Effect) != row.Effect {
			t.Errorf("%s is declared %s/%s, the catalog says %s/%s", op.ID, op.Idempotency, op.Effect, row.Idempotency, row.Effect)
		}
		// Every operation reads: a second delivery reads again and changes nothing, so
		// none may warn of a duplicate.
		if op.DuplicateRisk() != "" {
			t.Errorf("%s is declared %s/%s, which warns of a duplicate a read cannot cause", op.ID, op.Idempotency, op.Effect)
		}
	}
	slices.Sort(declared)
	if want := catalog.Names(); !slices.Equal(declared, want) {
		t.Errorf("declared operations %v, the catalog runs %v", declared, want)
	}
}
