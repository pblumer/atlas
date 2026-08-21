package api_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
)

// timelineResp is the replay transport as these tests read it: the ordered steps, and
// whether the instance crossed a migration at all.
type timelineResp struct {
	InstanceKey   uint64 `json:"instanceKey"`
	ProcessDefKey uint64 `json:"processDefKey"`
	Version       int32  `json:"version"`
	Migrated      bool   `json:"migrated"`
	Steps         []struct {
		ElementID string `json:"elementId"`
		Type      string `json:"type"`
		Action    string `json:"action"`
		Position  uint64 `json:"position"`
		Migration *struct {
			FromProcessDefKey uint64 `json:"fromProcessDefKey"`
			FromVersion       int32  `json:"fromVersion"`
			ToProcessDefKey   uint64 `json:"toProcessDefKey"`
			ToVersion         int32  `json:"toVersion"`
			Actor             string `json:"actor"`
			Reason            string `json:"reason"`
		} `json:"migration"`
	} `json:"steps"`
}

func readTimeline(t *testing.T, ts *httptest.Server, key uint64) timelineResp {
	t.Helper()
	code, body := doReq(t, ts, http.MethodGet, fmt.Sprintf("/api/v1/instances/%d/timeline", key), "", "")
	if code != http.StatusOK {
		t.Fatalf("timeline: status=%d body=%s", code, body)
	}
	var tl timelineResp
	if err := json.Unmarshal(body, &tl); err != nil {
		t.Fatalf("decode timeline: %v (%s)", err, body)
	}
	return tl
}

// elementSteps is the timeline's ordinary element activations, in order — the migration
// blocks filtered out.
func elementSteps(tl timelineResp) []string {
	var ids []string
	for _, s := range tl.Steps {
		if s.Action != "migrate" {
			ids = append(ids, s.ElementID)
		}
	}
	return ids
}

// TestTimelineReadsStepsUnderTheVersionTheyRanOn is the whole point of slice 3
// (ADR-0162). v1's "review" is element index 1; in v2 index 1 is "triage", a task this
// instance never touched. The steps recorded before the migration name their elements by
// index, so reading them back through the version the instance is on *now* would report
// that the token had been on "triage" — a step that never happened, on an element that
// did not exist when it supposedly ran.
func TestTimelineReadsStepsUnderTheVersionTheyRanOn(t *testing.T) {
	ts := newTestServer(t)
	v1 := deployXML(t, ts, migrateV1BPMN)
	key := startInstance(t, ts, v1)

	// Before: the instance is on v1 and its steps name v1's elements.
	before := elementSteps(readTimeline(t, ts, key))
	if len(before) < 2 || before[0] != "start" || before[1] != "review" {
		t.Fatalf("pre-migration steps = %v, want start then review", before)
	}

	v2 := deployXML(t, ts, migrateV2BPMN)
	code, _, raw := migrateCall(t, ts, fmt.Sprintf("/api/v1/instances/%d/migrate", key),
		fmt.Sprintf(`{"targetProcessDefKey":%d,"reason":"model fix"}`, v2))
	if code != http.StatusOK {
		t.Fatalf("migrate: status=%d body=%s", code, raw)
	}

	tl := readTimeline(t, ts, key)
	if !tl.Migrated {
		t.Error("timeline does not report the instance as migrated")
	}
	if tl.ProcessDefKey != v2 {
		t.Errorf("timeline processDefKey = %d, want the target %d", tl.ProcessDefKey, v2)
	}
	got := elementSteps(tl)
	if len(got) != len(before) {
		t.Fatalf("migration changed the step count: %v then %v", before, got)
	}
	for i := range before {
		if got[i] != before[i] {
			t.Errorf("step %d names %q after the migration, %q before it — "+
				"a step recorded on v1 must keep naming the element it ran on", i, got[i], before[i])
		}
	}
	// The specific misreading this guards against: v2's index 1 is "triage".
	for _, id := range got {
		if id == "triage" {
			t.Error(`a step names "triage", an element of v2 that this instance never reached — ` +
				`the pre-migration indices are being read through the target version`)
		}
	}
}

