package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/pblumer/atlas/api/panorama"
	"github.com/pblumer/atlas/api/runloop"
	"github.com/pblumer/atlas/opensearch"
)

// stubSearcher stands in for a cluster. It records the query it was handed, so the
// tests can assert what was actually asked rather than only what came back.
type stubSearcher struct {
	body  []byte
	err   error
	asked [][]byte
	index string
}

func (s *stubSearcher) Search(_ context.Context, index string, query []byte) ([]byte, error) {
	s.index = index
	s.asked = append(s.asked, query)
	return s.body, s.err
}

// contextServer is a server with an application, two deployed versions of one
// process, and an event log configured.
func contextServer(t *testing.T, searcher opensearch.Searcher) *Server {
	t.Helper()
	s := storesFor(t)
	if err := s.projects.Save(project{ID: "proj-abc", Name: "Billing"}); err != nil {
		t.Fatalf("save project: %v", err)
	}
	s.order = []uint64{11, 12, 13}
	s.deployments = map[uint64]*deployment{
		11: {Key: 11, ProcessID: "ship", Version: 1, ProjectID: "proj-abc"},
		12: {Key: 12, ProcessID: "ship", Version: 2, ProjectID: "proj-abc"},
		13: {Key: 13, ProcessID: "other", Version: 1, ProjectID: "proj-other"},
	}
	s.versions = map[string]int32{"ship": 2, "other": 1}
	s.osExportCfg = opensearch.Config{URL: "http://events.invalid", Index: "atlas-events"}
	s.eventSearch = searcher
	return s
}

func testWindow() panorama.ContextWindow {
	window, _ := panorama.NewContextWindow(panorama.Window1h, 1_700_000_000)
	return window
}

// aggregationBody is a cluster's reply with counts on two of the three measures.
func aggregationBody(t *testing.T, started, completed int) []byte {
	t.Helper()
	window := testWindow()
	bucket := func(n int) []map[string]any {
		return []map[string]any{
			{"key": window.From * 1000, "doc_count": n},
			{"key": (window.From + 60) * 1000, "doc_count": 0},
		}
	}
	body, err := json.Marshal(map[string]any{"aggregations": map[string]any{
		"instancesStarted": map[string]any{
			"doc_count": started, "overTime": map[string]any{"buckets": bucket(started)},
		},
		"instancesCompleted": map[string]any{
			"doc_count": completed, "overTime": map[string]any{"buckets": bucket(completed)},
		},
	}})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return body
}

func resolve(t *testing.T, s *Server, key, value string) contextTarget {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	projs, err := s.projectsByID()
	if err != nil {
		t.Fatalf("projectsByID: %v", err)
	}
	return s.resolveContextTarget(req, projs,
		panorama.ContextQuery{Key: key, Value: value, Window: testWindow()})
}

// TestEventContextAnswersAboutProcessesAndApplications. Those are the two kinds the
// exported record can attribute — every event carries its process definition key —
// and both resolve to the definition keys the query names.
func TestEventContextAnswersAboutProcessesAndApplications(t *testing.T) {
	stub := &stubSearcher{body: aggregationBody(t, 7, 5)}
	s := contextServer(t, stub)

	// A process id resolves to every version of it, not only the current one: a
	// window of history spans deployments, and counting only the latest version
	// would silently drop everything that ran before this morning's release.
	target := resolve(t, s, panorama.KeyProcessID, "ship")
	if len(target.keys) != 2 || target.keys[0] != 11 || target.keys[1] != 12 {
		t.Fatalf("process keys = %v, want every deployed version", target.keys)
	}
	// An application rolls up the processes it owns, and nothing else.
	app := resolve(t, s, panorama.KeyApplicationID, "proj-abc")
	if len(app.keys) != 2 {
		t.Fatalf("application keys = %v", app.keys)
	}

	result := s.eventContext(context.Background(), target)
	if result.State != panorama.ContextAvailable || result.Source != panorama.ContextSourceEvents {
		t.Fatalf("result = %+v", result)
	}
	if len(result.Measures) != len(eventMeasures) {
		t.Fatalf("measures = %+v, want all three the adapter claims", result.Measures)
	}
	if result.Measures[0].Total != 7 || result.Measures[1].Total != 5 {
		t.Errorf("totals = %+v", result.Measures)
	}
	// A measure the cluster left out is present and empty, not omitted: dropping it
	// would silently narrow what this adapter claims to answer.
	if result.Measures[2].Name != "instancesTerminated" || result.Measures[2].Total != 0 {
		t.Errorf("the third measure = %+v", result.Measures[2])
	}
	// The absence of incidents is named, because it is a real gap and a silent one.
	if !strings.Contains(result.Detail["notCounted"], "incident") {
		t.Errorf("detail = %+v, want the gap named", result.Detail)
	}
}

