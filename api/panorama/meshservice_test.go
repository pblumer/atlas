package panorama

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/pblumer/atlas/api/runloop"
)

// meshLoop starts a run loop for one test and stops it on cleanup.
func meshLoop(t *testing.T) *runloop.Loop {
	t.Helper()
	quit := make(chan struct{})
	loop := runloop.New(quit)
	go loop.Run()
	t.Cleanup(func() { close(quit) })
	return loop
}

// stoppedLoop returns a loop whose quit channel is already closed, so Loop.Do
// declines to run anything dispatched onto it.
func stoppedLoop() *runloop.Loop {
	quit := make(chan struct{})
	close(quit)
	return runloop.New(quit)
}

func TestMeshHandleGraphServesTheDerivedGraph(t *testing.T) {
	mesh := NewMesh(meshLoop(t), func(*http.Request) (Landscape, ReachOut, error) {
		return Landscape{
			Applications: []Application{app("a1", "Billing")},
			Processes:    []Process{proc(1, "invoice", "Invoice", "a1")},
		}, nil, nil
	}, nil, 0)

	rec := httptest.NewRecorder()
	mesh.HandleGraph(rec, httptest.NewRequest(http.MethodGet, "/api/v1/panorama/mesh", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body)
	}
	var g Graph
	if err := json.Unmarshal(rec.Body.Bytes(), &g); err != nil {
		t.Fatalf("decode: %v (%s)", err, rec.Body)
	}
	if len(g.Nodes) != 2 {
		t.Errorf("nodes = %d, want an application and its process", len(g.Nodes))
	}
}

// TestMeshHandleGraphReportsACollectorFailure: the collector reads the stores, so
// its failure is a server fault and must not be answered with an empty graph. An
// empty mesh means "nothing is deployed", which is a different and reassuring lie.
func TestMeshHandleGraphReportsACollectorFailure(t *testing.T) {
	mesh := NewMesh(meshLoop(t), func(*http.Request) (Landscape, ReachOut, error) {
		return Landscape{}, nil, errors.New("store is on fire")
	}, nil, 0)

	rec := httptest.NewRecorder()
	mesh.HandleGraph(rec, httptest.NewRequest(http.MethodGet, "/api/v1/panorama/mesh", nil))

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", rec.Code)
	}
	if body := rec.Body.String(); !strings.Contains(body, "store is on fire") {
		t.Errorf("body = %s, want the underlying cause", body)
	}
}

// TestMeshHandleGraphRefusesWhenTheLoopIsClosing guards the sharp edge in
// runloop.Do: on a closing loop it declines to run the closure at all and returns,
// leaving every result variable at its zero value. Read naively that is an empty
// Landscape with a nil error — indistinguishable from a healthy server on which
// nothing is deployed. The mesh must answer "ask again", never "there is nothing
// here", because the second is a claim and it would be false.
func TestMeshHandleGraphRefusesWhenTheLoopIsClosing(t *testing.T) {
	collected := false
	mesh := NewMesh(stoppedLoop(), func(*http.Request) (Landscape, ReachOut, error) {
		collected = true
		return Landscape{}, nil, nil
	}, nil, 0)

	rec := httptest.NewRecorder()
	mesh.HandleGraph(rec, httptest.NewRequest(http.MethodGet, "/api/v1/panorama/mesh", nil))

	if collected {
		t.Fatal("the collector ran on a closed loop; this test no longer covers what it claims")
	}
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503 — an unanswered read is not an empty graph", rec.Code)
	}
	if strings.Contains(rec.Body.String(), `"nodes"`) {
		t.Errorf("body = %s, want a refusal rather than a graph", rec.Body)
	}
}

