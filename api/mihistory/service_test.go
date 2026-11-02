package mihistory

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/cockroachdb/pebble"
	"github.com/pblumer/atlas/compiler"
	"github.com/pblumer/atlas/engine"
	"github.com/pblumer/atlas/expr"
	"github.com/pblumer/atlas/model"
	"github.com/pblumer/atlas/state"
	"github.com/pblumer/atlas/wal"
)

type fixedClock struct{ t int64 }

func (c *fixedClock) Now() int64 { return c.t }

// world is one engine with one deployed MI process, driven synchronously.
type world struct {
	t       *testing.T
	dir     string
	log     *wal.Log
	store   *state.Store
	p       *engine.Processor
	cp      *compiler.CompiledProcess
	jobType int32
	svc     *Service
	now     time.Time
	srv     *httptest.Server
}

func compileFEEL(t *testing.T, src string) *expr.Compiled {
	t.Helper()
	c, err := expr.CompileAuto(src)
	if err != nil {
		t.Fatalf("compile %q: %v", src, err)
	}
	return c
}

func miProcess(t *testing.T, items string, sequential bool) (*compiler.CompiledProcess, int32) {
	t.Helper()
	b := compiler.NewBuilder(1, "mi-api", 1)
	start := b.AddStartEvent()
	setup := b.AddScriptTask(compileFEEL(t, items), "items")
	work := b.AddServiceTask("apiwork", 3)
	b.SetMultiInstance(work, sequential, "item", "", compileFEEL(t, "items"), nil, nil, nil)
	end := b.AddEndEvent()
	b.Connect(start, setup)
	b.Connect(setup, work)
	b.Connect(work, end)
	cp, err := b.Build()
	if err != nil {
		t.Fatal(err)
	}
	return cp, cp.ServiceTask(cp.Node(work).Detail).JobType
}

func newWorld(t *testing.T, items string, sequential bool) *world {
	t.Helper()
	w := &world{t: t, dir: t.TempDir(), now: time.Unix(1_800_000_000, 0)}
	var err error
	if w.log, err = wal.Open(wal.Options{Dir: filepath.Join(w.dir, "wal")}); err != nil {
		t.Fatal(err)
	}
	if w.store, err = state.Open(filepath.Join(w.dir, "state")); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = w.store.Close()
		_ = w.log.Close()
	})
	w.cp, w.jobType = miProcess(t, items, sequential)
	w.p = engine.New(1, w.log, w.store, &fixedClock{t: 1})
	w.p.Deploy(w.cp)
	if err := w.p.Recover(); err != nil {
		t.Fatal(err)
	}
	w.svc = newService(w.view, []byte("test-cursor-key-0123456789abcdef"), func() time.Time { return w.now })
	mux := http.NewServeMux()
	w.mount(mux, w.svc)
	w.srv = httptest.NewServer(mux)
	t.Cleanup(w.srv.Close)
	return w
}

func (w *world) mount(mux *http.ServeMux, svc *Service) {
	mux.HandleFunc("GET /api/v1/instances/{key}/multi-instances", svc.HandleBodies)
	mux.HandleFunc("GET /api/v1/instances/{key}/multi-instances/{bodyKey}", svc.HandleBody)
	mux.HandleFunc("GET /api/v1/instances/{key}/multi-instances/{bodyKey}/iterations", svc.HandleIterations)
	mux.HandleFunc("GET /api/v1/instances/{key}/multi-instances/{bodyKey}/iterations/{iterationKey}", svc.HandleIteration)
	mux.HandleFunc("GET /api/v1/instances/{key}/multi-instances/{bodyKey}/iterations/{iterationKey}/steps", svc.HandleIterationSteps)
	mux.HandleFunc("GET /api/v1/instances/{key}/multi-instances/{bodyKey}/iterations/{iterationKey}/variables", svc.HandleIterationVariables)
	mux.HandleFunc("GET /api/v1/instances/{key}/outline", svc.HandleOutline)
}

// view is what the server supplies: a read view of the store, closed after fn.
// The test drives the processor synchronously, so no loop is needed to take it.
func (w *world) view(fn func(*state.ReadView, Definitions) error) error {
	rv := w.store.ReadView()
	defer rv.Close()
	return fn(rv, func(k uint64) *compiler.CompiledProcess {
		if k == w.cp.Key {
			return w.cp
		}
		return nil
	})
}

func (w *world) run() {
	w.t.Helper()
	if err := w.p.RunUntilIdle(); err != nil {
		w.t.Fatal(err)
	}
}

func (w *world) start() uint64 {
	w.t.Helper()
	w.p.CreateInstance(w.cp.Key)
	w.run()
	var pi uint64
	if err := w.store.ActiveProcessInstances(func(k uint64, _ *model.ProcessInstanceValue) error {
		if k > pi {
			pi = k
		}
		return nil
	}); err != nil {
		w.t.Fatal(err)
	}
	return pi
}

func (w *world) jobs() []uint64 {
	var out []uint64
	if err := w.store.ActivatableJobs(w.jobType, func(k uint64) error { out = append(out, k); return nil }); err != nil {
		w.t.Fatal(err)
	}
	return out
}

func (w *world) jobFor(round uint64) uint64 {
	w.t.Helper()
	for _, jk := range w.jobs() {
		j, ok, err := w.store.GetJob(jk)
		if err != nil || !ok {
			w.t.Fatal(err)
		}
		if j.ElementInstanceKey == round {
			return jk
		}
	}
	w.t.Fatalf("no job for iteration %d", round)
	return 0
}

func (w *world) get(path string, query url.Values, out any) (int, map[string]string) {
	w.t.Helper()
	u := w.srv.URL + path
	if len(query) > 0 {
		u += "?" + query.Encode()
	}
	resp, err := http.Get(u)
	if err != nil {
		w.t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		var e map[string]string
		_ = json.NewDecoder(resp.Body).Decode(&e)
		return resp.StatusCode, e
	}
	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		w.t.Fatal(err)
	}
	return resp.StatusCode, nil
}

func (w *world) onlyBody(pi uint64) BodyView {
	w.t.Helper()
	var resp BodiesResponse
	if code, e := w.get(instPath(pi), nil, &resp); code != 200 {
		w.t.Fatalf("bodies: %d %v", code, e)
	}
	if len(resp.Bodies) != 1 {
		w.t.Fatalf("bodies: %+v", resp.Bodies)
	}
	return resp.Bodies[0]
}

func instPath(pi uint64) string {
	return "/api/v1/instances/" + strconv.FormatUint(pi, 10) + "/multi-instances"
}

func iterPath(pi, body uint64) string {
	return instPath(pi) + "/" + strconv.FormatUint(body, 10) + "/iterations"
}

func listIter(items int) string {
	var b strings.Builder
	b.WriteString("[")
	for i := 1; i <= items; i++ {
		if i > 1 {
			b.WriteString(",")
		}
		b.WriteString(strconv.Itoa(i))
	}
	b.WriteString("]")
	return b.String()
}

func numbers(rows []IterationView) []uint64 {
	out := make([]uint64, len(rows))
	for i, r := range rows {
		if r.Iteration != nil {
			out[i] = *r.Iteration
		}
	}
	return out
}

