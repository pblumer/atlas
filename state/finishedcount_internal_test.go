package state

import (
	"testing"

	"github.com/pblumer/atlas/model"
)

// TestCountFinishedOfDefBefore pins the count a history-purge preview shows
// (ADR-0486): the finished instances of one
// definition that completed strictly before a cut-off, read off the by-definition index
// without decoding a record — and nothing of another definition.
func TestCountFinishedOfDefBefore(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer func() { _ = s.Close() }()

	tx := s.NewTransaction()
	for i, at := range []int64{100, 200, 300, 300} {
		key := uint64(200 + i)
		if err := tx.PutProcessInstanceHistory(key, &model.ProcessInstanceValue{ProcessDefKey: 7, State: model.PICompleted, CompletedAt: at}); err != nil {
			t.Fatalf("PutProcessInstanceHistory: %v", err)
		}
	}
	if err := tx.PutProcessInstanceHistory(300, &model.ProcessInstanceValue{ProcessDefKey: 8, State: model.PICompleted, CompletedAt: 100}); err != nil {
		t.Fatalf("PutProcessInstanceHistory: %v", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("Commit: %v", err)
	}
	_ = tx.Close()

	for _, tc := range []struct {
		before int64
		want   int
	}{
		{100, 0}, // strictly before: the one finished at 100 is not counted
		{101, 1},
		{300, 2},
		{301, 4}, // both finished at 300, the tie broken by instance key
	} {
		got, err := s.CountFinishedOfDefBefore(7, tc.before)
		if err != nil {
			t.Fatalf("CountFinishedOfDefBefore(7, %d): %v", tc.before, err)
		}
		if got != tc.want {
			t.Errorf("CountFinishedOfDefBefore(7, %d) = %d, want %d", tc.before, got, tc.want)
		}
	}

	// The snapshot a preview reads from answers the same.
	v := s.ReadView()
	defer func() { _ = v.Close() }()
	if got, err := v.CountFinishedOfDefBefore(8, 1000); err != nil || got != 1 {
		t.Errorf("ReadView.CountFinishedOfDefBefore(8) = (%d, %v), want 1", got, err)
	}
}
