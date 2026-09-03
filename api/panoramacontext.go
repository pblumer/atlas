package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/pblumer/atlas/api/panorama"
	"github.com/pblumer/atlas/opensearch"
	"github.com/pblumer/atlas/promquery"
)

// Panorama's historical context adapters (ADR-0189 P5b).
//
// The live projection says what an element is doing now; the drift journal says
// what changed while somebody was watching. Neither answers *has it been like
// this*, because both are bounded by this process. The stores that can answer it
// are already fed by Atlas and owned by somebody else, so this queries them and
// keeps nothing.
//
// What each store can be asked is decided by what it can *identify*, and that is
// not a matter of effort:
//
//   - The exported event log (ADR-0114) carries each record's process definition
//     key, so it can answer about a process and about the application whose
//     processes those are. It carries a job's type only as an interned index — a
//     number meaningless without this process's intern table — so it cannot answer
//     about a worker or a job type at all.
//   - Metrics (ADR-0142) carry no per-element labels by deliberate design: that
//     record forbids labelling by process id, instance key, or any other value the
//     data can invent, because one such label turns a metric into unboundedly many
//     series. A metrics store can therefore answer about a *node* and never about
//     one process — and it identifies that node the only way it knows one, by the
//     scrape target it came from. Atlas derives that from a deployment target's base
//     URL; for the server itself it cannot derive it at all, because how this
//     process appears in somebody's Prometheus is their scrape configuration. That
//     one is configured, and left unset the local runtime is honestly
//     unidentifiable rather than silently matched to the wrong series.
//
// Each of those is reported as its own state rather than as an absence of data,
// because they send an operator to entirely different places.

const (
	// contextQueryTimeout bounds one store's answer. It is the same order as the
	// peer-descriptor read (ADR-0189 §6): long enough for a cluster doing real work,
	// short enough that a panel does not hang on somebody else's outage.
	contextQueryTimeout = 8 * time.Second
	// maxContextConcurrency bounds how many of an element's values are asked about at
	// once, so one page cannot open a connection per binding against a shared cluster.
	maxContextConcurrency = 4
	// maxContextDefinitionKeys bounds how many definition keys one query names. A
	// process id accumulates one key per deployed version, and an application rolls
	// up every process it owns; past this the terms clause is doing the cluster's
	// query planning no favours, and the answer is reported as the floor it is.
	maxContextDefinitionKeys = 200
)

// contextTarget is one query after the run-loop phase has resolved it: either the
// definition keys the log store can be asked about, or the reason it cannot be.
type contextTarget struct {
	query panorama.ContextQuery
	// keys are the process definition keys this value resolves to, empty when the
	// log store cannot identify it.
	keys []uint64
	// state and reason are set when phase one already knows the answer — nothing on
	// this server names the value, or the store cannot identify this kind at all.
	state  string
	reason string
	// truncated reports that more definition keys matched than the query names, so
	// the counts it returns are a floor rather than a total.
	truncated bool
	// instance is how the metrics store labels the node this value names, and
	// metricReason is why there is none. Exactly one of the two is set for a value a
	// metrics store could speak about at all.
	instance     string
	metricReason string
}

// collectContext answers one element's context queries, in the same two phases the
// observation projection uses.
//
// Phase one runs on the run loop: it turns bound ids into definition keys, under
// the same sharing scope every other Panorama read honours (ADR-0071) — a process
// this caller may not see contributes no keys, so the log store is never asked a
// question that would leak one. Phase two runs off the loop, because a query
// against somebody else's cluster must not hold the single writer (I3).
func (s *Server) collectContext(r *http.Request, queries []panorama.ContextQuery) ([]panorama.ContextResult, error) {
	var (
		targets []contextTarget
		err     error
		ran     bool
	)
	s.do(func() { ran = true; targets, err = s.resolveContextTargets(r, queries) })
	if !ran {
		return nil, panorama.ErrShuttingDown
	}
	if err != nil {
		return nil, err
	}
	return s.askContextStores(r.Context(), targets), nil
}

