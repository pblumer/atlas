package sharepoint

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// stubTokens is a TokenSource that returns a fixed token (or an error).
type stubTokens struct {
	tok string
	err error
}

func (s stubTokens) Token(context.Context) (string, error) { return s.tok, s.err }

// graphServer is a fake Microsoft Graph endpoint that records the last create-item
// request and returns a canned response.
type graphServer struct {
	srv      *httptest.Server
	lastPath string
	lastAuth string
	lastBody map[string]any
	status   int
	respBody string
}

func newGraphServer(t *testing.T) *graphServer {
	t.Helper()
	gs := &graphServer{status: 201, respBody: `{"id":"42","webUrl":"https://x"}`}
	gs.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gs.lastPath = r.URL.Path
		gs.lastAuth = r.Header.Get("Authorization")
		raw, _ := io.ReadAll(r.Body)
		gs.lastBody = map[string]any{}
		_ = json.Unmarshal(raw, &gs.lastBody)
		w.WriteHeader(gs.status)
		_, _ = w.Write([]byte(gs.respBody))
	}))
	t.Cleanup(gs.srv.Close)
	return gs
}

func TestGraphClientCreateItem(t *testing.T) {
	gs := newGraphServer(t)
	c := NewGraphClient(stubTokens{tok: "abc"}, gs.srv.URL)
	item, err := c.CreateItem(context.Background(), ItemRequest{
		Site:   "contoso.sharepoint.com,s,w",
		List:   "Incidents",
		Fields: map[string]string{"Title": "Order shipped"},
	})
	if err != nil {
		t.Fatalf("CreateItem: %v", err)
	}
	if gs.lastAuth != "Bearer abc" {
		t.Errorf("Authorization = %q, want the bearer token", gs.lastAuth)
	}
	if !strings.HasSuffix(gs.lastPath, "/lists/Incidents/items") || !strings.Contains(gs.lastPath, "/sites/") {
		t.Errorf("request path = %q, want the site/list items endpoint", gs.lastPath)
	}
	fields, _ := gs.lastBody["fields"].(map[string]any)
	if fields["Title"] != "Order shipped" {
		t.Errorf("request fields = %v, want the item field under fields{}", gs.lastBody)
	}
	m, ok := item.(map[string]any)
	if !ok || m["id"] != "42" {
		t.Errorf("returned item = %#v, want the decoded created item", item)
	}
}

// TestGraphClientDefaultsBase proves an empty base falls back to the Graph v1.0 API.
func TestGraphClientDefaultsBase(t *testing.T) {
	c := NewGraphClient(stubTokens{tok: "x"}, "")
	if c.baseURL != graphDefaultBase {
		t.Errorf("baseURL = %q, want the Graph default %q", c.baseURL, graphDefaultBase)
	}
}

func TestGraphClientCreateItemErrors(t *testing.T) {
	t.Run("missing site", func(t *testing.T) {
		c := NewGraphClient(stubTokens{tok: "x"}, "http://x")
		if _, err := c.CreateItem(context.Background(), ItemRequest{List: "l"}); err == nil {
			t.Error("want an error for a missing site")
		}
	})
	t.Run("missing list", func(t *testing.T) {
		c := NewGraphClient(stubTokens{tok: "x"}, "http://x")
		if _, err := c.CreateItem(context.Background(), ItemRequest{Site: "s"}); err == nil {
			t.Error("want an error for a missing list")
		}
	})
	t.Run("token error", func(t *testing.T) {
		c := NewGraphClient(stubTokens{err: context.DeadlineExceeded}, "http://x")
		if _, err := c.CreateItem(context.Background(), ItemRequest{Site: "s", List: "l"}); err == nil {
			t.Error("want an error when the token cannot be acquired")
		}
	})
	t.Run("non-2xx", func(t *testing.T) {
		gs := newGraphServer(t)
		gs.status = 403
		gs.respBody = `{"error":{"code":"accessDenied"}}`
		c := NewGraphClient(stubTokens{tok: "x"}, gs.srv.URL)
		if _, err := c.CreateItem(context.Background(), ItemRequest{Site: "s", List: "l"}); err == nil {
			t.Error("want an error for a non-2xx Graph response")
		}
	})
	t.Run("empty body", func(t *testing.T) {
		gs := newGraphServer(t)
		gs.status = 204
		gs.respBody = ""
		c := NewGraphClient(stubTokens{tok: "x"}, gs.srv.URL)
		item, err := c.CreateItem(context.Background(), ItemRequest{Site: "s", List: "l"})
		if err != nil || item != nil {
			t.Errorf("empty body: item=%v err=%v, want nil,nil", item, err)
		}
	})
	t.Run("malformed body", func(t *testing.T) {
		gs := newGraphServer(t)
		gs.respBody = `not json`
		c := NewGraphClient(stubTokens{tok: "x"}, gs.srv.URL)
		if _, err := c.CreateItem(context.Background(), ItemRequest{Site: "s", List: "l"}); err == nil {
			t.Error("want an error for a malformed Graph response body")
		}
	})
	t.Run("unreachable", func(t *testing.T) {
		c := NewGraphClient(stubTokens{tok: "x"}, "http://127.0.0.1:0")
		if _, err := c.CreateItem(context.Background(), ItemRequest{Site: "s", List: "l"}); err == nil {
			t.Error("want an error for an unreachable Graph endpoint")
		}
	})
}
