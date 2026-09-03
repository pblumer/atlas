package webscrape

import (
	"encoding/xml"
	"fmt"
	"io"
	"strings"

	"golang.org/x/net/html/charset"
)

// FeedEntry is the stable structured result one RSS item or Atom entry produces
// (ADR-0190, extended by ADR-0231). Every field is
// always serialized; a source that omits one leaves it empty rather than making
// Atlas invent data. Categories is always a list, empty when the source names none.
//
// Guid is the entry's own identity as the publisher states it (RSS <guid>, Atom
// <id>) — it is what a recurring scrape deduplicates on across runs, which a title
// cannot do because publishers edit titles.
type FeedEntry struct {
	Title       string   `json:"title"`
	Link        string   `json:"link"`
	Description string   `json:"description"`
	Published   string   `json:"published"`
	Guid        string   `json:"guid"`
	Author      string   `json:"author"`
	Categories  []string `json:"categories"`
	Image       string   `json:"image"`
}

// RSS 2.0 / 0.9x: <rss><channel><item>. RSS 1.0 is RDF and puts the items beside the
// channel instead: <rdf:RDF><channel/><item>. Both are what an author means by
// format="rss", so both decode into the same items (ADR-0231).
type rssDocument struct {
	XMLName xml.Name   `xml:"rss"`
	Channel rssChannel `xml:"channel"`
}

type rdfDocument struct {
	XMLName xml.Name  `xml:"RDF"`
	Items   []rssItem `xml:"item"`
}

type rssChannel struct {
	Items []rssItem `xml:"item"`
}

// rssItem reads the namespaced extensions publishers actually use alongside plain
// RSS: dc:creator and dc:date, and media:content/media:thumbnail for the image.
// encoding/xml matches on local name when the tag names no namespace, so Creator
// covers dc:creator whatever prefix the feed binds it to. Media also matches
// <content:encoded>, whose url attribute is absent — which is exactly how it is
// skipped when the image is picked.
type rssItem struct {
	Title       string         `xml:"title"`
	Link        string         `xml:"link"`
	Description string         `xml:"description"`
	Published   string         `xml:"pubDate"`
	Date        string         `xml:"date"`
	Guid        string         `xml:"guid"`
	Author      string         `xml:"author"`
	Creator     string         `xml:"creator"`
	Categories  []string       `xml:"category"`
	Enclosures  []rssEnclosure `xml:"enclosure"`
	Media       []mediaRef     `xml:"content"`
	Thumbnails  []mediaRef     `xml:"thumbnail"`
}

type rssEnclosure struct {
	URL  string `xml:"url,attr"`
	Type string `xml:"type,attr"`
}

type mediaRef struct {
	URL    string `xml:"url,attr"`
	Type   string `xml:"type,attr"`
	Medium string `xml:"medium,attr"`
}

type atomDocument struct {
	XMLName xml.Name    `xml:"feed"`
	Entries []atomEntry `xml:"entry"`
}

type atomEntry struct {
	Title      string         `xml:"title"`
	Links      []atomLink     `xml:"link"`
	Summary    string         `xml:"summary"`
	Content    string         `xml:"content"`
	Published  string         `xml:"published"`
	Updated    string         `xml:"updated"`
	ID         string         `xml:"id"`
	Authors    []atomAuthor   `xml:"author"`
	Categories []atomCategory `xml:"category"`
}

type atomLink struct {
	Rel  string `xml:"rel,attr"`
	Href string `xml:"href,attr"`
	Type string `xml:"type,attr"`
}

type atomAuthor struct {
	Name string `xml:"name"`
}

// An Atom category carries its value in an attribute, not as text: term is the
// machine-readable one, label the display form when the feed gives one.
type atomCategory struct {
	Term  string `xml:"term,attr"`
	Label string `xml:"label,attr"`
}

