package sharepointserver

import (
	"encoding/json"
	"os"
	"slices"
	"testing"

	"github.com/pblumer/atlas/compiler"
	"github.com/pblumer/atlas/connector/sharepointserver/catalog"
	"github.com/pblumer/atlas/workertype"
)

// The manifest is this Worker Type's declaration of what a second delivery of each of
// its operations does (ADR-0538). It is
// written by hand beside an operation table it must not drift from, so the two are held
// together here: every operation the Worker runs is declared, and nothing it does not
// run.
func readManifest(t *testing.T) workertype.Manifest {
	t.Helper()
	raw, err := os.ReadFile("manifest.json")
	if err != nil {
		t.Fatal(err)
	}
	m, err := workertype.ParseJSON(raw)
	if err != nil {
		t.Fatalf("manifest.json: %v", err)
	}
	return m
}

func TestTheManifestDeclaresEveryOperationTheWorkerRuns(t *testing.T) {
	m := readManifest(t)
	if !slices.Equal(m.Spec.JobTypes, []string{compiler.SharePointServerJobType}) {
		t.Errorf("jobTypes = %v, want only %s", m.Spec.JobTypes, compiler.SharePointServerJobType)
	}
	var declared []string
	for _, op := range m.Spec.Operations {
		declared = append(declared, op.ID)
		if op.DuplicateRisk() != "" {
			t.Errorf("%s is declared %s/%s; every operation of this Worker checks or is a set, so none may warn", op.ID, op.Idempotency, op.Effect)
		}
	}
	slices.Sort(declared)
	if want := catalog.Names(); !slices.Equal(declared, want) {
		t.Errorf("declared operations %v, the catalog runs %v", declared, want)
	}
}

// The configuration schema says what parseBundle accepts: the managed path and the
// timeout, and nothing else — a credential above all, which this Worker never holds.
func TestTheManifestsConfigurationIsTheBundle(t *testing.T) {
	m := readManifest(t)
	schema, err := workertype.CompileConfiguration(m.Spec.Configuration)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		bundle string
		ok     bool
	}{
		{`{}`, true},
		{`{"managedPath":"sites","timeoutSeconds":300}`, true},
		{`{"managedPath":"a/b"}`, false},
		{`{"timeoutSeconds":-1}`, false},
		{`{"password":"x"}`, false},
	} {
		_, parseErr := NewClient("http://sp01", tc.bundle, Options{})
		var config map[string]any
		if err := json.Unmarshal([]byte(tc.bundle), &config); err != nil {
			t.Fatal(err)
		}
		schemaErr := schema.Validate(config)
		if (parseErr == nil) != tc.ok || (schemaErr == nil) != tc.ok {
			t.Errorf("%s: worker says %v, schema says %v, want ok=%v", tc.bundle, parseErr, schemaErr, tc.ok)
		}
	}
}
