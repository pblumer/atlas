package api

import (
	"strings"
	"testing"

	"github.com/pblumer/atlas/api/panorama"
)

// TestProcessStatusSeparatesQuietFromParked pins the two sentences the landscape
// puts under a process. "No work is parked" is a claim the engine can make about
// itself — it holds every incident it ever raised — and the parked case must carry
// the count, because "something is wrong here" without a number is what an operator
// learns to scroll past.
func TestProcessStatusSeparatesQuietFromParked(t *testing.T) {
	state, reason := processStatus(0)
	if state != panorama.StateHealthy || reason == "" {
		t.Fatalf("nothing parked = %q/%q, want healthy with a reason", state, reason)
	}
	state, reason = processStatus(7)
	if state != panorama.StateDegraded || !strings.Contains(reason, "7 token(s)") {
		t.Fatalf("seven parked = %q/%q, want degraded naming the count", state, reason)
	}
}

// TestWorkerStatusOfAWorkerOnlyKindRestsOnWhatWorkersReported is the honesty this
// collector turns on. For a worker-only kind (ADR-0172) the engine builds no client
// and holds no credential, so the absence of a problem in *its* registry is not
// evidence of anything. The only evidence there is comes from the polls, and the
// three answers below are three genuinely different states of knowledge.
func TestWorkerStatusOfAWorkerOnlyKindRestsOnWhatWorkersReported(t *testing.T) {
	s := &Server{}

	// Nobody has polled: nothing is known, and a fresh server must not paint a red
	// node for a worker that would report itself fine on its first poll.
	state, reason := s.workerStatus(connectorKindEntra, "directory", false, map[string]bool{})
	if state != panorama.StateUnbound {
		t.Fatalf("before any poll = %q, want unbound; %q", state, reason)
	}

	// Workers have polled and one holds it: that is a real observation of health.
	state, _ = s.workerStatus(connectorKindEntra, "directory", true, map[string]bool{"directory": true})
	if state != panorama.StateHealthy {
		t.Fatalf("held by a polling worker = %q, want healthy", state)
	}

	// Workers have polled and none holds it: its tasks would park, which is the
	// difference between "unknown" and "broken".
	state, reason = s.workerStatus(connectorKindEntra, "directory", true, map[string]bool{"other": true})
	if state != panorama.StateNotReady || !strings.Contains(reason, "park") {
		t.Fatalf("polled but unheld = %q/%q, want not-ready saying what happens", state, reason)
	}
}

// TestConnectorKindIsWorkerOnlyMatchesTheRegistry keeps the one thing this file
// decides from drifting away from the list that actually decides it. An engine-run
// kind wrongly treated as worker-only would report unbound where the engine knows
// the answer; the reverse would report health the engine cannot vouch for.
func TestConnectorKindIsWorkerOnlyMatchesTheRegistry(t *testing.T) {
	for _, k := range managedConnectorKinds {
		if got := connectorKindIsWorkerOnly(k.name); got != k.workerOnly {
			t.Errorf("connectorKindIsWorkerOnly(%q) = %v, want %v", k.name, got, k.workerOnly)
		}
	}
	if connectorKindIsWorkerOnly("not-a-kind") {
		t.Error("an unrecognized kind is reported worker-only")
	}
}

// TestWorkerHoldingsReflectsWhatWorkersReported covers the evidence the worker-only
// answer rests on. The registry is runtime state a restart clears, so the two
// returns are genuinely different facts: what is held, and whether anyone has said
// anything at all. Collapsing them would turn a just-restarted server into a
// landscape full of red nodes that resolve themselves on the first poll.
func TestWorkerHoldingsReflectsWhatWorkersReported(t *testing.T) {
	s := &Server{workers: newWorkerRegistry(func() int64 { return 1 })}

	held, polled := s.workerHoldings()
	if polled || len(held) != 0 {
		t.Fatalf("an empty registry = %v/%v, want nothing held and nobody polled", held, polled)
	}

	s.workers.holdsConnectors("worker-a", []string{"directory", "ops-mail"})
	s.workers.holdsConnectors("worker-b", []string{"ops-mail"})
	held, polled = s.workerHoldings()
	if !polled {
		t.Fatal("workers have polled and the registry says otherwise")
	}
	if !held["directory"] || !held["ops-mail"] || len(held) != 2 {
		t.Fatalf("held = %v, want the union of what both workers reported", held)
	}

	// A worker that polled while holding nothing still counts as a poll: it is
	// evidence that the workers are talking, which is what separates "nobody has
	// said anything yet" from "nobody serves this".
	quiet := &Server{workers: newWorkerRegistry(func() int64 { return 1 })}
	quiet.workers.seen("worker-c")
	if held, polled := quiet.workerHoldings(); !polled || len(held) != 0 {
		t.Fatalf("a worker holding nothing = %v/%v, want polled with nothing held", held, polled)
	}
}
