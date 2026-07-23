package compiler_test

import (
	"testing"

	"github.com/pblumer/atlas/compiler"
)

// TestDMNJobTypeReservedIndex proves the DMN job type is pinned to
// DMNJobTypeIndex (0) in every process, while a service task's type interns to a
// different, higher index — so a global DMN worker registered for index 0 never
// catches a service-task job.
func TestDMNJobTypeReservedIndex(t *testing.T) {
	b := compiler.NewBuilder(7, "p", 1)
	start := b.AddStartEvent()
	svc := b.AddServiceTask("payment", 3)
	rule, err := b.AddBusinessRuleTask("Dish", map[string]any{"Season": "Winter"}, 3)
	if err != nil {
		t.Fatalf("AddBusinessRuleTask: %v", err)
	}
	end := b.AddEndEvent()
	b.Connect(start, svc)
	b.Connect(svc, rule)
	b.Connect(rule, end)
	cp, err := b.Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	brt := cp.BusinessRuleTask(cp.Node(rule).Detail)
	if brt.JobType != compiler.DMNJobTypeIndex {
		t.Errorf("business rule job type = %d, want DMNJobTypeIndex %d", brt.JobType, compiler.DMNJobTypeIndex)
	}
	if compiler.DMNJobTypeIndex != 0 {
		t.Errorf("DMNJobTypeIndex = %d, want 0", compiler.DMNJobTypeIndex)
	}
	if st := cp.ServiceTask(cp.Node(svc).Detail); st.JobType == compiler.DMNJobTypeIndex {
		t.Errorf("service task job type collides with the DMN index %d", st.JobType)
	}
	if cp.Intern(compiler.DMNJobTypeIndex) != compiler.DMNJobType {
		t.Errorf("index %d interns to %q, want %q", compiler.DMNJobTypeIndex, cp.Intern(compiler.DMNJobTypeIndex), compiler.DMNJobType)
	}

	decisions := cp.BusinessRuleDecisions()
	if len(decisions) != 1 || decisions[0] != "Dish" {
		t.Fatalf("BusinessRuleDecisions = %v, want [Dish]", decisions)
	}
}

// TestBusinessRuleDecisionsEmpty proves a process without business rule tasks
// reports no decisions (and still reserves the DMN index).
func TestBusinessRuleDecisionsEmpty(t *testing.T) {
	b := compiler.NewBuilder(1, "p", 1)
	start := b.AddStartEvent()
	end := b.AddEndEvent()
	b.Connect(start, end)
	cp, err := b.Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if d := cp.BusinessRuleDecisions(); len(d) != 0 {
		t.Fatalf("BusinessRuleDecisions = %v, want empty", d)
	}
	if cp.Intern(compiler.DMNJobTypeIndex) != compiler.DMNJobType {
		t.Errorf("DMN index not reserved in a BRT-free process")
	}
}