// TestEventContextQueryNamesOnlyTheResolvedKeys. The query is what the scope check
// turns into: a definition this caller may not see contributes no key, so the
// cluster is never asked a question whose answer would count somebody else's work.
func TestEventContextQueryNamesOnlyTheResolvedKeys(t *testing.T) {
	stub := &stubSearcher{body: aggregationBody(t, 1, 1)}
	s := contextServer(t, stub)

	s.eventContext(context.Background(), resolve(t, s, panorama.KeyProcessID, "ship"))
	if len(stub.asked) != 1 {
		t.Fatalf("asked %d times", len(stub.asked))
	}
	query := string(stub.asked[0])
	for _, want := range []string{"value.ProcessDefKey", "ProcessInstance", "date_histogram", "11", "12"} {
		if !strings.Contains(query, want) {
			t.Errorf("query does not mention %q: %s", want, query)
		}
	}
	// The other application's definition is not in it.
	if strings.Contains(query, "13") {
		t.Errorf("query names a definition outside the binding: %s", query)
	}
	// It asks for no documents: the answer's size is then the aggregation's shape
	// rather than anything that grows with how busy the landscape was.
	if !strings.Contains(query, `"size":0`) {
		t.Errorf("query requests documents: %s", query)
	}
	if stub.index != "atlas-events" {
		t.Errorf("index = %q, want the one the exporter writes", stub.index)
	}
}

