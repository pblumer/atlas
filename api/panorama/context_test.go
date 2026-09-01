package panorama

import (
	"strings"
	"testing"
)

// TestContextWindowIsAnAllowlist. A window is the bound on somebody else's cluster
// doing work for a page of ours, and the person who owns that cluster is not the
// person who typed the query string. A window outside the list is refused rather
// than clamped: silently answering a different question than the one asked is
// worse than saying no.
func TestContextWindowIsAnAllowlist(t *testing.T) {
	for _, name := range Windows() {
		window, ok := NewContextWindow(name, 1_000_000)
		if !ok {
			t.Fatalf("%q is listed as allowed and was refused", name)
		}
		seconds, _ := WindowSeconds(name)
		if window.To-window.From != seconds || window.To != 1_000_000 {
			t.Errorf("%q resolved to %+v", name, window)
		}
	}

	// An empty window is the default rather than an error: a caller who names none
	// is not asking a wrong question, only an unspecific one.
	window, ok := NewContextWindow("", 1_000_000)
	if !ok || window.Window != DefaultWindow {
		t.Errorf("an unnamed window = %+v, %v", window, ok)
	}

	for _, bad := range []string{"30d", "1s", "forever", "24H", "-1h", "1h "} {
		if _, ok := NewContextWindow(bad, 1_000_000); ok {
			t.Errorf("%q was accepted; the allowlist is the bound on somebody else's cluster", bad)
		}
	}
}

// TestBucketsCoarsenRatherThanLengthen. The panel's width does not change with the
// span, so a longer window must produce wider buckets rather than a longer series.
func TestBucketsCoarsenRatherThanLengthen(t *testing.T) {
	var previous int64
	for _, name := range Windows() {
		window, _ := NewContextWindow(name, 1_000_000)
		width := BucketSeconds(window)
		if width <= previous {
			t.Errorf("%q buckets are %ds wide, not coarser than the shorter window's %ds",
				name, width, previous)
		}
		if count := (window.To - window.From) / width; count > maxContextBuckets {
			t.Errorf("%q yields %d buckets, past the bound of %d", name, count, maxContextBuckets)
		}
		previous = width
	}

	// A degenerate window does not divide by zero and does not produce a negative
	// width — an adapter handed one would loop forever on it.
	if got := BucketSeconds(ContextWindow{From: 500, To: 500}); got < 1 {
		t.Errorf("an empty window has bucket width %d", got)
	}
	// Nor does a window shorter than the bucket count, which integer division would
	// otherwise round to zero: an adapter turns this into a histogram interval, and
	// "0s" is a query no cluster will accept.
	if got := BucketSeconds(ContextWindow{From: 0, To: maxContextBuckets - 1}); got != 1 {
		t.Errorf("a window shorter than the bucket count has width %d, want 1", got)
	}
}

// TestQueriesForKeepsUnidentifiableBindings. A binding no source can identify still
// produces a query, because *unidentifiable* is an answer this document owes the
// reader. Dropping it here would make it indistinguishable from an element that
// binds nothing at all.
func TestQueriesForKeepsUnidentifiableBindings(t *testing.T) {
	set := BindingSet{Bindings: []Binding{
		{ElementID: "e-node", Key: KeyRuntimeID, Values: []string{"node-9"}},
		{ElementID: "e-app", Key: KeyProcessID, Values: []string{"ship", "bill"}},
		{ElementID: "e-app", Key: KeyApplicationID, Values: []string{"proj-1"}},
		{ElementID: "e-other", Key: KeyProcessID, Values: []string{"not-mine"}},
	}}
	window, _ := NewContextWindow(Window1h, 1_000)

	queries := QueriesFor(set, "e-app", window)
	if len(queries) != 3 {
		t.Fatalf("queries = %+v, want every bound value of the element", queries)
	}
	// Stable order, by key then value: two identical requests must produce a
	// document that can be diffed.
	want := []string{
		KeyApplicationID + "=proj-1", KeyProcessID + "=bill", KeyProcessID + "=ship",
	}
	for i, query := range queries {
		if got := query.Key + "=" + query.Value; got != want[i] {
			t.Errorf("query %d = %s, want %s", i, got, want[i])
		}
		if query.Window != window {
			t.Errorf("query %d carries %+v, want the requested window", i, query.Window)
		}
	}

	// Another element's bindings are not this element's context.
	if got := QueriesFor(set, "e-missing", window); len(got) != 0 {
		t.Errorf("an element with no bindings produced %+v", got)
	}
}

