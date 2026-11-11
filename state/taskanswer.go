package state

import (
	"encoding/binary"
	"errors"
	"fmt"

	"github.com/cockroachdb/pebble"

	"github.com/pblumer/atlas/model"
)

// A person's completed tasks (ADR-0554 §3).
//
// applyToState folds every TaskAnswered fact into three rows:
//
//   - taskAns:<piKey>:<jobKey> → at(8) position(8) TaskAnswerValue — the fact itself, under its
//     instance, so a purge of the instance's history takes it by prefix;
//   - taskAnsBy:<completer>0x00<at>:<jobKey> → piKey(8) — the person's list, which a
//     reader walks newest first;
//   - taskAnsJob:<jobKey> → piKey(8) — the point read a task opened by its key needs.
//
// The two indexes are keyed by person and by job, not by instance, so a purge finds
// them through the facts before it drops those (deleteTaskAnswerIndexes). A completed
// task is therefore listed exactly as long as its instance's history exists.

// TaskAnswer is one answered user task as the index holds it: the fact, when it was
// recorded (the record header's timestamp, unix nanoseconds) and where on the log. The
// position is what the instance's history is read up to, to show the variables as
// they stood once the task was answered (ADR-0554 §3).
type TaskAnswer struct {
	At       int64
	Position uint64
	Value    model.TaskAnswerValue
}

func taskAnswerInstancePrefix(piKey uint64) []byte {
	return appendBE64([]byte{byte(cfTaskAnswer)}, piKey)
}

func keyTaskAnswer(piKey, jobKey uint64) []byte {
	return appendBE64(taskAnswerInstancePrefix(piKey), jobKey)
}

// taskAnswerByPrefix scans one person's answered tasks. A principal id never holds a
// zero byte, so the separator keeps "usr_a" from being a prefix of "usr_ab".
func taskAnswerByPrefix(completer string) []byte {
	out := append([]byte{byte(cfTaskAnswerByCompleter)}, completer...)
	return append(out, 0x00)
}

func keyTaskAnswerBy(completer string, at int64, jobKey uint64) []byte {
	return appendBE64(appendOrderedInt64(taskAnswerByPrefix(completer), at), jobKey)
}

func keyTaskAnswerJob(jobKey uint64) []byte {
	return appendBE64([]byte{byte(cfTaskAnswerByJob)}, jobKey)
}

// RecordTaskAnswer folds one TaskAnswered fact (ADR-0554). Everything comes from the
// event — the value and the header's timestamp — so replay writes the same rows under
// the same keys (I4, I6). A fact without a completer is not a person's answer and is
// not indexed; the engine never appends one, and a hand-made log is not trusted to.
func (t *Tx) RecordTaskAnswer(at int64, pos uint64, v *model.TaskAnswerValue) error {
	if v.Completer == "" || v.ProcessInstanceKey == 0 || v.JobKey == 0 {
		return nil
	}
	t.scratch = binary.BigEndian.AppendUint64(t.scratch[:0], uint64(at))
	t.scratch = binary.BigEndian.AppendUint64(t.scratch, pos)
	t.scratch = model.AppendValue(t.scratch, v)
	if err := t.b.Set(keyTaskAnswer(v.ProcessInstanceKey, v.JobKey), t.scratch, nil); err != nil {
		return err
	}
	var ref [8]byte
	binary.BigEndian.PutUint64(ref[:], v.ProcessInstanceKey)
	if err := t.b.Set(keyTaskAnswerBy(v.Completer, at, v.JobKey), ref[:], nil); err != nil {
		return err
	}
	return t.b.Set(keyTaskAnswerJob(v.JobKey), ref[:], nil)
}

