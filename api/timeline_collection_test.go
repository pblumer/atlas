package api_test

import "testing"

// A multi-instance body fills its output collection one element per round (ADR-0077),
// and the log records only the element (ADR-0296). The timeline showed the collection
// as it was seeded — all nulls — in every round, and filled only once the loop had
// promoted it, so a reader stepping through the rounds saw none of them contribute.
// Each round's element is now retained, and the timeline applies it: a round sees the
// collection as the rounds before it left it.
func TestTimelineFillsTheCollectionRoundByRound(t *testing.T) {
	steps := scopedTimeline(t, miCollectModel, `{}`)
	wantOnEntry := map[int]string{1: "[null,null,null]", 2: "[2,null,null]", 3: "[2,4,null]"}
	wantAfter := map[int]string{1: "[2,null,null]", 2: "[2,4,null]"}
	seen := 0
	for _, s := range steps {
		if s.ElementID != "rechne" || s.Iteration == 0 {
			continue
		}
		seen++
		entry, ok := findBodyVar(s.Variables, "ergebnisse")
		if !ok || entry.Value != wantOnEntry[s.Iteration] {
			t.Errorf("round %d on entry: ergebnisse = %+v (found %v), want %s", s.Iteration, entry, ok, wantOnEntry[s.Iteration])
		}
		// It says its elements are on record, so the Console does not explain an
		// unfilled list away as one recorded before they were.
		if ok && !entry.ElementsRecorded {
			t.Errorf("round %d: ergebnisse does not say its elements are recorded", s.Iteration)
		}
		if want, ok := wantAfter[s.Iteration]; ok {
			after, found := findBodyVar(s.VariablesAfter, "ergebnisse")
			if !found || after.Value != want {
				t.Errorf("round %d after: ergebnisse = %+v (found %v), want %s", s.Iteration, after, found, want)
			}
		}
	}
	if seen != 3 {
		t.Fatalf("saw %d rounds, want 3", seen)
	}
}

// findBodyVar finds the multi-instance body's variable of that name, not the process
// variable the loop promotes it to once it is done.
func findBodyVar(vars []scopedVar, name string) (scopedVar, bool) {
	for _, v := range vars {
		if v.Name == name && v.ScopeKind == "multiInstanceBody" {
			return v, true
		}
	}
	return scopedVar{}, false
}
