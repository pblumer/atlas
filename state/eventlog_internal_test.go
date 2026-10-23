package state

import (
	"testing"

	"github.com/pblumer/atlas/model"
)

func readEventLog(t *testing.T, s *Store, partition uint16, after uint64) []EventLogEntry {
	t.Helper()
	var out []EventLogEntry
	must(t, s.EventLogAfter(partition, after, func(e EventLogEntry) error {
		out = append(out, e)
		return nil
	}))
	return out
}

func readEventLogBefore(t *testing.T, s *Store, partition uint16, before uint64) []EventLogEntry {
	t.Helper()
	var out []EventLogEntry
	must(t, s.EventLogBefore(partition, before, func(e EventLogEntry) error {
		out = append(out, e)
		return nil
	}))
	return out
}

// TestTheEventLogHoldsEveryKindInPositionOrder: each kind of row comes back with its
// time, its cause and its value, in position order from a cursor and newest first
// before one, one partition's never among another's; a row cannot carry a value its
// kind does not name, nor a message's payload (ADR-0472 §3).
func TestTheEventLogHoldsEveryKindInPositionOrder(t *testing.T) {
	s := openStore(t)
	tx := s.NewTransaction()
	sent := &model.DispatchValue{Channel: model.DispatchSignal, Source: model.DispatchFromProcess, Name: "go", Reached: 1}
	sig := &model.SignalSubscriptionValue{ProcessInstanceKey: 4, SignalName: "go", ProcessDefKey: 2, SenderProcessInstanceKey: 3}
	flow := &model.MessageFlowValue{SenderProcessInstanceKey: 3, ReceiverProcessInstanceKey: 5, ReceiverProcessDefKey: 2, MessageName: "m", CorrelationKey: "k"}
	kept := &model.MessageValue{MessageName: "m", CorrelationKey: "k", ExpiresAt: 99}
	must(t, tx.PutEventLogRow(1, 10, 100, 9, EventLogSignalReceived, sig))
	must(t, tx.PutEventLogRow(1, 11, 100, 9, EventLogSignalStarted, sig))
	must(t, tx.PutEventLogRow(1, 12, 100, 9, EventLogDispatched, sent))
	must(t, tx.PutEventLogRow(1, 13, 200, 13, EventLogMessageDelivered, flow))
	must(t, tx.PutEventLogRow(1, 14, 300, 14, EventLogMessageKept, kept))
	must(t, tx.PutEventLogRow(1, 15, 400, 15, EventLogMessageTaken, kept))
	must(t, tx.PutEventLogRow(1, 16, 500, 16, EventLogMessageExpired, kept))
	must(t, tx.PutEventLogRow(2, 1, 50, 1, EventLogDispatched, sent))
	if err := tx.PutEventLogRow(1, 20, 600, 20, EventLogDispatched, sig); err == nil {
		t.Fatal("a send row carrying a signal subscription was written")
	}
	if err := tx.PutEventLogRow(1, 20, 600, 20, EventLogKind(99), sent); err == nil {
		t.Fatal("a row of an unknown kind was written")
	}
	payload := &model.MessageValue{MessageName: "m", Variables: []model.VariableValue{{Name: "secret", Kind: model.VarString, Text: "x"}}}
	if err := tx.PutEventLogRow(1, 20, 600, 20, EventLogMessageKept, payload); err == nil {
		t.Fatal("a row carrying a message's payload was written")
	}
	commit(t, tx)

	rows := readEventLog(t, s, 1, 0)
	if len(rows) != 7 {
		t.Fatalf("partition 1 = %d rows, want 7", len(rows))
	}
	r := rows
	if r[0].Signal == nil || r[0].Signal.SenderProcessInstanceKey != 3 || r[0].Cause != 9 || r[0].At != 100 ||
		r[1].Kind != EventLogSignalStarted || r[2].Dispatch == nil || r[2].Dispatch.Reached != 1 ||
		r[3].Flow == nil || r[3].Flow.CorrelationKey != "k" || r[4].Message == nil || r[4].Message.ExpiresAt != 99 ||
		r[5].Kind != EventLogMessageTaken || r[6].Kind != EventLogMessageExpired || r[6].Position != 16 {
		t.Fatalf("rows decoded as %+v", rows)
	}
	if after := readEventLog(t, s, 1, 14); len(after) != 2 || after[0].Position != 15 {
		t.Fatalf("after 14 = %+v", after)
	}
	if none := readEventLog(t, s, 1, ^uint64(0)); len(none) != 0 {
		t.Fatalf("after the last position = %+v", none)
	}
	if newest := readEventLogBefore(t, s, 1, 0); len(newest) != 7 || newest[0].Position != 16 || newest[6].Position != 10 {
		t.Fatalf("newest first = %+v", newest)
	}
	if older := readEventLogBefore(t, s, 1, 12); len(older) != 2 || older[0].Position != 11 {
		t.Fatalf("before 12 = %+v", older)
	}
	if other := readEventLog(t, s, 2, 0); len(other) != 1 || other[0].Dispatch == nil {
		t.Fatalf("partition 2 = %+v", other)
	}
}

