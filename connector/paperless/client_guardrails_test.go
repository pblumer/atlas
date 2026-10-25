package paperless

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"testing"
)

func TestPaperlessRefusesOversizedProviderAnswers(t *testing.T) {
	c, server := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, "{\"id\":7,\"title\":\"safe\",\"content\":%q}", strings.Repeat("sensitive", 1<<20))
	})
	defer server.Close()
	_, err := c.Do(context.Background(), Request{Operation: "get-document", DocumentID: 7})
	if err == nil || strings.Contains(err.Error(), "sensitive") {
		t.Fatalf("oversized OCR body must be rejected without leaking its contents: %v", err)
	}
}

func TestPaperlessRefusesProviderPagesOverLimit(t *testing.T) {
	results := make([]map[string]any, 26)
	for i := range results {
		results[i] = map[string]any{"id": i + 1, "title": "ordinary"}
	}
	c, server := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/api/correspondents/" {
			other := make([]map[string]any, len(results))
			for i := range other {
				other[i] = map[string]any{"id": i + 1, "name": "Person"}
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"results": other, "next": nil})
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"results": results, "next": nil})
	})
	defer server.Close()
	if _, err := c.Do(context.Background(), Request{Operation: "search-documents", Query: "ordinary"}); err == nil {
		t.Fatal("accepted search results beyond requested page size")
	}
	if _, err := c.Do(context.Background(), Request{Operation: "list-correspondents"}); err == nil {
		t.Fatal("accepted correspondent results beyond requested page size")
	}
}

func TestPaperlessCorrespondentSearchMustTerminateBeforeCreating(t *testing.T) {
	calls := 0
	c, server := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Error("must not create when catalogue is incomplete")
			return
		}
		calls++
		if got := r.URL.Query().Get("page"); got != strconv.Itoa(calls) {
			t.Errorf("incorrect page: %s vs %d", got, calls)
		}
		fmt.Fprint(w, "{\"next\":\"/api/correspondents/?page=more\",\"results\":[]}")
	})
	defer server.Close()
	if _, err := c.Do(context.Background(), Request{Operation: "add-correspondent", Name: "Unknown"}); err == nil {
		t.Fatal("accepted incomplete scan as proof of absence")
	}
	if calls != 50 {
		t.Fatalf("expected a bounded 50-page scan, got %d", calls)
	}
}

func TestPaperlessCorrespondentAmbiguityFailsClosed(t *testing.T) {
	c, server := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, "{\"next\":null,\"results\":[{\"id\":2,\"name\":\"Example\"},{\"id\":3,\"name\":\"example\"}]}")
	})
	defer server.Close()
	if _, err := c.Do(context.Background(), Request{Operation: "add-correspondent", Name: "Example"}); err == nil {
		t.Fatal("an ambiguous name must not be assumed to refer to either correspondent")
	}
}

func TestPaperlessCreateConflictDoesNotInventSuccess(t *testing.T) {
	for _, status := range []int{http.StatusBadRequest, http.StatusConflict} {
		t.Run(strconv.Itoa(status), func(t *testing.T) {
			calls := 0
			c, server := testClient(t, func(w http.ResponseWriter, r *http.Request) {
				if r.Method == http.MethodPost {
					w.WriteHeader(status)
					fmt.Fprint(w, "{\"detail\":\"SECRET RESPONSE\"}")
					return
				}
				calls++
				fmt.Fprint(w, "{\"next\":null,\"results\":[]}")
			})
			defer server.Close()
			_, err := c.Do(context.Background(), Request{Operation: "add-correspondent", Name: "Acme"})
			if err == nil || strings.Contains(err.Error(), "SECRET RESPONSE") || calls != 2 {
				t.Fatalf("expected safe conflict after one retry lookup: calls=%d, err=%v", calls, err)
			}
		})
	}
}

func TestPaperlessCreateRequiresAUsableID(t *testing.T) {
	c, server := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			w.WriteHeader(http.StatusCreated)
			fmt.Fprint(w, "{\"name\":\"Acme\"}")
			return
		}
		fmt.Fprint(w, "{\"next\":null,\"results\":[]}")
	})
	defer server.Close()
	if _, err := c.Do(context.Background(), Request{Operation: "add-correspondent", Name: "Acme"}); err == nil {
		t.Fatal("an accepted correspondent without an ID is not usable")
	}
}

func TestPaperlessCorrespondentCaseSensitivityIsPreserved(t *testing.T) {
	insensitive := false
	c, server := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, "{\"next\":null,\"results\":[{\"id\":2,\"name\":\"Example\",\"is_insensitive\":true}]}")
	})
	defer server.Close()
	if _, err := c.Do(context.Background(), Request{Operation: "add-correspondent", Name: "Example", IsInsensitive: &insensitive}); err == nil {
		t.Fatal("must not silently change an existing matching rule")
	}
}

func TestPaperlessDoesNotLeakNetworkDetailsOnCancel(t *testing.T) {
	c, server := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		t.Error("cancelled request reached remote server")
	})
	defer server.Close()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := c.Do(ctx, Request{Operation: "get-document", DocumentID: 1})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled network operation must preserve cancellation status: %v", err)
	}
}

func TestPaperlessClientDoesNotMutateSharedHTTPClient(t *testing.T) {
	original := &http.Client{}
	c, err := NewClient("https://paperless.example.test", "token", Options{HTTPClient: original})
	if err != nil {
		t.Fatal(err)
	}
	if c.http.Timeout == 0 || original.Timeout != 0 || original.CheckRedirect != nil {
		t.Fatal("client must install timeout and redirect policy on a private copy")
	}
	if _, err := NewClient("https://paperless.example.test", "\nsecret", Options{}); err == nil {
		t.Fatal("accepted header injection as credential")
	}
}
