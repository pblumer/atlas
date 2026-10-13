package state_test

import (
	"testing"

	"github.com/pblumer/atlas/model"
	"github.com/pblumer/atlas/state"
)

// The buffer hands a subscription the oldest message for its pair that is still
// alive, passing over one that expired before the sweep reached it, and leaves other
// pairs alone (ADR-0370).
func TestTheOldestLiveBufferedMessageIsHandedOut(t *testing.T) {
	s := openStore(t)
	commit(t, s, func(tx *state.Tx) error {
		for _, m := range []struct {
			key uint64
			v   model.MessageValue
		}{
			{1, model.MessageValue{MessageName: "order", CorrelationKey: "42", ExpiresAt: 100}},
			{2, model.MessageValue{MessageName: "order", CorrelationKey: "42", ExpiresAt: 500}},
			{3, model.MessageValue{MessageName: "order", CorrelationKey: "42", ExpiresAt: 900}},
			{4, model.MessageValue{MessageName: "order", CorrelationKey: "7", ExpiresAt: 900}},
		} {
			if err := tx.PutBufferedMessage(m.key, &m.v); err != nil {
				return err
			}
		}
		return nil
	})

	tx := s.NewTransaction()
	defer tx.Close()
	key, v, ok, err := tx.OldestBufferedMessage("order", "42", 200)
	if err != nil || !ok || key != 2 || v.ExpiresAt != 500 {
		t.Fatalf("oldest live for (order, 42) at 200 = key %d %+v ok=%v err=%v, want key 2", key, v, ok, err)
	}
	if _, _, ok, err := tx.OldestBufferedMessage("order", "42", 1000); ok || err != nil {
		t.Fatalf("oldest live at 1000: ok=%v err=%v, want none (all expired)", ok, err)
	}
	if _, _, ok, err := tx.OldestBufferedMessage("invoice", "42", 0); ok || err != nil {
		t.Fatalf("oldest live for another name: ok=%v err=%v, want none", ok, err)
	}
}

// A message without an id is forgotten whole when it is delivered: its expiry entry
// goes with it, so the sweep has nothing left to offer. Expiring one drops what is
// left, and asking about an entry already gone says so (ADR-0370).
func TestAMessageWithoutAnIdLeavesNothingBehind(t *testing.T) {
	s := openStore(t)
	consumed := model.MessageValue{MessageName: "order", CorrelationKey: "1", ExpiresAt: 50}
	waiting := model.MessageValue{MessageName: "order", CorrelationKey: "2", ExpiresAt: 60}
	commit(t, s, func(tx *state.Tx) error {
		if err := tx.PutBufferedMessage(10, &consumed); err != nil {
			return err
		}
		if err := tx.PutBufferedMessage(11, &waiting); err != nil {
			return err
		}
		return tx.ConsumeBufferedMessage(10, &consumed)
	})

	var due []uint64
	if err := s.DueMessageExpiries(100, func(k uint64, _ *model.MessageValue) error {
		due = append(due, k)
		return nil
	}); err != nil {
		t.Fatalf("DueMessageExpiries: %v", err)
	}
	if len(due) != 1 || due[0] != 11 {
		t.Fatalf("due at 100 = %v, want only the message still waiting (11)", due)
	}

	commit(t, s, func(tx *state.Tx) error {
		pending, err := tx.MessageExpiryPending(10, consumed.ExpiresAt)
		if err != nil || pending {
			t.Fatalf("expiry of the consumed message pending=%v err=%v, want gone", pending, err)
		}
		return tx.ExpireMessage(11, &waiting)
	})
	n := 0
	if err := s.BufferedMessages(func(uint64, *model.MessageValue) error { n++; return nil }); err != nil {
		t.Fatalf("BufferedMessages: %v", err)
	}
	if n != 0 {
		t.Fatalf("buffer after consuming one and expiring the other holds %d, want 0", n)
	}
}

// A message with an id is remembered past its delivery: the id stays taken and its
// expiry entry stays due until the sweep expires it, which forgets the id (ADR-0370
// idempotency).
func TestAMessageIdIsRememberedUntilItsMessageExpires(t *testing.T) {
	s := openStore(t)
	m := model.MessageValue{MessageName: "order", CorrelationKey: "42", MessageId: "m-1", ExpiresAt: 70,
		Variables: []model.VariableValue{{Name: "note", Kind: model.VarString, Text: "x", Index: -1}}}
	commit(t, s, func(tx *state.Tx) error {
		if err := tx.PutBufferedMessage(20, &m); err != nil {
			return err
		}
		taken, err := tx.MessageIdTaken("order", "42", "m-1")
		if err != nil || !taken {
			t.Fatalf("id after buffering taken=%v err=%v, want taken", taken, err)
		}
		if other, err := tx.MessageIdTaken("order", "7", "m-1"); err != nil || other {
			t.Fatalf("the same id under another key taken=%v err=%v, want free", other, err)
		}
		return nil
	})
	var held []model.MessageValue
	if err := s.BufferedMessages(func(_ uint64, v *model.MessageValue) error {
		held = append(held, *v)
		return nil
	}); err != nil {
		t.Fatalf("BufferedMessages: %v", err)
	}
	if len(held) != 1 || len(held[0].Variables) != 1 || held[0].MessageId != "m-1" {
		t.Fatalf("buffer = %+v, want the message with its payload and id", held)
	}

	commit(t, s, func(tx *state.Tx) error { return tx.ConsumeBufferedMessage(20, &m) })
	commit(t, s, func(tx *state.Tx) error {
		if taken, err := tx.MessageIdTaken("order", "42", "m-1"); err != nil || !taken {
			t.Fatalf("id after delivery taken=%v err=%v, want still taken", taken, err)
		}
		if pending, err := tx.MessageExpiryPending(20, m.ExpiresAt); err != nil || !pending {
			t.Fatalf("expiry after delivery pending=%v err=%v, want still due", pending, err)
		}
		return tx.ExpireMessage(20, &m)
	})
	commit(t, s, func(tx *state.Tx) error {
		if taken, err := tx.MessageIdTaken("order", "42", "m-1"); err != nil || taken {
			t.Fatalf("id after expiry taken=%v err=%v, want free", taken, err)
		}
		// Recording an id without a payload remembers it the same way.
		bare := model.MessageValue{MessageName: "order", CorrelationKey: "42", MessageId: "m-2", ExpiresAt: 80}
		if err := tx.RecordMessageId(21, &bare); err != nil {
			return err
		}
		if taken, err := tx.MessageIdTaken("order", "42", "m-2"); err != nil || !taken {
			t.Fatalf("recorded id taken=%v err=%v, want taken", taken, err)
		}
		return nil
	})
}
