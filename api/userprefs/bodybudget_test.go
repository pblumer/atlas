package userprefs

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/pblumer/atlas/limits"
)

// TestPreferencesPastTheirBudgetAreRefusedAndNotStored: a save reads its body up to
// the budget the server configured, answers 413 past it, and writes nothing.
func TestPreferencesPastTheirBudgetAreRefusedAndNotStored(t *testing.T) {
	s := newService(t)
	s.Limits = limits.Default()
	s.Limits.Request = 256

	rec := httptest.NewRecorder()
	s.HandleSave(rec, as("usr_1", "PUT", `{"language":"de","padding":"`+strings.Repeat("a", 300)+`"}`))
	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("a save past its budget = %d (%s), want 413", rec.Code, rec.Body)
	}
	rec = httptest.NewRecorder()
	s.HandleGet(rec, as("usr_1", "GET", ""))
	if got := decode(t, rec); got["language"] != "" {
		t.Errorf("the refused save was stored: %v", got)
	}

	rec = httptest.NewRecorder()
	s.HandleSave(rec, as("usr_1", "PUT", `{"language":"de"}`))
	if rec.Code != http.StatusOK {
		t.Errorf("a save within its budget = %d (%s), want 200", rec.Code, rec.Body)
	}
}
