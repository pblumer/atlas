package api_test

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A picture of the person behind an account (ADR-0368), end to end.
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

// TestThePictureRoutesSayWhichThingIsMissing.
//
// Three refusals that are easy to collapse into one and must not be. "Not yours"
// and "no such account" are different sentences: a caller who may not act on
// somebody else's account learns nothing from being told which ids exist, so the
// gate is asked first and answers without touching the store. And an id nothing
// answers to is a 404 rather than a silent success on a file beside no record.
func TestThePictureRoutesSayWhichThingIsMissing(t *testing.T) {
	ts, dir := newAuthServer(t, "root", "rootpassword")
	admin := newClient(t)
	if login(t, admin, ts, "root", "rootpassword") != http.StatusOK {
		t.Fatal("admin login failed")
	}
	ghost := "/api/v1/users/usr_nobodyhasthisid/avatar"

	// An administrator may act on any account, so what stops this is that there is
	// no account — which is the answer the store gives and the gate cannot.
	if code, b := cReqTyped(t, admin, ts, "PUT", ghost, "image/png", aPNG); code != http.StatusNotFound {
		t.Errorf("setting a picture on an id nothing answers to: %d (%s), want 404", code, b)
	}
	if code, b := cReq(t, admin, ts, "DELETE", ghost, ""); code != http.StatusNotFound {
		t.Errorf("removing a picture from an id nothing answers to: %d (%s), want 404", code, b)
	}
	if code, _ := cReq(t, admin, ts, "GET", ghost, ""); code != http.StatusNotFound {
		t.Error("an id nothing answers to has a picture")
	}
	// And nothing was written beside a record that does not exist.
	if got := avatarFiles(t, dir); len(got) != 0 {
		t.Errorf("a refused write left %v", got)
	}

	// A caller with no session at all. The gate answers before the store is read,
	// so this is a refusal and not a 404 that would enumerate ids.
	anon := newClient(t)
	alice := twoUsers(t, ts, admin, "alice")[0]
	path := "/api/v1/users/" + meID(t, alice, ts) + "/avatar"
	if code, b := cReqTyped(t, anon, ts, "PUT", path, "image/png", aPNG); code != http.StatusUnauthorized &&
		code != http.StatusForbidden {
		t.Errorf("a caller with no session set a picture: %d (%s)", code, b)
	}
}

// TestWithEnforcementOffThereIsNobodyToBe.
//
// The rule every other gate in the product states: enforcement off means there is
// nobody to be, not nobody who may. It is worth a test of its own because the
// opposite reading is the one that gets written by accident — a nil principal
// looks like "not allowed" to anybody reading the gate in isolation — and it has
// already cost this product one round, on the catalogue's appearance.
func TestWithEnforcementOffThereIsNobodyToBe(t *testing.T) {
	ts := newTestServer(t)
	code, body := doReq(t, ts, "POST", "/api/v1/users",
		`{"username":"arno","password":"password1"}`, "application/json")
	if code != http.StatusCreated {
		t.Fatalf("create user: %d (%s)", code, body)
	}
	var made struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(body, &made); err != nil {
		t.Fatalf("decode: %v (%s)", err, body)
	}
	path := "/api/v1/users/" + made.ID + "/avatar"
	if code, b := doReq(t, ts, "PUT", path, aPNG, "image/png"); code != http.StatusNoContent {
		t.Fatalf("with enforcement off a picture could not be set: %d (%s)", code, b)
	}
	if code, _ := doReq(t, ts, "GET", path, "", ""); code != http.StatusOK {
		t.Error("the picture that was just set cannot be read")
	}
	if code, b := doReq(t, ts, "DELETE", path, "", ""); code != http.StatusNoContent {
		t.Errorf("with enforcement off a picture could not be removed: %d (%s)", code, b)
	}
}

// putRaw is cReqTyped for a body that may be empty. cReqTyped omits the
// Content-Type header when there is nothing to send, which is right for it and
// wrong here: an empty body under a declared type is precisely one of the cases
// this route has to answer for.
func putRaw(t *testing.T, c *http.Client, ts *httptest.Server, path, contentType, body string) (int, []byte) {
	t.Helper()
	req, err := http.NewRequest("PUT", ts.URL+path, strings.NewReader(body))
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Content-Type", contentType)
	res, err := c.Do(req)
	if err != nil {
		t.Fatalf("PUT %s: %v", path, err)
	}
	defer res.Body.Close()
	out, _ := io.ReadAll(res.Body)
	return res.StatusCode, out
}

// TestAPictureIsNeitherNothingNorEverything.
//
// The two bounds on the body, and both are refusals rather than corrections. An
// empty body under a declared type is not a picture and must not be stored as one;
// an over-large body is refused whole rather than truncated, because half a JPEG
// is not a smaller JPEG — it would pass the format check, since the magic is at
// the front, and land as a broken image nobody could explain.
func TestAPictureIsNeitherNothingNorEverything(t *testing.T) {
	ts, dir := newAuthServer(t, "root", "rootpassword")
	admin := newClient(t)
	if login(t, admin, ts, "root", "rootpassword") != http.StatusOK {
		t.Fatal("admin login failed")
	}
	alice := twoUsers(t, ts, admin, "alice")[0]
	path := "/api/v1/users/" + meID(t, alice, ts) + "/avatar"

	// The empty body is checked by its *answer*, not by its status. The content
	// check would refuse it too — empty bytes are not a PNG — so a 400 proves
	// nothing about the case that exists for it. What the separate case buys is the
	// sentence: somebody who uploaded nothing is told they uploaded nothing, rather
	// than being told the bytes they did not send are the wrong shape.
	code, b := putRaw(t, alice, ts, path, "image/png", "")
	if code != http.StatusBadRequest {
		t.Errorf("an empty body: %d (%s), want 400", code, b)
	}
	if !strings.Contains(string(b), "empty") {
		t.Errorf("an empty upload is answered %q, which describes the bytes rather than "+
			"the fact that there are none", b)
	}
	// One byte past the budget. The refusal names the figure rather than repeating
	// it here, so this asks only that the body was not accepted.
	big := aPNG + strings.Repeat("x", (512<<10)+1-len(aPNG))
	if code, over := putRaw(t, alice, ts, path, "image/png", big); code != http.StatusRequestEntityTooLarge {
		t.Errorf("a body past the budget: %d (%s), want 413", code, over)
	}
	if got := avatarFiles(t, dir); len(got) != 0 {
		t.Errorf("a refused body was stored: %v", got)
	}
}
