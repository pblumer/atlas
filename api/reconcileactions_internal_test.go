package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// The three discrepancy actions share one lookup, one set of refusals and one
// journal write (actOnDiscrepancy), and none of them had a test. The refusals are
// what these pin: each one is a sentence an operator has to act on, and the reason
// it gives is the whole point — a bare 409 would send them back to the list to work
// out which of two opposite remedies applies.

// seedDiscrepancy plants one finding the way a reconciliation run leaves it.
func seedDiscrepancy(t *testing.T, s *Server, rec discrepancyRecord) {
	t.Helper()
	var err error
	s.do(func() { err = s.discrepancies.Save(rec) })
	if err != nil {
		t.Fatalf("seed finding %s: %v", rec.ID, err)
	}
}

func deprovision(t *testing.T, s *Server, id string) (int, string) {
	t.Helper()
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, httptest.NewRequest(
		http.MethodPost, "/api/v1/reconciliation/"+id+"/deprovision", nil))
	return rec.Code, rec.Body.String()
}

func openUnmanaged(id string) discrepancyRecord {
	now := time.Now().Unix()
	return discrepancyRecord{
		ID: id, Kind: recUnmanaged, System: "ad", Principal: "p.blumer",
		ItemID: "vpn-access", OpenedAt: now, LastSeenAt: now,
	}
}

func TestDeprovisioningAFindingThatIsNotThere(t *testing.T) {
	srv, _ := newValidateServer(t)
	code, body := deprovision(t, srv, "nope")
	if code != http.StatusNotFound {
		t.Fatalf("code = %d (%s), want 404", code, body)
	}
	if !strings.Contains(body, "nope") {
		t.Errorf("refusal does not name the finding: %s", body)
	}
}

// Already closed is a 409 that says *how* it closed. "Somebody adopted this an hour
// ago" and "this went away on its own" call for different next steps.
func TestDeprovisioningAFindingThatIsAlreadyClosedSaysHow(t *testing.T) {
	srv, _ := newValidateServer(t)
	rec := openUnmanaged("d1")
	rec.ClosedAt, rec.ClosedHow, rec.ClosedBy = time.Now().Unix(), closedAdopted, "someone"
	seedDiscrepancy(t, srv, rec)

	code, body := deprovision(t, srv, "d1")
	if code != http.StatusConflict {
		t.Fatalf("code = %d (%s), want 409", code, body)
	}
	for _, want := range []string{"already closed", closedAdopted, "reconcile again"} {
		if !strings.Contains(body, want) {
			t.Errorf("refusal does not mention %q: %s", want, body)
		}
	}
}

// The two directions of a disagreement do not take the same remedy, so acting on
// one with the other's action is refused rather than quietly reinterpreted.
func TestDeprovisioningAFindingOfTheOtherKindIsRefused(t *testing.T) {
	srv, _ := newValidateServer(t)
	rec := openUnmanaged("d2")
	rec.Kind = recMissing
	seedDiscrepancy(t, srv, rec)

	code, body := deprovision(t, srv, "d2")
	if code != http.StatusConflict {
		t.Fatalf("code = %d (%s), want 409", code, body)
	}
	for _, want := range []string{recMissing, recUnmanaged, "do not take the same remedy"} {
		if !strings.Contains(body, want) {
			t.Errorf("refusal does not mention %q: %s", want, body)
		}
	}
}

// Deprovisioning revokes a right through the product's own process, so a product
// that has gone says so rather than failing as a missing process.
func TestDeprovisioningAFindingWhoseProductIsGone(t *testing.T) {
	srv, _ := newValidateServer(t)
	seedDiscrepancy(t, srv, openUnmanaged("d3"))

	code, body := deprovision(t, srv, "d3")
	if code != http.StatusInternalServerError {
		t.Fatalf("code = %d (%s), want 500", code, body)
	}
	for _, want := range []string{"vpn-access", "withdrawn or removed"} {
		if !strings.Contains(body, want) {
			t.Errorf("refusal does not mention %q: %s", want, body)
		}
	}
	// And the finding stays open: the act failed, so the journal must not record a
	// decision that was never carried out.
	var rec discrepancyRecord
	var found bool
	srv.do(func() { rec, found, _ = srv.discrepancies.Get("d3") })
	if !found || !rec.Open() {
		t.Errorf("finding closed after a failed act (found=%v, closedHow=%q)", found, rec.ClosedHow)
	}
}
