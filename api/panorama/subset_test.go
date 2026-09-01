package panorama

import (
	"net/http"
	"strings"
	"testing"
)

// TestTheSubsetCoversWhatTheRecordAsksFor. ADR-0189 names the starting scope:
// Capability, Business Process, the core Application layer, and the Technology
// elements needed to model artifacts, nodes, services and networks.
func TestTheSubsetCoversWhatTheRecordAsksFor(t *testing.T) {
	have := map[string]ElementKind{}
	for _, kind := range AuthorableElements() {
		have[kind.Type] = kind
	}
	for _, want := range []string{
		"Capability", "BusinessProcess",
		"ApplicationComponent", "ApplicationService", "ApplicationInterface", "DataObject",
		"Artifact", "Node", "TechnologyService", "CommunicationNetwork",
	} {
		if _, found := have[want]; !found {
			t.Errorf("the subset does not author %s, which the record names", want)
		}
	}
	// Every element carries the layer and aspect the rules are written in terms of;
	// one missing would silently make a whole row of the matrix behave as if the
	// element were in no layer at all.
	for _, kind := range AuthorableElements() {
		if kind.Label == "" || layerRank(kind.Layer) < 0 {
			t.Errorf("%s has no label or an unknown layer: %+v", kind.Type, kind)
		}
		switch kind.Aspect {
		case AspectActive, AspectBehavior, AspectPassive:
		default:
			t.Errorf("%s has aspect %q, which no rule understands", kind.Type, kind.Aspect)
		}
	}
}

// TestTheMatrixMatchesTheStandardsOwnExamples. These are the pairs the ArchiMate
// specification uses to explain each relationship, so they are the cases most worth
// pinning: if the rules drift, they drift here first.
func TestTheMatrixMatchesTheStandardsOwnExamples(t *testing.T) {
	allowed := []struct{ relationship, source, target, why string }{
		{"Assignment", "ApplicationComponent", "ApplicationFunction",
			"active structure performs behaviour"},
		{"Assignment", "Node", "Artifact",
			"an artifact is deployed on a node"},
		{"Assignment", "BusinessRole", "BusinessProcess",
			"a role performs a process"},
		{"Realization", "ApplicationComponent", "ApplicationService",
			"a component realizes the service it offers"},
		{"Realization", "ApplicationProcess", "BusinessService",
			"a lower layer realizes a higher one"},
		{"Realization", "Artifact", "ApplicationService",
			"technology realizes application behaviour"},
		{"Serving", "ApplicationService", "BusinessProcess",
			"a service serves the process that uses it"},
		{"Serving", "TechnologyService", "ApplicationComponent",
			"technology serves the application above it"},
		{"Access", "ApplicationProcess", "DataObject",
			"behaviour reads or writes something passive"},
		{"Triggering", "BusinessProcess", "BusinessProcess",
			"one piece of behaviour causes another"},
		{"Flow", "ApplicationFunction", "ApplicationProcess",
			"something passes between two pieces of behaviour"},
		{"Composition", "ApplicationComponent", "ApplicationComponent",
			"a whole and its parts are the same kind of thing"},
		{"Specialization", "BusinessProcess", "BusinessProcess",
			"a kind of the same type"},
		{"Association", "BusinessActor", "DataObject",
			"association relates anything to anything"},
	}
	for _, tc := range allowed {
		if ok, refusal := MayConnect(tc.relationship, tc.source, tc.target); !ok {
			t.Errorf("%s %s→%s was refused (%s), but %s",
				tc.relationship, tc.source, tc.target, refusal.Message, tc.why)
		}
	}

	refused := []struct{ relationship, source, target, why string }{
		{"Composition", "ApplicationComponent", "BusinessProcess",
			"a business process is not part of an application component"},
		{"Composition", "ApplicationComponent", "ApplicationService",
			"structure and behaviour are not parts of each other"},
		{"Assignment", "ApplicationService", "ApplicationComponent",
			"assignment runs from the active thing to the behaviour, not back"},
		{"Assignment", "ApplicationComponent", "DataObject",
			"deployment onto passive structure is a technology-layer relationship"},
		{"Access", "ApplicationComponent", "DataObject",
			"a component does not access data; its behaviour does"},
		{"Access", "ApplicationProcess", "ApplicationService",
			"access runs to something passive"},
		{"Triggering", "ApplicationComponent", "ApplicationComponent",
			"a component does not trigger a component; its behaviour does"},
		{"Realization", "BusinessProcess", "ApplicationComponent",
			"realization runs from the concrete to the abstract, not upward to downward"},
		{"Specialization", "ApplicationComponent", "ApplicationService",
			"an element can only specialize the same type"},
		{"Serving", "DataObject", "ApplicationProcess",
			"something passive does not serve"},
		{"Serving", "BusinessProcess", "Node",
			"serving does not run downhill; the business does not serve the technology"},
	}
	for _, tc := range refused {
		ok, refusal := MayConnect(tc.relationship, tc.source, tc.target)
		if ok {
			t.Errorf("%s %s→%s was allowed, but %s", tc.relationship, tc.source, tc.target, tc.why)
			continue
		}
		if refusal.Reason != RefusedByNotation {
			t.Errorf("%s %s→%s refused as %q, want it named as the notation's rule",
				tc.relationship, tc.source, tc.target, refusal.Reason)
		}
		// The message has to teach, not just refuse: this canvas is where most people
		// will meet the rule.
		if !strings.Contains(refusal.Message, "→") || len(refusal.Message) < 40 {
			t.Errorf("%s %s→%s says only %q", tc.relationship, tc.source, tc.target, refusal.Message)
		}
	}
}

