package webscrape

import (
	"net/url"
	"strings"
	"testing"
)

// The page a structured scrape is for: a list where one item carries several values.
// The third row is the case that makes parallel arrays wrong — it has no link, so a
// title array and a link array would pair "Ohne Link" with the wrong href from then on.
const recordsHTML = `<!DOCTYPE html>
<html><body>
  <table class="rates">
    <tr class="row"><td class="term">Fest 5 Jahre</td><td class="rate">1.45%</td><td><a href="/de/hypo/5">Details</a></td></tr>
    <tr class="row"><td class="term">Fest 10 Jahre</td><td class="rate">1.72%</td><td><a href="https://example.com/10">Details</a></td></tr>
    <tr class="row"><td class="term">Ohne Link</td><td class="rate">2.05%</td><td><span>–</span></td></tr>
  </table>
</body></html>`

func recordsRequest(fields ...Field) Request {
	return Request{Selector: "tr.row", Fields: fields}
}

func parseRecordsPage(t *testing.T, r Request, base string) []map[string]string {
	t.Helper()
	doc, err := parseHTML(strings.NewReader(recordsHTML), "")
	if err != nil {
		t.Fatalf("parse page: %v", err)
	}
	var u *url.URL
	if base != "" {
		if u, err = url.Parse(base); err != nil {
			t.Fatalf("parse base: %v", err)
		}
	}
	got, err := extractRecords(doc, r, u)
	if err != nil {
		t.Fatalf("extractRecords: %v", err)
	}
	return got
}

// One match is one object: the fields of a row arrive together, so a model reads
// eintrag.zins beside eintrag.laufzeit instead of indexing two arrays.
func TestExtractRecordsAssemblesOneObjectPerMatch(t *testing.T) {
	got := parseRecordsPage(t, recordsRequest(
		Field{Name: "laufzeit", Selector: "td.term"},
		Field{Name: "zins", Selector: "td.rate"},
	), "")

	if len(got) != 3 {
		t.Fatalf("records = %d, want 3", len(got))
	}
	if got[0]["laufzeit"] != "Fest 5 Jahre" || got[0]["zins"] != "1.45%" {
		t.Errorf("first record = %v, want the first row's cells", got[0])
	}
	if got[2]["laufzeit"] != "Ohne Link" || got[2]["zins"] != "2.05%" {
		t.Errorf("third record = %v, want the third row's cells", got[2])
	}
}

// A field the item does not carry is present and empty. Every item therefore has the
// same shape, which is what lets a FEEL expression read entry.link unconditionally —
// and it is why a missing value cannot shift the values after it.
func TestExtractRecordsKeepsTheShapeWhenAFieldIsMissing(t *testing.T) {
	got := parseRecordsPage(t, recordsRequest(
		Field{Name: "titel", Selector: "td.term"},
		Field{Name: "link", Selector: "a", Attribute: "href"},
	), "")

	if len(got) != 3 {
		t.Fatalf("records = %d, want 3", len(got))
	}
	if got[2]["titel"] != "Ohne Link" {
		t.Errorf("third record title = %q, want the row's own title", got[2]["titel"])
	}
	link, ok := got[2]["link"]
	if !ok {
		t.Fatalf("third record has no link key: %v — a missing value must not change the shape", got[2])
	}
	if link != "" {
		t.Errorf("third record link = %q, want an empty value for the row without one", link)
	}
}

// An empty field selector means the matched item itself, so a task can take the
// item's own text or one of its own attributes without a redundant selector.
func TestExtractRecordsEmptySelectorReadsTheItem(t *testing.T) {
	got := parseRecordsPage(t, Request{
		Selector: "td.rate",
		Fields:   []Field{{Name: "zins"}, {Name: "klasse", Attribute: "class"}},
	}, "")

	if len(got) != 3 {
		t.Fatalf("records = %d, want 3", len(got))
	}
	if got[0]["zins"] != "1.45%" || got[0]["klasse"] != "rate" {
		t.Errorf("record = %v, want the cell's own text and class", got[0])
	}
}

