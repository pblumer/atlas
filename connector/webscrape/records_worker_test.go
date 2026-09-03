package webscrape_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/pblumer/atlas/compiler"
	"github.com/pblumer/atlas/connector/webscrape"
	"github.com/pblumer/atlas/model"
)

// recordScrapingClient is a client that can do both shapes, like the built-in HTTP
// one: the handler must pick the field path from the task, not from the client.
type recordScrapingClient struct {
	requests    []webscrape.Request
	records     []map[string]string
	values      []string
	recordCalls int
	valueCalls  int
}

func (r *recordScrapingClient) Scrape(_ context.Context, req webscrape.Request) ([]string, error) {
	r.valueCalls++
	r.requests = append(r.requests, req)
	return r.values, nil
}

func (r *recordScrapingClient) ScrapeRecords(_ context.Context, req webscrape.Request) ([]map[string]string, error) {
	r.recordCalls++
	r.requests = append(r.requests, req)
	return r.records, nil
}

func fieldScrapeProcess(t *testing.T, fields []compiler.WebScrapeFieldConfig, absoluteLinks bool) (*compiler.CompiledProcess, int32) {
	t.Helper()
	b := compiler.NewBuilder(wsDefKey, "zinsen", 1)
	start := b.AddStartEvent()
	scrape := b.AddWebScrapeExtractionTask(compiler.WebScrapeExtractionConfig{
		Url:           compiler.RestExpr{Literal: "https://example.com/zinsen"},
		Selector:      compiler.RestExpr{Literal: "tr.row"},
		Fields:        fields,
		AbsoluteLinks: absoluteLinks,
		Result:        "zinsen",
		Retries:       3,
	})
	wait := b.AddServiceTask("wait", 3)
	end := b.AddEndEvent()
	b.Connect(start, scrape)
	b.Connect(scrape, wait)
	b.Connect(wait, end)
	cp, err := b.Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	return cp, cp.ConnectorTask(cp.Node(scrape).Detail).JobType
}

// The whole field path through the engine: the authored fields reach the worker, and
// the result variable holds one object per item rather than a string array.
func TestWebScrapeWritesOneObjectPerItem(t *testing.T) {
	log, store := openStore(t)
	cp, jobType := fieldScrapeProcess(t, []compiler.WebScrapeFieldConfig{
		{Name: "laufzeit", Selector: "td.term"},
		{Name: "zins", Selector: "td.rate"},
		{Name: "link", Selector: "a", Attribute: "href"},
	}, true)
	client := &recordScrapingClient{records: []map[string]string{
		{"laufzeit": "Fest 5 Jahre", "zins": "1.45%", "link": "https://example.com/5"},
		{"laufzeit": "Fest 10 Jahre", "zins": "1.72%", "link": ""},
	}}
	if err := drive(t, cp, jobType, client, store, log); err != nil {
		t.Fatalf("Drive: %v", err)
	}

	if client.recordCalls != 1 || client.valueCalls != 0 {
		t.Fatalf("record/value calls = %d/%d, want the field path taken once", client.recordCalls, client.valueCalls)
	}
	req := client.requests[0]
	if len(req.Fields) != 3 || req.Fields[0].Name != "laufzeit" || req.Fields[2].Attribute != "href" {
		t.Errorf("request fields = %+v, want the authored three in order", req.Fields)
	}
	if req.Selector != "tr.row" || !req.AbsoluteLinks {
		t.Errorf("request = %+v, want the item selector and absolute links", req)
	}

	scope := soleInstanceKey(t, store)
	got := readVar(t, store, scope, "zinsen")
	if got == nil || got.Kind != model.VarJSON {
		t.Fatalf("result variable = %+v, want VarJSON", got)
	}
	var items []map[string]any
	if err := json.Unmarshal([]byte(got.Text), &items); err != nil {
		t.Fatalf("result JSON: %v (%q)", err, got.Text)
	}
	if len(items) != 2 {
		t.Fatalf("items = %d, want 2", len(items))
	}
	if items[0]["laufzeit"] != "Fest 5 Jahre" || items[0]["zins"] != "1.45%" {
		t.Errorf("first item = %v, want the row's fields together in one object", items[0])
	}
	if link, ok := items[1]["link"]; !ok || link != "" {
		t.Errorf("second item link = %v (present=%v), want an empty value, not a missing key", link, ok)
	}
}

// A task with no fields still takes the string path, so every model authored under
// ADR-0118 keeps the value it has.
func TestWebScrapeWithoutFieldsStillWritesStrings(t *testing.T) {
	log, store := openStore(t)
	cp, jobType := fieldScrapeProcess(t, nil, false)
	client := &recordScrapingClient{values: []string{"1.45%", "1.72%"}}
	if err := drive(t, cp, jobType, client, store, log); err != nil {
		t.Fatalf("Drive: %v", err)
	}
	if client.valueCalls != 1 || client.recordCalls != 0 {
		t.Fatalf("value/record calls = %d/%d, want the string path", client.valueCalls, client.recordCalls)
	}
	got := readVar(t, store, soleInstanceKey(t, store), "zinsen")
	if got == nil || got.Kind != model.VarJSON {
		t.Fatalf("result variable = %+v, want VarJSON", got)
	}
	var values []string
	if err := json.Unmarshal([]byte(got.Text), &values); err != nil {
		t.Fatalf("result is not a string array: %v (%q)", err, got.Text)
	}
	if len(values) != 2 || values[0] != "1.45%" {
		t.Errorf("values = %v, want the scraped strings", values)
	}
}

// A field scrape whose selector matched nothing writes an empty array — the token
// moves on, and the model's own count() decides what "no rows today" means.
func TestWebScrapeFieldScrapeWithoutMatchesWritesAnEmptyArray(t *testing.T) {
	log, store := openStore(t)
	cp, jobType := fieldScrapeProcess(t, []compiler.WebScrapeFieldConfig{{Name: "zins", Selector: "td"}}, false)
	if err := drive(t, cp, jobType, &recordScrapingClient{}, store, log); err != nil {
		t.Fatalf("Drive: %v", err)
	}
	got := readVar(t, store, soleInstanceKey(t, store), "zinsen")
	if got == nil || got.Kind != model.VarJSON || got.Text != "[]" {
		t.Fatalf("result variable = %+v, want an empty JSON array", got)
	}
}

// A client that predates ADR-0231 cannot assemble
// objects. The job fails with a message saying so rather than silently returning the
// wrong shape.
func TestWebScrapeFieldScrapeNeedsARecordClient(t *testing.T) {
	_, err := webscrape.Run(context.Background(), webscrape.Job{
		URL:      "https://example.com",
		Selector: "tr",
		Fields:   []webscrape.Field{{Name: "zins"}},
		Format:   "html",
	}, &feedRecordingClient{})
	if err == nil {
		t.Fatal("Run error = nil, want the missing capability reported")
	}
}