// TestOutOfSubsetIsNotTheSameAsForbidden. One is a limit of this build, the other
// is a fact about the notation, and they send somebody to entirely different
// places — one to a future slice, the other to a modelling mistake.
func TestOutOfSubsetIsNotTheSameAsForbidden(t *testing.T) {
	// A real ArchiMate element Atlas does not author yet.
	ok, refusal := MayConnect("Association", "Goal", "BusinessProcess")
	if ok || refusal.Reason != RefusedOutOfSubset {
		t.Errorf("an unauthored element = %v, %+v", ok, refusal)
	}
	if !strings.Contains(refusal.Message, "reading one is unaffected") &&
		!strings.Contains(refusal.Message, "does not author") {
		t.Errorf("the refusal does not say this is Atlas's limit: %q", refusal.Message)
	}

	// A real ArchiMate relationship Atlas does not author yet.
	ok, refusal = MayConnect("Influence", "Capability", "Capability")
	if ok || refusal.Reason != RefusedOutOfSubset {
		t.Errorf("an unauthored relationship = %v, %+v", ok, refusal)
	}

	// An unauthored element at either end, because a rule that only checked the
	// source would let somebody draw into something Atlas cannot create.
	ok, refusal = MayConnect("Association", "BusinessProcess", "Requirement")
	if ok || refusal.Reason != RefusedOutOfSubset {
		t.Errorf("an unauthored target = %v, %+v", ok, refusal)
	}
	if !strings.Contains(refusal.Message, "Requirement") {
		t.Errorf("the refusal does not name the element it cannot author: %q", refusal.Message)
	}

	// And the same pair, refused by the notation rather than by the subset.
	ok, refusal = MayConnect("Access", "ApplicationComponent", "DataObject")
	if ok || refusal.Reason != RefusedByNotation {
		t.Errorf("a notation refusal = %v, %+v", ok, refusal)
	}
}

// TestUnknownShapesAreNotSilentlyRanked. layerRank and notationMessage both have a
// fallback, and both are defensive rather than reachable today. They are checked
// because a fallback that returns something plausible is how a future element with
// a typo in its layer gets quietly treated as the most abstract thing in the model.
func TestUnknownShapesAreNotSilentlyRanked(t *testing.T) {
	if got := layerRank("motivation"); got != -1 {
		t.Errorf("layerRank of a layer outside the subset = %d, want it marked unknown", got)
	}
	// A relationship with a rule but no sentence still refuses in words rather than
	// with an empty string.
	message := notationMessage("Junction",
		ElementKind{Label: "A"}, ElementKind{Label: "B"})
	if !strings.Contains(message, "A → B") || !strings.Contains(message, "ArchiMate") {
		t.Errorf("an unlisted relationship refuses with %q", message)
	}
}

// TestNothingIsRelatedToItself. The standard permits a few reflexive
// relationships; Atlas does not author one, because on a canvas it is almost
// always a drop that missed rather than a statement.
func TestNothingIsRelatedToItself(t *testing.T) {
	ok, refusal := MayConnectElements("Triggering",
		"e-1", "BusinessProcess", "e-1", "BusinessProcess")
	if ok || refusal.Reason != RefusedSelfReference {
		t.Errorf("an element related to itself = %v, %+v", ok, refusal)
	}
	// Two *different* elements of the same type is the ordinary case and stays legal.
	if ok, refusal := MayConnectElements("Triggering",
		"e-1", "BusinessProcess", "e-2", "BusinessProcess"); !ok {
		t.Errorf("two processes may not be connected: %+v", refusal)
	}
}

// TestAllowedBetweenOffersOnlyWhatWillBeAccepted. The connect menu is built from
// this, so an entry in it that the write path would refuse is a promise the server
// breaks.
func TestAllowedBetweenOffersOnlyWhatWillBeAccepted(t *testing.T) {
	for _, source := range AuthorableElements() {
		for _, target := range AuthorableElements() {
			for _, relationship := range AllowedBetween(source.Type, target.Type) {
				if ok, refusal := MayConnect(relationship, source.Type, target.Type); !ok {
					t.Fatalf("the menu offers %s %s→%s, which is refused: %+v",
						relationship, source.Type, source.Type, refusal)
				}
			}
		}
	}
	// Association relates anything to anything, so no pair in the subset is ever
	// left with nothing to draw — a menu that is sometimes empty would read as "these
	// two cannot be related", which is not what the notation says.
	for _, source := range AuthorableElements() {
		for _, target := range AuthorableElements() {
			if len(AllowedBetween(source.Type, target.Type)) == 0 {
				t.Errorf("%s→%s offers nothing at all", source.Type, target.Type)
			}
		}
	}
}

