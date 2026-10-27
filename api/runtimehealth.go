package api

import (
	"fmt"
	"math"
	"net/http"
	"runtime"
	"runtime/metrics"
	"strconv"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"

	"github.com/pblumer/atlas/api/httpapi"
)

// Whether, and where, the server is slow (ADR-draft-admin-profiling).
//
// A profile says where the time goes. Before one is worth taking, somebody has to know
// whether the server is slow at all, and in which part: callers queueing for the single
// writer, the writer's turns themselves, the disk under it, goroutines waiting for a CPU,
// or the garbage collector. Those numbers exist — the run loop's and the writer's in the
// Prometheus registry, the runtime's in runtime/metrics — but only /metrics shows them,
// and that is for a scraper with a metrics token, not for an administrator or an agent
// asking now.
//
// So this route measures a window: it reads every figure, waits ?seconds=, reads them
// again and answers with what happened in between. A since-start average would hide the
// incident somebody is asking about under the hours before it.

const (
	runtimeHealthDefault = 5
	// runtimeHealthMax keeps a window inside the thirty seconds the MCP client waits.
	runtimeHealthMax = 20
)

// processStarted is close enough to when the process started: this package initialises
// before the server does anything.
var processStarted = time.Now()

// runtimeHealthMetrics are the runtime/metrics figures the answer reads. One a runtime
// does not know reads as KindBad and is left out rather than reported as zero.
var runtimeHealthMetrics = []string{
	"/sched/goroutines:goroutines",
	"/sched/goroutines/runnable:goroutines",
	"/sched/goroutines/running:goroutines",
	"/sched/goroutines/waiting:goroutines",
	"/sched/gomaxprocs:threads",
	"/sched/latencies:seconds",
	"/sched/pauses/total/gc:seconds",
	"/gc/cycles/total:gc-cycles",
	"/gc/heap/allocs:bytes",
	"/gc/heap/live:bytes",
	"/gc/heap/goal:bytes",
	"/gc/heap/objects:objects",
	"/gc/gogc:percent",
	"/gc/gomemlimit:bytes",
	"/memory/classes/total:bytes",
	"/cpu/classes/gc/total:cpu-seconds",
	"/cpu/classes/idle:cpu-seconds",
	"/cpu/classes/total:cpu-seconds",
}

// healthDistribution is a latency distribution over the window, in milliseconds. The
// percentiles are bucket bounds: p99 is "at most this", and a value at the top bucket's
// bound means at least that.
type healthDistribution struct {
	Count      uint64   `json:"count"`
	MeanMillis *float64 `json:"meanMillis,omitempty"`
	P50Millis  float64  `json:"p50Millis"`
	P90Millis  float64  `json:"p90Millis"`
	P99Millis  float64  `json:"p99Millis"`
	MaxMillis  float64  `json:"maxMillis"`
}

type healthGoroutines struct {
	Total    uint64  `json:"total"`
	Runnable *uint64 `json:"runnable,omitempty"`
	Running  *uint64 `json:"running,omitempty"`
	Waiting  *uint64 `json:"waiting,omitempty"`
}

type healthMemory struct {
	HeapLiveBytes    uint64 `json:"heapLiveBytes"`
	HeapGoalBytes    uint64 `json:"heapGoalBytes"`
	HeapObjects      uint64 `json:"heapObjects"`
	TotalMappedBytes uint64 `json:"totalMappedBytes"`
	GOGCPercent      uint64 `json:"gogcPercent"`
	// MemoryLimitBytes is the soft limit GOMEMLIMIT sets, absent without one.
	MemoryLimitBytes *uint64 `json:"memoryLimitBytes,omitempty"`
}

type healthGC struct {
	Cycles                  uint64             `json:"cycles"`
	Pause                   healthDistribution `json:"pause"`
	AllocatedBytesPerSecond float64            `json:"allocatedBytesPerSecond"`
	// CPUShareSinceStart is the share of the CPU the process used that went to the
	// collector, as of the last collection: the runtime updates its CPU figures only then.
	CPUShareSinceStart float64 `json:"cpuShareSinceStart"`
	// BusyShareSinceStart is how much of GOMAXPROCS the process used, on the same terms.
	BusyShareSinceStart float64 `json:"busyShareSinceStart"`
}

type healthRunLoop struct {
	Turns          uint64             `json:"turns"`
	TurnsPerSecond float64            `json:"turnsPerSecond"`
	Wait           healthDistribution `json:"wait"`
	Held           healthDistribution `json:"held"`
}

