package webscrape

import (
	"strings"
	"testing"
)

const rssFixture = `<?xml version="1.0"?>
<rss version="2.0"><channel>
  <item><title> First </title><link> https://example.com/1 </link><description> One </description><pubDate> Tue, 26 Aug 2026 08:30:00 GMT </pubDate></item>
  <item><title>Second</title><link>https://example.com/2</link></item>
</channel></rss>`

func TestExtractRSS(t *testing.T) {
	got, err := extractRSS(strings.NewReader(rssFixture), 0, false)
	if err != nil {
		t.Fatalf("extractRSS: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("entries = %d, want 2", len(got))
	}
	if got[0].Title != "First" || got[0].Link != "https://example.com/1" || got[0].Description != "One" || got[0].Published != "Tue, 26 Aug 2026 08:30:00 GMT" {
		t.Errorf("first entry = %+v, want trimmed RSS fields", got[0])
	}
	if got[1].Description != "" || got[1].Published != "" {
		t.Errorf("missing RSS fields = %+v, want empty strings", got[1])
	}
}

func TestExtractRSSMaxItems(t *testing.T) {
	got, err := extractRSS(strings.NewReader(rssFixture), 1, false)
	if err != nil {
		t.Fatalf("extractRSS: %v", err)
	}
	if len(got) != 1 || got[0].Title != "First" {
		t.Fatalf("entries = %+v, want only the first item", got)
	}
}

const atomFixture = `<?xml version="1.0"?>
<feed xmlns="http://www.w3.org/2005/Atom">
  <entry>
    <title> First atom </title>
    <link rel="self" href="https://example.com/feed/1"/>
    <link rel="alternate" href=" https://example.com/1 "/>
    <summary> Summary </summary>
    <published> 2026-08-26T08:30:00Z </published>
  </entry>
  <entry>
    <title>Second atom</title>
    <link href="https://example.com/2"/>
    <content> Content fallback </content>
    <updated> 2026-08-26T09:00:00Z </updated>
  </entry>
</feed>`

func TestExtractAtomFallbacksAndAlternateLink(t *testing.T) {
	got, err := extractAtom(strings.NewReader(atomFixture), 0, false)
	if err != nil {
		t.Fatalf("extractAtom: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("entries = %d, want 2", len(got))
	}
	if got[0].Link != "https://example.com/1" || got[0].Description != "Summary" || got[0].Published != "2026-08-26T08:30:00Z" {
		t.Errorf("first entry = %+v, want alternate/summary/published", got[0])
	}
	if got[1].Link != "https://example.com/2" || got[1].Description != "Content fallback" || got[1].Published != "2026-08-26T09:00:00Z" {
		t.Errorf("second entry = %+v, want absent-rel/content/updated fallbacks", got[1])
	}
}

func TestExtractAtomMaxItems(t *testing.T) {
	got, err := extractAtom(strings.NewReader(atomFixture), 1, false)
	if err != nil {
		t.Fatalf("extractAtom: %v", err)
	}
	if len(got) != 1 || got[0].Title != "First atom" {
		t.Fatalf("entries = %+v, want only the first entry", got)
	}
}

func TestFeedFormatMismatchFails(t *testing.T) {
	if _, err := extractRSS(strings.NewReader(atomFixture), 0, false); err == nil {
		t.Fatal("Atom decoded as RSS without error")
	}
	if _, err := extractAtom(strings.NewReader(rssFixture), 0, false); err == nil {
		t.Fatal("RSS decoded as Atom without error")
	}
}
