package api_test

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
)

// A picture from the directory, end to end (ADR-draft-directory-photo).
//
// The decider's own rules are held next door. What this holds is the half that
// only a real run can show: the bytes reach the account's file, the ordinary
// avatar route serves them to whoever is shown the person's name, and a reporting
// run writes none of it.

// photoMessage is a sync message naming one person and one picture.
func photoMessage(apply bool, revision int, photos []map[string]any) string {
	msg := map[string]any{
		"apply":        apply,
		"fromRevision": revision,
		"users": []map[string]any{
			{"id": "oid-ada", "userPrincipalName": "ada@example.org", "displayName": "Ada Lovelace",
				"mail": "ada@example.org", "accountEnabled": true},
		},
		"usersDeltaLink": "https://graph.microsoft.com/v1.0/users/delta?$deltatoken=U1",
		"photos":         photos,
	}
	raw, err := json.Marshal(msg)
	if err != nil {
		panic(err)
	}
	return string(raw)
}

func aDirectoryJPEG(tail string) map[string]any {
	return map[string]any{
		"id":          "oid-ada",
		"contentType": "image/jpeg",
		"data":        base64.StdEncoding.EncodeToString([]byte("\xff\xd8\xff" + tail)),
	}
}

// TestAPictureTheDirectorySentIsServedByTheOrdinaryRoute.
//
// The whole point of putting it beside the account rather than somewhere of its
// own: nothing downstream has to know where a picture came from. The approval
// page, the recipient picker and the portal's corner all read the same route, and
// they neither can nor should tell a mirrored face from an uploaded one.
func TestAPictureTheDirectorySentIsServedByTheOrdinaryRoute(t *testing.T) {
	ts, dir := newAuthServer(t, "root", "rootpassword")
	admin := newClient(t)
	if login(t, admin, ts, "root", "rootpassword") != http.StatusOK {
		t.Fatal("admin login failed")
	}

	// A reporting run first: it must write nothing, pictures included.
	if code, b := cReq(t, admin, ts, "POST", "/api/v1/directory-sync",
		photoMessage(false, 0, []map[string]any{aDirectoryJPEG("ada")})); code != http.StatusOK {
		t.Fatalf("report: %d (%s)", code, b)
	}
	if got, _ := filepath.Glob(filepath.Join(dir, "users", "*.avatar.*")); len(got) != 0 {
		t.Fatalf("a reporting run wrote a picture: %v", got)
	}

	code, raw := cReq(t, admin, ts, "POST", "/api/v1/directory-sync",
		photoMessage(true, 0, []map[string]any{aDirectoryJPEG("ada")}))
	if code != http.StatusOK {
		t.Fatalf("apply: %d (%s)", code, raw)
	}
	if strings.Contains(string(raw), base64.StdEncoding.EncodeToString([]byte("ada"))) {
		t.Error("the report carries the picture's bytes")
	}

	// Which account it landed on, read the way any page would.
	code, body := cReq(t, admin, ts, "GET", "/api/v1/users", "")
	if code != http.StatusOK {
		t.Fatalf("roster: %d (%s)", code, body)
	}
	var roster []struct {
		ID           string `json:"id"`
		DisplayName  string `json:"displayName"`
		AvatarSource string `json:"avatarSource"`
	}
	if err := json.Unmarshal(body, &roster); err != nil {
		t.Fatalf("decode roster: %v", err)
	}
	var ada string
	for _, u := range roster {
		if u.DisplayName == "Ada Lovelace" {
			ada = u.ID
			if u.AvatarSource != "entra" {
				t.Errorf("provenance = %q, want entra — a reader cannot tell where to change it", u.AvatarSource)
			}
		}
	}
	if ada == "" {
		t.Fatalf("the mirror created no account for Ada: %s", body)
	}

	res, err := admin.Get(ts.URL + "/api/v1/users/" + ada + "/avatar")
	if err != nil {
		t.Fatalf("get picture: %v", err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("the ordinary picture route does not serve a mirrored face: %d", res.StatusCode)
	}
	if ct := res.Header.Get("Content-Type"); ct != "image/jpeg" {
		t.Errorf("served as %q", ct)
	}
	if got := res.Header.Get("X-Content-Type-Options"); got != "nosniff" {
		t.Error("a mirrored picture travels without the headers an uploaded one gets")
	}

	// And the directory may take it back.
	if code, b := cReq(t, admin, ts, "POST", "/api/v1/directory-sync",
		photoMessage(true, 1, []map[string]any{{"id": "oid-ada", "removed": true}})); code != http.StatusOK {
		t.Fatalf("removal run: %d (%s)", code, b)
	}
	if code, _ := cReq(t, admin, ts, "GET", "/api/v1/users/"+ada+"/avatar", ""); code != http.StatusNotFound {
		t.Errorf("the picture survived its removal: %d", code)
	}
	if got, _ := filepath.Glob(filepath.Join(dir, "users", "*.avatar.*")); len(got) != 0 {
		t.Errorf("the file survived its removal: %v", got)
	}
}

// TestAMirrorRunLeavesAnUploadedPictureAlone, through the real routes.
//
// The rule is held against the decider next door; this is the same rule where it
// can actually be got wrong — a mirror that wrote the file and then decided not to
// would leave the account wearing a face the record does not claim.
func TestAMirrorRunLeavesAnUploadedPictureAlone(t *testing.T) {
	ts, _ := newAuthServer(t, "root", "rootpassword")
	admin := newClient(t)
	if login(t, admin, ts, "root", "rootpassword") != http.StatusOK {
		t.Fatal("admin login failed")
	}
	// The account exists first, and somebody has chosen a picture for it.
	if code, b := cReq(t, admin, ts, "POST", "/api/v1/directory-sync",
		photoMessage(true, 0, nil)); code != http.StatusOK {
		t.Fatalf("first run: %d (%s)", code, b)
	}
	code, body := cReq(t, admin, ts, "GET", "/api/v1/users", "")
	if code != http.StatusOK {
		t.Fatalf("roster: %d (%s)", code, body)
	}
	var roster []struct {
		ID          string `json:"id"`
		DisplayName string `json:"displayName"`
	}
	if err := json.Unmarshal(body, &roster); err != nil {
		t.Fatalf("decode: %v", err)
	}
	var ada string
	for _, u := range roster {
		if u.DisplayName == "Ada Lovelace" {
			ada = u.ID
		}
	}
	if ada == "" {
		t.Fatalf("no account: %s", body)
	}
	mine := "\x89PNG\r\n\x1a\n" + "the one she chose"
	if code, b := cReqTyped(t, admin, ts, "PUT", "/api/v1/users/"+ada+"/avatar", "image/png", mine); code != http.StatusNoContent {
		t.Fatalf("upload: %d (%s)", code, b)
	}

	// Now the directory offers one of its own.
	if code, b := cReq(t, admin, ts, "POST", "/api/v1/directory-sync",
		photoMessage(true, 1, []map[string]any{aDirectoryJPEG("the tenant's")})); code != http.StatusOK {
		t.Fatalf("second run: %d (%s)", code, b)
	}
	res, err := admin.Get(ts.URL + "/api/v1/users/" + ada + "/avatar")
	if err != nil {
		t.Fatalf("get picture: %v", err)
	}
	defer res.Body.Close()
	if ct := res.Header.Get("Content-Type"); ct != "image/png" {
		t.Errorf("served as %q; the mirror replaced a picture somebody chose", ct)
	}
}