// TestEventContextSeparatesEveryWayOfNotAnswering. Six states exist because each
// sends an operator somewhere different, and collapsing them into "no data" is the
// conflation this whole surface is arranged against.
func TestEventContextSeparatesEveryWayOfNotAnswering(t *testing.T) {
	t.Run("no store wired", func(t *testing.T) {
		s := contextServer(t, &stubSearcher{})
		s.osExportCfg = opensearch.Config{}
		got := s.eventContext(context.Background(), resolve(t, s, panorama.KeyProcessID, "ship"))
		if got.State != panorama.ContextNotConfigured {
			t.Errorf("state = %q", got.State)
		}
	})

	t.Run("refused", func(t *testing.T) {
		s := contextServer(t, &stubSearcher{err: opensearch.ErrSearchRefused})
		got := s.eventContext(context.Background(), resolve(t, s, panorama.KeyProcessID, "ship"))
		if got.State != panorama.ContextRefused {
			t.Errorf("state = %q, want a refusal told apart from an outage", got.State)
		}
	})

	t.Run("unreachable", func(t *testing.T) {
		s := contextServer(t, &stubSearcher{err: errors.New("dial: no route")})
		got := s.eventContext(context.Background(), resolve(t, s, panorama.KeyProcessID, "ship"))
		if got.State != panorama.ContextUnreachable {
			t.Errorf("state = %q", got.State)
		}
		if !strings.Contains(got.Reason, "not the same as nothing having happened") {
			t.Errorf("reason = %q", got.Reason)
		}
	})

	t.Run("unreadable answer", func(t *testing.T) {
		s := contextServer(t, &stubSearcher{body: []byte("<html>proxy error</html>")})
		got := s.eventContext(context.Background(), resolve(t, s, panorama.KeyProcessID, "ship"))
		if got.State != panorama.ContextUnreachable {
			t.Errorf("state = %q, want an unparseable reply reported as not knowing", got.State)
		}
	})

	t.Run("empty", func(t *testing.T) {
		s := contextServer(t, &stubSearcher{body: aggregationBody(t, 0, 0)})
		got := s.eventContext(context.Background(), resolve(t, s, panorama.KeyProcessID, "ship"))
		if got.State != panorama.ContextEmpty {
			t.Errorf("state = %q, want the one state that is about the architecture", got.State)
		}
	})

	t.Run("process nothing here deploys", func(t *testing.T) {
		s := contextServer(t, &stubSearcher{body: aggregationBody(t, 1, 1)})
		got := s.eventContext(context.Background(), resolve(t, s, panorama.KeyProcessID, "absent"))
		if got.State != panorama.ContextUnidentifiable {
			t.Errorf("state = %q", got.State)
		}
		if !strings.Contains(got.Reason, "definition key") {
			t.Errorf("reason = %q, want it to name the obstacle", got.Reason)
		}
	})

	// An application that exists and has nothing deployed is a statement about the
	// architecture, so it is empty rather than unidentifiable — the distinction the
	// two states are for.
	t.Run("application with nothing deployed", func(t *testing.T) {
		s := contextServer(t, &stubSearcher{body: aggregationBody(t, 1, 1)})
		if err := s.projects.Save(project{ID: "proj-idle", Name: "Idle"}); err != nil {
			t.Fatalf("save: %v", err)
		}
		got := s.eventContext(context.Background(), resolve(t, s, panorama.KeyApplicationID, "proj-idle"))
		if got.State != panorama.ContextEmpty {
			t.Errorf("state = %q", got.State)
		}
	})

	t.Run("application that is not here", func(t *testing.T) {
		s := contextServer(t, &stubSearcher{body: aggregationBody(t, 1, 1)})
		got := s.eventContext(context.Background(), resolve(t, s, panorama.KeyApplicationID, "proj-nope"))
		if got.State != panorama.ContextUnidentifiable {
			t.Errorf("state = %q", got.State)
		}
	})
}

// TestEventContextCannotIdentifyWhatTheLogDoesNotName. These are properties of the
// exported record rather than gaps in the data, and each reason names the actual
// obstacle so nobody goes looking for a field that was never written.
func TestEventContextCannotIdentifyWhatTheLogDoesNotName(t *testing.T) {
	stub := &stubSearcher{body: aggregationBody(t, 1, 1)}
	s := contextServer(t, stub)

	for key, want := range map[string]string{
		panorama.KeyConnectorID:        "interned index",
		panorama.KeyJobType:            "interned index",
		panorama.KeyRuntimeID:          "no node identity",
		panorama.KeyDeploymentTargetID: "no node identity",
		panorama.KeyReleaseID:          "not something the engine emits",
	} {
		got := s.eventContext(context.Background(), resolve(t, s, key, "v"))
		if got.State != panorama.ContextUnidentifiable {
			t.Errorf("%s: state = %q", key, got.State)
		}
		if !strings.Contains(got.Reason, want) {
			t.Errorf("%s: reason = %q, want it to mention %q", key, got.Reason, want)
		}
	}
	// None of them asked the cluster: a question the store cannot answer is not a
	// question worth somebody else's CPU.
	if len(stub.asked) != 0 {
		t.Errorf("an unidentifiable value still queried the cluster: %d times", len(stub.asked))
	}
}

