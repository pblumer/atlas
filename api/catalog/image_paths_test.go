package catalog

import (
	"net/http"
	"strings"
	"testing"
)

// TestAPictureOverTheBudgetIsRefusedNotTruncated: the body is read one byte past
// the budget, so an image too large is refused with 413 rather than cut down to
// something that still passes the format check — and the old picture stays.
func TestAPictureOverTheBudgetIsRefusedNotTruncated(t *testing.T) {
	s, _ := catalogueWithAPicturedProduct(t)
	s.Limits.Asset = int64(len(onePNG))
	owner := user("usr_owner")

	big := onePNG + strings.Repeat("x", 10)
	rec := asTyped(t, s.HandleSetPicture, owner, "PUT", "image/png", big, "id", "prd_gate")
	if rec.Code != http.StatusRequestEntityTooLarge || !strings.Contains(rec.Body.String(), "exceeds the") {
		t.Fatalf("oversized picture = %d (%s), want 413", rec.Code, rec.Body)
	}
	data, _, ok, err := s.store.Picture("prd_gate")
	if err != nil || !ok || string(data) != onePNG {
		t.Fatalf("picture after the refusal = %q (ok=%v, err=%v), want the old one", data, ok, err)
	}
}

// TestTheStoreRefusesAPictureTypeItHasNoFileFor: the handler checks the type first,
// and the store holds the same line on its own, so no caller can write a file the
// reader would never serve.
func TestTheStoreRefusesAPictureTypeItHasNoFileFor(t *testing.T) {
	s := serviceWithAdmin(t)
	if err := s.store.SavePicture("prd_x", []byte("%PDF-1.7"), "application/pdf"); err == nil ||
		!strings.Contains(err.Error(), `unsupported picture type "application/pdf"`) {
		t.Fatalf("SavePicture(pdf) = %v, want the type refused", err)
	}
}
