package webscrape

import (
	"strings"
	"testing"
)

// latin1 encodes a UTF-8 string as ISO-8859-1 bytes, which is what a publisher that
// declares that encoding actually sends. Every rune in these fixtures is inside the
// Latin-1 range, so one byte per rune is the whole encoder.
func latin1(s string) string {
	b := make([]byte, 0, len(s))
	for _, r := range s {
		b = append(b, byte(r))
	}
	return string(b)
}

// An RSS feed that declares ISO-8859-1 used to fail the job outright — a plain
// xml.Decoder has no CharsetReader and refuses the document rather than decoding it.
func TestExtractRSSDecodesADeclaredCharset(t *testing.T) {
	feed := latin1(`<?xml version="1.0" encoding="ISO-8859-1"?>
<rss version="2.0"><channel>
  <item><title>Zinssätze für Bürgschaften</title><description>Übersicht</description></item>
</channel></rss>`)

	got, err := extractRSS(strings.NewReader(feed), 0, false)
	if err != nil {
		t.Fatalf("extractRSS: %v", err)
	}
	if len(got) != 1 || got[0].Title != "Zinssätze für Bürgschaften" {
		t.Fatalf("entries = %+v, want the title with its umlauts intact", got)
	}
	if got[0].Description != "Übersicht" {
		t.Errorf("description = %q, want it decoded", got[0].Description)
	}
}

// The quieter half of the same defect: an HTML page in Latin-1 parsed without error
// and delivered mojibake into the process. The <meta> charset is what says so.
func TestParseHTMLDecodesADeclaredCharset(t *testing.T) {
	page := latin1(`<html><head><meta charset="ISO-8859-1"></head>
<body><p class="x">Zinssätze für Grüezi</p></body></html>`)

	got, err := extract(strings.NewReader(page), "p.x", "")
	if err != nil {
		t.Fatalf("extract: %v", err)
	}
	if len(got) != 1 || got[0] != "Zinssätze für Grüezi" {
		t.Fatalf("text = %q, want the umlauts decoded rather than mojibake", got)
	}
}