// TestMetricContextIsHonestBeforeItsAdapterExists. Metrics carry no per-element
// labels by design (ADR-0142), so the store answers about a node and never about
// one process. Saying so now keeps the distinction visible rather than turning it
// into a surprise when the adapter lands.
func TestMetricContextIsHonestBeforeItsAdapterExists(t *testing.T) {
	s := contextServer(t, &stubSearcher{})

	node := s.metricContext(resolve(t, s, panorama.KeyRuntimeID, "node-1"))
	if node.State != panorama.ContextNotConfigured {
		t.Errorf("a node = %+v, want not-configured on a build with no metrics adapter", node)
	}
	process := s.metricContext(resolve(t, s, panorama.KeyProcessID, "ship"))
	if process.State != panorama.ContextUnidentifiable {
		t.Errorf("a process = %+v, want unidentifiable rather than absent data", process)
	}
	if !strings.Contains(process.Reason, "no per-element labels") {
		t.Errorf("reason = %q", process.Reason)
	}
}

// TestCollectContextAsksEverySourceAboutEveryValue. Both sources answer for every
// value, because a row that was simply left out is indistinguishable from a source
// nobody thought to ask.
func TestCollectContextAsksEverySourceAboutEveryValue(t *testing.T) {
	s := contextServer(t, &stubSearcher{body: aggregationBody(t, 3, 3)})
	// A live run loop, because phase one takes a turn on it and a server without one
	// reports itself as shutting down.
	quit := make(chan struct{})
	s.quit, s.runLoop = quit, runloop.New(quit)
	var wg sync.WaitGroup
	wg.Add(1)
	go func() { defer wg.Done(); s.runLoop.Run() }()
	t.Cleanup(func() { close(quit); wg.Wait() })

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	results, err := s.collectContext(req, []panorama.ContextQuery{
		{Key: panorama.KeyProcessID, Value: "ship", Window: testWindow()},
		{Key: panorama.KeyRuntimeID, Value: "node-1", Window: testWindow()},
	})
	if err != nil {
		t.Fatalf("collectContext: %v", err)
	}
	if len(results) != 4 {
		t.Fatalf("results = %d, want one per source per value: %+v", len(results), results)
	}
	seen := map[string]string{}
	for _, result := range results {
		seen[result.Value+"/"+result.Source] = result.State
	}
	if seen["ship/"+panorama.ContextSourceEvents] != panorama.ContextAvailable {
		t.Errorf("the log store's answer about a process = %q", seen["ship/"+panorama.ContextSourceEvents])
	}
	if seen["node-1/"+panorama.ContextSourceEvents] != panorama.ContextUnidentifiable {
		t.Errorf("the log store's answer about a node = %q", seen["node-1/"+panorama.ContextSourceEvents])
	}
	if seen["node-1/"+panorama.ContextSourceMetrics] != panorama.ContextNotConfigured {
		t.Errorf("the metrics store's answer about a node = %q", seen["node-1/"+panorama.ContextSourceMetrics])
	}
}

// TestParseEventContextDropsBucketsOutsideTheWindow. The histogram keys in epoch
// milliseconds while every other timestamp in this contract is Unix seconds, and a
// bucket outside the window asked about is a sign the two were mixed — which is how
// a renderer ends up drawing 1970.
func TestParseEventContextDropsBucketsOutsideTheWindow(t *testing.T) {
	window := testWindow()
	body, err := json.Marshal(map[string]any{"aggregations": map[string]any{
		"instancesStarted": map[string]any{"doc_count": 2, "overTime": map[string]any{
			"buckets": []map[string]any{
				{"key": window.From * 1000, "doc_count": 1},
				{"key": int64(0), "doc_count": 1},                    // the epoch
				{"key": (window.To + 10_000) * 1000, "doc_count": 1}, // past the window
			},
		}},
	}})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	measures, total, err := parseEventContext(body, window)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if total != 2 {
		t.Errorf("total = %d", total)
	}
	if len(measures[0].Buckets) != 1 || measures[0].Buckets[0].At != window.From {
		t.Errorf("buckets = %+v, want only the one inside the window", measures[0].Buckets)
	}

	// An empty body is an empty answer, not a parse failure: a missing index answers
	// with no bytes at all.
	if _, _, err := parseEventContext(nil, window); err != nil {
		t.Errorf("an empty body failed to parse: %v", err)
	}
}