type healthWriter struct {
	BatchesPerSecond  float64            `json:"batchesPerSecond"`
	CommandsPerSecond float64            `json:"commandsPerSecond"`
	EventsPerSecond   float64            `json:"eventsPerSecond"`
	QueueDepth        float64            `json:"queueDepth"`
	Fsync             healthDistribution `json:"fsync"`
	Commit            healthDistribution `json:"commit"`
}

type runtimeHealthResp struct {
	Version       string             `json:"version"`
	Go            string             `json:"go"`
	UptimeSeconds int64              `json:"uptimeSeconds"`
	CPUs          int                `json:"cpus"`
	GOMAXPROCS    uint64             `json:"gomaxprocs"`
	WindowSeconds int                `json:"windowSeconds"`
	Goroutines    healthGoroutines   `json:"goroutines"`
	Scheduler     healthDistribution `json:"schedulerLatency"`
	Memory        healthMemory       `json:"memory"`
	GC            healthGC           `json:"gc"`
	RunLoop       *healthRunLoop     `json:"runLoop,omitempty"`
	Writer        *healthWriter      `json:"writer,omitempty"`
	Note          string             `json:"note"`
}

const runtimeHealthNote = "Distributions cover the window (since the server started when windowSeconds is 0); " +
	"their percentiles are bucket bounds, so p99Millis reads \"at most\". Rules of thumb, not limits: " +
	"runLoop.wait p99 well above held p99 means callers queue behind many short turns, a high held p99 means " +
	"one kind of turn is slow (take a CPU profile); writer.fsync p99 above ~50 ms is the disk; schedulerLatency " +
	"p99 above ~10 ms or runnable goroutines persistently above gomaxprocs means the CPU is saturated; " +
	"gc.cpuShareSinceStart above ~0.25 or many cycles a second means allocation pressure (take an allocs profile)."

// handleRuntimeHealth answers how the server is doing over a window of ?seconds=.
func (s *Server) handleRuntimeHealth(w http.ResponseWriter, r *http.Request) {
	seconds := runtimeHealthDefault
	if v := r.URL.Query().Get("seconds"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 0 || n > runtimeHealthMax {
			httpapi.Error(w, http.StatusBadRequest, fmt.Sprintf("seconds must be a whole number from 0 to %d", runtimeHealthMax))
			return
		}
		seconds = n
	}

	var before *healthSnapshot
	if seconds > 0 {
		before = s.healthSnapshot()
		timer := time.NewTimer(time.Duration(seconds) * time.Second)
		select {
		case <-timer.C:
		case <-r.Context().Done():
			timer.Stop()
			return // the client went away; there is nobody to answer
		}
	}
	after := s.healthSnapshot()
	httpapi.JSON(w, http.StatusOK, s.runtimeHealth(before, after, seconds))
}

// healthSnapshot is every figure the answer is made of, read at one moment.
type healthSnapshot struct {
	at       time.Time
	rt       map[string]metrics.Value
	runLoop  *[2]promHistogram // wait, held
	writer   *healthWriterSnapshot
	queueLen float64
}

type healthWriterSnapshot struct {
	batches, commands, events float64
	fsync, commit             promHistogram
}

// promHistogram is a Prometheus histogram's cumulative buckets.
type promHistogram struct {
	count  uint64
	sum    float64
	bounds []float64
	cum    []uint64
}

func (s *Server) healthSnapshot() *healthSnapshot {
	samples := make([]metrics.Sample, len(runtimeHealthMetrics))
	for i, name := range runtimeHealthMetrics {
		samples[i].Name = name
	}
	metrics.Read(samples)
	snap := &healthSnapshot{at: time.Now(), rt: make(map[string]metrics.Value, len(samples))}
	for _, sm := range samples {
		if sm.Value.Kind() != metrics.KindBad {
			snap.rt[sm.Name] = sm.Value
		}
	}
	if rl := s.runLoopStats; rl != nil {
		snap.runLoop = &[2]promHistogram{readPromHistogram(rl.waited), readPromHistogram(rl.held)}
	}
	if em := s.engineStats; em != nil {
		snap.writer = &healthWriterSnapshot{
			batches: readPromValue(em.batches), commands: readPromValue(em.commands), events: readPromValue(em.events),
			fsync: readPromHistogram(em.syncSeconds), commit: readPromHistogram(em.commitSeconds),
		}
		snap.queueLen = readPromValue(em.queueDepth)
	}
	return snap
}

