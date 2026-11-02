package mihistory

import (
	"encoding/binary"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

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

func TestUnavailableIndexIsAnExplainedConflictNotAnEmptyList(t *testing.T) {
	dir := t.TempDir()
	statePath := filepath.Join(dir, "state")
	w := &world{t: t, now: time.Unix(1_800_000_000, 0)}
	var err error
	if w.log, err = wal.Open(wal.Options{Dir: filepath.Join(dir, "wal")}); err != nil {
		t.Fatal(err)
	}
	defer w.log.Close()
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
	// Simulate a store written before the v2 index: remove its readiness marker.
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
	if err := db.Delete(append([]byte{0x00}, "mi_history_index_v2"...), pebble.Sync); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	if w.store, err = state.Open(statePath); err != nil {
		t.Fatal(err)
	}
	defer w.store.Close()
	w.svc = newService(w.view, []byte("k"), func() time.Time { return w.now })
	mux := http.NewServeMux()
	w.mount(mux, w.svc)
	w.srv = httptest.NewServer(mux)
	defer w.srv.Close()

	for _, path := range []string{instPath(pi), instPath(pi) + "/1", iterPath(pi, 1), iterPath(pi, 1) + "/2"} {
		var ignored any
		status, e := w.get(path, nil, &ignored)
		if status != http.StatusConflict || e["code"] != "history_unavailable" || !strings.Contains(e["error"], "does not mean no iterations ran") {
			t.Fatalf("%s: %d %v", path, status, e)
		}
	}
}

func TestBodiesListTopLevelAndNestedExecutionsSeparately(t *testing.T) {
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
	pi := w.start()

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
