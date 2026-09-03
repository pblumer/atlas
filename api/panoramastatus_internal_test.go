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

// TestIncidentSitesRankTheWorstFirstAndStopThere. A landscape view is where somebody
// decides *which* process to open, not where they triage eleven broken tasks — so
// the list is the worst few and Operations holds the rest. Ordering past the count
// is by element id because two reads of an unchanged server have to produce the same
// document: a list that reshuffled itself would make the drift journal record
// changes that never happened.
func TestIncidentSitesRankTheWorstFirstAndStopThere(t *testing.T) {
	byElement := map[string]*panorama.IncidentSite{}
	// Seven elements, so the bound bites, with two sharing a count to pin the tie.
	for _, seed := range []struct {
		id    string
		count int
	}{
		{"a-task", 2}, {"b-task", 9}, {"c-task", 2}, {"d-task", 7},
		{"e-task", 1}, {"f-task", 5}, {"g-task", 4},
	} {
		byElement[seed.id] = &panorama.IncidentSite{ElementID: seed.id, Count: seed.count}
	}

	ranked := rankIncidentSites(byElement)

	if len(ranked) != maxIncidentSites {
		t.Fatalf("ranked %d site(s), want the bound of %d", len(ranked), maxIncidentSites)
	}
	var got []string
	for _, site := range ranked {
		got = append(got, site.ElementID)
	}
	want := []string{"b-task", "d-task", "f-task", "g-task", "a-task"}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("ranked = %v, want %v — worst first, then by element id", got, want)
		}
	}
	// The tie is broken by id, so the same map produces the same list every time.
	for i := 0; i < 20; i++ {
		again := rankIncidentSites(byElement)
		if again[len(again)-1].ElementID != "a-task" {
			t.Fatalf("run %d ended on %q; the order is not deterministic", i, again[len(again)-1].ElementID)
		}
	}
}

// TestATruncatedMessageSaysItWasTruncated. A worker can return a page of HTML as
// its error and a panel is not where somebody reads that — but a silently shortened
// message reads as a complete one, and then somebody searches their logs for a
// string that does not exist.
func TestATruncatedMessageSaysItWasTruncated(t *testing.T) {
	short := "502 Bad Gateway"
	if got := truncateMessage(short); got != short {
		t.Errorf("truncateMessage(%q) = %q, want it untouched", short, got)
	}

	long := strings.Repeat("x", maxIncidentMessage+50)
	got := truncateMessage(long)
	if len([]rune(got)) != maxIncidentMessage+1 {
		t.Errorf("truncated length = %d rune(s), want the bound plus the mark", len([]rune(got)))
	}
	if !strings.HasSuffix(got, "…") {
		t.Errorf("truncateMessage did not mark the cut: %q", got[len(got)-10:])
	}

	// Exactly at the bound is not truncated: the mark means "there is more", and
	// there is not.
	exact := strings.Repeat("y", maxIncidentMessage)
	if got := truncateMessage(exact); got != exact {
		t.Error("a message exactly at the bound was marked as cut")
	}
}

// TestJobTypeStatusAnswersOnlyWhatTheEngineCanSee is the mapping ADR-0189 §6 needed
// and nobody had chosen, asserted at the place that chose it.
//
// The turn it makes is the whole design: a job type is a name for work, not a thing
// that can be well or unwell, so the question becomes "is this kind of work getting
// done here" — and the last row is the one that matters most. A fresh server has an
// empty worker registry, and a mapping that read that as "nobody serves this" would
// mark every worker-served kind broken on every restart, which is exactly what §4's
// severity rules exist to prevent.
func TestJobTypeStatusAnswersOnlyWhatTheEngineCanSee(t *testing.T) {
	for name, tc := range map[string]struct {
		taken, incidents int64
		inProcess        bool
		state            string
		says             string
	}{
		"parked work is a finding": {
			incidents: 3, state: panorama.StateDegraded, says: "3 job(s)",
		},
		"a finding outranks the engine serving it": {
			incidents: 2, inProcess: true, taken: 90,
			state: panorama.StateDegraded, says: "parked",
		},
		"the engine serving it is knowledge, not inference": {
			inProcess: true, state: panorama.StateHealthy, says: "runs this job type itself",
		},
		"work demonstrably done is healthy": {
			taken: 12, state: panorama.StateHealthy, says: "12 job(s)",
		},
		"nothing seen is unbound, not broken": {
			state: panorama.StateUnbound, says: "emptied by a restart",
		},
	} {
		t.Run(name, func(t *testing.T) {
			state, reason := jobTypeStatus(tc.taken, tc.incidents, tc.inProcess)
			if state != tc.state {
				t.Errorf("state = %q, want %q", state, tc.state)
			}
			if !strings.Contains(reason, tc.says) {
				t.Errorf("reason = %q, want it to mention %q", reason, tc.says)
			}
		})
	}

	// That the unbound case is neutral rather than a fourth level of badness is
	// pinned where the mapping lives (TestSeverityMapsEachStateThroughOneTable in the
	// panorama package), so it is not restated here through an export that would
	// exist only for this test.
}
