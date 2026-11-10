package entrachannel

import (
	"context"
	"os"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/pblumer/atlas/workertype"
	"github.com/pblumer/atlas/workertype/evidence"
)

// The drafted contract, kept where the evidence tests bind unpublished contracts.
// Publishing it moves the file unchanged to workertype/contracts/.
const draftContract = "../../testdata/contracts/collaboration-channel-create.json"

func loadContract(t *testing.T) workertype.TechnicalContract {
	t.Helper()
	raw, err := os.ReadFile(draftContract)
	if err != nil {
		t.Fatal(err)
	}
	c, err := workertype.ParseTechnicalContractJSON(raw)
	if err != nil {
		t.Fatalf("the drafted contract does not parse: %v", err)
	}
	if _, err := workertype.CompileTechnicalContract(c); err != nil {
		t.Fatalf("the drafted contract's schemas do not compile: %v", err)
	}
	return c
}

// TestTheSuiteReportsWhatTheOperationDoesToday pins every case's outcome. The two
// failures are the point of a negative case, not a defect of the suite: Entra's
// create-channel ends both refusals with a plain failure, so a process cannot tell
// "no such space" from "the name is taken". The record says so until the operation
// throws a code for each.
func TestTheSuiteReportsWhatTheOperationDoesToday(t *testing.T) {
	results, err := Suite().Run(context.Background(), loadContract(t))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	want := []struct{ name, outcome string }{
		{caseCreates, evidence.OutcomePassed},
		{caseUnknownSpaceCreatesNothing, evidence.OutcomePassed},
		{caseUnknownSpaceCode, evidence.OutcomeFailed},
		{caseRepeatCreatesNoSecond, evidence.OutcomePassed},
		{caseRepeatCode, evidence.OutcomeFailed},
		{caseRefusedSpace, evidence.OutcomePassed},
		{caseVisibleToMembers, evidence.OutcomeNotExercised},
	}
	if len(results) != len(want) {
		t.Fatalf("%d results, want %d: %+v", len(results), len(want), results)
	}
	for i, w := range want {
		r := results[i]
		if r.Case != w.name || r.Outcome != w.outcome {
			t.Errorf("case %d = %q %s (%s), want %q %s", i, r.Case, r.Outcome, r.Detail, w.name, w.outcome)
		}
		if r.Outcome != evidence.OutcomePassed && strings.TrimSpace(r.Detail) == "" {
			t.Errorf("case %q is %s without saying why", r.Case, r.Outcome)
		}
	}
}

// TestTheSuiteIsDeterministic: a record is current when deriving it again gives its
// bytes, so two runs must report exactly the same — no port, id or time in a detail.
func TestTheSuiteIsDeterministic(t *testing.T) {
	c := loadContract(t)
	a, err := Suite().Run(context.Background(), c)
	if err != nil {
		t.Fatal(err)
	}
	b, err := Suite().Run(context.Background(), c)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(a, b) {
		t.Fatalf("two runs differ:\n%+v\n%+v", a, b)
	}
}

// TestTheSuiteRunsOnlyAgainstItsContract: its cases encode one contract's meaning, so
// binding it to another contract would record results that say nothing about it.
func TestTheSuiteRunsOnlyAgainstItsContract(t *testing.T) {
	for _, mutate := range []func(*workertype.TechnicalContract){
		func(c *workertype.TechnicalContract) { c.Metadata.ID = "io.atlas.collaboration.space.create" },
		func(c *workertype.TechnicalContract) { c.Metadata.Version = "2.0.0" },
		func(c *workertype.TechnicalContract) { c.Metadata.Profile = "slack" },
	} {
		c := loadContract(t)
		mutate(&c)
		if _, err := Suite().Run(context.Background(), c); err == nil {
			t.Errorf("the suite ran against %s@%s+%s", c.Metadata.ID, c.Metadata.Version, c.Metadata.Profile)
		}
	}
}

// TestTheSuiteNamesItsOwnPackage: the record's fixture revision is the digest of
// Suite.Package, so it must be this package and not one that does not move with it.
func TestTheSuiteNamesItsOwnPackage(t *testing.T) {
	if got, want := Suite().Package, reflect.TypeOf(fakeGraph{}).PkgPath(); got != want {
		t.Fatalf("Suite.Package = %q, want %q", got, want)
	}
}
