package panorama

import (
	"encoding/json"
	"strings"
	"testing"
)

const observedAt = int64(1_700_000_000)

// binds builds one binding on one element. (bound() is taken by the extractor's
// own tests, which read a set rather than build one.)
func binds(elementID, elementType, key string, values ...string) Binding {
	return Binding{ElementID: elementID, ElementType: elementType, Key: key, Values: values}
}

// observationFor finds the observation for one element and value.
func observationFor(t *testing.T, doc ObservationDocument, elementID, value string) Observation {
	t.Helper()
	for _, o := range doc.Observations {
		if o.ElementID == elementID && o.Value == value {
			return o
		}
	}
	t.Fatalf("no observation for %q/%q in %#v", elementID, value, doc.Observations)
	return Observation{}
}

// TestObserveProjectsFactsOntoTheElementsThatBindThem is the whole slice in one
// case: the model declares what the architecture is, the server says what it is
// doing, and the two meet on the binding without the document learning anything.
func TestObserveProjectsFactsOntoTheElementsThatBindThem(t *testing.T) {
	doc := Observe(BindingSet{
		ContractVersion: BindingContractVersion,
		Bindings: []Binding{
			binds("e-app", "ApplicationComponent", KeyApplicationID, "app-1"),
			binds("e-proc", "ApplicationProcess", KeyProcessID, "invoice"),
		},
	}, Facts{
		Applications: map[string]Fact{"app-1": {
			Source: SourceDeployments, State: StateHealthy, Reason: "3 process(es) deployed.",
			Detail: map[string]string{"processes": "3"},
		}},
		Processes: map[string]Fact{"invoice": {
			Source: SourceInstances, State: StateDegraded, Reason: "2 token(s) are parked.",
		}},
	}, observedAt)

	if doc.ContractVersion != BindingContractVersion || doc.ObservedAt != observedAt {
		t.Fatalf("document header = %d/%d", doc.ContractVersion, doc.ObservedAt)
	}
	app := observationFor(t, doc, "e-app", "app-1")
	if app.State != StateHealthy || app.Severity != SeverityOK || app.Source != SourceDeployments {
		t.Errorf("application observation = %+v", app)
	}
	if app.Detail["processes"] != "3" {
		t.Errorf("detail = %v, want the number behind the sentence", app.Detail)
	}
	proc := observationFor(t, doc, "e-proc", "invoice")
	if proc.State != StateDegraded || proc.Severity != SeverityAttention {
		t.Errorf("process observation = %+v", proc)
	}
	// The class is a reading aid and never a replacement: both travel.
	if proc.Reason == "" {
		t.Error("a finding arrived without the sentence behind it")
	}
	if doc.Summary.OK != 1 || doc.Summary.Attention != 1 {
		t.Errorf("summary = %+v", doc.Summary)
	}
}

// TestObserveKeepsTwoDifferentSilencesApart is the honesty this projection turns
// on. "Nobody looked" and "somebody looked and found nothing" are both unbound
// states, and collapsing them would send an architect to fix a model that is
// correct — or leave them believing a resource was checked when it was not.
func TestObserveKeepsTwoDifferentSilencesApart(t *testing.T) {
	doc := Observe(BindingSet{Bindings: []Binding{
		binds("e-job", "ApplicationService", KeyJobType, "sendMail"),
		binds("e-app", "ApplicationComponent", KeyApplicationID, "app-gone"),
	}}, Facts{
		// Applications are observed and hold none; job types are not observed at all.
		Applications: map[string]Fact{},
	}, observedAt)

	unobserved := observationFor(t, doc, "e-job", "sendMail")
	if unobserved.State != StateUnbound || unobserved.Source != SourceNone {
		t.Fatalf("an unobserved kind = %+v", unobserved)
	}
	if !strings.Contains(unobserved.Reason, KeyJobType) {
		t.Errorf("reason = %q, want it to name the kind nothing observes", unobserved.Reason)
	}

	absent := observationFor(t, doc, "e-app", "app-gone")
	if absent.State != StateUnbound {
		t.Fatalf("an absent resource = %+v", absent)
	}
	if absent.Reason == unobserved.Reason {
		t.Error("an unobserved kind and an absent resource give the same answer")
	}
	if !strings.Contains(absent.Reason, "nothing to observe") {
		t.Errorf("reason = %q, want it to say the resource is not here", absent.Reason)
	}
	// Neither is a severity: most elements of a young landscape are unbound, and
	// colouring them as problems makes the whole model a problem.
	if unobserved.Severity != SeverityUnknown || absent.Severity != SeverityUnknown {
		t.Errorf("an unbound observation carries a severity: %q/%q",
			unobserved.Severity, absent.Severity)
	}
}