// feedDecoder builds the XML decoder every feed parse uses. Two settings matter for
// documents that exist rather than documents that validate: CharsetReader accepts a
// feed that declares a non-UTF-8 encoding (a plain decoder fails the job outright on
// ISO-8859-1), and the HTML entity table plus non-strict mode survive the &nbsp; and
// bare & that publishers ship (ADR-0231).
func feedDecoder(body io.Reader) *xml.Decoder {
	dec := xml.NewDecoder(body)
	dec.CharsetReader = charset.NewReaderLabel
	dec.Entity = xml.HTMLEntity
	dec.Strict = false
	return dec
}

// feedRoot advances to the document's first element and returns it. A feed that is
// really an HTML error page — or the other feed format — is the common authoring
// mistake, and the caller turns this into a message naming the Format setting rather
// than an XML syntax error at line 1.
func feedRoot(dec *xml.Decoder) (xml.StartElement, error) {
	for {
		tok, err := dec.Token()
		if err != nil {
			return xml.StartElement{}, err
		}
		if start, ok := tok.(xml.StartElement); ok {
			return start, nil
		}
	}
}

// wrongRootError names what the document actually is, in the vocabulary of the
// Format setting the author picked — the one thing that turns a parse failure into a
// fix.
func wrongRootError(format, root string) error {
	switch strings.ToLower(root) {
	case "html":
		return fmt.Errorf("webscrape: the document is HTML, not %s: set the task's Format to HTML and author a CSS selector", format)
	case "feed":
		return fmt.Errorf("webscrape: the document is an Atom feed, not %s: set the task's Format to Atom", format)
	case "rss", "rdf":
		return fmt.Errorf("webscrape: the document is an RSS feed, not %s: set the task's Format to RSS", format)
	default:
		return fmt.Errorf("webscrape: the document root is <%s>, which is not %s", root, format)
	}
}

func extractRSS(body io.Reader, maxItems int32, plainText bool) ([]FeedEntry, error) {
	dec := feedDecoder(body)
	root, err := feedRoot(dec)
	if err != nil {
		return nil, fmt.Errorf("webscrape: parse rss: %w", err)
	}
	var items []rssItem
	switch root.Name.Local {
	case "rss":
		var doc rssDocument
		if err := dec.DecodeElement(&doc, &root); err != nil {
			return nil, fmt.Errorf("webscrape: parse rss: %w", err)
		}
		items = doc.Channel.Items
	case "RDF":
		var doc rdfDocument
		if err := dec.DecodeElement(&doc, &root); err != nil {
			return nil, fmt.Errorf("webscrape: parse rss: %w", err)
		}
		items = doc.Items
	default:
		return nil, wrongRootError("an RSS feed", root.Name.Local)
	}
	limit := boundedLen(len(items), maxItems)
	out := make([]FeedEntry, 0, limit)
	for i := 0; i < limit; i++ {
		out = append(out, rssEntry(items[i], plainText))
	}
	return out, nil
}

func rssEntry(item rssItem, plainText bool) FeedEntry {
	published := strings.TrimSpace(item.Published)
	if published == "" {
		published = strings.TrimSpace(item.Date) // dc:date, which is how RSS 1.0 dates an item
	}
	author := strings.TrimSpace(item.Author)
	if author == "" {
		author = strings.TrimSpace(item.Creator)
	}
	return FeedEntry{
		Title:       strings.TrimSpace(item.Title),
		Link:        strings.TrimSpace(item.Link),
		Description: description(item.Description, plainText),
		Published:   published,
		Guid:        strings.TrimSpace(item.Guid),
		Author:      author,
		Categories:  trimmedList(item.Categories),
		Image:       rssImage(item),
	}
}

