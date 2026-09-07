package api_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
)

// The step replay of a deferred choice (ADR-0110), which is the one construct where the
// engine hands a token on *before* the element that held it completes: the gateway arms
// every branch's catch on activation and only then completes itself. The frame fold
// (ADR-0046/0136) defers a completion until its successor activates, and here the
// successors have already activated — so without a case of its own the gateway's token
// waits for an arrival that has come and gone, and stays parked on the gateway for the
// rest of the replay. On a looping model that is a token drawn on the gateway one race
// behind, for a race that was decided long ago.

// raceTimeline reads one instance's replay timeline.
func raceTimeline(t *testing.T, ts *httptest.Server, key uint64) instanceTimeline {
	t.Helper()
	code, body := doReq(t, ts, http.MethodGet, fmt.Sprintf("/api/v1/instances/%d/timeline", key), "", "")
	if code != http.StatusOK {
		t.Fatalf("timeline: status=%d body=%s", code, body)
	}
	var tl instanceTimeline
	if err := json.Unmarshal(body, &tl); err != nil {
		t.Fatalf("decode timeline: %v (%s)", err, body)
	}
	return tl
}

// frameElements names the elements a frame draws a token on, in the frame's own order.
func frameElements(f timelineFrame) []string {
	out := make([]string, 0, len(f.Tokens))
	for _, tok := range f.Tokens {
		out = append(out, tok.ElementID)
	}
	return out
}

// TestArmedRaceLeavesNoTokenOnTheEventGateway: while the race is armed the tokens are on
// the branches and nowhere else, and each of them names the gateway's token as its
// parent — the fact the replay draws the race from, exactly as the live overlay derives
// it from the diagram (ADR-0249).
func TestArmedRaceLeavesNoTokenOnTheEventGateway(t *testing.T) {
	ts := newTestServer(t)
	if code, body := doReq(t, ts, http.MethodPost, "/api/v1/deployments", eventGatewayBPMN, "application/xml"); code != http.StatusOK {
		t.Fatalf("deploy: %d %s", code, body)
	}
	if code, body := doReq(t, ts, http.MethodPost, "/api/v1/processes/1/instances", "{}", "application/json"); code != http.StatusOK {
		t.Fatalf("create instance: %d %s", code, body)
	}
	tl := raceTimeline(t, ts, onlyInstanceKey(t, ts))
	if len(tl.Frames) == 0 {
		t.Fatal("no frames")
	}

	// The gateway's own token, while it still held one: the parent every armed branch
	// is a fork of.
	var gwToken uint64
	for _, f := range tl.Frames {
		for _, tok := range f.Tokens {
			if tok.ElementID == "gw" {
				gwToken = tok.TokenID
			}
		}
	}
	if gwToken == 0 {
		t.Fatal("no frame ever put a token on the gateway itself")
	}

	last := tl.Frames[len(tl.Frames)-1]
	if got, want := frameElements(last), []string{"reply", "timeout"}; !sameElements(got, want) {
		t.Errorf("armed frame draws %v, want exactly the two branches %v — the gateway consumed itself into the waits", got, want)
	}
	for _, tok := range last.Tokens {
		if tok.ParentTokenID != gwToken {
			t.Errorf("armed branch %s: parentTokenId = %d, want the gateway's token %d", tok.ElementID, tok.ParentTokenID, gwToken)
		}
	}
}

// TestDecidedRaceStrandsNothingOnTheEventGateway: once the message wins, the replay of
// the finished instance carries no token on the gateway in any frame after it completed
// — and the instance ends empty-handed rather than with a ghost still parked there.
func TestDecidedRaceStrandsNothingOnTheEventGateway(t *testing.T) {
	ts := newTestServer(t)
	if code, body := doReq(t, ts, http.MethodPost, "/api/v1/deployments", eventGatewayBPMN, "application/xml"); code != http.StatusOK {
		t.Fatalf("deploy: %d %s", code, body)
	}
	if code, body := doReq(t, ts, http.MethodPost, "/api/v1/processes/1/instances", "{}", "application/json"); code != http.StatusOK {
		t.Fatalf("create instance: %d %s", code, body)
	}
	key := onlyInstanceKey(t, ts)
	if code, body := doReq(t, ts, http.MethodPost, "/api/v1/messages", `{"name":"go"}`, "application/json"); code != http.StatusOK {
		t.Fatalf("publish: %d %s", code, body)
	}

	tl := raceTimeline(t, ts, key)
	armed := false
	for _, f := range tl.Frames {
		on := frameElements(f)
		if sameElements(on, []string{"reply", "timeout"}) {
			armed = true
			continue
		}
		if !armed {
			continue // the walk up to the gateway, where its token is genuinely there
		}
		for _, tok := range f.Tokens {
			if tok.ElementID == "gw" {
				t.Fatalf("frame at %d draws a token on the gateway after the race was armed: %v", f.Position, on)
			}
		}
	}
	if !armed {
		t.Fatalf("no frame armed both branches: %v", framesSummary(tl.Frames))
	}
	if last := tl.Frames[len(tl.Frames)-1]; len(last.Tokens) != 0 {
		t.Errorf("the finished instance's last frame still holds %v, want none", frameElements(last))
	}
}

// sameElements compares two element lists as sets: a frame's token order is by token id,
// which is not what these tests are about.
func sameElements(got, want []string) bool {
	if len(got) != len(want) {
		return false
	}
	seen := map[string]int{}
	for _, g := range got {
		seen[g]++
	}
	for _, w := range want {
		seen[w]--
	}
	for _, n := range seen {
		if n != 0 {
			return false
		}
	}
	return true
}

// framesSummary renders the frames for a failure message.
func framesSummary(frames []timelineFrame) string {
	out := ""
	for _, f := range frames {
		out += fmt.Sprintf("\n  %d: %v", f.Position, frameElements(f))
	}
	return out
}
