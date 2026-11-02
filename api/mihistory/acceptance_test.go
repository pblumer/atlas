package mihistory_test

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/pblumer/atlas/api"
	"github.com/pblumer/atlas/compiler"
	"github.com/pblumer/atlas/engine"
	"github.com/pblumer/atlas/state"
	"github.com/pblumer/atlas/wal"
)

// mi50kBPMN is the acceptance fixture of ADR-0517 / Issue #1481: a parallel
// multi-instance subprocess of 50,000 iterations between two ordinary steps.
// Every 5,000th iteration reaches a gateway none of whose conditions holds, so
// ten iterations park with a real, engine-raised incident and the rest
// complete — the instance stays active on those ten.
const mi50kBPMN = `<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL" xmlns:zeebe="http://camunda.org/schema/zeebe/1.0" id="defs">
  <process id="mi50k" isExecutable="true">
    <startEvent id="start"/>
    <scriptTask id="calculate" name="Calculate positions">
      <extensionElements>
        <zeebe:script resultVariable="position"
          expression="= if modulo(loopCounter, 5000) = 0 then string join(for i in 1..110000 return &quot;0123456789&quot;) else loopCounter * 2"/>
      </extensionElements>
      <multiInstanceLoopCharacteristics isSequential="false"><loopCardinality>%d</loopCardinality></multiInstanceLoopCharacteristics>
    </scriptTask>
    <endEvent id="end"/>
    <sequenceFlow id="f1" sourceRef="start" targetRef="calculate"/>
    <sequenceFlow id="f2" sourceRef="calculate" targetRef="end"/>
  </process>
</definitions>`

type acceptanceServer struct {
	t      *testing.T
	dir    string
	ts     *httptest.Server
	srv    *api.Server
	log    *wal.Log
	store  *state.Store
	closed bool
	// deployed is every definition deployed through the API, so a WAL replay
	// can deploy the same compiled processes under the same keys.
	deployed []deployment
}

type deployment struct {
	key uint64
	xml string
}

func newAcceptanceServer(t *testing.T) *acceptanceServer {
	t.Helper()
	dir := t.TempDir()
	log, err := wal.Open(wal.Options{Dir: filepath.Join(dir, "wal")})
	if err != nil {
		t.Fatal(err)
	}
	store, err := state.Open(filepath.Join(dir, "state"))
	if err != nil {
		t.Fatal(err)
	}
	proc := engine.New(1, log, store, nil)
	if err := proc.Recover(); err != nil {
		t.Fatal(err)
	}
	srv, err := api.New(proc, store, dir)
	if err != nil {
		t.Fatal(err)
	}
	a := &acceptanceServer{t: t, dir: dir, ts: httptest.NewServer(srv.Handler()), srv: srv, log: log, store: store}
	t.Cleanup(a.shutdown)
	return a
}

func (a *acceptanceServer) shutdown() {
	if a.closed {
		return
	}
	a.closed = true
	a.ts.Close()
	a.srv.Close()
	_ = a.store.Close()
	_ = a.log.Close()
}

func (a *acceptanceServer) deploy(xml string) uint64 {
	a.t.Helper()
	code, body, _ := a.do(http.MethodPost, "/api/v1/deployments", xml)
	if code != http.StatusOK {
		a.t.Fatalf("deploy: %d %s", code, body)
	}
	var dep struct {
		Key uint64 `json:"key"`
	}
	if err := json.Unmarshal(body, &dep); err != nil {
		a.t.Fatal(err)
	}
	a.deployed = append(a.deployed, deployment{dep.Key, xml})
	return dep.Key
}

// replay recovers the server's WAL into a brand-new state store, deploying the
// same definitions, and reports how long recovery took.
func (a *acceptanceServer) replay() (*state.Store, time.Duration) {
	a.t.Helper()
	a.shutdown()
	log, err := wal.Open(wal.Options{Dir: filepath.Join(a.dir, "wal")})
	if err != nil {
		a.t.Fatal(err)
	}
	a.t.Cleanup(func() { _ = log.Close() })
	fresh, err := state.Open(filepath.Join(a.t.TempDir(), "state"))
	if err != nil {
		a.t.Fatal(err)
	}
	a.t.Cleanup(func() { _ = fresh.Close() })
	p := engine.New(1, log, fresh, nil)
	for _, d := range a.deployed {
		cp, err := compiler.Parse(d.key, 1, strings.NewReader(d.xml))
		if err != nil {
			a.t.Fatal(err)
		}
		p.Deploy(cp)
	}
	started := time.Now()
	if err := p.Recover(); err != nil {
		a.t.Fatal(err)
	}
	return fresh, time.Since(started)
}

