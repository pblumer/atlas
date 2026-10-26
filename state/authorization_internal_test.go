package state

import (
	"errors"
	"testing"

	"github.com/cockroachdb/pebble"

	"github.com/pblumer/atlas/model"
)

// A decision or approval fact that does not decode is reported, on every read path,
// rather than skipped: evidence that silently lost a row would read as a history in
// which that decision was never made.
func TestACorruptAuthorizationRecordIsAnErrorNotAShorterHistory(t *testing.T) {
	s := openStore(t)
	const pi, job, other = uint64(10), uint64(30), uint64(31)
	tx := s.NewTransaction()
	if err := tx.RecordAuthorization(5, &model.AuthorizationValue{ProcessInstanceKey: pi, JobKey: job, ReasonCode: "OK"}); err != nil {
		t.Fatal(err)
	}
	if err := tx.RecordAuthorization(6, &model.AuthorizationValue{ProcessInstanceKey: pi, JobKey: other, ReasonCode: "OTHER"}); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	// One byte is shorter than any AuthorizationValue, after a good row of the same job.
	if err := s.db.Set(keyAuthorization(pi, job, 7), []byte{0x01}, pebble.NoSync); err != nil {
		t.Fatalf("seed corrupt record: %v", err)
	}

	seen := 0
	count := func(uint64, *model.AuthorizationValue) error { seen++; return nil }
	if err := s.AuthorizationHistory(pi, count); !errors.Is(err, model.ErrShortBuffer) {
		t.Errorf("AuthorizationHistory over a corrupt record: err = %v, want a decode error", err)
	}
	seen = 0
	if err := s.AuthorizationsOfJob(pi, job, count); !errors.Is(err, model.ErrShortBuffer) {
		t.Errorf("AuthorizationsOfJob over a corrupt record: err = %v, want a decode error", err)
	}
	if seen != 1 {
		t.Errorf("the job's good decision before the corrupt one was visited %d times, want 1", seen)
	}
	// Another job of the same instance is a different scan and still reads.
	seen = 0
	if err := s.AuthorizationsOfJob(pi, other, count); err != nil || seen != 1 {
		t.Errorf("another job's decisions: %d read, err = %v", seen, err)
	}
}

func TestACorruptApprovalFactIsAnErrorOnBothReadPaths(t *testing.T) {
	s := openStore(t)
	const pi = uint64(10)
	if err := s.db.Set(keyApprovalFact(pi, 5, 1), []byte{0x01}, pebble.NoSync); err != nil {
		t.Fatalf("seed corrupt fact: %v", err)
	}
	if err := s.ApprovalFacts(pi, func(*model.ActionApprovalValue) error { return nil }); !errors.Is(err, model.ErrShortBuffer) {
		t.Errorf("ApprovalFacts over a corrupt fact: err = %v, want a decode error", err)
	}
	// The command path reads committed rows through its batch, and must not mistake a
	// corrupt one for "no open request".
	tx := s.NewTransaction()
	defer tx.Close()
	if err := tx.ApprovalFactsInBatch(pi, func(*model.ActionApprovalValue) error { return nil }); !errors.Is(err, model.ErrShortBuffer) {
		t.Errorf("ApprovalFactsInBatch over a corrupt fact: err = %v, want a decode error", err)
	}
}

// A visitor that returns an error stops the scan at that row and the error comes back
// unchanged, on every evidence query.
func TestAVisitorErrorStopsTheEvidenceScans(t *testing.T) {
	s := openStore(t)
	const pi, job = uint64(10), uint64(30)
	tx := s.NewTransaction()
	for pos := uint64(1); pos <= 3; pos++ {
		if err := tx.RecordAuthorization(pos, &model.AuthorizationValue{ProcessInstanceKey: pi, JobKey: job}); err != nil {
			t.Fatal(err)
		}
		if err := tx.RecordApprovalFact(pos, &model.ActionApprovalValue{ProcessInstanceKey: pi, ApprovalKey: 5, Kind: model.ApprovalRequested}); err != nil {
			t.Fatal(err)
		}
	}
	// Before the commit: the batch's own rows are what the in-batch scan stops in.
	stop := errors.New("enough")
	calls := 0
	if err := tx.ApprovalFactsInBatch(pi, func(*model.ActionApprovalValue) error { calls++; return stop }); !errors.Is(err, stop) || calls != 1 {
		t.Errorf("ApprovalFactsInBatch: err = %v after %d calls, want the visitor's error after 1", err, calls)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}

	for name, scan := range map[string]func(func() error) error{
		"AuthorizationHistory": func(fn func() error) error {
			return s.AuthorizationHistory(pi, func(uint64, *model.AuthorizationValue) error { return fn() })
		},
		"AuthorizationsOfJob": func(fn func() error) error {
			return s.AuthorizationsOfJob(pi, job, func(uint64, *model.AuthorizationValue) error { return fn() })
		},
		"ApprovalFacts": func(fn func() error) error {
			return s.ApprovalFacts(pi, func(*model.ActionApprovalValue) error { return fn() })
		},
	} {
		calls := 0
		err := scan(func() error {
			calls++
			if calls == 2 {
				return stop
			}
			return nil
		})
		if !errors.Is(err, stop) || calls != 2 {
			t.Errorf("%s: err = %v after %d calls, want the visitor's error after 2 of 3 rows", name, err, calls)
		}
	}
}
