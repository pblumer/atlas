package state

import (
	"fmt"
	"testing"

	"github.com/pblumer/atlas/model"
)

// ADR-0142 slice 3 needs runtime counts a Prometheus scrape can take every 15 seconds.
// The counts themselves already exist per definition as maintained merge counters
// (ADR-0080); what was missing is a way to read the *total* without walking the runtime
// set, which is the cost ADR-0080 and ADR-0085 exist to avoid.
//
// These sums are O(definitions) and O(definition-elements) — bounded by what has been
// *deployed*, not by what is *running*. The tests below pin both halves of that: the sum
// agrees with the scan that is ground truth, and it keeps agreeing as instances come and
// go.

// TestTotalActiveInstancesMatchesTheScan: the maintained sum and the authoritative scan
// must agree, or the cheap read is simply wrong. They are computed from different data
// — one from the merge counters, one from the instance records themselves — so agreement
// is a real check rather than a tautology.
func TestTotalActiveInstancesMatchesTheScan(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer func() { _ = s.Close() }()

	// Three definitions with differing populations, so a sum that dropped or
	// double-counted a definition would not accidentally match.
	populations := map[uint64]int{7: 3, 9: 1, 11: 5}
	key := uint64(1000)
	tx := s.NewTransaction()
	for defKey, n := range populations {
		for i := 0; i < n; i++ {
			key++
			if err := tx.PutProcessInstance(key, &model.ProcessInstanceValue{ProcessDefKey: defKey}); err != nil {
				t.Fatalf("PutProcessInstance: %v", err)
			}
			if err := tx.IncDefInstanceCount(defKey); err != nil {
				t.Fatalf("IncDefInstanceCount: %v", err)
			}
		}
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("Commit: %v", err)
	}
	_ = tx.Close()

	scanned, err := s.ActiveProcessInstanceCount()
	if err != nil {
		t.Fatalf("ActiveProcessInstanceCount: %v", err)
	}
	summed, err := s.TotalActiveInstances()
	if err != nil {
		t.Fatalf("TotalActiveInstances: %v", err)
	}
	if scanned != 9 {
		t.Fatalf("the fixture put %d instances, want 9", scanned)
	}
	if int(summed) != scanned {
		t.Fatalf("maintained sum = %d, scan = %d — the cheap read disagrees with ground truth", summed, scanned)
	}
}

// TestTotalActiveInstancesFollowsCompletions: the sum tracks instances leaving, not just
// arriving. A gauge that only climbs is worse than none — it would show a healthy engine
// as an ever-growing backlog.
func TestTotalActiveInstancesFollowsCompletions(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer func() { _ = s.Close() }()

	tx := s.NewTransaction()
	for i := 0; i < 4; i++ {
		if err := tx.IncDefInstanceCount(7); err != nil {
			t.Fatalf("IncDefInstanceCount: %v", err)
		}
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("Commit: %v", err)
	}
	_ = tx.Close()
	if got, _ := s.TotalActiveInstances(); got != 4 {
		t.Fatalf("after 4 starts: %d, want 4", got)
	}

	tx = s.NewTransaction()
	for i := 0; i < 3; i++ {
		if err := tx.DecDefInstanceCount(7); err != nil {
			t.Fatalf("DecDefInstanceCount: %v", err)
		}
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("Commit: %v", err)
	}
	_ = tx.Close()
	if got, _ := s.TotalActiveInstances(); got != 1 {
		t.Fatalf("after 3 completions: %d, want 1", got)
	}
}