// resolveContextTargets is phase one. Run-loop goroutine only.
func (s *Server) resolveContextTargets(r *http.Request, queries []panorama.ContextQuery) ([]contextTarget, error) {
	projs, err := s.projectsByID()
	if err != nil {
		return nil, err
	}
	targets := make([]contextTarget, 0, len(queries))
	for _, query := range queries {
		targets = append(targets, s.resolveContextTarget(r, projs, query))
	}
	return targets, nil
}

// resolveContextTarget turns one bound value into the definition keys the log store
// can be asked about, or into the reason it cannot be.
//
// Run-loop goroutine only.
func (s *Server) resolveContextTarget(r *http.Request, projs map[string]project, query panorama.ContextQuery) contextTarget {
	target := contextTarget{query: query}
	// The metrics side resolves first and independently of the log side: they
	// identify a thing in different ways, and a value one of them cannot name is
	// often one the other can. It is done before the switch rather than after
	// because several of those branches return early, and a metrics row whose
	// reason went missing because the log side answered first is exactly the empty
	// answer this whole surface exists to avoid.
	target.instance, target.metricReason = s.metricInstanceFor(query)

	switch query.Key {
	case panorama.KeyProcessID:
		target.keys, target.truncated = s.definitionKeys(r, projs, func(d *deployment) bool {
			return d.ProcessID == query.Value
		})
		if len(target.keys) == 0 {
			target.state = panorama.ContextUnidentifiable
			target.reason = "Nothing deployed here carries this process id, so there is no " +
				"definition key to ask the event log about."
		}
	case panorama.KeyApplicationID:
		if _, ok := projs[query.Value]; !ok {
			target.state = panorama.ContextUnidentifiable
			target.reason = "No application with this id is present here, so there are no " +
				"processes to ask the event log about."
			return target
		}
		target.keys, target.truncated = s.definitionKeys(r, projs, func(d *deployment) bool {
			return d.ProjectID == query.Value
		})
		if len(target.keys) == 0 {
			target.state = panorama.ContextEmpty
			// The application exists and this caller may see it; it simply has nothing
			// deployed. That is a statement about the architecture, not about the lookup,
			// which is why it is empty rather than unidentifiable.
			target.reason = "This application has nothing deployed here, so the event log " +
				"holds no process history for it."
		}
	default:
		target.state = panorama.ContextUnidentifiable
		target.reason = contextUnidentifiableReason(query.Key)
	}
	return target
}

// metricInstanceFor resolves how the metrics store labels the node a bound value
// names, or the reason there is none.
//
// A metrics store identifies a node by its scrape target, so this answers only for
// the two kinds that name a node — and for those, only when Atlas actually knows an
// address. Guessing one would answer a question about somebody else's process while
// looking exactly like an answer about this one.
//
// Run-loop goroutine only.
func (s *Server) metricInstanceFor(query panorama.ContextQuery) (instance, reason string) {
	switch query.Key {
	case panorama.KeyDeploymentTargetID:
		targets, err := s.targets.LoadAll()
		if err != nil {
			return "", "The deployment targets could not be read, so the address the " +
				"metrics store would know this node by is unknown here."
		}
		for _, t := range targets {
			if t.ID != query.Value {
				continue
			}
			if host := hostOf(t.BaseURL); host != "" {
				return host, ""
			}
			return "", "This deployment target's base URL names no host, so there is " +
				"no scrape address to match on."
		}
		return "", "No deployment target with this id is configured here, so there is " +
			"no address the metrics store would know it by."
	case panorama.KeyRuntimeID:
		// This server's own runtime. Atlas cannot derive how it is scraped — that is
		// the operator's configuration, not Atlas's — so it is configured, and left
		// unset this says so rather than matching whatever series is nearest.
		node, err := s.nodeIdentity()
		if err == nil && node.ID == query.Value {
			if instance := strings.TrimSpace(s.metricsQueryCfg.Instance); instance != "" {
				return instance, ""
			}
			return "", "This is the server answering, and it does not know how it " +
				"appears in the metrics store. Set --metrics-instance to the scrape " +
				"target it is known by there."
		}
		// Another node. It is reachable only through the deployment target that
		// resolved it, which is where its address is.
		if host := s.hostOfRuntime(query.Value); host != "" {
			return host, ""
		}
		return "", "No deployment target here has reported this runtime, so Atlas " +
			"knows no address the metrics store would identify it by."
	}
	return "", "Atlas's metrics carry no per-element labels by design, so a metrics " +
		"store can answer about a node and never about one process, application, " +
		"worker or release."
}