// TestTheEventLogIsCutByItsRetention: the sweep's cut stops at the first row recorded
// at or after the cutoff; a prune drops through its position and remembers the cut,
// and a lower cut later changes nothing (ADR-0472 §4).
func TestTheEventLogIsCutByItsRetention(t *testing.T) {
	s := openStore(t)
	sent := &model.DispatchValue{Channel: model.DispatchMessage, Source: model.DispatchFromPublish, Name: "m"}
	tx := s.NewTransaction()
	for i, at := range []int64{100, 200, 300} {
		must(t, tx.PutEventLogRow(1, uint64(i+1), at, uint64(i+1), EventLogDispatched, sent))
	}
	commit(t, tx)

	if through, found, err := s.EventLogThroughBefore(1, 250); err != nil || !found || through != 2 {
		t.Fatalf("through before 250 = %d %v %v, want 2", through, found, err)
	}
	if _, found, err := s.EventLogThroughBefore(1, 50); err != nil || found {
		t.Fatalf("nothing is older than 50, yet found=%v err=%v", found, err)
	}
	if through, err := s.EventLogPrunedThrough(1); err != nil || through != 0 {
		t.Fatalf("never pruned, yet through = %d %v", through, err)
	}

	tx = s.NewTransaction()
	must(t, tx.PruneEventLog(1, 2))
	commit(t, tx)
	if rows := readEventLog(t, s, 1, 0); len(rows) != 1 || rows[0].Position != 3 {
		t.Fatalf("after the prune = %+v", rows)
	}
	if through, err := s.EventLogPrunedThrough(1); err != nil || through != 2 {
		t.Fatalf("pruned through = %d %v", through, err)
	}
	tx = s.NewTransaction()
	must(t, tx.PruneEventLog(1, 1))
	commit(t, tx)
	if through, _ := s.EventLogPrunedThrough(1); through != 2 {
		t.Fatalf("a lower cut moved the mark to %d", through)
	}
	if rows := readEventLog(t, s, 1, 0); len(rows) != 1 {
		t.Fatalf("a lower cut dropped rows: %+v", rows)
	}
}

// TestABufferedMessageWaitsUntilItIsTaken: the expiry folds a row only for a message
// that still waits, and this is how it asks.
func TestABufferedMessageWaitsUntilItIsTaken(t *testing.T) {
	s := openStore(t)
	msg := &model.MessageValue{MessageName: "m", CorrelationKey: "k", ExpiresAt: 99}
	tx := s.NewTransaction()
	if waits, err := tx.BufferedMessageWaits(7, msg); err != nil || waits {
		t.Fatalf("before it was kept: waits=%v err=%v", waits, err)
	}
	must(t, tx.PutBufferedMessage(7, msg))
	if waits, err := tx.BufferedMessageWaits(7, msg); err != nil || !waits {
		t.Fatalf("once kept: waits=%v err=%v", waits, err)
	}
	must(t, tx.ConsumeBufferedMessage(7, msg))
	if waits, err := tx.BufferedMessageWaits(7, msg); err != nil || waits {
		t.Fatalf("once taken: waits=%v err=%v", waits, err)
	}
	commit(t, tx)
}

// TestACutShortEventLogRowIsAnError: a row too short for its head, or of a kind the
// table does not know, is refused on read rather than read as something it is not.
func TestACutShortEventLogRowIsAnError(t *testing.T) {
	key := keyEventLogRow(1, 5)
	if _, err := decodeEventLogRow(1, key, []byte{byte(EventLogDispatched)}); err == nil {
		t.Error("a row cut short was decoded")
	}
	raw := append([]byte{99}, make([]byte, 16)...)
	if _, err := decodeEventLogRow(1, key, raw); err == nil {
		t.Error("a row of an unknown kind was decoded")
	}
	raw = append([]byte{byte(EventLogDispatched)}, make([]byte, 16)...)
	if _, err := decodeEventLogRow(1, key, raw); err == nil {
		t.Error("a send row without its value was decoded")
	}

	// A reader in either direction stops at such a row and says so.
	s := openStore(t)
	tx := s.NewTransaction()
	must(t, tx.b.Set(key, []byte{byte(EventLogDispatched)}, nil))
	commit(t, tx)
	noRow := func(EventLogEntry) error { t.Error("a cut-short row reached the reader"); return nil }
	if err := s.EventLogAfter(1, 0, noRow); err == nil {
		t.Error("reading forward over a cut-short row was no error")
	}
	if err := s.EventLogBefore(1, 0, noRow); err == nil {
		t.Error("reading backward over a cut-short row was no error")
	}
}
