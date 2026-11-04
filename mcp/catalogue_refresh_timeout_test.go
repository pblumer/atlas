package mcp

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// The catalogue refresh waits longer than the client's own timeout, and only it
// does: a server that answers after the client's ordinary limit still answers the
// refresh, while an ordinary call to the same server gives up.
func TestTheCatalogueRefreshOutwaitsTheClientsOwnTimeout(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		time.Sleep(300 * time.Millisecond)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`[{"item":"vpn","status":"refreshed"}]`))
	}))
	defer ts.Close()
	c := NewClient(ts.URL)
	c.http.Timeout = 50 * time.Millisecond

	var refresh Tool
	for _, tl := range contentTools() {
		if tl.Name == "atlas_refresh_catalog_content" {
			refresh = tl
		}
	}
	if refresh.Handler == nil {
		t.Fatal("atlas_refresh_catalog_content is not a content tool")
	}
	text, err := refresh.Handler(c, map[string]any{"id": "cat_1"})
	if err != nil || !strings.Contains(text, "refreshed") {
		t.Fatalf("refresh = %q, %v; want the answer that came after the client's own timeout", text, err)
	}
	if _, err := c.get("/api/v1/info"); err == nil {
		t.Fatal("an ordinary call outwaited the client's timeout too; the longer wait must be the refresh's alone")
	}
	if c.http.Timeout != 50*time.Millisecond {
		t.Fatalf("the client's own timeout became %v", c.http.Timeout)
	}
}

func TestWithTimeoutLeavesTheClientAsItWas(t *testing.T) {
	c := NewClient("http://127.0.0.1:1")
	long := c.withTimeout(time.Hour)
	if long.http.Timeout != time.Hour || c.http.Timeout != 30*time.Second {
		t.Fatalf("copy = %v, original = %v", long.http.Timeout, c.http.Timeout)
	}
	if long.http.Transport != c.http.Transport {
		t.Fatal("the copy does not share the transport")
	}
}
