package panorama

import (
	"encoding/json"
	"strings"
	"testing"
)

// elem builds one bound ArchiMate element for the overlay.
func elem(id, typ, name, key string, values ...string) ModelElement {
	return ModelElement{ElementID: id, ElementType: typ, Name: name, Key: key, Values: values}
}

func nodeProvenance(t *testing.T, g Graph, id string) string {
	t.Helper()
	return nodeByID(t, g, id).Provenance
}

// The landscape both the mesh and the overlay tests work from: one application
// with one process, and one configured connector it uses.
func overlayLandscape() Landscape {
	p := proc(1, "invoice", "Invoice", "a1")
	p.Workers = []WorkerUse{{ElementID: "Task_1", Name: "ops-mail", TargetID: "c1"}}
	return Landscape{
		Applications: []Application{app("a1", "Billing")},
		Processes:    []Process{p},
		Workers:      []Worker{{ID: "c1", Name: "ops-mail", Type: "mail", CanView: true}},
	}
}

// A resource a model binds to is known from both sides, and says so. This is the
// comparison ADR-0211 §1 names as the reason to overlay a model onto the mesh at
// all: without it you have two pictures and no relationship between them.
func TestOverlayMarksABoundResourceAsBoth(t *testing.T) {
	g := DeriveGraph(overlayLandscape(), Options{Overlays: []Overlay{{
		ModelID: "m1", ModelName: "Landscape",
		Elements: []ModelElement{elem("app-orders", "ApplicationComponent", "Order Service", KeyApplicationID, "a1")},
	}}})

	if got := nodeProvenance(t, g, "application:a1"); got != ProvenanceBoth {
		t.Errorf("provenance = %q, want %q", got, ProvenanceBoth)
	}
	node := nodeByID(t, g, "application:a1")
	// The Atlas name stays the node's name; the architect's name is carried beside
	// it, because the two are allowed to differ and the difference is informative.
	if node.Name != "Billing" || node.ModelName != "Order Service" {
		t.Errorf("names = %q / %q, want the Atlas name and the modeled one", node.Name, node.ModelName)
	}
	if node.ModelElementID != "app-orders" || node.ModelElementType != "ApplicationComponent" {
		t.Errorf("model element = %+v", node)
	}
}

// A resource nothing models keeps derived provenance and is counted, so "we have
// this and nobody wrote it down" is visible rather than merely absent from a list.
func TestOverlayCountsPresentButUnmodeled(t *testing.T) {
	g := DeriveGraph(overlayLandscape(), Options{Overlays: []Overlay{{
		ModelID: "m1", Elements: []ModelElement{elem("app-orders", "ApplicationComponent", "Order Service", KeyApplicationID, "a1")},
	}}})

	if got := nodeProvenance(t, g, "process:1"); got != ProvenanceDerived {
		t.Errorf("unmodeled process provenance = %q, want %q", got, ProvenanceDerived)
	}
	// The application is modeled; the process and the connector are not.
	if g.Unmodeled != 2 {
		t.Errorf("Unmodeled = %d, want the process and the connector", g.Unmodeled)
	}
}

// A model that declares something Atlas does not have gets a node of its own. That
// is the other half of the comparison, and it is the half a drawn-only view can
// never show: the architecture says this exists and the instance disagrees.
func TestOverlayAddsModeledButAbsentNodes(t *testing.T) {
	g := DeriveGraph(overlayLandscape(), Options{Overlays: []Overlay{{
		ModelID: "m1", ModelName: "Landscape",
		Elements: []ModelElement{elem("app-ghost", "ApplicationComponent", "Reporting", KeyApplicationID, "a-nope")},
	}}})

	node := nodeByID(t, g, "modeled:application:a-nope")
	if node.Provenance != ProvenanceModeled || node.Kind != KindApplication {
		t.Errorf("node = %+v, want a modeled application", node)
	}
	// Its name is the architect's: Atlas has none to offer.
	if node.Name != "Reporting" {
		t.Errorf("name = %q, want the modeled name", node.Name)
	}
	if g.Modeled != 1 {
		t.Errorf("Modeled = %d, want 1", g.Modeled)
	}
}

// A process is bound by BPMN process id while a derived process node is keyed by
// deployment key. Matching on the wrong one would report every modeled process as
// absent, which reads as a landscape-wide drift that is not there.
func TestOverlayMatchesAProcessByItsBPMNId(t *testing.T) {
	g := DeriveGraph(overlayLandscape(), Options{Overlays: []Overlay{{
		ModelID: "m1", Elements: []ModelElement{elem("bp-1", "BusinessProcess", "Fulfil", KeyProcessID, "invoice")},
	}}})

	if got := nodeProvenance(t, g, "process:1"); got != ProvenanceBoth {
		t.Errorf("provenance = %q, want the deployed process matched by its BPMN id", got)
	}
	if g.Modeled != 0 {
		t.Errorf("Modeled = %d, want the binding matched rather than reported absent", g.Modeled)
	}
}

