package state_test

import (
	"reflect"
	"testing"

	"github.com/pblumer/atlas/model"
	"github.com/pblumer/atlas/state"
)

// TestDataObjectPutGetScan round-trips data objects through the current-value
// family: a scope's objects are one prefix scan, isolated from another scope's,
// and a repeated Put upserts in place (the live value is the latest). Mirrors the
// variable store, plus the data-state the value carries (ADR-0051).
func TestDataObjectPutGetScan(t *testing.T) {
	s := openStore(t)
	i1 := model.NewKey(1, 10)
	i2 := model.NewKey(1, 11)

	commit(t, s, func(tx *state.Tx) error {
		puts := []model.DataObjectValue{
			{ScopeKey: i1, Name: "order", State: "received", Kind: model.VarNull},
			{ScopeKey: i1, Name: "invoice", State: "draft", Kind: model.VarNull},
			{ScopeKey: i2, Name: "order", State: "received", Kind: model.VarNull},
			// Upsert order@i1 to a later state; the live value must be this one.
			{ScopeKey: i1, Name: "order", State: "approved", Kind: model.VarJSON, Text: `{"amount":42}`},
		}
		for i := range puts {
			if err := tx.PutDataObject(&puts[i]); err != nil {
				return err
			}
		}
		return nil
	})

	// Get returns the latest value for a (scope, name).
	got, err := getDataObject(t, s, i1, "order")
	if err != nil {
		t.Fatalf("getDataObject: %v", err)
	}
	want := &model.DataObjectValue{ScopeKey: i1, Name: "order", State: "approved", Kind: model.VarJSON, Text: `{"amount":42}`}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("get order@i1 = %+v, want %+v", got, want)
	}

	// A scope scan yields only that scope's objects (order upserted, invoice).
	names := map[string]string{} // name -> state
	if err := s.DataObjectsOfScope(i1, func(v *model.DataObjectValue) error {
		names[v.Name] = v.State
		return nil
	}); err != nil {
		t.Fatalf("DataObjectsOfScope(i1): %v", err)
	}
	if want := map[string]string{"order": "approved", "invoice": "draft"}; !reflect.DeepEqual(names, want) {
		t.Errorf("scope-i1 objects = %v, want %v", names, want)
	}

	// A missing (scope, name) reads back absent.
	if v, err := getDataObject(t, s, i1, "nope"); err != nil || v != nil {
		t.Errorf("get missing = (%+v, %v), want (nil, nil)", v, err)
	}
}

// TestDataObjectSnapshotHistory records a data-object state trail and checks the
// history scan returns one scope's changes in (timestamp, position) order,
// isolated from another scope's — the event-sourced data-state timeline that makes
// data provenance replayable (ADR-0051, mirroring ADR-0048).
func TestDataObjectSnapshotHistory(t *testing.T) {
	s := openStore(t)
	i1 := model.NewKey(1, 20)
	i2 := model.NewKey(1, 21)

	changes := []struct {
		ts  int64
		pos uint64
		v   model.DataObjectValue
	}{
		{ts: 300, pos: 30, v: model.DataObjectValue{ScopeKey: i1, Name: "order", State: "approved"}},
		{ts: 100, pos: 10, v: model.DataObjectValue{ScopeKey: i1, Name: "order", State: "received"}},
		{ts: 200, pos: 20, v: model.DataObjectValue{ScopeKey: i1, Name: "order", State: "validated"}},
		{ts: 150, pos: 15, v: model.DataObjectValue{ScopeKey: i2, Name: "claim", State: "open"}},
	}
	commit(t, s, func(tx *state.Tx) error {
		for i := range changes {
			c := changes[i]
			if err := tx.RecordDataObjectSnapshot(c.ts, c.pos, &c.v); err != nil {
				return err
			}
		}
		return nil
	})

	type row struct {
		ts    int64
		pos   uint64
		name  string
		state string
	}
	fold := func(scope uint64) []row {
		var out []row
		if err := s.DataObjectSnapshotHistory(scope, func(ts int64, pos uint64, v *model.DataObjectValue) error {
			out = append(out, row{ts, pos, v.Name, v.State})
			return nil
		}); err != nil {
			t.Fatalf("DataObjectSnapshotHistory(%d): %v", scope, err)
		}
		return out
	}

	if got, want := fold(i1), []row{{100, 10, "order", "received"}, {200, 20, "order", "validated"}, {300, 30, "order", "approved"}}; !reflect.DeepEqual(got, want) {
		t.Errorf("scope-i1 state history = %v, want %v", got, want)
	}
	if got, want := fold(i2), []row{{150, 15, "claim", "open"}}; !reflect.DeepEqual(got, want) {
		t.Errorf("scope-i2 state history = %v, want %v", got, want)
	}
	if got := fold(model.NewKey(1, 99)); len(got) != 0 {
		t.Errorf("unknown scope history = %v, want none", got)
	}
}

// getDataObject reads a single data object through a throwaway transaction, so the
// test exercises the Tx.GetDataObject read path.
func getDataObject(t *testing.T, s *state.Store, scope uint64, name string) (*model.DataObjectValue, error) {
	t.Helper()
	tx := s.NewTransaction()
	defer func() {
		if err := tx.Close(); err != nil {
			t.Errorf("tx.Close: %v", err)
		}
	}()
	return tx.GetDataObject(scope, name)
}
