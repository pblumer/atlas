package engine

import (
	"path/filepath"
	"reflect"
	"testing"

	"github.com/pblumer/atlas/model"
	"github.com/pblumer/atlas/wal"
)

// TestEveryCommandFieldIsClassified is the guard that keeps the continuation
// honest as Command grows.
//
// The codec persists a subset of Command on purpose: the rest of the fields ride
// only on commands a client submitted, which are unacknowledged and may be lost.
// That is a sound rule and a fragile one — a field added later is silently
// dropped on every restart, and nothing fails until someone loses work in
// production. So the classification is checked against the struct rather than
// remembered.
//
// Adding a field? Decide which it is and say so in continuationCarries. If it can
// appear on a followup a handler schedules, it is "carried" and appendCommandEntry
// must write it.
func TestEveryCommandFieldIsClassified(t *testing.T) {
	typ := reflect.TypeOf(Command{})
	seen := map[string]bool{}
	for i := 0; i < typ.NumField(); i++ {
		name := typ.Field(i).Name
		seen[name] = true
		class, ok := continuationCarries[name]
		if !ok {
			t.Errorf("Command.%s is not classified for the continuation codec. Decide whether it "+
				"can ride on a followup a handler schedules (\"carried\", and appendCommandEntry "+
				"must write it) or only on a client-submitted command (\"external\"), and record "+
				"that in continuationCarries.", name)
			continue
		}
		if class != "carried" && class != "external" {
			t.Errorf("Command.%s is classified %q, want \"carried\" or \"external\"", name, class)
		}
	}
	for name := range continuationCarries {
		if !seen[name] {
			t.Errorf("continuationCarries names %q, which Command no longer has", name)
		}
	}
}

// TestContinuationRoundTrip: every carried field survives encode/decode, for the
// command shapes a handler actually schedules.
func TestContinuationRoundTrip(t *testing.T) {
	elementCmd := Command{
		Key:       model.NewKey(1, 7),
		ValueType: model.VTElementInstance,
		Intent:    model.IntentActivating,
		SourcePos: 11,
		Value: inflightValue{element: model.ElementInstanceValue{
			ProcessInstanceKey: model.NewKey(1, 3), ProcessDefKey: 9,
			ElementId: 4, FlowScopeKey: model.NewKey(1, 2), TokenID: 5,
		}},
	}
	createCmd := Command{
		ValueType: model.VTProcessInstance,
		Intent:    model.IntentActivating,
		SourcePos: 12,
		Value: inflightValue{process: model.ProcessInstanceValue{
			ProcessDefKey: 9, CorrelationKey: "order-1",
		}},
		StartVars: []model.VariableValue{
			{ScopeKey: model.NewKey(1, 3), Name: "amount", Kind: model.VarString, Text: "42"},
			{ScopeKey: model.NewKey(1, 3), Name: "who", Kind: model.VarString, Text: "alice"},
		},
		StartElements: []int32{2},
	}
	childCmd := Command{
		ValueType: model.VTProcessInstance,
		Intent:    model.IntentActivating,
		SourcePos: 13,
		Value: inflightValue{process: model.ProcessInstanceValue{
			ProcessDefKey: 8, ParentElementInstanceKey: model.NewKey(1, 6),
		}},
	}
	want := []Command{elementCmd, createCmd, childCmd}

	got, err := decodeContinuation(encodeContinuation(nil, want))
	if err != nil {
		t.Fatalf("decodeContinuation: %v", err)
	}
	if len(got) != len(want) {
		t.Fatalf("decoded %d commands, want %d", len(got), len(want))
	}
	for i := range want {
		if got[i].Key != want[i].Key || got[i].ValueType != want[i].ValueType ||
			got[i].Intent != want[i].Intent || got[i].SourcePos != want[i].SourcePos {
			t.Errorf("command %d header = %+v, want %+v", i, got[i], want[i])
		}
		if !reflect.DeepEqual(got[i].Value, want[i].Value) {
			t.Errorf("command %d value = %+v, want %+v", i, got[i].Value, want[i].Value)
		}
		if !reflect.DeepEqual(got[i].StartVars, want[i].StartVars) {
			t.Errorf("command %d start vars = %+v, want %+v", i, got[i].StartVars, want[i].StartVars)
		}
		if !reflect.DeepEqual(got[i].StartElements, want[i].StartElements) {
			t.Errorf("command %d start elements = %v, want %v", i, got[i].StartElements, want[i].StartElements)
		}
	}
}

