package webscrape_test

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/pblumer/atlas/connector/webscrape"
)

// htmlOnly is a custom client that can scrape a page and nothing else: the shape a
// client written before feeds and fields existed has.
type htmlOnly struct{ got webscrape.Request }

func (c *htmlOnly) Scrape(_ context.Context, r webscrape.Request) ([]string, error) {
	c.got = r
	return []string{"Headline"}, nil
}

// failing implements every client half and fails each, as an unreachable site does.
type failing struct{}

func (failing) Scrape(context.Context, webscrape.Request) ([]string, error) {
	return nil, errors.New("site down")
}

func (failing) ScrapeFeed(context.Context, webscrape.Request) ([]webscrape.FeedEntry, error) {
	return nil, errors.New("site down")
}

func (failing) ScrapeRecords(context.Context, webscrape.Request) ([]map[string]string, error) {
	return nil, errors.New("site down")
}

// TestAJobWithNoFormatIsAnHTMLScrape: every task authored before formats existed
// carries none, and it means what it always meant.
func TestAJobWithNoFormatIsAnHTMLScrape(t *testing.T) {
	c := &htmlOnly{}
	res, err := webscrape.Run(context.Background(), webscrape.Job{URL: "https://example.com", Selector: "h1", Result: "titles"}, c)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.Format != "html" || c.got.Format != "html" || !reflect.DeepEqual(res.Values, []string{"Headline"}) {
		t.Fatalf("Run = %+v (request format %q), want an html scrape", res, c.got.Format)
	}
}

// TestRunRefusesWhatItCannotDo: a format this build never compiles, and a feed or
// field scrape handed to a client that cannot do one, are refused by name rather than
// answered with an empty list.
func TestRunRefusesWhatItCannotDo(t *testing.T) {
	for _, tc := range []struct {
		name string
		job  webscrape.Job
		want string
	}{
		{"an unknown format", webscrape.Job{URL: "https://example.com", Format: "xml"}, `unsupported compiled format "xml"`},
		{"a feed", webscrape.Job{URL: "https://example.com", Format: "atom"}, "does not support atom feed extraction"},
		{"fields", webscrape.Job{URL: "https://example.com", Fields: []webscrape.Field{{Name: "t", Selector: "h1"}}},
			"does not support per-item field extraction"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := webscrape.Run(context.Background(), tc.job, &htmlOnly{}); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("Run = %v, want an error containing %q", err, tc.want)
			}
		})
	}
}

// TestAFailedFetchFailsTheJob: whichever kind of scrape it is, a site that cannot be
// read fails the job so it is retried, rather than completing with nothing.
func TestAFailedFetchFailsTheJob(t *testing.T) {
	for _, job := range []webscrape.Job{
		{URL: "https://example.com", Format: "rss"},
		{URL: "https://example.com", Fields: []webscrape.Field{{Name: "t", Selector: "h1"}}},
	} {
		if _, err := webscrape.Run(context.Background(), job, failing{}); err == nil || err.Error() != "site down" {
			t.Errorf("Run(%+v) = %v, want the fetch error", job, err)
		}
	}
}

// TestAFeedItemCarriesItsCategories: categories reach a model as a list of strings,
// in the feed's order.
func TestAFeedItemCarriesItsCategories(t *testing.T) {
	items := webscrape.Items(webscrape.Result{Format: "rss", Entries: []webscrape.FeedEntry{
		{Title: "One", Categories: []string{"politics", "zurich"}},
	}})
	if len(items) != 1 {
		t.Fatalf("items = %v, want one", items)
	}
	entry := items[0].(map[string]any)
	if !reflect.DeepEqual(entry["categories"], []any{"politics", "zurich"}) {
		t.Fatalf("categories = %#v, want [politics zurich]", entry["categories"])
	}
}

// TestResolveWithoutADetailIsRefused: nothing to scrape, refused before any read.
func TestResolveWithoutADetailIsRefused(t *testing.T) {
	if _, err := webscrape.Resolve(nil, nil, nil, nil, 0); err == nil || !strings.Contains(err.Error(), "task has no detail") {
		t.Fatalf("Resolve(nil detail) = %v, want the refusal", err)
	}
}
