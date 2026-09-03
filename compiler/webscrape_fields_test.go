package compiler

import "testing"

// A field list is deploy-time structure: the names, the field-relative selectors and
// the attributes are all interned into the compiled detail, so the worker reads them
// back without the model being consulted at runtime (I5).
func TestWebScrapeFieldsCompile(t *testing.T) {
	d := parseWebScrapeExtension(t, `<atlas:webscrapeConnector url="https://example.com/zinsen" selector="tr.row" absoluteLinks="true" resultVariable="zinsen">
	  <atlas:scrapeField name="laufzeit" selector="td.term"/>
	  <atlas:scrapeField name="zins" selector="td.rate"/>
	  <atlas:scrapeField name="link" selector="a" attribute="href"/>
	  <atlas:scrapeField name="zeile"/>
	</atlas:webscrapeConnector>`)

	if len(d.ScrapeFields) != 4 {
		t.Fatalf("fields = %d, want 4", len(d.ScrapeFields))
	}
	if !d.ScrapeAbsoluteLinks {
		t.Error("absoluteLinks = false, want the authored true")
	}
	if d.ScrapePlainText {
		t.Error("plainText = true, want false where the model authored none")
	}
	cp := parseWebScrapeProcess(t, `<atlas:webscrapeConnector url="https://example.com/zinsen" selector="tr.row" resultVariable="zinsen">
	  <atlas:scrapeField name="laufzeit" selector="td.term"/>
	  <atlas:scrapeField name="link" selector="a" attribute="href"/>
	  <atlas:scrapeField name="zeile"/>
	</atlas:webscrapeConnector>`)
	detail := webScrapeDetail(t, cp)
	for i, want := range []struct{ name, selector, attribute string }{
		{"laufzeit", "td.term", ""},
		{"link", "a", "href"},
		{"zeile", "", ""}, // no selector: the matched item itself
	} {
		f := detail.ScrapeFields[i]
		if got := cp.Intern(f.Name); got != want.name {
			t.Errorf("field %d name = %q, want %q", i, got, want.name)
		}
		if got := cp.Intern(f.Selector); got != want.selector {
			t.Errorf("field %d selector = %q, want %q", i, got, want.selector)
		}
		if got := cp.Intern(f.Attribute); got != want.attribute {
			t.Errorf("field %d attribute = %q, want %q", i, got, want.attribute)
		}
	}
}

// No fields is the ADR-0118 task, unchanged: nil rather than an empty list, because
// that distinction is what the result shape turns on.
func TestWebScrapeWithoutFieldsStaysAStringScrape(t *testing.T) {
	d := parseWebScrapeExtension(t, `<atlas:webscrapeConnector url="https://example.com" selector=".headline" resultVariable="r"/>`)
	if d.ScrapeFields != nil {
		t.Errorf("fields = %v, want nil for a task that authored none", d.ScrapeFields)
	}
	if d.ScrapeAbsoluteLinks || d.ScrapePlainText {
		t.Errorf("flags = %v/%v, want both off by default", d.ScrapeAbsoluteLinks, d.ScrapePlainText)
	}
}

// Each rejected combination is one an author could otherwise deploy and then wonder
// about at runtime, when the ignored half silently does nothing.
func TestWebScrapeFieldValidation(t *testing.T) {
	cases := []struct {
		name string
		ext  string
		want string
	}{
		{
			"unnamed field",
			`<atlas:webscrapeConnector url="https://x" selector="tr" resultVariable="r"><atlas:scrapeField selector="td"/></atlas:webscrapeConnector>`,
			"scrapeField without a name",
		},
		{
			"duplicate field",
			`<atlas:webscrapeConnector url="https://x" selector="tr" resultVariable="r"><atlas:scrapeField name="a" selector="td.one"/><atlas:scrapeField name="a" selector="td.two"/></atlas:webscrapeConnector>`,
			`two scrapeFields named "a"`,
		},
		{
			"attribute beside fields",
			`<atlas:webscrapeConnector url="https://x" selector="tr" attribute="href" resultVariable="r"><atlas:scrapeField name="a" selector="td"/></atlas:webscrapeConnector>`,
			"each field carries its own attribute",
		},
		{
			"fields in a feed",
			`<atlas:webscrapeConnector url="https://x" format="rss" resultVariable="r"><atlas:scrapeField name="a" selector="td"/></atlas:webscrapeConnector>`,
			"does not use scrapeField children",
		},
		{
			"absoluteLinks in a feed",
			`<atlas:webscrapeConnector url="https://x" format="atom" absoluteLinks="true" resultVariable="r"/>`,
			"does not use absoluteLinks",
		},
		{
			"plainText in html",
			`<atlas:webscrapeConnector url="https://x" selector=".x" plainText="true" resultVariable="r"/>`,
			"does not use plainText",
		},
		{
			"non-boolean absoluteLinks",
			`<atlas:webscrapeConnector url="https://x" selector=".x" absoluteLinks="ja" resultVariable="r"/>`,
			"non-boolean absoluteLinks",
		},
		{
			"non-boolean plainText",
			`<atlas:webscrapeConnector url="https://x" format="rss" plainText="1" resultVariable="r"/>`,
			"non-boolean plainText",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) { parseWebScrapeError(t, tc.ext, tc.want) })
	}
}

// plainText is the feed flag, and it compiles where it belongs.
func TestWebScrapePlainTextCompilesForFeeds(t *testing.T) {
	d := parseWebScrapeExtension(t, `<atlas:webscrapeConnector url="https://x/rss" format="rss" plainText="true" resultVariable="r"/>`)
	if !d.ScrapePlainText {
		t.Error("plainText = false, want the authored true")
	}
}

// An explicit false is not an error and not a surprise — it is the default, said out
// loud, which is what the Modeler writes when an author switches the flag back off.
func TestWebScrapeExplicitFalseFlagsCompile(t *testing.T) {
	d := parseWebScrapeExtension(t, `<atlas:webscrapeConnector url="https://x" selector=".x" absoluteLinks="FALSE" resultVariable="r"/>`)
	if d.ScrapeAbsoluteLinks {
		t.Error("absoluteLinks = true, want the authored false")
	}
}
