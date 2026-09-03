package webscrape_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/pblumer/atlas/connector/webscrape"
)

// Every request says who it is. Go's default identifies a scrape as an anonymous
// script, which a large share of sites answer with 403 — a failure the author sees as
// a bare status code with nothing to act on.
func TestHTTPClientSendsAnIdentifyingUserAgent(t *testing.T) {
	var agents []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		agents = append(agents, r.Header.Get("User-Agent"))
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte(`<html><body><p class="x">hi</p></body></html>`))
	}))
	defer srv.Close()

	client := webscrape.NewHTTPClient()
	if _, err := client.Scrape(context.Background(), webscrape.Request{URL: srv.URL, Selector: ".x"}); err != nil {
		t.Fatalf("Scrape: %v", err)
	}
	if _, err := client.ScrapeFeed(context.Background(), webscrape.Request{URL: srv.URL, Format: "rss"}); err == nil {
		t.Fatal("ScrapeFeed over an HTML page = nil error, want the format mismatch reported")
	}
	if len(agents) != 2 {
		t.Fatalf("requests = %d, want one page fetch and one feed fetch", len(agents))
	}
	for _, agent := range agents {
		if agent != webscrape.UserAgent {
			t.Errorf("User-Agent = %q, want %q", agent, webscrape.UserAgent)
		}
		if strings.Contains(agent, "Go-http-client") {
			t.Errorf("User-Agent = %q, want Atlas's own identity", agent)
		}
	}
}

// A page whose charset is declared only in the Content-Type header: the response
// header is as authoritative as a <meta>, and the worker honors it.
func TestHTTPClientDecodesTheResponseCharset(t *testing.T) {
	body := []byte{'<', 'p', ' ', 'c', 'l', 'a', 's', 's', '=', '"', 'x', '"', '>',
		'Z', 'i', 'n', 's', 's', 0xE4, 't', 'z', 'e', '<', '/', 'p', '>'} // Latin-1 "Zinssätze"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=ISO-8859-1")
		_, _ = w.Write(body)
	}))
	defer srv.Close()

	got, err := webscrape.NewHTTPClient().Scrape(context.Background(), webscrape.Request{URL: srv.URL, Selector: ".x"})
	if err != nil {
		t.Fatalf("Scrape: %v", err)
	}
	if len(got) != 1 || got[0] != "Zinssätze" {
		t.Fatalf("scraped = %q, want the header's charset honored", got)
	}
}

const ratesPage = `<html><body><table>
  <tr class="row"><td class="term">Fest 5 Jahre</td><td class="rate">1.45%</td><td><a href="detail/5.html">mehr</a></td></tr>
  <tr class="row"><td class="term">Fest 10 Jahre</td><td class="rate">1.72%</td><td><a href="detail/10.html">mehr</a></td></tr>
</table></body></html>`

// The whole structured path over real HTTP, including what "absolute" is relative to:
// the URL the page was finally served from, which a redirect changes.
func TestHTTPClientScrapeRecordsResolvesAgainstTheFinalURL(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/de/zinsen/", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte(ratesPage))
	})
	mux.HandleFunc("/alt", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/de/zinsen/", http.StatusFound)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	got, err := webscrape.NewHTTPClient().ScrapeRecords(context.Background(), webscrape.Request{
		URL: srv.URL + "/alt",
		Fields: []webscrape.Field{
			{Name: "laufzeit", Selector: "td.term"},
			{Name: "zins", Selector: "td.rate"},
			{Name: "link", Selector: "a", Attribute: "href"},
		},
		Selector:      "tr.row",
		AbsoluteLinks: true,
	})
	if err != nil {
		t.Fatalf("ScrapeRecords: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("records = %d, want 2", len(got))
	}
	if got[0]["laufzeit"] != "Fest 5 Jahre" || got[0]["zins"] != "1.45%" {
		t.Errorf("record = %v, want the row's cells", got[0])
	}
	if want := srv.URL + "/de/zinsen/detail/5.html"; got[0]["link"] != want {
		t.Errorf("link = %q, want it resolved against the redirected-to URL %q", got[0]["link"], want)
	}
}

// A fetch failure on the records path is an error, not an empty result: the job
// retries rather than writing "no rates today" into the process.
func TestHTTPClientScrapeRecordsFetchError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "nope", http.StatusServiceUnavailable)
	}))
	defer srv.Close()
	_, err := webscrape.NewHTTPClient().ScrapeRecords(context.Background(), webscrape.Request{
		URL: srv.URL, Selector: "tr", Fields: []webscrape.Field{{Name: "x"}},
	})
	if err == nil {
		t.Fatal("ScrapeRecords error = nil, want the non-2xx surfaced")
	}
}
