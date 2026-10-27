package api

import (
	"bytes"
	"fmt"
	"log/slog"
	"math"
	"net/http"
	"runtime/pprof"
	"sort"
	"strconv"

	"github.com/pblumer/atlas/api/httpapi"
	"github.com/pblumer/atlas/logging"
)

// A profile an agent can read (ADR-0502).
//
// The binary route serves what `go tool pprof` opens, which is a file for a person with a
// profiler. An agent diagnosing a slow server over MCP has no profiler, and a megabyte of
// gzip in its context is worse than nothing. So the server reads its own profile and
// answers with the part a diagnosis starts from — the functions that took the time or hold
// the memory, and the stacks the goroutines wait in — bounded to a top-N list.
//
// Two things are deliberately not in it. Argument values: the goroutine summary is built
// from the aggregated profile, not from the panic-style dump, which prints each frame's
// arguments as raw words. And a long window: a CPU summary is at most
// pprofSummaryCPUMax seconds, because the MCP client gives up on a call after thirty.

const (
	pprofSummaryCPUDefault = 10
	pprofSummaryCPUMax     = 20
	pprofSummaryTopDefault = 20
	pprofSummaryTopMax     = 50
	// pprofStackFrames bounds one goroutine stack in the summary: the leaf frames say what
	// a goroutine waits on, the root frames which part of Atlas started it, and the middle
	// of a deep stack says neither.
	pprofStackFrames = 16
)

// pprofRow is one function's share of a profile: flat is what it spent or holds itself,
// cum what it and everything it called did.
type pprofRow struct {
	Function    string  `json:"function"`
	Flat        float64 `json:"flat"`
	FlatPercent float64 `json:"flatPercent"`
	Cum         float64 `json:"cum"`
	CumPercent  float64 `json:"cumPercent"`
}

// pprofStack is one group of goroutines with the same stack.
type pprofStack struct {
	Count  int64    `json:"count"`
	Frames []string `json:"frames"`
}

type pprofSummaryResp struct {
	Profile    string       `json:"profile"`
	Unit       string       `json:"unit"`
	Seconds    int          `json:"seconds,omitempty"`
	Samples    int64        `json:"samples,omitempty"`
	Cores      *float64     `json:"cores,omitempty"`
	Total      float64      `json:"total"`
	Objects    int64        `json:"objects,omitempty"`
	Top        []pprofRow   `json:"top,omitempty"`
	Cumulative []pprofRow   `json:"cumulative,omitempty"`
	Stacks     []pprofStack `json:"stacks,omitempty"`
	Note       string       `json:"note"`
}

// pprofSummaries is what the summary route answers, with what each figure means — said in
// the answer, because an agent reading "flat" has nothing else to go on.
var pprofSummaries = map[string]string{
	"profile": "CPU time sampled at 100 Hz over the window, in milliseconds. flat is time in the function itself, " +
		"cum includes what it called; cores is the CPU time over the window's length. A function with high cum " +
		"and low flat is a path, one with high flat is where the work is.",
	"heap": "Memory held by live objects as of the last completed garbage collection, in bytes, sampled about " +
		"every 512 KiB allocated; objects is how many. flat is what a function allocated and still holds, cum " +
		"includes what it called. Ask with gc=1 for the heap after a collection now.",
	"allocs": "Memory allocated since the server started, in bytes, freed or not, sampled about every 512 KiB; " +
		"objects is how many. High allocation drives garbage collection: compare with atlas_runtime_health's gc " +
		"figures.",
	"goroutine": "Every goroutine, grouped by identical stack, largest group first; total is how many there are. " +
		"frames run from the leaf — what the goroutine is doing or waiting on (runtime.gopark under " +
		"runtime.selectgo or runtime.chanrecv is a goroutine waiting on a channel) — toward where it was started.",
}