// hostOfRuntime finds the address of a peer that reported this runtime id, from the
// descriptors the observation projection has already fetched (ADR-0189 P4c).
//
// It reads only what a previous observation cached: this must not become a reason
// to contact peers, because the context route is scoped to one element and asking
// the whole landscape to resolve one label would be the fan-out it exists to avoid.
//
// Run-loop goroutine only.
func (s *Server) hostOfRuntime(runtimeID string) string {
	targets, err := s.targets.LoadAll()
	if err != nil {
		return ""
	}
	for _, t := range targets {
		observed, ok := s.remoteNodes.get(t.ID)
		if !ok || observed.descriptor.ID != runtimeID {
			continue
		}
		if host := hostOf(t.BaseURL); host != "" {
			return host
		}
	}
	return ""
}

// hostOf is the host a base URL names, without its port. The port is dropped
// because a scrape target's port is the metrics listener's, which is rarely the
// API's — matching on it would find nothing and look like an empty store.
func hostOf(baseURL string) string {
	parsed, err := url.Parse(strings.TrimSpace(baseURL))
	if err != nil {
		return ""
	}
	return parsed.Hostname()
}

// contextUnidentifiableReason says why the event log cannot name a thing of this
// kind. Each sentence names the actual obstacle rather than a general apology,
// because "the log does not record this" and "the log records it in a form nothing
// outside this process can read" are different facts about the same store.
func contextUnidentifiableReason(key string) string {
	switch key {
	case panorama.KeyConnectorID, panorama.KeyJobType:
		return "The event log stores a job's type as an interned index — a number that " +
			"means nothing outside the process that wrote it — so it cannot be asked " +
			"about a worker or a job type."
	case panorama.KeyRuntimeID, panorama.KeyDeploymentTargetID:
		return "The event log is this node's own history and carries no node identity, " +
			"so it cannot be asked about a runtime or a deployment target. A metrics " +
			"store is what answers about a node."
	case panorama.KeyReleaseID:
		return "A release is a record of what shipped, not something the engine emits " +
			"events for, so the event log has nothing to say about one."
	}
	return "The event log records nothing that identifies a resource of this kind."
}

// definitionKeys collects the deployment keys matching want that this caller may
// see, in ascending key order and bounded. Run-loop goroutine only.
func (s *Server) definitionKeys(r *http.Request, projs map[string]project, want func(*deployment) bool) ([]uint64, bool) {
	var keys []uint64
	for _, key := range s.order {
		d := s.deployments[key]
		if d == nil || !want(d) {
			continue
		}
		// The same scope check every other Panorama read makes. A definition this
		// caller may not see contributes no key, so the query cannot become a way of
		// counting somebody else's traffic.
		if !s.canViewArtifact(r, d.ProjectID, d.DeployedBy, projs) {
			continue
		}
		keys = append(keys, d.Key)
	}
	// Sorted, so the query body for one unchanged landscape is byte-identical between
	// two requests — which is what lets a cluster cache it.
	sort.Slice(keys, func(a, b int) bool { return keys[a] < keys[b] })
	if len(keys) > maxContextDefinitionKeys {
		return keys[:maxContextDefinitionKeys], true
	}
	return keys, false
}

// askContextStores is phase two: every store that can identify a value is asked
// about it, off the run loop, with bounded concurrency.
func (s *Server) askContextStores(ctx context.Context, targets []contextTarget) []panorama.ContextResult {
	results := make([][]panorama.ContextResult, len(targets))
	var wg sync.WaitGroup
	slots := make(chan struct{}, maxContextConcurrency)
	for i, target := range targets {
		wg.Add(1)
		go func(i int, target contextTarget) {
			defer wg.Done()
			slots <- struct{}{}
			defer func() { <-slots }()
			results[i] = s.contextResultsFor(ctx, target)
		}(i, target)
	}
	wg.Wait()

	var all []panorama.ContextResult
	for _, batch := range results {
		all = append(all, batch...)
	}
	return all
}

