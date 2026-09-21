package engine_test

import (
	"strings"
	"testing"

	"github.com/pblumer/atlas/compiler"
	"github.com/pblumer/atlas/engine"
	"github.com/pblumer/atlas/model"
)

// The single writer's fail-closed check for ADR-0314, and why it has to exist at all.
//
// Personal values are enciphered at the edges: a form submission, a worker's result, an
// operator's write. Not every path into an instance has an edge to seal at — a message
// payload correlates to its instance *here*, inside the engine, which holds no key and
// must not hold one (I4). Without a check at the write itself, that one path would put an
// un-erasable personal value in the log while the declaration said the opposite, and
// nothing about it would look wrong.
//
// So the writer refuses a declared personal value that arrives readable. It can ask the
// question without a key: an enciphered value carries a marker, and recognising one is a
// prefix comparison on bytes the engine already holds.

// personalProcess is start → user task → end, declaring vorname personal and pnr as the
// data subject. The user task parks the instance, so its start variables stay observable.
func personalProcess(t testing.TB) *compiler.CompiledProcess {
	t.Helper()
	b := compiler.NewBuilder(defKey, "bestellung", 1)
	start := b.AddStartEvent()
	task := b.AddUserTask("Freigeben", compiler.Assignment{Literal: "admin"}, compiler.Assignment{Literal: "admins"}, "", 50, 0, 3)
	end := b.AddEndEvent()
	b.Connect(start, task)
	b.Connect(task, end)
	b.SetPersonalVariables([]string{"vorname"})
	b.SetDataSubjectVariable("pnr")
	cp, err := b.Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	return cp
}

// enciphered is a value in the shape an edge produces: the envelope's marker, which is all
// the engine can and should recognise. The contents are irrelevant here — the writer never
// opens one.
func enciphered(name string) model.VariableValue {
	return model.VariableValue{
		Name: name,
		Kind: model.VarJSON,
		Text: model.EncipheredMarker + `{"subject":"P-4711","kind":3,"nonce":"AAAAAAAAAAAAAAAA","value":"AAAAAAAAAAAAAAAAAAAAAAAA"}}`,
	}
}

// TestAClearPersonalWriteIsRefusedWithAnIncident is the guard. The subject's own id is a
// reference and is written; the declared value is not.
func TestAClearPersonalWriteIsRefusedWithAnIncident(t *testing.T) {
	h := openHarness(t, t.TempDir())
	defer h.close(t)
	cp := personalProcess(t)

	p := engine.New(1, h.log, h.store, &manualClock{})
	p.Deploy(cp)
	if err := p.Recover(); err != nil {
		t.Fatalf("Recover: %v", err)
	}
	p.CreateInstance(cp.Key,
		model.VariableValue{Name: "pnr", Kind: model.VarString, Text: "P-4711"},
		model.VariableValue{Name: "vorname", Kind: model.VarString, Text: "Ida"},
	)
	if err := p.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle: %v", err)
	}

	var piKey uint64
	if err := h.store.ActiveProcessInstances(func(key uint64, _ *model.ProcessInstanceValue) error {
		piKey = key
		return nil
	}); err != nil {
		t.Fatalf("ActiveProcessInstances: %v", err)
	}
	if piKey == 0 {
		t.Fatal("no instance was created")
	}

	names := map[string]model.VariableValue{}
	if err := h.store.VariablesOfScope(piKey, func(v *model.VariableValue) error {
		names[v.Name] = *v
		return nil
	}); err != nil {
		t.Fatalf("VariablesOfScope: %v", err)
	}
	if _, ok := names["vorname"]; ok {
		t.Errorf("a personal value arrived in the clear and was written anyway: %q", names["vorname"].Text)
	}
	if got := names["pnr"]; got.Text != "P-4711" {
		t.Errorf("the data subject's id was not written: %+v — it is a reference and must stay readable", got)
	}

	var incidents []*model.IncidentValue
	if err := h.store.Incidents(func(_ uint64, v *model.IncidentValue) error {
		incidents = append(incidents, v)
		return nil
	}); err != nil {
		t.Fatalf("Incidents: %v", err)
	}
	if len(incidents) != 1 {
		t.Fatalf("raised %d incidents, want 1 — a refused write must say so, not vanish", len(incidents))
	}
	inc := incidents[0]
	if inc.Reason != model.IncidentPersonalInTheClear {
		t.Errorf("incident reason = %d, want IncidentPersonalInTheClear", inc.Reason)
	}
	for _, want := range []string{"vorname", "personal", "resolve"} {
		if !strings.Contains(inc.Message, want) {
			t.Errorf("the incident message does not mention %q: %s", want, inc.Message)
		}
	}
	if strings.Contains(inc.Message, "Ida") {
		t.Errorf("the incident message carries the value it refused: %s", inc.Message)
	}
}

