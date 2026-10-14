package state

import (
	"testing"

	"github.com/cockroachdb/pebble"
	"github.com/pblumer/atlas/model"
)

// A store that held waiting messages before it counted them is counted once on open,
// so the gauge does not read zero for what already waits and then drift negative as
// it is delivered (ADR-0370, the ADR-0142 backfill's shape).
func TestABufferThatPredatesItsCounterIsCountedOnOpen(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(dir)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	tx := s.NewTransaction()
	for i, name := range []string{"order", "invoice"} {
		m := model.MessageValue{MessageName: name, ExpiresAt: 10}
		if err := tx.PutBufferedMessage(uint64(i+1), &m); err != nil {
			t.Fatalf("PutBufferedMessage: %v", err)
		}
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("Commit: %v", err)
	}
	tx.Close()
	// Take the store back to before the counter: the messages, and no count or marker.
	if err := s.db.Delete(keyRuntimeTotal(rtBufferedMessages), pebble.Sync); err != nil {
		t.Fatalf("delete counter: %v", err)
	}
	if err := s.db.Delete(keyMeta(metaBufferedMessagesV1), pebble.Sync); err != nil {
		t.Fatalf("delete marker: %v", err)
	}
	if err := s.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	s, err = Open(dir)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	if n, err := s.BufferedMessageCount(); err != nil || n != 2 {
		t.Fatalf("count after reopening = %d (err %v), want the 2 waiting messages", n, err)
	}
	// Seeded once: a second open does not count them again.
	if err := s.Close(); err != nil {
		t.Fatalf("Close 2: %v", err)
	}
	s2, err := Open(dir)
	if err != nil {
		t.Fatalf("reopen 2: %v", err)
	}
	defer s2.Close()
	if n, _ := s2.BufferedMessageCount(); n != 2 {
		t.Fatalf("count after a second open = %d, want still 2", n)
	}
}
