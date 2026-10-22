package api

import (
	"fmt"
	"math"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/pblumer/atlas/metrics"
)

// apiTokenCollector reports the API tokens' lifetimes on /metrics (ADR-0194,
// amendment 2026-10-22). A token that runs out fails every call its machine makes,
// without a word from the server; the Console warns about it, but only to whoever
// opens the Console. These series let an alert say it instead.
//
// They are bare aggregates. Which token is about to go is a value the data invents —
// a name an administrator typed — so a label carrying it is exactly what ADR-0142's
// cardinality rule forbids, and on /metrics it would also hand every holder of a
// metrics-scoped token the inventory of the server's credentials. GET
// /api/v1/api-tokens answers "which", to an administrator.
//
// Like the durability collector it is read at scrape time, from the in-memory index
// authentication already reads on the handler goroutine: nothing is counted as tokens
// are used, so neither the engine nor the run loop is touched (I1, I3).
type apiTokenCollector struct {
	srv *Server

	soon    *prometheus.Desc
	expired *prometheus.Desc
	next    *prometheus.Desc
}

func newAPITokenCollector(s *Server) *apiTokenCollector {
	d := func(name, help string) *prometheus.Desc {
		return prometheus.NewDesc(metrics.Namespace+"_"+name, help, nil, nil)
	}
	return &apiTokenCollector{
		srv: s,
		soon: d("api_tokens_expiring_soon",
			fmt.Sprintf("API tokens still accepted that expire within %d days, the Console's warning window (ADR-0194).",
				int(apiTokenExpiryWarning.Hours()/24))),
		expired: d("api_tokens_expired",
			"API tokens whose lifetime has run out and that are refused, but have not been revoked yet."),
		next: d("api_token_next_expiry_timestamp_seconds",
			"When the next API token still accepted expires, in unix seconds; +Inf when none expires."),
	}
}

func (c *apiTokenCollector) Describe(ch chan<- *prometheus.Desc) {
	ch <- c.soon
	ch <- c.expired
	ch <- c.next
}

// Collect reads the index at the server's clock. The next expiry is +Inf rather than
// absent when no accepted token runs out: "never" is a known answer, and an absent
// series is how /metrics says a number could not be read (ADR-0142). +Inf also keeps
// the natural alert — next expiry minus now below some margin — quiet.
func (c *apiTokenCollector) Collect(ch chan<- prometheus.Metric) {
	e := c.srv.apiTokens.expiry(c.srv.now()/nanosPerSecond, apiTokenExpiryWarning)
	next := math.Inf(1)
	if e.next != 0 {
		next = float64(e.next)
	}
	ch <- prometheus.MustNewConstMetric(c.soon, prometheus.GaugeValue, float64(e.soon))
	ch <- prometheus.MustNewConstMetric(c.expired, prometheus.GaugeValue, float64(e.expired))
	ch <- prometheus.MustNewConstMetric(c.next, prometheus.GaugeValue, next)
}
