// Package webscrape integrates web scraping as a service-task connector: a BPMN
// web-scraping connector task fetches a model-authored URL and extracts the elements
// matching a CSS selector through the job path (ADR-0118), mirroring how the rest
// package calls a model-authored HTTP endpoint (ADR-0067). ADR-0190 extends the same
// connector with explicit RSS and Atom extraction modes. The integration inherits
// the job protocol's durability and non-blocking properties (ADR-0007):
//
//   - A connector task creates a job carrying the reserved [compiler.WebScrapeJobType].
//     The processor never performs the outbound fetch itself, so it stays
//     allocation-free (invariant I1) and free of any HTTP/HTML/XML dependency.
//   - The in-process [Handler] — a job worker — pulls those jobs, fetches the document
//     off the processor goroutine and after fsync (invariant I2, never inside
//     applyToState / I4), extracts the authored representation, writes it into the
//     task's result variable, and completes the job, which drives the token onward.
//
// Like the REST connector, the URL and extraction settings are authored in the model;
// there is no server-registered connector and no credential. A scrape is a plain GET,
// so an at-least-once retry simply refetches — the operation is idempotent and
// side-effect-free.
package webscrape

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/PuerkitoBio/goquery"
	"github.com/andybalholm/cascadia"

	"github.com/pblumer/atlas/connector/nettimeout"
)

const (
	formatHTML = "html"
	formatRSS  = "rss"
	formatAtom = "atom"
)

// Request is one scrape a web-scraping connector task performs. URL is the full,
// model-authored document to fetch. Format is the already-compiled representation
// (html/rss/atom); empty retains the pre-ADR-0190 HTML default. Selector/Attribute
// apply only to HTML. MaxItems is the deterministic first-N bound; 0 is unlimited.
type Request struct {
	URL       string
	Selector  string
	Attribute string
	Format    string
	MaxItems  int32
}

// Client fetches a page and extracts an HTML scrape's matches. It remains the
// original interface so existing HTML clients and tests stay source-compatible.
// Feed-capable clients additionally implement [FeedClient].
type Client interface {
	Scrape(ctx context.Context, r Request) ([]string, error)
}

// FeedClient is the optional structured-feed half of a web-scrape client
// (ADR-0190). The built-in HTTPClient implements it; keeping it separate preserves
// source compatibility for HTML-only custom clients.
type FeedClient interface {
	ScrapeFeed(ctx context.Context, r Request) ([]FeedEntry, error)
}

// HTTPClient scrapes a real document over HTTP.
type HTTPClient struct {
	http *http.Client
}

// NewHTTPClient builds a web-scraping HTTP client bounded by the shared connector
// call budget (nettimeout.Default). The worker runs on the run-loop goroutine, so an
// unbounded call would let a hung site stall the whole engine; see nettimeout.
func NewHTTPClient() *HTTPClient {
	return &HTTPClient{http: nettimeout.HTTPClient()}
}

// Scrape GETs r.URL as HTML and extracts the matches of r.Selector. A non-2xx
// status or fetch/parse failure is an error so the job is retried. An empty result
// is valid and becomes an empty JSON array. MaxItems is applied in document order.
func (c *HTTPClient) Scrape(ctx context.Context, r Request) ([]string, error) {
	resp, err := c.get(ctx, r.URL, "text/html,application/xhtml+xml")
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	return extractHTML(resp.Body, r.Selector, r.Attribute, r.MaxItems)
}

// ScrapeFeed GETs r.URL and decodes the explicitly authored RSS or Atom format.
// Content-Type is used only for negotiation: r.Format, not the response, selects
// the parser (ADR-0190).
func (c *HTTPClient) ScrapeFeed(ctx context.Context, r Request) ([]FeedEntry, error) {
	accept := "application/rss+xml,application/atom+xml,application/xml,text/xml"
	resp, err := c.get(ctx, r.URL, accept)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	switch r.Format {
	case formatRSS:
		return extractRSS(resp.Body, r.MaxItems)
	case formatAtom:
		return extractAtom(resp.Body, r.MaxItems)
	default:
		return nil, fmt.Errorf("webscrape: unsupported feed format %q", r.Format)
	}
}

func (c *HTTPClient) get(ctx context.Context, url, accept string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("webscrape: build request: %w", err)
	}
	if req.Header.Get("Accept") == "" {
		req.Header.Set("Accept", accept)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("webscrape: fetch %s: %w", url, err)
	}
	if resp.StatusCode/100 != 2 {
		resp.Body.Close()
		return nil, fmt.Errorf("webscrape: fetch %s: unexpected status %d", url, resp.StatusCode)
	}
	return resp, nil
}

// extract retains the pre-ADR-0190 helper contract for tests and callers inside the
// package: unlimited HTML extraction.
func extract(body io.Reader, selector, attribute string) ([]string, error) {
	return extractHTML(body, selector, attribute, 0)
}

// extractHTML parses HTML from body and returns the values of every element matching
// selector, optionally bounded to the first maxItems matches. Each match contributes
// trimmed text when attribute is empty, otherwise the named attribute's value.
func extractHTML(body io.Reader, selector, attribute string, maxItems int32) ([]string, error) {
	sel, err := cascadia.Compile(selector)
	if err != nil {
		return nil, fmt.Errorf("webscrape: invalid selector %q: %w", selector, err)
	}
	doc, err := goquery.NewDocumentFromReader(body)
	if err != nil {
		return nil, fmt.Errorf("webscrape: parse html: %w", err)
	}
	matches := doc.FindMatcher(sel)
	capacity := boundedLen(matches.Length(), maxItems)
	out := make([]string, 0, capacity)
	matches.EachWithBreak(func(_ int, s *goquery.Selection) bool {
		if maxItems > 0 && int32(len(out)) >= maxItems {
			return false
		}
		if attribute == "" {
			out = append(out, strings.TrimSpace(s.Text()))
			return true
		}
		if v, ok := s.Attr(attribute); ok {
			out = append(out, v)
		}
		return true
	})
	return out, nil
}
