package dmn_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/pblumer/atlas/compiler"
	"github.com/pblumer/atlas/dmn"
	"github.com/pblumer/atlas/engine"
	"github.com/pblumer/atlas/expr"
	"github.com/pblumer/atlas/job"
	"github.com/pblumer/atlas/model"
	"github.com/pblumer/atlas/state"
	"github.com/pblumer/atlas/wal"
)

func mustCompileFEEL(t *testing.T, src string) *expr.Compiled {
	t.Helper()
	c, err := expr.CompileAuto(src)
	if err != nil {
		t.Fatalf("compile %q: %v", src, err)
	}
	return c
}

func rootVar(t *testing.T, s *state.Store, scope uint64, name string) *model.VariableValue {
	t.Helper()
	var out *model.VariableValue
	if err := s.VariablesOfScope(scope, func(v *model.VariableValue) error {
		if v.Name == name {
			c := *v
			out = &c
		}
		return nil
	}); err != nil {
		t.Fatalf("VariablesOfScope: %v", err)
	}
	return out
}

// TestBusinessRuleTaskReadsMultiInstanceScope proves the scope-chain read added to
// the DMN worker (ADR-0068 follow-up, ADR-0084): a business rule task nested inside
// a multi-instance subprocess resolves its input mappings against the per-iteration
// `inputElement` — a variable that lives in an enclosing scope, not the process
// root. Before the scope-chain read, `row` resolved to null and every decision input
// was null. This is the exact composition CSV batch validation relies on: one DMN
// evaluation per row, collected in order into `verdicts`.
func TestBusinessRuleTaskReadsMultiInstanceScope(t *testing.T) {
	dir := t.TempDir()
	log, err := wal.Open(wal.Options{Dir: filepath.Join(dir, "wal")})
	if err != nil {
		t.Fatalf("wal.Open: %v", err)
	}
	store, err := state.Open(filepath.Join(dir, "state"))
	if err != nil {
		t.Fatalf("state.Open: %v", err)
	}
	t.Cleanup(func() { store.Close(); log.Close() })

	// A multi-instance subprocess over `rows`, each iteration validating its `row`
	// via the RowValid decision and writing a `verdict`, collected into `verdicts`.
	b := compiler.NewBuilder(dishDefKey, "validate-batch", 1)
	start := b.AddStartEvent()
	sub := b.AddSubProcess()
	b.SetMultiInstance(sub, false, "row", "verdicts",
		mustCompileFEEL(t, "rows"), nil, mustCompileFEEL(t, "verdict"), nil)
	b.PushScope(sub)
	istart := b.AddStartEvent()
	rule, err := b.AddBusinessRuleTaskMapped("RowValid", "verdict", nil, []compiler.DecisionInputMapping{
		{Target: "email", Source: mustCompileFEEL(t, "row.email")},
		{Target: "group", Source: mustCompileFEEL(t, "row.group")},
		{Target: "license", Source: mustCompileFEEL(t, "row.license")},
	}, 3, compiler.BindingLatest)
	if err != nil {
		t.Fatalf("AddBusinessRuleTaskMapped: %v", err)
	}
	iend := b.AddEndEvent()
	b.Connect(istart, rule)
	b.Connect(rule, iend)
	b.PopScope()
	end := b.AddEndEvent()
	b.Connect(start, sub)
	b.Connect(sub, end)
	cp, err := b.Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	jobType := cp.BusinessRuleTask(cp.Node(rule).Detail).JobType

	// The decision is the shipped example, so a drift there breaks this test too.
	model2, err := os.ReadFile(filepath.Join("..", "examples", "pruefe-datensaetze.dmn"))
	if err != nil {
		t.Fatalf("read example DMN: %v", err)
	}
	reg := dmn.NewRegistry()
	if err := reg.Deploy(cp.Key, model2); err != nil {
		t.Fatalf("Registry.Deploy: %v", err)
	}

	p := engine.New(1, log, store, &fixedClock{})
	p.Deploy(cp)
	if err := p.Recover(); err != nil {
		t.Fatalf("Recover: %v", err)
	}
	runner := job.NewRunner(store, p)
	runner.HandleCompleting(jobType, dmn.Handler(store, func(uint64) *compiler.CompiledProcess { return cp }, reg, nil))

	rows := `[{"email":"ada@x.io","group":"users","license":"PRO"},{"email":"bob","group":"ops","license":"NONE"}]`
	p.CreateInstance(cp.Key, model.VariableValue{Name: "rows", Kind: model.VarJSON, Text: rows})
	if err := runner.Drive(); err != nil {
		t.Fatalf("Drive: %v", err)
	}

	got := rootVar(t, store, model.NewKey(1, 1), "verdicts")
	if got == nil {
		t.Fatal("verdicts variable not found at the process root")
	}
	want := `[{"emailOk":true,"groupOk":true,"licenseOk":true,"valid":true},` +
		`{"emailOk":false,"groupOk":false,"licenseOk":false,"valid":false}]`
	if got.Text != want {
		t.Fatalf("verdicts =\n  %s\nwant\n  %s", got.Text, want)
	}
}
