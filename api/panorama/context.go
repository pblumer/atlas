package panorama

import (
	"sort"
	"strings"
)

// Historical context from stores outside Atlas (ADR-0189 P5b).
//
// P4 says what an element is doing now. P5a says what has been seen to change
// while somebody was looking. Neither can answer the question an operator actually
// arrives with — *has it been like this?* — because both are bounded by this
// process: one reads the current instant, the other remembers only what was asked
// for and forgets it on restart.
//
// The stores that can answer it already exist and are already fed by Atlas. The
// event log is exported to OpenSearch (ADR-0114); the metrics are scraped by
// Prometheus (ADR-0142). This slice *queries* them. It copies nothing: ADR-0189
// rejected "copy all remote metrics and logs into a Panorama-specific internal
// database" by name, and a cache of somebody else's history is that database with
// a shorter retention and no owner.
//
// So this file is a projection of answers, not a store of them. The server asks
// whichever adapters can identify the resources an element binds to, and this
// assembles what came back — including, in the same shape and with the same
// weight, every adapter that could not answer and why.

// ContextContractVersion is the shape of the document below.
const ContextContractVersion = 1

// Context sources. Each names a store outside Atlas, because an answer's worth
// depends on where it came from and an operator disagreeing with one needs to know
// which system to go and argue with.
const (
	// ContextSourceEvents is Atlas's own event log, exported to an OpenSearch index.
	// It is the only store that can speak about one process: every record carries
	// the definition key it belongs to.
	ContextSourceEvents = "events"
	// ContextSourceMetrics is a Prometheus-compatible store scraping this or another
	// Atlas node. It speaks about *nodes* and never about one process — see
	// ContextUnidentifiable.
	ContextSourceMetrics = "metrics"
)

// The states one source can be in for one bound value. They are six rather than
// two because every one of them sends somebody somewhere different, and collapsing
// them into "no data" would be the same lie this whole feature is arranged against:
// "nobody looked" and "somebody looked and found nothing" are not the same finding.
const (
	// ContextNotConfigured: no such store is wired to this server. Nothing was
	// asked, so nothing is claimed.
	ContextNotConfigured = "not-configured"
	// ContextUnidentifiable: the store is wired and was not asked, because it cannot
	// name a thing of this kind at all. Atlas's metrics carry no per-element labels
	// by deliberate design (ADR-0142 forbids labelling by process id, instance key
	// or any other value the data can invent), so a metrics store can answer about a
	// node and never about one process. That is a property of the contract, not a
	// gap in the data, and reporting it as "nothing found" would send somebody to
	// look for a series that must not exist.
	ContextUnidentifiable = "unidentifiable"
	// ContextUnreachable: it was asked and could not be reached. Nothing is known.
	ContextUnreachable = "unreachable"
	// ContextRefused: it was reached and declined to answer — credentials, or a
	// permission on its side. Something is there and this server may not have it,
	// which is a different thing to tell an operator than "it is down".
	ContextRefused = "refused"
	// ContextEmpty: asked, answered, and holds nothing about this value in this
	// window. This is the only one of the six that is a statement about the
	// architecture rather than about the lookup.
	ContextEmpty = "empty"
	// ContextAvailable: asked, answered, and has something to show.
	ContextAvailable = "available"
)

// The query windows. It is an allowlist rather than a free-form duration because a
// window is the bound on somebody else's cluster doing work for a page of ours: an
// arbitrary range is an arbitrary query, and the person who owns that cluster is
// not the person who typed it.
const (
	Window1h  = "1h"
	Window6h  = "6h"
	Window24h = "24h"
	Window7d  = "7d"
)

// DefaultWindow is what a caller who names none gets. A day is the span in which
// "has it been like this" is usually asked, and it is short enough that the
// buckets below stay readable.
const DefaultWindow = Window24h

// windowSeconds is each allowed window's length. A window not in this map is
// refused rather than clamped: silently answering a different question than the
// one asked is worse than saying no.
var windowSeconds = map[string]int64{
	Window1h:  3600,
	Window6h:  6 * 3600,
	Window24h: 24 * 3600,
	Window7d:  7 * 24 * 3600,
}