func readPromHistogram(h prometheus.Histogram) promHistogram {
	var m dto.Metric
	if h.Write(&m) != nil || m.GetHistogram() == nil {
		return promHistogram{}
	}
	hh := m.GetHistogram()
	out := promHistogram{count: hh.GetSampleCount(), sum: hh.GetSampleSum()}
	for _, b := range hh.GetBucket() {
		out.bounds = append(out.bounds, b.GetUpperBound())
		out.cum = append(out.cum, b.GetCumulativeCount())
	}
	return out
}

func readPromValue(c prometheus.Metric) float64 {
	var m dto.Metric
	if c.Write(&m) != nil {
		return 0
	}
	if m.GetCounter() != nil {
		return m.GetCounter().GetValue()
	}
	return m.GetGauge().GetValue()
}

// runtimeHealth builds the answer from two snapshots, or from one since the start.
func (s *Server) runtimeHealth(before, after *healthSnapshot, seconds int) runtimeHealthResp {
	resp := runtimeHealthResp{
		Version: Version, Go: runtime.Version(), CPUs: runtime.NumCPU(),
		UptimeSeconds: int64(time.Since(processStarted).Seconds()),
		WindowSeconds: seconds, Note: runtimeHealthNote,
	}
	elapsed := 0.0
	if before != nil {
		elapsed = after.at.Sub(before.at).Seconds()
	} else {
		elapsed = time.Since(processStarted).Seconds()
	}
	rate := func(d float64) float64 {
		if elapsed <= 0 {
			return 0
		}
		return round1(d / elapsed)
	}
	u := func(name string) uint64 {
		if v, ok := after.rt[name]; ok && v.Kind() == metrics.KindUint64 {
			return v.Uint64()
		}
		return 0
	}
	uOpt := func(name string) *uint64 {
		if v, ok := after.rt[name]; ok && v.Kind() == metrics.KindUint64 {
			n := v.Uint64()
			return &n
		}
		return nil
	}
	delta := func(name string) uint64 {
		d := u(name)
		if before != nil {
			if v, ok := before.rt[name]; ok && v.Kind() == metrics.KindUint64 && v.Uint64() <= d {
				d -= v.Uint64()
			}
		}
		return d
	}
	hist := func(name string) healthDistribution {
		a, ok := after.rt[name]
		if !ok || a.Kind() != metrics.KindFloat64Histogram {
			return healthDistribution{}
		}
		var b *metrics.Float64Histogram
		if before != nil {
			if v, ok := before.rt[name]; ok && v.Kind() == metrics.KindFloat64Histogram {
				b = v.Float64Histogram()
			}
		}
		return runtimeDistribution(b, a.Float64Histogram())
	}
	f := func(name string) float64 {
		if v, ok := after.rt[name]; ok && v.Kind() == metrics.KindFloat64 {
			return v.Float64()
		}
		return 0
	}

	resp.GOMAXPROCS = u("/sched/gomaxprocs:threads")
	resp.Goroutines = healthGoroutines{
		Total:    u("/sched/goroutines:goroutines"),
		Runnable: uOpt("/sched/goroutines/runnable:goroutines"),
		Running:  uOpt("/sched/goroutines/running:goroutines"),
		Waiting:  uOpt("/sched/goroutines/waiting:goroutines"),
	}
	if resp.Goroutines.Total == 0 {
		resp.Goroutines.Total = uint64(runtime.NumGoroutine())
	}
	resp.Scheduler = hist("/sched/latencies:seconds")
	resp.Memory = healthMemory{
		HeapLiveBytes: u("/gc/heap/live:bytes"), HeapGoalBytes: u("/gc/heap/goal:bytes"),
		HeapObjects: u("/gc/heap/objects:objects"), TotalMappedBytes: u("/memory/classes/total:bytes"),
		GOGCPercent: u("/gc/gogc:percent"),
	}
	if limit := u("/gc/gomemlimit:bytes"); limit != 0 && limit < math.MaxInt64 {
		resp.Memory.MemoryLimitBytes = &limit
	}
	resp.GC = healthGC{
		Cycles:                  delta("/gc/cycles/total:gc-cycles"),
		Pause:                   hist("/sched/pauses/total/gc:seconds"),
		AllocatedBytesPerSecond: rate(float64(delta("/gc/heap/allocs:bytes"))),
	}
	if total := f("/cpu/classes/total:cpu-seconds"); total > 0 {
		busy := total - f("/cpu/classes/idle:cpu-seconds")
		resp.GC.BusyShareSinceStart = math.Round(busy/total*1000) / 1000
		if busy > 0 {
			resp.GC.CPUShareSinceStart = math.Round(f("/cpu/classes/gc/total:cpu-seconds")/busy*1000) / 1000
		}
	}

	if after.runLoop != nil {
		var bw, bh *promHistogram
		if before != nil && before.runLoop != nil {
			bw, bh = &before.runLoop[0], &before.runLoop[1]
		}
		wait := promDistribution(bw, after.runLoop[0])
		resp.RunLoop = &healthRunLoop{
			Turns: wait.Count, TurnsPerSecond: rate(float64(wait.Count)),
			Wait: wait, Held: promDistribution(bh, after.runLoop[1]),
		}
	}
	if aw := after.writer; aw != nil {
		var bw healthWriterSnapshot
		var bf, bc *promHistogram
		if before != nil && before.writer != nil {
			bw, bf, bc = *before.writer, &before.writer.fsync, &before.writer.commit
		}
		resp.Writer = &healthWriter{
			BatchesPerSecond: rate(aw.batches - bw.batches), CommandsPerSecond: rate(aw.commands - bw.commands),
			EventsPerSecond: rate(aw.events - bw.events), QueueDepth: after.queueLen,
			Fsync: promDistribution(bf, aw.fsync), Commit: promDistribution(bc, aw.commit),
		}
	}
	if resp.RunLoop == nil {
		resp.Note += " runLoop and writer are absent: this server exports no metrics (--metrics=false), so nothing observes them."
	}
	return resp
}

