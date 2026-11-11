// Package catalog is the Atlas report Worker's operation table (ADR-0555 §3): which
// values each operation takes in its input object, and the checks a deploy and a run
// share.
//
// It imports nothing of atlas's, for the reason connector/calendar/catalog gives: the
// compiler reads these rows to refuse a model at deploy, and connector/atlasreport reads
// the same rows to build its calls, so there is one table and nothing to keep in step.
//
// # The operations
//
// Each reads exactly one of Atlas's own read routes and folds its answer into a report
// in the contract atlas.report/1 (the chart component's input, ADR-0555 §2). Only routes
// that answer counts and durations are offered: a route that answers people's names is
// not an operation, so a report addressed to a group cannot disclose who holds or did
// what (ADR-0353, ADR-0515).
package catalog

import (
	"fmt"
	"slices"
	"sort"
	"strings"
)

// Result is what an operation hands back to its result variable.
type Result int

const (
	// ResultNone marks an operation that returns nothing; none of this Worker Type's
	// does. The value is the zero of the type.
	ResultNone Result = iota
	// ResultReport is a report in the contract atlas.report/1.
	ResultReport
)

// Idempotency and Effect are ADR-0538's two declarations, as their manifest values.
// Every operation reads: repeating it is harmless (natural), and nothing it does needs
// undoing (reversible — the manifest has no value for "no effect at all").
const (
	IdempotencyNatural = "natural"
	EffectReversible   = "reversible"
)

// The operations.
const (
	OpInstancesSummary  = "instances-summary"
	OpProcessStatistics = "process-statistics"
	OpIncidentSummary   = "incident-summary"
	OpDeliveryReport    = "delivery-report"
)

// The keys an input object may carry.
const (
	InProcess  = "process"
	InFrom     = "from"
	InTo       = "to"
	InBuckets  = "buckets"
	InBucket   = "bucket"
	InCatalog  = "catalog"
	InTitle    = "title"
	InLanguage = "language"
)

// Languages are the languages a report's series names and notes are written in.
var Languages = []string{"de", "en"}

// Bucket sizes a process-statistics series may be cut into.
const (
	BucketDay   = "day"
	BucketWeek  = "week"
	BucketMonth = "month"
)

// Buckets is the bucket sizes, in the order the Modeler offers them.
var Buckets = []string{BucketDay, BucketWeek, BucketMonth}

// MaxBuckets bounds a time series: each bucket is one statistics fold of up to ten
// thousand instances on the server, so the bound is a ceiling on the work one task asks.
const MaxBuckets = 26

// Op is one operation a model can author.
type Op struct {
	// Label describes the operation in an error message and the Modeler.
	Label string
	// Route is the read route the operation calls, for the documentation.
	Route string
	// Inputs are the keys the task's input object may carry, Required those it must.
	Inputs   []string
	Required []string
	// Result is what the operation answers; NeedsResult that a model must keep it — a
	// report nobody keeps is a read nothing asked for.
	Result      Result
	NeedsResult bool
	// Catalogue marks the operation that reads the service catalogue, which a server can
	// switch off (ADR-0434).
	Catalogue bool
	// Idempotency and Effect are the operation's ADR-0538 declarations.
	Idempotency string
	Effect      string
}

// Ops is the operation table.
var Ops = map[string]Op{
	OpInstancesSummary: {
		Label: "summarise the instances per process", Route: "GET /api/v1/instances/summary",
		Inputs: []string{InProcess, InTitle, InLanguage},
		Result: ResultReport, NeedsResult: true,
		Idempotency: IdempotencyNatural, Effect: EffectReversible,
	},
	OpProcessStatistics: {
		Label: "measure one process over a period", Route: "GET /api/v1/processes/{key}/statistics",
		Inputs: []string{InProcess, InFrom, InTo, InBuckets, InBucket, InTitle, InLanguage}, Required: []string{InProcess},
		Result: ResultReport, NeedsResult: true,
		Idempotency: IdempotencyNatural, Effect: EffectReversible,
	},
	OpIncidentSummary: {
		Label: "count the open incidents", Route: "GET /api/v1/incidents/summary",
		Inputs: []string{InProcess, InTitle, InLanguage},
		Result: ResultReport, NeedsResult: true,
		Idempotency: IdempotencyNatural, Effect: EffectReversible,
	},
	OpDeliveryReport: {
		Label: "measure the catalogue's delivery promises", Route: "GET /api/v1/catalog-products/delivery-report",
		Inputs: []string{InCatalog, InFrom, InTo, InTitle, InLanguage},
		Result: ResultReport, NeedsResult: true, Catalogue: true,
		Idempotency: IdempotencyNatural, Effect: EffectReversible,
	},
}

// Lookup returns the operation of that name.
func Lookup(name string) (Op, bool) {
	op, ok := Ops[name]
	return op, ok
}

// Names lists the operations, sorted.
func Names() []string {
	out := make([]string, 0, len(Ops))
	for n := range Ops {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}

// TakesResult reports whether the operation writes a result variable at all.
func (o Op) TakesResult() bool { return o.Result != ResultNone }

// CheckInputKeys reports the first key of an input object the operation does not take,
// and the first it needs that is missing.
func CheckInputKeys(name string, keys []string) error {
	op, ok := Ops[name]
	if !ok {
		return fmt.Errorf("atlasreport: unknown operation %q", name)
	}
	have := map[string]bool{}
	for _, k := range keys {
		if !slices.Contains(op.Inputs, k) {
			return fmt.Errorf("atlasreport: operation %q does not take %q in its input (it takes %s)", name, k, strings.Join(op.Inputs, ", "))
		}
		have[k] = true
	}
	for _, k := range op.Required {
		if !have[k] {
			return fmt.Errorf("atlasreport: operation %q needs %q in its input", name, k)
		}
	}
	return nil
}

// CheckBucket checks a bucket size.
func CheckBucket(b string) error {
	if !slices.Contains(Buckets, b) {
		return fmt.Errorf("atlasreport: bucket %q is not one of %s", b, strings.Join(Buckets, ", "))
	}
	return nil
}

// CheckLanguage checks a report's language.
func CheckLanguage(l string) error {
	if !slices.Contains(Languages, l) {
		return fmt.Errorf("atlasreport: language %q is not one of %s", l, strings.Join(Languages, ", "))
	}
	return nil
}

// CheckBuckets checks a number of buckets.
func CheckBuckets(n int64) error {
	if n < 1 || n > MaxBuckets {
		return fmt.Errorf("atlasreport: buckets is %d; it must be a whole number between 1 and %d", n, MaxBuckets)
	}
	return nil
}
