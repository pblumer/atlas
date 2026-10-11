package engine_test

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/pblumer/atlas/compiler"
	"github.com/pblumer/atlas/engine"
	"github.com/pblumer/atlas/model"
	"github.com/pblumer/atlas/state"
	"github.com/pblumer/atlas/wal"
)

// A personal value leaves a subprocess under its own name (ADR-0314, amended): the engine
// copies the sealed record it cannot open, byte for byte, instead of evaluating the
// mapping — which is the only way out of a wizard (ADR-0449) for the answers its screens
// sealed. And because a sealed value now does live outside the root, the check that keeps
// the data subject from moving looks in every scope.

const passThroughKey = 4711

// passThroughProcess is start → subprocess (two screens, iban handed out under its own
// name) → a task that parks the instance → end. iban is personal, pnr its data subject.
func passThroughProcess(t testing.TB) *compiler.CompiledProcess {
	t.Helper()
	cp, err := compiler.Parse(passThroughKey, 1, strings.NewReader(`<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL"
                    xmlns:atlas="http://atlas/schema/1.0"
                    xmlns:zeebe="http://camunda.org/schema/zeebe/1.0">
  <process id="bogen" isExecutable="true" atlas:personal="iban" atlas:dataSubject="pnr">
    <startEvent id="s"/>
    <subProcess id="sitting">
      <extensionElements><zeebe:ioMapping><zeebe:output source="=iban" target="iban"/></zeebe:ioMapping></extensionElements>
      <startEvent id="ws"/>
      <userTask id="eins"/>
      <userTask id="zwei"/>
      <endEvent id="we"/>
      <sequenceFlow id="w1" sourceRef="ws" targetRef="eins"/>
      <sequenceFlow id="w2" sourceRef="eins" targetRef="zwei"/>
      <sequenceFlow id="w3" sourceRef="zwei" targetRef="we"/>
    </subProcess>
    <userTask id="danach"/>
    <endEvent id="e"/>
    <sequenceFlow id="f1" sourceRef="s" targetRef="sitting"/>
    <sequenceFlow id="f2" sourceRef="sitting" targetRef="danach"/>
    <sequenceFlow id="f3" sourceRef="danach" targetRef="e"/>
  </process>
</definitions>`))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	return cp
}

// sealedFor is a value in the shape an edge produces for subject, with a member order and
// spacing FEEL would not reproduce — so a copy that went through an evaluation instead of
// a byte copy shows.
func sealedFor(name, subject string) model.VariableValue {
	return model.VariableValue{
		Name: name,
		Kind: model.VarJSON,
		Text: model.EncipheredMarker + `{"subject":"` + subject + `","kind":3,"nonce":"AAAAAAAAAAAAAAAA","value":"AAAAAAAAAAAAAAAAAAAAAAAA"}}`,
	}
}

// screen completes the one open user task with vars.
func screen(t *testing.T, p *engine.Processor, s *state.Store, vars ...model.VariableValue) {
	t.Helper()
	p.CompleteJob(singleActivatableJob(t, s, compiler.UserTaskJobTypeIndex), vars...)
	if err := p.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle: %v", err)
	}
}

// rootVariable reads one variable of the single active process instance's root scope.
func rootVariable(t *testing.T, s *state.Store, name string) (*model.VariableValue, uint64) {
	t.Helper()
	var piKey uint64
	for k := range activeInstances(t, s) {
		piKey = k
	}
	var got *model.VariableValue
	if err := s.VariablesOfScope(piKey, func(v *model.VariableValue) error {
		if v.Name == name {
			c := *v
			got = &c
		}
		return nil
	}); err != nil {
		t.Fatalf("VariablesOfScope: %v", err)
	}
	return got, piKey
}

func incidentReasons(t *testing.T, s *state.Store) []model.IncidentReason {
	t.Helper()
	var reasons []model.IncidentReason
	if err := s.Incidents(func(_ uint64, v *model.IncidentValue) error {
		reasons = append(reasons, v.Reason)
		return nil
	}); err != nil {
		t.Fatalf("Incidents: %v", err)
	}
	return reasons
}

func startPassThrough(t *testing.T, h *harness) (*engine.Processor, *compiler.CompiledProcess) {
	t.Helper()
	cp := passThroughProcess(t)
	p := engine.New(1, h.log, h.store, &manualClock{})
	p.Deploy(cp)
	if err := p.Recover(); err != nil {
		t.Fatalf("Recover: %v", err)
	}
	p.CreateInstance(cp.Key, model.VariableValue{Name: "pnr", Kind: model.VarString, Text: "P-4711"})
	if err := p.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle: %v", err)
	}
	return p, cp
}

