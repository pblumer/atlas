package catalog

import (
	"encoding/hex"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pblumer/atlas/api/httpapi"
	"github.com/pblumer/atlas/limits"
)

// A catalogue's brand mark: who may set it, who may see it, and what the server
// refuses to store.

func pngBytes() string { return "\x89PNG\r\n\x1a\n" + "raster" }
func svgBytes() string { return `<svg xmlns="http://www.w3.org/2000/svg"/>` }
func TestALogoGoesInAndComesBack(t *testing.T) {
	s := serviceWithAdmin(t)
	cat := makeCatalog(t, s, user("usr_owner"))

	if rec := asTyped(t, s.HandleSetLogo, admin(), "PUT", "image/png", pngBytes(), "id", cat.ID); rec.Code != http.StatusNoContent {
		t.Fatalf("set = %d (%s), want 204", rec.Code, rec.Body)
	}

	rec := as(t, s.HandleGetLogo, admin(), "GET", "", "id", cat.ID)
	if rec.Code != http.StatusOK {
		t.Fatalf("get = %d (%s), want 200", rec.Code, rec.Body)
	}
	if rec.Body.String() != pngBytes() {
		t.Errorf("the bytes came back changed: %q", rec.Body.String())
	}
	if got := rec.Header().Get("Content-Type"); got != "image/png" {
		t.Errorf("Content-Type = %q", got)
	}
	// The headers are the mitigation, not a nicety: they are what makes a stored
	// SVG inert. Asserted here too, so removing brandimage.Serve from this path
	// fails in the package that serves it.
	if got := rec.Header().Get("X-Content-Type-Options"); got != "nosniff" {
		t.Errorf("nosniff missing on a catalogue's mark: %q", got)
	}
	if !strings.Contains(rec.Header().Get("Content-Security-Policy"), "sandbox") {
		t.Errorf("CSP = %q", rec.Header().Get("Content-Security-Policy"))
	}

	if rec := as(t, s.HandleDeleteLogo, admin(), "DELETE", "", "id", cat.ID); rec.Code != http.StatusNoContent {
		t.Fatalf("delete = %d (%s), want 204", rec.Code, rec.Body)
	}
	if rec := as(t, s.HandleGetLogo, admin(), "GET", "", "id", cat.ID); rec.Code != http.StatusNotFound {
		t.Fatalf("get after delete = %d, want 404", rec.Code)
	}
	// Twice, because clearing something that is already clear is the state the
	// caller asked for and not an error.
	if rec := as(t, s.HandleDeleteLogo, admin(), "DELETE", "", "id", cat.ID); rec.Code != http.StatusNoContent {
		t.Fatalf("second delete = %d, want 204", rec.Code)
	}
}

// TestSwitchingFormatLeavesNothingBehind: a PNG replaced by an SVG must not leave
// a file the reader would find first and serve instead.
func TestSwitchingFormatLeavesNothingBehind(t *testing.T) {
	s := serviceWithAdmin(t)
	cat := makeCatalog(t, s, user("usr_owner"))

	asTyped(t, s.HandleSetLogo, admin(), "PUT", "image/png", pngBytes(), "id", cat.ID)
	if rec := asTyped(t, s.HandleSetLogo, admin(), "PUT", "image/svg+xml", svgBytes(), "id", cat.ID); rec.Code != http.StatusNoContent {
		t.Fatalf("switch = %d (%s)", rec.Code, rec.Body)
	}

	rec := as(t, s.HandleGetLogo, admin(), "GET", "", "id", cat.ID)
	if got := rec.Header().Get("Content-Type"); got != "image/svg+xml" {
		t.Fatalf("Content-Type = %q; the stale PNG was served", got)
	}
	if rec.Body.String() != svgBytes() {
		t.Errorf("body = %q", rec.Body.String())
	}
}

