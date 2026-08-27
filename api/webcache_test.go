package api_test

import (
	"net/http"
	"testing"
)

// The embedded UI ships as a graph of ES modules that import each other by name. Served
// with no validator — an embedded file has a zero modtime, so http.ServeContent omits
// Last-Modified and http.FileServerFS sets no ETag — a browser is left to guess how long
// each file stays fresh, and it guesses per file. After an upgrade that could hand a
// returning browser a *mixed* UI: one module from the new build, another still from the
// old, and the app dies on an import of something the stale half does not export yet.
//
// Every asset now carries a strong ETag over its own bytes and `Cache-Control: no-cache`
// — "reuse it, but ask first". These tests hold that: the validator is there, an
// unchanged file costs a 304 with no body, and two different files never share a tag.

// get issues a plain GET and returns the response with its body already closed, so a
// test can read headers without leaking the connection.
func get(t *testing.T, url string, header map[string]string) *http.Response {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	for k, v := range header {
		req.Header.Set(k, v)
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET %s: %v", url, err)
	}
	res.Body.Close()
	return res
}

func TestUIAssetsCarryAValidator(t *testing.T) {
	ts := newTestServer(t)

	// The shell and the modules alike: a stale copy of any one of them is what breaks
	// the graph, so every one of them has to be revalidated.
	for _, path := range []string{"/", "/index.html", "/app.js", "/editor.js", "/formviewer.js", "/app.css"} {
		res := get(t, ts.URL+path, nil)
		if res.StatusCode != http.StatusOK {
			t.Fatalf("GET %s status=%d", path, res.StatusCode)
		}
		if got := res.Header.Get("ETag"); got == "" {
			t.Errorf("GET %s: no ETag; a browser has nothing to revalidate against", path)
		}
		if got := res.Header.Get("Cache-Control"); got != "no-cache" {
			t.Errorf("GET %s: Cache-Control = %q, want %q", path, got, "no-cache")
		}
	}
}

func TestUnchangedUIAssetRevalidatesToNotModified(t *testing.T) {
	ts := newTestServer(t)

	first := get(t, ts.URL+"/editor.js", nil)
	tag := first.Header.Get("ETag")
	if tag == "" {
		t.Fatal("no ETag on the first response")
	}

	// "no-cache" keeps the copy and asks; the ask must cost a 304 with no body, or the
	// revalidation would be a full re-download on every navigation.
	again := get(t, ts.URL+"/editor.js", map[string]string{"If-None-Match": tag})
	if again.StatusCode != http.StatusNotModified {
		t.Fatalf("revalidate with the same ETag: status=%d, want %d", again.StatusCode, http.StatusNotModified)
	}
	if n := again.ContentLength; n > 0 {
		t.Errorf("304 carried %d bytes of body", n)
	}

	// A tag from an older build must not match, or the stale copy would be kept.
	stale := get(t, ts.URL+"/editor.js", map[string]string{"If-None-Match": `"0000000000000000"`})
	if stale.StatusCode != http.StatusOK {
		t.Fatalf("revalidate with a stale ETag: status=%d, want %d", stale.StatusCode, http.StatusOK)
	}
}

func TestUIAssetETagsAreDistinctPerFile(t *testing.T) {
	ts := newTestServer(t)

	// The tag is over the file's own bytes. Two modules sharing one would mean a change
	// to either invalidated both — and, worse, that a browser could match the wrong one.
	seen := map[string]string{}
	for _, path := range []string{"/app.js", "/editor.js", "/formviewer.js", "/app.css", "/index.html"} {
		tag := get(t, ts.URL+path, nil).Header.Get("ETag")
		if other, dup := seen[tag]; dup {
			t.Errorf("%s and %s share the ETag %s", path, other, tag)
		}
		seen[tag] = path
	}
}
