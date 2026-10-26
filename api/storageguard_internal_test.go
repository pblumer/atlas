package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/pblumer/atlas/engine"
	"github.com/pblumer/atlas/internal/diskspace"
	"github.com/pblumer/atlas/state"
	"github.com/pblumer/atlas/wal"
)

// scriptedDisk is a file system whose free space a test sets. Atomic because the
// monitor's own goroutine reads it on its cadence while the test changes it.
type scriptedDisk struct{ available atomic.Uint64 }

func (d *scriptedDisk) read(string) (diskspace.Usage, error) {
	a := d.available.Load()
	return diskspace.Usage{Total: 20 << 30, Free: a, Available: a, Volume: "scripted"}, nil
}

// guardedServer boots a server over a scripted disk with a 1 GiB reserve, and hands
// back the log so a test can make the disk stop taking writes.
func guardedServer(t *testing.T, opts ...Option) (*Server, *scriptedDisk, *wal.Log) {
	t.Helper()
	dir := t.TempDir()
	log, err := wal.Open(wal.Options{Dir: filepath.Join(dir, "wal")})
	if err != nil {
		t.Fatalf("wal.Open: %v", err)
	}
	store, err := state.Open(filepath.Join(dir, "state"))
	if err != nil {
		t.Fatalf("state.Open: %v", err)
	}
	proc := engine.New(1, log, store, nil)
	if err := proc.Recover(); err != nil {
		t.Fatalf("Recover: %v", err)
	}
	disk := &scriptedDisk{}
	disk.available.Store(10 << 30)
	opts = append([]Option{WithStorageReserve(1 << 30), withStorageReader(disk.read)}, opts...)
	srv, err := New(proc, store, dir, opts...)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(func() {
		srv.Close()
		_ = store.Close()
		_ = log.Close()
	})
	srv.storage.Sample()
	return srv, disk, log
}

func httpRequest(method, path, body string) *http.Request {
	r := httptest.NewRequest(method, path, strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	return r
}

func serveRecorded(srv *Server, r *http.Request) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, r)
	return rec
}

func liveInstances(t *testing.T, srv *Server) float64 {
	t.Helper()
	code, raw := serveInternal(t, srv, http.MethodGet, "/api/v1/stats", "", "")
	if code != http.StatusOK {
		t.Fatalf("stats: status=%d body=%s", code, raw)
	}
	var stats struct {
		Active float64 `json:"activeProcessInstances"`
	}
	if err := json.Unmarshal(raw, &stats); err != nil {
		t.Fatalf("decode stats: %v", err)
	}
	return stats.Active
}

// TestAFrozenServerRefusesNewWorkAndFinishesWhatRuns is the freeze end to end: below
// the reserve a new instance is refused before it reaches the engine, a worker leases
// nothing new, a job already leased may still report back, reads go on — and once the
// reserve and its margin are free the server takes writes again by itself.
func TestAFrozenServerRefusesNewWorkAndFinishesWhatRuns(t *testing.T) {
	srv, disk, _ := guardedServer(t)
	code, raw := serveInternal(t, srv, http.MethodPost, "/api/v1/deployments", serviceJobBPMN, "application/xml")
	if code != http.StatusOK && code != http.StatusCreated {
		t.Fatalf("deploy: status=%d body=%s", code, raw)
	}
	var deployed struct {
		Key uint64 `json:"key"`
	}
	if err := json.Unmarshal(raw, &deployed); err != nil {
		t.Fatalf("decode deploy: %v (%s)", err, raw)
	}
	start := fmt.Sprintf("/api/v1/processes/%d/instances", deployed.Key)
	if code, raw := serveInternal(t, srv, http.MethodPost, start, "{}", "application/json"); code != http.StatusOK {
		t.Fatalf("create before the freeze: status=%d body=%s", code, raw)
	}
	job := lease(t, srv, "payment")

	disk.available.Store(512 << 20)
	srv.storage.Sample()

	r := httpRequest(http.MethodPost, start, "{}")
	rec := serveRecorded(srv, r)
	if rec.Code != http.StatusInsufficientStorage {
		t.Fatalf("create while frozen: status=%d body=%s, want 507", rec.Code, rec.Body)
	}
	if rec.Header().Get("Retry-After") == "" {
		t.Error("a 507 without Retry-After: the caller is not told it may try again")
	}
	if n := liveInstances(t, srv); n != 1 {
		t.Fatalf("%v active instances while frozen, want the 1 created before: the refused one must not exist", n)
	}
	if code, raw := serveInternal(t, srv, http.MethodPost, "/api/v1/jobs/activate",
		`{"type":"payment","worker":"w1","leaseMs":60000}`, "application/json"); code != http.StatusInsufficientStorage {
		t.Fatalf("lease while frozen: status=%d body=%s, want 507", code, raw)
	}
	body := fmt.Sprintf(`{"worker":"w1","leaseToken":%d}`, job.LeaseToken)
	if code, raw := completeJob(t, srv, job, body); code != http.StatusOK && code != http.StatusNoContent {
		t.Fatalf("completing a job leased before the freeze: status=%d body=%s; its work is done and must be reported", code, raw)
	}
	if n := liveInstances(t, srv); n != 0 {
		t.Fatalf("%v active instances after the completion, want 0", n)
	}

	// Above the reserve but inside the margin: still frozen.
	disk.available.Store(1100 << 20)
	srv.storage.Sample()
	if code, _ := serveInternal(t, srv, http.MethodPost, start, "{}", "application/json"); code != http.StatusInsufficientStorage {
		t.Fatalf("create just above the reserve: status=%d, want 507 until the margin is free too", code)
	}

	disk.available.Store(2 << 30)
	srv.storage.Sample()
	if code, raw := serveInternal(t, srv, http.MethodPost, start, "{}", "application/json"); code != http.StatusOK {
		t.Fatalf("create after space came back: status=%d body=%s", code, raw)
	}
	if n := liveInstances(t, srv); n != 1 {
		t.Fatalf("%v active instances after the thaw, want 1", n)
	}
}