// TestASealedValueLeavesTheSubprocessByteForByte: the value the screen sealed reaches the
// root unchanged — the same bytes, so it opens under the same key and dies with it — and a
// replay of the log from nothing writes it again identically (I4, I6).
func TestASealedValueLeavesTheSubprocessByteForByte(t *testing.T) {
	dir := t.TempDir()
	h := openHarness(t, dir)
	p, cp := startPassThrough(t, h)

	want := sealedFor("iban", "P-4711")
	screen(t, p, h.store, want)
	screen(t, p, h.store)

	got, _ := rootVariable(t, h.store, "iban")
	if got == nil {
		t.Fatal("the sealed value did not leave the subprocess")
	}
	if got.Kind != model.VarJSON || got.Text != want.Text {
		t.Fatalf("root iban = %v %q, want the identical envelope %q", got.Kind, got.Text, want.Text)
	}
	if r := incidentReasons(t, h.store); len(r) != 0 {
		t.Errorf("incidents = %v, want none", r)
	}
	h.close(t)

	// Replay the log into an empty store.
	log2, err := wal.Open(wal.Options{Dir: filepath.Join(dir, "wal")})
	if err != nil {
		t.Fatalf("wal.Open: %v", err)
	}
	store2, err := state.Open(filepath.Join(dir, "state2"))
	if err != nil {
		t.Fatalf("state.Open: %v", err)
	}
	defer func() { _ = store2.Close(); _ = log2.Close() }()
	p2 := engine.New(1, log2, store2, &manualClock{})
	p2.Deploy(cp)
	if err := p2.Recover(); err != nil {
		t.Fatalf("Recover: %v", err)
	}
	if again, _ := rootVariable(t, store2, "iban"); again == nil || again.Text != want.Text {
		t.Errorf("after replay root iban = %+v, want the identical envelope", again)
	}
}

// TestAScreenNobodySawHandsOutNull: a pass-through of a value no screen wrote is null, as
// an evaluated `=iban` over a missing variable would be — not an error, not the clear.
func TestAScreenNobodySawHandsOutNull(t *testing.T) {
	h := openHarness(t, t.TempDir())
	defer h.close(t)
	p, _ := startPassThrough(t, h)
	screen(t, p, h.store)
	screen(t, p, h.store)

	got, _ := rootVariable(t, h.store, "iban")
	if got == nil || got.Kind != model.VarNull {
		t.Fatalf("root iban = %+v, want null", got)
	}
	if r := incidentReasons(t, h.store); len(r) != 0 {
		t.Errorf("incidents = %v, want none", r)
	}
}

// TestASealedValueOfAnotherSubjectIsNotHandedOut: a subprocess that set its own data
// subject before anything was sealed sealed its screen's value for that subject. Copied to
// the root, whose subject is somebody else, it would still open — under the other key —
// and survive an erasure of the instance's subject. The write is refused with an incident.
func TestASealedValueOfAnotherSubjectIsNotHandedOut(t *testing.T) {
	h := openHarness(t, t.TempDir())
	defer h.close(t)
	p, _ := startPassThrough(t, h)

	screen(t, p, h.store, model.VariableValue{Name: "pnr", Kind: model.VarString, Text: "P-0815"})
	screen(t, p, h.store, sealedFor("iban", "P-0815"))

	if got, _ := rootVariable(t, h.store, "iban"); got != nil {
		t.Errorf("root iban = %q, want nothing: it is sealed for P-0815 and the instance's subject is P-4711", got.Text)
	}
	r := incidentReasons(t, h.store)
	if len(r) != 1 || r[0] != model.IncidentPersonalOfAnotherSubject {
		t.Fatalf("incident reasons = %v, want one IncidentPersonalOfAnotherSubject", r)
	}
}

// TestTheDataSubjectCannotMoveFromUnderAValueSealedInASubprocess: a screen inside a
// subprocess writes its sealed value into the subprocess's scope, not the root. The check
// that refuses moving the data subject once something is sealed used to look only at the
// root, and let this one through — splitting one person's data across two keys.
func TestTheDataSubjectCannotMoveFromUnderAValueSealedInASubprocess(t *testing.T) {
	h := openHarness(t, t.TempDir())
	defer h.close(t)
	p, _ := startPassThrough(t, h)

	screen(t, p, h.store, sealedFor("iban", "P-4711"))
	screen(t, p, h.store, model.VariableValue{Name: "pnr", Kind: model.VarString, Text: "P-0815"})

	r := incidentReasons(t, h.store)
	if len(r) != 1 || r[0] != model.IncidentDataSubjectMoved {
		t.Fatalf("incident reasons = %v, want one IncidentDataSubjectMoved", r)
	}
	if got, _ := rootVariable(t, h.store, "pnr"); got == nil || got.Text != "P-4711" {
		t.Errorf("root pnr = %+v, want P-4711 unchanged", got)
	}
}