// TestAnEncipheredPersonalWriteGoesThrough is the other half, and the one that would make
// the check worthless if it failed: the ordinary path — an edge sealed the value — must be
// untouched, and it must not raise anything.
func TestAnEncipheredPersonalWriteGoesThrough(t *testing.T) {
	h := openHarness(t, t.TempDir())
	defer h.close(t)
	cp := personalProcess(t)

	p := engine.New(1, h.log, h.store, &manualClock{})
	p.Deploy(cp)
	if err := p.Recover(); err != nil {
		t.Fatalf("Recover: %v", err)
	}
	sealed := enciphered("vorname")
	p.CreateInstance(cp.Key,
		model.VariableValue{Name: "pnr", Kind: model.VarString, Text: "P-4711"},
		sealed,
	)
	if err := p.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle: %v", err)
	}

	var piKey uint64
	if err := h.store.ActiveProcessInstances(func(key uint64, _ *model.ProcessInstanceValue) error {
		piKey = key
		return nil
	}); err != nil {
		t.Fatalf("ActiveProcessInstances: %v", err)
	}
	var stored model.VariableValue
	if err := h.store.VariablesOfScope(piKey, func(v *model.VariableValue) error {
		if v.Name == "vorname" {
			stored = *v
		}
		return nil
	}); err != nil {
		t.Fatalf("VariablesOfScope: %v", err)
	}
	if stored.Text != sealed.Text || stored.Kind != model.VarJSON {
		t.Errorf("the sealed value was not stored as it arrived: %+v", stored)
	}
	if n, err := h.store.IncidentCount(); err != nil || n != 0 {
		t.Errorf("IncidentCount = %d (%v), want 0: an enciphered write is the ordinary path", n, err)
	}
}

// TestAProcessDeclaringNothingIsUnaffected is the regression guard for every model that
// exists. The check is opt-in by declaration, and a process that declares nothing must
// write exactly what it always did.
func TestAProcessDeclaringNothingIsUnaffected(t *testing.T) {
	h := openHarness(t, t.TempDir())
	defer h.close(t)
	cp := searchableProcess(t, "identityId")

	p := engine.New(1, h.log, h.store, &manualClock{})
	p.Deploy(cp)
	if err := p.Recover(); err != nil {
		t.Fatalf("Recover: %v", err)
	}
	p.CreateInstance(cp.Key, model.VariableValue{Name: "vorname", Kind: model.VarString, Text: "Ida"})
	if err := p.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle: %v", err)
	}
	var piKey uint64
	if err := h.store.ActiveProcessInstances(func(key uint64, _ *model.ProcessInstanceValue) error {
		piKey = key
		return nil
	}); err != nil {
		t.Fatalf("ActiveProcessInstances: %v", err)
	}
	var found bool
	if err := h.store.VariablesOfScope(piKey, func(v *model.VariableValue) error {
		if v.Name == "vorname" && v.Text == "Ida" {
			found = true
		}
		return nil
	}); err != nil {
		t.Fatalf("VariablesOfScope: %v", err)
	}
	if !found {
		t.Error("a process that declares nothing personal no longer writes a plain value")
	}
}

