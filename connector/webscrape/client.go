// Package webscrape integrates web scraping as a service-task worker: a BPMN
// web-scraping task fetches a model-authored URL and extracts the elements
// matching a CSS selector through the job path (ADR-0118), mirroring how the rest
// package calls a model-authored HTTP endpoint (ADR-0067). ADR-0190 extends the same
// worker with explicit RSS and Atom extraction modes, and
// ADR-0231 adds per-item fields, richer feed
// entries, and a fetch that copes with the encodings and feed flavors real
// publishers ship. The integration inherits the job protocol's durability and
// non-blocking properties (ADR-0007):
//
//   - A task creates a job carrying the reserved [compiler.WebScrapeJobType].
//     The processor never performs the outbound fetch itself, so it stays
//     allocation-free (invariant I1) and free of any HTTP/HTML/XML dependency.
//   - The in-process [Handler] — a job worker — pulls those jobs, fetches the document
//     off the processor goroutine and after fsync (invariant I2, never inside
//     applyToState / I4), extracts the authored representation, writes it into the
//     task's result variable, and completes the job, which drives the token onward.
//
// Like the REST worker, the URL and extraction settings are authored in the model;
// there is no server-registered worker and no credential. A scrape is a plain GET,
// so an at-least-once retry simply refetches — the operation is idempotent and
// side-effect-free.
package webscrape

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	"github.com/PuerkitoBio/goquery"
	"github.com/andybalholm/cascadia"
	"golang.org/x/net/html"
	"golang.org/x/net/html/atom"
	"golang.org/x/net/html/charset"

	"github.com/pblumer/atlas/connector/nettimeout"
)

const (
	formatHTML = "html"
	formatRSS  = "rss"
	formatAtom = "atom"
)

// UserAgent is what a scrape says it is. Go's default ("Go-http-client/2.0") reads as
// an anonymous script and is refused outright by a large share of sites, which
// reaches the author as a bare 403 with nothing to act on. An honest identity with a
// project link is something a site operator can allow — or block deliberately, which
// is the point (ADR-0231).
const UserAgent = "Atlas-Webscrape/1.0 (+https://github.com/pblumer/atlas)"

// maxBodyBytes bounds one fetched document. In-process workers run on the
// run-loop goroutine, so an endless response is the same hazard as an unbounded
// timeout (see connector/nettimeout): it stalls the engine's single writer and
// balloons memory. 32 MiB is far above any feed or article page and far below trouble.
const maxBodyBytes int64 = 32 << 20

// Field is one named field of a structured HTML scrape. Name is the key the value
// lands under in the item's object; Selector is optional and evaluated within the
// matched item (empty = the item element itself); Attribute is optional (empty = the
// element's text).
type Field struct {
	Name      string `json:"name"`
	Selector  string `json:"selector,omitempty"`
	Attribute string `json:"attribute,omitempty"`
}

// Request is one scrape a web-scraping task performs. URL is the full,
// model-authored document to fetch. Format is the already-compiled representation
// (html/rss/atom); empty retains the pre-ADR-0190 HTML default. Selector/Attribute/
// Fields/AbsoluteLinks apply only to HTML, PlainText only to feeds. MaxItems is the
// deterministic first-N bound; 0 is unlimited.
type Request struct {
	URL           string
	Selector      string
	Attribute     string
	Fields        []Field
	Format        string
	MaxItems      int32
	AbsoluteLinks bool
	PlainText     bool
}

// Client fetches a page and extracts an HTML scrape's matches. It remains the
// original interface so existing HTML clients and tests stay source-compatible.
// Feed-capable clients additionally implement [FeedClient], and clients that can
// assemble one object per match implement [RecordClient].
type Client interface {
	Scrape(ctx context.Context, r Request) ([]string, error)
}

// FeedClient is the optional structured-feed half of a web-scrape client
// (ADR-0190). The built-in HTTPClient implements it; keeping it separate preserves
// source compatibility for HTML-only custom clients.
type FeedClient interface {
	ScrapeFeed(ctx context.Context, r Request) ([]FeedEntry, error)
}

// RecordClient is the optional structured-HTML half (ADR-0231):
// one object per selector match, keyed by the task's authored field names. Separate
// from Client for the same reason FeedClient is — a custom client written for
// ADR-0118 keeps compiling.
type RecordClient interface {
	ScrapeRecords(ctx context.Context, r Request) ([]map[string]string, error)
}

// HTTPClient scrapes a real document over HTTP.
type HTTPClient struct {
	http *http.Client
}

// NewHTTPClient builds a web-scraping HTTP client bounded by the shared worker
// call budget (nettimeout.Default). The worker runs on the run-loop goroutine, so an
// unbounded call would let a hung site stall the whole engine; see nettimeout.
func NewHTTPClient() *HTTPClient {
	return &HTTPClient{http: nettimeout.HTTPClient()}
}

