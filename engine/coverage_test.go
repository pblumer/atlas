package engine

import (
	"errors"
	"path/filepath"
	"testing"

	"github.com/pblumer/atlas/expr"
	"github.com/pblumer/atlas/model"
	"github.com/pblumer/atlas/state"
	"github.com/pblumer/atlas/wal"
)

// wbClock is a deterministic clock for white-box tests.
type wbClock struct{ t int64 }

func (c *wbClock) Now() int64 { c.t++; return c.t }

// openStore opens a fresh state store in a temp dir.
func openStore(t *testing.T) *state.Store {
	t.Helper()
	s, err := state.Open(filepath.Join(t.TempDir(), "state"))
	if err != nil {
		t.Fatalf("state.Open: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

// TestSystemClockNow exercises the host-clock reader.
func TestSystemClockNow(t *testing.T) {
	if got := (SystemClock{}).Now(); got <= 0 {
		t.Errorf("SystemClock.Now() = %d, want > 0", got)
	}
}

// TestProcessingContextNow checks the context reads time through the injected
// clock (invariant I4: time is captured in the processor, not applyToState).
func TestProcessingContextNow(t *testing.T) {
	p := &Processor{clock: &wbClock{t: 41}}
	c := &ProcessingContext{p: p}
	if got := c.Now(); got != 42 {
		t.Errorf("ProcessingContext.Now() = %d, want 42", got)
	}
}

// TestNewDefaultsToSystemClock: a nil clock falls back to the system clock.
func TestNewDefaultsToSystemClock(t *testing.T) {
	p := New(1, nil, nil, nil)
	if _, ok := p.clock.(SystemClock); !ok {
		t.Errorf("New with nil clock: clock = %T, want SystemClock", p.clock)
	}
}

// TestToVarKind maps every expr scalar kind (and the null default).
func TestToVarKind(t *testing.T) {
	tests := []struct {
		in   expr.ValueKind
		want model.VarKind
	}{
		{expr.KindBool, model.VarBool},
		{expr.KindNumber, model.VarNumber},
		{expr.KindString, model.VarString},
		{expr.KindNull, model.VarNull},
		{expr.ValueKind(200), model.VarNull}, // unknown → null default
	}
	for _, tt := range tests {
		if got := toVarKind(tt.in); got != tt.want {
			t.Errorf("toVarKind(%v) = %v, want %v", tt.in, got, tt.want)
		}
	}
}

// TestToExprKind is the inverse mapping (and the null default).
func TestToExprKind(t *testing.T) {
	tests := []struct {
		in   model.VarKind
		want expr.ValueKind
	}{
		{model.VarBool, expr.KindBool},
		{model.VarNumber, expr.KindNumber},
		{model.VarString, expr.KindString},
		{model.VarNull, expr.KindNull},
		{model.VarKind(200), expr.KindNull}, // unknown → null default
	}
	for _, tt := range tests {
		if got := toExprKind(tt.in); got != tt.want {
			t.Errorf("toExprKind(%v) = %v, want %v", tt.in, got, tt.want)
		}
	}
}

// TestAsValue returns a pointer to the active field for each value type, and nil
// for an unknown type. It must not box (see alloc_test.go); here we check the
// pointer identity/emptiness only.
func TestAsValue(t *testing.T) {
	iv := &inflightValue{
		process:  model.ProcessInstanceValue{ProcessDefKey: 9},
		element:  model.ElementInstanceValue{ElementId: 3},
		job:      model.JobValue{JobType: 7},
		variable: model.VariableValue{Name: "x"},
	}
	if v := iv.asValue(model.VTProcessInstance); v != &iv.process {
		t.Error("asValue(VTProcessInstance) should point at process field")
	}
	if v := iv.asValue(model.VTElementInstance); v != &iv.element {
		t.Error("asValue(VTElementInstance) should point at element field")
	}
	if v := iv.asValue(model.VTJob); v != &iv.job {
		t.Error("asValue(VTJob) should point at job field")
	}
	if v := iv.asValue(model.VTVariable); v != &iv.variable {
		t.Error("asValue(VTVariable) should point at variable field")
	}
	if v := iv.asValue(model.ValueType(200)); v != nil {
		t.Errorf("asValue(unknown) = %v, want nil", v)
	}
}

// TestInflightFromRecord copies each payload type back into an inflightValue on
// recovery, tolerates a mismatched Value (leaves the field zero), and no-ops an
// unknown value type.
func TestInflightFromRecord(t *testing.T) {
	pi := &model.ProcessInstanceValue{ProcessDefKey: 9}
	if iv := inflightFromRecord(model.Record{Header: model.RecordHeader{ValueType: model.VTProcessInstance}, Value: pi}); iv.process != *pi {
		t.Errorf("process = %+v, want %+v", iv.process, *pi)
	}
	el := &model.ElementInstanceValue{ElementId: 3}
	if iv := inflightFromRecord(model.Record{Header: model.RecordHeader{ValueType: model.VTElementInstance}, Value: el}); iv.element != *el {
		t.Errorf("element = %+v, want %+v", iv.element, *el)
	}
	job := &model.JobValue{JobType: 7}
	if iv := inflightFromRecord(model.Record{Header: model.RecordHeader{ValueType: model.VTJob}, Value: job}); iv.job != *job {
		t.Errorf("job = %+v, want %+v", iv.job, *job)
	}
	vr := &model.VariableValue{Name: "x"}
	if iv := inflightFromRecord(model.Record{Header: model.RecordHeader{ValueType: model.VTVariable}, Value: vr}); iv.variable != *vr {
		t.Errorf("variable = %+v, want %+v", iv.variable, *vr)
	}

	// Mismatched concrete Value: the type assertion fails, leaving the field zero.
	iv := inflightFromRecord(model.Record{Header: model.RecordHeader{ValueType: model.VTJob}, Value: pi})
	if iv.job != (model.JobValue{}) {
		t.Errorf("mismatched value: job = %+v, want zero", iv.job)
	}
	// Unknown value type: nothing copied.
	iv = inflightFromRecord(model.Record{Header: model.RecordHeader{ValueType: model.ValueType(200)}})
	if iv != (inflightValue{}) {
		t.Errorf("unknown value type: iv = %+v, want zero", iv)
	}
}

// TestApplyToStateAllIntents drives applyToState through every value-type/intent
// branch it handles, plus the no-op defaults, using a real transaction. This is
// the single live+replay state applier (invariant I4).
func TestApplyToStateAllIntents(t *testing.T) {
	store := openStore(t)
	tx := store.NewTransaction()
	defer tx.Close()

	pi := inflightValue{process: model.ProcessInstanceValue{ProcessDefKey: 9}}
	el := inflightValue{element: model.ElementInstanceValue{ElementId: 3, FlowScopeKey: 100}}
	job := inflightValue{job: model.JobValue{JobType: 7}}
	vr := inflightValue{variable: model.VariableValue{ScopeKey: 100, Name: "x", Kind: model.VarNumber, Text: "1"}}

	cases := []struct {
		name   string
		vt     model.ValueType
		intent model.Intent
		key    uint64
		v      inflightValue
	}{
		{"pi activated", model.VTProcessInstance, model.IntentActivated, 1, pi},
		{"pi completed", model.VTProcessInstance, model.IntentCompleted, 1, pi},
		{"pi terminated", model.VTProcessInstance, model.IntentTerminated, 1, pi},
		{"pi unknown intent", model.VTProcessInstance, model.IntentActivating, 1, pi},
		{"ei activated", model.VTElementInstance, model.IntentActivated, 2, el},
		{"ei completed", model.VTElementInstance, model.IntentCompleted, 2, el},
		{"ei terminated", model.VTElementInstance, model.IntentTerminated, 2, el},
		{"ei unknown intent", model.VTElementInstance, model.IntentActivating, 2, el},
		{"job created", model.VTJob, model.IntentJobCreated, 3, job},
		{"job completed", model.VTJob, model.IntentJobCompleted, 3, job},
		{"job failed", model.VTJob, model.IntentJobFailed, 3, job},
		{"job unknown intent", model.VTJob, model.IntentActivating, 3, job},
		{"var created", model.VTVariable, model.IntentVariableCreated, 100, vr},
		{"var updated", model.VTVariable, model.IntentVariableUpdated, 100, vr},
		{"var unknown intent", model.VTVariable, model.IntentActivating, 100, vr},
		{"unknown value type", model.ValueType(200), model.IntentActivated, 1, pi},
	}
	for _, tc := range cases {
		v := tc.v
		h := model.RecordHeader{Key: tc.key, ValueType: tc.vt, Intent: tc.intent}
		if err := applyToState(tx, h, &v); err != nil {
			t.Errorf("%s: applyToState error: %v", tc.name, err)
		}
	}
}

// TestFail records the first error and ignores nil / later errors.
func TestFail(t *testing.T) {
	p := &Processor{}
	p.fail(nil)
	if p.fatalErr != nil {
		t.Errorf("fail(nil) set fatalErr = %v, want nil", p.fatalErr)
	}
	first := errors.New("first")
	p.fail(first)
	p.fail(errors.New("second"))
	if p.fatalErr != first {
		t.Errorf("fatalErr = %v, want first", p.fatalErr)
	}
}

// TestProcessOneUnknownCommand: a command with no registered handler is silently
// rejected (never persisted), the batch stays empty, and the queue drains.
func TestProcessOneUnknownCommand(t *testing.T) {
	dir := t.TempDir()
	log, err := wal.Open(wal.Options{Dir: filepath.Join(dir, "wal")})
	if err != nil {
		t.Fatalf("wal.Open: %v", err)
	}
	defer log.Close()
	store, err := state.Open(filepath.Join(dir, "state"))
	if err != nil {
		t.Fatalf("state.Open: %v", err)
	}
	defer store.Close()

	p := New(1, log, store, &wbClock{})
	p.queue = append(p.queue, Command{ValueType: model.VTProcessInstance, Intent: model.Intent(200)})
	if err := p.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle: %v", err)
	}
	if len(p.queue) != 0 {
		t.Errorf("queue = %d, want drained", len(p.queue))
	}
}

// TestFatalErrPropagates: a state read error set during command processing aborts
// the batch and surfaces from RunUntilIdle. We inject it by overriding a handler
// to fail — exercising the fatalErr abort path (processBatch) and the error
// return from RunUntilIdle deterministically, without corrupting storage.
func TestFatalErrPropagates(t *testing.T) {
	store := openStore(t)
	p := New(1, nil, store, &wbClock{})
	boom := errors.New("boom")
	p.handlers[handlerKey(model.VTProcessInstance, model.IntentActivating)] = func(c *ProcessingContext) {
		c.p.fail(boom)
	}
	p.queue = append(p.queue, Command{ValueType: model.VTProcessInstance, Intent: model.IntentActivating})
	if err := p.RunUntilIdle(); !errors.Is(err, boom) {
		t.Fatalf("RunUntilIdle error = %v, want boom", err)
	}
}

// TestHandleJobCompletedMissingJob: completing a job that does not exist is a
// no-op (nothing to retire), not an error.
func TestHandleJobCompletedMissingJob(t *testing.T) {
	dir := t.TempDir()
	log, err := wal.Open(wal.Options{Dir: filepath.Join(dir, "wal")})
	if err != nil {
		t.Fatalf("wal.Open: %v", err)
	}
	defer log.Close()
	store, err := state.Open(filepath.Join(dir, "state"))
	if err != nil {
		t.Fatalf("state.Open: %v", err)
	}
	defer store.Close()

	p := New(1, log, store, &wbClock{})
	if err := p.Recover(); err != nil {
		t.Fatalf("Recover: %v", err)
	}
	p.CompleteJob(model.NewKey(1, 999)) // never created
	if err := p.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle: %v", err)
	}
}

// TestHandleJobCompletedMissingElement: a job whose element instance is already
// gone is retired without scheduling a completion command (the ei == nil branch).
func TestHandleJobCompletedMissingElement(t *testing.T) {
	dir := t.TempDir()
	log, err := wal.Open(wal.Options{Dir: filepath.Join(dir, "wal")})
	if err != nil {
		t.Fatalf("wal.Open: %v", err)
	}
	defer log.Close()
	store, err := state.Open(filepath.Join(dir, "state"))
	if err != nil {
		t.Fatalf("state.Open: %v", err)
	}
	defer store.Close()

	// Seed a job pointing at a non-existent element instance.
	jobKey := model.NewKey(1, 1)
	tx := store.NewTransaction()
	if err := tx.PutJob(jobKey, &model.JobValue{ElementInstanceKey: model.NewKey(1, 42), JobType: 5}); err != nil {
		t.Fatalf("PutJob: %v", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("Commit: %v", err)
	}
	tx.Close()

	p := New(1, log, store, &wbClock{})
	if err := p.Recover(); err != nil {
		t.Fatalf("Recover: %v", err)
	}
	p.CompleteJob(jobKey)
	if err := p.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle: %v", err)
	}
	// Job retired.
	if _, ok, err := store.GetJob(jobKey); err != nil || ok {
		t.Errorf("GetJob after completion: ok=%v err=%v, want retired", ok, err)
	}
}

// TestRecoverCorruptRecord: a log frame that is not a decodable record makes
// recovery fail rather than silently skipping it.
func TestRecoverCorruptRecord(t *testing.T) {
	dir := t.TempDir()
	log, err := wal.Open(wal.Options{Dir: filepath.Join(dir, "wal")})
	if err != nil {
		t.Fatalf("wal.Open: %v", err)
	}
	// A single byte is a valid WAL frame but too short to be a record header.
	if err := log.Append([]byte{0x01}); err != nil {
		t.Fatalf("Append: %v", err)
	}
	if err := log.Sync(); err != nil {
		t.Fatalf("Sync: %v", err)
	}
	if err := log.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	log2, err := wal.Open(wal.Options{Dir: filepath.Join(dir, "wal")})
	if err != nil {
		t.Fatalf("wal.Open 2: %v", err)
	}
	defer log2.Close()
	store, err := state.Open(filepath.Join(dir, "state"))
	if err != nil {
		t.Fatalf("state.Open: %v", err)
	}
	defer store.Close()

	p := New(1, log2, store, &wbClock{})
	if err := p.Recover(); err == nil {
		t.Fatal("Recover over a corrupt record = nil error, want error")
	}
}
