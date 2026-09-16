package api_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A picture of the person behind an account (ADR-draft-user-avatar), end to end.
//
// The bytes are the easy half. What these hold is the half that is easy to get
// wrong and hard to notice wrong: who may change how a colleague appears, that a
// vector never reaches a browser through this route, that a mislabelled body is
// refused, and that the picture does not outlive the account.

// aPNG and aJPEG are the smallest bodies that are genuinely what they claim.
// Genuinely, because the server checks the bytes and not the header — a fixture
// that only set a Content-Type would prove the header check twice and the content
// check never.
var (
	aPNG  = "\x89PNG\r\n\x1a\n" + "a face"
	aJPEG = "\xff\xd8\xff" + "a face"
)

// myAvatarSource reads the provenance off the caller's own account, through
// /auth/me — which is the route the portal reads and the only one an ordinary
// account may read about itself. The roster (admin-only) is checked separately,
// because that is what the console's rows depend on.
func myAvatarSource(t *testing.T, c *http.Client, ts *httptest.Server) string {
	t.Helper()
	code, body := cReq(t, c, ts, "GET", "/api/v1/auth/me", "")
	if code != http.StatusOK {
		t.Fatalf("me: %d (%s)", code, body)
	}
	var out struct {
		User struct {
			AvatarSource string `json:"avatarSource"`
		} `json:"user"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		t.Fatalf("decode me: %v (%s)", err, body)
	}
	return out.User.AvatarSource
}

// avatarFiles is every stored picture in a data directory, whatever its format.
func avatarFiles(t *testing.T, dir string) []string {
	t.Helper()
	hits, err := filepath.Glob(filepath.Join(dir, "users", "*.avatar.*"))
	if err != nil {
		t.Fatalf("glob: %v", err)
	}
	return hits
}

// TestAPictureGoesInAndComesBack, under the headers that make an image inert.
//
// The headers are not decoration. This is bytes a person supplied that the server
// hands to every browser that opens a page naming them, and the response is the
// whole of what keeps that safe.
func TestAPictureGoesInAndComesBack(t *testing.T) {
	ts, dir := newAuthServer(t, "root", "rootpassword")
	admin := newClient(t)
	if login(t, admin, ts, "root", "rootpassword") != http.StatusOK {
		t.Fatal("admin login failed")
	}
	alice := twoUsers(t, ts, admin, "alice")[0]
	id := meID(t, alice, ts)
	path := "/api/v1/users/" + id + "/avatar"

	// Nothing yet, and that is an answer rather than a failure.
	if code, _ := cReq(t, alice, ts, "GET", path, ""); code != http.StatusNotFound {
		t.Fatalf("an account with no picture answered %d", code)
	}
	if got := myAvatarSource(t, alice, ts); got != "" {
		t.Errorf("an account with no picture claims a provenance %q", got)
	}

	if code, b := cReqTyped(t, alice, ts, "PUT", path, "image/png", aPNG); code != http.StatusNoContent {
		t.Fatalf("upload: %d (%s)", code, b)
	}
	res, err := alice.Get(ts.URL + path)
	if err != nil {
		t.Fatalf("get picture: %v", err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("get picture: %d", res.StatusCode)
	}
	if ct := res.Header.Get("Content-Type"); ct != "image/png" {
		t.Errorf("served as %q, want image/png", ct)
	}
	if got := res.Header.Get("X-Content-Type-Options"); got != "nosniff" {
		t.Errorf("X-Content-Type-Options = %q; the declared type is not pinned", got)
	}
	if got := res.Header.Get("Content-Security-Policy"); !strings.Contains(got, "sandbox") {
		t.Errorf("Content-Security-Policy = %q; an image opened as a document is not sandboxed", got)
	}
	if got := myAvatarSource(t, alice, ts); got != "uploaded" {
		t.Errorf("provenance = %q, want uploaded", got)
	}

	// Replacing one format with another leaves nothing of the first behind: a
	// stale file in the other extension is one the read would find first, and the
	// account would go on wearing a face nobody can remove.
	if code, b := cReqTyped(t, alice, ts, "PUT", path, "image/jpeg", aJPEG); code != http.StatusNoContent {
		t.Fatalf("replace: %d (%s)", code, b)
	}
	res2, err := alice.Get(ts.URL + path)
	if err != nil {
		t.Fatalf("get picture: %v", err)
	}
	defer res2.Body.Close()
	if ct := res2.Header.Get("Content-Type"); ct != "image/jpeg" {
		t.Errorf("after replacing, served as %q", ct)
	}
	// Counted on disk, not inferred from what came back. The read walks the
	// extensions in a fixed order, and that order decides which of two files wins —
	// so a stale one is invisible from the outside exactly half the time, and the
	// half it is invisible in depends on nothing but the alphabet.
	if got := avatarFiles(t, dir); len(got) != 1 {
		t.Errorf("replacing left %d pictures for one account: %v", len(got), got)
	}

	if code, b := cReq(t, alice, ts, "DELETE", path, ""); code != http.StatusNoContent {
		t.Fatalf("remove: %d (%s)", code, b)
	}
	if code, _ := cReq(t, alice, ts, "GET", path, ""); code != http.StatusNotFound {
		t.Errorf("the picture survived its removal: %d", code)
	}
	if got := myAvatarSource(t, alice, ts); got != "" {
		t.Errorf("provenance = %q after removal, want none", got)
	}
}

// TestHowSomebodyAppearsIsTheirsOrAnAdministratorsToChange.
//
// The gate, from both sides. An operator is deliberately not here: an operator
// runs what is deployed, and changing the face a colleague wears to everybody
// else is not running anything.
func TestHowSomebodyAppearsIsTheirsOrAnAdministratorsToChange(t *testing.T) {
	ts, _ := newAuthServer(t, "root", "rootpassword")
	admin := newClient(t)
	if login(t, admin, ts, "root", "rootpassword") != http.StatusOK {
		t.Fatal("admin login failed")
	}
	clients := twoUsers(t, ts, admin, "alice", "mallory")
	alice, mallory := clients[0], clients[1]
	aliceID := meID(t, alice, ts)
	path := "/api/v1/users/" + aliceID + "/avatar"

	if code, b := cReqTyped(t, mallory, ts, "PUT", path, "image/png", aPNG); code != http.StatusForbidden {
		t.Errorf("a colleague set somebody else's picture: %d (%s)", code, b)
	}
	if code, _ := cReq(t, alice, ts, "GET", path, ""); code != http.StatusNotFound {
		t.Error("the refused upload was stored anyway")
	}
	if code, b := cReqTyped(t, admin, ts, "PUT", path, "image/png", aPNG); code != http.StatusNoContent {
		t.Errorf("an administrator could not set it: %d (%s)", code, b)
	}
	// Reading is everybody's, which is the point of having one.
	if code, _ := cReq(t, mallory, ts, "GET", path, ""); code != http.StatusOK {
		t.Error("a colleague cannot see the face beside a name they are shown")
	}
	if code, b := cReq(t, mallory, ts, "DELETE", path, ""); code != http.StatusForbidden {
		t.Errorf("a colleague removed somebody else's picture: %d (%s)", code, b)
	}
}

// TestWhatThisRouteRefusesToServeOnToABrowser.
//
// A vector, whatever it claims, and a body that is not what its header says. The
// second is the one that matters most: without it the format check would be the
// uploader's own word, and the uploader here is every account rather than an
// administrator.
func TestWhatThisRouteRefusesToServeOnToABrowser(t *testing.T) {
	ts, _ := newAuthServer(t, "root", "rootpassword")
	admin := newClient(t)
	if login(t, admin, ts, "root", "rootpassword") != http.StatusOK {
		t.Fatal("admin login failed")
	}
	alice := twoUsers(t, ts, admin, "alice")[0]
	path := "/api/v1/users/" + meID(t, alice, ts) + "/avatar"

	for _, c := range []struct {
		name string
		ct   string
		body string
		want int
	}{
		{"a vector, which a brand mark may be and a face may not", "image/svg+xml", `<svg/>`, http.StatusUnsupportedMediaType},
		{"a type nothing accepts", "application/pdf", "%PDF-1.4", http.StatusUnsupportedMediaType},
		{"a PNG wearing a JPEG's header", "image/jpeg", aPNG, http.StatusBadRequest},
		{"an SVG wearing a PNG's header", "image/png", `<svg onload="alert(1)"/>`, http.StatusBadRequest},
	} {
		if code, b := cReqTyped(t, alice, ts, "PUT", path, c.ct, c.body); code != c.want {
			t.Errorf("%s: %d (%s), want %d", c.name, code, b, c.want)
		}
	}
	if code, _ := cReq(t, alice, ts, "GET", path, ""); code != http.StatusNotFound {
		t.Error("one of the refused bodies was stored")
	}
}

// TestAPictureDoesNotOutliveItsAccount.
//
// Ids are assigned, so a file left behind is not merely untidy: the next account
// handed that id would inherit a stranger's face. Held at the file level rather
// than through the API, because the API cannot see the difference — a deleted
// account answers 404 either way, which is exactly why this is the failure that
// would never be noticed.
func TestAPictureDoesNotOutliveItsAccount(t *testing.T) {
	ts, dir := newAuthServer(t, "root", "rootpassword")
	admin := newClient(t)
	if login(t, admin, ts, "root", "rootpassword") != http.StatusOK {
		t.Fatal("admin login failed")
	}
	alice := twoUsers(t, ts, admin, "alice")[0]
	id := meID(t, alice, ts)
	if code, b := cReqTyped(t, alice, ts, "PUT", "/api/v1/users/"+id+"/avatar",
		"image/png", aPNG); code != http.StatusNoContent {
		t.Fatalf("upload: %d (%s)", code, b)
	}

	if got := avatarFiles(t, dir); len(got) != 1 {
		t.Fatalf("the upload left %d files beside the accounts, want 1: %v", len(got), got)
	}
	if code, b := cReq(t, admin, ts, "DELETE", "/api/v1/users/"+id, ""); code != http.StatusNoContent {
		t.Fatalf("delete user: %d (%s)", code, b)
	}
	if got := avatarFiles(t, dir); len(got) != 0 {
		t.Errorf("the account is gone and its picture is not: %v", got)
	}
	// And the record went with it, or the picture would be the only thing removed.
	if _, err := os.Stat(filepath.Join(dir, "users")); err != nil {
		t.Fatalf("the users directory is gone: %v", err)
	}
}

// TestAPictureIsCarriedByAFullSnapshot.
//
// It lives inside the users directory, which the snapshot walks whole, so nothing
// had to be added to an allowlist. That is the kind of property that is true by
// accident until somebody moves the file, and then silently false.
func TestAPictureIsCarriedByAFullSnapshot(t *testing.T) {
	ts, dir := newAuthServer(t, "root", "rootpassword")
	admin := newClient(t)
	if login(t, admin, ts, "root", "rootpassword") != http.StatusOK {
		t.Fatal("admin login failed")
	}
	alice := twoUsers(t, ts, admin, "alice")[0]
	id := meID(t, alice, ts)
	if code, b := cReqTyped(t, alice, ts, "PUT", "/api/v1/users/"+id+"/avatar",
		"image/png", aPNG); code != http.StatusNoContent {
		t.Fatalf("upload: %d (%s)", code, b)
	}
	hits, err := filepath.Glob(filepath.Join(dir, "users", "*.avatar.*"))
	if err != nil || len(hits) != 1 {
		t.Fatalf("glob: %v %v", hits, err)
	}
	rel, err := filepath.Rel(dir, hits[0])
	if err != nil {
		t.Fatalf("rel: %v", err)
	}
	if got := filepath.ToSlash(rel); !strings.HasPrefix(got, "users/") {
		t.Errorf("the picture is stored at %q, outside the directory a snapshot carries "+
			"with the accounts; it would be backed up separately or not at all", got)
	}
	// And it is not a record, or a listing would try to read a JPEG as an account.
	if strings.HasSuffix(rel, ".json") {
		t.Errorf("the picture is named like a record: %q", rel)
	}
	if code, b := cReq(t, admin, ts, "GET", "/api/v1/users", ""); code != http.StatusOK ||
		!strings.Contains(string(b), fmt.Sprintf("%q", id)) {
		t.Errorf("the account listing no longer reads: %d", code)
	}
}

// TestTheRosterSaysWhichAccountsHaveAFace.
//
// The console draws a picture beside a name only for the accounts that have one,
// and it knows which from the roster rather than by asking per row. Without the
// field on the projection that becomes a request per account that mostly 404s —
// so the field is load-bearing, not decoration.
func TestTheRosterSaysWhichAccountsHaveAFace(t *testing.T) {
	ts, _ := newAuthServer(t, "root", "rootpassword")
	admin := newClient(t)
	if login(t, admin, ts, "root", "rootpassword") != http.StatusOK {
		t.Fatal("admin login failed")
	}
	clients := twoUsers(t, ts, admin, "alice", "mallory")
	alice := clients[0]
	aliceID := meID(t, alice, ts)
	if code, b := cReqTyped(t, alice, ts, "PUT", "/api/v1/users/"+aliceID+"/avatar",
		"image/png", aPNG); code != http.StatusNoContent {
		t.Fatalf("upload: %d (%s)", code, b)
	}

	code, body := cReq(t, admin, ts, "GET", "/api/v1/users", "")
	if code != http.StatusOK {
		t.Fatalf("roster: %d (%s)", code, body)
	}
	var roster []struct {
		ID           string `json:"id"`
		Username     string `json:"username"`
		AvatarSource string `json:"avatarSource"`
	}
	if err := json.Unmarshal(body, &roster); err != nil {
		t.Fatalf("decode roster: %v (%s)", err, body)
	}
	seen := map[string]string{}
	for _, u := range roster {
		seen[u.Username] = u.AvatarSource
	}
	if seen["alice"] != "uploaded" {
		t.Errorf("the roster says alice's provenance is %q, so her face is not drawn", seen["alice"])
	}
	if seen["mallory"] != "" {
		t.Errorf("the roster claims a picture for an account without one (%q), so the "+
			"row asks for bytes that are not there", seen["mallory"])
	}
	// And the secret never travels, which is the whole reason the projection exists.
	if strings.Contains(string(body), "passwordHash") {
		t.Error("the roster carries a password hash")
	}
}
