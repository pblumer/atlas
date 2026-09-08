package engine

import (
	"encoding/binary"
	"fmt"

	"github.com/pblumer/atlas/model"
)

// This file makes a batch's outstanding work durable alongside the events that
// scheduled it (ADR-draft-durable-continuation).
//
// A batch can commit events and, in doing so, schedule the commands that carry
// its instances forward. Those commands lived only in memory, so a restart at a
// batch boundary left instances that were correctly materialized and would never
// move again: state == replay(WAL) held, and the process was stopped. Recovery
// rebuilt what had happened and not what still had to.
//
// The continuation is the batch's answer to "what is still owed". It is written
// as one entry inside the batch's own frame, so it is durable exactly when the
// events are — the same all-or-nothing unit (ADR-draft-wal-batch-envelope) — and
// it is not an event: applyToState never sees it, and replay does not fold it.
// It only seeds the queue.

// continuationCarries classifies every field of [Command] for this codec, so a
// field added later cannot be forgotten into silent data loss on restart.
//
//   - "carried" is persisted and restored.
//   - "external" rides only on a command a client submitted, never on a followup
//     a handler scheduled. Such a command has not been acknowledged as durable
//     work — the API answers only after the batch that processed it commits — so
//     losing it to a crash is the same contract an unacknowledged command has
//     always had.
//
// TestEveryCommandFieldIsClassified holds this map against the struct.
var continuationCarries = map[string]string{
	"Key":           "carried",
	"ValueType":     "carried",
	"Intent":        "carried",
	"Value":         "carried",
	"SourcePos":     "carried",
	"StartVars":     "carried", // a triggered start and a call activity both seed variables
	"StartElements": "carried", // which start event fired, for a triggered instantiation
	"Decision":      "external",
	"ToolCalls":     "external",
	"Actor":         "external",
	"Reason":        "external",
	"Manual":        "external",
	"RetryBackoff":  "external",
	"LeaseFor":      "external",
}

// schedulingIsDurable reports whether a queued command must survive a restart.
//
// SourcePos is the log position of the event that scheduled the command, and it
// is zero for anything a client submitted. So a non-zero SourcePos says exactly
// what is wanted here: this work was scheduled by an event that is on disk, so
// the obligation to do it is on disk too and must not evaporate. A submitted
// command with no such cause is unacknowledged and may be dropped.
func schedulingIsDurable(cmd *Command) bool { return cmd.SourcePos != 0 }

// encodeContinuation appends the durable-scheduling commands among cmds to dst.
//
// A batch that owes nothing still encodes — as a count of zero. Each continuation
// states the whole outstanding queue, and recovery takes the newest one, so an
// empty one is how a batch says "the queue is now empty". Writing nothing instead
// would leave the previous batch's continuation standing as the newest, and a
// restart would re-run work that had already been done: duplicate jobs, duplicate
// effects.
func encodeContinuation(dst []byte, cmds []Command) []byte {
	start := len(dst)
	dst = binary.LittleEndian.AppendUint32(dst, 0) // count, filled in below
	count := uint32(0)
	for i := range cmds {
		if !schedulingIsDurable(&cmds[i]) {
			continue
		}
		dst = appendCommandEntry(dst, &cmds[i])
		count++
	}
	binary.LittleEndian.PutUint32(dst[start:], count)
	return dst
}

// highestRestoredCounter is the largest per-partition key counter the commands
// carry, for keys this partition owns.
//
// It matters because a queued command already holds a key that was minted for it
// and has not appeared in any event yet — the event it will produce is exactly
// what the crash prevented. Recovery derives the key counter from the events it
// replays, so without this it would hand that same number out again, and the
// restored command's element would collide with a freshly minted one. The symptom
// is a lost element instance, not an error.
func highestRestoredCounter(partition uint16, cmds []Command) uint64 {
	var highest uint64
	for i := range cmds {
		if cmds[i].Key == 0 || model.PartitionOf(cmds[i].Key) != partition {
			continue
		}
		if c := model.CounterOf(cmds[i].Key); c > highest {
			highest = c
		}
	}
	return highest
}

