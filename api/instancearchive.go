package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/pblumer/atlas/model"
	"github.com/pblumer/atlas/opensearch"
)

// Searching the archive (S4 of the fast instance search, ADR-0114, ADR-0241).
//
// The local index (ADR-0241) answers about instances this server still holds. It
// cannot answer about instances history retention has hard-deleted (ADR-0115) — the
// data corpses the exporter was built to preserve before they are removed. Those
// live only in the exported event log, and this file is how they are asked about.
//
// An answer from here is a different kind of answer, and the surface says so: the
// instance does not exist on this server any more. It cannot be opened, cancelled or
// migrated, and what is reported about it is what the log recorded, not what is true
// now. Mixing such a row into the live result list unmarked would be a lie an
// operator has no way to catch.
//
// The queries are built here rather than in the opensearch package, which
// deliberately knows nothing of Atlas's vocabulary: that package moves bytes and
// bounds the response, this one knows what a variable and an instance are.

// archiveScopeQuery asks which instances held a variable, not which events wrote it.
// A value written five times is five documents and one instance, so the question is
// put as a terms aggregation over the scope key: it returns distinct instances, and
// it returns them small enough to stay well inside the response bound the client
// enforces.
func archiveScopeQuery(pred varQuery) ([]byte, error) {
	match := map[string]any{"term": map[string]any{"value.Text.keyword": pred.rawValue}}
	if pred.prefix {
		match = map[string]any{"prefix": map[string]any{"value.Text.keyword": pred.rawValue}}
	}
	return json.Marshal(map[string]any{
		"size": 0,
		"query": map[string]any{"bool": map[string]any{"filter": []any{
			map[string]any{"term": map[string]any{"valueType.keyword": "Variable"}},
			map[string]any{"term": map[string]any{"value.Name.keyword": pred.rawName}},
			match,
		}}},
		"aggs": map[string]any{"instances": map[string]any{"terms": map[string]any{
			"field": "value.ScopeKey",
			"size":  maxInstanceSearchResults,
		}}},
	})
}

// archiveScopeResponse is the subset of the reply this reads. The bucket key is held
// as a json.Number rather than a uint64 or a float64: an instance key needs all 64
// bits, JSON numbers decode through float64 when read into an any, and key_as_string
// — the obvious alternative — is only emitted when the aggregation carries an
// explicit format, which a plain terms aggregation over a long does not. A number
// read from its literal text is exact and depends on nothing optional.
type archiveScopeResponse struct {
	Aggregations struct {
		Instances struct {
			Buckets []struct {
				Key json.Number `json:"key"`
			} `json:"buckets"`
		} `json:"instances"`
	} `json:"aggregations"`
}

// parseArchiveScopes reads the instance keys out of the aggregation. A nil body is
// an index that does not exist — nothing has been exported yet, or it was rotated
// away — which is an empty answer rather than a failure.
func parseArchiveScopes(body []byte) ([]uint64, error) {
	if len(body) == 0 {
		return nil, nil
	}
	var resp archiveScopeResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, fmt.Errorf("archive: read scope aggregation: %w", err)
	}
	buckets := resp.Aggregations.Instances.Buckets
	out := make([]uint64, 0, len(buckets))
	for _, b := range buckets {
		key, err := strconv.ParseUint(b.Key.String(), 10, 64)
		if err != nil {
			// A bucket key that is not an instance key is skipped rather than reported:
			// the aggregation is over a field somebody else's tooling may also write.
			continue
		}
		out = append(out, key)
	}
	return out, nil
}

// maxArchiveHits bounds the documents step two asks for. An instance writes a handful
// of ProcessInstance events over its life, so this is the instance cap with room for
// each instance's own history — still small enough that the response stays inside the
// bound the client enforces.
const maxArchiveHits = maxInstanceSearchResults * 8

// archiveInstanceQuery asks what the instances found in step one were. It is sorted
// newest-first and reads an explicit field list rather than whole documents: an
// exported document carries the record's value verbatim, which has no bound.
func archiveInstanceQuery(keys []uint64, defKey uint64) ([]byte, error) {
	clauses := []any{
		map[string]any{"term": map[string]any{"valueType.keyword": "ProcessInstance"}},
		map[string]any{"terms": map[string]any{"key": keys}},
	}
	if defKey != 0 {
		clauses = append(clauses, map[string]any{"term": map[string]any{"value.ProcessDefKey": defKey}})
	}
	return json.Marshal(map[string]any{
		"size": maxArchiveHits,
		"_source": []string{
			"key", "position", "value.ProcessDefKey", "value.State",
			"value.CreatedAt", "value.CompletedAt", "value.CorrelationKey",
		},
		"sort":  []any{map[string]any{"position": "desc"}},
		"query": map[string]any{"bool": map[string]any{"filter": clauses}},
	})
}