// do issues a request and returns status, body and elapsed wall time.
func (a *acceptanceServer) do(method, path, body string) (int, []byte, time.Duration) {
	a.t.Helper()
	var r io.Reader
	if body != "" {
		r = strings.NewReader(body)
	}
	req, err := http.NewRequest(method, a.ts.URL+path, r)
	if err != nil {
		a.t.Fatal(err)
	}
	if strings.HasPrefix(body, "<") {
		req.Header.Set("Content-Type", "application/xml")
	} else if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	start := time.Now()
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		a.t.Fatal(err)
	}
	defer res.Body.Close()
	data, _ := io.ReadAll(res.Body)
	return res.StatusCode, data, time.Since(start)
}

func (a *acceptanceServer) getJSON(path string, out any) (int, time.Duration) {
	a.t.Helper()
	code, data, d := a.do(http.MethodGet, path, "")
	if code == http.StatusOK {
		if err := json.Unmarshal(data, out); err != nil {
			a.t.Fatalf("decode %s: %v", path, err)
		}
	} else {
		a.t.Logf("%s: %d %s", path, code, data)
	}
	return code, d
}

func (a *acceptanceServer) startInstance(iterations int) uint64 {
	a.t.Helper()
	key := a.deploy(fmt.Sprintf(mi50kBPMN, iterations))
	code, body, _ := a.do(http.MethodPost, fmt.Sprintf("/api/v1/processes/%d/instances", key), `{}`)
	if code != http.StatusOK {
		a.t.Fatalf("start: %d %s", code, body)
	}
	var inst struct {
		Key uint64 `json:"instanceKey"`
	}
	if err := json.Unmarshal(body, &inst); err != nil || inst.Key == 0 {
		a.t.Fatalf("start response %s: %v", body, err)
	}
	return inst.Key
}

// acceptanceRun holds what the 50,000-iteration acceptance measured, so the
// test can log it in one place for the programme's evidence.
type acceptanceRun struct {
	name  string
	bytes int
	took  time.Duration
}