func TestIterationsPageStablyAndJumpByIterationNumber(t *testing.T) {
	w := newWorld(t, listIter(250), false)
	pi := w.start()
	body := w.onlyBody(pi)
	if body.Mode != "parallel" || body.Iterations != 250 || body.Active != 250 || body.State != "active" {
		t.Fatalf("body summary: %+v", body)
	}
	var listing BodiesResponse
	w.get(instPath(pi), nil, &listing)
	if listing.ProcessDefinitionKey != w.cp.Key || listing.InstanceState != "active" {
		t.Fatalf("instance facts on the listing: %+v", listing)
	}

	seen := map[uint64]bool{}
	q := url.Values{}
	pages := 0
	for {
		var page IterationsResponse
		if code, e := w.get(iterPath(pi, body.BodyElementInstanceKey), q, &page); code != 200 {
			t.Fatalf("page %d: %d %v", pages, code, e)
		}
		pages++
		for _, r := range page.Iterations {
			n := *r.Iteration
			if seen[n] {
				t.Fatalf("iteration %d on two pages", n)
			}
			seen[n] = true
		}
		if page.NextCursor == "" {
			break
		}
		q = url.Values{"cursor": {page.NextCursor}}
	}
	if pages != 3 || len(seen) != 250 {
		t.Fatalf("%d pages, %d iterations", pages, len(seen))
	}

	var jump IterationsResponse
	w.get(iterPath(pi, body.BodyElementInstanceKey), url.Values{"from": {"137"}, "limit": {"3"}}, &jump)
	if got := numbers(jump.Iterations); len(got) != 3 || got[0] != 137 || got[2] != 139 || jump.NextCursor == "" {
		t.Fatalf("jump to 137: %v next=%q", got, jump.NextCursor)
	}
	// The cursor of a jump continues after the jump, with the same filters.
	var after IterationsResponse
	w.get(iterPath(pi, body.BodyElementInstanceKey), url.Values{"from": {"137"}, "limit": {"3"}, "cursor": {jump.NextCursor}}, &after)
	if got := numbers(after.Iterations); len(got) != 3 || got[0] != 140 {
		t.Fatalf("after jump: %v", got)
	}
	var exact IterationsResponse
	w.get(iterPath(pi, body.BodyElementInstanceKey), url.Values{"iteration": {"200"}}, &exact)
	if got := numbers(exact.Iterations); len(got) != 1 || got[0] != 200 || exact.NextCursor != "" {
		t.Fatalf("exact iteration 200: %v next=%q", got, exact.NextCursor)
	}
	var byKey IterationsResponse
	w.get(iterPath(pi, body.BodyElementInstanceKey), url.Values{"elementInstanceKey": {strconv.FormatUint(exact.Iterations[0].ElementInstanceKey, 10)}}, &byKey)
	if len(byKey.Iterations) != 1 || *byKey.Iterations[0].Iteration != 200 {
		t.Fatalf("lookup by element-instance key: %+v", byKey.Iterations)
	}
	var none IterationsResponse
	w.get(iterPath(pi, body.BodyElementInstanceKey), url.Values{"iteration": {"251"}}, &none)
	if len(none.Iterations) != 0 {
		t.Fatalf("iteration 251 exists: %+v", none.Iterations)
	}

	// Complete iteration 7 and look it up by state and through its detail route.
	w.p.CompleteJob(w.jobFor(exactKeyOf(t, w, pi, body.BodyElementInstanceKey, 7)))
	w.run()
	var done IterationsResponse
	w.get(iterPath(pi, body.BodyElementInstanceKey), url.Values{"state": {"completed"}}, &done)
	if got := numbers(done.Iterations); len(got) != 1 || got[0] != 7 || done.Body.Completed != 1 || done.Body.Active != 249 {
		t.Fatalf("completed filter: %v body=%+v", got, done.Body)
	}
	var detail IterationResponse
	path := iterPath(pi, body.BodyElementInstanceKey) + "/" + strconv.FormatUint(done.Iterations[0].ElementInstanceKey, 10)
	if code, e := w.get(path, nil, &detail); code != 200 || detail.Iteration.State != "completed" || detail.Iteration.EndPosition == 0 {
		t.Fatalf("detail: %d %v %+v", code, e, detail)
	}
}

func exactKeyOf(t *testing.T, w *world, pi, body uint64, n int) uint64 {
	t.Helper()
	var resp IterationsResponse
	w.get(iterPath(pi, body), url.Values{"iteration": {strconv.Itoa(n)}}, &resp)
	if len(resp.Iterations) != 1 {
		t.Fatalf("iteration %d not found", n)
	}
	return resp.Iterations[0].ElementInstanceKey
}

func TestIterationRequestsRejectBadParametersAndCursors(t *testing.T) {
	w := newWorld(t, listIter(5), false)
	pi := w.start()
	other := w.start()
	body := w.onlyBody(pi)
	otherBody := w.onlyBody(other)
	path := iterPath(pi, body.BodyElementInstanceKey)

	var first IterationsResponse
	w.get(path, url.Values{"limit": {"2"}}, &first)
	if first.NextCursor == "" {
		t.Fatal("no cursor on a partial page")
	}
	tampered := []byte(first.NextCursor)
	tampered[10] ^= 1
	if tampered[10] == '-' || tampered[10] == '_' {
		tampered[10] = 'A'
	}
	foreign := newService(w.view, []byte("another-servers-key-000000000000"), func() time.Time { return w.now })
	var foreignPage IterationsResponse
	mux := http.NewServeMux()
	w.mount(mux, foreign)
	fsrv := httptest.NewServer(mux)
	defer fsrv.Close()
	resp, err := http.Get(fsrv.URL + path + "?limit=2")
	if err != nil {
		t.Fatal(err)
	}
	_ = json.NewDecoder(resp.Body).Decode(&foreignPage)
	resp.Body.Close()

	tests := []struct {
		name   string
		path   string
		query  url.Values
		status int
		code   string
	}{
		{"limit zero", path, url.Values{"limit": {"0"}}, 400, "invalid_parameter"},
		{"limit too large", path, url.Values{"limit": {"201"}}, 400, "invalid_parameter"},
		{"limit not a number", path, url.Values{"limit": {"ten"}}, 400, "invalid_parameter"},
		{"state unknown", path, url.Values{"state": {"failed"}}, 400, "invalid_parameter"},
		{"incidents unknown", path, url.Values{"incidents": {"maybe"}}, 400, "invalid_parameter"},
		{"iteration zero", path, url.Values{"iteration": {"0"}}, 400, "invalid_parameter"},
		{"iteration and from", path, url.Values{"iteration": {"1"}, "from": {"2"}}, 400, "invalid_parameter"},
		{"key with filter", path, url.Values{"elementInstanceKey": {"1"}, "state": {"active"}}, 400, "invalid_parameter"},
		{"garbage cursor", path, url.Values{"cursor": {"not-a-cursor"}}, 400, "invalid_cursor"},
		{"tampered cursor", path, url.Values{"cursor": {string(tampered)}, "limit": {"2"}}, 400, "invalid_cursor"},
		{"other server's cursor", path, url.Values{"cursor": {foreignPage.NextCursor}}, 400, "invalid_cursor"},
		{"cursor for another instance", iterPath(other, otherBody.BodyElementInstanceKey), url.Values{"cursor": {first.NextCursor}}, 400, "cursor_mismatch"},
		{"cursor with changed filters", path, url.Values{"cursor": {first.NextCursor}, "state": {"active"}}, 400, "cursor_mismatch"},
		{"body cursor on iterations", path, url.Values{"cursor": {bodiesCursor(t, w, pi)}}, 400, "cursor_mismatch"},
		{"unknown instance", iterPath(999999, body.BodyElementInstanceKey), nil, 404, "not_found"},
		{"unknown body", iterPath(pi, 424242), nil, 404, "not_found"},
		{"body of another instance", iterPath(pi, otherBody.BodyElementInstanceKey), nil, 404, "not_found"},
		{"bad instance key", "/api/v1/instances/abc/multi-instances", nil, 400, "invalid_parameter"},
		{"from not a number", path, url.Values{"from": {"x"}}, 400, "invalid_parameter"},
		{"outline of a bad instance key", "/api/v1/instances/abc/outline", nil, 400, "invalid_parameter"},
		{"outline limit too large", "/api/v1/instances/" + strconv.FormatUint(pi, 10) + "/outline", url.Values{"limit": {"201"}}, 400, "invalid_parameter"},
		{"unknown iteration", path + "/31337", nil, 404, "not_found"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var ignored any
			status, e := w.get(tt.path, tt.query, &ignored)
			if status != tt.status || e["code"] != tt.code || e["error"] == "" {
				t.Fatalf("got %d %v, want %d %s", status, e, tt.status, tt.code)
			}
		})
	}

	w.now = w.now.Add(2 * time.Hour)
	var ignored any
	if status, e := w.get(path, url.Values{"cursor": {first.NextCursor}, "limit": {"2"}}, &ignored); status != 400 || e["code"] != "cursor_expired" {
		t.Fatalf("expired cursor: %d %v", status, e)
	}
}