// contextResultsFor produces every source's answer about one value. Both sources
// answer for every value — a source that cannot help says so in the same shape as
// one that can, because a row that was simply left out is indistinguishable from a
// source nobody thought to ask.
func (s *Server) contextResultsFor(ctx context.Context, target contextTarget) []panorama.ContextResult {
	return []panorama.ContextResult{
		s.eventContext(ctx, target),
		s.metricContext(ctx, target),
	}
}

// metricMeasures are what one node query returns, in the order the panel shows
// them. Every expression aggregates, so each reduces to one series — a per-series
// breakdown would be the per-element labels ADR-0142 forbids, arriving through the
// query instead of through the metric.
//
// The counters are asked with increase() rather than rate() so a bucket's value is
// a count over that bucket, exactly like the event log's buckets, and the window
// total is their sum. The gauge is asked with max_over_time, and its total is the
// peak rather than a sum — which is why Measure carries a total at all.
var metricMeasures = []struct {
	name, label, expr string
	gauge             bool
}{
	{name: "commandsProcessed", label: "Commands processed",
		expr: `sum(increase(atlas_commands_processed_total{%s}[%s]))`},
	{name: "jobsFailed", label: "Jobs failed",
		expr: `sum(increase(atlas_jobs_failed_total{%s}[%s]))`},
	{name: "jobsWaiting", label: "Jobs waiting (peak)",
		expr: `max(max_over_time(atlas_open_jobs{%s}[%s]))`, gauge: true},
}

// metricContext is the metrics store's answer about one bound value.
//
// It answers about a node and never about one process, and that is ADR-0142's rule
// rather than this adapter's limitation: a label whose values the data can invent
// turns one metric into unboundedly many series, so Atlas emits none. A value that
// names no node is reported unidentifiable, which sends a reader to the event log
// instead of to a series that must not exist.
func (s *Server) metricContext(ctx context.Context, target contextTarget) panorama.ContextResult {
	result := panorama.ContextResult{
		Source: panorama.ContextSourceMetrics,
		Key:    target.query.Key, Value: target.query.Value,
	}
	if !s.metricsQueryCfg.Enabled() {
		result.State = panorama.ContextNotConfigured
		result.Reason = "No metrics store is wired to this server, so nothing was asked. " +
			"Point --metrics-url at one to give this question somewhere to look."
		return result
	}
	if target.instance == "" {
		// Phase one already knows there is no node to ask about, and why.
		result.State, result.Reason = panorama.ContextUnidentifiable, target.metricReason
		return result
	}

	window := target.query.Window
	step := panorama.BucketSeconds(window)
	matcher := `instance=~"^` + promquery.EscapeLabelValue(regexp.QuoteMeta(target.instance)) + `(:[0-9]+)?$"`
	rang := strconv.FormatInt(step, 10) + "s"

	ctx, cancel := context.WithTimeout(ctx, contextQueryTimeout)
	defer cancel()

	var (
		measures []panorama.Measure
		any      bool
	)
	for _, want := range metricMeasures {
		samples, err := s.metricQuerier().QueryRange(ctx,
			fmt.Sprintf(want.expr, matcher, rang), window.From, window.To, step)
		switch {
		case errors.Is(err, promquery.ErrQueryRefused):
			result.State = panorama.ContextRefused
			result.Reason = "The metrics store declined the query. Its credentials here " +
				"may not carry read access."
			return result
		case err != nil:
			result.State = panorama.ContextUnreachable
			result.Reason = "The metrics store could not be asked, so nothing is known " +
				"about this window — which is not the same as nothing having happened."
			return result
		}
		measure := panorama.Measure{
			Name: want.name, Label: want.label, Buckets: []panorama.Bucket{},
		}
		for _, sample := range samples {
			if sample.At < window.From || sample.At > window.To {
				continue
			}
			measure.Buckets = append(measure.Buckets,
				panorama.Bucket{At: sample.At, Value: sample.Value})
			// A counter's window total is the sum of its buckets; a gauge's is its
			// peak, because adding two readings of a queue depth measures nothing.
			switch {
			case want.gauge:
				measure.Total = max(measure.Total, sample.Value)
			default:
				measure.Total += sample.Value
			}
		}
		if len(measure.Buckets) > 0 {
			any = true
		}
		measures = append(measures, measure)
	}

	result.Measures = measures
	result.Detail = map[string]string{"instance": target.instance}
	switch {
	case !any:
		result.State = panorama.ContextEmpty
		result.Reason = "The metrics store holds no Atlas series for this node in the " +
			"window asked about. It may not be scraping it, or may have aged the " +
			"window out."
	default:
		result.State = panorama.ContextAvailable
	}
	return result
}

