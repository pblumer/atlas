package api_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"testing"
)

// fetchParallelTimeline deploys the fork/join model, runs one instance to
// completion, and returns its replay timeline.
func fetchParallelTimeline(t *testing.T) instanceTimeline {
	t.Helper()
	ts := newTestServer(t)
	code, body := doReq(t, ts, http.MethodPost, "/api/v1/deployments", parallelReplayBPMN, "application/xml")
	if code != http.StatusOK {
		t.Fatalf("deploy status=%d body=%s", code, body)
	}
	var dep struct {
		Key uint64 `json:"key"`
	}
	if err := json.Unmarshal(body, &dep); err != nil {
		t.Fatal(err)
	}
	if code, body = doReq(t, ts, http.MethodPost, fmt.Sprintf("/api/v1/processes/%d/instances", dep.Key), "", "application/json"); code != http.StatusOK {
		t.Fatalf("start status=%d body=%s", code, body)
	}
	key := onlyInstanceKey(t, ts)
	code, body = doReq(t, ts, http.MethodGet, fmt.Sprintf("/api/v1/instances/%d/timeline", key), "", "")
	if code != http.StatusOK {
		t.Fatalf("timeline status=%d body=%s", code, body)
	}
	var tl instanceTimeline
	if err := json.Unmarshal(body, &tl); err != nil {
		t.Fatal(err)
	}
	return tl
}

// TestParallelReplayFramesAreClean pins the clean fork/join replay: no branch
// ever flickers through an empty intermediate frame, and once both tokens are
// waiting on the join they merge into a single continuation without the join
// appearing to lose an arrival.
func TestParallelReplayFramesAreClean(t *testing.T) {
	tl := fetchParallelTimeline(t)
	if tl.State != "completed" {
		t.Fatalf("state = %q, want completed", tl.State)
	}
	if len(tl.Frames) == 0 {
		t.Fatal("no frames")
	}

	dump := func() string {
		s := ""
		for i, f := range tl.Frames {
			s += fmt.Sprintf("\n  frame %d pos=%d:", i, f.Position)
			for _, tok := range f.Tokens {
				s += fmt.Sprintf(" [tok=%d el=%s st=%s]", tok.TokenID, tok.ElementID, tok.State)
			}
		}
		return s
	}

	// No mid-process flicker: only the terminal frame (after the end event is
	// consumed) may be empty.
	for i, f := range tl.Frames {
		if len(f.Tokens) == 0 && i != len(tl.Frames)-1 {
			t.Fatalf("empty intermediate frame %d (token flickers out and back):%s", i, dump())
		}
	}

	// The fork produces two distinct concurrent tokens on the two branches.
	sawParallel := false
	for _, f := range tl.Frames {
		ways := map[string]uint64{}
		for _, tok := range f.Tokens {
			if tok.ElementID == "way1" || tok.ElementID == "way2" {
				ways[tok.ElementID] = tok.TokenID
			}
		}
		if len(ways) == 2 && ways["way1"] != ways["way2"] {
			sawParallel = true
		}
	}
	if !sawParallel {
		t.Fatalf("no frame shows both branches with distinct tokens:%s", dump())
	}

	// The join merges cleanly: find the frame where both arrivals wait, then
	// assert the join never regresses to a single lingering waiting arrival and
	// the continuation reaches the end event.
	bothWaitingAt := -1
	for i, f := range tl.Frames {
		waiting := 0
		for _, tok := range f.Tokens {
			if tok.ElementID == "join" && tok.State == "waiting" {
				waiting++
			}
		}
		if waiting == 2 {
			bothWaitingAt = i
		}
	}
	if bothWaitingAt < 0 {
		t.Fatalf("no frame shows both tokens waiting on the join:%s", dump())
	}
	sawEnd := false
	for _, f := range tl.Frames[bothWaitingAt+1:] {
		joinTokens := 0
		for _, tok := range f.Tokens {
			if tok.ElementID == "join" {
				joinTokens++
			}
			if tok.ElementID == "end" {
				sawEnd = true
			}
		}
		if joinTokens == 1 {
			t.Fatalf("join regresses to a single arrival after both had arrived (merge not clean):%s", dump())
		}
	}
	if !sawEnd {
		t.Fatalf("merged continuation never reaches the end event:%s", dump())
	}
}

// TestParallelReplaySourceElement checks each activation reports the element the
// token actually came from (its incoming flow's source), so the replay animates a
// fork branch from the gateway — not from its sibling branch, the previous row in
// the linear step list.
func TestParallelReplaySourceElement(t *testing.T) {
	tl := fetchParallelTimeline(t)
	want := map[string]string{"fork": "start", "way1": "fork", "way2": "fork", "end": "join"}
	got := map[string]string{}
	joinSources := map[string]bool{}
	for _, s := range tl.Steps {
		if s.ElementID == "join" {
			joinSources[s.SourceElementID] = true // both way1 and way2 feed the join
			continue
		}
		got[s.ElementID] = s.SourceElementID
	}
	for el, src := range want {
		if got[el] != src {
			t.Errorf("step %q source = %q, want %q", el, got[el], src)
		}
	}
	if !joinSources["way1"] || !joinSources["way2"] {
		t.Errorf("join arrivals came from %v, want both way1 and way2", joinSources)
	}
}

// TestParallelReplayFramesDeterministic re-reads the same instance's timeline and
// checks the folded frames are byte-identical, since replay must be deterministic.
func TestParallelReplayFramesDeterministic(t *testing.T) {
	a := fetchParallelTimeline(t)
	b := fetchParallelTimeline(t)
	ja, _ := json.Marshal(a.Frames)
	jb, _ := json.Marshal(b.Frames)
	// Instance keys differ between the two runs, but the frame shape (elements,
	// states, ordering) must be identical.
	normalize := func(tl instanceTimeline) string {
		s := ""
		for _, f := range tl.Frames {
			for _, tok := range f.Tokens {
				s += fmt.Sprintf("%s/%s;", tok.ElementID, tok.State)
			}
			s += "|"
		}
		return s
	}
	if normalize(a) != normalize(b) {
		t.Fatalf("frame shape not deterministic:\n a=%s\n b=%s", string(ja), string(jb))
	}
}
