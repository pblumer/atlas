package api

import (
	"io/fs"
	"mime"
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestAScriptsTypeIsNotTheHostsOpinion: Go's file server asks the operating system
// what type a file extension is, and on Windows the answer comes from the registry,
// where any installed program may have written it — "application/javascript" on one
// machine, "text/plain" on another. A browser that is told not to sniff
// (withSecurityHeaders) loads a script or a stylesheet only when the answer declares
// it as one, so that opinion decided whether the Console starts. The embedded files
// are Atlas's own and it knows what they are: the type of a script and of a
// stylesheet is said by the server, whatever the host says.
func TestAScriptsTypeIsNotTheHostsOpinion(t *testing.T) {
	// What a host with such a registry does to Go's table, done by hand.
	for ext, wrong := range map[string]string{".js": "text/plain", ".mjs": "text/plain", ".css": "application/octet-stream"} {
		before := mime.TypeByExtension(ext)
		if err := mime.AddExtensionType(ext, wrong); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			if before != "" {
				_ = mime.AddExtensionType(ext, before)
			}
		})
	}

	sub, err := fs.Sub(webFS, "web")
	if err != nil {
		t.Fatal(err)
	}
	h := revalidated(sub, http.FileServerFS(sub))
	for path, want := range map[string]string{
		"/app.js":  "text/javascript; charset=utf-8",
		"/app.css": "text/css; charset=utf-8",
	} {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
		if rec.Code != http.StatusOK {
			t.Fatalf("GET %s: %d", path, rec.Code)
		}
		if got := rec.Header().Get("Content-Type"); got != want {
			t.Errorf("%s is served as %q on a host that calls its extension something else, want %q", path, got, want)
		}
	}

	// A path nothing is stored under is not given a type it has no file for.
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/no-such-file.js", nil))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("GET /no-such-file.js: %d, want 404", rec.Code)
	}
	if got := rec.Header().Get("Content-Type"); got == "text/javascript; charset=utf-8" {
		t.Errorf("a 404 is declared as a script: %q", got)
	}
}