// TestAHaltedEngineRefusesWritesUntilRestarted: once a batch could not be made
// durable, every write is refused up front with 503 — even a job report, which the
// engine could not record anyway — and reads still answer.
func TestAHaltedEngineRefusesWritesUntilRestarted(t *testing.T) {
	srv, _, log := guardedServer(t)
	code, raw := serveInternal(t, srv, http.MethodPost, "/api/v1/deployments", serviceJobBPMN, "application/xml")
	if code != http.StatusOK && code != http.StatusCreated {
		t.Fatalf("deploy: status=%d body=%s", code, raw)
	}
	var deployed struct {
		Key uint64 `json:"key"`
	}
	if err := json.Unmarshal(raw, &deployed); err != nil {
		t.Fatalf("decode deploy: %v", err)
	}
	start := fmt.Sprintf("/api/v1/processes/%d/instances", deployed.Key)

	// The disk stops taking writes.
	if err := log.Close(); err != nil {
		t.Fatalf("log.Close: %v", err)
	}
	if code, _ := serveInternal(t, srv, http.MethodPost, start, "{}", "application/json"); code == http.StatusOK {
		t.Fatal("an instance was acknowledged although its batch could not be written")
	}
	if srv.proc.Halted() == nil {
		t.Fatal("the engine did not halt after a failed write")
	}
	if code, raw := serveInternal(t, srv, http.MethodPost, start, "{}", "application/json"); code != http.StatusServiceUnavailable {
		t.Fatalf("create on a halted engine: status=%d body=%s, want 503", code, raw)
	}
	if code, raw := serveInternal(t, srv, http.MethodPost, "/api/v1/jobs/1/complete", "{}", "application/json"); code != http.StatusServiceUnavailable {
		t.Fatalf("a job report on a halted engine: status=%d body=%s, want 503", code, raw)
	}
	if n := liveInstances(t, srv); n != 0 {
		t.Fatalf("%v active instances, want 0", n)
	}
	if !srv.writesPaused() {
		t.Fatal("background work is not paused on a halted engine")
	}
}

// TestTheTimerSchedulerWaitsOutAFreeze: a due timer is new work, so it does not fire
// while the disk is below the reserve — and fires on the first tick after.
func TestTheTimerSchedulerWaitsOutAFreeze(t *testing.T) {
	ticks := make(chan time.Time)
	srv, disk, _ := guardedServer(t, withTimerTrigger(ticks))
	code, raw := serveInternal(t, srv, http.MethodPost, "/api/v1/deployments", dueTimerBPMN, "application/xml")
	if code != http.StatusOK && code != http.StatusCreated {
		t.Fatalf("deploy: status=%d body=%s", code, raw)
	}
	var deployed struct {
		Key uint64 `json:"key"`
	}
	if err := json.Unmarshal(raw, &deployed); err != nil {
		t.Fatalf("decode deploy: %v", err)
	}
	if code, raw := serveInternal(t, srv, http.MethodPost, fmt.Sprintf("/api/v1/processes/%d/instances", deployed.Key), "{}", "application/json"); code != http.StatusOK {
		t.Fatalf("create: status=%d body=%s", code, raw)
	}

	disk.available.Store(512 << 20)
	srv.storage.Sample()
	tick(t, ticks)
	if n := liveInstances(t, srv); n != 1 {
		t.Fatalf("%v active instances after a tick while frozen, want 1: the timer must wait", n)
	}

	disk.available.Store(2 << 30)
	srv.storage.Sample()
	tick(t, ticks)
	if n := liveInstances(t, srv); n != 0 {
		t.Fatalf("%v active instances after a tick with room, want 0: the timer is due and must fire", n)
	}
}