// TestTotalLiveTokensSumsEveryDefinition: live tokens are keyed per definition *and*
// element, so the total has to span both dimensions — summing only one definition's
// elements, or only one element per definition, is the easy mistake.
func TestTotalLiveTokensSumsEveryDefinition(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer func() { _ = s.Close() }()

	tx := s.NewTransaction()
	// def 7: elements 1 and 2; def 9: element 1. Distinct amounts everywhere.
	// def 7 element 1 ×2, def 7 element 2 ×3, def 9 element 1 ×4.
	for _, tok := range []struct {
		def     uint64
		element int32
		n       int
	}{{7, 1, 2}, {7, 2, 3}, {9, 1, 4}} {
		for i := 0; i < tok.n; i++ {
			if err := tx.IncElementToken(tok.def, tok.element); err != nil {
				t.Fatalf("IncElementToken: %v", err)
			}
		}
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("Commit: %v", err)
	}
	_ = tx.Close()

	got, err := s.TotalLiveTokens()
	if err != nil {
		t.Fatalf("TotalLiveTokens: %v", err)
	}
	if got != 9 {
		t.Fatalf("live tokens = %d, want 9 (2+3 on def 7, 4 on def 9)", got)
	}
}

// TestRuntimeTotalsAreEmptyOnAFreshStore: a store that has run nothing reports zero
// rather than failing — the state every scrape of a just-started server sees.
func TestRuntimeTotalsAreEmptyOnAFreshStore(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer func() { _ = s.Close() }()

	if got, err := s.TotalActiveInstances(); err != nil || got != 0 {
		t.Errorf("TotalActiveInstances on a fresh store = %d, %v; want 0, nil", got, err)
	}
	if got, err := s.TotalLiveTokens(); err != nil || got != 0 {
		t.Errorf("TotalLiveTokens on a fresh store = %d, %v; want 0, nil", got, err)
	}
}

// BenchmarkTotalActiveInstances is the evidence behind TotalActiveInstances's cost
// claim, including the part that is easy to get wrong. Three variants over the same data
// at two population sizes:
//
//   - summed: the maintained sum straight after the writes. These are *merge* counters,
//     so this also folds in every operand Pebble has not compacted yet — it grows with
//     recent writes, not with definitions. Measuring it is the point: the naive claim
//     "O(definitions)" is false in this state.
//
//   - summed-flushed: the same sum after a flush collapses the operands. Flat — 2,000
//     instances read as fast as 100 — which is the steady-state cost a scrape pays, since
//     flushes happen on their own and the ADR-0131 checkpoint cadence forces one every
//     few minutes.
//
//   - scanned: the authoritative count, which walks the runtime set and grows with it.
//
//     go test ./state -run '^$' -bench 'ActiveInstances' -benchtime=200x
func BenchmarkTotalActiveInstances(b *testing.B) {
	for _, instances := range []int{100, 2000} {
		s, err := Open(b.TempDir())
		if err != nil {
			b.Fatalf("Open: %v", err)
		}
		tx := s.NewTransaction()
		for i := 0; i < instances; i++ {
			defKey := uint64(i%4) + 7 // four definitions, however many instances
			if err := tx.PutProcessInstance(uint64(1000+i), &model.ProcessInstanceValue{ProcessDefKey: defKey}); err != nil {
				b.Fatalf("PutProcessInstance: %v", err)
			}
			if err := tx.IncDefInstanceCount(defKey); err != nil {
				b.Fatalf("IncDefInstanceCount: %v", err)
			}
		}
		if err := tx.Commit(); err != nil {
			b.Fatalf("Commit: %v", err)
		}
		_ = tx.Close()

		b.Run(fmt.Sprintf("summed/%d", instances), func(b *testing.B) {
			for range b.N {
				if _, err := s.TotalActiveInstances(); err != nil {
					b.Fatalf("TotalActiveInstances: %v", err)
				}
			}
		})
		if err := s.db.Flush(); err != nil {
			b.Fatalf("Flush: %v", err)
		}
		b.Run(fmt.Sprintf("summed-flushed/%d", instances), func(b *testing.B) {
			for range b.N {
				if _, err := s.TotalActiveInstances(); err != nil {
					b.Fatalf("TotalActiveInstances: %v", err)
				}
			}
		})
		b.Run(fmt.Sprintf("scanned/%d", instances), func(b *testing.B) {
			for range b.N {
				if _, err := s.ActiveProcessInstanceCount(); err != nil {
					b.Fatalf("ActiveProcessInstanceCount: %v", err)
				}
			}
		})
		_ = s.Close()
	}
}