// Windows lists the allowed windows, shortest first, for an API that has to tell a
// client what it may ask for.
func Windows() []string { return []string{Window1h, Window6h, Window24h, Window7d} }

// WindowSeconds reports how long a window is, and whether it is one this contract
// allows.
func WindowSeconds(window string) (int64, bool) {
	seconds, ok := windowSeconds[window]
	return seconds, ok
}

const (
	// maxContextBuckets bounds one measure's series. It is a shape somebody reads in
	// a side panel, not a chart to zoom into: past this many points the buckets are
	// thinner than the pixels drawn for them.
	maxContextBuckets = 48
	// maxContextMeasures bounds how many measures one source returns for one value,
	// so an adapter cannot decide on its own how big this document is.
	maxContextMeasures = 8
	// maxContextResults bounds the whole document. One element can bind several
	// values and each is asked of every source that can identify it, so the product
	// is what needs the bound.
	maxContextResults = 24
)

// Bucket is one interval of a measure: the moment the interval starts, in Unix
// seconds, and the value observed across it.
type Bucket struct {
	At    int64   `json:"at"`
	Value float64 `json:"value"`
}

// Measure is one named series over the window.
type Measure struct {
	// Name is the stable identifier a client may key on; Label is what a person
	// reads. Both travel because one of them is a contract and the other is prose,
	// and using either for both jobs makes it impossible to change the prose.
	Name  string `json:"name"`
	Label string `json:"label"`
	// Total is the measure across the whole window. It is carried rather than left
	// to be summed from the buckets, because for a gauge the sum of the buckets is
	// not the total and a client cannot tell which kind it has.
	Total   float64  `json:"total"`
	Buckets []Bucket `json:"buckets"`
}

// ContextWindow is the span a result covers, in Unix seconds.
type ContextWindow struct {
	Window string `json:"window"`
	From   int64  `json:"from"`
	To     int64  `json:"to"`
}

// ContextResult is one source's answer about one bound value.
type ContextResult struct {
	Source string `json:"source"`
	Key    string `json:"key"`
	Value  string `json:"value"`
	State  string `json:"state"`
	// Reason is why the state is what it is, in the source's own words. It is
	// required for every state except available: a source that could not answer owes
	// the reader a sentence, and one that could does not need to explain itself.
	Reason   string            `json:"reason,omitempty"`
	Measures []Measure         `json:"measures,omitempty"`
	Detail   map[string]string `json:"detail,omitempty"`
}

// ContextLimit is one thing this surface cannot do, with the reason.
type ContextLimit struct {
	Limit  string `json:"limit"`
	Reason string `json:"reason"`
}

// contextLimits travels with every answer, for the same reason the drift journal's
// limits do: without them a reader cannot tell an architecture that was quiet from
// a question nobody could ask. These are properties of the design rather than of a
// request, so the list is fixed.
var contextLimits = []ContextLimit{
	{
		Limit: "not Atlas's memory",
		Reason: "Both stores are external and retained on their own terms. What they have " +
			"aged out, this cannot show, and Atlas does not know what they dropped.",
	},
	{
		Limit: "a query, not a copy",
		Reason: "Nothing here is stored or cached. Each answer is read when it is asked for, " +
			"which is why Panorama stays a correlation surface rather than a second " +
			"history with a shorter retention and no owner.",
	},
	{
		Limit: "only what a store can identify",
		Reason: "Atlas's metrics carry no per-element labels by design, so a metrics store " +
			"answers about a node and never about one process. That is reported as " +
			"unidentifiable rather than as an absence of data.",
	},
}

// ContextDocument is what a caller asking "has it been like this" gets, for one
// element.
//
// It is scoped to one element rather than to a model on purpose. Every bound value
// costs a query against somebody else's cluster, and a model-wide answer would
// multiply that by the whole landscape for a panel that shows one element at a
// time.
type ContextDocument struct {
	ContractVersion int             `json:"contractVersion"`
	ElementID       string          `json:"elementId"`
	ObservedAt      int64           `json:"observedAt"`
	Window          ContextWindow   `json:"window"`
	Results         []ContextResult `json:"results"`
	// Truncated reports that results were dropped to stay inside the bound, so a
	// short answer is not read as a complete one.
	Truncated bool           `json:"truncated"`
	Limits    []ContextLimit `json:"limits"`
}

