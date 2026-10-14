package api

import (
	"encoding/hex"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pblumer/atlas/api/catalog"
)

// The discrepancy actions (ADR-0334) when the finding, the product or the journal
// cannot be read or written. In each the finding must still stand afterwards: a
// finding closed over an act that did not happen is a disagreement the journal
// stops showing while it is still true.

// reconcileActionsPathsAct posts one action on one finding.
func reconcileActionsPathsAct(t *testing.T, s *Server, id, action string) (int, string) {
	t.Helper()
	return recertifyHTTPPathsCall(t, s.Handler(), http.MethodPost,
		"/api/v1/reconciliation/"+id+"/"+action, nil)
}

// reconcileActionsPathsStillOpen fails the test unless the finding still stands.
func reconcileActionsPathsStillOpen(t *testing.T, s *Server, id string) {
	t.Helper()
	var (
		rec   discrepancyRecord
		found bool
		err   error
	)
	s.do(func() { rec, found, err = s.discrepancies.Get(id) })
	if err != nil || !found || !rec.Open() {
		t.Errorf("finding %s after the failed action = %+v found=%v err=%v, want it open", id, rec, found, err)
	}
}

// TestReconcileActionsAFindingThatCannotBeReadIsAFault. A finding record that does
// not decode is not "no finding": the operator is told the journal is broken
// rather than that the finding they are looking at does not exist.
func TestReconcileActionsAFindingThatCannotBeReadIsAFault(t *testing.T) {
	srv := newServerForErrors(t)
	if err := os.WriteFile(srv.discrepancies.FileFor("d-torn"), []byte("{"), 0o600); err != nil {
		t.Fatalf("corrupt finding: %v", err)
	}
	code, body := reconcileActionsPathsAct(t, srv, "d-torn", "adopt")
	if code != http.StatusInternalServerError || !strings.Contains(body, "read the finding") {
		t.Errorf("adopt = %d %s, want 500 naming the read", code, body)
	}
}

// TestReconcileActionsAJournalThatCannotBeWrittenIsSaid. The act is done first and
// the journal last, so a journal write that fails is reported as exactly that —
// the act happened, the finding could not be closed — and the finding stands for
// the next run to close.
func TestReconcileActionsAJournalThatCannotBeWrittenIsSaid(t *testing.T) {
	srv := newServerForErrors(t)
	rec := openUnmanaged("d-stuck")
	rec.Kind = recMissing
	seedDiscrepancy(t, srv, rec)
	reconcileApplyPathsBlockSave(t, srv.discrepancies.FileFor(rec.ID))

	code, body := reconcileActionsPathsAct(t, srv, rec.ID, "revoke")
	if code != http.StatusInternalServerError || !strings.Contains(body, "the finding could not be closed") {
		t.Errorf("revoke = %d %s, want 500 saying the finding stays open", code, body)
	}
	reconcileActionsPathsStillOpen(t, srv, rec.ID)
}

// TestReconcileActionsDeprovisioningNeedsAProductThatCanDoIt. The process comes
// from the catalogue as it stands now; a product that cannot be read, or that
// binds no deprovisioning, starts nothing and closes nothing.
func TestReconcileActionsDeprovisioningNeedsAProductThatCanDoIt(t *testing.T) {
	t.Run("the product cannot be read", func(t *testing.T) {
		srv := newServerForErrors(t)
		rec := openUnmanaged("d-torn-item")
		seedDiscrepancy(t, srv, rec)
		path := filepath.Join(srv.dataDir, "catalog", "items", hex.EncodeToString([]byte(rec.ItemID))+".json")
		if err := os.WriteFile(path, []byte("{"), 0o600); err != nil {
			t.Fatalf("corrupt product: %v", err)
		}

		code, body := reconcileActionsPathsAct(t, srv, rec.ID, "deprovision")
		if code != http.StatusInternalServerError || !strings.Contains(body, "catalogitemstore") {
			t.Errorf("deprovision = %d %s, want 500 naming the product store", code, body)
		}
		reconcileActionsPathsStillOpen(t, srv, rec.ID)
	})

	t.Run("the product binds no deprovisioning", func(t *testing.T) {
		srv := newServerForErrors(t)
		rec := openUnmanaged("d-unbound")
		seedDiscrepancy(t, srv, rec)
		recertifyActionsPathsSaveItem(t, srv, catalog.Item{ID: rec.ItemID, ProvisionProcess: "p"})

		code, body := reconcileActionsPathsAct(t, srv, rec.ID, "deprovision")
		if code != http.StatusInternalServerError || !strings.Contains(body, "binds no deprovisioning process") {
			t.Errorf("deprovision = %d %s, want 500 naming the missing binding", code, body)
		}
		reconcileActionsPathsStillOpen(t, srv, rec.ID)
	})
}