// metricQuerier is the client the node queries go through, built per call because
// it is stateless. A test replaces it wholesale.
func (s *Server) metricQuerier() promquery.Querier {
	if s.metricsQuery != nil {
		return s.metricsQuery
	}
	return promquery.NewHTTPClient(s.metricsQueryCfg)
}

// eventContext is the exported event log's answer about one value.
func (s *Server) eventContext(ctx context.Context, target contextTarget) panorama.ContextResult {
	result := panorama.ContextResult{
		Source: panorama.ContextSourceEvents,
		Key:    target.query.Key, Value: target.query.Value,
	}
	if !s.osExportCfg.Enabled() {
		result.State = panorama.ContextNotConfigured
		result.Reason = "This server exports no event log, so there is no history to read. " +
			"Enable the OpenSearch exporter to give this question somewhere to look."
		return result
	}
	if target.state != "" {
		// Phase one already knows the answer, and it is about this server rather than
		// about the store — asking the cluster would only turn a definite finding into
		// an empty one.
		result.State, result.Reason = target.state, target.reason
		return result
	}

	query, err := eventContextQuery(target)
	if err != nil {
		result.State, result.Reason = panorama.ContextUnreachable, "The query could not be built."
		return result
	}
	ctx, cancel := context.WithTimeout(ctx, contextQueryTimeout)
	defer cancel()

	body, err := s.eventSearcher().Search(ctx, s.osExportCfg.Index, query)
	switch {
	case errors.Is(err, opensearch.ErrSearchRefused):
		result.State = panorama.ContextRefused
		result.Reason = "The event log store declined the query. Its credentials here may " +
			"not carry read access to the index Atlas writes."
		return result
	case err != nil:
		result.State = panorama.ContextUnreachable
		result.Reason = "The event log store could not be reached, so nothing is known " +
			"about this window — which is not the same as nothing having happened."
		return result
	}

	measures, total, err := parseEventContext(body, target.query.Window)
	if err != nil {
		result.State = panorama.ContextUnreachable
		result.Reason = "The event log store answered in a shape this server could not read."
		return result
	}
	result.Measures = measures
	result.Detail = map[string]string{
		"definitions": strconv.Itoa(len(target.keys)),
		// Named because its absence is a real gap and a silent one: an incident is
		// recorded against an instance, and the exported record does not carry the
		// definition it belonged to, so no single query can attribute one here.
		"notCounted": "incidents, which the exported record does not attribute to a definition",
	}
	if target.truncated {
		result.Detail["definitionsTruncated"] = "true"
	}
	switch {
	case total == 0:
		result.State = panorama.ContextEmpty
		result.Reason = "The event log holds nothing for this in the window asked about."
	default:
		result.State = panorama.ContextAvailable
	}
	return result
}

// eventSearcher is the client the event-log queries go through. It is built per
// call rather than held on the server because it is stateless and the exporter's
// own client is driven from a different goroutine; sharing one would couple a read
// path to a write path for no gain.
func (s *Server) eventSearcher() opensearch.Searcher {
	if s.eventSearch != nil {
		return s.eventSearch
	}
	return opensearch.NewHTTPClient(s.osExportCfg)
}