// absoluteLinks resolves a relative href against the page it was read from — a
// relative path is not something a process can open, mail, or store. An href that is
// already absolute is left alone, and a non-link attribute is never rewritten.
func TestExtractRecordsResolvesRelativeLinks(t *testing.T) {
	req := recordsRequest(
		Field{Name: "link", Selector: "a", Attribute: "href"},
		Field{Name: "klasse", Selector: "td.term", Attribute: "class"},
	)
	req.AbsoluteLinks = true
	got := parseRecordsPage(t, req, "https://example.com/de/hypotheken/zinsen.html")

	if got[0]["link"] != "https://example.com/de/hypo/5" {
		t.Errorf("relative link = %q, want it resolved against the page URL", got[0]["link"])
	}
	if got[1]["link"] != "https://example.com/10" {
		t.Errorf("absolute link = %q, want it untouched", got[1]["link"])
	}
	if got[0]["klasse"] != "term" {
		t.Errorf("class attribute = %q, want a value, not a resolved URL", got[0]["klasse"])
	}
}

// Without the flag the page's own spelling survives, so every model authored before
// ADR-0231 keeps the value it reads today.
func TestExtractRecordsLeavesRelativeLinksAloneByDefault(t *testing.T) {
	got := parseRecordsPage(t, recordsRequest(Field{Name: "link", Selector: "a", Attribute: "href"}),
		"https://example.com/de/hypotheken/zinsen.html")
	if got[0]["link"] != "/de/hypo/5" {
		t.Errorf("link = %q, want the authored value verbatim", got[0]["link"])
	}
}

// The first-N bound counts items, exactly as it does for matches and feed entries.
func TestExtractRecordsHonorsMaxItems(t *testing.T) {
	req := recordsRequest(Field{Name: "laufzeit", Selector: "td.term"})
	req.MaxItems = 2
	got := parseRecordsPage(t, req, "")
	if len(got) != 2 || got[1]["laufzeit"] != "Fest 10 Jahre" {
		t.Fatalf("records = %v, want the first two rows", got)
	}
}

// A bad field selector names the field it is on. The item selector's own error keeps
// naming the selector, which is what an author sees in the incident.
func TestExtractRecordsInvalidSelectorsNameThemselves(t *testing.T) {
	doc, err := parseHTML(strings.NewReader(recordsHTML), "")
	if err != nil {
		t.Fatalf("parse page: %v", err)
	}
	_, err = extractRecords(doc, recordsRequest(Field{Name: "zins", Selector: "td["}), nil)
	if err == nil || !strings.Contains(err.Error(), `field "zins"`) {
		t.Fatalf("error = %v, want the field named", err)
	}
	_, err = extractRecords(doc, Request{Selector: "tr[", Fields: []Field{{Name: "x"}}}, nil)
	if err == nil || !strings.Contains(err.Error(), "invalid selector") {
		t.Fatalf("error = %v, want the item selector named", err)
	}
}

// A selector that matches nothing is an empty result, not an error: a page with no
// rows today is a normal day, and the job must complete rather than raise an incident.
func TestExtractRecordsNoMatchesIsEmpty(t *testing.T) {
	got := parseRecordsPage(t, Request{Selector: "tr.missing", Fields: []Field{{Name: "x"}}}, "")
	if len(got) != 0 {
		t.Fatalf("records = %v, want none", got)
	}
}

// The no-fields result is unchanged, including under absoluteLinks: ADR-0118's string
// array is still what a task without fields returns.
func TestExtractMatchesStillReturnsStrings(t *testing.T) {
	doc, err := parseHTML(strings.NewReader(recordsHTML), "")
	if err != nil {
		t.Fatalf("parse page: %v", err)
	}
	base, err := url.Parse("https://example.com/de/hypotheken/zinsen.html")
	if err != nil {
		t.Fatalf("parse base: %v", err)
	}
	got, err := extractMatches(doc, Request{Selector: "a", Attribute: "href", AbsoluteLinks: true}, base)
	if err != nil {
		t.Fatalf("extractMatches: %v", err)
	}
	want := []string{"https://example.com/de/hypo/5", "https://example.com/10"}
	if len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("matches = %v, want %v", got, want)
	}
}
