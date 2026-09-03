package panorama

import (
	"strings"
	"testing"
)

// landscapeForExport is a landscape with one of everything the export has to decide
// about: two applications, a call between processes, a worker and a decision that a
// process uses, a call target nobody provides, a peer, and one dependency this
// reader may not see.
func landscapeForExport() Landscape {
	return Landscape{
		Applications: []Application{app("billing", "Billing"), app("onboarding", "Onboarding")},
		Processes: []Process{
			{
				Key: 1, ProcessID: "invoice", Name: "Invoice", Version: 2,
				ApplicationID: "billing", CanView: true,
				Calls: []Call{
					{ElementID: "Task_Dun", CalledProcessID: "dunning", TargetKey: 2},
					{ElementID: "Task_Arch", CalledProcessID: "archive"},
					{ElementID: "Task_Secret", CalledProcessID: "ledger", TargetKey: 9},
				},
				Workers:   []WorkerUse{{ElementID: "Task_Mail", Name: "ops-mail", TargetID: "w-mail"}},
				Decisions: []string{"credit"},
				// Observation state, which the exported document must not carry.
				State: "degraded", Reason: "4 token(s) are parked.", Incidents: 4,
			},
			proc(2, "dunning", "Dunning", "billing"),
			// Visible to nobody: it is the reason a restricted placeholder appears.
			{Key: 9, ProcessID: "ledger", Name: "Ledger", Version: 1, ApplicationID: "onboarding"},
		},
		Workers: []Worker{{
			ID: "w-mail", Name: "ops-mail", Type: "mail", CanView: true,
			Endpoint: "https://mail.internal.example/send", CredentialsRef: "cred-mail",
		}},
		Decisions: []Decision{{ID: "credit", Name: "Credit score", CanView: true}},
		Targets:   []Target{{ID: "t-prod", Name: "Production", State: StateUnreachable}},
	}
}

func exportedLandscape(t *testing.T) string {
	t.Helper()
	graph := DeriveGraph(landscapeForExport(), Options{ObservedAt: 1_700_000_000})
	return string(ExportArchiMate(graph, ArchiMateExport{
		Instance: "atlas.example.test", GeneratedAt: 1_700_000_000,
	}))
}

// The check that matters most, and the cheapest one to have: Atlas's own importer
// is the strictest reader this document will meet in this repository, so a document
// Atlas generates has to be one Atlas would accept. It catches a malformed envelope,
// a duplicate identifier, an element type that is not in the standard's vocabulary,
// and a relationship pointing at something that is not there — which is most of the
// ways a generator goes wrong.
func TestExportedLandscapeIsAModelAtlasWouldImport(t *testing.T) {
	result := Validate([]byte(exportedLandscape(t)))
	if !result.Valid {
		t.Fatalf("Atlas will not import its own export: %#v", result.Problems)
	}
	if result.Namespace != ExchangeNamespace {
		t.Errorf("namespace = %q", result.Namespace)
	}
	if result.Elements == 0 || result.Relationships == 0 {
		t.Errorf("counts = %d elements, %d relationships; want a populated model",
			result.Elements, result.Relationships)
	}
}

// Each mesh kind is written as the element type the served mapping names, so the
// picture and the file cannot come to disagree about what a node is.
func TestExportedElementsUseTheMappedTypes(t *testing.T) {
	xml := exportedLandscape(t)
	notation, ok := NotationByID(NotationArchiMate32)
	if !ok {
		t.Fatal("the ArchiMate notation is not in the table")
	}
	for _, want := range []struct{ name, kind string }{
		{"Billing", KindApplication},
		{"Invoice", KindProcess},
		{"ops-mail", KindWorker},
		{"Credit score", KindDecision},
		{"Production", KindTarget},
	} {
		element := elementBlock(t, xml, want.name)
		if got := notation.Types[want.kind].Type; !strings.Contains(element, `xsi:type="`+got+`"`) {
			t.Errorf("%s is not written as %s:\n%s", want.name, got, element)
		}
	}
}