// RSS 1.0 is RDF: the items are siblings of the channel, not inside it, and the date
// comes from dc:date. An author who picked "RSS" means this too.
func TestExtractRSSReadsRDFFeeds(t *testing.T) {
	feed := `<?xml version="1.0"?>
<rdf:RDF xmlns:rdf="http://www.w3.org/1999/02/22-rdf-syntax-ns#"
         xmlns="http://purl.org/rss/1.0/" xmlns:dc="http://purl.org/dc/elements/1.1/">
  <channel rdf:about="https://example.com/"><title>Kanal</title></channel>
  <item rdf:about="https://example.com/1">
    <title>Erste Meldung</title>
    <link>https://example.com/1</link>
    <description>Kurz</description>
    <dc:date>2026-09-03T06:00:00Z</dc:date>
    <dc:creator>R. Redaktion</dc:creator>
  </item>
</rdf:RDF>`

	got, err := extractRSS(strings.NewReader(feed), 0, false)
	if err != nil {
		t.Fatalf("extractRSS: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("entries = %d, want the RDF item", len(got))
	}
	if got[0].Title != "Erste Meldung" || got[0].Link != "https://example.com/1" {
		t.Errorf("entry = %+v, want the RDF item's title and link", got[0])
	}
	if got[0].Published != "2026-09-03T06:00:00Z" {
		t.Errorf("published = %q, want the dc:date an RSS 1.0 item carries", got[0].Published)
	}
	if got[0].Author != "R. Redaktion" {
		t.Errorf("author = %q, want the dc:creator", got[0].Author)
	}
}

// The entry fields ADR-draft-webscrape-structured-extraction added, from where RSS
// publishers actually put them.
func TestExtractRSSReadsIdentityAuthorCategoriesAndImage(t *testing.T) {
	feed := `<rss version="2.0" xmlns:media="http://search.yahoo.com/mrss/"
     xmlns:content="http://purl.org/rss/1.0/modules/content/"><channel>
  <item>
    <title>Mit allem</title>
    <guid isPermaLink="false">urn:id:4711</guid>
    <author>redaktion@example.com</author>
    <category>Wirtschaft</category>
    <category> Zinsen </category>
    <content:encoded>&lt;p&gt;voller Text&lt;/p&gt;</content:encoded>
    <enclosure url="https://example.com/audio.mp3" type="audio/mpeg"/>
    <enclosure url="https://example.com/bild.jpg" type="image/jpeg"/>
  </item>
  <item>
    <title>Nur Thumbnail</title>
    <media:thumbnail url="https://example.com/thumb.jpg"/>
  </item>
  <item><title>Ganz nackt</title></item>
</channel></rss>`

	got, err := extractRSS(strings.NewReader(feed), 0, false)
	if err != nil {
		t.Fatalf("extractRSS: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("entries = %d, want 3", len(got))
	}
	if got[0].Guid != "urn:id:4711" {
		t.Errorf("guid = %q, want the item's own identity — what a daily run deduplicates on", got[0].Guid)
	}
	if got[0].Author != "redaktion@example.com" {
		t.Errorf("author = %q, want the RSS author", got[0].Author)
	}
	if len(got[0].Categories) != 2 || got[0].Categories[0] != "Wirtschaft" || got[0].Categories[1] != "Zinsen" {
		t.Errorf("categories = %v, want both, trimmed", got[0].Categories)
	}
	if got[0].Image != "https://example.com/bild.jpg" {
		t.Errorf("image = %q, want the image enclosure and not the audio one", got[0].Image)
	}
	if got[1].Image != "https://example.com/thumb.jpg" {
		t.Errorf("image = %q, want the media:thumbnail when there is no enclosure", got[1].Image)
	}
	if got[2].Image != "" || got[2].Guid != "" || got[2].Author != "" {
		t.Errorf("entry = %+v, want empty values rather than invented ones", got[2])
	}
	if got[2].Categories == nil || len(got[2].Categories) != 0 {
		t.Errorf("categories = %v, want an empty list so a model can count() it", got[2].Categories)
	}
}

func TestExtractAtomReadsIdentityAuthorCategoriesAndImage(t *testing.T) {
	feed := `<feed xmlns="http://www.w3.org/2005/Atom">
  <entry>
    <title>Mit allem</title>
    <id>urn:uuid:4711</id>
    <author><name>A. Autorin</name></author>
    <category term="wirtschaft" label="Wirtschaft"/>
    <category term="zinsen"/>
    <link rel="alternate" href="https://example.com/1"/>
    <link rel="enclosure" type="image/png" href="https://example.com/bild.png"/>
  </entry>
</feed>`

	got, err := extractAtom(strings.NewReader(feed), 0, false)
	if err != nil {
		t.Fatalf("extractAtom: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("entries = %d, want 1", len(got))
	}
	if got[0].Guid != "urn:uuid:4711" || got[0].Author != "A. Autorin" {
		t.Errorf("entry = %+v, want the Atom id and author name", got[0])
	}
	if len(got[0].Categories) != 2 || got[0].Categories[0] != "Wirtschaft" || got[0].Categories[1] != "zinsen" {
		t.Errorf("categories = %v, want the label where there is one and the term otherwise", got[0].Categories)
	}
	if got[0].Image != "https://example.com/bild.png" {
		t.Errorf("image = %q, want the image enclosure link", got[0].Image)
	}
	if got[0].Link != "https://example.com/1" {
		t.Errorf("link = %q, want the alternate link and not the enclosure", got[0].Link)
	}
}

// plainText is for the description a process puts in front of a person: a mail body,
// a user task, a chat message. Markup renders as tags there.
func TestPlainTextDescriptionStripsMarkup(t *testing.T) {
	feed := `<rss version="2.0"><channel><item>
    <title>Zins</title>
    <description>&lt;p&gt;Erste Zeile&lt;/p&gt;&lt;p&gt;Zweite &amp;amp; letzte&lt;/p&gt;</description>
  </item></channel></rss>`

	verbatim, err := extractRSS(strings.NewReader(feed), 0, false)
	if err != nil {
		t.Fatalf("extractRSS: %v", err)
	}
	if !strings.Contains(verbatim[0].Description, "<p>") {
		t.Errorf("description = %q, want the publisher's markup verbatim by default", verbatim[0].Description)
	}

	plain, err := extractRSS(strings.NewReader(feed), 0, true)
	if err != nil {
		t.Fatalf("extractRSS: %v", err)
	}
	if plain[0].Description != "Erste Zeile Zweite & letzte" {
		t.Errorf("plain description = %q, want text with the block boundary as a space", plain[0].Description)
	}
}

// Feeds ship &nbsp; and bare ampersands. A strict XML parse rejects both, which fails
// a job over a document every reader renders fine.
func TestExtractRSSSurvivesHTMLEntities(t *testing.T) {
	feed := `<rss version="2.0"><channel><item>
    <title>Fest&nbsp;5 Jahre</title><description>Zins &amp; Marge</description>
  </item></channel></rss>`

	got, err := extractRSS(strings.NewReader(feed), 0, false)
	if err != nil {
		t.Fatalf("extractRSS: %v", err)
	}
	if !strings.Contains(got[0].Title, "Fest") || !strings.Contains(got[0].Title, "5 Jahre") {
		t.Errorf("title = %q, want the entity resolved rather than the document rejected", got[0].Title)
	}
	if got[0].Description != "Zins & Marge" {
		t.Errorf("description = %q, want the ampersand decoded", got[0].Description)
	}
}

// The wrong Format is the authoring mistake this connector cannot detect for the
// author (ADR-0190 keeps the choice explicit), so the error says what the document is
// and which setting to change — instead of an XML syntax error at line 1.
func TestFeedParseNamesTheFormatToChange(t *testing.T) {
	page := `<!DOCTYPE html><html><body><h1>Zinsen</h1></body></html>`
	_, err := extractRSS(strings.NewReader(page), 0, false)
	if err == nil || !strings.Contains(err.Error(), "HTML") || !strings.Contains(err.Error(), "Format") {
		t.Fatalf("error = %v, want it to name HTML and the Format setting", err)
	}

	_, err = extractRSS(strings.NewReader(atomFixture), 0, false)
	if err == nil || !strings.Contains(err.Error(), "Atom") {
		t.Fatalf("error = %v, want an Atom document to say so", err)
	}

	_, err = extractAtom(strings.NewReader(rssFixture), 0, false)
	if err == nil || !strings.Contains(err.Error(), "RSS") {
		t.Fatalf("error = %v, want an RSS document to say so", err)
	}
}

// An empty document is a parse failure, not an empty feed: nothing was served.
func TestFeedParseEmptyDocumentFails(t *testing.T) {
	if _, err := extractRSS(strings.NewReader(""), 0, false); err == nil {
		t.Fatal("extractRSS error = nil, want an empty document to fail")
	}
	if _, err := extractAtom(strings.NewReader(""), 0, false); err == nil {
		t.Fatal("extractAtom error = nil, want an empty document to fail")
	}
}

// A document past the cap fails the job rather than being truncated into a plausible
// but silently short result.
func TestCappedReaderStopsAtTheLimit(t *testing.T) {
	r := &cappedReader{r: strings.NewReader(strings.Repeat("x", 64)), left: 16}
	n, err := r.Read(make([]byte, 32))
	if err != nil || n != 16 {
		t.Fatalf("first read = %d, %v; want the capped 16 bytes", n, err)
	}
	if _, err := r.Read(make([]byte, 8)); err == nil || !strings.Contains(err.Error(), "MiB limit") {
		t.Fatalf("second read error = %v, want the cap named", err)
	}
}
