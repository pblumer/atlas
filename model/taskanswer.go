package model

import "encoding/binary"

// TaskAnswerValue is the fact that a person answered a user task (ADR-0554 §1): who
// completed it, through which revision of its form, and where it sat. It is appended
// in the batch that completes the task's job, so the two are durable together, and it
// is folded into the index a person's completed tasks are listed from.
//
// It exists beside the `completedBy` variable rather than instead of it. A variable is
// process data: the next completion in the same scope overwrites it and nothing
// indexes it. This value is never overwritten, and it is keyed under its instance so
// it goes with the instance's history.
//
// The element is referenced by its index in the definition's compiled graph, never by
// its id (invariant I5), and the form by the revision the person answered; the form's
// id is the compiled element's, so it is not written again. Completer is a principal
// id, the same reference an order records for who decided a line. Assignee and
// CandidateGroups are the job's as it ended. All strings are length-prefixed.
type TaskAnswerValue struct {
	ProcessInstanceKey uint64 // owning instance (the scope this record is keyed under)
	ElementInstanceKey uint64 // the user task's element instance
	JobKey             uint64 // the user task's job: the task's key
	ProcessDefKey      uint64 // the definition the instance ran when the task ended
	ElementId          int32  // the user task's index in that definition's compiled graph
	Completer          string // principal id of the person who completed it
	// FormRevision is the digest of the form revision the person answered
	// (`sha256:<hex>`), or "" when the task had no form or no revision was known.
	FormRevision    string
	Assignee        string // the job's assignee as it ended; "" when unassigned
	CandidateGroups string // the job's candidate groups as it ended, comma-separated
}

func (*TaskAnswerValue) ValueType() ValueType { return VTTaskAnswer }

func (v *TaskAnswerValue) encode(dst []byte) []byte {
	dst = binary.LittleEndian.AppendUint64(dst, v.ProcessInstanceKey)
	dst = binary.LittleEndian.AppendUint64(dst, v.ElementInstanceKey)
	dst = binary.LittleEndian.AppendUint64(dst, v.JobKey)
	dst = binary.LittleEndian.AppendUint64(dst, v.ProcessDefKey)
	dst = binary.LittleEndian.AppendUint32(dst, uint32(v.ElementId))
	dst = appendString(dst, v.Completer)
	dst = appendString(dst, v.FormRevision)
	dst = appendString(dst, v.Assignee)
	dst = appendString(dst, v.CandidateGroups)
	return dst
}

func (v *TaskAnswerValue) decode(src []byte) error {
	if len(src) < 36 {
		return ErrShortBuffer
	}
	v.ProcessInstanceKey = binary.LittleEndian.Uint64(src)
	v.ElementInstanceKey = binary.LittleEndian.Uint64(src[8:])
	v.JobKey = binary.LittleEndian.Uint64(src[16:])
	v.ProcessDefKey = binary.LittleEndian.Uint64(src[24:])
	v.ElementId = int32(binary.LittleEndian.Uint32(src[32:]))
	rest := src[36:]
	for _, dst := range []*string{&v.Completer, &v.FormRevision, &v.Assignee, &v.CandidateGroups} {
		s, r, err := readString(rest)
		if err != nil {
			return err
		}
		*dst, rest = s, r
	}
	return nil
}