// Scrape GETs r.URL as HTML and extracts the matches of r.Selector. A non-2xx
// status or fetch/parse failure is an error so the job is retried. An empty result
// is valid and becomes an empty JSON array. MaxItems is applied in document order.
func (c *HTTPClient) Scrape(ctx context.Context, r Request) ([]string, error) {
	doc, base, err := c.document(ctx, r)
	if err != nil {
		return nil, err
	}
	return extractMatches(doc, r, base)
}

// ScrapeRecords GETs r.URL as HTML and returns one object per match of r.Selector,
// carrying the task's authored fields. The item selector and the field selectors are
// compiled before the fetch is parsed, so a typo names itself rather than producing an
// empty result.
func (c *HTTPClient) ScrapeRecords(ctx context.Context, r Request) ([]map[string]string, error) {
	doc, base, err := c.document(ctx, r)
	if err != nil {
		return nil, err
	}
	return extractRecords(doc, r, base)
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
	body := cappedBody(resp)
	switch r.Format {
	case formatRSS:
		return extractRSS(body, r.MaxItems, r.PlainText)
	case formatAtom:
		return extractAtom(body, r.MaxItems, r.PlainText)
	default:
		return nil, fmt.Errorf("webscrape: unsupported feed format %q", r.Format)
	}
}

// document fetches an HTML page and parses it, returning the document and the URL it
// was finally served from — the base every relative href on the page is relative to,
// which is the response's URL and not the authored one whenever a redirect moved it.
func (c *HTTPClient) document(ctx context.Context, r Request) (*goquery.Document, *url.URL, error) {
	resp, err := c.get(ctx, r.URL, "text/html,application/xhtml+xml")
	if err != nil {
		return nil, nil, err
	}
	defer resp.Body.Close()
	doc, err := parseHTML(cappedBody(resp), resp.Header.Get("Content-Type"))
	if err != nil {
		return nil, nil, err
	}
	var base *url.URL
	if resp.Request != nil {
		base = resp.Request.URL
	}
	return doc, base, nil
}

func (c *HTTPClient) get(ctx context.Context, url, accept string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("webscrape: build request: %w", err)
	}
	if req.Header.Get("Accept") == "" {
		req.Header.Set("Accept", accept)
	}
	req.Header.Set("User-Agent", UserAgent)
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

func cappedBody(resp *http.Response) io.Reader {
	// One byte of headroom: a document of exactly maxBodyBytes must still reach EOF
	// through the parser's final read rather than failing on the boundary.
	return &cappedReader{r: resp.Body, left: maxBodyBytes + 1}
}

// cappedReader fails the read once a document exceeds maxBodyBytes instead of
// truncating it: half a document parses into a plausible-looking result that is
// silently short, which is worse than a job that says why it stopped.
type cappedReader struct {
	r    io.Reader
	left int64
}

func (c *cappedReader) Read(p []byte) (int, error) {
	if c.left <= 0 {
		return 0, fmt.Errorf("webscrape: document exceeds the %d MiB limit", maxBodyBytes>>20)
	}
	if int64(len(p)) > c.left {
		p = p[:c.left]
	}
	n, err := c.r.Read(p)
	c.left -= int64(n)
	return n, err
}

// parseHTML decodes the page through its declared or sniffed character set before
// parsing. net/html assumes UTF-8, so a Latin-1 page would otherwise parse without
// error into strings whose every umlaut is mojibake — a failure that reaches the
// process as data rather than as an incident.
func parseHTML(body io.Reader, contentType string) (*goquery.Document, error) {
	decoded, err := charset.NewReader(body, contentType)
	if err != nil {
		return nil, fmt.Errorf("webscrape: decode page: %w", err)
	}
	doc, err := goquery.NewDocumentFromReader(decoded)
	if err != nil {
		return nil, fmt.Errorf("webscrape: parse html: %w", err)
	}
	return doc, nil
}

// extract retains the pre-ADR-0190 helper contract for tests and callers inside the
// package: unlimited HTML extraction.
func extract(body io.Reader, selector, attribute string) ([]string, error) {
	return extractHTML(body, selector, attribute, 0)
}

// extractHTML parses HTML from body and returns values from the first maxItems
// selector matches (or all matches when maxItems is zero).
func extractHTML(body io.Reader, selector, attribute string, maxItems int32) ([]string, error) {
	doc, err := parseHTML(body, "")
	if err != nil {
		return nil, err
	}
	return extractMatches(doc, Request{Selector: selector, Attribute: attribute, MaxItems: maxItems}, nil)
}

// extractMatches is the no-fields HTML result (ADR-0118): one string per match. A
// match without the requested attribute contributes no value, but still counts toward
// the authored first-N bound: maxItems limits selector matches, not successful
// attribute reads.
func extractMatches(doc *goquery.Document, r Request, base *url.URL) ([]string, error) {
	sel, err := compileSelector(r.Selector)
	if err != nil {
		return nil, err
	}
	matches := doc.FindMatcher(sel)
	out := make([]string, 0, boundedLen(matches.Length(), r.MaxItems))
	var seen int32
	matches.EachWithBreak(func(_ int, s *goquery.Selection) bool {
		if r.MaxItems > 0 && seen >= r.MaxItems {
			return false
		}
		seen++
		if r.Attribute == "" {
			out = append(out, strings.TrimSpace(s.Text()))
			return true
		}
		if v, ok := s.Attr(r.Attribute); ok {
			out = append(out, linkValue(v, r.Attribute, r.AbsoluteLinks, base))
		}
		return true
	})
	return out, nil
}