// A dependency nothing provides is a statement about architecture — "the model
// declares this and it is not here" — so it belongs in an architecture document,
// wearing the type of the thing that is missing and saying that it is missing.
func TestExportedUnresolvedDependencyIsAnElementThatSaysItIsMissing(t *testing.T) {
	element := elementBlock(t, exportedLandscape(t), "archive")
	if !strings.Contains(element, `xsi:type="ApplicationProcess"`) {
		t.Errorf("an unresolved process is not typed as one:\n%s", element)
	}
	if !strings.Contains(element, "nothing on this server provides it") {
		t.Errorf("the element does not say it is missing:\n%s", element)
	}
}

// A restricted placeholder has no ArchiMate element: it stands for a resource this
// reader may not see, which is a fact about the reader rather than about the
// architecture. Leaving it out silently would be the drop ADR-0211 §8 forbids, so
// the document counts it and says the model is a part of the landscape.
func TestExportedRestrictedPlaceholderIsAbsentAndDeclared(t *testing.T) {
	xml := exportedLandscape(t)
	if strings.Contains(xml, "restricted") && !strings.Contains(xml, "may not see") {
		t.Errorf("the word restricted appears without the declaration")
	}
	for _, want := range []string{
		"points at a resource the exporting reader may not see",
		"a part of the landscape, not all of it",
		"relationship(s) had an end that is not in this model",
	} {
		if !strings.Contains(xml, want) {
			t.Errorf("documentation does not say %q", want)
		}
	}
	// And the invisible process itself is nowhere in the file, by either name.
	if strings.Contains(xml, "Ledger") || strings.Contains(xml, "ledger") {
		t.Error("a process this reader may not see is named in the export")
	}
}

// ADR-0189 §4's own binding keys, so a model exported here and imported back arrives
// with its bindings already resolving rather than needing to be re-bound by hand.
func TestExportedElementsCarryAtlasBindings(t *testing.T) {
	xml := exportedLandscape(t)
	for _, want := range []struct{ element, key, value string }{
		{"Billing", KeyApplicationID, "billing"},
		{"Invoice", KeyProcessID, "invoice"},
		{"ops-mail", KeyConnectorID, "w-mail"},
		{"Production", KeyDeploymentTargetID, "t-prod"},
	} {
		block := elementBlock(t, xml, want.element)
		if !strings.Contains(block, ">"+want.value+"<") {
			t.Errorf("%s does not carry %s = %s:\n%s", want.element, want.key, want.value, block)
		}
	}
	// Every property definition a property references exists, which is what makes
	// the keys readable rather than dangling ids.
	for _, key := range []string{KeyApplicationID, KeyProcessID, KeyConnectorID, KeyDeploymentTargetID} {
		if !strings.Contains(xml, "<name>"+key+"</name>") {
			t.Errorf("no propertyDefinition for %q", key)
		}
	}
}

// The relationship mapping, including the one reversal. ArchiMate's Serving runs
// from provider to consumer and the landscape's `uses` edge runs the other way, so
// the document has to say "the mail worker serves the invoice process" rather than
// the reverse — which would be a false claim in ArchiMate's own terms.
func TestExportedRelationshipsCarryDirectionAndType(t *testing.T) {
	xml := exportedLandscape(t)
	id := func(name string) string {
		block := elementBlock(t, xml, name)
		start := strings.Index(block, `identifier="`) + len(`identifier="`)
		return block[start : start+strings.Index(block[start:], `"`)]
	}
	billing, invoice, dunning, mail := id("Billing"), id("Invoice"), id("Dunning"), id("ops-mail")

	for _, want := range []struct{ what, source, target, kind string }{
		{"an application is assigned to its process", billing, invoice, "Assignment"},
		{"a call activity triggers", invoice, dunning, "Triggering"},
		// Reversed on purpose.
		{"a worker serves the process that uses it", mail, invoice, "Serving"},
	} {
		fragment := `source="` + want.source + `" target="` + want.target + `" xsi:type="` + want.kind + `"`
		if !strings.Contains(xml, fragment) {
			t.Errorf("%s: no relationship %s", want.what, fragment)
		}
	}
}

// The document is architecture, not a monitoring snapshot. A file that froze this
// morning's health would go on asserting it — which is the undated green picture
// ADR-0211 §10 exists to prevent, in another wrapper. Health lives on the image
// export, dated.
func TestExportedModelCarriesNoObservationState(t *testing.T) {
	// The model's own content, not the paragraph that explains why health is absent
	// from it — that paragraph has to name what it is leaving out.
	body := modelBody(t, exportedLandscape(t))
	for _, leak := range []string{"degraded", "unreachable", "parked", "incident", "critical", "attention"} {
		if strings.Contains(strings.ToLower(body), leak) {
			t.Errorf("the exported model carries observation state: %q", leak)
		}
	}
}

