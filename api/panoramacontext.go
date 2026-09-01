package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"sync"
	"time"

	"github.com/pblumer/atlas/api/panorama"
	"github.com/pblumer/atlas/opensearch"
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
//     about a connector or a job type at all.
//   - Metrics (ADR-0142) carry no per-element labels by deliberate design: that
//     record forbids labelling by process id, instance key, or any other value the
//     data can invent, because one such label turns a metric into unboundedly many
//     series. A metrics store can therefore answer about a *node* and never about
//     one process. Its adapter arrives with P5b-ii; until then it reports
//     not-configured, which is true of this build.
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

// contextUnidentifiableReason says why the event log cannot name a thing of this
// kind. Each sentence names the actual obstacle rather than a general apology,
// because "the log does not record this" and "the log records it in a form nothing
// outside this process can read" are different facts about the same store.
func contextUnidentifiableReason(key string) string {
	switch key {
	case panorama.KeyConnectorID, panorama.KeyJobType:
		return "The event log stores a job's type as an interned index — a number that " +
			"means nothing outside the process that wrote it — so it cannot be asked " +
			"about a connector or a job type."
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
		s.metricContext(target),
	}
}

// metricContext is the metrics store's answer, which in this build is always the
// same one: no adapter is wired. It is reported rather than omitted so the panel
// says which stores exist and which were asked, and so wiring one later changes an
// answer rather than adding a row that was never there.
func (s *Server) metricContext(target contextTarget) panorama.ContextResult {
	result := panorama.ContextResult{
		Source: panorama.ContextSourceMetrics,
		Key:    target.query.Key, Value: target.query.Value,
		State: panorama.ContextNotConfigured,
		Reason: "No metrics store is wired to this server, so nothing was asked. " +
			"Metrics answer about a node rather than about one process.",
	}
	// Even with a store wired, most kinds could not be asked. Saying so now keeps the
	// distinction visible before the adapter exists, rather than turning it into a
	// surprise when it does.
	switch target.query.Key {
	case panorama.KeyRuntimeID, panorama.KeyDeploymentTargetID:
	default:
		result.State = panorama.ContextUnidentifiable
		result.Reason = "Atlas's metrics carry no per-element labels by design, so a " +
			"metrics store can answer about a node and never about one process, " +
			"application, connector or release."
	}
	return result
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