// extractRecords is the field result (ADR-0231): the
// selector picks items, and each match becomes one object carrying every authored
// field. A field whose selector matches nothing — or whose attribute the element does
// not carry — is present and empty, so every item has the same shape and a FEEL
// expression can read entry.link without checking whether the key exists.
func extractRecords(doc *goquery.Document, r Request, base *url.URL) ([]map[string]string, error) {
	itemSel, err := compileSelector(r.Selector)
	if err != nil {
		return nil, err
	}
	// Compiled once for the whole document rather than per item: a page with 200 rows
	// and 4 fields would otherwise compile the same four selectors 200 times.
	fieldSels := make([]cascadia.Selector, len(r.Fields))
	for i, f := range r.Fields {
		if f.Selector == "" {
			continue // the item element itself
		}
		if fieldSels[i], err = compileFieldSelector(f); err != nil {
			return nil, err
		}
	}
	items := doc.FindMatcher(itemSel)
	out := make([]map[string]string, 0, boundedLen(items.Length(), r.MaxItems))
	var seen int32
	items.EachWithBreak(func(_ int, item *goquery.Selection) bool {
		if r.MaxItems > 0 && seen >= r.MaxItems {
			return false
		}
		seen++
		record := make(map[string]string, len(r.Fields))
		for i, f := range r.Fields {
			record[f.Name] = fieldValue(item, f, fieldSels[i], r.AbsoluteLinks, base)
		}
		out = append(out, record)
		return true
	})
	return out, nil
}

func fieldValue(item *goquery.Selection, f Field, sel cascadia.Selector, absolute bool, base *url.URL) string {
	target := item
	if sel != nil {
		target = item.FindMatcher(sel).First()
		if target.Length() == 0 {
			return ""
		}
	}
	if f.Attribute == "" {
		return strings.TrimSpace(target.Text())
	}
	v, ok := target.Attr(f.Attribute)
	if !ok {
		return ""
	}
	return linkValue(v, f.Attribute, absolute, base)
}

// linkValue resolves a relative href/src against the page it was read from when the
// task asked for absolute links. A relative path is not data a process can act on —
// it cannot be opened, mailed, or stored — and the base is knowable only here, at the
// response. Other attributes are values, not locations, and are never rewritten.
func linkValue(value, attribute string, absolute bool, base *url.URL) string {
	if !absolute || base == nil {
		return value
	}
	switch strings.ToLower(attribute) {
	case "href", "src":
	default:
		return value
	}
	ref, err := url.Parse(strings.TrimSpace(value))
	if err != nil {
		return value
	}
	return base.ResolveReference(ref).String()
}

func compileSelector(selector string) (cascadia.Selector, error) {
	sel, err := cascadia.Compile(selector)
	if err != nil {
		return nil, fmt.Errorf("webscrape: invalid selector %q: %w", selector, err)
	}
	return sel, nil
}

func compileFieldSelector(f Field) (cascadia.Selector, error) {
	sel, err := cascadia.Compile(f.Selector)
	if err != nil {
		return nil, fmt.Errorf("webscrape: field %q has an invalid selector %q: %w", f.Name, f.Selector, err)
	}
	return sel, nil
}

// textOf renders an HTML fragment as the text a reader would see: markup removed,
// entities decoded, and a single space wherever the markup was a line break or the
// end of a block, so "<p>a</p><p>b</p>" reads "a b" and not "ab".
func textOf(raw string) string {
	// The context node must be a *recognized* element: parsed against an unknown one,
	// the fragment's own tags come back as text and the stripping silently no-ops.
	nodes, err := html.ParseFragment(strings.NewReader(raw), &html.Node{
		Type:     html.ElementNode,
		Data:     "body",
		DataAtom: atom.Body,
	})
	if err != nil {
		return strings.Join(strings.Fields(raw), " ")
	}
	var b strings.Builder
	for _, n := range nodes {
		writeText(&b, n)
	}
	return strings.Join(strings.Fields(b.String()), " ")
}

// blockElements are the tags whose boundary is a visible break in the rendered text.
var blockElements = map[string]bool{
	"br": true, "p": true, "div": true, "li": true, "tr": true, "td": true, "th": true,
	"h1": true, "h2": true, "h3": true, "h4": true, "h5": true, "h6": true,
	"blockquote": true, "section": true, "article": true, "ul": true, "ol": true,
}

func writeText(b *strings.Builder, n *html.Node) {
	switch n.Type {
	case html.TextNode:
		b.WriteString(n.Data)
		return
	case html.ElementNode:
		if blockElements[n.Data] {
			b.WriteByte(' ')
		}
	}
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		writeText(b, c)
	}
	if n.Type == html.ElementNode && blockElements[n.Data] {
		b.WriteByte(' ')
	}
}
