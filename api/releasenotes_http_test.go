package api_test

import (
	"encoding/json"
	"net/http"
	"testing"
)

// The Console's release notes through the whole stack: the server answers them from
// the CHANGELOG the binary embeds, and only to somebody signed in
// (ADR-draft-release-notes-from-the-changelog).
func TestReleaseNotesAreServedFromTheEmbeddedChangelog(t *testing.T) {
	ts, _ := newAuthServer(t, "admin", "s3cret-pass")

	if code, _ := doReq(t, ts, http.MethodGet, "/api/v1/release-notes", "", ""); code != http.StatusUnauthorized {
		t.Fatalf("anonymous GET /api/v1/release-notes = %d, want 401: the notes are the Console's, not the login screen's", code)
	}

	c := newClient(t)
	if code := login(t, c, ts, "admin", "s3cret-pass"); code != http.StatusOK {
		t.Fatalf("login = %d", code)
	}
	code, body := cReq(t, c, ts, http.MethodGet, "/api/v1/release-notes", "")
	if code != http.StatusOK {
		t.Fatalf("GET /api/v1/release-notes = %d: %s", code, body)
	}
	var index struct {
		Releases []struct {
			Version     string `json:"version"`
			ChangeCount int    `json:"changeCount"`
		} `json:"releases"`
	}
	if err := json.Unmarshal(body, &index); err != nil || len(index.Releases) == 0 {
		t.Fatalf("index = %s (%v), want at least one release", body, err)
	}

	newest := index.Releases[0]
	code, body = cReq(t, c, ts, http.MethodGet, "/api/v1/release-notes/"+newest.Version, "")
	if code != http.StatusOK {
		t.Fatalf("GET /api/v1/release-notes/%s = %d: %s", newest.Version, code, body)
	}
	var rel struct {
		Version string            `json:"version"`
		Changes []json.RawMessage `json:"changes"`
	}
	if err := json.Unmarshal(body, &rel); err != nil {
		t.Fatalf("release = %s: %v", body, err)
	}
	if rel.Version != newest.Version || len(rel.Changes) != newest.ChangeCount {
		t.Errorf("release %s carries %d changes, the index said %d", rel.Version, len(rel.Changes), newest.ChangeCount)
	}

	if code, _ := cReq(t, c, ts, http.MethodGet, "/api/v1/release-notes/0.0.0-none", ""); code != http.StatusNotFound {
		t.Errorf("an unknown version = %d, want 404", code)
	}
}