// eventMeasures are what one query returns, in the order the panel shows them. The
// intent names are the engine's own (model.Intent.String), which is what the
// exporter wrote into the document.
var eventMeasures = []struct{ name, label, intent string }{
	{"instancesStarted", "Instances started", "Activated"},
	{"instancesCompleted", "Instances completed", "Completed"},
	{"instancesTerminated", "Instances terminated", "Terminated"},
}

// eventContextQuery builds the aggregation. One request answers all three measures:
// a filter per intent, each holding a date histogram over the window, under a
// common filter of this value's definition keys and the window's range.
//
// It asks for no documents. The answer's size is then the aggregation's shape —
// three filters times at most the bucket bound — rather than anything that grows
// with how busy the landscape was.
func eventContextQuery(target contextTarget) ([]byte, error) {
	window := target.query.Window
	aggs := map[string]any{}
	for _, measure := range eventMeasures {
		aggs[measure.name] = map[string]any{
			"filter": map[string]any{"term": map[string]any{"intent.keyword": measure.intent}},
			"aggs": map[string]any{
				"overTime": map[string]any{
					"date_histogram": map[string]any{
						"field":          "timestamp",
						"fixed_interval": strconv.FormatInt(panorama.BucketSeconds(window), 10) + "s",
						"min_doc_count":  0,
						"extended_bounds": map[string]any{
							"min": window.From * 1000, "max": window.To * 1000,
						},
					},
				},
			},
		}
	}
	return json.Marshal(map[string]any{
		"size": 0,
		"query": map[string]any{"bool": map[string]any{"filter": []any{
			map[string]any{"terms": map[string]any{"value.ProcessDefKey": target.keys}},
			map[string]any{"term": map[string]any{"valueType.keyword": "ProcessInstance"}},
			map[string]any{"range": map[string]any{"timestamp": map[string]any{
				"gte":    window.From * 1000,
				"lte":    window.To * 1000,
				"format": "epoch_millis",
			}}},
		}}},
		"aggs": aggs,
	})
}

// eventSearchResponse is the subset of the reply this reads. Everything else the
// cluster returns is ignored rather than round-tripped: a projection that carried
// fields it does not understand would publish somebody else's schema as part of
// this contract.
type eventSearchResponse struct {
	Aggregations map[string]struct {
		DocCount int64 `json:"doc_count"`
		OverTime struct {
			Buckets []struct {
				Key      int64 `json:"key"`
				DocCount int64 `json:"doc_count"`
			} `json:"buckets"`
		} `json:"overTime"`
	} `json:"aggregations"`
}

// parseEventContext turns the reply into measures, and reports how many events
// they cover in total.
//
// A missing aggregation is a measure with no buckets rather than an omitted one:
// the three measures are what this adapter claims to answer, and dropping one
// because a cluster left it out would silently narrow the claim.
func parseEventContext(body []byte, window panorama.ContextWindow) ([]panorama.Measure, int64, error) {
	var parsed eventSearchResponse
	if len(body) > 0 {
		if err := json.Unmarshal(body, &parsed); err != nil {
			return nil, 0, fmt.Errorf("parse event context: %w", err)
		}
	}
	var (
		measures []panorama.Measure
		total    int64
	)
	for _, want := range eventMeasures {
		agg := parsed.Aggregations[want.name]
		measure := panorama.Measure{
			Name: want.name, Label: want.label, Total: float64(agg.DocCount),
			Buckets: []panorama.Bucket{},
		}
		for _, bucket := range agg.OverTime.Buckets {
			// The histogram keys in epoch milliseconds; every other timestamp in this
			// contract is Unix seconds, and mixing the two in one document is how a
			// renderer ends up drawing 1970.
			at := bucket.Key / 1000
			if at < window.From || at > window.To {
				continue
			}
			measure.Buckets = append(measure.Buckets,
				panorama.Bucket{At: at, Value: float64(bucket.DocCount)})
		}
		total += agg.DocCount
		measures = append(measures, measure)
	}
	return measures, total, nil
}
