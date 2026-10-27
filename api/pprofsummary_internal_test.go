package api

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"runtime"
	"runtime/pprof"
	"strings"
	"sync"
	"testing"
)

// The summary an agent reads (ADR-0502): the server reads its own profile
// and answers with the functions and stacks a diagnosis starts from.

func readPprofSummary(t *testing.T, path string) pprofSummaryResp {
	t.Helper()
	rec := servePprof(t, path)
	if rec.Code != http.StatusOK {
		t.Fatalf("%s: status=%d body=%s", path, rec.Code, rec.Body.Bytes())
	}
	var resp pprofSummaryResp
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("%s: %v", path, err)
	}
	if resp.Note == "" {
		t.Errorf("%s: the answer does not say what its figures mean", path)
	}
	return resp
}

func rowNamed(rows []pprofRow, part string) (pprofRow, bool) {
	for _, r := range rows {
		if strings.Contains(r.Function, part) {
			return r, true
		}
	}
	return pprofRow{}, false
}

// pprofSummaryTestParked waits until released: a stack the goroutine summary must find.
//
//go:noinline
func pprofSummaryTestParked(release <-chan struct{}, ready *sync.WaitGroup) {
	ready.Done()
	<-release
}

// TestTheGoroutineSummaryGroupsIdenticalStacks: seven goroutines waiting in the same place
// are one group of seven, with the frame that says where.
func TestTheGoroutineSummaryGroupsIdenticalStacks(t *testing.T) {
	release := make(chan struct{})
	var ready sync.WaitGroup
	for range 7 {
		ready.Add(1)
		go pprofSummaryTestParked(release, &ready)
	}
	ready.Wait()
	defer close(release)

	resp := readPprofSummary(t, "/api/v1/debug/pprof/goroutine/summary?top=50")
	if resp.Unit != "goroutines" || resp.Total < 7 {
		t.Fatalf("goroutine summary: unit %q total %v, want goroutines and at least 7", resp.Unit, resp.Total)
	}
	for _, st := range resp.Stacks {
		for _, f := range st.Frames {
			if strings.Contains(f, "pprofSummaryTestParked") {
				if st.Count != 7 {
					t.Errorf("the parked goroutines are a group of %d, want 7", st.Count)
				}
				if st.Frames[0] != "runtime.gopark" {
					t.Errorf("the leaf frame is %q, want what the goroutine waits in", st.Frames[0])
				}
				return
			}
		}
	}
	t.Fatalf("no stack names the parked goroutines: %+v", resp.Stacks)
}

// pprofSummaryTestHeld is what the heap summary must find holding memory.
var pprofSummaryTestHeld [][]byte

//go:noinline
func pprofSummaryTestHold() {
	for range 8 {
		pprofSummaryTestHeld = append(pprofSummaryTestHeld, make([]byte, 1<<20))
	}
}

// TestTheHeapSummaryNamesWhatHoldsTheMemory: eight megabytes kept by one function are that
// function's, flat and cumulative, after a collection.
func TestTheHeapSummaryNamesWhatHoldsTheMemory(t *testing.T) {
	pprofSummaryTestHold()
	defer func() { pprofSummaryTestHeld = nil }()

	resp := readPprofSummary(t, "/api/v1/debug/pprof/heap/summary?gc=1&top=50")
	if resp.Unit != "bytes" || resp.Objects == 0 {
		t.Fatalf("heap summary: unit %q objects %d", resp.Unit, resp.Objects)
	}
	row, ok := rowNamed(resp.Top, "pprofSummaryTestHold")
	if !ok {
		t.Fatalf("the heap summary does not name the function holding 8 MiB: %+v", resp.Top)
	}
	if row.Flat < 4<<20 || row.Cum < row.Flat || row.FlatPercent <= 0 {
		t.Errorf("pprofSummaryTestHold: %+v, want at least 4 MiB flat and cum ≥ flat", row)
	}
	if _, ok := rowNamed(resp.Cumulative, "pprofSummaryTestHold"); !ok {
		t.Errorf("the cumulative list misses the function holding the memory")
	}
	if resp := readPprofSummary(t, "/api/v1/debug/pprof/allocs/summary"); resp.Total < 8<<20 {
		t.Errorf("allocs summary total %v, want at least the 8 MiB allocated", resp.Total)
	}
}

// pprofSummaryTestBurn spends CPU until stopped.
//
//go:noinline
func pprofSummaryTestBurn(stop <-chan struct{}) uint64 {
	var x uint64 = 1
	for {
		select {
		case <-stop:
			return x
		default:
		}
		for i := 0; i < 1e5; i++ {
			x = x*6364136223846793005 + 1442695040888963407
		}
	}
}

