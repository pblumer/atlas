package state

import (
	"fmt"
	"testing"

	"github.com/pblumer/atlas/model"
)

// BenchmarkStatsAtProductionSize is the evidence behind readStats reading the
// maintained counters (ADR-draft-whole-store-reads-leave-the-writer) rather than
// walking the runtime families for the same answer.
//
// Two populations, one shape: the second is the size a real server reached — ~50.000
// active instances carrying ~200.000 tokens — and the first is a tenth of it, so the
// two show the shape of the curve rather than one point on it.
//
//   - scanned: what the counts used to cost. O(active instances + tokens), so it grows
//     with the engine and roughly tenfolds between the two rows.
//   - counted: the ADR-0080 counters, bounded by how many definitions and elements are
//     *deployed*. Not flat — a bigger store spreads the same counter keys over more
//     levels, so it rises by about half across a tenfold rise in population, against
//     elevenfold for the scan. "Stops tracking how much data exists" is the claim, not
//     "free".
//
// The incident count is in both because it stays a scan either way — the family holds
// one key per stuck token, and no counter can track something that leaves state two
// ways.
//
//	go test ./state -run '^$' -bench 'StatsAtProductionSize' -benchtime=20x
func BenchmarkStatsAtProductionSize(b *testing.B) {
	for _, n := range []struct{ pi, el int }{{5_000, 20_000}, {50_000, 200_000}} {
		s, err := Open(b.TempDir())
		if err != nil {
			b.Fatalf("Open: %v", err)
		}
		tx := s.NewTransaction()
		flush := func(i int) {
			b.Helper()
			if i%2000 != 0 {
				return
			}
			if err := tx.Commit(); err != nil {
				b.Fatalf("Commit: %v", err)
			}
			_ = tx.Close()
			tx = s.NewTransaction()
		}
		// 386 definitions, which is the order a long-lived server accumulates: it is
		// what the counters are bounded by, so it has to be realistic for the
		// comparison to be honest.
		for i := range n.pi {
			defKey := uint64(i%386) + 1
			if err := tx.PutProcessInstance(uint64(1000+i), &model.ProcessInstanceValue{ProcessDefKey: defKey}); err != nil {
				b.Fatalf("PutProcessInstance: %v", err)
			}
			if err := tx.IncDefInstanceCount(defKey); err != nil {
				b.Fatalf("IncDefInstanceCount: %v", err)
			}
			flush(i)
		}
		for i := range n.el {
			defKey := uint64(i%386) + 1
			elementID := int32(i % 18)
			if err := tx.PutElementInstance(uint64(5_000_000+i), &model.ElementInstanceValue{
				ProcessInstanceKey: uint64(1000 + i%n.pi), ProcessDefKey: defKey, ElementId: elementID,
			}); err != nil {
				b.Fatalf("PutElementInstance: %v", err)
			}
			if err := tx.IncElementToken(defKey, elementID); err != nil {
				b.Fatalf("IncElementToken: %v", err)
			}
			flush(i)
		}
		if err := tx.Commit(); err != nil {
			b.Fatalf("Commit: %v", err)
		}
		_ = tx.Close()
		// Merge counters fold on read, so an un-flushed store measures the write
		// backlog rather than the steady state a running engine is in — see
		// BenchmarkTotalActiveInstances, which measures that difference directly.
		if err := s.db.Flush(); err != nil {
			b.Fatalf("Flush: %v", err)
		}

		label := fmt.Sprintf("%dpi_%del", n.pi, n.el)
		b.Run("scanned/"+label, func(b *testing.B) {
			for range b.N {
				if _, err := s.ActiveProcessInstanceCount(); err != nil {
					b.Fatal(err)
				}
				if _, err := s.ActiveElementInstanceCount(); err != nil {
					b.Fatal(err)
				}
				if _, err := s.IncidentCount(); err != nil {
					b.Fatal(err)
				}
			}
		})
		b.Run("counted/"+label, func(b *testing.B) {
			for range b.N {
				if _, err := s.TotalActiveInstances(); err != nil {
					b.Fatal(err)
				}
				if _, err := s.TotalLiveTokens(); err != nil {
					b.Fatal(err)
				}
				if _, err := s.IncidentCount(); err != nil {
					b.Fatal(err)
				}
			}
		})
		_ = s.Close()
	}
}
