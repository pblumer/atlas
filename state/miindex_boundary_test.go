package state

import (
	"testing"

	"github.com/cockroachdb/pebble"
	"github.com/pblumer/atlas/model"
)

// TestMIHistoryBoundsAndLegacyParent guards the two dangerous interpretations
// of a read: scanning beyond the requested page, and attributing a round from
// an older store to a body whose activation is not indexed.
func TestMIHistoryBoundsAndLegacyParent(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	body := model.ElementInstanceValue{
		ProcessInstanceKey: 70, ProcessDefKey: 3, MultiInstance: 1,
	}
	round := body
	round.MultiInstance = 2
	tx := s.NewTransaction()
	round.FlowScopeKey = 999 // missing body activation after an upgrade
	if err := tx.RecordMIActivation(901, &round, 2); err != nil {
		t.Fatal(err)
	}
	for i := uint64(0); i < 3; i++ {
		bodyKey := uint64(100 + i*10)
		pos := 10 + i*10
		if err := tx.RecordMIActivation(bodyKey, &body, pos); err != nil {
			t.Fatal(err)
		}
		round.FlowScopeKey = bodyKey
		if err := tx.RecordMIActivation(bodyKey+1, &round, pos+1); err != nil {
			t.Fatal(err)
		}
		if err := tx.RecordMIActivation(bodyKey+2, &round, pos+2); err != nil {
			t.Fatal(err)
		}
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	_ = tx.Close()

	if _, ok, err := s.MIIteration(70, 901); err != nil || ok {
		t.Fatalf("legacy round acquired invented parent: ok=%v err=%v", ok, err)
	}
	if _, ok, err := s.MIBody(70, 999); err != nil || ok {
		t.Fatalf("missing body lookup: ok=%v err=%v", ok, err)
	}
	if _, ok, err := s.MIIteration(70, 999); err != nil || ok {
		t.Fatalf("missing iteration lookup: ok=%v err=%v", ok, err)
	}

	for _, limit := range []int{0, -1, 201} {
		if err := s.MIBodies(70, limit, func(MIHistoryEntry) error { return nil }); err == nil {
			t.Fatalf("accepted body page limit %d", limit)
		}
		if err := s.MIIterations(70, 100, 10, limit, func(MIHistoryEntry) error { return nil }); err == nil {
			t.Fatalf("accepted round page limit %d", limit)
		}
	}
	var bodies []MIHistoryEntry
	if err := s.MIBodies(70, 2, func(v MIHistoryEntry) error {
		bodies = append(bodies, v)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if len(bodies) != 2 || bodies[0].ElementInstanceKey != 100 || bodies[1].ElementInstanceKey != 110 {
		t.Fatalf("body page crossed its bound or changed order: %+v", bodies)
	}
	var rounds []MIHistoryEntry
	if err := s.MIIterations(70, 100, 10, 1, func(v MIHistoryEntry) error {
		rounds = append(rounds, v)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if len(rounds) != 1 || rounds[0].ElementInstanceKey != 101 {
		t.Fatalf("iteration page crossed body boundary: %+v", rounds)
	}
	if err := s.MIBodies(71, 200, func(MIHistoryEntry) error {
		t.Fatal("saw another process instance's body")
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func TestMIHistoryCorruptionCannotReadAsPlausibleLineage(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	body := model.ElementInstanceValue{ProcessInstanceKey: 80, ProcessDefKey: 1, MultiInstance: 1}
	round := body
	round.MultiInstance = 2
	round.FlowScopeKey = 100
	tx := s.NewTransaction()
	if err := tx.RecordMIActivation(100, &body, 10); err != nil {
		t.Fatal(err)
	}
	if err := tx.RecordMIActivation(101, &round, 11); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	_ = tx.Close()

	// Each corrupt key/value can otherwise silently return a wrong body or round.
	tests := []struct {
		name string
		key []byte
		read func() error
	}{
		{"body reference", keyMIBodyByElement(80, 100), func() error { _, _, err := s.MIBody(80, 100); return err }},
		{"body value", keyMIBody(80, 10, 100), func() error { _, _, err := s.MIBody(80, 100); return err }},
		{"round reference", keyMIRoundByElement(80, 101), func() error { _, _, err := s.MIIteration(80, 101); return err }},
		{"round value", keyMIIteration(80, 100, 10, 101), func() error { _, _, err := s.MIIteration(80, 101); return err }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			original, ok, err := getCopy(s.db, tt.key)
			if err != nil || !ok {
				t.Fatalf("missing reference before corruption: ok=%v err=%v", ok, err)
			}
			if err := s.db.Set(tt.key, []byte{0xff}, pebble.NoSync); err != nil {
				t.Fatal(err)
			}
			if err := tt.read(); err == nil {
				t.Fatal("corrupt MI projection was accepted")
			}
			if err := s.db.Set(tt.key, original, pebble.NoSync); err != nil {
				t.Fatal(err)
			}
			if err := tt.read(); err != nil {
				t.Fatalf("valid projection not restored: %v", err)
			}
		})
	}
	value := appendMIHistoryValue(make([]byte, 0, miHistoryValueSize), MIHistoryEntry{State: MIHistoryActive})
	value[45] = 255
	if _, err := decodeMIHistoryValue(value); err == nil {
		t.Fatal("invalid lifecycle status was accepted")
	}
}