// TestTheCPUSummaryNamesWhereTheTimeWent: a function that burns a core for the window is
// at the top of it, and the window reports the cores it measured.
func TestTheCPUSummaryNamesWhereTheTimeWent(t *testing.T) {
	stop := make(chan struct{})
	done := make(chan struct{})
	go func() { pprofSummaryTestBurn(stop); close(done) }()
	defer func() { close(stop); <-done }()

	resp := readPprofSummary(t, "/api/v1/debug/pprof/profile/summary?seconds=1&top=50")
	if resp.Unit != "milliseconds" || resp.Seconds != 1 || resp.Samples == 0 || resp.Cores == nil {
		t.Fatalf("CPU summary: %+v", resp)
	}
	row, ok := rowNamed(resp.Top, "pprofSummaryTestBurn")
	if !ok {
		t.Fatalf("the CPU summary does not name the function that burned the window: %+v", resp.Top)
	}
	if row.FlatPercent < 20 {
		t.Errorf("pprofSummaryTestBurn took %.1f%% of the samples; a busy core should take more", row.FlatPercent)
	}
	if *resp.Cores < 0.3 || *resp.Cores > float64(runtime.NumCPU())+0.5 {
		t.Errorf("cores = %v for one busy goroutine", *resp.Cores)
	}
}

// TestTheSummaryRefusesWhatItDoesNotSummarize: the recordings an agent cannot read and the
// windows the MCP client would give up on are refused, and so is a second CPU profile.
func TestTheSummaryRefusesWhatItDoesNotSummarize(t *testing.T) {
	for _, name := range []string{"trace", "threadcreate", "cmdline", "nonsense"} {
		if rec := servePprof(t, "/api/v1/debug/pprof/"+name+"/summary"); rec.Code != http.StatusNotFound {
			t.Errorf("%s/summary: status=%d, want 404", name, rec.Code)
		}
	}
	for _, q := range []string{"profile/summary?seconds=21", "profile/summary?seconds=0", "heap/summary?top=0",
		"heap/summary?top=51", "goroutine/summary?top=x", "heap/summary?gc=2"} {
		if rec := servePprof(t, "/api/v1/debug/pprof/"+q); rec.Code != http.StatusBadRequest {
			t.Errorf("%s: status=%d, want 400", q, rec.Code)
		}
	}
	if err := pprof.StartCPUProfile(io.Discard); err != nil {
		t.Fatalf("start a CPU profile to collide with: %v", err)
	}
	rec := servePprof(t, "/api/v1/debug/pprof/profile/summary?seconds=1")
	pprof.StopCPUProfile()
	if rec.Code != http.StatusConflict {
		t.Errorf("a CPU summary while a profile runs: status=%d, want 409", rec.Code)
	}
}

// TestTheSummaryIsForAdministrators, like the profiles it reads.
func TestTheSummaryIsForAdministrators(t *testing.T) {
	path := "/api/v1/debug/pprof/heap/summary"
	if got := boundaryStatus(t, []string{RoleModeler, RoleOperator, RoleUser}, http.MethodGet, path); got != http.StatusForbidden {
		t.Errorf("%s without an admin principal: status=%d, want 403", path, got)
	}
	if got := boundaryStatus(t, []string{RoleAdmin}, http.MethodGet, path); got != http.StatusTeapot {
		t.Errorf("%s as an administrator: status=%d, want the request to reach the handler", path, got)
	}
}

// TestTheProfileReaderReadsBothRepeatedForms: the runtime writes a short repeated field as
// separate fields and a longer one packed, and the reader takes either; a truncated buffer
// is an error rather than a half-read profile.
func TestTheProfileReaderReadsBothRepeatedForms(t *testing.T) {
	// field 1 varint 150, then field 2 packed {1, 2, 300}, then field 2 unpacked 7.
	msg := []byte{0x08, 0x96, 0x01, 0x12, 0x04, 0x01, 0x02, 0xac, 0x02, 0x10, 0x07}
	var ones, twos []uint64
	err := protoFields(msg, func(f protoField) error {
		var err error
		switch f.num {
		case 1:
			ones, err = f.varints(ones)
		case 2:
			twos, err = f.varints(twos)
		}
		return err
	})
	if err != nil {
		t.Fatalf("protoFields: %v", err)
	}
	if len(ones) != 1 || ones[0] != 150 || len(twos) != 4 || twos[2] != 300 || twos[3] != 7 {
		t.Errorf("read %v and %v, want [150] and [1 2 300 7]", ones, twos)
	}
	for _, cut := range [][]byte{{0x08}, {0x12, 0x05, 0x01}, {0x09, 0x01, 0x02}} {
		if err := protoFields(cut, func(protoField) error { return nil }); err == nil {
			t.Errorf("% x read without error", cut)
		}
	}

	// And a real profile: the heap the runtime writes now has its four sample types and
	// names its functions.
	var buf bytes.Buffer
	if err := pprof.Lookup("heap").WriteTo(&buf, 0); err != nil {
		t.Fatal(err)
	}
	p, err := parsePprof(buf.Bytes())
	if err != nil {
		t.Fatalf("parsePprof: %v", err)
	}
	for _, typ := range []string{"alloc_objects", "alloc_space", "inuse_objects", "inuse_space"} {
		if p.valueIndex(typ) < 0 {
			t.Errorf("the heap profile has no %s: %+v", typ, p.sampleTypes)
		}
	}
	if len(p.functions) == 0 || len(p.samples) == 0 {
		t.Errorf("the heap profile read as %d functions and %d samples", len(p.functions), len(p.samples))
	}
}
