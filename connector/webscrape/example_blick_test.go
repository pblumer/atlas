package webscrape_test

import (
	"encoding/json"
	"os"
	"testing"

	"github.com/pblumer/atlas/compiler"
	"github.com/pblumer/atlas/connector/webscrape"
	"github.com/pblumer/atlas/model"
	"github.com/pblumer/atlas/state"
	"github.com/pblumer/atlas/wal"
)

// blickExamplePath is the shipped RSS example. examples/models_test.go proves it
// compiles; these tests prove it does what its own documentation says — that the
// feed arrives as structured entries, that the FEEL filter keeps the right ones,
// and that the gateway routes on the result.
const blickExamplePath = "../../examples/blick-schlagzeilen.bpmn"

// blickFeed is a Blick-shaped feed: the three fields a reader sees plus the RFC-1123
// pubDate an RSS publisher writes, passed through verbatim (ADR-0190 does not reformat
// a source timestamp).
var blickFeed = []webscrape.FeedEntry{
	{
		Title:       "Bundesrat entscheidet über die Strommarkt-Vorlage",
		Link:        "https://www.blick.ch/politik/strommarkt",
		Description: "Die Landesregierung hat sich auf einen Kompromiss geeinigt.",
		Published:   "Wed, 26 Aug 2026 08:30:00 +0200",
	},
	{
		Title:       "FC Basel gewinnt das Spitzenspiel",
		Link:        "https://www.blick.ch/sport/fcb",
		Description: "Ein später Treffer entscheidet die Partie.",
		Published:   "Wed, 26 Aug 2026 09:05:00 +0200",
	},
	{
		Title:       "Hitzewelle: So heiss wird das Wochenende",
		Link:        "https://www.blick.ch/wetter/hitze",
		Description: "Bis zu 34 Grad im Mittelland.",
		Published:   "Wed, 26 Aug 2026 09:40:00 +0200",
	},
}

func blickExample(t *testing.T) *compiler.CompiledProcess {
	t.Helper()
	f, err := os.Open(blickExamplePath)
	if err != nil {
		t.Fatalf("open %s: %v", blickExamplePath, err)
	}
	defer f.Close()
	cp, err := compiler.Parse(wsDefKey, 1, f)
	if err != nil {
		t.Fatalf("compile %s: %v", blickExamplePath, err)
	}
	return cp
}

func driveBlick(t *testing.T, suchwort string) (*feedRecordingClient, *state.Store, *wal.Log) {
	t.Helper()
	log, store := openStore(t)
	client := &feedRecordingClient{entries: blickFeed}
	err := drive(t, blickExample(t), compiler.WebScrapeJobTypeIndex, client, store, log,
		model.VariableValue{Name: "suchwort", Kind: model.VarString, Text: suchwort})
	if err != nil {
		t.Fatalf("Drive: %v", err)
	}
	return client, store, log
}

// TestBlickExampleRequestsTheAuthoredFeed pins what the model asks the worker for:
// the feed URL, the rss parser chosen at deploy time, and the first-15 bound — and
// no HTML selector, which the compiler rejects in feed modes anyway.
func TestBlickExampleRequestsTheAuthoredFeed(t *testing.T) {
	client, _, _ := driveBlick(t, "basel")
	if len(client.requests) != 1 {
		t.Fatalf("feed requests = %d, want 1", len(client.requests))
	}
	req := client.requests[0]
	if req.URL != "https://www.blick.ch/rss.xml" {
		t.Errorf("url = %q, want the authored Blick feed", req.URL)
	}
	if req.Format != "rss" {
		t.Errorf("format = %q, want rss — the model, not the response, picks the parser", req.Format)
	}
	if req.MaxItems != 15 {
		t.Errorf("maxItems = %d, want the authored bound of 15", req.MaxItems)
	}
	if req.Selector != "" || req.Attribute != "" {
		t.Errorf("request carries HTML fields %+v, want none in a feed mode", req)
	}
}

// TestBlickExampleKeepsTheMatchingHeadlines runs the whole example: the feed lands
// as structured entries, the script keeps the ones whose title carries the search
// word, and the token parks on the review task the gateway routes to.
func TestBlickExampleKeepsTheMatchingHeadlines(t *testing.T) {
	_, store, _ := driveBlick(t, "basel")

	scope := soleInstanceKey(t, store)
	feed := readVar(t, store, scope, "schlagzeilen")
	if feed == nil || feed.Kind != model.VarJSON {
		t.Fatalf("schlagzeilen = %+v, want a structured JSON array", feed)
	}
	var entries []map[string]any
	if err := json.Unmarshal([]byte(feed.Text), &entries); err != nil {
		t.Fatalf("schlagzeilen is not a JSON array of objects: %v", err)
	}
	if len(entries) != len(blickFeed) {
		t.Fatalf("schlagzeilen entries = %d, want %d", len(entries), len(blickFeed))
	}
	if entries[0]["title"] != blickFeed[0].Title || entries[0]["link"] != blickFeed[0].Link {
		t.Errorf("first entry = %v, want the feed's first item", entries[0])
	}
	if entries[0]["published"] != blickFeed[0].Published {
		t.Errorf("published = %q, want the source timestamp verbatim", entries[0]["published"])
	}

	hits := readVar(t, store, scope, "treffer")
	if hits == nil || hits.Kind != model.VarJSON {
		t.Fatalf("treffer = %+v, want a JSON array", hits)
	}
	var matched []map[string]any
	if err := json.Unmarshal([]byte(hits.Text), &matched); err != nil {
		t.Fatalf("treffer is not a JSON array: %v", err)
	}
	if len(matched) != 1 {
		t.Fatalf("treffer = %d entries, want only the Basel headline: %s", len(matched), hits.Text)
	}
	if matched[0]["titel"] != blickFeed[1].Title || matched[0]["link"] != blickFeed[1].Link {
		t.Errorf("match = %v, want the Basel headline with its feed link", matched[0])
	}
	if matched[0]["veroeffentlicht"] != blickFeed[1].Published {
		t.Errorf("veroeffentlicht = %v, want the entry's published field", matched[0]["veroeffentlicht"])
	}
}

// TestBlickExampleEmptySearchWordMatchesEverything holds the claim the model's own
// documentation makes: the start form defaults suchwort to "", and contains(text, "")
// is true, so an unfiltered run keeps every headline instead of none.
func TestBlickExampleEmptySearchWordMatchesEverything(t *testing.T) {
	_, store, _ := driveBlick(t, "")
	scope := soleInstanceKey(t, store)
	hits := readVar(t, store, scope, "treffer")
	if hits == nil || hits.Kind != model.VarJSON {
		t.Fatalf("treffer = %+v, want a JSON array", hits)
	}
	var matched []map[string]any
	if err := json.Unmarshal([]byte(hits.Text), &matched); err != nil {
		t.Fatalf("treffer is not a JSON array: %v", err)
	}
	if len(matched) != len(blickFeed) {
		t.Errorf("treffer = %d entries, want all %d: %s", len(matched), len(blickFeed), hits.Text)
	}
}

// TestBlickExampleWithoutAMatchEnds is the gateway's other branch: nothing matches,
// count(treffer) > 0 is false, the default flow reaches "nichts Neues" and the
// instance completes rather than parking a token on the review task.
func TestBlickExampleWithoutAMatchEnds(t *testing.T) {
	_, store, _ := driveBlick(t, "eishockey")
	if n := mustActiveProcs(t, store); n != 0 {
		t.Errorf("live instances = %d, want 0 — no match must reach the end event", n)
	}
}
