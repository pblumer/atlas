package evidence_test

import (
	"bytes"
	"context"
	"testing"

	"github.com/pblumer/atlas/workertype/evidence"
	"github.com/pblumer/atlas/workertype/evidence/suites/entrachannel"
)

// TestTheDraftedChannelContractYieldsACurrentRecord derives the record that would bind
// Entra's create-channel to the drafted contract io.atlas.collaboration.channel.create,
// exactly as publishing it will, and verifies it against the module. Nothing is written
// to workertype/evidence/records: the contract is unpublished until the trust boundary
// of ADR-0483 — branch protection with the code owner's review — is in force.
// TestMain points the record at the contracts kept under testdata.
func TestTheDraftedChannelContractYieldsACurrentRecord(t *testing.T) {
	ctx := context.Background()
	rec, err := evidence.Create(ctx, "../..", evidence.Binding{
		Contract:   "workertype/evidence/testdata/contracts/collaboration-channel-create.json",
		WorkerType: "atlas.entra",
		Operation:  "create-channel",
		Suite:      entrachannel.Name,
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if rec.Contract.ID != "io.atlas.collaboration.channel.create" || rec.Contract.Version != "1.0.0" || rec.Contract.Profile != "microsoft-teams" {
		t.Fatalf("record binds %s@%s+%s", rec.Contract.ID, rec.Contract.Version, rec.Contract.Profile)
	}
	if rec.Operation.WorkerType != "atlas.entra" || rec.Operation.ID != "create-channel" || rec.Operation.JobType == "" {
		t.Fatalf("record binds operation %+v", rec.Operation)
	}
	counts := map[string]int{}
	for _, r := range rec.Conformance.Results {
		counts[r.Outcome]++
	}
	if counts[evidence.OutcomePassed] != 4 || counts[evidence.OutcomeFailed] != 2 || counts[evidence.OutcomeNotExercised] != 1 {
		t.Fatalf("outcomes %v, want 4 passed, 2 failed, 1 not exercised", counts)
	}
	raw, err := rec.Render()
	if err != nil {
		t.Fatal(err)
	}
	if err := evidence.Verify(ctx, "../..", raw); err != nil {
		t.Fatalf("the derived record is not current: %v", err)
	}
	again, err := evidence.Create(ctx, "../..", evidence.Binding{
		Contract: rec.Contract.Path, WorkerType: rec.Operation.WorkerType, Operation: rec.Operation.ID, Suite: rec.Conformance.Suite,
	})
	if err != nil {
		t.Fatal(err)
	}
	raw2, _ := again.Render()
	if !bytes.Equal(raw, raw2) {
		t.Fatal("deriving the record twice gives different bytes")
	}
}
