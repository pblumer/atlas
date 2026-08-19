package state_test

import (
	"errors"
	"testing"

	"github.com/pblumer/atlas/model"
	"github.com/pblumer/atlas/state"
)

// TestSignalSubscriptionBroadcast covers the signal subscription family: several
// instances waiting on the same name all match one name-only scan (1:n broadcast),
// a different name matches none, and deleting one retires just it (ADR-0088).
func TestSignalSubscriptionBroadcast(t *testing.T) {
	s := openStore(t)

	sub := func(el uint64, name string) *model.SignalSubscriptionValue {
		return &model.SignalSubscriptionValue{
			ProcessInstanceKey: model.NewKey(1, el*10),
			ElementInstanceKey: model.NewKey(1, el),
			SignalName:         name,
			ProcessDefKey:      model.NewKey(1, 7),
			ElementId:          int32(el),
		}
	}
	a := sub(1, "cancelled")
	b := sub(2, "cancelled") // same name, different instance → both match (broadcast)
	c := sub(3, "shipped")   // different name → must not match

	commit(t, s, func(tx *state.Tx) error {
		for _, v := range []*model.SignalSubscriptionValue{a, b, c} {
			if err := tx.PutSignalSubscription(v); err != nil {
				return err
			}
		}
		return nil
	})

	// A broadcast scan for "cancelled" returns exactly a and b.
	got := subscribed(t, s, "cancelled")
	if len(got) != 2 {
		t.Fatalf("matches = %d, want 2 (both instances on the same name)", len(got))
	}
	if !got[a.ElementInstanceKey] || !got[b.ElementInstanceKey] {
		t.Errorf("matched = %v, want a and b", got)
	}

	// A name nothing is waiting on matches nothing.
	if n := len(subscribed(t, s, "unknown")); n != 0 {
		t.Errorf("matches for unknown name = %d, want 0", n)
	}

	// Retiring (deleting) a leaves just b waiting.
	commit(t, s, func(tx *state.Tx) error { return tx.DeleteSignalSubscription(a) })
	got = subscribed(t, s, "cancelled")
	if len(got) != 1 || !got[b.ElementInstanceKey] {
		t.Fatalf("after delete: matches = %v, want only b", got)
	}
}

// TestSubscribedSignalsPropagatesError proves the scan surfaces an error the
// callback returns rather than swallowing it (the broadcast fail path).
func TestSubscribedSignalsPropagatesError(t *testing.T) {
	s := openStore(t)
	commit(t, s, func(tx *state.Tx) error {
		return tx.PutSignalSubscription(&model.SignalSubscriptionValue{
			ElementInstanceKey: model.NewKey(1, 1),
			SignalName:         "cancelled",
		})
	})

	sentinel := errors.New("boom")
	tx := s.NewTransaction()
	defer tx.Close()
	err := tx.SubscribedSignals("cancelled", func(uint64, *model.SignalSubscriptionValue) error {
		return sentinel
	})
	if !errors.Is(err, sentinel) {
		t.Fatalf("SubscribedSignals err = %v, want sentinel", err)
	}
}

// subscribed collects the element-instance keys of every subscription waiting on
// the given signal name.
func subscribed(t *testing.T, s *state.Store, name string) map[uint64]bool {
	t.Helper()
	out := map[uint64]bool{}
	tx := s.NewTransaction()
	defer tx.Close()
	if err := tx.SubscribedSignals(name, func(elKey uint64, v *model.SignalSubscriptionValue) error {
		out[elKey] = true
		return nil
	}); err != nil {
		t.Fatalf("SubscribedSignals: %v", err)
	}
	return out
}