// TestFiftyThousandIterationsAreBoundedEndToEnd is the OHS2 acceptance gate of
// ADR-0517 / Issue #1481 against the whole server: HTTP routing, the run loop,
// the off-loop read view and the event-derived index, on an instance with
// 50,000 parallel iterations, ten of them parked on real incidents.
//
// It asserts what must hold for the operator experience to scale — every read
// is bounded by its page, not by the iteration count; a jump to iteration
// 37,421 is a seek; the counters are exact; the single writer keeps serving
// commands under concurrent history reads; and a WAL replay into a fresh store
// rebuilds identical answers — and logs the measured latencies and sizes. The
// latency bounds are deliberately loose: they catch a full scan, not a slow CI
// machine.
func TestFiftyThousandIterationsAreBoundedEndToEnd(t *testing.T) {
	const n = 50_000
	a := newAcceptanceServer(t)
	var runs []acceptanceRun
	measure := func(name, path string, out any) {
		t.Helper()
		code, data, took := a.do(http.MethodGet, path, "")
		if code != http.StatusOK {
			t.Fatalf("%s: %d %s", name, code, data)
		}
		if err := json.Unmarshal(data, out); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		runs = append(runs, acceptanceRun{name, len(data), took})
	}
	t.Cleanup(func() {
		for _, r := range runs {
			t.Logf("%-46s %7d bytes %12v", r.name, r.bytes, r.took)
		}
	})

	small := a.startInstance(2_000)
	started := time.Now()
	pi := a.startInstance(n)
	t.Logf("start and run %d iterations: %v", n, time.Since(started))

	type body struct {
		BodyElementInstanceKey  uint64 `json:"bodyElementInstanceKey"`
		Mode                    string `json:"mode"`
		ElementID               string `json:"elementId"`
		Iterations              uint64 `json:"iterations"`
		Active                  uint64 `json:"active"`
		Completed               uint64 `json:"completed"`
		Terminated              uint64 `json:"terminated"`
		IncidentsRaised         uint64 `json:"incidentsRaised"`
		IterationsWithIncidents uint64 `json:"iterationsWithIncidents"`
		OpenIncidents           *int   `json:"openIncidents"`
		OpenIncidentsExact      *bool  `json:"openIncidentsExact"`
	}
	type iteration struct {
		ElementInstanceKey uint64  `json:"elementInstanceKey"`
		Iteration          *uint64 `json:"iteration"`
		State              string  `json:"state"`
		OpenIncidents      int     `json:"openIncidents"`
	}
	var bodies struct {
		Bodies []body `json:"bodies"`
	}
	measure("overview: bodies of the 50k instance", fmt.Sprintf("/api/v1/instances/%d/multi-instances", pi), &bodies)
	if len(bodies.Bodies) != 1 {
		t.Fatalf("bodies: %+v", bodies.Bodies)
	}
	b := bodies.Bodies[0]
	if b.Mode != "parallel" || b.ElementID != "calculate" || b.Iterations != n || b.Completed != n-10 ||
		b.Active != 10 || b.Terminated != 0 || b.IncidentsRaised != 10 || b.IterationsWithIncidents != 10 {
		t.Fatalf("body summary: %+v", b)
	}
	var outline struct {
		Steps []struct {
			ElementID         string `json:"elementId"`
			MultiInstanceBody bool   `json:"multiInstanceBody"`
		} `json:"steps"`
		NextCursor string `json:"nextCursor"`
	}
	measure("overview: outline of the 50k instance", fmt.Sprintf("/api/v1/instances/%d/outline", pi), &outline)
	if len(outline.Steps) > 10 || outline.NextCursor != "" {
		t.Fatalf("outline of the 50k instance holds %d steps", len(outline.Steps))
	}
	bodyRows := 0
	for _, st := range outline.Steps {
		if st.MultiInstanceBody {
			bodyRows++
		}
	}
	if bodyRows != 1 {
		t.Fatalf("outline body rows: %+v", outline.Steps)
	}
	var smallBodies struct {
		Bodies []body `json:"bodies"`
	}
	measure("overview: bodies of a 2k instance", fmt.Sprintf("/api/v1/instances/%d/multi-instances", small), &smallBodies)

	base := fmt.Sprintf("/api/v1/instances/%d/multi-instances/%d", pi, b.BodyElementInstanceKey)
	var detail struct {
		Body body `json:"body"`
	}
	measure("body detail with open incidents", base, &detail)
	if detail.Body.OpenIncidents == nil || *detail.Body.OpenIncidents != 10 || !*detail.Body.OpenIncidentsExact {
		t.Fatalf("open incidents: %+v", detail.Body)
	}

	type page struct {
		Iterations []iteration `json:"iterations"`
		NextCursor string      `json:"nextCursor"`
	}
	first := func(p page) uint64 {
		if len(p.Iterations) == 0 || p.Iterations[0].Iteration == nil {
			t.Fatalf("empty page: %+v", p)
		}
		return *p.Iterations[0].Iteration
	}
	var p1 page
	measure("first page (100)", base+"/iterations", &p1)
	if len(p1.Iterations) != 100 || first(p1) != 1 || p1.NextCursor == "" {
		t.Fatalf("first page: %d rows from %d", len(p1.Iterations), first(p1))
	}
	var p2 page
	measure("second page via cursor", base+"/iterations?cursor="+p1.NextCursor, &p2)
	if first(p2) != 101 {
		t.Fatalf("second page starts at %d", first(p2))
	}
	var jump page
	measure("jump to iteration 37,421", base+"/iterations?from=37421", &jump)
	if len(jump.Iterations) != 100 || first(jump) != 37421 {
		t.Fatalf("jump: %d rows from %d", len(jump.Iterations), first(jump))
	}
	var exact page
	measure("exact iteration 37,421", base+"/iterations?iteration=37421", &exact)
	if len(exact.Iterations) != 1 || first(exact) != 37421 || exact.Iterations[0].State != "completed" {
		t.Fatalf("exact: %+v", exact.Iterations)
	}
	var byKey page
	measure("exact element-instance key", fmt.Sprintf("%s/iterations?elementInstanceKey=%d", base, exact.Iterations[0].ElementInstanceKey), &byKey)
	if len(byKey.Iterations) != 1 || first(byKey) != 37421 {
		t.Fatalf("by key: %+v", byKey.Iterations)
	}
	var last page
	measure("last page", base+"/iterations?from=49901", &last)
	if len(last.Iterations) != 100 || first(last) != 49901 || last.NextCursor != "" {
		t.Fatalf("last page: %d rows from %d next=%q", len(last.Iterations), first(last), last.NextCursor)
	}
	var withInc page
	measure("iterations with incidents", base+"/iterations?incidents=true", &withInc)
	if len(withInc.Iterations) != 10 || first(withInc) != 5000 || withInc.NextCursor != "" {
		t.Fatalf("incident iterations: %+v", withInc.Iterations)
	}
	for i, it := range withInc.Iterations {
		if *it.Iteration != uint64(5000*(i+1)) || it.OpenIncidents != 1 || it.State != "active" {
			t.Fatalf("incident iteration %d: %+v", i, it)
		}
	}
	var active page
	measure("state=active", base+"/iterations?state=active", &active)
	if len(active.Iterations) != 10 {
		t.Fatalf("active iterations: %d", len(active.Iterations))
	}
	var one struct {
		Iteration iteration `json:"iteration"`
		Incidents []struct {
			Status string `json:"status"`
		} `json:"incidents"`
	}
	measure("iteration detail", fmt.Sprintf("%s/iterations/%d", base, withInc.Iterations[3].ElementInstanceKey), &one)
	if *one.Iteration.Iteration != 20000 || len(one.Incidents) != 1 || one.Incidents[0].Status != "open" {
		t.Fatalf("iteration 20,000: %+v", one)
	}
	var window struct {
		Steps []struct {
			Action                 string `json:"action"`
			MultiInstanceIteration bool   `json:"multiInstanceIteration"`
		} `json:"steps"`
		NextCursor string `json:"nextCursor"`
	}
	measure("replay window of iteration 37,421", fmt.Sprintf("%s/iterations/%d/steps", base, exact.Iterations[0].ElementInstanceKey), &window)
	if len(window.Steps) != 2 || window.Steps[0].Action != "activated" || window.Steps[1].Action != "completed" ||
		!window.Steps[0].MultiInstanceIteration || window.NextCursor != "" {
		t.Fatalf("replay window of iteration 37,421: %+v", window)
	}

	// The overview must not grow with the iteration count: against the
	// 2,000-iteration instance it is the same size, give or take the digits of
	// larger numbers and the words for different states (one instance is still
	// active, the other completed). 25 times the iterations may not cost more
	// than those few bytes, and neither overview may approach a kilobyte.
	if d := runs[0].bytes - runs[2].bytes; d < -64 || d > 64 || runs[0].bytes > 1024 {
		t.Fatalf("overview size depends on the iteration count: %d vs %d bytes", runs[0].bytes, runs[2].bytes)
	}
	for _, r := range runs {
		if r.bytes > 64<<10 {
			t.Fatalf("%s returned %d bytes", r.name, r.bytes)
		}
		if r.took > 2*time.Second {
			t.Fatalf("%s took %v: a bounded read does not take seconds", r.name, r.took)
		}
	}

	// Bounded work, not only bounded responses: the seek to 37,421 examines
	// exactly the rows it returns, and the state filter examines only the ten
	// active iterations, not the 49,990 others.
	rv := a.store.ReadView()
	jumpPage, err := rv.MIIterationPage(pi, b.BodyElementInstanceKey, bodyPosition(t, rv, pi, b.BodyElementInstanceKey),
		state.MIIterationQuery{From: 37421, Limit: 100}, func(state.MIHistoryEntry) error { return nil })
	if err != nil || jumpPage.Scanned != 100 {
		t.Fatalf("jump examined %d index keys (err %v)", jumpPage.Scanned, err)
	}
	activePage, err := rv.MIIterationPage(pi, b.BodyElementInstanceKey, bodyPosition(t, rv, pi, b.BodyElementInstanceKey),
		state.MIIterationQuery{State: state.MIHistoryActive, Limit: 100}, func(state.MIHistoryEntry) error { return nil })
	if err != nil || activePage.Scanned != 10 {
		t.Fatalf("state filter examined %d index keys (err %v)", activePage.Scanned, err)
	}
	rv.Close()

	assertWriterResponsive(t, a, base)

	// The legacy full timeline of this instance is deliberately not requested:
	// it grows with the square of the iteration count (measured on 2026-11-02:
	// 139 MB at 1,000 iterations, 2.2 GB and 59 s on the writer at 4,000), and at
	// 50,000 the test process was killed for memory while serving it.

	// Recovery: the same answers from a WAL replay into a fresh store.
	live := miAnswers(t, a.store, pi, b.BodyElementInstanceKey)
	fresh, recovery := a.replay()
	t.Logf("WAL replay of the whole log into a fresh store: %v", recovery)
	if got := miAnswers(t, fresh, pi, b.BodyElementInstanceKey); !reflect.DeepEqual(got, live) {
		t.Fatalf("MI history after WAL replay differs from live")
	}
}