// TestAssembleContextPublishesWhatItCannotDo. The limits travel with every answer,
// for the same reason the drift journal's do: without them a reader cannot tell an
// architecture that was quiet from a question nobody could ask.
func TestAssembleContextPublishesWhatItCannotDo(t *testing.T) {
	doc := AssembleContext("e-1", ContextWindow{Window: Window1h, From: 0, To: 3600}, nil, 3600)
	if len(doc.Limits) != 3 {
		t.Fatalf("limits = %+v, want all three", doc.Limits)
	}
	joined := ""
	for _, limit := range doc.Limits {
		if limit.Reason == "" {
			t.Errorf("limit %q has no reason", limit.Limit)
		}
		joined += limit.Limit + " " + limit.Reason
	}
	for _, must := range []string{"aged out", "stored or cached", "per-element labels"} {
		if !strings.Contains(joined, must) {
			t.Errorf("the limits do not mention %q: %+v", must, doc.Limits)
		}
	}
	// An empty document answers with an empty list rather than a null: the renderer
	// iterates it.
	if doc.Results == nil || doc.ContractVersion != ContextContractVersion {
		t.Errorf("empty document = %+v", doc)
	}
}

// TestAssembleContextOrdersByResourceNotByStore. Somebody reading down the page is
// looking at one resource at a time, not one store at a time.
func TestAssembleContextOrdersByResourceNotByStore(t *testing.T) {
	window := ContextWindow{Window: Window1h, From: 0, To: 3600}
	doc := AssembleContext("e-1", window, []ContextResult{
		{Source: ContextSourceMetrics, Key: KeyProcessID, Value: "ship", State: ContextUnidentifiable},
		{Source: ContextSourceEvents, Key: KeyProcessID, Value: "ship", State: ContextAvailable},
		{Source: ContextSourceEvents, Key: KeyApplicationID, Value: "proj-1", State: ContextEmpty},
	}, 3600)

	var order []string
	for _, result := range doc.Results {
		order = append(order, result.Key+"/"+result.Value+"/"+result.Source)
	}
	want := []string{
		KeyApplicationID + "/proj-1/" + ContextSourceEvents,
		KeyProcessID + "/ship/" + ContextSourceEvents,
		KeyProcessID + "/ship/" + ContextSourceMetrics,
	}
	for i := range want {
		if order[i] != want[i] {
			t.Errorf("result %d = %s, want %s", i, order[i], want[i])
		}
	}
}

// TestAssembleContextBoundsAndSaysSo. An adapter must not be able to decide how big
// this document is, and a short answer must not read as a complete one.
func TestAssembleContextBoundsAndSaysSo(t *testing.T) {
	window := ContextWindow{Window: Window1h, From: 0, To: 3600}

	var many []ContextResult
	for i := range maxContextResults + 5 {
		many = append(many, ContextResult{
			Source: ContextSourceEvents, Key: KeyProcessID,
			Value: string(rune('a'+i%26)) + strings.Repeat("x", i), State: ContextEmpty,
		})
	}
	doc := AssembleContext("e-1", window, many, 3600)
	if len(doc.Results) != maxContextResults || !doc.Truncated {
		t.Fatalf("document holds %d results, truncated=%v", len(doc.Results), doc.Truncated)
	}

	// Buckets are trimmed from the oldest end: the recent end of a window is the end
	// somebody is looking at.
	var buckets []Bucket
	for i := range maxContextBuckets + 10 {
		buckets = append(buckets, Bucket{At: int64(i), Value: float64(i)})
	}
	doc = AssembleContext("e-1", window, []ContextResult{{
		Source: ContextSourceEvents, Key: KeyProcessID, Value: "ship", State: ContextAvailable,
		Measures: []Measure{{Name: "instances", Buckets: buckets}},
	}}, 3600)
	kept := doc.Results[0].Measures[0].Buckets
	if len(kept) != maxContextBuckets {
		t.Fatalf("kept %d buckets, want %d", len(kept), maxContextBuckets)
	}
	if kept[len(kept)-1].At != int64(maxContextBuckets+9) {
		t.Errorf("the newest bucket was trimmed away: last is %+v", kept[len(kept)-1])
	}

	// Measures past the bound go from the end, because the adapter's order is its
	// own statement of which matter most.
	var measures []Measure
	for i := range maxContextMeasures + 3 {
		measures = append(measures, Measure{Name: string(rune('a' + i))})
	}
	doc = AssembleContext("e-1", window, []ContextResult{{
		Source: ContextSourceEvents, Key: KeyProcessID, Value: "ship",
		State: ContextAvailable, Measures: measures,
	}}, 3600)
	got := doc.Results[0].Measures
	if len(got) != maxContextMeasures || got[0].Name != "a" {
		t.Errorf("measures = %+v, want the adapter's first %d", got, maxContextMeasures)
	}
}

// TestAssembleContextInventsNothing. A result missing from the input is missing
// from the output: a placeholder this code made up would be indistinguishable from
// an answer a source actually gave, which is the one thing every state in this
// contract exists to keep apart.
func TestAssembleContextInventsNothing(t *testing.T) {
	window := ContextWindow{Window: Window24h, From: 0, To: 86400}
	doc := AssembleContext("e-1", window, []ContextResult{}, 86400)
	if len(doc.Results) != 0 {
		t.Errorf("an element nobody could answer for produced %+v", doc.Results)
	}
	if doc.ElementID != "e-1" || doc.Window != window || doc.ObservedAt != 86400 {
		t.Errorf("document = %+v", doc)
	}
	if doc.Truncated {
		t.Error("an empty document reports itself truncated")
	}
}