// TestTimelineShowsTheMigrationAsAStep is the other half: the steps on either side of a
// migration name elements of different versions, and without the boundary in the
// sequence nothing on the replay says why.
func TestTimelineShowsTheMigrationAsAStep(t *testing.T) {
	ts := newTestServer(t)
	v1 := deployXML(t, ts, migrateV1BPMN)
	key := startInstance(t, ts, v1)
	v2 := deployXML(t, ts, migrateV2BPMN)

	if code, _, raw := migrateCall(t, ts, fmt.Sprintf("/api/v1/instances/%d/migrate", key),
		fmt.Sprintf(`{"targetProcessDefKey":%d,"reason":"the tax rate was wrong"}`, v2)); code != http.StatusOK {
		t.Fatalf("migrate: status=%d body=%s", code, raw)
	}

	tl := readTimeline(t, ts, key)
	var blocks, seen int
	var lastElementPos uint64
	for _, s := range tl.Steps {
		if s.Action != "migrate" {
			seen++
			lastElementPos = s.Position
			continue
		}
		blocks++
		if s.Migration == nil {
			t.Fatal(`a step with action "migrate" carries no migration block`)
		}
		m := s.Migration
		if m.FromProcessDefKey != v1 || m.ToProcessDefKey != v2 {
			t.Errorf("migration block says %d→%d, want %d→%d", m.FromProcessDefKey, m.ToProcessDefKey, v1, v2)
		}
		if m.FromVersion != 1 || m.ToVersion != 2 {
			t.Errorf("migration block says version %d→%d, want 1→2", m.FromVersion, m.ToVersion)
		}
		if m.Reason != "the tax rate was wrong" {
			t.Errorf("migration reason = %q, want the one the operator gave", m.Reason)
		}
		// It belongs *after* the steps that ran before it: the ordering is what makes it
		// legible as a boundary rather than a footnote.
		if s.Position <= lastElementPos {
			t.Errorf("migration block at position %d is not after the last preceding step at %d",
				s.Position, lastElementPos)
		}
		if seen == 0 {
			t.Error("the migration block leads the timeline; the steps that ran before it should")
		}
	}
	if blocks != 1 {
		t.Errorf("timeline carries %d migration blocks, want exactly 1", blocks)
	}
}

// TestUnmigratedTimelineCarriesNoMigrationBlock keeps the addition from leaking into the
// ordinary case: an instance that never moved should read exactly as it did before.
func TestUnmigratedTimelineCarriesNoMigrationBlock(t *testing.T) {
	ts := newTestServer(t)
	v1 := deployXML(t, ts, migrateV1BPMN)
	key := startInstance(t, ts, v1)

	tl := readTimeline(t, ts, key)
	if tl.Migrated {
		t.Error("an instance that never migrated reports migrated=true")
	}
	for _, s := range tl.Steps {
		if s.Action == "migrate" || s.Migration != nil {
			t.Errorf("unmigrated timeline carries a migration step: %+v", s)
		}
		if s.ElementID == "" {
			t.Error("a step on a deployed definition has no element id")
		}
	}
}

// TestTimelineNamesEveryStepAcrossTwoMigrations walks the chain rather than one hop: the
// definition in force between two migrations is named by neither the instance record nor
// the first migration — it is the *second* migration's source. A reader that took the
// instance's current version for everything after the first hop would mislabel the
// middle span, which is exactly the span a two-hop chain exists to test.
func TestTimelineNamesEveryStepAcrossTwoMigrations(t *testing.T) {
	ts := newTestServer(t)
	v1 := deployXML(t, ts, migrateV1BPMN)
	key := startInstance(t, ts, v1)
	before := elementSteps(readTimeline(t, ts, key))

	v2 := deployXML(t, ts, migrateV2BPMN)
	if code, _, raw := migrateCall(t, ts, fmt.Sprintf("/api/v1/instances/%d/migrate", key),
		fmt.Sprintf(`{"targetProcessDefKey":%d,"reason":"first"}`, v2)); code != http.StatusOK {
		t.Fatalf("migrate to v2: status=%d body=%s", code, raw)
	}
	v3 := deployXML(t, ts, migrateV3BPMN)
	if code, _, raw := migrateCall(t, ts, fmt.Sprintf("/api/v1/instances/%d/migrate", key),
		fmt.Sprintf(`{"targetProcessDefKey":%d,"reason":"second"}`, v3)); code != http.StatusOK {
		t.Fatalf("migrate to v3: status=%d body=%s", code, raw)
	}

	tl := readTimeline(t, ts, key)
	if got := elementSteps(tl); len(got) != len(before) {
		t.Fatalf("step count changed across two migrations: %v then %v", before, got)
	} else {
		for i := range before {
			if got[i] != before[i] {
				t.Errorf("step %d names %q after two migrations, %q before them", i, got[i], before[i])
			}
		}
	}

	// Both hops are on the timeline, in order, and their def keys chain: the first
	// migration's target is the second's source.
	var hops []*struct {
		FromProcessDefKey uint64 `json:"fromProcessDefKey"`
		FromVersion       int32  `json:"fromVersion"`
		ToProcessDefKey   uint64 `json:"toProcessDefKey"`
		ToVersion         int32  `json:"toVersion"`
		Actor             string `json:"actor"`
		Reason            string `json:"reason"`
	}
	for _, s := range tl.Steps {
		if s.Migration != nil {
			hops = append(hops, s.Migration)
		}
	}
	if len(hops) != 2 {
		t.Fatalf("timeline carries %d migration blocks, want 2", len(hops))
	}
	if hops[0].FromProcessDefKey != v1 || hops[0].ToProcessDefKey != v2 {
		t.Errorf("first hop says %d→%d, want %d→%d", hops[0].FromProcessDefKey, hops[0].ToProcessDefKey, v1, v2)
	}
	if hops[1].FromProcessDefKey != v2 || hops[1].ToProcessDefKey != v3 {
		t.Errorf("second hop says %d→%d, want %d→%d", hops[1].FromProcessDefKey, hops[1].ToProcessDefKey, v2, v3)
	}
	if hops[0].Reason != "first" || hops[1].Reason != "second" {
		t.Errorf("hops out of order: %q then %q", hops[0].Reason, hops[1].Reason)
	}
}
