package engine_test

import (
	"path/filepath"
	"reflect"
	"testing"

	"github.com/pblumer/atlas/engine"
	"github.com/pblumer/atlas/model"
	"github.com/pblumer/atlas/state"
	"github.com/pblumer/atlas/wal"
)

// auditRow is a flattened variable-audit record for assertions: who set which
// variable, on which scope, to what value.
type auditRow struct {
	actor string
	scope uint64
	name  string
	text  string
}

// variableAudit reads one instance's external-override audit trail into ordered
// rows, oldest first (ADR-0097).
func variableAudit(t *testing.T, s *state.Store, piKey uint64) []auditRow {
	t.Helper()
	var out []auditRow
	if err := s.VariableAuditHistory(piKey, func(_ int64, _ uint64, v *model.VariableAuditValue) error {
		out = append(out, auditRow{actor: v.Actor, scope: v.ScopeKey, name: v.Name, text: v.Text})
		return nil
	}); err != nil {
		t.Fatalf("VariableAuditHistory: %v", err)
	}
	return out
}

// TestSetVariablesRecordsAudit covers the "who changed it" trail (ADR-0097): an
// operator override records one audit row per variable set, naming the actor, the
// scope, and the new value, in change order — alongside the variable write itself.
func TestSetVariablesRecordsAudit(t *testing.T) {
	h := openHarness(t, t.TempDir())
	defer h.close(t)
	cp, _ := linearProcess(t) // parks at a service task, so the instance stays live

	p := engine.New(1, h.log, h.store, &manualClock{})
	p.Deploy(cp)
	if err := p.Recover(); err != nil {
		t.Fatalf("Recover: %v", err)
	}
	p.CreateInstance(cp.Key, model.VariableValue{Name: "amount", Kind: model.VarNumber, Text: "100"})
	if err := p.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle: %v", err)
	}
	pi := model.NewKey(1, 1)

	p.SetVariables(pi, 0, "patrick",
		model.VariableValue{Name: "amount", Kind: model.VarNumber, Text: "200"},
		model.VariableValue{Name: "reviewer", Kind: model.VarString, Text: "sam"},
	)
	if err := p.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle (after set): %v", err)
	}

	// Two overrides, both attributed to patrick, in the order they were set. The
	// seeded amount=100 (a model write) is NOT in the audit trail — only external
	// overrides are.
	want := []auditRow{
		{actor: "patrick", scope: pi, name: "amount", text: "200"},
		{actor: "patrick", scope: pi, name: "reviewer", text: "sam"},
	}
	if got := variableAudit(t, h.store, pi); !reflect.DeepEqual(got, want) {
		t.Fatalf("audit trail = %+v, want %+v", got, want)
	}
}

// TestSetVariablesAuditRecovers is the recovery property for the audit trail
// (invariants I4/I6, ADR-0097): the override is recorded as an ordinary event, so
// replaying the log into a fresh store rebuilds an identical audit trail — the actor
// is a durable fact read back from the log, never re-derived by re-running the modify
// command (commands are not replayed).
func TestSetVariablesAuditRecovers(t *testing.T) {
	dir := t.TempDir()
	cp, _ := linearProcess(t)
	clock := &manualClock{}

	h1 := openHarness(t, dir)
	p1 := engine.New(1, h1.log, h1.store, clock)
	p1.Deploy(cp)
	if err := p1.Recover(); err != nil {
		t.Fatalf("Recover 1: %v", err)
	}
	p1.CreateInstance(cp.Key)
	if err := p1.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle 1: %v", err)
	}
	pi := model.NewKey(1, 1)
	p1.SetVariables(pi, 0, "patrick", model.VariableValue{Name: "amount", Kind: model.VarNumber, Text: "200"})
	if err := p1.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle (set): %v", err)
	}
	live := variableAudit(t, h1.store, pi)
	if len(live) != 1 || live[0].actor != "patrick" || live[0].name != "amount" || live[0].text != "200" {
		t.Fatalf("live audit = %+v, want one patrick/amount/200 row", live)
	}
	h1.close(t)

	// Replay the same log into a fresh, empty store.
	log2, err := wal.Open(wal.Options{Dir: filepath.Join(dir, "wal")})
	if err != nil {
		t.Fatalf("wal.Open 2: %v", err)
	}
	store2, err := state.Open(filepath.Join(dir, "state2"))
	if err != nil {
		t.Fatalf("state.Open 2: %v", err)
	}
	defer func() {
		if err := store2.Close(); err != nil {
			t.Errorf("store2.Close: %v", err)
		}
		if err := log2.Close(); err != nil {
			t.Errorf("log2.Close: %v", err)
		}
	}()
	p2 := engine.New(1, log2, store2, clock)
	p2.Deploy(cp)
	if err := p2.Recover(); err != nil {
		t.Fatalf("Recover 2 (replay): %v", err)
	}
	if replayed := variableAudit(t, store2, pi); !reflect.DeepEqual(replayed, live) {
		t.Fatalf("replayed audit = %+v, want %+v", replayed, live)
	}
}
