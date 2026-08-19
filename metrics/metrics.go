// Package metrics is Atlas's Prometheus exposition: an owned registry, the naming
// conventions every Atlas metric follows, and the HTTP handler that serves them
// (ADR-0142).
//
// # Why an owned registry
//
// Nothing here touches prometheus.DefaultRegisterer. A package-level default is global
// mutable state shared with every library in the process: it makes what an operator
// scrapes depend on the import graph, and it makes tests order-dependent. An owned
// registry means the exported set is exactly what Atlas registered, and a test can
// scrape a server without reaching through a global. Go-runtime and process collectors
// are opt-in for the same reason — what appears at /metrics should be a decision.
//
// # The two rules
//
// **Bounded cardinality.** A label's values must be fixed by the code, not by the data.
// Never label by instance key, job key, correlation key, process id, element id, URL, or
// any other value an operator or a model can invent: one such label turns a metric into
// unboundedly many time series. A per-definition breakdown is a query over the API,
// which can paginate; a scrape target can only fall over.
//
// **No allocation on the hot path.** A labeled metric is resolved to its concrete child
// once, when the registry is built, and stored as a field; the engine's batch loop then
// touches a prometheus.Counter, never a *Vec — no map lookup, no label slice, no
// allocation (invariant I1). The proof is a benchmark, not a review.
package metrics

import (
	"net/http"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	dto "github.com/prometheus/client_model/go"
)

// Namespace prefixes every Atlas metric name, so a scrape shared with other exporters
// stays unambiguous.
const Namespace = "atlas"

// Registry owns the Prometheus registry Atlas exports and the handler that serves it.
type Registry struct {
	reg *prometheus.Registry
}

// NewRegistry builds an empty registry. It deliberately registers no Go-runtime or
// process collectors: a caller that wants them adds them explicitly.
func NewRegistry() *Registry {
	return &Registry{reg: prometheus.NewRegistry()}
}

// Register adds collectors to the registry. A duplicate or inconsistent registration is
// a programming error caught at construction, not at scrape time, so it is returned
// rather than swallowed.
func (r *Registry) Register(cs ...prometheus.Collector) error {
	for _, c := range cs {
		if err := r.reg.Register(c); err != nil {
			return err
		}
	}
	return nil
}

// Handler serves the exposition. Collection errors are reported to the scraper rather
// than silently dropped: a gauge that cannot be read should show up as a broken scrape,
// not as a plausible number.
func (r *Registry) Handler() http.Handler {
	return promhttp.HandlerFor(r.reg, promhttp.HandlerOpts{ErrorHandling: promhttp.HTTPErrorOnError})
}

// Gather returns the current metric families, for tests and for callers that want the
// numbers without going through HTTP.
func (r *Registry) Gather() ([]*dto.MetricFamily, error) { return r.reg.Gather() }

// BuildInfo is the conventional single-series metric carrying the running build, so a
// dashboard can correlate a graph with the binary that produced it. The label values are
// fixed at construction — one series, forever.
func BuildInfo(version, revision string) prometheus.Collector {
	g := prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Namespace: Namespace,
		Name:      "build_info",
		Help:      "Always 1; the labels carry the running Atlas version and VCS revision.",
	}, []string{"version", "revision"})
	g.WithLabelValues(version, revision).Set(1)
	return g
}