// ContextQuery is one lookup the server is asked to perform: a bound value, and
// the window to ask about.
type ContextQuery struct {
	Key    string
	Value  string
	Window ContextWindow
}

// QueriesFor lists the lookups one element's bindings imply, in a stable order.
//
// A binding whose value no source can identify still produces a query: the answer
// *unidentifiable* is one this document owes the reader, and dropping the binding
// here would make it indistinguishable from an element that binds nothing.
func QueriesFor(set BindingSet, elementID string, window ContextWindow) []ContextQuery {
	var queries []ContextQuery
	for _, binding := range set.Bindings {
		if binding.ElementID != elementID {
			continue
		}
		for _, value := range binding.Values {
			queries = append(queries, ContextQuery{Key: binding.Key, Value: value, Window: window})
		}
	}
	sort.SliceStable(queries, func(i, j int) bool {
		if queries[i].Key != queries[j].Key {
			return queries[i].Key < queries[j].Key
		}
		return queries[i].Value < queries[j].Value
	})
	return queries
}

// NewContextWindow resolves a requested window name against the allowlist and
// anchors it at now.
func NewContextWindow(window string, now int64) (ContextWindow, bool) {
	if strings.TrimSpace(window) == "" {
		window = DefaultWindow
	}
	seconds, ok := windowSeconds[window]
	if !ok {
		return ContextWindow{}, false
	}
	return ContextWindow{Window: window, From: now - seconds, To: now}, true
}

// BucketSeconds is how wide one bucket of a window is. It divides the window into
// at most maxContextBuckets intervals, so a longer window gets coarser buckets
// rather than a longer series — the panel's width does not change with the span.
func BucketSeconds(window ContextWindow) int64 {
	span := window.To - window.From
	if span <= 0 {
		return 1
	}
	width := span / maxContextBuckets
	if width < 1 {
		width = 1
	}
	return width
}

// AssembleContext builds the document from what the sources returned.
//
// It bounds and orders; it never invents. A result missing from the input is
// missing from the output, because a placeholder this function made up would be
// indistinguishable from an answer a source actually gave.
func AssembleContext(elementID string, window ContextWindow, results []ContextResult, now int64) ContextDocument {
	doc := ContextDocument{
		ContractVersion: ContextContractVersion,
		ElementID:       elementID,
		ObservedAt:      now,
		Window:          window,
		Results:         []ContextResult{},
		Limits:          contextLimits,
	}
	// Stable order: by bound value, then by source. Somebody reading down the page
	// is looking at one resource at a time, not one store at a time.
	sorted := append([]ContextResult(nil), results...)
	sort.SliceStable(sorted, func(i, j int) bool {
		a, b := sorted[i], sorted[j]
		if a.Key != b.Key {
			return a.Key < b.Key
		}
		if a.Value != b.Value {
			return a.Value < b.Value
		}
		return a.Source < b.Source
	})
	if len(sorted) > maxContextResults {
		sorted = sorted[:maxContextResults]
		doc.Truncated = true
	}
	for _, result := range sorted {
		doc.Results = append(doc.Results, boundResult(result))
	}
	return doc
}

// boundResult trims one source's answer to what this document carries. Measures
// past the bound are dropped in the order the adapter listed them, because that
// order is the adapter's own statement of which matter most; buckets are trimmed
// from the oldest, because the recent end of a window is the end somebody is
// looking at.
func boundResult(result ContextResult) ContextResult {
	if len(result.Measures) > maxContextMeasures {
		result.Measures = result.Measures[:maxContextMeasures]
	}
	measures := make([]Measure, 0, len(result.Measures))
	for _, measure := range result.Measures {
		if len(measure.Buckets) > maxContextBuckets {
			measure.Buckets = measure.Buckets[len(measure.Buckets)-maxContextBuckets:]
		}
		measures = append(measures, measure)
	}
	if len(measures) > 0 {
		result.Measures = measures
	}
	return result
}
