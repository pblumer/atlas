package state

import (
	"bytes"
	"testing"

	"github.com/cockroachdb/pebble"
	"github.com/pblumer/atlas/model"
)

// miRangeKeys lists every key of the MI families with its value.
func miRangeKeys(t *testing.T, s *Store) map[string]string {
	t.Helper()
	out := map[string]string{}
	if err := s.scanRange(miRangeLo, miRangeHi, func(k, v []byte) error {
		out[string(k)] = string(v)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	return out
}

// rebuiltPair is a shadow store with a small MI history and a live store at the
// same applied position whose MI families hold a partial projection.
func rebuiltPair(t *testing.T) (src, live *miFixture) {
	t.Helper()
	src = newMIFixture(t, 40)
	src.body(10, 40, 1)
	for i := 1; i <= 7; i++ {
		src.round(10+uint64(i), 10, 1+uint64(i), i)
	}
	src.must(src.tx.RecordMIIncidentRaised(&model.IncidentValue{ProcessInstanceKey: 40, ElementInstanceKey: 12}, 20))
	src.must(src.tx.SetLastAppliedPosition(30))
	src.commit()
	live = newMIFixture(t, 40)
	live.body(99, 40, 25) // a partial projection the rebuild must supersede
	live.must(live.tx.SetLastAppliedPosition(30))
	live.commit()
	live.must(live.s.db.Delete(keyMeta(metaMIHistoryIndex), pebble.Sync))
	live.must(live.s.db.Delete(keyMeta(metaMIDecisionIndex), pebble.Sync))
	return src, live
}

func TestAdoptMIHistoryReplacesThePartialProjection(t *testing.T) {
	src, live := rebuiltPair(t)
	n, err := live.s.AdoptMIHistory(src.s, 3)
	if err != nil {
		t.Fatal(err)
	}
	want := miRangeKeys(t, src.s)
	if n != len(want) {
		t.Fatalf("copied %d keys, source has %d", n, len(want))
	}
	got := miRangeKeys(t, live.s)
	if len(got) != len(want) {
		t.Fatalf("live has %d MI keys, want %d", len(got), len(want))
	}
	for k, v := range want {
		if got[k] != v {
			t.Fatalf("key %x: %q, want %q", k, got[k], v)
		}
	}
	if needed, err := live.s.MIHistoryRebuildNeeded(); err != nil || needed {
		t.Fatalf("not complete after adoption: %v %v", needed, err)
	}
	if _, ok, _ := getCopy(live.s.db, keyMeta(metaMIRebuildProgress)); ok {
		t.Fatal("progress marker left behind")
	}
	if _, ok, err := live.s.MIBody(40, 99); err != nil || ok {
		t.Fatalf("partial projection survived: %v %v", ok, err)
	}
	b, ok, err := live.s.MIBody(40, 10)
	if err != nil || !ok || b.Iterations != 7 || b.IncidentsRaised != 1 {
		t.Fatalf("adopted body: %+v %v %v", b, ok, err)
	}
}

// A copy interrupted after some durable batches resumes after the last key it
// recorded, and ends with the same keys as an uninterrupted one.
func TestAdoptMIHistoryResumesAnInterruptedCopy(t *testing.T) {
	src, live := rebuiltPair(t)
	want := miRangeKeys(t, src.s)
	var keys []string
	for k := range want {
		keys = append(keys, k)
	}
	// Simulate the state a crash leaves: the range cleared, the first keys
	// copied, the marker naming the last of them, no readiness markers.
	sorted := sortedKeys(keys)
	b := live.s.db.NewBatch()
	live.must(b.DeleteRange(miRangeLo, miRangeHi, nil))
	for _, k := range sorted[:4] {
		live.must(b.Set([]byte(k), []byte(want[k]), nil))
	}
	live.must(b.Set(keyMeta(metaMIRebuildProgress), []byte(sorted[3]), nil))
	live.must(b.Commit(pebble.Sync))
	_ = b.Close()
	if needed, _ := live.s.MIHistoryRebuildNeeded(); !needed {
		t.Fatal("an interrupted copy reads as complete")
	}
	n, err := live.s.AdoptMIHistory(src.s, 2)
	if err != nil {
		t.Fatal(err)
	}
	if n != len(want)-4 {
		t.Fatalf("resumed copy wrote %d keys, want %d", n, len(want)-4)
	}
	got := miRangeKeys(t, live.s)
	for k, v := range want {
		if got[k] != v {
			t.Fatalf("key %x after resume: %q, want %q", k, got[k], v)
		}
	}
	if len(got) != len(want) {
		t.Fatalf("live has %d keys after resume, want %d", len(got), len(want))
	}
}

func sortedKeys(keys []string) []string {
	out := append([]string(nil), keys...)
	for i := 1; i < len(out); i++ {
		for j := i; j > 0 && bytes.Compare([]byte(out[j]), []byte(out[j-1])) < 0; j-- {
			out[j], out[j-1] = out[j-1], out[j]
		}
	}
	return out
}

func TestAdoptMIHistoryRefusesWhatItCannotProve(t *testing.T) {
	src, live := rebuiltPair(t)
	if _, err := live.s.AdoptMIHistory(src.s, 0); err == nil {
		t.Fatal("a zero batch was accepted")
	}
	live.must(live.tx.SetLastAppliedPosition(31))
	live.commit()
	if _, err := live.s.AdoptMIHistory(src.s, 10); err == nil {
		t.Fatal("a shadow store at another position was adopted")
	}
	src.must(src.s.db.Delete(keyMeta(metaMIDecisionIndex), pebble.Sync))
	if _, err := live.s.AdoptMIHistory(src.s, 10); err == nil {
		t.Fatal("an incomplete shadow store was adopted")
	}
}

// Every MI family lies in the one range the swap clears and copies, and nothing
// else does.
func TestMIFamiliesAreOneContiguousRange(t *testing.T) {
	in := map[byte]bool{}
	for _, f := range miIndexFamilies {
		if byte(f) < miRangeLo[0] || byte(f) >= miRangeHi[0] {
			t.Fatalf("family %#x outside the MI range", f)
		}
		in[byte(f)] = true
	}
	for b := miRangeLo[0]; b < miRangeHi[0]; b++ {
		if !in[b] {
			t.Fatalf("byte %#x inside the MI range is not an MI family", b)
		}
	}
}
