package panorama

import (
	"encoding/json"
	"strings"
	"testing"
)

func ref(id, name string, canView bool) ResourceRef {
	return ResourceRef{ID: id, Name: name, CanView: canView}
}

func resolvedValues(t *testing.T, res Resolution, element, key string) []ResolvedValue {
	t.Helper()
	for _, b := range res.Bindings {
		if b.ElementID == element && b.Key == key {
			return b.Values
		}
	}
	t.Fatalf("no resolved binding %s/%s in %#v", element, key, res.Bindings)
	return nil
}

func setOf(bindings ...Binding) BindingSet {
	return BindingSet{ContractVersion: BindingContractVersion, Bindings: bindings, Problems: []Problem{}}
}

// A binding holds an opaque id and nothing else (ADR-0189 §4); the name is the
// server's to supply at read time. A model that stored the name would be a second,
// stale copy of it — which is the thing the record exists to prevent.
func TestResolveBindingsSuppliesTheName(t *testing.T) {
	res := ResolveBindings(
		setOf(Binding{ElementID: "app-1", ElementType: "ApplicationComponent",
			Key: KeyApplicationID, Values: []string{"proj-abc"}}),
		Catalog{Applications: map[string]ResourceRef{"proj-abc": ref("proj-abc", "Billing", true)}},
	)

	values := resolvedValues(t, res, "app-1", KeyApplicationID)
	if len(values) != 1 || values[0].Status != StatusResolved || values[0].Name != "Billing" {
		t.Errorf("resolved value = %#v", values)
	}
	if res.Unresolved != 0 {
		t.Errorf("Unresolved = %d, want 0", res.Unresolved)
	}
}

// "Nothing here has that id" and "you may not see it" are different findings that
// are fixed in different places: one is a typo or a deleted resource, the other is a
// sharing decision. Collapsing them would send somebody to correct a model that is
// already correct.
func TestResolveBindingsSeparatesMissingFromForbidden(t *testing.T) {
	res := ResolveBindings(
		setOf(Binding{ElementID: "app-1", ElementType: "ApplicationComponent",
			Key: KeyApplicationID, Values: []string{"proj-hidden", "proj-gone"}}),
		Catalog{Applications: map[string]ResourceRef{
			"proj-hidden": ref("proj-hidden", "Payroll", false),
		}},
	)

	values := resolvedValues(t, res, "app-1", KeyApplicationID)
	if len(values) != 2 {
		t.Fatalf("values = %#v", values)
	}
	if values[0].Status != StatusForbidden {
		t.Errorf("hidden resource status = %q, want %q", values[0].Status, StatusForbidden)
	}
	if values[1].Status != StatusMissing {
		t.Errorf("absent resource status = %q, want %q", values[1].Status, StatusMissing)
	}
	if res.Unresolved != 2 {
		t.Errorf("Unresolved = %d, want both counted", res.Unresolved)
	}
}

// A forbidden resource discloses that the id resolves to something and nothing
// more. The name is what the sharing scope withholds, so the assertion is on the
// marshalled payload rather than on the rendering.
func TestForbiddenBindingNeverCarriesTheName(t *testing.T) {
	res := ResolveBindings(
		setOf(Binding{ElementID: "app-1", ElementType: "ApplicationComponent",
			Key: KeyApplicationID, Values: []string{"proj-hidden"}}),
		Catalog{Applications: map[string]ResourceRef{
			"proj-hidden": ref("proj-hidden", "HR Confidential", false),
		}},
	)

	body, err := json.Marshal(res)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if strings.Contains(string(body), "HR Confidential") {
		t.Errorf("resolution leaks a forbidden resource's name: %s", body)
	}
	// The id itself stays: it is in the caller's own model, which they can export.
	if !strings.Contains(string(body), "proj-hidden") {
		t.Errorf("resolution dropped the bound id: %s", body)
	}
}

// ADR-0189 §4: a missing or inaccessible resource stays an explicit unresolved
// binding rather than being silently removed. Dropping it would make a broken
// binding look like an absent one, and the model would then look correct.
func TestResolveBindingsNeverDropsAValue(t *testing.T) {
	res := ResolveBindings(
		setOf(Binding{ElementID: "app-1", ElementType: "ApplicationComponent",
			Key: KeyApplicationID, Values: []string{"a", "b", "c"}}),
		Catalog{Applications: map[string]ResourceRef{"b": ref("b", "Middle", true)}},
	)

	if got := resolvedValues(t, res, "app-1", KeyApplicationID); len(got) != 3 {
		t.Errorf("values = %#v, want all three kept in order", got)
	}
}

// Each key resolves against its own kind of resource. A process id looked up in the
// application catalog would report every binding as missing, which reads as a
// broken model rather than as a broken resolver.
func TestResolveBindingsUsesTheCatalogForEachKey(t *testing.T) {
	res := ResolveBindings(
		setOf(
			Binding{ElementID: "bp-1", ElementType: "BusinessProcess", Key: KeyProcessID, Values: []string{"order"}},
			Binding{ElementID: "svc-1", ElementType: "ApplicationService", Key: KeyConnectorID, Values: []string{"c1"}},
			Binding{ElementID: "node-1", ElementType: "Node", Key: KeyDeploymentTargetID, Values: []string{"t1"}},
			Binding{ElementID: "art-1", ElementType: "Artifact", Key: KeyReleaseID, Values: []string{"r1"}},
		),
		Catalog{
			Processes:  map[string]ResourceRef{"order": ref("order", "Fulfil order", true)},
			Connectors: map[string]ResourceRef{"c1": ref("c1", "ops-mail", true)},
			Targets:    map[string]ResourceRef{"t1": ref("t1", "Production", true)},
			Releases:   map[string]ResourceRef{"r1": ref("r1", "Billing v4", true)},
		},
	)

	for _, want := range []struct{ element, key, name string }{
		{"bp-1", KeyProcessID, "Fulfil order"},
		{"svc-1", KeyConnectorID, "ops-mail"},
		{"node-1", KeyDeploymentTargetID, "Production"},
		{"art-1", KeyReleaseID, "Billing v4"},
	} {
		values := resolvedValues(t, res, want.element, want.key)
		if len(values) != 1 || values[0].Name != want.name {
			t.Errorf("%s/%s = %#v, want %q", want.element, want.key, values, want.name)
		}
	}
}

// The extractor's problems travel with the resolution: a caller asking "what is
// bound here" needs to hear about the declarations that were refused as much as
// about the ones that stood.
func TestResolveBindingsCarriesExtractionProblems(t *testing.T) {
	set := setOf()
	set.Problems = append(set.Problems, Problem{Severity: "error", Message: "bad key"})

	res := ResolveBindings(set, Catalog{})
	if len(res.Problems) != 1 {
		t.Errorf("problems = %#v, want the extractor's carried through", res.Problems)
	}
}

// An empty model resolves to empty collections rather than nulls: the API's
// consumers iterate them.
func TestResolveBindingsMarshalsEmptyCollections(t *testing.T) {
	body, err := json.Marshal(ResolveBindings(setOf(), Catalog{}))
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if !strings.Contains(string(body), `"bindings":[]`) || !strings.Contains(string(body), `"problems":[]`) {
		t.Errorf("empty resolution marshals as %s", body)
	}
}
