package api_test

import (
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

// headersOf asks one path and returns the answer's headers.
func headersOf(t *testing.T, srv *httptest.Server, path string) http.Header {
	t.Helper()
	resp, err := srv.Client().Get(srv.URL + path)
	if err != nil {
		t.Fatalf("GET %s: %v", path, err)
	}
	resp.Body.Close()
	return resp.Header
}

// TestEveryAnswerCarriesTheBaselineHeaders: three things a browser should be told
// about an answer used to be said by the handlers that had thought of them, which is
// a dozen out of several hundred. They are said for every answer now — a page, a
// script, a JSON document, a refusal and a path nothing serves alike — so that a
// route written tomorrow has them without its author knowing they exist:
//
//   - the declared content type is the type (no sniffing a document into a script),
//   - the address of a page inside Atlas is not handed to another site as a referrer,
//   - only Atlas itself may put an answer in a frame.
func TestEveryAnswerCarriesTheBaselineHeaders(t *testing.T) {
	srv := newTestServer(t)
	for _, path := range []string{
		"/healthz",
		"/",
		"/app.js",
		"/app.css",
		"/api/v1/stats",
		"/api/v1/process-instances/not-a-key",
		"/nothing/serves/this",
	} {
		h := headersOf(t, srv, path)
		if got := h.Get("X-Content-Type-Options"); got != "nosniff" {
			t.Errorf("%s: X-Content-Type-Options %q, want nosniff", path, got)
		}
		if got := h.Get("Referrer-Policy"); got != "same-origin" {
			t.Errorf("%s: Referrer-Policy %q, want same-origin", path, got)
		}
		if got := h.Get("Content-Security-Policy"); got != "frame-ancestors 'self'" {
			t.Errorf("%s: Content-Security-Policy %q, want frame-ancestors 'self'", path, got)
		}
		if got := h.Get("X-Frame-Options"); got != "SAMEORIGIN" {
			t.Errorf("%s: X-Frame-Options %q, want SAMEORIGIN", path, got)
		}
	}
}

// TestAHandlerThatSaysMoreIsNotOverruled: the baseline is what an answer gets when
// its handler said nothing. A page that keeps itself out of every frame and sends no
// referrer at all still does exactly that — the baseline is written first and the
// handler writes over it — and there is one value of each header, not two for a
// browser to reconcile.
func TestAHandlerThatSaysMoreIsNotOverruled(t *testing.T) {
	srv := newTestServer(t)
	for _, path := range []string{"/public/password", "/public/answer"} {
		h := headersOf(t, srv, path)
		for name, want := range map[string]string{
			"Content-Security-Policy": "frame-ancestors 'none'",
			"X-Frame-Options":         "DENY",
			"Referrer-Policy":         "no-referrer",
			"X-Content-Type-Options":  "nosniff",
		} {
			if got := h.Values(name); len(got) != 1 || got[0] != want {
				t.Errorf("%s: %s = %q, want exactly %q", path, name, got, want)
			}
		}
	}
}

// TestThePublicStartFormStaysFrameable: a public start form may be put on somebody
// else's page in a frame — ADR-0186 names that as one of the two ways of embedding
// it, and sites that do it exist. So the route family of the form is the one place
// the framing rule does not reach. The other two headers do: the link's token is in
// the address, and same-origin keeps it from being sent anywhere as a referrer.
func TestThePublicStartFormStaysFrameable(t *testing.T) {
	srv := newTestServer(t)
	h := headersOf(t, srv, "/public/forms/0123456789abcdef")
	if got := h.Get("X-Frame-Options"); got != "" {
		t.Errorf("the public form page carries X-Frame-Options %q; it is meant to be framed (ADR-0186)", got)
	}
	if got := h.Get("Content-Security-Policy"); strings.Contains(got, "frame-ancestors") {
		t.Errorf("the public form page carries %q; it is meant to be framed (ADR-0186)", got)
	}
	if got := h.Get("X-Content-Type-Options"); got != "nosniff" {
		t.Errorf("the public form page: X-Content-Type-Options %q, want nosniff", got)
	}
	if got := h.Get("Referrer-Policy"); got != "same-origin" {
		t.Errorf("the public form page: Referrer-Policy %q, want same-origin", got)
	}
}

// TestEveryScriptAndStylesheetDeclaresItsType is what nosniff makes load-bearing. A
// browser told not to sniff refuses a script whose answer is not declared as a script
// and a stylesheet that is not declared as one, where it used to guess. Every file
// the Console can load as either is therefore asked for once, through the server, and
// has to say what it is.
func TestEveryScriptAndStylesheetDeclaresItsType(t *testing.T) {
	srv := newTestServer(t)
	web := os.DirFS("web")
	checked := 0
	err := fs.WalkDir(web, ".", func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		var want string
		switch {
		case strings.HasSuffix(path, ".js"):
			want = "text/javascript"
		case strings.HasSuffix(path, ".css"):
			want = "text/css"
		default:
			return nil
		}
		checked++
		h := headersOf(t, srv, "/"+path)
		if got := h.Get("Content-Type"); !strings.HasPrefix(got, want) {
			t.Errorf("/%s is served as %q; with nosniff a browser loads it only as %s", path, got, want)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if checked < 100 {
		t.Fatalf("only %d scripts and stylesheets found under web/; the walk is looking in the wrong place", checked)
	}
}