// bodiesCursor makes a bodies-route cursor for an instance with two bodies.
func bodiesCursor(t *testing.T, w *world, pi uint64) string {
	t.Helper()
	return w.svc.encodeCursor(cursor{kind: kindBodies, instance: pi, filter: filterDigest("bodies|parent=0"), issuedAt: w.now.Unix()})
}

func TestIterationPagesFreezeTheHighWaterMarkAndRefuseStaleStateFilters(t *testing.T) {
	w := newWorld(t, listIter(6), true) // sequential: iterations appear one by one
	pi := w.start()
	body := w.onlyBody(pi)
	path := iterPath(pi, body.BodyElementInstanceKey)
	if body.Mode != "sequential" || body.Iterations != 1 {
		t.Fatalf("sequential body: %+v", body)
	}
	finish := func(n int) {
		w.p.CompleteJob(w.jobFor(exactKeyOf(t, w, pi, body.BodyElementInstanceKey, n)))
		w.run()
	}
	finish(1)
	finish(2) // iterations 1, 2 completed; 3 active

	var first IterationsResponse
	w.get(path, url.Values{"limit": {"1"}}, &first)
	var active IterationsResponse
	w.get(path, url.Values{"limit": {"1"}, "state": {"completed"}}, &active)
	if numbers(first.Iterations)[0] != 1 || active.NextCursor == "" {
		t.Fatalf("first pages: %v %v", numbers(first.Iterations), numbers(active.Iterations))
	}
	finish(3)
	finish(4) // iteration 5 now active, seeded after the first page

	var rest []uint64
	q := url.Values{"limit": {"1"}, "cursor": {first.NextCursor}}
	for {
		var page IterationsResponse
		if code, e := w.get(path, q, &page); code != 200 {
			t.Fatalf("resume: %d %v", code, e)
		}
		rest = append(rest, numbers(page.Iterations)...)
		if page.NextCursor == "" {
			break
		}
		q.Set("cursor", page.NextCursor)
	}
	if len(rest) != 2 || rest[0] != 2 || rest[1] != 3 {
		t.Fatalf("pages after the high-water mark: %v, want [2 3]", rest)
	}
	var ignored any
	if status, e := w.get(path, url.Values{"limit": {"1"}, "state": {"completed"}, "cursor": {active.NextCursor}}, &ignored); status != 409 || e["code"] != "cursor_stale" {
		t.Fatalf("stale state-filtered cursor: %d %v", status, e)
	}
}

func TestIncidentFilterAndDetailDistinguishOpenFromHistorical(t *testing.T) {
	w := newWorld(t, listIter(4), false)
	pi := w.start()
	body := w.onlyBody(pi)
	k2 := exactKeyOf(t, w, pi, body.BodyElementInstanceKey, 2)
	k3 := exactKeyOf(t, w, pi, body.BodyElementInstanceKey, 3)
	w.p.FailJob(w.jobFor(k2), 0, "first", 0)
	w.run()
	w.p.ResolveIncident(k2, 1)
	w.run()
	w.p.FailJob(w.jobFor(k3), 0, "second", 0)
	w.run()

	var page IterationsResponse
	w.get(iterPath(pi, body.BodyElementInstanceKey), url.Values{"incidents": {"true"}}, &page)
	if got := numbers(page.Iterations); len(got) != 2 || got[0] != 2 || got[1] != 3 {
		t.Fatalf("incident iterations: %v", got)
	}
	if page.Iterations[0].OpenIncidents != 0 || page.Iterations[1].OpenIncidents != 1 || !page.Iterations[1].OpenIncidentsExact {
		t.Fatalf("open incidents per row: %+v", page.Iterations)
	}
	var one BodyResponse
	if code, e := w.get(instPath(pi)+"/"+strconv.FormatUint(body.BodyElementInstanceKey, 10), nil, &one); code != 200 {
		t.Fatalf("body: %d %v", code, e)
	}
	if one.Body.IncidentsRaised != 2 || one.Body.IterationsWithIncidents != 2 || *one.Body.OpenIncidents != 1 || !*one.Body.OpenIncidentsExact {
		t.Fatalf("body incidents: %+v open=%v", one.Body, *one.Body.OpenIncidents)
	}
	var detail IterationResponse
	w.get(iterPath(pi, body.BodyElementInstanceKey)+"/"+strconv.FormatUint(k2, 10), nil, &detail)
	if len(detail.Incidents) != 1 || detail.Incidents[0].Status != "resolved" {
		t.Fatalf("iteration 2 incidents: %+v", detail.Incidents)
	}
	w.get(iterPath(pi, body.BodyElementInstanceKey)+"/"+strconv.FormatUint(k3, 10), nil, &detail)
	if len(detail.Incidents) != 1 || detail.Incidents[0].Status != "open" || detail.Iteration.OpenIncidents != 1 {
		t.Fatalf("iteration 3 incidents: %+v", detail)
	}

	// Cancelling the instance terminates iteration 3 with its incident open; no
	// resolution is recorded, and the incident reads as cleared, not open.
	w.p.CancelInstance(pi)
	w.run()
	w.get(iterPath(pi, body.BodyElementInstanceKey)+"/"+strconv.FormatUint(k3, 10), nil, &detail)
	if detail.Iteration.State != "terminated" || detail.Incidents[0].Status != "cleared" || detail.Iteration.OpenIncidents != 0 {
		t.Fatalf("after cancel: %+v", detail)
	}
	w.get(instPath(pi)+"/"+strconv.FormatUint(body.BodyElementInstanceKey, 10), nil, &one)
	if *one.Body.OpenIncidents != 0 || one.Body.State != "terminated" || one.Body.Terminated != 4 {
		t.Fatalf("body after cancel: %+v", one.Body)
	}
}

