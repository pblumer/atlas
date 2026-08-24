package api

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/pblumer/atlas/compiler"
	"github.com/pblumer/atlas/expr"
)

// schemaKeys is the field keys a derived schema binds, in the order it renders them.
func schemaKeys(t *testing.T, schema any) []string {
	t.Helper()
	raw, err := json.Marshal(schema)
	if err != nil {
		t.Fatalf("marshal schema: %v", err)
	}
	var doc struct {
		Components []struct {
			Type string `json:"type"`
			Key  string `json:"key"`
		} `json:"components"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("decode schema: %v", err)
	}
	var keys []string
	for _, c := range doc.Components {
		if c.Key != "" {
			keys = append(keys, c.Key)
		}
	}
	return keys
}

// taskWithInputs builds a one-task process whose input mappings read the given FEEL
// sources, so the derivation has something real to walk.
func taskWithInputs(t *testing.T, sources ...string) (*compiler.CompiledProcess, int32) {
	t.Helper()
	b := compiler.NewBuilder(1, "p", 1)
	task := b.AddServiceTask("work", 3)
	for i, src := range sources {
		e, err := expr.CompileAuto(src)
		if err != nil {
			t.Fatalf("compile %q: %v", src, err)
		}
		b.AddInputMapping(task, "target"+string(rune('a'+i)), e)
	}
	cp, err := b.Build()
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	return cp, task
}

// TestDerivedRepairSchemaNamesTheVariablesTheTaskReads is the derivation's whole claim: a
// task's input mappings say which process variables it was handed, and those are the
// values a retry depends on. Deriving them is what gives an operator named fields with
// nobody having authored anything.
func TestDerivedRepairSchemaNamesTheVariablesTheTaskReads(t *testing.T) {
	cp, task := taskWithInputs(t, "recipient", "order.total")
	got := schemaKeys(t, derivedRepairSchema(cp, task))
	// Sorted, so the form is stable across deploys rather than following whatever order
	// the mappings happen to be in. "order.total" reads the variable "order".
	want := []string{"order", "recipient"}
	if len(got) != len(want) {
		t.Fatalf("derived keys = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("derived key %d = %q, want %q", i, got[i], want[i])
		}
	}
}

// TestDerivedRepairSchemaDeduplicates: two mappings reading the same variable must not
// produce the same field twice — a form with two "recipient" boxes writes whichever the
// renderer happens to submit last.
func TestDerivedRepairSchemaDeduplicates(t *testing.T) {
	cp, task := taskWithInputs(t, "recipient", "recipient")
	if got := schemaKeys(t, derivedRepairSchema(cp, task)); len(got) != 1 || got[0] != "recipient" {
		t.Errorf("derived keys = %v, want exactly [recipient]", got)
	}
}

// TestDerivedRepairSchemaIsNothingWithoutInputs keeps the derivation from inventing a
// form it cannot fill: a task with no input mappings, or whose mappings reference no
// variable at all, has nothing to derive from and must say so rather than render empty.
func TestDerivedRepairSchemaIsNothingWithoutInputs(t *testing.T) {
	b := compiler.NewBuilder(1, "p", 1)
	task := b.AddServiceTask("work", 3)
	cp, err := b.Build()
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	if got := derivedRepairSchema(cp, task); got != nil {
		t.Errorf("a task with no input mappings derived %v, want nothing", got)
	}
	// A constant source reads no variable, so there is still nothing an operator could
	// usefully correct.
	cpConst, constTask := taskWithInputs(t, `"a literal"`)
	if got := derivedRepairSchema(cpConst, constTask); got != nil {
		t.Errorf("a constant-only mapping derived %v, want nothing", got)
	}
	if got := derivedRepairSchema(nil, 0); got != nil {
		t.Errorf("a nil process derived %v, want nothing", got)
	}
}

// TestResolveRepairFormPrefersTheMostSpecificSource is the precedence, which is the whole
// design. Each source below knows strictly less than the one above it, so a general
// answer must never shadow a specific one — otherwise authoring a form for one
// troublesome task would not be enough to make it win.
func TestResolveRepairFormPrefersTheMostSpecificSource(t *testing.T) {
	// A mail connector task that also has an input mapping and a bound form: all three
	// sources apply at once, which is exactly when the order matters.
	build := func(withTaskForm bool) (*compiler.CompiledProcess, int32) {
		t.Helper()
		b := compiler.NewBuilder(1, "p", 1)
		e, err := expr.CompileAuto("recipient")
		if err != nil {
			t.Fatalf("compile: %v", err)
		}
		task := b.AddMailConnectorTask(compiler.MailConfig{Connector: "Postbox", To: compiler.RestExpr{Expr: e}, Retries: 3})
		b.AddInputMapping(task, "to", e)
		if withTaskForm {
			b.SetRepairForm(task, "authored-for-this-task")
		}
		cp, err := b.Build()
		if err != nil {
			t.Fatalf("build: %v", err)
		}
		return cp, task
	}
	byKind := map[string]string{"mail": "the-mail-form"}

	// 1. The task's own binding wins over both.
	cp, task := build(true)
	source, id, derived, ok := resolveRepairForm(cp, task, byKind)
	if !ok || source != repairSourceTask || id != "authored-for-this-task" || derived != nil {
		t.Errorf("with a task binding: source=%q id=%q derived=%v ok=%v, want the task's own form",
			source, id, derived, ok)
	}

	// 2. Without one, the connector kind's form wins over the derivation.
	cp, task = build(false)
	source, id, derived, ok = resolveRepairForm(cp, task, byKind)
	if !ok || source != repairSourceConnector || id != "the-mail-form" || derived != nil {
		t.Errorf("without a task binding: source=%q id=%q derived=%v ok=%v, want the connector kind's form",
			source, id, derived, ok)
	}

	// 3. With neither, the derivation answers — no authoring at all.
	source, id, derived, ok = resolveRepairForm(cp, task, nil)
	if !ok || source != repairSourceDerived || id != "" || derived == nil {
		t.Errorf("with no binding at all: source=%q id=%q derived=%v ok=%v, want a derived form",
			source, id, derived, ok)
	}
	if keys := schemaKeys(t, derived); len(keys) != 1 || keys[0] != "recipient" {
		t.Errorf("derived keys = %v, want [recipient]", keys)
	}
}

// TestResolveRepairFormAnswersNothingWhenNothingApplies: a plain job-worker task with no
// mappings and no binding has no form, and saying so is a complete answer — the raw
// editor is the way, exactly as before any of this existed.
func TestResolveRepairFormAnswersNothingWhenNothingApplies(t *testing.T) {
	b := compiler.NewBuilder(1, "p", 1)
	task := b.AddServiceTask("work", 3)
	cp, err := b.Build()
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	if _, _, _, ok := resolveRepairForm(cp, task, map[string]string{"mail": "x"}); ok {
		t.Error("a plain task with no mappings resolved a repair form")
	}
	if _, _, _, ok := resolveRepairForm(nil, 0, nil); ok {
		t.Error("a nil process resolved a repair form")
	}
}

// TestResolveRepairFormIgnoresAnUnboundKind: a connector task whose kind nobody
// configured falls through to the derivation rather than reporting a binding to nothing.
func TestResolveRepairFormIgnoresAnUnboundKind(t *testing.T) {
	b := compiler.NewBuilder(1, "p", 1)
	e, err := expr.CompileAuto("recipient")
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	task := b.AddMailConnectorTask(compiler.MailConfig{Connector: "Postbox", To: compiler.RestExpr{Expr: e}, Retries: 3})
	b.AddInputMapping(task, "to", e)
	cp, err := b.Build()
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	// Another kind is configured, but not this one.
	source, _, _, ok := resolveRepairForm(cp, task, map[string]string{"rest": "some-form"})
	if !ok || source != repairSourceDerived {
		t.Errorf("source = %q ok = %v, want the derivation when this kind has no binding", source, ok)
	}
}

// TestGetRepairFormsAlwaysReturnsAMap: the store promises a non-nil map so every caller
// can index it without a guard — including the incident listings, which read it per row.
// A settings file holding `{}` (or a `byKind` explicitly null) is the case that would
// otherwise hand back nil and turn a listing into a panic.
func TestGetRepairFormsAlwaysReturnsAMap(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "settings")
	st, err := newSettingsStore(dir)
	if err != nil {
		t.Fatalf("newSettingsStore: %v", err)
	}
	// Never written: the state every installation starts in.
	got, err := st.getRepairForms()
	if err != nil || got == nil || len(got) != 0 {
		t.Fatalf("unwritten: got %v err %v, want an empty non-nil map", got, err)
	}
	if err := os.WriteFile(st.rfFile, []byte(`{"byKind":null}`), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	got, err = st.getRepairForms()
	if err != nil || got == nil || len(got) != 0 {
		t.Fatalf("null byKind: got %v err %v, want an empty non-nil map", got, err)
	}
}

// TestGetRepairFormsRejectsACorruptFile: a settings file someone edited by hand into
// invalid JSON is reported, not silently read as "no kind has a form". Swallowing it
// would drop every binding an org made and give no sign of why.
func TestGetRepairFormsRejectsACorruptFile(t *testing.T) {
	st, err := newSettingsStore(filepath.Join(t.TempDir(), "settings"))
	if err != nil {
		t.Fatalf("newSettingsStore: %v", err)
	}
	if err := os.WriteFile(st.rfFile, []byte("{not json"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	if _, err := st.getRepairForms(); err == nil {
		t.Fatal("a corrupt repair-forms file read as an empty binding set")
	}
}