// TestWhatIsRefusedOnTheWayIn covers every reason the server declines to store
// what it was handed. Each one matters for a different reason: the type because
// nothing else is servable, the emptiness because a zero-byte mark is an invisible
// one nobody would debug, the size because it is the installation's memory, and
// the content because a Content-Type is the caller's claim rather than a fact.
func TestWhatIsRefusedOnTheWayIn(t *testing.T) {
	s := serviceWithAdmin(t)
	s.Limits = limits.Default()
	s.Limits.Asset = 32
	cat := makeCatalog(t, s, user("usr_owner"))

	for _, tc := range []struct {
		name string
		ct   string
		body string
		want int
	}{
		{"a format nothing serves", "image/gif", pngBytes(), http.StatusUnsupportedMediaType},
		{"no type at all", "", pngBytes(), http.StatusUnsupportedMediaType},
		{"nothing at all", "image/png", "", http.StatusBadRequest},
		{"larger than the budget", "image/png", "\x89PNG\r\n\x1a\n" + strings.Repeat("x", 40), http.StatusRequestEntityTooLarge},
		{"not the image it claims", "image/png", "GIF89a-really", http.StatusBadRequest},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rec := asTyped(t, s.HandleSetLogo, admin(), "PUT", tc.ct, tc.body, "id", cat.ID)
			if rec.Code != tc.want {
				t.Fatalf("= %d (%s), want %d", rec.Code, rec.Body, tc.want)
			}
			if rec := as(t, s.HandleGetLogo, admin(), "GET", "", "id", cat.ID); rec.Code != http.StatusNotFound {
				t.Fatalf("a refused upload was stored anyway (get = %d)", rec.Code)
			}
		})
	}
}

// TestTheParametersAreCanonicalised: a browser that spells the type with a charset
// is not wrong, and refusing it would be.
func TestTheParametersAreCanonicalised(t *testing.T) {
	s := serviceWithAdmin(t)
	cat := makeCatalog(t, s, user("usr_owner"))
	rec := asTyped(t, s.HandleSetLogo, admin(), "PUT", "IMAGE/SVG+XML; charset=utf-8", svgBytes(), "id", cat.ID)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("= %d (%s), want 204", rec.Code, rec.Body)
	}
}

// TestTheMarkFollowsTheCatalogueGate is the authorization half.
//
// Three distinct answers, and the difference between them is the point. Somebody
// in the audience may see the mark, because it is the face of the catalogue they
// order from. They may not change it, and are told so — 403, because they can see
// the catalogue and the refusal gives them nothing they did not have. Somebody
// outside gets 404 everywhere, because for them the catalogue does not exist.
func TestTheMarkFollowsTheCatalogueGate(t *testing.T) {
	s := serviceWithAdmin(t)
	cat := makeCatalog(t, s, user("usr_owner"))
	as(t, s.HandleUpdateCatalog, user("usr_owner"), "PATCH", `{"groups":["grp_inside"]}`, "id", cat.ID)
	asTyped(t, s.HandleSetLogo, admin(), "PUT", "image/png", pngBytes(), "id", cat.ID)

	customer := &httpapi.Principal{UserID: "usr_cust", Roles: []string{"user"}, GroupIDs: []string{"grp_inside"}}
	outsider := &httpapi.Principal{UserID: "usr_out", Roles: []string{"user"}, GroupIDs: []string{"grp_elsewhere"}}

	if rec := as(t, s.HandleGetLogo, customer, "GET", "", "id", cat.ID); rec.Code != http.StatusOK {
		t.Errorf("the audience cannot see its own mark: %d (%s)", rec.Code, rec.Body)
	}
	if rec := asTyped(t, s.HandleSetLogo, customer, "PUT", "image/png", pngBytes(), "id", cat.ID); rec.Code != http.StatusForbidden {
		t.Errorf("a customer set the brand: %d (%s)", rec.Code, rec.Body)
	}
	if rec := as(t, s.HandleDeleteLogo, customer, "DELETE", "", "id", cat.ID); rec.Code != http.StatusForbidden {
		t.Errorf("a customer cleared the brand: %d (%s)", rec.Code, rec.Body)
	}
	if rec := as(t, s.HandleGetLogo, outsider, "GET", "", "id", cat.ID); rec.Code != http.StatusNotFound {
		t.Errorf("an outsider saw a mark: %d", rec.Code)
	}
	// The product manager who maintains the catalogue is not the exception: the
	// brand is administration, exactly as the theme is (decision 12).
	if rec := asTyped(t, s.HandleSetLogo, user("usr_owner"), "PUT", "image/png", pngBytes(), "id", cat.ID); rec.Code != http.StatusForbidden {
		t.Errorf("the owner set the brand: %d (%s)", rec.Code, rec.Body)
	}
	// And an unauthenticated request is nobody, not everybody.
	if rec := as(t, s.HandleGetLogo, nil, "GET", "", "id", cat.ID); rec.Code != http.StatusNotFound {
		t.Errorf("an anonymous request saw a mark: %d", rec.Code)
	}
}