// archiveInstance is one instance as the log remembers it. It is deliberately not an
// instanceResp: this instance does not exist on this server, so it carries no element
// instance count and nothing that would invite an action against it.
type archiveInstance struct {
	key uint64
	// pos is the log position of the event this row was folded from, so a later event
	// can be recognised as the truer one without trusting the cluster's sort.
	pos            uint64
	processDefKey  uint64
	state          model.ProcessInstanceState
	createdAt      int64
	completedAt    int64
	correlationKey string
}

// archiveHits is the subset of the hit list this reads. As with the bucket keys, the
// record key is held as a json.Number so all 64 bits survive.
type archiveHits struct {
	Hits struct {
		Hits []struct {
			Source struct {
				Key      json.Number `json:"key"`
				Position uint64      `json:"position"`
				Value    struct {
					ProcessDefKey  uint64                     `json:"ProcessDefKey"`
					State          model.ProcessInstanceState `json:"State"`
					CreatedAt      int64                      `json:"CreatedAt"`
					CompletedAt    int64                      `json:"CompletedAt"`
					CorrelationKey string                     `json:"CorrelationKey"`
				} `json:"value"`
			} `json:"_source"`
		} `json:"hits"`
	} `json:"hits"`
}

// parseArchiveInstances folds the events back into instances: one row each, carrying
// what the instance's last recorded event said. An instance writes several events
// over its life and the operator asked about the instance, not about its narration.
func parseArchiveInstances(body []byte) ([]archiveInstance, error) {
	if len(body) == 0 {
		return nil, nil
	}
	var resp archiveHits
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, fmt.Errorf("archive: read instance hits: %w", err)
	}
	seen := map[uint64]int{}
	out := []archiveInstance{}
	for _, h := range resp.Hits.Hits {
		key, err := strconv.ParseUint(h.Source.Key.String(), 10, 64)
		if err != nil || key == 0 {
			continue
		}
		src := h.Source.Value
		row := archiveInstance{
			key: key, pos: h.Source.Position,
			processDefKey: src.ProcessDefKey, state: src.State,
			createdAt: src.CreatedAt, completedAt: src.CompletedAt,
			correlationKey: src.CorrelationKey,
		}
		at, ok := seen[key]
		if !ok {
			seen[key] = len(out)
			out = append(out, row)
			continue
		}
		// The query is sorted newest-first, but a cluster is not obliged to honour a
		// sort this code did not verify, and a later event is the truer one. Comparing
		// positions makes the fold right whatever order the hits arrive in.
		if row.pos > out[at].pos {
			out[at] = row
		}
	}
	return out, nil
}

// The outcomes a lookup against the archive can have. They are separate because they
// send an operator to different places: to the configuration, to the credentials, to
// the network, or nowhere at all because the answer really is that nothing matched.
const (
	archiveAvailable     = "available"
	archiveEmpty         = "empty"
	archiveNotConfigured = "notConfigured"
	archiveRefused       = "refused"
	archiveUnreachable   = "unreachable"
)

// archiveTimeout bounds how long somebody else's cluster may hold up a search. An
// operator waiting on the live index should not wait on the archive as well.
const archiveTimeout = 5 * time.Second

// archiveResult is what the archive had to say, including the case where it had
// nothing to say and why.
type archiveResult struct {
	Instances []archiveInstance
	State     string
	Reason    string
}