// TestObserveNeverDropsABoundValue: an element whose binding cannot be observed
// must stay in the document. Removing it would make a model with unanswerable
// bindings look like a model with nothing wrong.
func TestObserveNeverDropsABoundValue(t *testing.T) {
	set := BindingSet{Bindings: []Binding{
		binds("e-1", "Node", KeyRuntimeID, "rt-here", "rt-elsewhere"),
		binds("e-2", "ApplicationComponent", KeyApplicationID, "app-1"),
	}}
	doc := Observe(set, Facts{
		Runtimes: map[string]Fact{"rt-here": {Source: SourceNode, State: StateHealthy}},
	}, observedAt)

	if len(doc.Observations) != 3 {
		t.Fatalf("%d observations for 3 bound values: %#v", len(doc.Observations), doc.Observations)
	}
	if got := observationFor(t, doc, "e-1", "rt-elsewhere"); got.State != StateUnbound {
		t.Errorf("the unobservable runtime = %+v, want unbound rather than absent", got)
	}
}

// TestObserveDeclaresWhatItCannotObserve, from the same list the landscape mesh
// publishes. Two surfaces disagreeing about what is unwatched would be worse than
// either of them being silent.
func TestObserveDeclaresWhatItCannotObserve(t *testing.T) {
	doc := Observe(BindingSet{}, Facts{}, observedAt)

	declared := map[string]bool{}
	for _, u := range doc.Unavailable {
		declared[u.State] = true
	}
	for _, state := range []string{StateUnreachable, StateStale} {
		if !declared[state] {
			t.Errorf("state %q is not declared unavailable: %#v", state, doc.Unavailable)
		}
	}
	if len(doc.Unavailable) != len(unobservable) {
		t.Errorf("the document and the mesh declare different lists: %d vs %d",
			len(doc.Unavailable), len(unobservable))
	}
	// An empty model still answers with collections rather than nulls: the renderer
	// iterates them.
	raw, err := json.Marshal(doc)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if strings.Contains(string(raw), `"observations":null`) {
		t.Errorf("empty document carries nulls: %s", raw)
	}
}

// TestObserveIsDeterministic: this document is something people diff between two
// deployments, so two identical requests must produce identical bytes. Map
// iteration order is random in Go, and an answer that reshuffled would be
// undiffable.
func TestObserveIsDeterministic(t *testing.T) {
	set := BindingSet{Bindings: []Binding{
		binds("e-z", "Node", KeyRuntimeID, "rt-1"),
		binds("e-a", "ApplicationComponent", KeyApplicationID, "app-2", "app-1"),
	}}
	facts := Facts{
		Applications: map[string]Fact{
			"app-1": {Source: SourceDeployments, State: StateHealthy},
			"app-2": {Source: SourceDeployments, State: StateNotReady},
		},
		Runtimes: map[string]Fact{"rt-1": {Source: SourceNode, State: StateHealthy}},
	}
	first, err := json.Marshal(Observe(set, facts, observedAt))
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	for range 12 {
		again, err := json.Marshal(Observe(set, facts, observedAt))
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		if string(again) != string(first) {
			t.Fatalf("two identical requests differ:\n%s\n%s", first, again)
		}
	}
	// Sorted by element, then key, then value — the order somebody reading down the
	// page expects, not the order the document happened to declare them in.
	if doc := Observe(set, facts, observedAt); doc.Observations[0].ElementID != "e-a" ||
		doc.Observations[0].Value != "app-1" {
		t.Errorf("first observation = %+v, want the lowest element and value", doc.Observations[0])
	}
}