// legacyWorld runs one instance of a two-iteration MI process, then reopens its
// store with the named readiness marker removed — what a store written before
// that part of the index existed looks like.
func legacyWorld(t *testing.T, marker string) (*world, uint64) {
	t.Helper()
	dir := t.TempDir()
	statePath := filepath.Join(dir, "state")
	w := &world{t: t, now: time.Unix(1_800_000_000, 0)}
	var err error
	if w.log, err = wal.Open(wal.Options{Dir: filepath.Join(dir, "wal")}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = w.log.Close() })
	if w.store, err = state.Open(statePath); err != nil {
		t.Fatal(err)
	}
	w.cp, w.jobType = miProcess(t, "[1, 2]", false)
	w.p = engine.New(1, w.log, w.store, &fixedClock{t: 1})
	w.p.Deploy(w.cp)
	if err := w.p.Recover(); err != nil {
		t.Fatal(err)
	}
	pi := w.start()
	if err := w.store.Close(); err != nil {
		t.Fatal(err)
	}
	// The state store's merge operator is checked by name on open; this copy
	// sums the same little-endian int64 deltas, so a merge Pebble runs while the
	// test edits the store keeps the counters intact.
	db, err := pebble.Open(statePath, &pebble.Options{Merger: &pebble.Merger{
		Name: "atlas.counter.sum.v1",
		Merge: func(_, v []byte) (pebble.ValueMerger, error) {
			m := &sumMerger{}
			return m, m.MergeNewer(v)
		},
	}})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Delete(append([]byte{0x00}, marker...), pebble.Sync); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	if w.store, err = state.Open(statePath); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = w.store.Close() })
	w.svc = newService(w.view, []byte("k"), func() time.Time { return w.now })
	mux := http.NewServeMux()
	w.mount(mux, w.svc)
	w.srv = httptest.NewServer(mux)
	t.Cleanup(w.srv.Close)
	return w, pi
}

func TestUnavailableIndexIsAnExplainedConflictNotAnEmptyList(t *testing.T) {
	w, pi := legacyWorld(t, "mi_history_index_v2")
	for _, path := range []string{instPath(pi), instPath(pi) + "/1", iterPath(pi, 1), iterPath(pi, 1) + "/2"} {
		var ignored any
		status, e := w.get(path, nil, &ignored)
		if status != http.StatusConflict || e["code"] != "history_unavailable" || !strings.Contains(e["error"], "does not mean no iterations ran") {
			t.Fatalf("%s: %d %v", path, status, e)
		}
	}
}

// History that predates decision attribution keeps its iteration detail and says
// that its decision list is unknown rather than empty.
func TestDecisionsUnavailableOnOlderHistoryIsNotAnEmptyList(t *testing.T) {
	w, pi := legacyWorld(t, "mi_history_decisions_v1")
	body := w.onlyBody(pi)
	k := exactKeyOf(t, w, pi, body.BodyElementInstanceKey, 1)
	var d IterationResponse
	if code, e := w.get(iterPath(pi, body.BodyElementInstanceKey)+"/"+strconv.FormatUint(k, 10), nil, &d); code != 200 {
		t.Fatalf("detail: %d %v", code, e)
	}
	if d.DecisionsAvailable || len(d.Decisions) != 0 || len(d.Variables) == 0 {
		t.Fatalf("detail of older history: %+v", d)
	}
}

// nestedWorld runs an instance of a parallel MI subprocess over three items whose
// body holds a sequential MI service task over two; its jobs are left open.
func nestedWorld(t *testing.T) (*world, uint64) {
	t.Helper()
	w := newWorld(t, "[1]", false)
	b := compiler.NewBuilder(1, "mi-nest-api", 1)
	start := b.AddStartEvent()
	sub := b.AddSubProcess()
	b.SetMultiInstance(sub, false, "o", "", compileFEEL(t, "[1, 2, 3]"), nil, nil, nil)
	b.PushScope(sub)
	is := b.AddStartEvent()
	inner := b.AddServiceTask("nestedapi", 3)
	b.SetMultiInstance(inner, true, "x", "", compileFEEL(t, "[1, 2]"), nil, nil, nil)
	ie := b.AddEndEvent()
	b.Connect(is, inner)
	b.Connect(inner, ie)
	b.PopScope()
	end := b.AddEndEvent()
	b.Connect(start, sub)
	b.Connect(sub, end)
	cp, err := b.Build()
	if err != nil {
		t.Fatal(err)
	}
	w.cp = cp
	w.p.Deploy(cp)
	return w, w.start()
}

func TestBodiesListTopLevelAndNestedExecutionsSeparately(t *testing.T) {
	w, pi := nestedWorld(t)
	var top BodiesResponse
	w.get(instPath(pi), url.Values{"limit": {"1"}}, &top)
	if len(top.Bodies) != 1 || top.Bodies[0].Iterations != 3 || top.NextCursor != "" {
		t.Fatalf("top level: %+v next=%q", top.Bodies, top.NextCursor)
	}
	outer := top.Bodies[0]
	var rounds IterationsResponse
	w.get(iterPath(pi, outer.BodyElementInstanceKey), nil, &rounds)
	if len(rounds.Iterations) != 3 {
		t.Fatalf("outer iterations: %+v", rounds.Iterations)
	}
	second := rounds.Iterations[1].ElementInstanceKey
	var nested BodiesResponse
	w.get(instPath(pi), url.Values{"parentIteration": {strconv.FormatUint(second, 10)}}, &nested)
	if len(nested.Bodies) != 1 || nested.Bodies[0].ParentIterationKey != second || nested.Bodies[0].Mode != "sequential" {
		t.Fatalf("nested bodies of iteration 2: %+v", nested.Bodies)
	}
	var detail IterationResponse
	w.get(iterPath(pi, outer.BodyElementInstanceKey)+"/"+strconv.FormatUint(second, 10), nil, &detail)
	if len(detail.NestedBodies) != 1 || detail.NestedBodies[0].BodyElementInstanceKey != nested.Bodies[0].BodyElementInstanceKey {
		t.Fatalf("iteration detail nested bodies: %+v", detail.NestedBodies)
	}
	// Paging the top level with limit 1 over one body issues no cursor; a
	// bodies cursor is still bound to its parent filter.
	var ignored any
	cur := w.svc.encodeCursor(cursor{kind: kindBodies, instance: pi, filter: filterDigest("bodies|parent=0"),
		afterA: outer.ActivationPosition, afterB: outer.BodyElementInstanceKey, issuedAt: w.now.Unix()})
	if status, e := w.get(instPath(pi), url.Values{"cursor": {cur}, "parentIteration": {strconv.FormatUint(second, 10)}}, &ignored); status != 400 || e["code"] != "cursor_mismatch" {
		t.Fatalf("bodies cursor with another parent: %d %v", status, e)
	}
	if status, _ := w.get(instPath(pi), url.Values{"cursor": {cur}}, &top); status != 200 || len(top.Bodies) != 0 {
		t.Fatalf("resumed past the only body: %d %+v", status, top.Bodies)
	}
}

type sumMerger struct{ sum int64 }

func (m *sumMerger) MergeNewer(v []byte) error {
	if len(v) >= 8 {
		m.sum += int64(binary.LittleEndian.Uint64(v))
	}
	return nil
}

func (m *sumMerger) MergeOlder(v []byte) error { return m.MergeNewer(v) }

func (m *sumMerger) Finish(bool) ([]byte, io.Closer, error) {
	return binary.LittleEndian.AppendUint64(nil, uint64(m.sum)), nil, nil
}

