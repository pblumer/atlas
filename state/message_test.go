package state_test

import (
	"testing"

	"github.com/pblumer/atlas/model"
	"github.com/pblumer/atlas/state"
)

func TestMessageSubscriptionCorrelation(t *testing.T) {
	s := openStore(t)

	sub := func(el uint64, name, key string) *model.MessageSubscriptionValue {
		return &model.MessageSubscriptionValue{
			ProcessInstanceKey: model.NewKey(1, el*10),
			ElementInstanceKey: model.NewKey(1, el),
			MessageName:        name,
			CorrelationKey:     key,
		}
	}
	a := sub(1, "order", "42")
	b := sub(2, "order", "42") // same (name, key), different instance → both match
	c := sub(3, "order", "99") // same name, different key → must not match

	commit(t, s, func(tx *state.Tx) error {
		for _, v := range []*model.MessageSubscriptionValue{a, b, c} {
			if err := tx.PutMessageSubscription(v); err != nil {
				return err
			}
		}
		return nil
	})

	// A scan for ("order","42") returns exactly a and b.
	got := correlatable(t, s, "order", "42")
	if len(got) != 2 {
		t.Fatalf("matches = %d, want 2 (both instances on the same key)", len(got))
	}
	if got[a.ElementInstanceKey] != "42" || got[b.ElementInstanceKey] != "42" {
		t.Errorf("matched keys = %v, want a and b on \"42\"", got)
	}

	// A different key and a different name both match nothing of a/b.
	if n := len(correlatable(t, s, "order", "0")); n != 0 {
		t.Errorf("matches for wrong key = %d, want 0", n)
	}
	if n := len(correlatable(t, s, "shipment", "42")); n != 0 {
		t.Errorf("matches for wrong name = %d, want 0", n)
	}

	// Correlating (deleting) a retires just that subscription.
	commit(t, s, func(tx *state.Tx) error { return tx.DeleteMessageSubscription(a) })
	got = correlatable(t, s, "order", "42")
	if len(got) != 1 || got[b.ElementInstanceKey] != "42" {
		t.Fatalf("after delete: matches = %v, want only b", got)
	}
}

// correlatable collects the correlation keys of every subscription matching
// (name, correlationKey), indexed by element-instance key.
func correlatable(t *testing.T, s *state.Store, name, correlationKey string) map[uint64]string {
	t.Helper()
	out := map[uint64]string{}
	tx := s.NewTransaction()
	defer tx.Close()
	if err := tx.CorrelatableSubscriptions(name, correlationKey, func(elKey uint64, v *model.MessageSubscriptionValue) error {
		out[elKey] = v.CorrelationKey
		return nil
	}); err != nil {
		t.Fatalf("CorrelatableSubscriptions: %v", err)
	}
	return out
}