// TestContinuationSkipsClientSubmittedCommands: a command with no scheduling
// event behind it is not persisted. The API answers only after the batch that
// processed it commits, so one still sitting in the queue was never acknowledged
// — and encoding it would resurrect work a client believes did not happen.
func TestContinuationSkipsClientSubmittedCommands(t *testing.T) {
	submitted := Command{
		ValueType: model.VTProcessInstance,
		Intent:    model.IntentActivating,
		Value:     inflightValue{process: model.ProcessInstanceValue{ProcessDefKey: 9}},
		StartVars: []model.VariableValue{{Name: "x", Kind: model.VarString, Text: "1"}},
		// SourcePos deliberately zero.
	}
	scheduled := Command{
		Key:       model.NewKey(1, 7),
		ValueType: model.VTElementInstance,
		Intent:    model.IntentActivating,
		SourcePos: 4,
		Value:     inflightValue{element: model.ElementInstanceValue{ElementId: 1}},
	}

	only, err := decodeContinuation(encodeContinuation(nil, []Command{submitted}))
	if err != nil {
		t.Fatalf("decodeContinuation: %v", err)
	}
	if len(only) != 0 {
		t.Fatalf("a client-submitted command was persisted: %+v", only)
	}
	got, err := decodeContinuation(encodeContinuation(nil, []Command{submitted, scheduled}))
	if err != nil {
		t.Fatalf("decodeContinuation: %v", err)
	}
	if len(got) != 1 || got[0].Key != scheduled.Key {
		t.Fatalf("decoded %+v, want only the scheduled command", got)
	}
}

// TestContinuationRejectsCorruption: the continuation rides inside a batch that
// has already passed its checksum, so bytes that do not parse there were written
// that way. Reporting it is what keeps a restart from resuming with less work
// than it owes.
func TestContinuationRejectsCorruption(t *testing.T) {
	good := encodeContinuation(nil, []Command{{
		Key: model.NewKey(1, 7), ValueType: model.VTElementInstance,
		Intent: model.IntentActivating, SourcePos: 4,
		Value: inflightValue{element: model.ElementInstanceValue{ElementId: 1}},
	}})

	for _, tc := range []struct {
		name string
		b    []byte
	}{
		{"empty", nil},
		{"count without commands", []byte{1, 0, 0, 0}},
		{"truncated mid-command", good[:len(good)-3]},
		{"trailing bytes", append(append([]byte(nil), good...), 0x00)},
		{"count claims more than is there", func() []byte {
			b := append([]byte(nil), good...)
			b[0] = 9
			return b
		}()},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := decodeContinuation(tc.b); err == nil {
				t.Fatal("a malformed continuation decoded without error")
			}
		})
	}
}

// TestHighestRestoredCounterIgnoresForeignKeys: only this partition's keys move
// its counter. A key another partition minted says nothing about which numbers
// this one has handed out, and treating it as a high-water mark would skip a
// whole range of this partition's keys.
func TestHighestRestoredCounterIgnoresForeignKeys(t *testing.T) {
	cmds := []Command{
		{Key: model.NewKey(1, 5)},
		{Key: model.NewKey(2, 9999)}, // another partition's
		{Key: model.NewKey(1, 12)},
		{Key: 0}, // a creation mints its key in the handler and carries none
	}
	if got := highestRestoredCounter(1, cmds); got != 12 {
		t.Errorf("highestRestoredCounter = %d, want 12 (this partition's highest)", got)
	}
	if got := highestRestoredCounter(3, cmds); got != 0 {
		t.Errorf("highestRestoredCounter for an uninvolved partition = %d, want 0", got)
	}
}

// TestRecoveryRefusesACorruptContinuation: a continuation that does not parse
// rode inside a batch that passed its checksum, so those bytes are the bytes that
// were written. Resuming anyway would mean resuming with less work than the
// engine owes — silently, and with no way to notice. Recovery must refuse.
func TestRecoveryRefusesACorruptContinuation(t *testing.T) {
	dir := t.TempDir()
	l, err := wal.Open(wal.Options{Dir: filepath.Join(dir, "wal")})
	if err != nil {
		t.Fatalf("wal.Open: %v", err)
	}
	// A well-formed batch whose continuation claims one command and carries none.
	if err := l.Append([]byte("not-a-record-but-framed-fine")); err != nil {
		t.Fatalf("Append: %v", err)
	}
	if err := l.AppendContinuation([]byte{1, 0, 0, 0}); err != nil {
		t.Fatalf("AppendContinuation: %v", err)
	}
	if err := l.Sync(); err != nil {
		t.Fatalf("Sync: %v", err)
	}
	l.Close()

	p, l2, s := openAt(t, dir, "state")
	defer l2.Close()
	defer s.Close()
	if err := p.Recover(); err == nil {
		t.Fatal("recovery accepted a continuation that does not parse; it must refuse rather than resume owing less")
	}
}