// TestMeshAppliesItsConfiguredSizeBudget proves the budget reaches DeriveGraph
// rather than sitting unused on the service.
func TestMeshAppliesItsConfiguredSizeBudget(t *testing.T) {
	mesh := NewMesh(meshLoop(t), func(*http.Request) (Landscape, ReachOut, error) {
		land := Landscape{Applications: []Application{app("a1", "Billing")}}
		for i := 1; i <= 10; i++ {
			land.Processes = append(land.Processes, proc(uint64(i), "p", "P", "a1"))
		}
		return land, nil, nil
	}, nil, 5)

	rec := httptest.NewRecorder()
	mesh.HandleGraph(rec, httptest.NewRequest(http.MethodGet, "/api/v1/panorama/mesh", nil))

	var g Graph
	if err := json.Unmarshal(rec.Body.Bytes(), &g); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !g.Clustered {
		t.Errorf("clustered = false; the service did not pass its budget through")
	}
}

// TestMeshAsksPeersOffTheLoop is invariant I3 asserted where it could be broken.
//
// A landscape now reaches outside this process to ask deployment targets whether
// they are answering, and the single-writer run loop is the one place that call must
// never happen: hold it for the eight seconds a dead peer takes to time out and
// every other design-time request on the server waits behind it.
//
// The check is not "did it get called" but "was the loop free while it ran" — so the
// reach-out itself does a Loop.Do, which is the deadlock a same-goroutine call would
// produce and which passes trivially once the call is off the loop.
func TestMeshAsksPeersOffTheLoop(t *testing.T) {
	loop := meshLoop(t)
	var reached bool
	var loopWasFree bool

	mesh := NewMesh(loop, func(*http.Request) (Landscape, ReachOut, error) {
		return Landscape{
				Applications: []Application{app("a1", "Billing")},
				Targets:      []Target{{ID: "t1", Name: "Production"}},
			}, func(_ context.Context, land *Landscape) {
				reached = true
				// If this ran on the loop, this would deadlock rather than return.
				loop.Do(func() { loopWasFree = true })
				land.Targets[0].State = StateUnreachable
				land.Targets[0].Reason = "This peer could not be reached."
			}, nil
	}, nil, 0)

	rec := httptest.NewRecorder()
	mesh.HandleGraph(rec, httptest.NewRequest(http.MethodGet, "/api/v1/panorama/mesh", nil))

	if !reached {
		t.Fatal("the reach-out never ran; a landscape that cannot ask its peers reports nothing about them")
	}
	if !loopWasFree {
		t.Fatal("the run loop was not free while the peers were asked")
	}

	var g Graph
	if err := json.NewDecoder(rec.Body).Decode(&g); err != nil {
		t.Fatalf("decode: %v", err)
	}
	// The answer reached the picture, which is the only thing the caller sees.
	var found bool
	for _, n := range g.Nodes {
		if n.Kind != KindTarget {
			continue
		}
		found = true
		if n.State != StateUnreachable || n.Severity != SeverityAttention {
			t.Errorf("target node = %q/%q, want unreachable and attention", n.State, n.Severity)
		}
	}
	if !found {
		t.Fatal("no target node in the graph")
	}
	// And with a peer drawn, the payload stops declaring those states unproducible.
	if len(g.Status.Unavailable) != 0 {
		t.Errorf("Unavailable = %#v on a landscape that drew a peer", g.Status.Unavailable)
	}
}

// TestMeshWithoutPeersNeedsNoReachOut. A collector with nothing outside to ask hands
// back no closure, and the service must not invent a phase for it — the nil is the
// ordinary case on a server with no deployment targets configured.
func TestMeshWithoutPeersNeedsNoReachOut(t *testing.T) {
	mesh := NewMesh(meshLoop(t), func(*http.Request) (Landscape, ReachOut, error) {
		return Landscape{Applications: []Application{app("a1", "Billing")}}, nil, nil
	}, nil, 0)

	rec := httptest.NewRecorder()
	mesh.HandleGraph(rec, httptest.NewRequest(http.MethodGet, "/api/v1/panorama/mesh", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body)
	}
	var g Graph
	if err := json.NewDecoder(rec.Body).Decode(&g); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(g.Status.Unavailable) != 2 {
		t.Errorf("Unavailable = %#v, want unreachable and stale declared", g.Status.Unavailable)
	}
}