// deleteTaskAnswerIndexes drops the person and job index entries of the instance's
// answered tasks, read off the facts before the purge's prefix delete takes those.
// Bounded by the instance's own size, like the value-index entries the purge drops the
// same way.
func (t *Tx) deleteTaskAnswerIndexes(piKey uint64) error {
	prefix := taskAnswerInstancePrefix(piKey)
	iter, err := t.b.NewIter(&pebble.IterOptions{LowerBound: prefix, UpperBound: prefixEnd(prefix)})
	if err != nil {
		return err
	}
	var keys [][]byte
	for iter.First(); iter.Valid(); iter.Next() {
		a, err := decodeTaskAnswer(iter.Value())
		if err != nil {
			iter.Close()
			return err
		}
		keys = append(keys,
			keyTaskAnswerBy(a.Value.Completer, a.At, a.Value.JobKey),
			keyTaskAnswerJob(a.Value.JobKey))
	}
	if err := errors.Join(iter.Error(), iter.Close()); err != nil {
		return err
	}
	for _, k := range keys {
		if err := t.b.Delete(k, nil); err != nil {
			return err
		}
	}
	return nil
}

func decodeTaskAnswer(raw []byte) (TaskAnswer, error) {
	if len(raw) < 16 {
		return TaskAnswer{}, fmt.Errorf("state: a task answer of %d bytes is cut short", len(raw))
	}
	a := TaskAnswer{At: int64(binary.BigEndian.Uint64(raw)), Position: binary.BigEndian.Uint64(raw[8:])}
	if err := model.DecodeValueInto(&a.Value, raw[16:]); err != nil {
		return TaskAnswer{}, err
	}
	return a, nil
}

// TaskAnswerCursor is where a page of a person's answered tasks stopped: the last row
// it returned. The zero value starts at the newest.
type TaskAnswerCursor struct {
	At     int64
	JobKey uint64
}

// IsZero reports whether the cursor starts at the newest row.
func (c TaskAnswerCursor) IsZero() bool { return c.At == 0 && c.JobKey == 0 }

// TaskAnswersBy calls fn with the tasks completer answered, newest first, starting
// after the cursor, until the rows run out or fn returns an error; a caller stops a
// page early with a sentinel of its own. from and to bound the time the answer was
// recorded (unix nanoseconds, inclusive and exclusive); zero leaves a bound open.
func (q queries) TaskAnswersBy(completer string, after TaskAnswerCursor, from, to int64, fn func(TaskAnswer) error) error {
	if completer == "" {
		return nil
	}
	prefix := taskAnswerByPrefix(completer)
	lo, hi := prefix, prefixEnd(prefix)
	if from != 0 {
		lo = appendOrderedInt64(append([]byte(nil), prefix...), from)
	}
	if to != 0 {
		hi = appendOrderedInt64(append([]byte(nil), prefix...), to)
	}
	if !after.IsZero() {
		// Strictly before the cursor's row: its own key is the exclusive upper bound.
		if k := keyTaskAnswerBy(completer, after.At, after.JobKey); string(k) < string(hi) {
			hi = k
		}
	}
	return q.scanRangeDesc(lo, hi, func(k, v []byte) error {
		if len(v) != 8 || len(k) < len(prefix)+16 {
			return fmt.Errorf("state: corrupt task answer index entry")
		}
		jobKey := binary.BigEndian.Uint64(k[len(prefix)+8:])
		a, ok, err := q.taskAnswerAt(binary.BigEndian.Uint64(v), jobKey)
		if err != nil || !ok {
			return err
		}
		return fn(a)
	})
}

// TaskAnswer is the answered task with this job key, and whether the index holds it.
// A task still open, one cancelled, one completed before this index existed and one
// whose instance's history was purged are all "not held".
func (q queries) TaskAnswer(jobKey uint64) (TaskAnswer, bool, error) {
	raw, ok, err := getCopy(q.r, keyTaskAnswerJob(jobKey))
	if err != nil || !ok {
		return TaskAnswer{}, false, err
	}
	if len(raw) != 8 {
		return TaskAnswer{}, false, fmt.Errorf("state: corrupt task answer entry (%d bytes)", len(raw))
	}
	return q.taskAnswerAt(binary.BigEndian.Uint64(raw), jobKey)
}

func (q queries) taskAnswerAt(piKey, jobKey uint64) (TaskAnswer, bool, error) {
	raw, ok, err := getCopy(q.r, keyTaskAnswer(piKey, jobKey))
	if err != nil || !ok {
		return TaskAnswer{}, false, err
	}
	a, err := decodeTaskAnswer(raw)
	if err != nil {
		return TaskAnswer{}, false, err
	}
	return a, true, nil
}
