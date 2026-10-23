package state

import (
	"github.com/cockroachdb/pebble"
	"github.com/pblumer/atlas/model"
)

// Authorization evidence and approval facts
// (ADR-draft-identity-aware-worker-execution §12).
//
// Both families are append-only history keyed under their process instance, like the
// operator-action trail, and both go with the instance when its history is purged:
// evidence is retained exactly as long as the history it explains.
//
//	authz:<piKey>:<jobKey>:<pos>    → AuthorizationValue
//	apr:<piKey>:<approvalKey>:<pos> → ActionApprovalValue
//
// The job key sits before the position so "was this job's current lease permitted" is
// a scan of one job's decisions, not of the instance's.

const (
	cfAuthorization  columnFamily = 0x32
	cfActionApproval columnFamily = 0x33
)

func authorizationInstancePrefix(piKey uint64) []byte {
	return appendBE64([]byte{byte(cfAuthorization)}, piKey)
}

func authorizationJobPrefix(piKey, jobKey uint64) []byte {
	return appendBE64(authorizationInstancePrefix(piKey), jobKey)
}

func keyAuthorization(piKey, jobKey, pos uint64) []byte {
	return appendBE64(authorizationJobPrefix(piKey, jobKey), pos)
}

func approvalInstancePrefix(piKey uint64) []byte {
	return appendBE64([]byte{byte(cfActionApproval)}, piKey)
}

func keyApprovalFact(piKey, approvalKey, pos uint64) []byte {
	return appendBE64(appendBE64(approvalInstancePrefix(piKey), approvalKey), pos)
}

// RecordAuthorization retains one decision. Written from applyToState only; the
// position comes from the event's header, so replay writes the identical key.
func (t *Tx) RecordAuthorization(pos uint64, v *model.AuthorizationValue) error {
	return t.b.Set(keyAuthorization(v.ProcessInstanceKey, v.JobKey, pos), t.encodeValue(v), nil)
}

// RecordApprovalFact retains one fact about an approval. Written from applyToState
// only.
func (t *Tx) RecordApprovalFact(pos uint64, v *model.ActionApprovalValue) error {
	return t.b.Set(keyApprovalFact(v.ProcessInstanceKey, v.ApprovalKey, pos), t.encodeValue(v), nil)
}

// ApprovalFactsInBatch folds an instance's approval facts as this transaction sees
// them — committed rows plus the batch's own — in approval-then-log order. The command
// path reads it to find an open request and to check an answer against it, and must
// see a request written earlier in the same batch.
func (t *Tx) ApprovalFactsInBatch(piKey uint64, fn func(v *model.ActionApprovalValue) error) error {
	prefix := approvalInstancePrefix(piKey)
	iter, err := t.b.NewIter(&pebble.IterOptions{LowerBound: prefix, UpperBound: prefixEnd(prefix)})
	if err != nil {
		return err
	}
	defer iter.Close()
	for iter.First(); iter.Valid(); iter.Next() {
		var v model.ActionApprovalValue
		if err := model.DecodeValueInto(&v, iter.Value()); err != nil {
			return err
		}
		if err := fn(&v); err != nil {
			return err
		}
	}
	return iter.Error()
}

// AuthorizationHistory folds an instance's decisions in job-then-log order.
func (q queries) AuthorizationHistory(piKey uint64, fn func(pos uint64, v *model.AuthorizationValue) error) error {
	return q.scanAuthorizations(authorizationInstancePrefix(piKey), fn)
}

// AuthorizationsOfJob folds one job's decisions in log order.
func (q queries) AuthorizationsOfJob(piKey, jobKey uint64, fn func(pos uint64, v *model.AuthorizationValue) error) error {
	return q.scanAuthorizations(authorizationJobPrefix(piKey, jobKey), fn)
}

func (q queries) scanAuthorizations(prefix []byte, fn func(pos uint64, v *model.AuthorizationValue) error) error {
	return q.scanPrefix(prefix, func(k, raw []byte) error {
		var v model.AuthorizationValue
		if err := model.DecodeValueInto(&v, raw); err != nil {
			return err
		}
		return fn(trailingKey(k), &v)
	})
}

// ApprovalFacts folds an instance's approval facts in approval-then-log order.
func (q queries) ApprovalFacts(piKey uint64, fn func(v *model.ActionApprovalValue) error) error {
	return q.scanPrefix(approvalInstancePrefix(piKey), func(_, raw []byte) error {
		var v model.ActionApprovalValue
		if err := model.DecodeValueInto(&v, raw); err != nil {
			return err
		}
		return fn(&v)
	})
}