// searchArchive asks the exported event log which instances held a value. It is the
// answer for instances this server no longer has: history retention (ADR-0115) hard-
// deletes finished instances once the exporter has them, and from that moment the log
// is the only place they exist.
//
// It runs as two questions rather than one because the export is a stream of events,
// not a table of instances: first which instances a matching variable belonged to,
// then what those instances were. The first is an aggregation, so it is small and it
// is what bounds the second.
func (s *Server) searchArchive(ctx context.Context, defKey uint64, pred varQuery) archiveResult {
	if !s.osExportCfg.Enabled() {
		return archiveResult{State: archiveNotConfigured, Reason: "This server exports no event log, " +
			"so an instance it no longer holds is not recorded anywhere it can look. " +
			"Enable the OpenSearch exporter to give this question somewhere to look."}
	}
	ctx, cancel := context.WithTimeout(ctx, archiveTimeout)
	defer cancel()

	scopeQuery, err := archiveScopeQuery(pred)
	if err != nil {
		return archiveResult{State: archiveUnreachable, Reason: "The query could not be built."}
	}
	body, err := s.eventSearcher().Search(ctx, s.osExportCfg.Index, scopeQuery)
	if res, bad := archiveFailure(err); bad {
		return res
	}
	keys, err := parseArchiveScopes(body)
	if err != nil {
		return archiveResult{State: archiveUnreachable, Reason: unreadableArchive}
	}
	if len(keys) == 0 {
		// Asking the second question with no keys would drop the only clause that
		// bounds it and answer about every instance the filter still matches.
		return archiveResult{State: archiveEmpty, Reason: "The event log holds no instance with this value."}
	}

	instQuery, err := archiveInstanceQuery(keys, defKey)
	if err != nil {
		return archiveResult{State: archiveUnreachable, Reason: "The query could not be built."}
	}
	body, err = s.eventSearcher().Search(ctx, s.osExportCfg.Index, instQuery)
	if res, bad := archiveFailure(err); bad {
		return res
	}
	rows, err := parseArchiveInstances(body)
	if err != nil {
		return archiveResult{State: archiveUnreachable, Reason: unreadableArchive}
	}
	if len(rows) == 0 {
		return archiveResult{State: archiveEmpty, Reason: "The event log holds no instance with this value."}
	}
	return archiveResult{Instances: rows, State: archiveAvailable}
}

// unreadableArchive is one sentence rather than three identical ones inline.
const unreadableArchive = "The event log store answered in a shape this server could not read."

// archiveFailure turns a search error into the outcome it deserves. Refused and
// unreachable stay apart: one is about this server's credentials, the other about
// somebody else's network.
func archiveFailure(err error) (archiveResult, bool) {
	switch {
	case errors.Is(err, opensearch.ErrSearchRefused):
		return archiveResult{State: archiveRefused, Reason: "The event log store declined the query. " +
			"Its credentials here may not carry read access to the index Atlas writes."}, true
	case err != nil:
		return archiveResult{State: archiveUnreachable, Reason: "The event log store could not be reached, " +
			"so nothing is known about instances this server no longer holds — which is not " +
			"the same as there having been none."}, true
	}
	return archiveResult{}, false
}

// archiveRows projects what the log remembers into the rows the search returns. It
// goes through [newInstanceRow] so an archived row carries its definition's labels by
// the same rule a live one does — including the rule for a definition that is gone
// too, which is common here: an instance old enough to have been purged may well
// outlive the model it ran on.
//
// Two things it does not do. It sets no element instance count, because the archive
// knows of no live tokens and a zero that means "none recorded" must not be dressed
// up as a measurement. And it marks every row archived, because the row describes an
// instance that does not exist on this server: an operator who cannot see that would
// try to open, cancel or migrate something that is not there.
func archiveRows(found []archiveInstance, defs defIndex) []instanceResp {
	out := make([]instanceResp, 0, len(found))
	for _, a := range found {
		row := newInstanceRow(a.key, &model.ProcessInstanceValue{
			ProcessDefKey:  a.processDefKey,
			State:          a.state,
			CreatedAt:      a.createdAt,
			CompletedAt:    a.completedAt,
			CorrelationKey: a.correlationKey,
		}, defs)
		row.Archived = true
		out = append(out, row)
	}
	return out
}

// shouldAskArchive decides whether the archive is worth a round trip. It is a
// fallback rather than a second opinion: an instance this server still holds is
// answered from the live index, and only a question that came back empty is worth
// somebody else's cluster and the wait that comes with it.
//
// A free-text query is not asked here. The archive is reached through a term filter
// on a variable's name and value, so a needle with no name has nothing to filter on —
// answering it would mean scanning the export, which is the cost this whole staging
// exists to avoid.
func shouldAskArchive(found []instanceResp, pred varQuery) bool {
	return len(found) == 0 && pred.structured
}