// tick hands the scheduler one tick and waits until it has taken the next, which it
// only does once the first round — fire and drive — is over.
func tick(t *testing.T, ticks chan<- time.Time) {
	t.Helper()
	for i := 0; i < 2; i++ {
		select {
		case ticks <- time.Now():
		case <-time.After(10 * time.Second):
			t.Fatal("the timer scheduler did not take a tick")
		}
	}
}

// dueTimerBPMN waits on a timer that is already due, then ends.
const dueTimerBPMN = `<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL">
  <process id="waits" isExecutable="true">
    <startEvent id="start"/>
    <intermediateCatchEvent id="wait">
      <timerEventDefinition><timeDuration>PT0S</timeDuration></timerEventDefinition>
    </intermediateCatchEvent>
    <endEvent id="end"/>
    <sequenceFlow id="f1" sourceRef="start" targetRef="wait"/>
    <sequenceFlow id="f2" sourceRef="wait" targetRef="end"/>
  </process>
</definitions>`

// TestEveryWriteAFrozenServerTakesIsARoute: the allowlist names routes by the pattern
// they are mounted under, so a renamed route would silently fall off it and be
// refused. Each entry must be a pattern this server actually serves.
func TestEveryWriteAFrozenServerTakesIsARoute(t *testing.T) {
	srv, _, _ := guardedServer(t)
	_, policy := srv.mountRoutes()
	for pattern := range writesWhileFrozen {
		if pattern == "/mcp" || pattern == "/mcp/" {
			continue // mounted only when the binary supplies the MCP transport
		}
		if _, ok := policy.class[pattern]; !ok {
			t.Errorf("writesWhileFrozen names %q, which no route is mounted under", pattern)
		}
	}
}

// TestTheStorageSurfacesFollowTheMonitor: the route and the gauges report the reading
// the freeze was decided on, so a dashboard, an agent and an alert rule all see the
// same disk.
func TestTheStorageSurfacesFollowTheMonitor(t *testing.T) {
	srv, disk, _ := guardedServer(t)
	scrapeIt := func() string {
		t.Helper()
		code, raw := serveInternal(t, srv, http.MethodGet, "/metrics", "", "")
		if code != http.StatusOK {
			t.Fatalf("GET /metrics: status=%d", code)
		}
		return string(raw)
	}
	expo := scrapeIt()
	for name, want := range map[string]float64{
		"atlas_storage_reserve_bytes":   1 << 30,
		"atlas_storage_capacity_bytes":  20 << 30,
		"atlas_storage_available_bytes": 10 << 30,
		"atlas_storage_frozen":          0,
		"atlas_engine_halted":           0,
	} {
		if got := sampleValue(t, expo, name); got != want {
			t.Errorf("%s = %v, want %v", name, got, want)
		}
	}
	if hasMetric(expo, "atlas_storage_consumption_bytes_per_second") {
		t.Error("a consumption rate before an hour of observation; it must be absent, not a guess")
	}

	disk.available.Store(512 << 20)
	srv.storage.Sample()
	if got := sampleValue(t, scrapeIt(), "atlas_storage_frozen"); got != 1 {
		t.Errorf("atlas_storage_frozen = %v below the reserve, want 1", got)
	}
	code, raw := serveInternal(t, srv, http.MethodGet, "/api/v1/storage", "", "")
	if code != http.StatusOK {
		t.Fatalf("GET /api/v1/storage: status=%d body=%s", code, raw)
	}
	var snap struct {
		State        string `json:"state"`
		ReserveBytes uint64 `json:"reserveBytes"`
		FileSystems  []struct {
			Paths          []string `json:"paths"`
			AvailableBytes uint64   `json:"availableBytes"`
		} `json:"fileSystems"`
		Atlas struct {
			WALSegments int `json:"walSegments"`
		} `json:"atlas"`
	}
	if err := json.Unmarshal(raw, &snap); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if snap.State != "frozen" || snap.ReserveBytes != 1<<30 {
		t.Fatalf("storage = %s, want frozen with a 1 GiB reserve", raw)
	}
	if len(snap.FileSystems) != 1 || len(snap.FileSystems[0].Paths) != 3 || snap.FileSystems[0].AvailableBytes != 512<<20 {
		t.Fatalf("file systems = %+v, want the data directory, the WAL and the state store on one", snap.FileSystems)
	}
	if snap.Atlas.WALSegments == 0 {
		t.Fatalf("the WAL footprint is missing: %s", raw)
	}
}
