package webscrape

import (
	"strings"
	"testing"
)

// TestAFeedOfAnotherKindIsNamedByItsRoot: a root that is neither HTML nor a feed is
// named as it is, so the author sees what the URL actually returned.
func TestAFeedOfAnotherKindIsNamedByItsRoot(t *testing.T) {
	_, err := extractRSS(strings.NewReader(`<?xml version="1.0"?><catalog><book/></catalog>`), 0, false)
	if err == nil || err.Error() != "webscrape: the document root is <catalog>, which is not an RSS feed" {
		t.Fatalf("extractRSS(catalog) = %v, want the root named", err)
	}
}

// TestATruncatedFeedFailsTheScrape: a feed cut off mid-document — a dropped
// connection, a size limit — is an error, never the entries that happened to arrive
// presented as the whole feed.
func TestATruncatedFeedFailsTheScrape(t *testing.T) {
	for _, tc := range []struct {
		name    string
		extract func(string) ([]FeedEntry, error)
		doc     string
		want    string
	}{
		{"rss 2.0", func(s string) ([]FeedEntry, error) { return extractRSS(strings.NewReader(s), 0, false) },
			`<rss version="2.0"><channel><item><title>One</title></item><item><title>Tw`, "parse rss"},
		{"rss 1.0", func(s string) ([]FeedEntry, error) { return extractRSS(strings.NewReader(s), 0, false) },
			`<rdf:RDF xmlns:rdf="http://www.w3.org/1999/02/22-rdf-syntax-ns#"><item><title>On`, "parse rss"},
		{"atom", func(s string) ([]FeedEntry, error) { return extractAtom(strings.NewReader(s), 0, false) },
			`<feed xmlns="http://www.w3.org/2005/Atom"><entry><title>On`, "parse atom"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := tc.extract(tc.doc)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("extract = (%+v, %v), want an error containing %q", got, err, tc.want)
			}
		})
	}
}

// TestAMediaElementWithoutAURLIsNotTheImage: an image candidate with no url is
// skipped, and the next one that has one is taken.
func TestAMediaElementWithoutAURLIsNotTheImage(t *testing.T) {
	got, err := extractRSS(strings.NewReader(`<rss version="2.0" xmlns:media="http://search.yahoo.com/mrss/"><channel>
	  <item><title>One</title>
	    <media:content medium="image"/>
	    <media:thumbnail url=" https://example.com/t.jpg "/>
	  </item>
	</channel></rss>`), 0, false)
	if err != nil {
		t.Fatalf("extractRSS: %v", err)
	}
	if len(got) != 1 || got[0].Image != "https://example.com/t.jpg" {
		t.Fatalf("entries = %+v, want the thumbnail as the image", got)
	}
}

// TestAnAtomEntryWithOnlyASelfLinkHasNoLink: "self" is the entry's own feed address,
// not the article, so it is not offered as the link to read.
func TestAnAtomEntryWithOnlyASelfLinkHasNoLink(t *testing.T) {
	got, err := extractAtom(strings.NewReader(`<feed xmlns="http://www.w3.org/2005/Atom">
	  <entry><title>One</title><link rel="self" href="https://example.com/feed/1"/></entry>
	</feed>`), 0, false)
	if err != nil {
		t.Fatalf("extractAtom: %v", err)
	}
	if len(got) != 1 || got[0].Link != "" {
		t.Fatalf("entries = %+v, want no link", got)
	}
}
