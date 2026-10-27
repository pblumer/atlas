package api

import (
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"runtime"
	"runtime/pprof"
	"runtime/trace"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/pblumer/atlas/api/httpapi"
	"github.com/pblumer/atlas/logging"
)

// Profiling a running server (ADR-draft-admin-profiling).
//
// "The server is slow" is answered by where it spends its time, and the only server that
// can say is the one that is slow: a profile taken on a laptop against a test load
// measures the laptop. Go's runtime keeps exactly what is needed — CPU samples, the heap,
// every goroutine's stack, an execution trace — and `go tool pprof` reads it. What was
// missing was a way to reach it that is not a second, unauthenticated port.
//
// So it is a route like any other: behind the login, admin-only at the boundary, in the
// route table and the audit trail. Three choices make it safe to leave on.
//
//   - net/http/pprof is not imported. Its init registers handlers on
//     http.DefaultServeMux — no login, no role, no bound on a recording — and the day
//     anything serves that mux they would be public. The runtime packages it wraps are
//     used directly instead (TestTheStandardPprofHandlersAreNotLinked, cmd/atlas).
//   - What is offered is a list, and the command line is not on it: a flag can carry a
//     secret, and an API that hands flags back is a second place a secret is read from.
//     Neither are block and mutex, which stay empty unless a rate is set process-wide.
//   - A recording is bounded. The CPU profile and the trace cost the server while they
//     run, the runtime keeps one of each, and an unbounded window would let one request
//     hold the profiler and that cost for as long as it liked.

const (
	// pprofCPUDefault and pprofCPUMax bound a CPU profile: half a minute is what
	// `go tool pprof` asks for by default and is enough samples to see a hot path at
	// 100 Hz; two minutes catches something that happens once a minute.
	pprofCPUDefault = 30
	pprofCPUMax     = 120
	// pprofTraceDefault and pprofTraceMax bound a trace, which records every scheduling
	// event and grows by megabytes a second under load: seconds are what one is read in.
	pprofTraceDefault = 5
	pprofTraceMax     = 30
)

// pprofSnapshots are the profiles that describe the server as it is, each with the
// highest debug level the runtime renders for it: 0 is the binary pprof reads, 1 text,
// and 2 — goroutines only — every stack in the form a panic prints.
var pprofSnapshots = map[string]int{
	"heap":         1,
	"allocs":       1,
	"goroutine":    2,
	"threadcreate": 1,
}

// pprofOffered names everything this route answers, for the refusal of anything else.
func pprofOffered() string {
	names := []string{"profile", "trace"}
	for name := range pprofSnapshots {
		names = append(names, name)
	}
	sort.Strings(names)
	return strings.Join(names, ", ")
}

// handlePprof serves one profile: a CPU profile or a trace recorded over ?seconds=, or a
// snapshot of the heap, the allocations, the goroutines or the threads.
func (s *Server) handlePprof(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("profile")
	switch name {
	case "profile":
		servePprofWindow(w, r, name, "a CPU profile", pprofCPUDefault, pprofCPUMax, ".pb.gz", pprof.StartCPUProfile, pprof.StopCPUProfile)
		return
	case "trace":
		servePprofWindow(w, r, name, "a trace", pprofTraceDefault, pprofTraceMax, ".trace", trace.Start, trace.Stop)
		return
	}
	maxDebug, ok := pprofSnapshots[name]
	if !ok {
		httpapi.Error(w, http.StatusNotFound, "no such profile: "+name+"; one of "+pprofOffered())
		return
	}
	debug := 0
	if v := r.URL.Query().Get("debug"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 0 || n > maxDebug {
			httpapi.Error(w, http.StatusBadRequest, fmt.Sprintf("debug must be a whole number from 0 to %d for %s", maxDebug, name))
			return
		}
		debug = n
	}
	switch r.URL.Query().Get("gc") {
	case "", "0":
	case "1":
		// What is live rather than what is merely not yet collected.
		runtime.GC()
	default:
		httpapi.Error(w, http.StatusBadRequest, "gc must be 0 or 1")
		return
	}

	audit(r, logging.ProfileDownloaded, "profile downloaded", slog.String("profile", name))
	if debug > 0 {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	} else {
		pprofAttachment(w, name, ".pb.gz")
	}
	// The status is sent with the first byte, so a failure from here on is the client
	// going away, and there is nobody left to tell.
	_ = pprof.Lookup(name).WriteTo(w, debug)
}

// servePprofWindow records a CPU profile or a trace into the response for ?seconds=, or
// until the client goes away, whichever is first.
func servePprofWindow(w http.ResponseWriter, r *http.Request, name, what string, def, max int, ext string, start func(io.Writer) error, stop func()) {
	seconds := def
	if v := r.URL.Query().Get("seconds"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 || n > max {
			httpapi.Error(w, http.StatusBadRequest, fmt.Sprintf("seconds must be a whole number from 1 to %d for %s", max, name))
			return
		}
		seconds = n
	}
	pprofAttachment(w, name, ext)
	if err := start(w); err != nil {
		// The runtime keeps one of each. Somebody else's recording is running; this one
		// would not be a recording at all.
		w.Header().Del("Content-Disposition")
		httpapi.Error(w, http.StatusConflict, what+" is already being recorded on this server ("+err.Error()+"); try again when it has finished")
		return
	}
	audit(r, logging.ProfileDownloaded, "profile recording", slog.String("profile", name), slog.Int("seconds", seconds))
	timer := time.NewTimer(time.Duration(seconds) * time.Second)
	select {
	case <-timer.C:
	case <-r.Context().Done():
		timer.Stop()
	}
	stop()
}

// pprofAttachment names the download for the profile and the moment it was taken, so two
// taken in one incident are two files.
func pprofAttachment(w http.ResponseWriter, name, ext string) {
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q",
		"atlas-"+name+"-"+time.Now().UTC().Format("20060102-150405")+ext))
	w.Header().Set("X-Content-Type-Options", "nosniff")
}