func TestIterationDetailShowsItsVariablesAndDecisions(t *testing.T) {
	w := newWorld(t, listIter(3), false)
	pi := w.start()
	body := w.onlyBody(pi)
	k2 := exactKeyOf(t, w, pi, body.BodyElementInstanceKey, 2)
	w.p.CompleteJobWithDecision(w.jobFor(k2), &model.DecisionEvaluationValue{
		DecisionId: "risk", InputsJSON: `{"item":2}`, OutputsJSON: `{"ok":true}`, TraceJSON: `{"rules":[1]}`,
	})
	w.run()
	path := iterPath(pi, body.BodyElementInstanceKey) + "/" + strconv.FormatUint(k2, 10)
	var d IterationResponse
	if code, e := w.get(path, nil, &d); code != 200 {
		t.Fatalf("detail: %d %v", code, e)
	}
	names := map[string]string{}
	for _, v := range d.Variables {
		names[v.Name] = v.Value
	}
	if names["loopCounter"] != "2" || names["item"] != "2" {
		t.Fatalf("iteration variables: %+v", d.Variables)
	}
	if !d.DecisionsAvailable || len(d.Decisions) != 1 || d.Decisions[0].DecisionID != "risk" ||
		string(d.Decisions[0].Inputs) != `{"item":2}` || d.Decisions[0].Trace != nil {
		t.Fatalf("decisions: %+v", d)
	}
	var tech IterationResponse
	w.get(path, url.Values{"technical": {"true"}}, &tech)
	if len(tech.Decisions) != 1 || string(tech.Decisions[0].Trace) != `{"rules":[1]}` {
		t.Fatalf("technical detail: %+v", tech.Decisions)
	}
	// Another iteration made no decision: an empty list that is known to be empty.
	var other IterationResponse
	w.get(iterPath(pi, body.BodyElementInstanceKey)+"/"+strconv.FormatUint(exactKeyOf(t, w, pi, body.BodyElementInstanceKey, 1), 10), nil, &other)
	if !other.DecisionsAvailable || len(other.Decisions) != 0 {
		t.Fatalf("iteration 1 decisions: %+v", other)
	}
}

func TestRenderingHelpers(t *testing.T) {
	for _, tt := range []struct {
		v          model.VariableValue
		kind, text string
	}{
		{model.VariableValue{Kind: model.VarBool, Bool: true}, "boolean", "true"},
		{model.VariableValue{Kind: model.VarNumber, Text: "1"}, "number", "1"},
		{model.VariableValue{Kind: model.VarString, Text: "a"}, "string", "a"},
		{model.VariableValue{Kind: model.VarJSON, Text: "{}"}, "json", "{}"},
		{model.VariableValue{Kind: model.VarNull}, "null", "null"},
	} {
		if k, v := plainVariable(&tt.v); k != tt.kind || v != tt.text {
			t.Fatalf("%+v rendered %s %s", tt.v, k, v)
		}
	}
	if string(rawJSON("")) != "null" || string(rawJSON("{bad")) != `"{bad"` || string(rawJSON(`[1]`)) != `[1]` {
		t.Fatal("rawJSON")
	}
}

// An element that failed, was resolved and failed again keeps both incidents on
// its iteration's detail, each with its own status.
func TestIterationDetailKeepsEveryIncidentOfAnElement(t *testing.T) {
	w := newWorld(t, listIter(2), false)
	pi := w.start()
	body := w.onlyBody(pi)
	k := exactKeyOf(t, w, pi, body.BodyElementInstanceKey, 1)
	w.p.FailJob(w.jobFor(k), 0, "first", 0)
	w.run()
	// A retry that fails again raises a second incident on the same element.
	w.p.ResolveIncident(k, 1)
	w.run()
	w.p.FailJob(w.jobFor(k), 0, "second", 0)
	w.run()
	var d IterationResponse
	w.get(iterPath(pi, body.BodyElementInstanceKey)+"/"+strconv.FormatUint(k, 10), nil, &d)
	if len(d.Incidents) != 2 || d.Incidents[0].Status != "resolved" || d.Incidents[1].Status != "open" {
		t.Fatalf("incidents: %+v", d.Incidents)
	}
	// Paging the bodies with a cursor resumes after the body the cursor names.
	var first BodiesResponse
	w.get(instPath(pi), url.Values{"limit": {"1"}}, &first)
	if len(first.Bodies) != 1 || first.NextCursor != "" {
		t.Fatalf("bodies: %+v", first)
	}
}

// The engine raises a second incident on an element only after resolving the
// first; a superseded attribution is the index's guard against one that did
// not, and it must read as superseded without consulting the incident table.
func TestIncidentStatusOfAResolvedOrSupersededRecordNeedsNoLookup(t *testing.T) {
	for outcome, want := range map[state.MIIncidentOutcome]string{
		state.MIIncidentResolved:   "resolved",
		state.MIIncidentSuperseded: "superseded",
	} {
		got, err := incidentStatus(nil, state.MIIncidentRecord{Outcome: outcome})
		if err != nil || got != want {
			t.Fatalf("outcome %d: %q %v, want %q", outcome, got, err, want)
		}
	}
}

// twoBodyProcess runs two parallel MI service tasks one after the other, so an
// instance has two top-level body executions once the first has completed.
func twoBodyWorld(t *testing.T) (*world, uint64) {
	t.Helper()
	w := newWorld(t, "[1]", false)
	b := compiler.NewBuilder(1, "mi-two", 1)
	start := b.AddStartEvent()
	first := b.AddScriptTask(compileFEEL(t, "loopCounter"), "a")
	b.SetMultiInstance(first, false, "", "", nil, compileFEEL(t, "2"), nil, nil)
	second := b.AddServiceTask("twowork", 3)
	b.SetMultiInstance(second, false, "", "", nil, compileFEEL(t, "2"), nil, nil)
	end := b.AddEndEvent()
	b.Connect(start, first)
	b.Connect(first, second)
	b.Connect(second, end)
	cp, err := b.Build()
	if err != nil {
		t.Fatal(err)
	}
	w.cp, w.jobType = cp, cp.ServiceTask(cp.Node(second).Detail).JobType
	w.p.Deploy(cp)
	return w, w.start()
}

func TestBodiesPageByCursorAndKeysStayInsideTheirBody(t *testing.T) {
	w, pi := twoBodyWorld(t)
	var first BodiesResponse
	w.get(instPath(pi), url.Values{"limit": {"1"}}, &first)
	if len(first.Bodies) != 1 || first.NextCursor == "" {
		t.Fatalf("first page: %+v", first)
	}
	var second BodiesResponse
	w.get(instPath(pi), url.Values{"limit": {"1"}, "cursor": {first.NextCursor}}, &second)
	if len(second.Bodies) != 1 || second.Bodies[0].BodyElementInstanceKey == first.Bodies[0].BodyElementInstanceKey ||
		second.Bodies[0].ActivationPosition <= first.Bodies[0].ActivationPosition || second.NextCursor != "" {
		t.Fatalf("second page: %+v", second)
	}
	a, b := first.Bodies[0].BodyElementInstanceKey, second.Bodies[0].BodyElementInstanceKey
	var rows IterationsResponse
	w.get(iterPath(pi, a), nil, &rows)
	foreign := rows.Iterations[0].ElementInstanceKey
	// An iteration key of body a, asked for under body b, is not one of b's.
	var none IterationsResponse
	w.get(iterPath(pi, b), url.Values{"elementInstanceKey": {strconv.FormatUint(foreign, 10)}}, &none)
	if len(none.Iterations) != 0 {
		t.Fatalf("a foreign iteration was listed under another body: %+v", none.Iterations)
	}
	var ignored any
	if status, e := w.get(iterPath(pi, b)+"/"+strconv.FormatUint(foreign, 10), nil, &ignored); status != 404 || e["code"] != "not_found" {
		t.Fatalf("foreign iteration detail: %d %v", status, e)
	}
	// A cursor naming body b at another activation position is refused: a
	// body key alone does not name an execution.
	cur := w.svc.encodeCursor(cursor{kind: kindIterations, instance: pi, body: b, bodyPos: second.Bodies[0].ActivationPosition + 1,
		filter: filterDigest(iterationFilter{}.canonical()), issuedAt: w.now.Unix()})
	if status, e := w.get(iterPath(pi, b), url.Values{"cursor": {cur}}, &ignored); status != 400 || e["code"] != "cursor_mismatch" {
		t.Fatalf("cursor for another activation: %d %v", status, e)
	}
}

