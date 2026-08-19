package api

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

// TestPublicLinkTokenCannotEscapeTheStore is a regression test for a path
// traversal on the *unauthenticated* public-form endpoints. A link token names
// its own file (tokens are hex, so they are already filename-safe), and the token
// arrives straight from the URL. http.ServeMux unescapes a path value, so
// "%2e%2e%2f" reaches the handler as "../" and still matches {token} — which means
// an unguarded token would address a JSON file anywhere on disk.
//
// The store must treat a key that is not a valid record name as a miss rather
// than a path.
func TestPublicLinkTokenCannotEscapeTheStore(t *testing.T) {
	srv, _ := newValidateServer(t)

	// A JSON file outside the public-links directory that would deserialize into a
	// publicLink if it were ever read.
	outside := filepath.Dir(srv.publicLinks.Dir())
	if err := os.WriteFile(filepath.Join(outside, "secret.json"),
		[]byte(`{"token":"secret","processId":"p","formId":"f"}`), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	for _, token := range []string{"../secret", "..%2fsecret", "../../etc/passwd"} {
		if _, ok, err := srv.publicLinks.Get(token); ok || err != nil {
			t.Errorf("Get(%q) = ok %v, err %v; want a clean miss", token, ok, err)
		}
	}

	// And over HTTP, through the mux that produces the unescaped value.
	req := httptest.NewRequest(http.MethodGet, "/public/forms/%2e%2e%2fsecret/schema", nil)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code == http.StatusOK {
		t.Errorf("a traversing token resolved a link: status %d, body %s", rec.Code, rec.Body.String())
	}
}
