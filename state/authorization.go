package state

import (
	"encoding/binary"

	"github.com/cockroachdb/pebble"
	"github.com/pblumer/atlas/model"
)

// Authorization evidence and approval facts
// (ADR-0475 §12).
//
// Both families are append-only history keyed under their process instance, like the
// operator-action trail, and both go with the instance when its history is purged:
// evidence is retained exactly as long as the history it explains.
//
//	authz:<piKey>:<jobKey>:<pos>    → AuthorizationValue
//	apr:<piKey>:<approvalKey>:<pos> → ActionApprovalValue
//	aprExp:<expiresAt>:<approvalKey> → piKey
//
// The third is an index: every approval request, by when it expires. "Which approvals
// are open now" — the approver's inbox, across every instance — is then a range read
// from now, as long as the open requests and no longer, rather than a scan of every
// approval fact ever recorded.
//
// The job key sits before the position so "was this job's current lease permitted" is
// a scan of one job's decisions, not of the instance's. Both families are declared with
// every other in keys.go, where a value already taken is in plain sight.

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

func keyApprovalExpiry(expiresAt int64, approvalKey uint64) []byte {
	if expiresAt < 0 {
		expiresAt = 0
	}
	return appendBE64(appendBE64([]byte{byte(cfApprovalExpiry)}, uint64(expiresAt)), approvalKey)
}

// RecordApprovalFact retains one fact about an approval, and indexes a request by its
// expiry. Written from applyToState only.
func (t *Tx) RecordApprovalFact(pos uint64, v *model.ActionApprovalValue) error {
	if err := t.b.Set(keyApprovalFact(v.ProcessInstanceKey, v.ApprovalKey, pos), t.encodeValue(v), nil); err != nil {
		return err
	}
	if v.Kind != model.ApprovalRequested {
		return nil
	}
	return t.b.Set(keyApprovalExpiry(v.ExpiresAt, v.ApprovalKey), appendBE64(nil, v.ProcessInstanceKey), nil)
}

// deleteApprovalExpiries removes the expiry index entries of an instance's requests,
// before its approval facts — which say where those entries are — are purged.
func (t *Tx) deleteApprovalExpiries(piKey uint64) error {
	var keys [][]byte
	if err := t.ApprovalFactsInBatch(piKey, func(v *model.ActionApprovalValue) error {
		if v.Kind == model.ApprovalRequested {
			keys = append(keys, keyApprovalExpiry(v.ExpiresAt, v.ApprovalKey))
		}
		return nil
	}); err != nil {
		return err
	}
	for _, k := range keys {
		if err := t.b.Delete(k, nil); err != nil {
			return err
		}
	}
	return nil
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

// OpenApprovalRequests calls fn with every approval request still open at now — one
// that expires after now — soonest-expiring first, until the requests run out or fn
// returns an error.
func (q queries) OpenApprovalRequests(now int64, fn func(piKey, approvalKey uint64) error) error {
	lower := keyApprovalExpiry(now, ^uint64(0))
	return q.scanRange(prefixEnd(lower), prefixEnd([]byte{byte(cfApprovalExpiry)}), func(k, v []byte) error {
		if len(v) < 8 {
			return nil
		}
		return fn(binary.BigEndian.Uint64(v), trailingKey(k))
	})
}
