package paperless

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestInputValidationAndPagedMetadata(t *testing.T) {
	c, s := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/documents/":
			if r.URL.Query().Get("text") != "alpha" || r.URL.Query().Get("page") != "2" || r.URL.Query().Get("page_size") != "25" {
				t.Error("search paging query", r.URL.RawQuery)
			}
			fmt.Fprint(w, "{\"count\":3,\"next\":\"/api/documents/?page=3\",\"results\":[{\"id\":6,\"title\":\"ok\",\"content\":\"SECRET\"}]}")
		case "/api/correspondents/":
			fmt.Fprint(w, "{\"next\":null,\"results\":[{\"id\":2,\"name\":\"Acme\",\"match\":\"SECRET\"}]}")
		default:
			http.NotFound(w, r)
		}
	})
	defer s.Close()
	for _, r := range []Request{{Operation: "search-documents"}, {Operation: "search-documents", Query: "alpha", Page: -1}, {Operation: "search-documents", Query: "alpha", Page: 10001}, {Operation: "get-document"}, {Operation: "add-correspondent", Name: "a\nb"}, {Operation: "add-correspondent", Name: "x", Match: strings.Repeat("x", 1025)}} {
		if _, err := c.Do(context.Background(), r); err == nil {
			t.Fatalf("expected refusal for %#v", r)
		}
	}
	got, err := c.Do(context.Background(), Request{Operation: "search-documents", Query: "alpha", Page: 2})
	if err != nil {
		t.Fatal(err)
	}
	page := got.(DocumentPage)
	if !page.HasNext || page.Page != 2 || len(page.Documents) != 1 {
		t.Fatalf("unexpected page: %#v", page)
	}
	names, err := c.Do(context.Background(), Request{Operation: "list-correspondents"})
	if err != nil {
		t.Fatal(err)
	}
	b, _ := json.Marshal(names)
	if strings.Contains(string(b), "SECRET") || len(names.([]Correspondent)) != 1 {
		t.Fatalf("unsafe correspondent projection: %s", b)
	}
}

func TestDocumentPresentationRequiresMatchingID(t *testing.T) {
	c, s := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, "{\"id\":9,\"title\":\"Different\"}")
	})
	defer s.Close()
	if _, err := c.Do(context.Background(), Request{Operation: "present-document", DocumentID: 3}); err == nil {
		t.Fatal("must reject inconsistent id")
	}
}

func TestEnsureDoesNotClobberExistingCorrespondent(t *testing.T) {
	c, s := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			t.Error("unexpected rewrite")
		}
		fmt.Fprint(w, "{\"next\":null,\"results\":[{\"id\":99,\"name\":\"Acme\",\"match\":\"different\",\"matching_algorithm\":3,\"is_insensitive\":true}]}")
	})
	defer s.Close()
	for _, r := range []Request{{Operation: "add-correspondent", Name: "Acme"}, {Operation: "add-correspondent", Name: "Acme", Match: "other"}, {Operation: "add-correspondent", Name: "Acme", MatchingAlgorithm: 3}} {
		if _, err := c.Do(context.Background(), r); err == nil {
			t.Fatal("ignored mismatched rules")
		}
	}
}

func TestCorrespondentConflictRecoveredByExactLookup(t *testing.T) {
	calls := 0
	c, s := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Method == http.MethodPost {
			w.WriteHeader(409)
			return
		}
		calls++
		if calls == 1 {
			fmt.Fprint(w, "{\"next\":null,\"results\":[]}")
		} else {
			fmt.Fprint(w, "{\"next\":null,\"results\":[{\"id\":10,\"name\":\"Acme\",\"matching_algorithm\":0}]}")
		}
	})
	defer s.Close()
	v, err := c.Do(context.Background(), Request{Operation: "add-correspondent", Name: "Acme"})
	if err != nil {
		t.Fatal(err)
	}
	if res := v.(CorrespondentResult); res.Created || res.ID != 10 {
		t.Fatalf("retry result: %#v", res)
	}
}

func TestProviderFailuresRefuseAndSanitize(t *testing.T) {
	for _, scenario := range []struct {
		code    int
		body    string
		request Request
	}{{500, "SECRET", Request{Operation: "get-document", DocumentID: 3}}, {200, "not-json", Request{Operation: "get-document", DocumentID: 3}}, {403, "SECRET", Request{Operation: "add-correspondent", Name: "Acme"}}} {
		c, s := testClient(t, func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(scenario.code)
			fmt.Fprint(w, scenario.body)
		})
		_, err := c.Do(context.Background(), scenario.request)
		s.Close()
		if err == nil || strings.Contains(err.Error(), "SECRET") {
			t.Fatalf("unexpected response %v", err)
		}
	}
	c, s := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, "{\"id\":1} {\"id\":2}")
	})
	defer s.Close()
	if _, err := c.Do(context.Background(), Request{Operation: "get-document", DocumentID: 3}); err == nil {
		t.Fatal("accepted trailing JSON")
	}
}

func TestNetworkErrorIsSanitized(t *testing.T) {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	endpoint := s.URL
	s.Close()
	c, err := NewClient(endpoint, "secret", Options{AllowHTTP: true})
	if err != nil {
		t.Fatal(err)
	}
	_, err = c.Do(context.Background(), Request{Operation: "get-document", DocumentID: 3})
	if err == nil || strings.Contains(err.Error(), endpoint) || strings.Contains(err.Error(), "secret") {
		t.Fatalf("leaked request: %v", err)
	}
}