// TestMovingTheDataSubjectIsRefusedOnceSomethingIsSealed closes the quietest hole the
// mechanism had.
//
// Sealing follows whatever the data-subject variable says at the moment of the write. Change
// it halfway and one person's values are split across two keys — after which erasing either
// subject leaves the other half readable, while the request looks honoured. Nothing
// downstream could notice, because every value opens perfectly well under the key it names.
func TestMovingTheDataSubjectIsRefusedOnceSomethingIsSealed(t *testing.T) {
	h := openHarness(t, t.TempDir())
	defer h.close(t)
	cp := personalProcess(t)

	p := engine.New(1, h.log, h.store, &manualClock{})
	p.Deploy(cp)
	if err := p.Recover(); err != nil {
		t.Fatalf("Recover: %v", err)
	}
	p.CreateInstance(cp.Key,
		model.VariableValue{Name: "pnr", Kind: model.VarString, Text: "P-4711"},
		enciphered("vorname"),
	)
	if err := p.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle: %v", err)
	}
	var piKey uint64
	if err := h.store.ActiveProcessInstances(func(key uint64, _ *model.ProcessInstanceValue) error {
		piKey = key
		return nil
	}); err != nil {
		t.Fatalf("ActiveProcessInstances: %v", err)
	}

	p.SetVariables(piKey, piKey, "operator",
		model.VariableValue{Name: "pnr", Kind: model.VarString, Text: "P-0815"})
	if err := p.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle: %v", err)
	}

	var subject model.VariableValue
	if err := h.store.VariablesOfScope(piKey, func(v *model.VariableValue) error {
		if v.Name == "pnr" {
			subject = *v
		}
		return nil
	}); err != nil {
		t.Fatalf("VariablesOfScope: %v", err)
	}
	if subject.Text != "P-4711" {
		t.Errorf("the data subject moved to %q; the values sealed under P-4711 would now outlive an erasure of it", subject.Text)
	}
	var reasons []model.IncidentReason
	if err := h.store.Incidents(func(_ uint64, v *model.IncidentValue) error {
		reasons = append(reasons, v.Reason)
		if v.Reason == model.IncidentDataSubjectMoved && !strings.Contains(v.Message, "two keys") {
			t.Errorf("the incident does not say what the danger is: %s", v.Message)
		}
		return nil
	}); err != nil {
		t.Fatalf("Incidents: %v", err)
	}
	if len(reasons) != 1 || reasons[0] != model.IncidentDataSubjectMoved {
		t.Errorf("incident reasons = %v, want one IncidentDataSubjectMoved", reasons)
	}
}

// TestCorrectingTheDataSubjectBeforeAnythingIsSealedIsAllowed is the other side, and it is
// why the refusal asks whether anything has actually been sealed rather than simply
// freezing the variable. A mistyped id corrected before any personal value exists costs
// nothing, and refusing it would be pedantry dressed up as safety.
func TestCorrectingTheDataSubjectBeforeAnythingIsSealedIsAllowed(t *testing.T) {
	h := openHarness(t, t.TempDir())
	defer h.close(t)
	cp := personalProcess(t)

	p := engine.New(1, h.log, h.store, &manualClock{})
	p.Deploy(cp)
	if err := p.Recover(); err != nil {
		t.Fatalf("Recover: %v", err)
	}
	p.CreateInstance(cp.Key, model.VariableValue{Name: "pnr", Kind: model.VarString, Text: "P-4711"})
	if err := p.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle: %v", err)
	}
	var piKey uint64
	if err := h.store.ActiveProcessInstances(func(key uint64, _ *model.ProcessInstanceValue) error {
		piKey = key
		return nil
	}); err != nil {
		t.Fatalf("ActiveProcessInstances: %v", err)
	}
	p.SetVariables(piKey, piKey, "operator",
		model.VariableValue{Name: "pnr", Kind: model.VarString, Text: "P-0815"})
	if err := p.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle: %v", err)
	}
	var subject model.VariableValue
	if err := h.store.VariablesOfScope(piKey, func(v *model.VariableValue) error {
		if v.Name == "pnr" {
			subject = *v
		}
		return nil
	}); err != nil {
		t.Fatalf("VariablesOfScope: %v", err)
	}
	if subject.Text != "P-0815" {
		t.Errorf("the correction was refused although nothing was sealed yet: pnr = %q", subject.Text)
	}
	if n, err := h.store.IncidentCount(); err != nil || n != 0 {
		t.Errorf("IncidentCount = %d (%v), want 0", n, err)
	}
}