// A request whose client has gone away opens no read view.
func TestACancelledRequestReadsNothing(t *testing.T) {
	opened := false
	svc := newService(func(func(*state.ReadView, Definitions) error) error { opened = true; return nil }, []byte("k"), time.Now)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/instances/1/multi-instances", nil).WithContext(ctx)
	req.SetPathValue("key", "1")
	rec := httptest.NewRecorder()
	svc.HandleBodies(rec, req)
	if opened || rec.Code == http.StatusOK {
		t.Fatalf("cancelled request: status %d, view opened %v", rec.Code, opened)
	}
}

// reopenWith closes the world's store, applies edit to its Pebble directory and
// opens it again — how a test makes an index row corrupt.
func (w *world) reopenWith(edit func(db *pebble.DB)) {
	w.t.Helper()
	statePath := filepath.Join(w.dir, "state")
	if err := w.store.Close(); err != nil {
		w.t.Fatal(err)
	}
	db, err := pebble.Open(statePath, &pebble.Options{Merger: &pebble.Merger{
		Name: "atlas.counter.sum.v1",
		Merge: func(_, v []byte) (pebble.ValueMerger, error) {
			m := &sumMerger{}
			return m, m.MergeNewer(v)
		},
	}})
	if err != nil {
		w.t.Fatal(err)
	}
	edit(db)
	if err := db.Close(); err != nil {
		w.t.Fatal(err)
	}
	if w.store, err = state.Open(statePath); err != nil {
		w.t.Fatal(err)
	}
}

func be64(parts ...uint64) []byte {
	var out []byte
	for _, p := range parts {
		out = binary.BigEndian.AppendUint64(out, p)
	}
	return out
}

// A corrupt MI index is reported as an internal error on every route that
// reads the damaged row — never as a plausible body, page or detail.
func TestACorruptIndexIsAnErrorNeverAPlausibleAnswer(t *testing.T) {
	type fixture struct {
		pi, body, bodyPos, iter uint64
	}
	setup := func(t *testing.T) (*world, fixture) {
		w := newWorld(t, listIter(2), false)
		pi := w.start()
		b := w.onlyBody(pi)
		k := exactKeyOf(t, w, pi, b.BodyElementInstanceKey, 1)
		w.p.FailJob(w.jobFor(k), 0, "x", 0)
		w.run()
		w.p.CompleteJobWithDecision(w.jobFor(exactKeyOf(t, w, pi, b.BodyElementInstanceKey, 2)),
			&model.DecisionEvaluationValue{DecisionId: "d", InputsJSON: `{}`, OutputsJSON: `{}`})
		w.run()
		return w, fixture{pi, b.BodyElementInstanceKey, b.ActivationPosition, k}
	}
	junk := []byte{0xff}
	for _, tt := range []struct {
		name  string
		edit  func(db *pebble.DB, f fixture) error
		paths func(f fixture) []string
	}{
		{"body row", func(db *pebble.DB, f fixture) error {
			return db.Set(append([]byte{0x38}, be64(f.pi, f.bodyPos, f.body)...), junk, pebble.Sync)
		}, func(f fixture) []string {
			return []string{instPath(f.pi), instPath(f.pi) + "/" + strconv.FormatUint(f.body, 10), iterPath(f.pi, f.body),
				iterPath(f.pi, f.body) + "/" + strconv.FormatUint(f.iter, 10)}
		}},
		{"incident record", func(db *pebble.DB, f fixture) error {
			iter, err := db.NewIter(&pebble.IterOptions{LowerBound: append([]byte{0x3F}, be64(f.pi, f.iter)...),
				UpperBound: append([]byte{0x3F}, be64(f.pi, f.iter+1)...)})
			if err != nil {
				return err
			}
			defer iter.Close()
			if !iter.First() {
				return errors.New("no incident record to corrupt")
			}
			return db.Set(append([]byte(nil), iter.Key()...), junk, pebble.Sync)
		}, func(f fixture) []string {
			return []string{instPath(f.pi) + "/" + strconv.FormatUint(f.body, 10), iterPath(f.pi, f.body),
				iterPath(f.pi, f.body) + "/" + strconv.FormatUint(f.iter, 10)}
		}},
		{"decision reference", func(db *pebble.DB, f fixture) error {
			// A reference naming an evaluation that does not exist.
			return db.Set(append([]byte{0x43}, be64(f.pi, f.iter, 1<<63, 1)...), nil, pebble.Sync)
		}, func(f fixture) []string {
			return []string{iterPath(f.pi, f.body) + "/" + strconv.FormatUint(f.iter, 10)}
		}},
		{"iteration step", func(db *pebble.DB, f fixture) error {
			return db.Set(append([]byte{0x46}, be64(f.pi, f.iter, 1<<63, 1)...), junk, pebble.Sync)
		}, func(f fixture) []string {
			return []string{iterPath(f.pi, f.body) + "/" + strconv.FormatUint(f.iter, 10) + "/steps"}
		}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			w, f := setup(t)
			w.reopenWith(func(db *pebble.DB) {
				if err := tt.edit(db, f); err != nil {
					t.Fatal(err)
				}
			})
			for _, path := range tt.paths(f) {
				var ignored any
				status, e := w.get(path, nil, &ignored)
				if status != http.StatusInternalServerError || e["code"] != "internal" {
					t.Fatalf("%s: %d %v", path, status, e)
				}
			}
		})
	}
}

func TestOutlineShowsTheInstanceWithoutItsIterations(t *testing.T) {
	w := newWorld(t, listIter(250), false)
	pi := w.start()
	var page OutlineResponse
	if code, e := w.get("/api/v1/instances/"+strconv.FormatUint(pi, 10)+"/outline", nil, &page); code != 200 {
		t.Fatalf("outline: %d %v", code, e)
	}
	// start activated+completed, script activated+completed, the MI body
	// activated — 250 iterations are behind it, not in the outline.
	var bodies, rows int
	for _, s := range page.Steps {
		rows++
		if s.MultiInstanceBody {
			bodies++
		}
		if s.Type == "" || s.Action == "" {
			t.Fatalf("step without type or action: %+v", s)
		}
	}
	if bodies != 1 || rows != 5 || page.NextCursor != "" || page.InstanceState != "active" || page.ProcessDefinitionKey != w.cp.Key {
		t.Fatalf("outline: %+v", page)
	}
	var first, second OutlineResponse
	path := "/api/v1/instances/" + strconv.FormatUint(pi, 10) + "/outline"
	w.get(path, url.Values{"limit": {"2"}}, &first)
	w.get(path, url.Values{"limit": {"2"}, "cursor": {first.NextCursor}}, &second)
	if len(first.Steps) != 2 || len(second.Steps) != 2 || second.Steps[0].Position <= first.Steps[1].Position {
		t.Fatalf("paged outline: %+v / %+v", first.Steps, second.Steps)
	}
	var ignored any
	if status, e := w.get(path, url.Values{"cursor": {bodiesCursor(t, w, pi)}}, &ignored); status != 400 || e["code"] != "cursor_mismatch" {
		t.Fatalf("a bodies cursor on the outline: %d %v", status, e)
	}
	if status, e := w.get("/api/v1/instances/999999/outline", nil, &ignored); status != 404 {
		t.Fatalf("unknown instance: %d %v", status, e)
	}
}

