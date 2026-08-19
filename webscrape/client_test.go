package webscrape_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/pblumer/atlas/webscrape"
)

const pageHTML = `<html><body>
  <h2 class="title">Alpha</h2>
  <h2 class="title">Beta</h2>
</body></html>`

func TestHTTPClientScrape(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Accept"); got == "" {
			t.Errorf("Accept header = %q, want a non-empty default", got)
		}
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte(pageHTML))
	}))
	defer srv.Close()

	got, err := webscrape.NewHTTPClient().Scrape(context.Background(), webscrape.Request{
		URL:      srv.URL,
		Selector: "h2.title",
	})
	if err != nil {
		t.Fatalf("Scrape: %v", err)
	}
	want := []string{"Alpha", "Beta"}
	if len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("scraped = %v, want %v", got, want)
	}
}

// A non-2xx status fails the scrape so the job is retried (at-least-once).
func TestHTTPClientScrapeNon2xx(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "nope", http.StatusInternalServerError)
	}))
	defer srv.Close()

	_, err := webscrape.NewHTTPClient().Scrape(context.Background(), webscrape.Request{
		URL:      srv.URL,
		Selector: "h2",
	})
	if err == nil {
		t.Fatal("Scrape error = nil, want a non-2xx error")
	}
}

// A transport failure (an unreachable host) fails the scrape.
func TestHTTPClientScrapeFetchError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	url := srv.URL
	srv.Close() // close immediately so the next request is refused

	_, err := webscrape.NewHTTPClient().Scrape(context.Background(), webscrape.Request{
		URL:      url,
		Selector: "h2",
	})
	if err == nil {
		t.Fatal("Scrape error = nil, want a fetch error against a closed server")
	}
}

// An unparseable request URL fails before any fetch.
func TestHTTPClientScrapeBadURL(t *testing.T) {
	_, err := webscrape.NewHTTPClient().Scrape(context.Background(), webscrape.Request{
		URL:      "://not a url",
		Selector: "h2",
	})
	if err == nil {
		t.Fatal("Scrape error = nil, want a build-request error")
	}
}