// miAnswers reads the body and a spread of iteration pages straight from a store.
func miAnswers(t *testing.T, s *state.Store, pi, bodyKey uint64) []state.MIHistoryEntry {
	t.Helper()
	body, ok, err := s.MIBody(pi, bodyKey)
	if err != nil || !ok {
		t.Fatalf("body: %v %v", ok, err)
	}
	out := []state.MIHistoryEntry{*body}
	// An iteration's steps, as entries: its activation and end positions.
	for _, n := range []uint64{1, 37421, 50000} {
		if _, err := s.MIIterationPage(pi, bodyKey, body.ActivationPosition, state.MIIterationQuery{From: n, Limit: 1}, func(e state.MIHistoryEntry) error {
			_, err := s.IterationStepsPage(pi, e.ElementInstanceKey, nil, 0, state.MIMaxPageLimit, func(o state.OutlineStep) error {
				out = append(out, state.MIHistoryEntry{ElementInstanceKey: o.ElementInstanceKey, ActivationPosition: o.Position, LoopCounter: uint64(o.Action)})
				return nil
			})
			return err
		}); err != nil {
			t.Fatal(err)
		}
	}
	for _, q := range []state.MIIterationQuery{
		{Limit: 200}, {From: 37421, Limit: 200}, {From: 49901, Limit: 200},
		{IncidentsOnly: true, Limit: 200}, {State: state.MIHistoryActive, Limit: 200},
	} {
		if _, err := s.MIIterationPage(pi, bodyKey, body.ActivationPosition, q, func(e state.MIHistoryEntry) error {
			out = append(out, e)
			return nil
		}); err != nil {
			t.Fatal(err)
		}
	}
	return out
}