func TestOutlineOfOlderHistoryIsUnavailable(t *testing.T) {
	w, pi := legacyWorld(t, "instance_outline_v1")
	var ignored any
	status, e := w.get("/api/v1/instances/"+strconv.FormatUint(pi, 10)+"/outline", nil, &ignored)
	if status != http.StatusConflict || e["code"] != "history_unavailable" {
		t.Fatalf("outline of older history: %d %v", status, e)
	}
}

// The replay window of one iteration is what ran inside it, in order: its own
// activation, the subprocess's start event, the nested body as one step — and
// nothing of the other iterations.
func TestIterationStepsAreOneIterationsReplayWindow(t *testing.T) {
	w, pi := nestedWorld(t)
	var top BodiesResponse
	w.get(instPath(pi), nil, &top)
	outer := top.Bodies[0].BodyElementInstanceKey
	var rounds IterationsResponse
	w.get(iterPath(pi, outer), nil, &rounds)
	second := rounds.Iterations[1].ElementInstanceKey
	path := iterPath(pi, outer) + "/" + strconv.FormatUint(second, 10) + "/steps"
	var page IterationStepsResponse
	if code, e := w.get(path, nil, &page); code != 200 {
		t.Fatalf("steps: %d %v", code, e)
	}
	var got []string
	for _, s := range page.Steps {
		if s.ElementInstanceKey != second && s.Action == "activated" && s.MultiInstanceIteration {
			t.Fatalf("another iteration in this one's window: %+v", s)
		}
		got = append(got, s.Action+":"+s.Type)
	}
	if page.Iteration.ElementInstanceKey != second || page.Iteration.Iteration == nil || *page.Iteration.Iteration != 2 || page.Body.BodyElementInstanceKey != outer ||
		len(page.Steps) != 4 || !page.Steps[0].MultiInstanceIteration || !page.Steps[3].MultiInstanceBody || page.NextCursor != "" {
		t.Fatalf("steps %v: %+v", got, page)
	}

	var first, rest IterationStepsResponse
	w.get(path, url.Values{"limit": {"3"}}, &first)
	if code, e := w.get(path, url.Values{"cursor": {first.NextCursor}}, &rest); code != 200 || len(first.Steps) != 3 || len(rest.Steps) != 1 ||
		rest.Steps[0].Position != page.Steps[3].Position {
		t.Fatalf("paged: %d %v %+v / %+v", code, e, first.Steps, rest.Steps)
	}

	var nested BodiesResponse
	w.get(instPath(pi), url.Values{"parentIteration": {strconv.FormatUint(second, 10)}}, &nested)
	inner := nested.Bodies[0].BodyElementInstanceKey
	var innerRounds IterationsResponse
	w.get(iterPath(pi, inner), nil, &innerRounds)
	var innerSteps IterationStepsResponse
	w.get(iterPath(pi, inner)+"/"+strconv.FormatUint(innerRounds.Iterations[0].ElementInstanceKey, 10)+"/steps", nil, &innerSteps)
	if len(innerSteps.Steps) != 1 || !innerSteps.Steps[0].MultiInstanceIteration || innerSteps.Steps[0].Type != page.Steps[3].Type {
		t.Fatalf("a nested iteration's own steps: %+v", innerSteps.Steps)
	}

	third := strconv.FormatUint(rounds.Iterations[2].ElementInstanceKey, 10)
	tests := []struct {
		name   string
		path   string
		query  url.Values
		status int
		code   string
	}{
		{"cursor of another iteration", iterPath(pi, outer) + "/" + third + "/steps", url.Values{"cursor": {first.NextCursor}}, 400, "cursor_mismatch"},
		{"outline cursor", path, url.Values{"cursor": {outlineCursor(t, w, pi)}}, 400, "cursor_mismatch"},
		{"iteration of another body", iterPath(pi, inner) + "/" + strconv.FormatUint(second, 10) + "/steps", nil, 404, "not_found"},
		{"unknown body", iterPath(pi, 424242) + "/" + strconv.FormatUint(second, 10) + "/steps", nil, 404, "not_found"},
		{"bad iteration key", iterPath(pi, outer) + "/x/steps", nil, 400, "invalid_parameter"},
		{"bad body key", "/api/v1/instances/" + strconv.FormatUint(pi, 10) + "/multi-instances/x/iterations/1/steps", nil, 400, "invalid_parameter"},
		{"bad instance key", "/api/v1/instances/x/multi-instances/1/iterations/1/steps", nil, 400, "invalid_parameter"},
		{"limit too large", path, url.Values{"limit": {"201"}}, 400, "invalid_parameter"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var ignored any
			if status, e := w.get(tt.path, tt.query, &ignored); status != tt.status || e["code"] != tt.code {
				t.Fatalf("got %d %v, want %d %s", status, e, tt.status, tt.code)
			}
		})
	}
}

func outlineCursor(t *testing.T, w *world, pi uint64) string {
	t.Helper()
	var page OutlineResponse
	w.get("/api/v1/instances/"+strconv.FormatUint(pi, 10)+"/outline", url.Values{"limit": {"1"}}, &page)
	if page.NextCursor == "" {
		t.Fatal("no outline cursor")
	}
	return page.NextCursor
}

func TestIterationStepsOfOlderHistoryAreUnavailable(t *testing.T) {
	w, pi := legacyWorld(t, "mi_iteration_steps_v1")
	b := w.onlyBody(pi)
	var rounds IterationsResponse
	w.get(iterPath(pi, b.BodyElementInstanceKey), nil, &rounds)
	var ignored any
	status, e := w.get(iterPath(pi, b.BodyElementInstanceKey)+"/"+strconv.FormatUint(rounds.Iterations[0].ElementInstanceKey, 10)+"/steps", nil, &ignored)
	if status != http.StatusConflict || e["code"] != "history_unavailable" {
		t.Fatalf("steps of older history: %d %v", status, e)
	}
}

func varsPath(pi, body, iter uint64) string {
	return iterPath(pi, body) + "/" + strconv.FormatUint(iter, 10) + "/variables"
}

func scopeValues(sc StepScope) map[string]string {
	out := map[string]string{}
	for _, v := range sc.Variables {
		out[v.Name] = v.Value
	}
	return out
}

