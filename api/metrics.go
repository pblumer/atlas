package api

import (
	"net/http"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/pblumer/atlas/metrics"
)

// The /metrics surface (ADR-0142). This slice exports the *durability* metrics — the
// applied log position, what the checkpoint root holds, what the WAL directory costs,
// and how far the exporter trails — and every one of them is read from durable state
// when Prometheus scrapes.
//
// That is the whole design, not an implementation detail. Because there is no in-memory
// counter to run ahead of the disk, a metric here cannot claim more than is durable
// (invariant I2 holds by construction), and because nothing is incremented as work
// happens, the engine's hot path is untouched (invariant I1). It also costs nothing
// when nobody scrapes.
//
// The collector reads through the same helpers GET /api/v1/checkpoints uses, so the two
// operator surfaces cannot drift into disagreeing about the same facts.

// durabilityCollector reports the durable-state gauges at scrape time. It is a
// prometheus.Collector rather than a set of gauges the server pokes: a Collector is
// pull-shaped, which is exactly the shape of "read what is on disk right now".
type durabilityCollector struct {
	srv *Server

	appliedPosition  *prometheus.Desc
	checkpoints      *prometheus.Desc
	checkpointPos    *prometheus.Desc
	checkpointAge    *prometheus.Desc
	lastPassAt       *prometheus.Desc
	lastPassFailed   *prometheus.Desc
	lastPassRemoved  *prometheus.Desc
	walSegments      *prometheus.Desc
	walBytes         *prometheus.Desc
	exporterPosition *prometheus.Desc
	exporterLag      *prometheus.Desc
}

func newDurabilityCollector(s *Server) *durabilityCollector {
	d := func(name, help string) *prometheus.Desc {
		return prometheus.NewDesc(metrics.Namespace+"_"+name, help, nil, nil)
	}
	return &durabilityCollector{
		srv:             s,
		appliedPosition: d("applied_log_position", "Highest log position the state store has durably applied."),
		checkpoints:     d("checkpoints", "Recovery checkpoints currently published on disk."),
		checkpointPos: d("checkpoint_position",
			"Applied log position of the newest checkpoint that still verifies; 0 when there is none."),
		checkpointAge: d("checkpoint_age_seconds",
			"Age of the newest checkpoint that still verifies; the log past it is what a restart replays."),
		lastPassAt:      d("checkpoint_last_pass_timestamp_seconds", "When the last checkpoint pass ran, in unix seconds."),
		lastPassFailed:  d("checkpoint_last_pass_failed", "1 when the last checkpoint pass reported an error, else 0."),
		lastPassRemoved: d("checkpoint_last_pass_segments_removed", "WAL segments the last pass compacted away."),
		walSegments:     d("wal_segments", "Segment files currently in the write-ahead log."),
		walBytes:        d("wal_bytes", "Bytes currently held by the write-ahead log."),
		exporterPosition: d("exporter_position",
			"Highest log position the OpenSearch exporter has provably indexed (ADR-0114)."),
		exporterLag: d("exporter_lag_positions",
			"Log positions the exporter still trails the durable applied position by."),
	}
}

func (c *durabilityCollector) Describe(ch chan<- *prometheus.Desc) {
	ch <- c.appliedPosition
	ch <- c.checkpoints
	ch <- c.checkpointPos
	ch <- c.checkpointAge
	ch <- c.lastPassAt
	ch <- c.lastPassFailed
	ch <- c.lastPassRemoved
	ch <- c.walSegments
	ch <- c.walBytes
	// The exporter descriptors are deliberately absent: they are only collected when an
	// exporter exists, and an unchecked collector is how Prometheus permits that.
}

// Collect reads the durable facts. A number it cannot read is *omitted*, never guessed:
// an absent series shows up as a gap in a graph and a firing "no data" alert, while a
// fabricated zero reads as healthy — the one failure mode a durability metric must not
// have.
func (c *durabilityCollector) Collect(ch chan<- prometheus.Metric) {
	s := c.srv
	gauge := func(desc *prometheus.Desc, v float64) {
		ch <- prometheus.MustNewConstMetric(desc, prometheus.GaugeValue, v)
	}

	applied, appliedErr := s.store.LastAppliedPosition()
	if appliedErr == nil {
		gauge(c.appliedPosition, float64(applied))
	}

	published := s.publishedCheckpoints()
	gauge(c.checkpoints, float64(len(published)))
	// "Newest that still verifies" rather than "newest": an unverifiable checkpoint
	// licenses no compaction and would not carry a restore, so counting it here would
	// report a recovery shortcut that does not exist.
	var newest *checkpointInfo
	for i := range published {
		if published[i].Verified {
			newest = &published[i]
		}
	}
	if newest != nil {
		gauge(c.checkpointPos, float64(newest.Position))
		if newest.CreatedAt > 0 {
			gauge(c.checkpointAge, float64(s.now()-newest.CreatedAt)/float64(nanosPerSecond))
		}
	} else {
		gauge(c.checkpointPos, 0)
	}

	s.lastPassMu.Lock()
	pass := s.lastPass
	s.lastPassMu.Unlock()
	if pass != nil {
		gauge(c.lastPassAt, float64(pass.At)/float64(nanosPerSecond))
		failed := 0.0
		if pass.CheckpointError != "" || pass.CompactionError != "" {
			failed = 1
		}
		gauge(c.lastPassFailed, failed)
		gauge(c.lastPassRemoved, float64(pass.SegmentsRemoved))
	}

	segments, bytes := s.walFootprint()
	gauge(c.walSegments, float64(segments))
	gauge(c.walBytes, float64(bytes))

	// Lag is only meaningful with an exporter. A zero on a server that exports nothing
	// would read exactly like a caught-up one.
	if s.exporter != nil && appliedErr == nil {
		hwm := s.exporter.HighWaterMark()
		gauge(c.exporterPosition, float64(hwm))
		lag := uint64(0)
		if applied > hwm {
			lag = applied - hwm
		}
		gauge(c.exporterLag, float64(lag))
	}
}

// nanosPerSecond converts the engine's unix-nanosecond timestamps to the seconds
// Prometheus conventions use.
const nanosPerSecond = 1_000_000_000

// buildMetrics assembles the registry this server exports: the build info and the
// durability collector. It runs at construction, so a duplicate or inconsistent
// registration fails the boot rather than a scrape.
func (s *Server) buildMetrics() error {
	reg := metrics.NewRegistry()
	b := Build()
	if err := reg.Register(metrics.BuildInfo(b.Version, b.Revision), newDurabilityCollector(s)); err != nil {
		return err
	}
	s.metrics = reg
	return nil
}

// handleMetrics serves the Prometheus exposition. It is unauthenticated, like /healthz:
// that is what a scrape expects, and the cardinality rule (ADR-0142) means the body
// carries only aggregates — no instance data, no variable payloads, no identifiers.
// Put a reverse proxy in front of anything exposed beyond the host.
func (s *Server) handleMetrics(w http.ResponseWriter, r *http.Request) {
	s.metrics.Handler().ServeHTTP(w, r)
}