func TestOverlayMatchesAWorker(t *testing.T) {
	g := DeriveGraph(overlayLandscape(), Options{Overlays: []Overlay{{
		ModelID: "m1", Elements: []ModelElement{elem("svc-1", "ApplicationService", "Mail", KeyConnectorID, "c1")},
	}}})

	if got := nodeProvenance(t, g, "worker:c1"); got != ProvenanceBoth {
		t.Errorf("provenance = %q, want both", got)
	}
}

// The mesh draws applications, processes and connectors. A binding to a release, a
// deployment target or a runtime is not absent — it is at an altitude this picture
// does not cover, and calling it absent would invent drift. It is counted
// separately so the number is visible rather than silently dropped.
func TestOverlayDoesNotCallOutOfScopeBindingsAbsent(t *testing.T) {
	g := DeriveGraph(overlayLandscape(), Options{Overlays: []Overlay{{
		ModelID: "m1",
		Elements: []ModelElement{
			elem("art-1", "Artifact", "Billing v4", KeyReleaseID, "rel-1"),
			elem("node-1", "Node", "Prod", KeyDeploymentTargetID, "t-1"),
			elem("node-2", "Node", "Runtime", KeyRuntimeID, "rt-1"),
		},
	}}})

	if g.Modeled != 0 {
		t.Errorf("Modeled = %d, want out-of-scope bindings not reported as absent", g.Modeled)
	}
	if g.OutOfScope != 3 {
		t.Errorf("OutOfScope = %d, want all three counted", g.OutOfScope)
	}
	for _, n := range g.Nodes {
		if strings.HasPrefix(n.ID, "modeled:") {
			t.Errorf("out-of-scope binding produced a node: %+v", n)
		}
	}
}

// Many-to-many survives the overlay: one component implemented by two applications
// marks both, rather than the first one only.
func TestOverlayMarksEveryBoundValue(t *testing.T) {
	land := overlayLandscape()
	land.Applications = append(land.Applications, app("a2", "Collections"))

	g := DeriveGraph(land, Options{Overlays: []Overlay{{
		ModelID: "m1", Elements: []ModelElement{
			elem("app-orders", "ApplicationComponent", "Order Service", KeyApplicationID, "a1", "a2")},
	}}})

	for _, id := range []string{"application:a1", "application:a2"} {
		if got := nodeProvenance(t, g, id); got != ProvenanceBoth {
			t.Errorf("%s provenance = %q, want both", id, got)
		}
	}
}

// Without any overlay nothing changes: the mesh stays what the first slices made
// it, and the comparison counts stay zero rather than reporting a landscape that
// is entirely unmodeled.
func TestOverlayAbsentLeavesTheMeshAlone(t *testing.T) {
	g := DeriveGraph(overlayLandscape(), Options{})

	if g.Modeled != 0 || g.Unmodeled != 0 || g.OutOfScope != 0 {
		t.Errorf("counts = %d/%d/%d, want all zero without an overlay",
			g.Modeled, g.Unmodeled, g.OutOfScope)
	}
	for _, n := range g.Nodes {
		if n.Provenance != ProvenanceDerived {
			t.Errorf("node %q provenance = %q without an overlay", n.ID, n.Provenance)
		}
	}
}

// A modeled node must not disclose a resource the caller cannot see. The overlay
// runs on the already-filtered landscape, so a binding to a hidden application
// finds no node and is reported as modeled-but-absent — which is what this caller
// can honestly be told.
func TestOverlayDoesNotRevealHiddenResources(t *testing.T) {
	land := overlayLandscape()
	land.Applications = append(land.Applications, Application{ID: "a-secret", Name: "HR Confidential"})

	g := DeriveGraph(land, Options{Overlays: []Overlay{{
		ModelID: "m1", Elements: []ModelElement{
			elem("app-hr", "ApplicationComponent", "People", KeyApplicationID, "a-secret")},
	}}})

	body, err := json.Marshal(g)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if strings.Contains(string(body), "HR Confidential") {
		t.Errorf("overlay leaked a hidden application's Atlas name: %s", body)
	}
	// The architect's own name for it is in the model the caller is reading, so it
	// is theirs to see; the Atlas name is not.
	if !strings.Contains(string(body), "People") {
		t.Errorf("modeled node lost its own name: %s", body)
	}
}