// A worker's endpoint and credential reference stay on the server, in every payload
// Panorama produces. An exported file is the one that travels furthest.
func TestExportedModelCarriesNoEndpointOrCredential(t *testing.T) {
	xml := exportedLandscape(t)
	for _, leak := range []string{"mail.internal.example", "cred-mail"} {
		if strings.Contains(xml, leak) {
			t.Errorf("the exported model leaks %q", leak)
		}
	}
	// And no address of any kind among the elements. The document's own root carries
	// the standard's namespace URLs, which is the only place a URL belongs here.
	if body := modelBody(t, xml); strings.Contains(body, "http") {
		t.Errorf("an address appears in the model body:\n%s", body)
	}
}

// The same landscape produces the same bytes, so the file can be committed and
// diffed: a change in it then means a change in the estate rather than a change in
// Go's map iteration order.
func TestExportIsDeterministic(t *testing.T) {
	first, second := exportedLandscape(t), exportedLandscape(t)
	if first != second {
		t.Error("two exports of one landscape differ")
	}
}

// It says what it is, before anything else it says. A generated document that could
// be taken for one somebody drew is the objection ADR-0211 §8 raised against
// exporting a projection at all.
func TestExportedModelSaysItWasNotDrawn(t *testing.T) {
	xml := exportedLandscape(t)
	for _, want := range []string{
		"DERIVED, NOT AUTHORED",
		"Generated by Atlas from its own resources on atlas.example.test",
		"mapping version 1",
		"editing it here does not change anything in Atlas",
		"What this vocabulary cannot carry:",
	} {
		if !strings.Contains(xml, want) {
			t.Errorf("the model does not say %q", want)
		}
	}
}

// An empty landscape is a document with no elements, not a malformed one: a server
// with nothing deployed still exports, and what it exports still opens.
func TestExportOfAnEmptyLandscapeIsStillAModel(t *testing.T) {
	xml := string(ExportArchiMate(DeriveGraph(Landscape{}, Options{}), ArchiMateExport{}))
	if result := Validate([]byte(xml)); !result.Valid {
		t.Fatalf("an empty landscape does not export a valid model: %#v", result.Problems)
	}
	if strings.Contains(xml, "<elements>") {
		t.Error("an empty elements block was written; leave it out instead")
	}
}

// A mesh id is namespaced with colons and an xsd:ID may not contain one, so every
// identifier is rewritten — and two different resources must never collapse onto
// one identifier, which in an exchange document silently merges them.
func TestExportedIdentifiersAreDistinctAndWellFormed(t *testing.T) {
	// Two nodes whose ids differ only where the rewrite replaces characters.
	graph := Graph{Nodes: []Node{
		{ID: "worker:a:b", Kind: KindWorker, Name: "First"},
		{ID: "worker:a-b", Kind: KindWorker, Name: "Second"},
	}}
	xml := string(ExportArchiMate(graph, ArchiMateExport{}))
	if result := Validate([]byte(xml)); !result.Valid {
		t.Fatalf("colliding ids did not survive: %#v", result.Problems)
	}
	if strings.Count(xml, "<element ") != 2 {
		t.Errorf("two nodes did not become two elements:\n%s", xml)
	}
}

// modelBody is the elements and relationships — the model itself, without the
// documentation that describes it.
func modelBody(t *testing.T, xml string) string {
	t.Helper()
	start := strings.Index(xml, "<elements>")
	end := strings.Index(xml, "<propertyDefinitions>")
	if start < 0 {
		t.Fatal("the export has no elements")
	}
	if end < 0 {
		end = len(xml)
	}
	return xml[start:end]
}

// elementBlock returns the <element> whose name is the given one.
func elementBlock(t *testing.T, xml, name string) string {
	t.Helper()
	at := strings.Index(xml, ">"+name+"</name>")
	if at < 0 {
		t.Fatalf("no element named %q in the export", name)
	}
	start := strings.LastIndex(xml[:at], "<element ")
	end := strings.Index(xml[at:], "</element>")
	if start < 0 || end < 0 {
		t.Fatalf("element %q is not enclosed", name)
	}
	return xml[start : at+end]
}