// rssImage picks the entry's image from the three places publishers put one, in the
// order of how explicitly each says "this is the image": a typed enclosure, then
// media:content, then media:thumbnail. An enclosure that is an audio or video file is
// not an image and is skipped rather than becoming a broken <img>.
func rssImage(item rssItem) string {
	for _, enc := range item.Enclosures {
		if strings.HasPrefix(strings.ToLower(strings.TrimSpace(enc.Type)), "image/") {
			return strings.TrimSpace(enc.URL)
		}
	}
	for _, m := range append(append([]mediaRef{}, item.Media...), item.Thumbnails...) {
		url := strings.TrimSpace(m.URL)
		if url == "" {
			continue // <content:encoded>, which shares the local name and carries no url
		}
		typ := strings.ToLower(strings.TrimSpace(m.Type))
		medium := strings.ToLower(strings.TrimSpace(m.Medium))
		if typ == "" && medium == "" || strings.HasPrefix(typ, "image/") || medium == "image" {
			return url
		}
	}
	return ""
}

func extractAtom(body io.Reader, maxItems int32, plainText bool) ([]FeedEntry, error) {
	dec := feedDecoder(body)
	root, err := feedRoot(dec)
	if err != nil {
		return nil, fmt.Errorf("webscrape: parse atom: %w", err)
	}
	if root.Name.Local != "feed" {
		return nil, wrongRootError("an Atom feed", root.Name.Local)
	}
	var doc atomDocument
	if err := dec.DecodeElement(&doc, &root); err != nil {
		return nil, fmt.Errorf("webscrape: parse atom: %w", err)
	}
	limit := boundedLen(len(doc.Entries), maxItems)
	out := make([]FeedEntry, 0, limit)
	for i := 0; i < limit; i++ {
		out = append(out, atomFeedEntry(doc.Entries[i], plainText))
	}
	return out, nil
}

func atomFeedEntry(entry atomEntry, plainText bool) FeedEntry {
	summary := strings.TrimSpace(entry.Summary)
	if summary == "" {
		summary = strings.TrimSpace(entry.Content)
	}
	published := strings.TrimSpace(entry.Published)
	if published == "" {
		published = strings.TrimSpace(entry.Updated)
	}
	var author string
	if len(entry.Authors) > 0 {
		author = strings.TrimSpace(entry.Authors[0].Name)
	}
	categories := make([]string, 0, len(entry.Categories))
	for _, c := range entry.Categories {
		// The label is the human-readable form when a feed gives one; term is the
		// machine value every Atom category has.
		if label := strings.TrimSpace(c.Label); label != "" {
			categories = append(categories, label)
			continue
		}
		if term := strings.TrimSpace(c.Term); term != "" {
			categories = append(categories, term)
		}
	}
	return FeedEntry{
		Title:       strings.TrimSpace(entry.Title),
		Link:        atomAlternateLink(entry.Links),
		Description: description(summary, plainText),
		Published:   published,
		Guid:        strings.TrimSpace(entry.ID),
		Author:      author,
		Categories:  categories,
		Image:       atomImage(entry.Links),
	}
}

func atomAlternateLink(links []atomLink) string {
	for _, link := range links {
		rel := strings.TrimSpace(link.Rel)
		if rel == "" || strings.EqualFold(rel, "alternate") {
			return strings.TrimSpace(link.Href)
		}
	}
	return ""
}

func atomImage(links []atomLink) string {
	for _, link := range links {
		if strings.EqualFold(strings.TrimSpace(link.Rel), "enclosure") &&
			strings.HasPrefix(strings.ToLower(strings.TrimSpace(link.Type)), "image/") {
			return strings.TrimSpace(link.Href)
		}
	}
	return ""
}

// description returns an entry's summary as the model asked for it: verbatim by
// default, or with its markup removed when the task authored plainText. Publishers
// routinely put HTML inside description, which renders as tags wherever a process
// puts it — a mail body, a user task, a chat message (ADR-0231).
func description(raw string, plainText bool) string {
	if !plainText {
		return strings.TrimSpace(raw)
	}
	return textOf(raw)
}

func trimmedList(values []string) []string {
	out := make([]string, 0, len(values))
	for _, v := range values {
		if v = strings.TrimSpace(v); v != "" {
			out = append(out, v)
		}
	}
	return out
}

func boundedLen(length int, maxItems int32) int {
	if maxItems <= 0 || int64(maxItems) >= int64(length) {
		return length
	}
	return int(maxItems)
}
