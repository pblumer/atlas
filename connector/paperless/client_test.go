package paperless

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

func testClient(t *testing.T, h http.HandlerFunc) (*Client, *httptest.Server) {
	t.Helper()
	s := httptest.NewServer(h)
	c, err := NewClient(s.URL, "secret-token", Options{AllowHTTP: true})
	if err != nil {
		t.Fatal(err)
	}
	return c, s
}

func TestSearchAndGetNeverLeakOCR(t *testing.T) {
	const secret = "FULL-OCR-PRIVATE-DATA"
	c, srv := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Token secret-token" {
			t.Errorf("wrong authorization header")
		}
		if r.Header.Get("Accept") != "application/json; version=10" {
			t.Errorf("wrong api version")
		}
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/documents/":
			fmt.Fprint(w, "{\"count\":1,\"next\":null,\"results\":[{\"id\":3,\"title\":\"Invoice\",\"content\":\""+secret+"\",\"__search_hit__\":{\"highlights\":\""+secret+"\"},\"tags\":[1],\"correspondent\":2,\"document_type\":4}]}")
		case "/api/documents/3/":
			fmt.Fprint(w, "{\"id\":3,\"title\":\"Invoice\",\"content\":\""+secret+"\",\"notes\":[{\"note\":\""+secret+"\"}]}")
		default:
			http.NotFound(w, r)
		}
	})
	defer srv.Close()
	for _, op := range []Request{{Operation: "search-documents", Query: "Invoice"}, {Operation: "get-document", DocumentID: 3}, {Operation: "present-document", DocumentID: 3}} {
		got, err := c.Do(context.Background(), op)
		if err != nil {
			t.Fatalf("%s: %v", op.Operation, err)
		}
		b, err := json.Marshal(got)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(b), secret) || strings.Contains(string(b), "content") || strings.Contains(string(b), "highlights") {
			t.Fatalf("leaked private content: %s", b)
		}
	}
}

func TestEnsureCorrespondentRetryNoDuplicate(t *testing.T) {
	var posts atomic.Int32
	c, srv := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path != "/api/correspondents/" {
			http.NotFound(w, r)
			return
		}
		if r.Method == http.MethodGet {
			if posts.Load() == 0 {
				fmt.Fprint(w, "{\"count\":0,\"next\":null,\"results\":[]}")
			} else {
				fmt.Fprint(w, "{\"count\":1,\"next\":null,\"results\":[{\"id\":42,\"name\":\"Muster AG\",\"matching_algorithm\":0,\"match\":\"\"}]}")
			}
			return
		}
		posts.Add(1)
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		if body["matching_algorithm"] != float64(0) {
			t.Errorf("unexpected default matcher: %v", body)
		}
		fmt.Fprint(w, "{\"id\":42,\"name\":\"Muster AG\",\"matching_algorithm\":0}")
	})
	defer srv.Close()
	req := Request{Operation: "add-correspondent", Name: "Muster AG"}
	for i := 0; i < 2; i++ {
		value, err := c.Do(context.Background(), req)
		if err != nil {
			t.Fatal(err)
		}
		item := value.(CorrespondentResult)
		if item.ID != 42 || item.Created != (i == 0) {
			t.Fatalf("call %d: %#v", i, item)
		}
	}
	if posts.Load() != 1 {
		t.Fatalf("created %d times", posts.Load())
	}
}

func TestNoRawErrorAndNoSecretInURL(t *testing.T) {
	c, srv := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(403)
		fmt.Fprint(w, "TOKEN-LEAK AND OCR-DATA")
	})
	defer srv.Close()
	_, err := c.Do(context.Background(), Request{Operation: "get-document", DocumentID: 3})
	if err == nil {
		t.Fatal("expected denied error")
	}
	if strings.Contains(err.Error(), "TOKEN-LEAK") || strings.Contains(err.Error(), "OCR-DATA") || strings.Contains(err.Error(), "secret-token") {
		t.Fatal("error leaked response/token", err)
	}
}

func TestRejectInsecureAndMalformedInput(t *testing.T) {
	for _, endpoint := range []string{"http://paperless.example.com", "https://user:pw@example.com", "file:///etc/passwd", "https://example.com/?token=abc"} {
		_, err := NewClient(endpoint, "token", Options{})
		if err == nil {
			t.Fatalf("accepted insecure endpoint %q", endpoint)
		}
	}
	c, srv := testClient(t, func(w http.ResponseWriter, r *http.Request) { t.Fatal("invalid request reached server") })
	defer srv.Close()
	for _, r := range []Request{{Operation: "get-document", DocumentID: -1}, {Operation: "add-correspondent", Name: "  "}, {Operation: "add-correspondent", Name: "M", MatchingAlgorithm: 7}, {Operation: "unknown"}} {
		if _, err := c.Do(context.Background(), r); err == nil {
			t.Fatalf("accepted bad request %#v", r)
		}
	}
}

func TestRedirectCannotExfiltrateToken(t *testing.T) {
	var leaked atomic.Bool
	other := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "" {
			leaked.Store(true)
		}
	}))
	defer other.Close()
	c, srv := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, other.URL, http.StatusFound)
	})
	defer srv.Close()
	if _, err := c.Do(context.Background(), Request{Operation: "get-document", DocumentID: 3}); err == nil {
		t.Fatal("accepted redirect")
	}
	if leaked.Load() {
		t.Fatal("redirect forwarded token")
	}
}
