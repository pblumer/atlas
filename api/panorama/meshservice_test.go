package panorama

import (
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
	mesh := NewMesh(meshLoop(t), func(*http.Request) (Landscape, error) {
		return Landscape{
			Applications: []Application{app("a1", "Billing")},
			Processes:    []Process{proc(1, "invoice", "Invoice", "a1")},
		}, nil
	}, 0)

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
	mesh := NewMesh(meshLoop(t), func(*http.Request) (Landscape, error) {
		return Landscape{}, errors.New("store is on fire")
	}, 0)

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
	mesh := NewMesh(stoppedLoop(), func(*http.Request) (Landscape, error) {
		collected = true
		return Landscape{}, nil
	}, 0)

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
	mesh := NewMesh(meshLoop(t), func(*http.Request) (Landscape, error) {
		land := Landscape{Applications: []Application{app("a1", "Billing")}}
		for i := 1; i <= 10; i++ {
			land.Processes = append(land.Processes, proc(uint64(i), "p", "P", "a1"))
		}
		return land, nil
	}, 5)

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
