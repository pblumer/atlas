package state

import (
	"testing"

	"github.com/cockroachdb/pebble"
	"github.com/pblumer/atlas/model"
)

// v1Row is a schema v1 MI value: version 1, no role, 46 bytes.
func v1Row() []byte {
	raw := make([]byte, miV1ValueSize)
	raw[0] = 1
	raw[miV1ValueSize-1] = byte(MIHistoryActive)
	return raw
}

// An execution that started under a v1 build and ends — or raises an incident —
// after the upgrade meets its v1 row in the apply path. WAL replay folds exactly
// that, so failing there stops the server from starting at all.
func TestMIApplyLeavesV1RowsAlone(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	const pi, bodyKey, roundKey, bodyPos, roundPos = 40, 100, 101, 10, 11
	body := model.ElementInstanceValue{ProcessInstanceKey: pi, ProcessDefKey: 9, MultiInstance: 1}
	round := body
	round.MultiInstance = 2
	round.FlowScopeKey = bodyKey

	tx := s.NewTransaction()
	if err := tx.RecordMIActivation(bodyKey, &body, bodyPos); err != nil {
		t.Fatal(err)
	}
	if err := tx.RecordMIActivation(roundKey, &round, roundPos); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	_ = tx.Close()

	// Rewrite both rows as a v1 build left them; the references are unchanged,
	// as they are between v1 and v2.
	tx = s.NewTransaction()
	bodyRow := append([]byte(nil), tx.miBodyRowKey(pi, bodyPos, bodyKey)...)
	roundRow := append([]byte(nil), tx.miIterRowKey(pi, bodyKey, bodyPos, roundKey)...)
	_ = tx.Close()
	for _, k := range [][]byte{bodyRow, roundRow} {
		if err := s.db.Set(k, v1Row(), pebble.Sync); err != nil {
			t.Fatal(err)
		}
	}

	tx = s.NewTransaction()
	defer tx.Close()
	inc := model.IncidentValue{ProcessInstanceKey: pi, ElementInstanceKey: roundKey}
	if err := tx.RecordMIIncidentRaised(&inc, 12); err != nil {
		t.Fatalf("incident on a v1 iteration: %v", err)
	}
	if err := tx.RecordMIIncidentResolved(&inc, 13); err != nil {
		t.Fatalf("resolving an unattributed incident: %v", err)
	}
	if err := tx.RecordMIEnd(roundKey, &round, 14, false); err != nil {
		t.Fatalf("end of a v1 iteration: %v", err)
	}
	if err := tx.RecordMIEnd(bodyKey, &body, 15, false); err != nil {
		t.Fatalf("end of a v1 body: %v", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}

	// The v1 rows are left exactly as they were: nothing is guessed into them.
	for _, k := range [][]byte{bodyRow, roundRow} {
		got, ok, err := getCopy(s.db, k)
		if err != nil || !ok || !isMIV1Value(got) {
			t.Fatalf("v1 row changed: ok=%v err=%v value=%x", ok, err, got)
		}
	}
}

// Only a well-formed v1 row is legacy. Anything else of that width is still
// corruption, and still stops the fold.
func TestMIApplyStillRejectsCorruptRows(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	body := model.ElementInstanceValue{ProcessInstanceKey: 40, ProcessDefKey: 9, MultiInstance: 1}
	tx := s.NewTransaction()
	if err := tx.RecordMIActivation(100, &body, 10); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	k := append([]byte(nil), tx.miBodyRowKey(40, 10, 100)...)
	_ = tx.Close()
	bad := v1Row()
	bad[0] = 7
	if err := s.db.Set(k, bad, pebble.Sync); err != nil {
		t.Fatal(err)
	}

	tx = s.NewTransaction()
	defer tx.Close()
	if err := tx.RecordMIEnd(100, &body, 11, false); err == nil {
		t.Fatal("a corrupt MI row was accepted as legacy")
	}
}