// TestAMarkNeedsACatalogue: every one of the three answers 404 for an id that
// names nothing, rather than creating a file for a catalogue that does not exist.
func TestAMarkNeedsACatalogue(t *testing.T) {
	s := serviceWithAdmin(t)
	if rec := as(t, s.HandleGetLogo, admin(), "GET", "", "id", "cat_nope"); rec.Code != http.StatusNotFound {
		t.Errorf("get = %d", rec.Code)
	}
	if rec := asTyped(t, s.HandleSetLogo, admin(), "PUT", "image/png", pngBytes(), "id", "cat_nope"); rec.Code != http.StatusNotFound {
		t.Errorf("set = %d", rec.Code)
	}
	if rec := as(t, s.HandleDeleteLogo, admin(), "DELETE", "", "id", "cat_nope"); rec.Code != http.StatusNotFound {
		t.Errorf("delete = %d", rec.Code)
	}
}

// TestAnIdCannotBecomeAPath is the reason the stored name is hex. Ids reaching
// these handlers come from a record that was read first, so this is the second
// lock — but a second lock is what a path built from a request-supplied string
// needs, and the store is where it belongs.
func TestAnIdCannotBecomeAPath(t *testing.T) {
	dir := t.TempDir()
	store, err := NewStore(dir)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	if err := store.SaveLogo("../../escaped", []byte(pngBytes()), "image/png"); err != nil {
		t.Fatalf("SaveLogo: %v", err)
	}

	entries, err := os.ReadDir(filepath.Join(dir, "logos"))
	if err != nil {
		t.Fatalf("read logo dir: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("wrote %d files, want 1", len(entries))
	}
	if want := hex.EncodeToString([]byte("../../escaped")) + ".png"; entries[0].Name() != want {
		t.Errorf("stored as %q, want %q — the id reached the filename unencoded", entries[0].Name(), want)
	}
	// And it reads back under the same key, so the encoding is a name and not a
	// mangling.
	data, ct, ok, err := store.Logo("../../escaped")
	if err != nil || !ok || ct != "image/png" || string(data) != pngBytes() {
		t.Errorf("Logo = %q, %q, %v, %v", data, ct, ok, err)
	}
}

// TestAnUnsupportedTypeNeverReachesTheDisk: the store has its own guard, because
// a handler is not the only caller a store will ever have.
func TestAnUnsupportedTypeNeverReachesTheDisk(t *testing.T) {
	store, err := NewStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	if err := store.SaveLogo("cat_1", []byte("GIF89a"), "image/gif"); err == nil {
		t.Fatal("the store took a format it cannot serve back")
	}
}

// TestABrokenDiskIsAFaultAndNotAnAbsence.
//
// "There is no mark" and "the mark cannot be read" are different answers and the
// portal treats them differently: the first falls back to the operator's brand,
// the second is somebody's storage failing. Reporting a broken read as 404 would
// hide a failing disk behind a page that looks slightly plain.
//
// A directory where the file belongs is how that is provoked: the read fails with
// something other than "not there", which is exactly the branch under test.
func TestABrokenDiskIsAFaultAndNotAnAbsence(t *testing.T) {
	s := serviceWithAdmin(t)
	cat := makeCatalog(t, s, user("usr_owner"))

	blocked := s.store.logoPath(cat.ID, "png")
	if err := os.Mkdir(blocked, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	// Something inside it, so removing it fails too.
	if err := os.WriteFile(filepath.Join(blocked, "occupied"), []byte("x"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	if rec := as(t, s.HandleGetLogo, admin(), "GET", "", "id", cat.ID); rec.Code != http.StatusInternalServerError {
		t.Errorf("get = %d (%s), want 500", rec.Code, rec.Body)
	}
	if rec := as(t, s.HandleDeleteLogo, admin(), "DELETE", "", "id", cat.ID); rec.Code != http.StatusInternalServerError {
		t.Errorf("delete = %d (%s), want 500", rec.Code, rec.Body)
	}
	if rec := asTyped(t, s.HandleSetLogo, admin(), "PUT", "image/svg+xml", svgBytes(), "id", cat.ID); rec.Code != http.StatusInternalServerError {
		t.Errorf("set = %d (%s), want 500", rec.Code, rec.Body)
	}
}

// TestAServiceBuiltByHandStillHasCeilings: the zero Limits is every budget at
// zero, and a budget of zero admits nothing — a logo upload that fails as "too
// large" at one byte would look like a bad request rather than like missing
// configuration.
func TestAServiceBuiltByHandStillHasCeilings(t *testing.T) {
	if got := (&Service{}).budgets(); got != limits.Default() {
		t.Errorf("budgets on a bare service = %+v, want the defaults", got)
	}
}
