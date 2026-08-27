package webscrape_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/pblumer/atlas/connector/webscrape"
)

func TestHTTPClientScrapeHonorsMaxItems(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte(`<html><body><p class="x">one</p><p class="x">two</p></body></html>`))
	}))
	defer srv.Close()

	got, err := webscrape.NewHTTPClient().Scrape(context.Background(), webscrape.Request{
		URL: srv.URL, Selector: ".x", MaxItems: 1,
	})
	if err != nil {
		t.Fatalf("Scrape: %v", err)
	}
	if len(got) != 1 || got[0] != "one" {
		t.Fatalf("scraped = %v, want [one]", got)
	}
}

func TestHTTPClientScrapeRSS(t *testing.T) {
	var accept string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		accept = r.Header.Get("Accept")
		w.Header().Set("Content-Type", "application/rss+xml")
		_, _ = w.Write([]byte(`<rss version="2.0"><channel><item><title>One</title><link>https://example.com/1</link></item><item><title>Two</title></item></channel></rss>`))
	}))
	defer srv.Close()

	got, err := webscrape.NewHTTPClient().ScrapeFeed(context.Background(), webscrape.Request{
		URL: srv.URL, Format: "rss", MaxItems: 1,
	})
	if err != nil {
		t.Fatalf("ScrapeFeed: %v", err)
	}
	if !strings.Contains(accept, "application/rss+xml") || !strings.Contains(accept, "application/xml") {
		t.Errorf("Accept = %q, want feed media types", accept)
	}
	if len(got) != 1 || got[0].Title != "One" || got[0].Link != "https://example.com/1" {
		t.Fatalf("entries = %+v, want first RSS item", got)
	}
}

func TestHTTPClientScrapeAtom(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/atom+xml")
		_, _ = w.Write([]byte(`<feed xmlns="http://www.w3.org/2005/Atom"><entry><title>One</title><link href="https://example.com/1"/></entry></feed>`))
	}))
	defer srv.Close()

	got, err := webscrape.NewHTTPClient().ScrapeFeed(context.Background(), webscrape.Request{
		URL: srv.URL, Format: "atom",
	})
	if err != nil {
		t.Fatalf("ScrapeFeed: %v", err)
	}
	if len(got) != 1 || got[0].Title != "One" || got[0].Link != "https://example.com/1" {
		t.Fatalf("entries = %+v, want Atom entry", got)
	}
}

func TestHTTPClientRejectsUnknownFeedFormat(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`<rss version="2.0"><channel/></rss>`))
	}))
	defer srv.Close()
	_, err := webscrape.NewHTTPClient().ScrapeFeed(context.Background(), webscrape.Request{URL: srv.URL, Format: "json"})
	if err == nil {
		t.Fatal("ScrapeFeed error = nil, want unsupported format")
	}
}
