package api

import (
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"net/http/httptest"
	"runtime"
	"runtime/metrics"
	"strings"
	"testing"
)

// Whether, and where, the server is slow (ADR-0502): the figures read
// before a profile is worth taking, measured over a window rather than since the start.

func readRuntimeHealth(t *testing.T, h http.Handler, query string) runtimeHealthResp {
	t.Helper()
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/debug/runtime"+query, nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("runtime health%s: status=%d body=%s", query, rec.Code, rec.Body.Bytes())
	}
	var resp runtimeHealthResp
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("runtime health%s: %v", query, err)
	}
	return resp
}

// TestRuntimeHealthMeasuresTheWindow: turns dispatched and instances started during the
// window are what the run loop and the writer report, alongside the runtime's figures.
func TestRuntimeHealthMeasuresTheWindow(t *testing.T) {
	srv := newServerForErrors(t)
	defKey := forkPathsDeploy(t, srv, processStatsBPMN)
	h := srv.Handler()

	stop, done := make(chan struct{}), make(chan struct{})
	go func() {
		defer close(done)
		for {
			select {
			case <-stop:
				return
			default:
			}
			rec := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodPost, fmt.Sprintf("/api/v1/processes/%d/instances", defKey), strings.NewReader(`{}`))
			req.Header.Set("Content-Type", "application/json")
			h.ServeHTTP(rec, req)
			srv.do(func() {})
		}
	}()
	resp := readRuntimeHealth(t, h, "?seconds=1")
	close(stop)
	<-done

	if resp.WindowSeconds != 1 || resp.Go != runtime.Version() || resp.GOMAXPROCS != uint64(runtime.GOMAXPROCS(0)) {
		t.Errorf("window %d, go %q, gomaxprocs %d", resp.WindowSeconds, resp.Go, resp.GOMAXPROCS)
	}
	if resp.Goroutines.Total == 0 || resp.Memory.HeapLiveBytes == 0 || resp.Memory.TotalMappedBytes == 0 {
		t.Errorf("runtime figures missing: goroutines %+v memory %+v", resp.Goroutines, resp.Memory)
	}
	if resp.RunLoop == nil || resp.Writer == nil {
		t.Fatalf("a server with metrics reports no run loop or writer: %+v", resp)
	}
	// The loop observes a turn's wait and then its hold, and a snapshot reads them in the
	// same order without stopping the loop, so a turn can sit between the two at either
	// end of the window: the distributions agree to within one turn, not exactly.
	if d := int64(resp.RunLoop.Held.Count) - int64(resp.RunLoop.Wait.Count); resp.RunLoop.Turns == 0 ||
		resp.RunLoop.Wait.Count != resp.RunLoop.Turns || d < -1 || d > 1 {
		t.Errorf("run loop: %+v, want the turns of the window counted once in each distribution, give or take the one turn a snapshot can fall inside", resp.RunLoop)
	}
	if resp.RunLoop.TurnsPerSecond <= 0 || resp.RunLoop.Held.MeanMillis == nil || resp.RunLoop.Held.P99Millis < resp.RunLoop.Held.P50Millis {
		t.Errorf("run loop distributions: %+v", resp.RunLoop)
	}
	if resp.Writer.CommandsPerSecond <= 0 || resp.Writer.BatchesPerSecond <= 0 {
		t.Errorf("writer: %+v, want the instances started in the window", resp.Writer)
	}
	if resp.Scheduler.Count == 0 {
		t.Errorf("no scheduling latency recorded in a window that ran goroutines")
	}
	if resp.Note == "" {
		t.Error("the answer does not say how to read it")
	}
	// No GOMEMLIMIT in a test binary: the limit is absent rather than 2^63-1.
	if l := resp.Memory.MemoryLimitBytes; l != nil && *l >= math.MaxInt64 {
		t.Errorf("memoryLimitBytes = %d, want it absent without a limit", *l)
	}
}

// TestRuntimeHealthWithoutMetricsSaysWhatIsMissing: no window, no metrics — the runtime's
// figures at once, and a sentence saying why the run loop is not among them.
func TestRuntimeHealthWithoutMetricsSaysWhatIsMissing(t *testing.T) {
	mux, _ := accessTestServer(t).mountRoutes()
	resp := readRuntimeHealth(t, mux, "?seconds=0")
	if resp.RunLoop != nil || resp.Writer != nil {
		t.Errorf("a server that observes nothing reports a run loop %+v / writer %+v", resp.RunLoop, resp.Writer)
	}
	if !strings.Contains(resp.Note, "--metrics=false") {
		t.Errorf("the note does not say why the run loop is absent: %s", resp.Note)
	}
	if resp.WindowSeconds != 0 || resp.Goroutines.Total == 0 || resp.UptimeSeconds < 0 {
		t.Errorf("since-start answer: %+v", resp)
	}
}

// TestRuntimeHealthRefusesAWindowTheClientWouldNotWaitFor, and anything but a number.
func TestRuntimeHealthRefusesAWindowTheClientWouldNotWaitFor(t *testing.T) {
	mux, _ := accessTestServer(t).mountRoutes()
	for _, q := range []string{"?seconds=-1", "?seconds=21", "?seconds=x"} {
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/debug/runtime"+q, nil))
		if rec.Code != http.StatusBadRequest {
			t.Errorf("%s: status=%d, want 400", q, rec.Code)
		}
	}
}

// TestRuntimeHealthIsForAdministrators, like the profiles it comes before.
func TestRuntimeHealthIsForAdministrators(t *testing.T) {
	if got := boundaryStatus(t, []string{RoleModeler, RoleOperator, RoleUser}, http.MethodGet, "/api/v1/debug/runtime"); got != http.StatusForbidden {
		t.Errorf("runtime health without an admin principal: status=%d, want 403", got)
	}
}

// TestHealthDistributionsAreTheWindowsBuckets pins the arithmetic: what was observed before
// the window is taken off, and a percentile is the bound of the bucket it falls in.
func TestHealthDistributionsAreTheWindowsBuckets(t *testing.T) {
	bounds := []float64{0.001, 0.002, 0.004}
	before := promHistogram{count: 1, sum: 0.0005, bounds: bounds, cum: []uint64{1, 1, 1}}
	after := promHistogram{count: 11, sum: 0.0305, bounds: bounds, cum: []uint64{1, 6, 11}}
	d := promDistribution(&before, after)
	if d.Count != 10 || d.P50Millis != 2 || d.P90Millis != 4 || d.MaxMillis != 4 || d.MeanMillis == nil || *d.MeanMillis != 3 {
		t.Errorf("prometheus window: %+v (mean %v), want 10 observations, p50 2, p90 4, max 4, mean 3", d, d.MeanMillis)
	}

	rb := &metrics.Float64Histogram{Buckets: []float64{0, 0.001, 0.002, math.Inf(1)}, Counts: []uint64{1, 1, 0}}
	ra := &metrics.Float64Histogram{Buckets: rb.Buckets, Counts: []uint64{2, 3, 1}}
	r := runtimeDistribution(rb, ra)
	// The window holds 1 below 1 ms, 2 between 1 and 2 ms, and 1 beyond, whose bucket has
	// no upper bound: it reads as its lower one.
	if r.Count != 4 || r.P50Millis != 2 || r.MaxMillis != 2 || r.MeanMillis != nil {
		t.Errorf("runtime window: %+v, want 4 observations, p50 2, max 2 and no mean", r)
	}
}