// TestTheSubsetPublishesWhatItIsNot. ADR-0189 requires the UI to state the
// implemented subset and forbids claiming complete ArchiMate 3.2 authoring.
// Shipping the limits with the table is how that survives somebody who reads only
// the palette.
func TestTheSubsetPublishesWhatItIsNot(t *testing.T) {
	subset := AuthoringSubset()
	if subset.Version != SubsetVersion {
		t.Errorf("version = %d", subset.Version)
	}
	if len(subset.Limits) < 4 {
		t.Fatalf("limits = %+v, want the subset to say what it is not", subset.Limits)
	}
	joined := ""
	for _, limit := range subset.Limits {
		if limit.Reason == "" {
			t.Errorf("limit %q has no reason", limit.Limit)
		}
		joined += limit.Limit + " " + limit.Reason
	}
	for _, must := range []string{"not all of ArchiMate", "round-trips untouched", "motivation", "derived"} {
		if !strings.Contains(joined, must) {
			t.Errorf("the limits do not mention %q", must)
		}
	}

	// The matrix is precomputed for every pair, because the canvas needs an answer
	// during a drag and a round trip per pointer move is not an answer.
	elements := AuthorableElements()
	if want := len(elements) * len(elements); len(subset.Matrix) != want {
		t.Errorf("matrix holds %d pairs, want %d", len(subset.Matrix), want)
	}
	if got := subset.Matrix["ApplicationComponent>ApplicationService"]; len(got) == 0 {
		t.Error("the matrix has no entry for a component and the service it realizes")
	}
	// And it agrees with the function the server enforces with — one table, not two.
	for key, offered := range subset.Matrix {
		source, target, _ := strings.Cut(key, ">")
		for _, relationship := range offered {
			if ok, _ := MayConnect(relationship, source, target); !ok {
				t.Fatalf("the matrix offers %s for %s, which MayConnect refuses", relationship, key)
			}
		}
	}
}

// TestEveryDrawableRelationshipHasARule. A relationship in the menu with no rule
// behind it would be refused as out-of-subset the moment somebody drew it — an
// offer the server does not honour.
func TestEveryDrawableRelationshipHasARule(t *testing.T) {
	for _, kind := range DrawableRelationships() {
		if _, known := rules[kind.Type]; !known {
			t.Errorf("%s is offered but has no rule", kind.Type)
		}
		if kind.Label == "" || kind.Rule == "" {
			t.Errorf("%s is offered without a label or an explanation: %+v", kind.Type, kind)
		}
	}
	// And the other way: a rule nothing offers is unreachable.
	offered := map[string]bool{}
	for _, kind := range DrawableRelationships() {
		offered[kind.Type] = true
	}
	for relationship := range rules {
		if !offered[relationship] {
			t.Errorf("%s has a rule but is never offered", relationship)
		}
	}
}

// TestSubsetRouteServesTheOneTable. The canvas enforces the matrix during a drag
// and the server enforces it on write; this route is what keeps those the same
// table rather than two that drift.
func TestSubsetRouteServesTheOneTable(t *testing.T) {
	fx := newServiceFixture(t)
	rec := request(t, fx.service.HandleSubset, http.MethodGet, "/api/v1/panorama/subset", nil, http.StatusOK)

	var served Subset
	decodeResponse(t, rec, &served)
	if served.Version != SubsetVersion || len(served.Elements) == 0 || len(served.Relationships) == 0 {
		t.Fatalf("served subset = %+v", served)
	}
	// Every rule the browser will apply agrees with the one the server enforces
	// with, because they are the same table read twice.
	for key, offered := range served.Matrix {
		source, target, _ := strings.Cut(key, ">")
		for _, relationship := range offered {
			if ok, refusal := MayConnect(relationship, source, target); !ok {
				t.Fatalf("the served matrix offers %s for %s, which the server refuses: %+v",
					relationship, key, refusal)
			}
		}
	}
	if len(served.Limits) == 0 {
		t.Error("the served subset does not say what it is not")
	}
}

// TestSubsetNeedsNoModelAndDisclosesNone. The subset is a property of this build,
// so asking for it must not require a model or say anything about which ones exist.
func TestSubsetNeedsNoModelAndDisclosesNone(t *testing.T) {
	fx := newServiceFixture(t)
	if err := fx.store.Save(Model{
		ID: testModelID, ApplicationID: "hidden", Name: "Secret landscape",
		Notation: NotationArchiMate32, Revision: 1,
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	body := request(t, fx.service.HandleSubset, http.MethodGet,
		"/api/v1/panorama/subset", nil, http.StatusOK).Body.String()
	if strings.Contains(body, "Secret landscape") || strings.Contains(body, testModelID) {
		t.Error("the subset answer mentions a stored model")
	}
}
