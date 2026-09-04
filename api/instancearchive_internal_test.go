package api

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/pblumer/atlas/model"
	"github.com/pblumer/atlas/opensearch"
)

// decodeQuery reads a built query body back as a generic tree, so a test asserts on
// the shape the cluster will see rather than on Go's map ordering.
func decodeQuery(t *testing.T, b []byte) map[string]any {
	t.Helper()
	var got map[string]any
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatalf("query is not JSON: %v (%s)", err, b)
	}
	return got
}

// filters pulls the bool/filter clauses out of a query so a test can look for one
// without walking the tree by hand at every assertion.
func filters(t *testing.T, q map[string]any) []any {
	t.Helper()
	query, ok := q["query"].(map[string]any)
	if !ok {
		t.Fatalf("query has no query clause: %v", q)
	}
	b, ok := query["bool"].(map[string]any)
	if !ok {
		t.Fatalf("query is not a bool: %v", query)
	}
	f, ok := b["filter"].([]any)
	if !ok {
		t.Fatalf("bool has no filter list: %v", b)
	}
	return f
}

// hasClause reports whether one of the filters is the given single-key clause with
// the given field and value.
func hasClause(fs []any, kind, field string, value any) bool {
	want, _ := json.Marshal(value)
	for _, f := range fs {
		m, ok := f.(map[string]any)
		if !ok {
			continue
		}
		inner, ok := m[kind].(map[string]any)
		if !ok {
			continue
		}
		got, err := json.Marshal(inner[field])
		if err == nil && string(got) == string(want) {
			return true
		}
	}
	return false
}

// The archive is asked for the instances a variable belonged to, not for the
// variable events themselves: an operator wants the instance back, and a value
// written five times is five documents but one instance. A terms aggregation over
// the scope key answers exactly that, and answers it small — which is what keeps
// the response inside the bound the client enforces.
func TestArchiveScopeQueryAsksForDistinctInstances(t *testing.T) {
	q, err := archiveScopeQuery(varQuery{rawName: "identityId", pattern: "MT-1998", literal: "MT-1998"})
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	got := decodeQuery(t, q)

	if got["size"] != float64(0) {
		t.Errorf("size = %v, want 0 — no documents, only the aggregation", got["size"])
	}
	fs := filters(t, got)
	if !hasClause(fs, "term", "valueType.keyword", "Variable") {
		t.Errorf("filters %v do not restrict to variable records", fs)
	}
	if !hasClause(fs, "term", "value.Name.keyword", "identityId") {
		t.Errorf("filters %v do not restrict to the name asked for", fs)
	}
	if !hasClause(fs, "term", "value.Text.keyword", "MT-1998") {
		t.Errorf("filters %v do not match the value exactly", fs)
	}
	aggs, ok := got["aggs"].(map[string]any)
	if !ok {
		t.Fatalf("query carries no aggregation: %v", got)
	}
	inst, ok := aggs["instances"].(map[string]any)
	if !ok {
		t.Fatalf("aggregation is not named instances: %v", aggs)
	}
	terms, ok := inst["terms"].(map[string]any)
	if !ok {
		t.Fatalf("instances is not a terms aggregation: %v", inst)
	}
	if terms["field"] != "value.ScopeKey" {
		t.Errorf("terms field = %v, want the scope key", terms["field"])
	}
	if terms["size"] != float64(maxInstanceSearchResults) {
		t.Errorf("terms size = %v, want the search cap %d", terms["size"], maxInstanceSearchResults)
	}
}

// A wildcard is the same question here as it is against the local index. The two must
// not be confusable: a wildcard clause answering a literal query would report
// instances that merely resemble the one asked for, which is exactly what a literal
// search exists to rule out. OpenSearch spells * and ? the same way, so the pattern
// goes over as typed.
func TestArchiveScopeQueryHonoursAPrefix(t *testing.T) {
	q, err := archiveScopeQuery(varQuery{rawName: "identityId", pattern: "MT-*", literal: "MT-", wild: true})
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	fs := filters(t, decodeQuery(t, q))
	if !hasClause(fs, "wildcard", "value.Text.keyword", "MT-*") {
		t.Errorf("filters %v do not match the pattern as a wildcard", fs)
	}
	if hasClause(fs, "term", "value.Text.keyword", "MT-*") {
		t.Errorf("filters %v also match the pattern literally — it must not do both", fs)
	}
}

