package state_test

import (
	"errors"
	"testing"

	"github.com/pblumer/atlas/model"
	"github.com/pblumer/atlas/state"
)

// TestCompensableIndex covers the compensable index family (ADR-0103): recording
// completed compensable activities under a scope keyed by log position, scanning them
// in reverse completion order (the order a throw runs handlers), the committed-read
// forward scan, deleting one consumed record, and dropping a whole scope on teardown.
func TestCompensableIndex(t *testing.T) {
	s := openStore(t)

	const scope = uint64(100)
	rec := func(seq uint64, el int32) *model.CompensableValue {
		return &model.CompensableValue{
			ProcessInstanceKey: model.NewKey(1, 1),
			ProcessDefKey:      model.NewKey(1, 2),
			ScopeKey:           scope,
			ElementInstanceKey: model.NewKey(1, uint64(el)),
			ElementId:          el,
			HandlerNode:        el + 100,
		}
	}
	// Three activities completed in order 1, 2, 3 (ascending log position).
	commit(t, s, func(tx *state.Tx) error {
		if err := tx.RecordCompensable(10, rec(10, 1)); err != nil {
			return err
		}
		if err := tx.RecordCompensable(20, rec(20, 2)); err != nil {
			return err
		}
		return tx.RecordCompensable(30, rec(30, 3))
	})

	// A record under a different scope must not bleed into this scope's scans.
	commit(t, s, func(tx *state.Tx) error {
		other := rec(40, 9)
		other.ScopeKey = 200
		return tx.RecordCompensable(40, other)
	})

	// Reverse scan yields reverse completion order: 3, 2, 1 — carrying each record's
	// key sequence so the caller can delete it after compensating.
	desc := descCompensables(t, s, scope)
	if got := elementIDs(desc); !equalInts(got, []int32{3, 2, 1}) {
		t.Fatalf("reverse scan element ids = %v, want [3 2 1]", got)
	}
	if desc[0].seq != 30 || desc[2].seq != 10 {
		t.Errorf("reverse scan seqs = %d,%d, want 30,10", desc[0].seq, desc[2].seq)
	}

	// Committed forward scan yields completion order 1, 2, 3.
	if got := elementIDs(descToPlain(committedCompensables(t, s, scope))); !equalInts(got, []int32{1, 2, 3}) {
		t.Fatalf("committed forward scan element ids = %v, want [1 2 3]", got)
	}

	// Consuming (deleting) the newest record retires just it; delete is idempotent.
	commit(t, s, func(tx *state.Tx) error {
		if err := tx.DeleteCompensable(scope, 30); err != nil {
			return err
		}
		return tx.DeleteCompensable(scope, 30) // second delete is a no-op
	})
	if got := elementIDs(descCompensables(t, s, scope)); !equalInts(got, []int32{2, 1}) {
		t.Fatalf("after consume: %v, want [2 1]", got)
	}

	// Tearing down the scope drops every remaining record; the other scope survives.
	commit(t, s, func(tx *state.Tx) error { return tx.DeleteCompensablesOfScope(scope) })
	if got := descCompensables(t, s, scope); len(got) != 0 {
		t.Fatalf("after scope teardown: %d records, want 0", len(got))
	}
	if got := committedCompensables(t, s, 200); len(got) != 1 {
		t.Errorf("other scope records = %d, want 1 (untouched)", len(got))
	}
	// Dropping a scope with nothing under it is a no-op.
	commit(t, s, func(tx *state.Tx) error { return tx.DeleteCompensablesOfScope(scope) })
}

// TestCompensablesOfScopeDescPropagatesError proves the reverse scan surfaces a
// callback error rather than swallowing it.
func TestCompensablesOfScopeDescPropagatesError(t *testing.T) {
	s := openStore(t)
	commit(t, s, func(tx *state.Tx) error {
		return tx.RecordCompensable(1, &model.CompensableValue{ScopeKey: 7, ElementId: 1})
	})
	sentinel := errors.New("boom")
	tx := s.NewTransaction()
	defer tx.Close()
	err := tx.CompensablesOfScopeDesc(7, func(uint64, *model.CompensableValue) error {
		return sentinel
	})
	if !errors.Is(err, sentinel) {
		t.Fatalf("CompensablesOfScopeDesc err = %v, want sentinel", err)
	}
}

type seqRecord struct {
	seq uint64
	v   *model.CompensableValue
}

// descCompensables collects a scope's records in reverse completion order.
func descCompensables(t *testing.T, s *state.Store, scope uint64) []seqRecord {
	t.Helper()
	var out []seqRecord
	tx := s.NewTransaction()
	defer tx.Close()
	if err := tx.CompensablesOfScopeDesc(scope, func(seq uint64, v *model.CompensableValue) error {
		cp := *v
		out = append(out, seqRecord{seq: seq, v: &cp})
		return nil
	}); err != nil {
		t.Fatalf("CompensablesOfScopeDesc: %v", err)
	}
	return out
}

// committedCompensables collects a scope's records via the committed-read forward scan.
func committedCompensables(t *testing.T, s *state.Store, scope uint64) []*model.CompensableValue {
	t.Helper()
	var out []*model.CompensableValue
	if err := s.CompensablesOfScope(scope, func(v *model.CompensableValue) error {
		cp := *v
		out = append(out, &cp)
		return nil
	}); err != nil {
		t.Fatalf("CompensablesOfScope: %v", err)
	}
	return out
}

func descToPlain(vs []*model.CompensableValue) []seqRecord {
	out := make([]seqRecord, len(vs))
	for i, v := range vs {
		out[i] = seqRecord{v: v}
	}
	return out
}

func elementIDs(recs []seqRecord) []int32 {
	out := make([]int32, len(recs))
	for i, r := range recs {
		out[i] = r.v.ElementId
	}
	return out
}

func equalInts(a, b []int32) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