// handlePprofSummary answers one profile as a top-N list rather than a file.
func (s *Server) handlePprofSummary(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("profile")
	note, ok := pprofSummaries[name]
	if !ok {
		httpapi.Error(w, http.StatusNotFound, "no summary of "+name+"; one of allocs, goroutine, heap, profile")
		return
	}
	top := pprofSummaryTopDefault
	if v := r.URL.Query().Get("top"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 || n > pprofSummaryTopMax {
			httpapi.Error(w, http.StatusBadRequest, fmt.Sprintf("top must be a whole number from 1 to %d", pprofSummaryTopMax))
			return
		}
		top = n
	}
	resp := pprofSummaryResp{Profile: name, Note: note}
	var buf bytes.Buffer
	if name == "profile" {
		seconds, msg := pprofSeconds(r, name, pprofSummaryCPUDefault, pprofSummaryCPUMax)
		if msg != "" {
			httpapi.Error(w, http.StatusBadRequest, msg)
			return
		}
		if err := recordPprof(r, &buf, seconds, pprof.StartCPUProfile, pprof.StopCPUProfile, func() {
			audit(r, logging.ProfileDownloaded, "profile summary recording",
				slog.String("profile", name), slog.Int("seconds", seconds), slog.Bool("summary", true))
		}); err != nil {
			pprofBusy(w, "a CPU profile", err)
			return
		}
		if r.Context().Err() != nil {
			return // the client went away; there is nobody to answer
		}
		resp.Seconds = seconds
	} else {
		if msg := pprofCollect(r, name); msg != "" {
			httpapi.Error(w, http.StatusBadRequest, msg)
			return
		}
		audit(r, logging.ProfileDownloaded, "profile summary", slog.String("profile", name), slog.Bool("summary", true))
		if err := pprof.Lookup(name).WriteTo(&buf, 0); err != nil {
			httpapi.Error(w, http.StatusInternalServerError, "write the "+name+" profile: "+err.Error())
			return
		}
	}
	p, err := parsePprof(buf.Bytes())
	if err != nil {
		httpapi.Error(w, http.StatusInternalServerError, "read the "+name+" profile: "+err.Error())
		return
	}

	switch name {
	case "profile":
		resp.Unit = "milliseconds"
		resp.Samples = p.sum(p.valueIndex("samples"))
		cpu := p.valueIndex("cpu")
		resp.Total, resp.Top, resp.Cumulative = p.byFunction(cpu, 1e-6, top)
		window := float64(p.durationNanos)
		if window <= 0 {
			window = float64(resp.Seconds) * 1e9
		}
		cores := math.Round(float64(p.sum(cpu))/window*100) / 100
		resp.Cores = &cores
	case "heap":
		resp.Unit = "bytes"
		resp.Objects = p.sum(p.valueIndex("inuse_objects"))
		resp.Total, resp.Top, resp.Cumulative = p.byFunction(p.valueIndex("inuse_space"), 1, top)
	case "allocs":
		resp.Unit = "bytes"
		resp.Objects = p.sum(p.valueIndex("alloc_objects"))
		resp.Total, resp.Top, resp.Cumulative = p.byFunction(p.valueIndex("alloc_space"), 1, top)
	case "goroutine":
		resp.Unit = "goroutines"
		resp.Total, resp.Stacks = p.stacks(top)
	}
	httpapi.JSON(w, http.StatusOK, resp)
}

// sum adds up one value over every sample; an absent sample type sums to zero.
func (p *pprofProfile) sum(idx int) int64 {
	var total int64
	if idx < 0 {
		return 0
	}
	for _, s := range p.samples {
		if idx < len(s.values) {
			total += s.values[idx]
		}
	}
	return total
}

// byFunction folds one value of every sample onto the functions of its stack and returns
// the total with the top functions twice: by what they did themselves, and by what they
// and their callees did. scale converts the profile's unit to the answer's.
func (p *pprofProfile) byFunction(idx int, scale float64, top int) (float64, []pprofRow, []pprofRow) {
	flat, cum := map[string]int64{}, map[string]int64{}
	var total int64
	if idx >= 0 {
		for _, s := range p.samples {
			if idx >= len(s.values) || s.values[idx] == 0 {
				continue
			}
			v := s.values[idx]
			total += v
			frames := p.frames(s)
			if len(frames) == 0 {
				continue
			}
			flat[frames[0]] += v
			seen := map[string]bool{}
			for _, f := range frames {
				if !seen[f] {
					seen[f] = true
					cum[f] += v
				}
			}
		}
	}
	rows := make([]pprofRow, 0, len(cum))
	for f, c := range cum {
		rows = append(rows, pprofRow{
			Function: f,
			Flat:     round1(float64(flat[f]) * scale), FlatPercent: percent(flat[f], total),
			Cum: round1(float64(c) * scale), CumPercent: percent(c, total),
		})
	}
	byFlat := append([]pprofRow(nil), rows...)
	sort.Slice(byFlat, func(i, j int) bool {
		if byFlat[i].Flat != byFlat[j].Flat {
			return byFlat[i].Flat > byFlat[j].Flat
		}
		return byFlat[i].Function < byFlat[j].Function
	})
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].Cum != rows[j].Cum {
			return rows[i].Cum > rows[j].Cum
		}
		return rows[i].Function < rows[j].Function
	})
	// A function that did nothing itself is not a hot spot, however often it called one.
	for len(byFlat) > 0 && byFlat[len(byFlat)-1].Flat == 0 {
		byFlat = byFlat[:len(byFlat)-1]
	}
	return round1(float64(total) * scale), firstRows(byFlat, top), firstRows(rows, top)
}

// stacks returns how many goroutines there are and the largest groups of them.
func (p *pprofProfile) stacks(top int) (float64, []pprofStack) {
	var total int64
	out := make([]pprofStack, 0, len(p.samples))
	for _, s := range p.samples {
		if len(s.values) == 0 || s.values[0] == 0 {
			continue
		}
		total += s.values[0]
		frames := p.frames(s)
		if len(frames) > pprofStackFrames {
			// Keep both ends: what it waits on, and what started it.
			frames = append(append(frames[:pprofStackFrames/2:pprofStackFrames/2], "…"), frames[len(frames)-pprofStackFrames/2:]...)
		}
		out = append(out, pprofStack{Count: s.values[0], Frames: frames})
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Count > out[j].Count })
	if len(out) > top {
		out = out[:top]
	}
	return float64(total), out
}

func firstRows(rows []pprofRow, n int) []pprofRow {
	if len(rows) > n {
		rows = rows[:n]
	}
	if rows == nil {
		rows = []pprofRow{}
	}
	return rows
}

func percent(part, total int64) float64 {
	if total == 0 {
		return 0
	}
	return round1(float64(part) * 100 / float64(total))
}

// round1 keeps one decimal: a summary is read, not computed with.
func round1(v float64) float64 { return math.Round(v*10) / 10 }
