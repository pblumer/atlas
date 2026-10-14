package engine

import (
	"encoding/binary"
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

// TestContinuationCorruptionIsCaughtAtEveryStage walks the decoder's structure and
// damages each stage in turn.
//
// The stages fail differently — a command record that does not decode, a length
// that points past the end, a start variable that decodes to something that is not
// a variable — and each has its own way of being wrong. A decoder that caught the
// first and not the rest would still let a restart resume with fewer commands than
// the log recorded, which is the failure the whole continuation exists to prevent.
// The point of this test is that "does not parse" is answered the same way
// everywhere: an error, never a shorter queue.
func TestContinuationCorruptionIsCaughtAtEveryStage(t *testing.T) {
	valid := encodeContinuation(nil, []Command{{
		Key:       model.NewKey(1, 7),
		ValueType: model.VTProcessInstance,
		Intent:    model.IntentActivating,
		SourcePos: 4,
		Value:     inflightValue{process: model.ProcessInstanceValue{ProcessDefKey: 9}},
		StartVars: []model.VariableValue{
			{ScopeKey: model.NewKey(1, 3), Name: "amount", Kind: model.VarString, Text: "42"},
		},
		StartElements: []int32{2},
	}})

	// Walk the layout so the damage lands where it is meant to:
	// [count][cmdLen][cmdRec][varCount][varLen][varRec][elemCount][elem]
	u32 := func(b []byte, off int) int { return int(binary.LittleEndian.Uint32(b[off:])) }
	cmdLen := u32(valid, 4)
	offVarCount := 8 + cmdLen
	varLen := u32(valid, offVarCount+4)
	offElemCount := offVarCount + 4 + 4 + varLen

	cut := func(n int) []byte { return append([]byte(nil), valid[:n]...) }
	withU32 := func(off int, v uint32) []byte {
		b := append([]byte(nil), valid...)
		binary.LittleEndian.PutUint32(b[off:], v)
		return b
	}

	for _, tc := range []struct {
		name string
		b    []byte
	}{
		{"command record does not decode", func() []byte {
			b := append([]byte(nil), valid...)
			b[8] = 0xFF // the record's codec version byte
			return b
		}()},
		{"command length points past the end", withU32(4, 0xFFFF)},
		{"ends before the start-variable count", cut(8 + cmdLen)},
		{"start-variable length points past the end", withU32(offVarCount+4, 0xFFFF)},
		{"start variable does not decode", func() []byte {
			b := append([]byte(nil), valid...)
			b[offVarCount+8] = 0xFF // that record's codec version byte
			return b
		}()},
		{"ends before the start-element count", cut(offElemCount)},
		{"claims more start elements than it carries", withU32(offElemCount, 0xFF)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := decodeContinuation(tc.b); err == nil {
				t.Fatal("a malformed continuation decoded without error; a restart would resume owing less than the log recorded")
			}
		})
	}

	// The undamaged encoding still decodes, so the cases above fail for the reason
	// they name rather than because the fixture was broken to begin with.
	if _, err := decodeContinuation(valid); err != nil {
		t.Fatalf("the undamaged fixture does not decode: %v", err)
	}
}

// TestContinuationRejectsAStartVariableThatIsNotOne: a record in the start-variable
// position that decodes cleanly as something else is still wrong. Accepting it
// would drop the variable and resume an instance seeded with less than it was
// started with — the kind of loss that shows up as a business decision taken on
// missing data, not as an error.
func TestContinuationRejectsAStartVariableThatIsNotOne(t *testing.T) {
	var b []byte
	b = binary.LittleEndian.AppendUint32(b, 1) // one command
	cmd := Command{
		Key: model.NewKey(1, 7), ValueType: model.VTElementInstance,
		Intent: model.IntentActivating, SourcePos: 4,
		Value: inflightValue{element: model.ElementInstanceValue{ElementId: 1}},
	}
	b = appendSized(b, func(dst []byte) []byte {
		rec := model.Record{
			Header: model.RecordHeader{
				SourcePos: cmd.SourcePos, Key: cmd.Key, RecordType: model.RecordCommand,
				ValueType: cmd.ValueType, Intent: cmd.Intent,
			},
			Value: cmd.Value.asValue(cmd.ValueType),
		}
		return model.AppendRecord(dst, &rec)
	})
	b = binary.LittleEndian.AppendUint32(b, 1) // one "start variable"…
	b = appendSized(b, func(dst []byte) []byte {
		// …that is a job, not a variable.
		rec := model.Record{
			Header: model.RecordHeader{RecordType: model.RecordCommand, ValueType: model.VTJob},
			Value:  &model.JobValue{JobType: 3},
		}
		return model.AppendRecord(dst, &rec)
	})
	b = binary.LittleEndian.AppendUint32(b, 0) // no start elements

	if _, err := decodeContinuation(b); err == nil {
		t.Fatal("a job in the start-variable position was accepted as a variable")
	}
}