// The cluster's answer is read for the one thing asked of it: which instances. A
// bucket key arrives as a plain JSON number — a terms aggregation over a long emits
// no key_as_string — and a uint64 read through float64 would round two neighbouring
// instance keys onto the same value. The fixture carries keys past 2^51 so a parser
// that goes through float64 fails here rather than in production.
func TestParseArchiveScopesReadsKeysExactly(t *testing.T) {
	// 9007199254740993 is past float64's exact-integer range for the low bits that
	// distinguish two neighbouring instance keys.
	body := []byte(`{"aggregations":{"instances":{"buckets":[
		{"key":9007199254740993,"doc_count":3},
		{"key":9007199254740995,"doc_count":1}
	]}}}`)
	got, err := parseArchiveScopes(body)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	want := []uint64{9007199254740993, 9007199254740995}
	if len(got) != len(want) {
		t.Fatalf("parsed %d keys, want %d (%v)", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("key[%d] = %d, want %d", i, got[i], want[i])
		}
	}
}

// An index that does not exist yet answers with nothing at all, and the client turns
// that into a nil body. Nothing exported is not a failure to report as one.
func TestParseArchiveScopesTreatsNoIndexAsEmpty(t *testing.T) {
	got, err := parseArchiveScopes(nil)
	if err != nil {
		t.Fatalf("parse of an absent index: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("got %v, want no instances", got)
	}
}

// A body that is not the shape asked for is an error, not an empty answer: the two
// send an operator to different places, and reporting a broken cluster as "nothing
// found" would hide the break behind a plausible result.
func TestParseArchiveScopesRefusesAnUnreadableBody(t *testing.T) {
	if _, err := parseArchiveScopes([]byte(`{"aggregations":`)); err == nil {
		t.Error("truncated body parsed without error")
	}
}

// Step two asks what those instances were. The archive holds every event an instance
// ever wrote, so the query is sorted newest-first and read one row per key: the last
// event is the one that says how the instance ended.
func TestArchiveInstanceQueryIsBoundedAndScoped(t *testing.T) {
	q, err := archiveInstanceQuery([]uint64{7, 9}, 42)
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	got := decodeQuery(t, q)

	if got["size"] != float64(maxArchiveHits) {
		t.Errorf("size = %v, want the hit bound %d", got["size"], maxArchiveHits)
	}
	src, ok := got["_source"].([]any)
	if !ok || len(src) == 0 {
		t.Fatalf("_source = %v, want an explicit field list — a full document is unbounded", got["_source"])
	}
	fs := filters(t, got)
	if !hasClause(fs, "term", "valueType.keyword", "ProcessInstance") {
		t.Errorf("filters %v do not restrict to instance records", fs)
	}
	if !hasClause(fs, "terms", "key", []uint64{7, 9}) {
		t.Errorf("filters %v do not restrict to the instances found in step one", fs)
	}
	if !hasClause(fs, "term", "value.ProcessDefKey", 42) {
		t.Errorf("filters %v do not scope to the definition asked about", fs)
	}
}

// Without a definition the query must not invent one: an unscoped search is a wider
// question, not a question about definition zero.
func TestArchiveInstanceQueryOmitsAnAbsentDefinition(t *testing.T) {
	q, err := archiveInstanceQuery([]uint64{7}, 0)
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	if fs := filters(t, decodeQuery(t, q)); hasClause(fs, "term", "value.ProcessDefKey", 0) {
		t.Errorf("filters %v scope to definition zero, which is no definition", fs)
	}
}

// One instance writes many events. What the operator gets back is one row per
// instance carrying its last recorded state — not four rows narrating its life.
func TestParseArchiveInstancesKeepsTheLastEventPerInstance(t *testing.T) {
	body := []byte(`{"hits":{"hits":[
		{"_source":{"key":7,"position":90,"value":{"ProcessDefKey":42,"State":3,"CreatedAt":100,"CompletedAt":900}}},
		{"_source":{"key":7,"position":10,"value":{"ProcessDefKey":42,"State":1,"CreatedAt":100}}},
		{"_source":{"key":9,"position":20,"value":{"ProcessDefKey":42,"State":1,"CreatedAt":200}}}
	]}}`)
	got, err := parseArchiveInstances(body)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("parsed %d rows from 3 events over 2 instances, want 2 (%+v)", len(got), got)
	}
	byKey := map[uint64]archiveInstance{}
	for _, r := range got {
		byKey[r.key] = r
	}
	seven, ok := byKey[7]
	if !ok {
		t.Fatalf("instance 7 missing from %+v", got)
	}
	if seven.completedAt != 900 {
		t.Errorf("instance 7 completedAt = %d, want 900 from its last event", seven.completedAt)
	}
	if seven.createdAt != 100 {
		t.Errorf("instance 7 createdAt = %d, want 100", seven.createdAt)
	}
	if byKey[9].completedAt != 0 {
		t.Errorf("instance 9 completedAt = %d, want 0 — it never finished", byKey[9].completedAt)
	}
}

