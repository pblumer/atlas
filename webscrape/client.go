// Package webscrape integrates web scraping as a service-task connector: a BPMN
// web-scraping connector task fetches a model-authored URL and extracts the elements
// matching a CSS selector through the job path (ADR-0117), mirroring how the rest
// package calls a model-authored HTTP endpoint (ADR-0067). The integration inherits
// the job protocol's durability and non-blocking properties (ADR-0007):
//
//   - A connector task creates a job carrying the reserved [compiler.WebScrapeJobType].
//     The processor never performs the outbound fetch itself, so it stays
//     allocation-free (invariant I1) and free of any HTTP/HTML dependency.
//   - The in-process [Handler] — a job worker — pulls those jobs, fetches the page off
//     the processor goroutine and after fsync (invariant I2, never inside
//     applyToState / I4), extracts the matches, writes them into the task's result
//     variable as a JSON array, and completes the job, which drives the token onward.
//
// Like the REST connector, the URL and selector are authored in the model; there is
// no server-registered connector and no credential. A scrape is a plain GET, so an
// at-least-once retry (a crash between "the page was fetched" and "job completed")
// simply refetches — the operation is idempotent and side-effect-free.
package webscrape

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/PuerkitoBio/goquery"
	"github.com/andybalholm/cascadia"
)

// Request is one scrape a web-scraping connector task performs. URL is the full,
// model-authored page to fetch. Selector is the CSS selector whose matches are
// extracted. Attribute, when non-empty, names the HTML attribute read from each
// match; empty means read each match's text content.
type Request struct {
	URL       string
	Selector  string
	Attribute string
}

// Client fetches a page and extracts a scrape's matches. It is an interface so the
// worker is testable without a live server.
type Client interface {
	Scrape(ctx context.Context, r Request) ([]string, error)
}

// HTTPClient scrapes a real page over HTTP. It GETs Request.URL, parses the HTML,
// and returns the text (or the named attribute) of every element matching
// Request.Selector. A non-2xx status is returned as an error so the job stays
// pending and is retried (at-least-once).
type HTTPClient struct {
	http *http.Client
}

// NewHTTPClient builds a web-scraping HTTP client backed by http.DefaultClient. A
// configurable timeout is a follow-up (ADR-0117).
func NewHTTPClient() *HTTPClient {
	return &HTTPClient{http: http.DefaultClient}
}

// Scrape GETs r.URL, parses the response as HTML, and extracts the matches of
// r.Selector. A non-2xx status or a fetch/parse failure is an error so the job is
// retried. An empty result (the selector matched nothing) is not an error — the task
// writes an empty JSON array.
func (c *HTTPClient) Scrape(ctx context.Context, r Request) ([]string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, r.URL, nil)
	if err != nil {
		return nil, fmt.Errorf("webscrape: build request: %w", err)
	}
	if req.Header.Get("Accept") == "" {
		req.Header.Set("Accept", "text/html,application/xhtml+xml")
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("webscrape: fetch %s: %w", r.URL, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		return nil, fmt.Errorf("webscrape: fetch %s: unexpected status %d", r.URL, resp.StatusCode)
	}
	return extract(resp.Body, r.Selector, r.Attribute)
}

// extract parses HTML from body and returns the values of every element matching
// selector: each match's trimmed text when attribute is empty, otherwise the named
// attribute's value for the matches that carry it (a match without the attribute is
// skipped, so `href` over a mix of anchors and non-anchors yields only real links).
// An invalid selector is an error (a modeling mistake surfaces as an incident rather
// than a silent empty result). The returned slice is non-nil even when empty, so the
// task always writes a JSON array rather than null.
func extract(body io.Reader, selector, attribute string) ([]string, error) {
	// Compile the selector explicitly: goquery.Find silently yields an empty
	// selection for an invalid selector, which would hide a modeling mistake as a
	// legitimate no-match. Compiling surfaces it as a job error (incident) instead.
	sel, err := cascadia.Compile(selector)
	if err != nil {
		return nil, fmt.Errorf("webscrape: invalid selector %q: %w", selector, err)
	}
	doc, err := goquery.NewDocumentFromReader(body)
	if err != nil {
		return nil, fmt.Errorf("webscrape: parse html: %w", err)
	}
	matches := doc.FindMatcher(sel)
	out := make([]string, 0, matches.Length())
	matches.Each(func(_ int, s *goquery.Selection) {
		if attribute == "" {
			out = append(out, strings.TrimSpace(s.Text()))
			return
		}
		if v, ok := s.Attr(attribute); ok {
			out = append(out, v)
		}
	})
	return out, nil
}
