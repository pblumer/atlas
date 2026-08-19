package engine_test

import (
	"testing"

	"github.com/pblumer/atlas/compiler"
	"github.com/pblumer/atlas/engine"
	"github.com/pblumer/atlas/model"
	"github.com/pblumer/atlas/state"
)

// runawayFEEL is an expression that fails at *evaluation* time rather than at
// compile time. FEEL is null-propagating almost everywhere — dividing by zero,
// adding a string to a number, reading an undefined variable and calling an
// unknown function all yield null, not an error — so the only way to make Eval
// return an error is to exhaust one of its budgets. This one exceeds the
// 10,000,000-iteration limit.
//
// Spending that budget costs about a second, and the count is per evaluation, so
// there is no way to share the cost between the cases below. That is why they sit
// in one table: the rule is one rule, and this keeps its price to one second per
// place that repeats it rather than one per test file someone might add later.
const runawayFEEL = `every i in 1..20000000 satisfies true`

// TestFeelEvaluationFailureWritesNull pins what the engine does when a FEEL
// evaluation fails at runtime, at each place it evaluates one.
//
// The rule is that the failure writes null and the token moves on. That is a
// deliberate choice, not an oversight: incidents are not modeled for expression
// failures, so the alternative to null would be halting the processor — and the
// processor is single-writer, so halting it over one instance's bad expression
// would stop every other instance in its partition too. A runaway loop in one
// model's output mapping must not be able to take the partition down with it.
//
// Six places evaluate FEEL and each restates the rule in its own three lines, so
// each is checked here. If one of them ever starts returning early or propagating
// the error instead, this is the test that says so.
func TestFeelEvaluationFailureWritesNull(t *testing.T) {
	// wantNullVar asserts a variable exists in the instance scope and is null.
	wantNullVar := func(name string) func(*testing.T, *state.Store, uint64) {
		return func(t *testing.T, s *state.Store, instance uint64) {
			t.Helper()
			v := readVar(t, s, instance, name)
			if v == nil {
				t.Fatalf("%s was never written; a failed evaluation must still write null", name)
			}
			if v.Kind != model.VarNull {
				t.Errorf("%s = kind %v text %q, want null", name, v.Kind, v.Text)
			}
		}
	}

	tests := []struct {
		name string
		// build wires a process whose run evaluates runawayFEEL at the site under test.
		build func(t *testing.T, b *compiler.Builder)
		check func(t *testing.T, s *state.Store, instance uint64)
		// parked marks a case that is still waiting on a timer when the run goes
		// idle, so the instance has not completed yet.
		parked bool
	}{
		{
			name: "inline script task",
			build: func(t *testing.T, b *compiler.Builder) {
				s, task, e := b.AddStartEvent(), b.AddScriptTask(mustCompile(t, runawayFEEL), "result"), b.AddEndEvent()
				b.Connect(s, task)
				b.Connect(task, e)
			},
			check: wantNullVar("result"),
		},
		{
			name: "io mapping",
			build: func(t *testing.T, b *compiler.Builder) {
				s, task, e := b.AddStartEvent(), b.AddScriptTask(mustCompile(t, `1`), "ok"), b.AddEndEvent()
				b.AddOutputMapping(task, "mapped", mustCompile(t, runawayFEEL))
				b.Connect(s, task)
				b.Connect(task, e)
			},
			check: wantNullVar("mapped"),
		},
		{
			name: "data input association",
			build: func(t *testing.T, b *compiler.Builder) {
				s, task, e := b.AddStartEvent(), b.AddScriptTask(mustCompile(t, `1`), "ok"), b.AddEndEvent()
				b.AddDataObject("order", "OrderType", "received", false)
				b.AddDataInputAssociation(task, "order", "fromObject", mustCompile(t, runawayFEEL))
				b.Connect(s, task)
				b.Connect(task, e)
			},
			check: wantNullVar("fromObject"),
		},
		{
			name: "data output association",
			build: func(t *testing.T, b *compiler.Builder) {
				s, task, e := b.AddStartEvent(), b.AddScriptTask(mustCompile(t, `1`), "ok"), b.AddEndEvent()
				b.AddDataObject("order", "OrderType", "received", false)
				b.AddDataOutputAssociation(task, "order", mustCompile(t, runawayFEEL), "", "")
				b.Connect(s, task)
				b.Connect(task, e)
			},
			check: func(t *testing.T, s *state.Store, instance uint64) {
				t.Helper()
				obj := readDataObject(t, s, instance, "order")
				if obj == nil {
					t.Fatal("the data object was never written")
				}
				if obj.Kind != model.VarNull {
					t.Errorf("order = kind %v text %q, want null", obj.Kind, obj.Text)
				}
			},
		},
		{
			name: "multi-instance output element",
			build: func(t *testing.T, b *compiler.Builder) {
				s, task, e := b.AddStartEvent(), b.AddScriptTask(mustCompile(t, `1`), "ok"), b.AddEndEvent()
				// One iteration, so the budget is spent once rather than per element.
				b.SetMultiInstance(task, true /*sequential*/, "item", "results",
					mustCompile(t, `[1]`), nil, mustCompile(t, runawayFEEL), nil)
				b.Connect(s, task)
				b.Connect(task, e)
			},
			check: func(t *testing.T, s *state.Store, instance uint64) {
				t.Helper()
				v := readVar(t, s, instance, "results")
				if v == nil {
					t.Fatal("the output collection was never written")
				}
				if v.Text != "[null]" {
					t.Errorf("results = %q, want [null] — the failed iteration still contributes an element", v.Text)
				}
			},
		},
		{
			name: "mockup task",
			build: func(t *testing.T, b *compiler.Builder) {
				s, e := b.AddStartEvent(), b.AddEndEvent()
				// The result is written on activation, before the simulated duration's
				// timer is armed, so it is readable while the token is still parked.
				task := b.AddMockupTask(compiler.MockupConfig{
					MinNanos: 10e9, MaxNanos: 10e9,
					ResultVar: "simulated", Expr: mustCompile(t, runawayFEEL),
				})
				b.Connect(s, task)
				b.Connect(task, e)
			},
			check:  wantNullVar("simulated"),
			parked: true,
		},
	}

	for i, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			h := openHarness(t, t.TempDir())
			defer h.close(t)

			b := compiler.NewBuilder(uint64(100+i), "feel-failure", 1)
			tc.build(t, b)
			cp, err := b.Build()
			if err != nil {
				t.Fatalf("Build: %v", err)
			}

			p := engine.New(1, h.log, h.store, &fixedClock{t: 5_000})
			p.Deploy(cp)
			if err := p.Recover(); err != nil {
				t.Fatalf("Recover: %v", err)
			}
			p.CreateInstance(cp.Key)
			if err := p.RunUntilIdle(); err != nil {
				t.Fatalf("RunUntilIdle: %v", err)
			}

			instance := model.NewKey(1, 1)
			tc.check(t, h.store, instance)

			// And the token carried on rather than parking at the failure.
			pi := activeInstance(t, h.store, instance)
			if pi == nil {
				t.Fatal("no instance record")
			}
			want := model.PICompleted
			if tc.parked {
				want = model.PIActive
			}
			if pi.State != want {
				t.Errorf("instance state = %v, want %v", pi.State, want)
			}
		})
	}
}