func bodyPosition(t *testing.T, rv *state.ReadView, pi, body uint64) uint64 {
	t.Helper()
	e, ok, err := rv.MIBody(pi, body)
	if err != nil || !ok {
		t.Fatalf("body %d: %v %v", body, ok, err)
	}
	return e.ActivationPosition
}

// assertWriterResponsive starts small instances — commands that need the single
// writer — while eight clients read MI history pages as fast as they can, and
// compares their latency to the same commands without readers.
func assertWriterResponsive(t *testing.T, a *acceptanceServer, base string) {
	t.Helper()
	trivial := a.deploy(trivialBPMN)
	startOne := func() time.Duration {
		code, body, took := a.do(http.MethodPost, fmt.Sprintf("/api/v1/processes/%d/instances", trivial), `{}`)
		if code != http.StatusOK {
			t.Fatalf("start trivial: %d %s", code, body)
		}
		return took
	}
	worst := func(k int) time.Duration {
		var max time.Duration
		for i := 0; i < k; i++ {
			if d := startOne(); d > max {
				max = d
			}
		}
		return max
	}
	baseline := worst(20)
	stop := make(chan struct{})
	done := make(chan int)
	for r := 0; r < 8; r++ {
		go func(r int) {
			reads := 0
			for {
				select {
				case <-stop:
					done <- reads
					return
				default:
				}
				res, err := http.Get(a.ts.URL + base + fmt.Sprintf("/iterations?from=%d", 1+(reads*7919+r*4999)%49900))
				if err == nil {
					_, _ = io.Copy(io.Discard, res.Body)
					res.Body.Close()
				}
				reads++
			}
		}(r)
	}
	loaded := worst(20)
	close(stop)
	total := 0
	for r := 0; r < 8; r++ {
		total += <-done
	}
	t.Logf("writer: worst start-instance latency %v alone, %v under %d concurrent MI page reads", baseline, loaded, total)
	if loaded > 2*time.Second {
		t.Fatalf("a command waited %v behind MI history reads", loaded)
	}
}

const trivialBPMN = `<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL" id="t">
  <process id="trivial" isExecutable="true">
    <startEvent id="s"/><endEvent id="e"/>
    <sequenceFlow id="f" sourceRef="s" targetRef="e"/>
  </process>
</definitions>`
