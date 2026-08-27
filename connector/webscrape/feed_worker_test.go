package webscrape_test

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/pblumer/atlas/compiler"
	"github.com/pblumer/atlas/connector/webscrape"
	"github.com/pblumer/atlas/engine"
	"github.com/pblumer/atlas/job"
	"github.com/pblumer/atlas/model"
	"github.com/pblumer/atlas/state"
	"github.com/pblumer/atlas/wal"
)

type feedRecordingClient struct {
	requests  []webscrape.Request
	entries   []webscrape.FeedEntry
	feedCalls int
}

func (r *feedRecordingClient) Scrape(context.Context, webscrape.Request) ([]string, error) {
	return nil, nil
}

func (r *feedRecordingClient) ScrapeFeed(_ context.Context, req webscrape.Request) ([]webscrape.FeedEntry, error) {
	r.feedCalls++
	r.requests = append(r.requests, req)
	return r.entries, nil
}

func feedThenWaitProcess(t *testing.T, format compiler.WebScrapeFormat, maxItems int32) (*compiler.CompiledProcess, int32) {
	t.Helper()
	b := compiler.NewBuilder(wsDefKey, "feed", 1)
	start := b.AddStartEvent()
	feed := b.AddWebScrapeExtractionTask(compiler.WebScrapeExtractionConfig{
		Url:      compiler.RestExpr{Literal: "https://example.com/feed"},
		Format:   format,
		MaxItems: maxItems,
		Result:   "headlines",
		Retries:  3,
	})
	wait := b.AddServiceTask("wait", 3)
	end := b.AddEndEvent()
	b.Connect(start, feed)
	b.Connect(feed, wait)
	b.Connect(wait, end)
	cp, err := b.Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	return cp, cp.ConnectorTask(cp.Node(feed).Detail).JobType
}

func TestWebScrapeConnectorWritesStructuredFeedArray(t *testing.T) {
	log, store := openStore(t)
	cp, jobType := feedThenWaitProcess(t, compiler.WebScrapeFormatRSS, 2)
	client := &feedRecordingClient{entries: []webscrape.FeedEntry{
		{Title: "Alpha", Link: "https://example.com/a", Description: "A", Published: "date-a"},
		{Title: "Beta", Link: "https://example.com/b"},
	}}
	if err := drive(t, cp, jobType, client, store, log); err != nil {
		t.Fatalf("Drive: %v", err)
	}
	if client.feedCalls != 1 || len(client.requests) != 1 {
		t.Fatalf("feed calls/requests = %d/%d, want 1/1", client.feedCalls, len(client.requests))
	}
	req := client.requests[0]
	if req.Format != "rss" || req.MaxItems != 2 || req.Selector != "" || req.Attribute != "" {
		t.Errorf("request = %+v, want rss/maxItems=2 and no HTML fields", req)
	}

	scope := soleInstanceKey(t, store)
	got := readVar(t, store, scope, "headlines")
	if got == nil || got.Kind != model.VarJSON {
		t.Fatalf("result variable = %+v, want VarJSON", got)
	}
	var entries []map[string]string
	if err := json.Unmarshal([]byte(got.Text), &entries); err != nil {
		t.Fatalf("result JSON: %v (%q)", err, got.Text)
	}
	if len(entries) != 2 {
		t.Fatalf("entries = %d, want 2", len(entries))
	}
	for i, entry := range entries {
		for _, key := range []string{"title", "link", "description", "published"} {
			if _, ok := entry[key]; !ok {
				t.Errorf("entry %d lacks key %q: %#v", i, key, entry)
			}
		}
	}
	if entries[0]["title"] != "Alpha" || entries[1]["description"] != "" || entries[1]["published"] != "" {
		t.Errorf("entries = %#v, want stable mapped fields and empty missing fields", entries)
	}
}

// TestWebScrapeFeedResultRecoversWithoutRefetch proves the external observation is
// frozen into the job-completion variable event. Recovery replays that fact into a
// fresh state store; it does not execute the feed client again (I4/I6).
func TestWebScrapeFeedResultRecoversWithoutRefetch(t *testing.T) {
	dir := t.TempDir()
	cp, jobType := feedThenWaitProcess(t, compiler.WebScrapeFormatAtom, 1)
	clock := &fixedClock{}
	client := &feedRecordingClient{entries: []webscrape.FeedEntry{{
		Title: "Frozen", Link: "https://example.com/frozen", Description: "once", Published: "2026-08-26T10:00:00Z",
	}}}

	log1, err := wal.Open(wal.Options{Dir: filepath.Join(dir, "wal")})
	if err != nil {
		t.Fatalf("wal.Open 1: %v", err)
	}
	store1, err := state.Open(filepath.Join(dir, "state"))
	if err != nil {
		t.Fatalf("state.Open 1: %v", err)
	}
	p1 := engine.New(1, log1, store1, clock)
	p1.Deploy(cp)
	if err := p1.Recover(); err != nil {
		t.Fatalf("Recover 1: %v", err)
	}
	runner := job.NewRunner(store1, p1)
	runner.HandleWithOutput(jobType, func(state.Reader) job.OutputHandler {
		return webscrape.Handler(store1, func(uint64) *compiler.CompiledProcess { return cp }, client)
	})
	p1.CreateInstance(cp.Key)
	if err := runner.Drive(); err != nil {
		t.Fatalf("Drive 1: %v", err)
	}
	if client.feedCalls != 1 {
		t.Fatalf("live feed calls = %d, want 1", client.feedCalls)
	}
	liveScope := soleInstanceKey(t, store1)
	live := readVar(t, store1, liveScope, "headlines")
	if live == nil {
		t.Fatal("live result missing")
	}
	liveText := live.Text
	store1.Close()
	log1.Close()

	log2, err := wal.Open(wal.Options{Dir: filepath.Join(dir, "wal")})
	if err != nil {
		t.Fatalf("wal.Open 2: %v", err)
	}
	store2, err := state.Open(filepath.Join(dir, "state-replay"))
	if err != nil {
		t.Fatalf("state.Open replay: %v", err)
	}
	t.Cleanup(func() { store2.Close(); log2.Close() })
	p2 := engine.New(1, log2, store2, clock)
	p2.Deploy(cp)
	if err := p2.Recover(); err != nil {
		t.Fatalf("Recover 2: %v", err)
	}
	if client.feedCalls != 1 {
		t.Fatalf("feed calls after replay = %d, want still 1 (no refetch)", client.feedCalls)
	}
	replayScope := soleInstanceKey(t, store2)
	replayed := readVar(t, store2, replayScope, "headlines")
	if replayed == nil || replayed.Text != liveText || replayed.Kind != live.Kind {
		t.Fatalf("replayed result = %+v, live = %+v", replayed, live)
	}
}
