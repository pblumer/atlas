package state

import "testing"

// Trigger receipts, at the layer that keeps them (ADR-0425). The engine tests the
// fold; coverage is measured per package, and the store's contract is the store's.


// TestATriggerReceiptIsReadBackByItsSenderOnly: a receipt answers for its own source
// and trigger id, a source whose name prefixes another's reads nothing of the other's,
// and a receipt written earlier in the same transaction counts.
func TestATriggerReceiptIsReadBackByItsSenderOnly(t *testing.T) {
	s := openStore(t)
	tx := s.NewTransaction()
	if err := tx.PutTriggerReceipt("hr", "leaver-1", 77, 1000); err != nil {
		t.Fatalf("Put: %v", err)
	}
	if key, ok, err := tx.TriggerReceipt("hr", "leaver-1"); err != nil || !ok || key != 77 {
		t.Fatalf("same transaction: key=%d ok=%v err=%v, want 77", key, ok, err)
	}
	commit(t, tx)

	tx = s.NewTransaction()
	defer tx.Close()
	for _, c := range []struct {
		source, id string
		want       bool
	}{
		{"hr", "leaver-1", true},
		{"hr", "leaver-2", false},
		{"h", "rleaver-1", false},
		{"hr2", "leaver-1", false},
	} {
		_, ok, err := tx.TriggerReceipt(c.source, c.id)
		if err != nil || ok != c.want {
			t.Errorf("TriggerReceipt(%q, %q) = %v (%v), want %v", c.source, c.id, ok, err, c.want)
		}
	}
}

// TestPruningDropsOnlyWhatIsOlderThanTheCutoff: the receipt at the cutoff stays, the
// one before it goes, and the stale check says so beforehand.
func TestPruningDropsOnlyWhatIsOlderThanTheCutoff(t *testing.T) {
	s := openStore(t)
	tx := s.NewTransaction()
	for i, at := range []int64{100, 200, 300} {
		if err := tx.PutTriggerReceipt("hr", string(rune('a'+i)), uint64(i+1), at); err != nil {
			t.Fatalf("Put: %v", err)
		}
	}
	commit(t, tx)

	if stale, err := s.HasTriggerReceiptBefore(100); err != nil || stale {
		t.Fatalf("HasTriggerReceiptBefore(100) = %v (%v), want false", stale, err)
	}
	if stale, err := s.HasTriggerReceiptBefore(250); err != nil || !stale {
		t.Fatalf("HasTriggerReceiptBefore(250) = %v (%v), want true", stale, err)
	}

	tx = s.NewTransaction()
	if err := tx.PruneTriggerReceipts(300); err != nil {
		t.Fatalf("Prune: %v", err)
	}
	commit(t, tx)
	if n, err := s.TriggerReceiptCount(); err != nil || n != 1 {
		t.Fatalf("receipts after prune = %d (%v), want 1", n, err)
	}
	tx = s.NewTransaction()
	defer tx.Close()
	if _, ok, _ := tx.TriggerReceipt("hr", "c"); !ok {
		t.Error("the receipt at the cutoff was dropped")
	}
}

// TestAShortReceiptIsAnErrorNotAnAnswer: a value too short to hold an instance key
// and a time is reported, never read as some instance.
func TestAShortReceiptIsAnErrorNotAnAnswer(t *testing.T) {
	s := openStore(t)
	tx := s.NewTransaction()
	defer tx.Close()
	if err := tx.b.Set(keyTriggerReceipt("hr", "x"), []byte{1, 2, 3}, nil); err != nil {
		t.Fatal(err)
	}
	if _, _, err := tx.TriggerReceipt("hr", "x"); err == nil {
		t.Fatal("a 3-byte receipt read back without an error")
	}
}