// appendCommandEntry encodes one command: the command itself as a record (which
// reuses the model codec for its typed value), then its start variables and
// start elements, each length-counted.
func appendCommandEntry(dst []byte, cmd *Command) []byte {
	rec := model.Record{
		Header: model.RecordHeader{
			SourcePos:   cmd.SourcePos,
			Key:         cmd.Key,
			RecordType:  model.RecordCommand,
			ValueType:   cmd.ValueType,
			Intent:      cmd.Intent,
			PartitionId: model.PartitionOf(cmd.Key),
		},
		Value: cmd.Value.asValue(cmd.ValueType),
	}
	dst = appendSized(dst, func(b []byte) []byte { return model.AppendRecord(b, &rec) })

	dst = binary.LittleEndian.AppendUint32(dst, uint32(len(cmd.StartVars)))
	for i := range cmd.StartVars {
		v := cmd.StartVars[i]
		vr := model.Record{
			Header: model.RecordHeader{RecordType: model.RecordCommand, ValueType: model.VTVariable},
			Value:  &v,
		}
		dst = appendSized(dst, func(b []byte) []byte { return model.AppendRecord(b, &vr) })
	}

	dst = binary.LittleEndian.AppendUint32(dst, uint32(len(cmd.StartElements)))
	for _, id := range cmd.StartElements {
		dst = binary.LittleEndian.AppendUint32(dst, uint32(id))
	}
	return dst
}

// appendSized writes a uint32 length followed by whatever write produces, so the
// decoder can find the end of a model record without parsing it.
func appendSized(dst []byte, write func([]byte) []byte) []byte {
	at := len(dst)
	dst = binary.LittleEndian.AppendUint32(dst, 0)
	dst = write(dst)
	binary.LittleEndian.PutUint32(dst[at:], uint32(len(dst)-at-4))
	return dst
}

// decodeContinuation rebuilds the commands a continuation entry carries.
//
// A continuation that does not parse is corruption, not an empty one: it sat
// inside a batch that passed its checksum, so its bytes are the bytes that were
// written. Reporting it is what keeps a restart from quietly resuming with less
// work than it owes.
func decodeContinuation(b []byte) ([]Command, error) {
	if len(b) < 4 {
		return nil, fmt.Errorf("engine: continuation is %d bytes, too short for a count", len(b))
	}
	count := binary.LittleEndian.Uint32(b)
	off := 4
	cmds := make([]Command, 0, count)
	for i := uint32(0); i < count; i++ {
		cmd, next, err := readCommandEntry(b, off)
		if err != nil {
			return nil, fmt.Errorf("engine: continuation command %d of %d: %w", i+1, count, err)
		}
		cmds = append(cmds, cmd)
		off = next
	}
	if off != len(b) {
		return nil, fmt.Errorf("engine: continuation has %d trailing bytes after %d commands", len(b)-off, count)
	}
	return cmds, nil
}

func readCommandEntry(b []byte, off int) (Command, int, error) {
	var cmd Command
	raw, off, err := readSized(b, off)
	if err != nil {
		return cmd, 0, err
	}
	rec, err := model.ReadRecord(raw)
	if err != nil {
		return cmd, 0, err
	}
	cmd.Key = rec.Header.Key
	cmd.ValueType = rec.Header.ValueType
	cmd.Intent = rec.Header.Intent
	cmd.SourcePos = rec.Header.SourcePos
	cmd.Value = inflightFromRecord(rec)

	if off+4 > len(b) {
		return cmd, 0, fmt.Errorf("truncated before the start-variable count")
	}
	nVars := int(binary.LittleEndian.Uint32(b[off:]))
	off += 4
	for i := 0; i < nVars; i++ {
		var vraw []byte
		vraw, off, err = readSized(b, off)
		if err != nil {
			return cmd, 0, fmt.Errorf("start variable %d: %w", i+1, err)
		}
		vrec, verr := model.ReadRecord(vraw)
		if verr != nil {
			return cmd, 0, fmt.Errorf("start variable %d: %w", i+1, verr)
		}
		v, ok := vrec.Value.(*model.VariableValue)
		if !ok {
			return cmd, 0, fmt.Errorf("start variable %d is a %s, not a variable", i+1, vrec.Header.ValueType)
		}
		cmd.StartVars = append(cmd.StartVars, *v)
	}

	if off+4 > len(b) {
		return cmd, 0, fmt.Errorf("truncated before the start-element count")
	}
	nElems := int(binary.LittleEndian.Uint32(b[off:]))
	off += 4
	if nElems > 0 {
		if off+4*nElems > len(b) {
			return cmd, 0, fmt.Errorf("truncated in %d start elements", nElems)
		}
		cmd.StartElements = make([]int32, nElems)
		for i := 0; i < nElems; i++ {
			cmd.StartElements[i] = int32(binary.LittleEndian.Uint32(b[off:]))
			off += 4
		}
	}
	return cmd, off, nil
}

func readSized(b []byte, off int) ([]byte, int, error) {
	if off+4 > len(b) {
		return nil, 0, fmt.Errorf("truncated length prefix at byte %d", off)
	}
	n := int(binary.LittleEndian.Uint32(b[off:]))
	off += 4
	if n < 0 || off+n > len(b) {
		return nil, 0, fmt.Errorf("length %d at byte %d runs past the continuation", n, off)
	}
	return b[off : off+n], off + n, nil
}