// TestObserveBoundsOneObservationsDetail keeps a per-element payload from being
// where free-form text accumulates. A model can bind hundreds of elements, so the
// per-observation size is what decides whether the whole document renders.
func TestObserveBoundsOneObservationsDetail(t *testing.T) {
	detail := map[string]string{"long": strings.Repeat("x", maxObservationDetailLen+50)}
	for i := range maxObservationDetail + 5 {
		detail[string(rune('a'+i))] = "v"
	}
	doc := Observe(BindingSet{Bindings: []Binding{
		binds("e-1", "ApplicationComponent", KeyApplicationID, "app-1"),
	}}, Facts{Applications: map[string]Fact{"app-1": {
		Source: SourceDeployments, State: StateHealthy, Detail: detail,
	}}}, observedAt)

	got := observationFor(t, doc, "e-1", "app-1")
	if len(got.Detail) != maxObservationDetail {
		t.Fatalf("detail holds %d entries, want it bounded at %d", len(got.Detail), maxObservationDetail)
	}
	for key, value := range got.Detail {
		if len(value) > maxObservationDetailLen {
			t.Errorf("detail[%q] is %d characters, past the bound", key, len(value))
		}
	}
	// Dropped in sorted-key order, so an over-bound detail loses the same entries
	// every time rather than a different arbitrary subset per request.
	if _, kept := got.Detail["a"]; !kept {
		t.Errorf("detail = %v, want the lowest keys kept", got.Detail)
	}
}

// TestObserveCarriesTheExtractorsProblems: a declaration that was refused is as
// much a finding as one that resolved, and this document is where somebody looking
// at the live view would notice it.
func TestObserveCarriesTheExtractorsProblems(t *testing.T) {
	doc := Observe(BindingSet{
		Problems: []Problem{{Severity: "warning", Message: `element "e-1" declares an unknown key`}},
	}, Facts{}, observedAt)

	if len(doc.Problems) != 1 || !strings.Contains(doc.Problems[0].Message, "unknown key") {
		t.Fatalf("problems = %#v, want the extractor's carried through", doc.Problems)
	}
}

// TestFactsRouteEveryBindingKindToItsOwnLookup keeps each kind resolving against
// the source that actually observes it. Looking a process id up among applications
// would report every binding as unobserved — a model that looks disconnected
// rather than a projection that looked in the wrong drawer.
func TestFactsRouteEveryBindingKindToItsOwnLookup(t *testing.T) {
	mark := func(id string) map[string]Fact {
		return map[string]Fact{"x": {Source: id, State: StateHealthy}}
	}
	facts := Facts{
		Applications: mark("applications"), Processes: mark("processes"),
		Connectors: mark("connectors"), JobTypes: mark("jobTypes"),
		Runtimes: mark("runtimes"), Targets: mark("targets"), Releases: mark("releases"),
	}
	for key, want := range map[string]string{
		KeyApplicationID:      "applications",
		KeyProcessID:          "processes",
		KeyConnectorID:        "connectors",
		KeyJobType:            "jobTypes",
		KeyRuntimeID:          "runtimes",
		KeyDeploymentTargetID: "targets",
		KeyReleaseID:          "releases",
	} {
		if got := facts.forKey(key)["x"].Source; got != want {
			t.Errorf("forKey(%q) resolved against %q, want %q", key, got, want)
		}
	}
	// An unknown key has no lookup at all, which is what makes it report "nothing
	// observes this" rather than "no such resource".
	if facts.forKey("atlas.invented") != nil {
		t.Error("an unknown binding key resolved against something")
	}
}