// promDistribution is the distribution a Prometheus histogram recorded after before, or
// since it was created when before is nil.
func promDistribution(before *promHistogram, after promHistogram) healthDistribution {
	count, sum := after.count, after.sum
	cum := append([]uint64(nil), after.cum...)
	if before != nil && len(before.cum) == len(cum) && before.count <= count {
		count -= before.count
		sum -= before.sum
		for i := range cum {
			cum[i] -= before.cum[i]
		}
	}
	d := healthDistribution{Count: count}
	if count == 0 || len(after.bounds) == 0 {
		return d
	}
	mean := round3(sum / float64(count) * 1000)
	d.MeanMillis = &mean
	at := func(q float64) float64 {
		target := uint64(math.Ceil(q * float64(count)))
		for i, c := range cum {
			if c >= target {
				return round3(after.bounds[i] * 1000)
			}
		}
		return round3(after.bounds[len(after.bounds)-1] * 1000)
	}
	d.P50Millis, d.P90Millis, d.P99Millis, d.MaxMillis = at(0.5), at(0.9), at(0.99), at(1)
	return d
}

// runtimeDistribution is the same for a runtime/metrics histogram, whose buckets are not
// cumulative and whose last bound may be infinite.
func runtimeDistribution(before, after *metrics.Float64Histogram) healthDistribution {
	counts := append([]uint64(nil), after.Counts...)
	if before != nil && len(before.Counts) == len(counts) {
		for i := range counts {
			if before.Counts[i] <= counts[i] {
				counts[i] -= before.Counts[i]
			}
		}
	}
	var total uint64
	for _, c := range counts {
		total += c
	}
	d := healthDistribution{Count: total}
	if total == 0 {
		return d
	}
	bound := func(i int) float64 {
		// The bucket's upper bound, or its lower one where the upper is infinite.
		if ub := after.Buckets[i+1]; !math.IsInf(ub, 1) {
			return round3(ub * 1000)
		}
		return round3(after.Buckets[i] * 1000)
	}
	at := func(q float64) float64 {
		target := uint64(math.Ceil(q * float64(total)))
		var run uint64
		for i, c := range counts {
			run += c
			if run >= target {
				return bound(i)
			}
		}
		return bound(len(counts) - 1)
	}
	d.P50Millis, d.P90Millis, d.P99Millis, d.MaxMillis = at(0.5), at(0.9), at(0.99), at(1)
	return d
}

// round3 keeps three decimals: a millisecond figure for a fast path is microseconds.
func round3(v float64) float64 { return math.Round(v*1000) / 1000 }