// The same exactness the bucket keys need, on the other path: a hit's record key is
// a JSON number too, and must survive with all 64 bits.
func TestParseArchiveInstancesReadsKeysExactly(t *testing.T) {
	body := []byte(`{"hits":{"hits":[
		{"_source":{"key":9007199254740993,"position":1,"value":{"ProcessDefKey":42,"State":1}}}
	]}}`)
	got, err := parseArchiveInstances(body)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(got) != 1 || got[0].key != 9007199254740993 {
		t.Errorf("parsed %+v, want the exact key 9007199254740993", got)
	}
}

func TestParseArchiveInstancesTreatsNoIndexAsEmpty(t *testing.T) {
	got, err := parseArchiveInstances(nil)
	if err != nil {
		t.Fatalf("parse of an absent index: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("got %+v, want no rows", got)
	}
}

// queueSearcher answers a sequence of searches, so a test can drive the two-step
// archive lookup: the scope aggregation first, then the instances it found.
type queueSearcher struct {
	bodies [][]byte
	errs   []error
	asked  [][]byte
}

func (q *queueSearcher) Search(_ context.Context, _ string, query []byte) ([]byte, error) {
	n := len(q.asked)
	q.asked = append(q.asked, query)
	var body []byte
	if n < len(q.bodies) {
		body = q.bodies[n]
	}
	if n < len(q.errs) && q.errs[n] != nil {
		return nil, q.errs[n]
	}
	return body, nil
}

// archiveServer is a server with the exporter configured and a cluster stubbed in.
func archiveServer(t *testing.T, searcher opensearch.Searcher) *Server {
	t.Helper()
	s := storesFor(t)
	s.osExportCfg = opensearch.Config{URL: "http://events.invalid", Index: "atlas-events"}
	s.eventSearch = searcher
	return s
}

// The whole point of S4: an instance retention hard-deleted is still findable, and
// the two-step lookup is what finds it — which instances held the value, then what
// those instances were.
func TestSearchArchiveFindsAPurgedInstance(t *testing.T) {
	q := &queueSearcher{bodies: [][]byte{
		[]byte(`{"aggregations":{"instances":{"buckets":[{"key":7,"doc_count":2}]}}}`),
		[]byte(`{"hits":{"hits":[{"_source":{"key":7,"position":90,"value":{"ProcessDefKey":42,"State":3,"CreatedAt":100,"CompletedAt":900}}}]}}`),
	}}
	s := archiveServer(t, q)

	got := s.searchArchive(context.Background(), 42, varQuery{rawName: "identityId", pattern: "MT-1998", literal: "MT-1998", structured: true})
	if got.State != archiveAvailable {
		t.Fatalf("state = %q (%s), want available", got.State, got.Reason)
	}
	if len(got.Instances) != 1 || got.Instances[0].key != 7 {
		t.Fatalf("instances = %+v, want the one instance the log remembers", got.Instances)
	}
	if len(q.asked) != 2 {
		t.Fatalf("asked %d queries, want 2 — scopes then instances", len(q.asked))
	}
	// The second query must ask about the instances the first one found, not about
	// everything: the aggregation is what bounds this lookup.
	if !hasClause(filters(t, decodeQuery(t, q.asked[1])), "terms", "key", []uint64{7}) {
		t.Errorf("second query %s does not narrow to the instances found", q.asked[1])
	}
}

// Nothing in the archive is an empty answer, and it must not cost a second query:
// asking about no instances would return whatever the filter matched instead.
func TestSearchArchiveStopsWhenNothingMatched(t *testing.T) {
	q := &queueSearcher{bodies: [][]byte{[]byte(`{"aggregations":{"instances":{"buckets":[]}}}`)}}
	s := archiveServer(t, q)

	got := s.searchArchive(context.Background(), 0, varQuery{rawName: "identityId", pattern: "nobody", literal: "nobody", structured: true})
	if got.State != archiveEmpty {
		t.Errorf("state = %q, want empty", got.State)
	}
	if len(q.asked) != 1 {
		t.Errorf("asked %d queries, want 1 — there was nothing to hydrate", len(q.asked))
	}
}

// A server with no exporter says so rather than answering "nothing found". The two
// are different facts, and only one of them is about the data.
func TestSearchArchiveSaysWhenItIsNotConfigured(t *testing.T) {
	s := storesFor(t)
	got := s.searchArchive(context.Background(), 0, varQuery{rawName: "x", pattern: "y", literal: "y", structured: true})
	if got.State != archiveNotConfigured {
		t.Errorf("state = %q, want notConfigured", got.State)
	}
	if got.Reason == "" {
		t.Error("no reason given for an unconfigured archive")
	}
}

// Refused and unreachable send an operator to two different places — the credentials
// and the network — so they must not be flattened into one outcome.
func TestSearchArchiveSeparatesRefusedFromUnreachable(t *testing.T) {
	refused := archiveServer(t, &queueSearcher{errs: []error{opensearch.ErrSearchRefused}})
	if got := refused.searchArchive(context.Background(), 0, varQuery{rawName: "x", pattern: "y", literal: "y", structured: true}); got.State != archiveRefused {
		t.Errorf("state = %q, want refused", got.State)
	}
	down := archiveServer(t, &queueSearcher{errs: []error{errors.New("dial tcp: no route to host")}})
	if got := down.searchArchive(context.Background(), 0, varQuery{rawName: "x", pattern: "y", literal: "y", structured: true}); got.State != archiveUnreachable {
		t.Errorf("state = %q, want unreachable", got.State)
	}
}

// A cluster that answers in a shape this server cannot read is a broken cluster, not
// an empty archive.
func TestSearchArchiveRefusesAnUnreadableAnswer(t *testing.T) {
	s := archiveServer(t, &queueSearcher{bodies: [][]byte{[]byte(`{"aggregations":`)}})
	if got := s.searchArchive(context.Background(), 0, varQuery{rawName: "x", pattern: "y", literal: "y", structured: true}); got.State != archiveUnreachable {
		t.Errorf("state = %q, want unreachable", got.State)
	}
}

// An archived row is not a live one and the wire says so. An operator who cannot see
// the difference would try to open, cancel or migrate an instance that is not there.
func TestArchiveRowIsMarkedAndCarriesNoLiveDetail(t *testing.T) {
	rows := archiveRows([]archiveInstance{{
		key: 7, processDefKey: 42, state: model.PICompleted,
		createdAt: 100, completedAt: 900, correlationKey: "c-1",
	}}, defIndex{42: defMeta{ProcessID: "identitaet", Version: 3}})

	if len(rows) != 1 {
		t.Fatalf("got %d rows, want 1", len(rows))
	}
	got := rows[0]
	if !got.Archived {
		t.Error("row is not marked archived")
	}
	if got.ElementInstances != 0 {
		t.Errorf("elementInstances = %d, want 0 — the archive knows of no live tokens", got.ElementInstances)
	}
	if got.Key != 7 || got.ProcessDefKey != 42 || got.CompletedAt != 900 {
		t.Errorf("row = %+v, want what the log recorded", got)
	}
	if got.ProcessID != "identitaet" || got.Version != 3 {
		t.Errorf("row = %+v, want the definition resolved where it still exists", got)
	}
	if got.Variables == nil {
		t.Error("variables is nil, which encodes as null rather than an empty list")
	}
}

// A definition the archive names but this server no longer has still yields a row:
// the instance existed, and saying nothing about it because its model is gone too
// would hide exactly the history the export was built to keep.
func TestArchiveRowSurvivesAMissingDefinition(t *testing.T) {
	rows := archiveRows([]archiveInstance{{key: 7, processDefKey: 42}}, defIndex{})
	if len(rows) != 1 || rows[0].Key != 7 {
		t.Fatalf("rows = %+v, want the instance even without its definition", rows)
	}
	if rows[0].ProcessID != "" {
		t.Errorf("processId = %q, want empty rather than invented", rows[0].ProcessID)
	}
}

// The archive is a fallback, not a second opinion. It is asked only when this
// server's own store had nothing to say — and only for a question it can answer.
func TestArchiveIsAskedOnlyWhenTheLiveStoreCameUpEmpty(t *testing.T) {
	structured := varQuery{rawName: "identityId", pattern: "MT-1998", literal: "MT-1998", structured: true}
	free := varQuery{pattern: "Testperson", literal: "Testperson"}
	oneRow := []instanceResp{{Key: 7}}

	cases := []struct {
		what string
		rows []instanceResp
		pred varQuery
		want bool
	}{
		{"nothing found for a structured query", nil, structured, true},
		{"the live store already answered", oneRow, structured, false},
		{"free text, which the archive is not asked in this shape", nil, free, false},
	}
	for _, c := range cases {
		if got := shouldAskArchive(c.rows, c.pred); got != c.want {
			t.Errorf("%s: shouldAskArchive = %v, want %v", c.what, got, c.want)
		}
	}
}