// The variables at a step are the iteration's own scope and the scopes running
// inside it then, each folded to the latest value per name at or before the
// step — never another iteration's.
func TestIterationVariablesAtAStep(t *testing.T) {
	w, pi := nestedWorld(t)
	var top BodiesResponse
	w.get(instPath(pi), nil, &top)
	outer := top.Bodies[0].BodyElementInstanceKey
	var rounds IterationsResponse
	w.get(iterPath(pi, outer), nil, &rounds)
	second := rounds.Iterations[1].ElementInstanceKey
	var steps IterationStepsResponse
	w.get(iterPath(pi, outer)+"/"+strconv.FormatUint(second, 10)+"/steps", nil, &steps)
	last := steps.Steps[len(steps.Steps)-1]

	at := func(pos uint64) IterationVariablesResponse {
		t.Helper()
		var resp IterationVariablesResponse
		if code, e := w.get(varsPath(pi, outer, second), url.Values{"at": {strconv.FormatUint(pos, 10)}}, &resp); code != 200 {
			t.Fatalf("variables at %d: %d %v", pos, code, e)
		}
		return resp
	}
	end := at(last.Position)
	if end.At != last.Position || end.Iteration.ElementInstanceKey != second || len(end.Scopes) == 0 ||
		!end.Scopes[0].Iteration || end.Scopes[0].ElementInstanceKey != second {
		t.Fatalf("variables at the last step: %+v", end)
	}
	own := scopeValues(end.Scopes[0])
	if own["o"] != "2" || own["loopCounter"] != "2" {
		t.Fatalf("the second iteration's own variables: %v", own)
	}
	for _, sc := range end.Scopes[1:] {
		if sc.ElementInstanceKey == second || sc.Iteration {
			t.Fatalf("a scope listed twice: %+v", end.Scopes)
		}
	}
	// An iteration's inputs are bound before it is activated; before they were
	// written its scope holds nothing, and it is listed anyway, so "no variables"
	// is said about this iteration.
	first := at(1)
	if len(first.Scopes) != 1 || len(first.Scopes[0].Variables) != 0 {
		t.Fatalf("variables before the iteration began: %+v", first.Scopes)
	}
	for _, v := range end.Scopes[0].Variables {
		if v.Position > last.Position {
			t.Fatalf("a change after the step: %+v", v)
		}
	}

	var nested BodiesResponse
	w.get(instPath(pi), url.Values{"parentIteration": {strconv.FormatUint(second, 10)}}, &nested)
	inner := nested.Bodies[0].BodyElementInstanceKey
	tests := []struct {
		name   string
		path   string
		query  url.Values
		status int
		code   string
	}{
		{"no position", varsPath(pi, outer, second), nil, 400, "invalid_parameter"},
		{"bad position", varsPath(pi, outer, second), url.Values{"at": {"x"}}, 400, "invalid_parameter"},
		{"iteration of another body", varsPath(pi, inner, second), url.Values{"at": {"1"}}, 404, "not_found"},
		{"unknown body", varsPath(pi, 424242, second), url.Values{"at": {"1"}}, 404, "not_found"},
		{"bad iteration key", iterPath(pi, outer) + "/x/variables", url.Values{"at": {"1"}}, 400, "invalid_parameter"},
		{"bad body key", "/api/v1/instances/" + strconv.FormatUint(pi, 10) + "/multi-instances/x/iterations/1/variables", url.Values{"at": {"1"}}, 400, "invalid_parameter"},
		{"bad instance key", "/api/v1/instances/x/multi-instances/1/iterations/1/variables", url.Values{"at": {"1"}}, 400, "invalid_parameter"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var ignored any
			if status, e := w.get(tt.path, tt.query, &ignored); status != tt.status || e["code"] != tt.code {
				t.Fatalf("got %d %v, want %d %s", status, e, tt.status, tt.code)
			}
		})
	}
}

// A large value is cut on a character boundary and says so.
func TestIterationVariablesCutALargeValue(t *testing.T) {
	big := strings.Repeat("ä", 3000) // 6,000 bytes
	w := newWorld(t, `["`+big+`"]`, false)
	pi := w.start()
	b := w.onlyBody(pi)
	var rounds IterationsResponse
	w.get(iterPath(pi, b.BodyElementInstanceKey), nil, &rounds)
	it := rounds.Iterations[0].ElementInstanceKey
	var resp IterationVariablesResponse
	if code, e := w.get(varsPath(pi, b.BodyElementInstanceKey, it), url.Values{"at": {"999999"}}, &resp); code != 200 {
		t.Fatalf("variables: %d %v", code, e)
	}
	var item *StepVariable
	for i, v := range resp.Scopes[0].Variables {
		if v.Name == "item" {
			item = &resp.Scopes[0].Variables[i]
		}
	}
	if item == nil || !item.ValueTruncated || len(item.Value) > stepValueCap || !utf8.ValidString(item.Value) || !strings.Contains(item.Value, "ää") {
		t.Fatalf("the large value: %+v", item)
	}
}

func TestIterationVariablesOfOlderHistoryAreUnavailable(t *testing.T) {
	w, pi := legacyWorld(t, "mi_iteration_steps_v1")
	b := w.onlyBody(pi)
	var rounds IterationsResponse
	w.get(iterPath(pi, b.BodyElementInstanceKey), nil, &rounds)
	var ignored any
	status, e := w.get(varsPath(pi, b.BodyElementInstanceKey, rounds.Iterations[0].ElementInstanceKey), url.Values{"at": {"1"}}, &ignored)
	if status != http.StatusConflict || e["code"] != "history_unavailable" {
		t.Fatalf("variables of older history: %d %v", status, e)
	}
}

// A scope running inside the iteration is shown while it runs: a nested body
// assembling its output collection holds it between its activation and its end,
// and is gone from the steps after.
func TestIterationVariablesShowAScopeRunningInsideTheIteration(t *testing.T) {
	w := newWorld(t, "[1]", false)
	b := compiler.NewBuilder(1, "mi-vars-nested", 1)
	start := b.AddStartEvent()
	sub := b.AddSubProcess()
	b.SetMultiInstance(sub, false, "o", "", compileFEEL(t, "[1, 2]"), nil, nil, nil)
	b.PushScope(sub)
	is := b.AddStartEvent()
	inner := b.AddScriptTask(compileFEEL(t, "x * 10"), "r")
	b.SetMultiInstance(inner, true, "x", "results", compileFEEL(t, "[1, 2, 3]"), nil, compileFEEL(t, "r"), nil)
	b.SetElementBpmnId(inner, "Calc")
	ie := b.AddEndEvent()
	b.Connect(is, inner)
	b.Connect(inner, ie)
	b.PopScope()
	end := b.AddEndEvent()
	b.Connect(start, sub)
	b.Connect(sub, end)
	cp, err := b.Build()
	if err != nil {
		t.Fatal(err)
	}
	w.cp = cp
	w.p.Deploy(cp)
	w.p.CreateInstance(cp.Key)
	w.run()
	pi := model.NewKey(1, 1) // the partition's first key: the instance, which has completed
	var top BodiesResponse
	w.get(instPath(pi), nil, &top)
	outer := top.Bodies[0].BodyElementInstanceKey
	var rounds IterationsResponse
	w.get(iterPath(pi, outer), nil, &rounds)
	first := rounds.Iterations[0].ElementInstanceKey
	var steps IterationStepsResponse
	w.get(iterPath(pi, outer)+"/"+strconv.FormatUint(first, 10)+"/steps", nil, &steps)
	var bodyKey uint64
	var bodyEnd uint64
	for _, s := range steps.Steps {
		if s.MultiInstanceBody && s.Action == "activated" {
			bodyKey = s.ElementInstanceKey
		}
		if s.MultiInstanceBody && s.Action == "completed" {
			bodyEnd = s.Position
		}
	}
	if bodyKey == 0 || bodyEnd == 0 {
		t.Fatalf("no nested body in the window: %+v", steps.Steps)
	}
	var during, after IterationVariablesResponse
	w.get(varsPath(pi, outer, first), url.Values{"at": {strconv.FormatUint(bodyEnd-1, 10)}}, &during)
	w.get(varsPath(pi, outer, first), url.Values{"at": {strconv.FormatUint(bodyEnd, 10)}}, &after)
	var nested *StepScope
	for i, sc := range during.Scopes {
		if sc.ElementInstanceKey == bodyKey {
			nested = &during.Scopes[i]
		}
	}
	if nested == nil || nested.ElementID != "Calc" || nested.Iteration || scopeValues(*nested)["results"] == "" {
		t.Fatalf("the nested body's scope while it ran: %+v", during.Scopes)
	}
	for _, sc := range after.Scopes {
		if sc.ElementInstanceKey == bodyKey {
			t.Fatalf("the nested body's scope after it ended: %+v", after.Scopes)
		}
	}
}
