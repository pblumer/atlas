package panorama

import "testing"

// The catalogue on the starmap (#1022, second half).
//
// A catalogue is the one part of Atlas that holds *composition* and *aggregation* as
// facts rather than as something an architect drew: a product is assembled from the
// services that provision themselves, and the catalogue record says which of them
// come with it and which are offered beside it. Those are edges the landscape can
// point at, which is the whole test ADR-0211 §1 applies to what it draws.
//
// The other half of why it belongs here is the edge that leaves the catalogue: a
// product names the BPMN process that provisions it, and a product whose process is
// not deployed is an order that will park with somebody waiting for a laptop. Until
// now nothing put those two facts on one picture.

// cat builds one visible catalogue offering the given items.
func cat(id, name string, items ...string) ProductCatalog {
	return ProductCatalog{ID: id, Name: name, CanView: true, Items: items}
}

// prod builds one visible product with its two processes.
func prod(id, name, home, provision, deprovision string) Product {
	return Product{
		ID: id, Name: name, HomeCatalog: home, CanView: true,
		ProvisionProcess: provision, DeprovisionProcess: deprovision,
	}
}

// TestACatalogueAndWhatItOffersAreDrawn is the shape of the whole feature: the
// catalogue is a node, every product it offers is a node, and the offering is an
// edge. Nothing here is a claim about health — a catalogue is a record, not a thing
// that can be asked how it is.
func TestACatalogueAndWhatItOffersAreDrawn(t *testing.T) {
	land := Landscape{
		Catalogs: []ProductCatalog{cat("cat_1", "Mobile devices", "phone", "case")},
		Products: []Product{
			prod("phone", "Apple iPhone", "cat_1", "", ""),
			prod("case", "Protective case", "cat_1", "", ""),
		},
	}

	g := DeriveGraph(land, Options{Subject: SubjectProducts})

	counts := kindsOf(g)
	if counts[KindCatalog] != 1 || counts[KindProduct] != 2 {
		t.Fatalf("drew %d catalogue(s) and %d product(s); want 1 and 2 — %#v", counts[KindCatalog], counts[KindProduct], g.Nodes)
	}
	node := nodeByID(t, g, "catalog:cat_1")
	if node.Name != "Mobile devices" || node.Provenance != ProvenanceDerived {
		t.Errorf("catalogue node is %#v", node)
	}
	// Nothing can be asked how a catalogue is, so it carries the neutral pair rather
	// than a finding: most of a young landscape is unobserved, and colouring it as a
	// problem makes the whole picture a problem (ADR-0211 §4).
	if node.State != StateUnbound || node.Severity != SeverityUnknown {
		t.Errorf("a catalogue carries an observation it cannot have: state %q severity %q", node.State, node.Severity)
	}
	if p := nodeByID(t, g, "product:phone"); p.State != StateUnbound || p.Severity != SeverityUnknown {
		t.Errorf("a product carries an observation it cannot have: state %q severity %q", p.State, p.Severity)
	}
	for _, id := range []string{"product:phone", "product:case"} {
		if !hasEdge(g, "catalog:cat_1", id, EdgeOffers) {
			t.Errorf("no offers edge to %s in %#v", id, g.Edges)
		}
	}
}

// TestTheArrangementIsDrawnAsItIsStored keeps the three structure answers apart on
// the picture. Included and optional are different promises to whoever orders, and a
// picture that drew them in one line would say the case comes with the phone.
func TestTheArrangementIsDrawnAsItIsStored(t *testing.T) {
	c := cat("cat_1", "Mobile devices", "package", "phone", "case", "sim")
	c.Edges = []CatalogEdge{
		{From: "package", To: "phone", Kind: EdgeComposition},
		{From: "package", To: "case", Kind: EdgeAggregation},
		{From: "phone", To: "sim", Kind: EdgeRequires},
		// Incompatibility, which this picture does not draw — see the test below.
		{From: "phone", To: "case", Kind: "excludes"},
	}
	land := Landscape{
		Catalogs: []ProductCatalog{c},
		Products: []Product{
			prod("package", "Package", "cat_1", "", ""),
			prod("phone", "Phone", "cat_1", "", ""),
			prod("case", "Case", "cat_1", "", ""),
			prod("sim", "SIM", "cat_1", "", ""),
		},
	}

	g := DeriveGraph(land, Options{Subject: SubjectProducts})

	for _, want := range []struct{ from, to, kind string }{
		{"product:package", "product:phone", EdgeComposition},
		{"product:package", "product:case", EdgeAggregation},
		{"product:phone", "product:sim", EdgeRequires},
	} {
		if !hasEdge(g, want.from, want.to, want.kind) {
			t.Errorf("no %s edge %s → %s in %#v", want.kind, want.from, want.to, g.Edges)
		}
	}
}

// TestIncompatibilityIsNotDrawnAsADependency.
//
// "These two must never be held by the same person" is the one catalogue edge that
// means the opposite of every other line on this canvas. Drawn in the same ink it
// would read as a dependency, and a reader would take "the clerk who creates a
// supplier must not approve payments to it" for "one needs the other".
func TestIncompatibilityIsNotDrawnAsADependency(t *testing.T) {
	c := cat("cat_1", "Rights", "create", "approve")
	c.Edges = []CatalogEdge{{From: "create", To: "approve", Kind: "excludes"}}
	land := Landscape{
		Catalogs: []ProductCatalog{c},
		Products: []Product{
			prod("create", "Create suppliers", "cat_1", "", ""),
			prod("approve", "Approve payments", "cat_1", "", ""),
		},
	}

	g := DeriveGraph(land, Options{Subject: SubjectProducts})

	for _, e := range g.Edges {
		if e.From == "product:create" && e.To == "product:approve" {
			t.Fatalf("incompatibility was drawn as %q, which reads as a dependency", e.Kind)
		}
	}
}

// TestAProductPointsAtTheProcessesThatProvisionIt is the edge that makes this worth
// drawing at all: it joins what is offered to what has to run.
func TestAProductPointsAtTheProcessesThatProvisionIt(t *testing.T) {
	land := Landscape{
		Applications: []Application{app("a1", "Billing")},
		Processes: []Process{
			proc(1, "provision-phone", "Provision a phone", "a1"),
			proc(2, "revoke-phone", "Revoke a phone", "a1"),
		},
		Catalogs: []ProductCatalog{cat("cat_1", "Mobile devices", "phone")},
		Products: []Product{prod("phone", "Phone", "cat_1", "provision-phone", "revoke-phone")},
	}

	g := DeriveGraph(land, Options{Subject: SubjectProducts})

	if !hasEdge(g, "product:phone", "process:1", EdgeUses) {
		t.Errorf("no edge to the provisioning process in %#v", g.Edges)
	}
	if !hasEdge(g, "product:phone", "process:2", EdgeUses) {
		t.Errorf("no edge to the deprovisioning process in %#v", g.Edges)
	}
}

// TestAProductWhoseProcessIsNotDeployedSaysSo.
//
// This is the finding the whole join exists for. A product bound to a process nobody
// deployed is orderable, and the order parks the moment somebody presses the button.
// The landscape already has a shape for "referenced, and nothing here provides it",
// and this is the same finding about a different referrer.
func TestAProductWhoseProcessIsNotDeployedSaysSo(t *testing.T) {
	land := Landscape{
		Catalogs: []ProductCatalog{cat("cat_1", "Mobile devices", "phone")},
		Products: []Product{prod("phone", "Phone", "cat_1", "provision-phone", "")},
	}

	g := DeriveGraph(land, Options{Subject: SubjectProducts})

	id := unresolvedNodeID(KindProcess, "provision-phone")
	node := nodeByID(t, g, id)
	if node.Kind != KindUnresolved || node.Name != "provision-phone" {
		t.Errorf("unresolved provisioning process is %#v", node)
	}
	if !hasEdge(g, "product:phone", id, EdgeUses) {
		t.Errorf("no edge from the product to what is missing, in %#v", g.Edges)
	}
}

// TestACatalogueOutsideTheCallersAccessIsAbsentWithItsProducts.
//
// A catalogue carries its audience, its approval rules and its price list, so who may
// look at one is a decision the catalogue already makes. The mesh honours it and does
// not re-decide it: a catalogue this caller does not maintain is not on their picture,
// and neither is a product only that catalogue offers.
func TestACatalogueOutsideTheCallersAccessIsAbsentWithItsProducts(t *testing.T) {
	hidden := cat("cat_2", "Somebody else's", "secret")
	hidden.CanView = false
	land := Landscape{
		Catalogs: []ProductCatalog{cat("cat_1", "Mine", "phone"), hidden},
		Products: []Product{
			prod("phone", "Phone", "cat_1", "", ""),
			{ID: "secret", Name: "Their product", HomeCatalog: "cat_2"},
		},
	}

	g := DeriveGraph(land, Options{Subject: SubjectProducts})

	for _, n := range g.Nodes {
		if n.Name == "Their product" || n.Name == "Somebody else's" {
			t.Fatalf("a catalogue outside the caller's access reached the picture: %#v", n)
		}
	}
	if counts := kindsOf(g); counts[KindProduct] != 1 {
		t.Errorf("drew %d products; want only the visible one — %#v", counts[KindProduct], g.Nodes)
	}
}

// TestAPartOutsideTheCallersAccessKeepsItsEdge.
//
// The other half of the same rule (ADR-0211 §3). A visible product assembled from one
// the reader may not see must not read as a product made of nothing: the edge is
// drawn to a placeholder that says only that something is there.
func TestAPartOutsideTheCallersAccessKeepsItsEdge(t *testing.T) {
	mine := cat("cat_1", "Mine", "package", "part")
	mine.Edges = []CatalogEdge{{From: "package", To: "part", Kind: EdgeComposition}}
	land := Landscape{
		Catalogs: []ProductCatalog{mine},
		Products: []Product{
			prod("package", "Package", "cat_1", "", ""),
			// Offered here, but the product itself is somebody else's to see.
			{ID: "part", Name: "Their part", HomeCatalog: "cat_2"},
		},
	}

	g := DeriveGraph(land, Options{Subject: SubjectProducts})

	if g.Restricted != 1 {
		t.Fatalf("restricted count is %d; want 1 — %#v", g.Restricted, g.Nodes)
	}
	if !hasEdge(g, "product:package", restrictedNodeID(1), EdgeComposition) {
		t.Errorf("the edge to the hidden part was dropped: %#v", g.Edges)
	}
	for _, n := range g.Nodes {
		if n.Kind == KindRestricted && n.Name != "" {
			t.Errorf("a placeholder named what it stands for: %#v", n)
		}
	}
}

// TestAnArrangementNamingAProductNobodyOffersSaysSo. The catalogue's edges are stored
// beside its item list and nothing keeps the two in step until publish, so an edge can
// name a product that is not in the landscape at all. That is a different finding from
// "not yours to see" and takes the shape the landscape already has for it.
func TestAnArrangementNamingAProductNobodyOffersSaysSo(t *testing.T) {
	c := cat("cat_1", "Mine", "package")
	c.Edges = []CatalogEdge{{From: "package", To: "ghost", Kind: EdgeComposition}}
	land := Landscape{
		Catalogs: []ProductCatalog{c},
		Products: []Product{prod("package", "Package", "cat_1", "", "")},
	}

	g := DeriveGraph(land, Options{Subject: SubjectProducts})

	id := unresolvedNodeID(KindProduct, "ghost")
	if node := nodeByID(t, g, id); node.Kind != KindUnresolved {
		t.Errorf("a missing part is %#v", node)
	}
	if !hasEdge(g, "product:package", id, EdgeComposition) {
		t.Errorf("no edge to the missing part in %#v", g.Edges)
	}
}

// TestAProductOfferedTwiceIsOneNode. A product is referenced by catalogues rather
// than owned by one (ADR-0315), and two catalogues offering the same laptop are two
// offers of one thing — drawing it twice would say there are two laptops.
func TestAProductOfferedTwiceIsOneNode(t *testing.T) {
	land := Landscape{
		Catalogs: []ProductCatalog{cat("cat_1", "Sales", "laptop"), cat("cat_2", "Support", "laptop")},
		Products: []Product{prod("laptop", "Laptop", "cat_1", "", "")},
	}

	g := DeriveGraph(land, Options{Subject: SubjectProducts})

	if counts := kindsOf(g); counts[KindProduct] != 1 {
		t.Fatalf("drew %d product nodes for one product: %#v", counts[KindProduct], g.Nodes)
	}
	if !hasEdge(g, "catalog:cat_1", "product:laptop", EdgeOffers) ||
		!hasEdge(g, "catalog:cat_2", "product:laptop", EdgeOffers) {
		t.Errorf("both catalogues must be drawn offering it: %#v", g.Edges)
	}
}

// TestNeitherPictureCountsACatalogueAsDrift.
//
// Drift is "the architecture declares this and Atlas does not have it", and the
// reverse. A binding can name an application, a process or a worker; ADR-0189 §4 has
// no key for a catalogue or a product, so counting one would report a debt no model
// could ever pay off — a number that only grows as somebody fills a catalogue.
//
// Since the two subjects were split there are two halves to this, and they answer it
// differently: the landscape has no catalogue to miscount, and the product map has no
// overlay at all, because every kind an overlay could match is on the other picture.
func TestNeitherPictureCountsACatalogueAsDrift(t *testing.T) {
	land := Landscape{
		Applications: []Application{app("a1", "Billing")},
		Processes:    []Process{proc(1, "invoice", "Invoice", "a1")},
		Catalogs:     []ProductCatalog{cat("cat_1", "Mobile devices", "phone")},
		Products:     []Product{prod("phone", "Phone", "cat_1", "", "")},
	}
	overlay := Overlay{ModelID: "m1", ModelName: "Estate", Elements: []ModelElement{
		{ElementID: "e1", ElementType: "ApplicationComponent", Name: "Billing",
			Key: KeyApplicationID, Values: []string{"a1"}},
	}}

	// The landscape: the process is the one derived node nothing declares.
	landscape := DeriveGraph(land, Options{Overlays: []Overlay{overlay}})
	if landscape.Unmodeled != 1 {
		t.Errorf("the landscape's unmodelled count is %d; want 1, the process alone", landscape.Unmodeled)
	}

	// The product map: nothing to compare, so nothing is claimed.
	products := DeriveGraph(land, Options{Subject: SubjectProducts, Overlays: []Overlay{overlay}})
	if products.Unmodeled != 0 || products.Modeled != 0 {
		t.Errorf("the product map reported drift (%d modelled, %d unmodelled); no binding key names a product",
			products.Modeled, products.Unmodeled)
	}
	for _, n := range products.Nodes {
		if n.Provenance != ProvenanceDerived {
			t.Errorf("an overlay reached the product map: %#v", n)
		}
	}
}

// TestAnOverBudgetLandscapeCollapsesProductsIntoTheirCatalogue.
//
// The size budget collapses the picture to what holds things. An application holds
// its processes; a catalogue holds what it offers. Leaving catalogues out of the
// collapse would drop them from an over-budget picture entirely, which is the
// truncated-but-complete-looking graph ADR-0211 §7 refuses.
func TestAnOverBudgetLandscapeCollapsesProductsIntoTheirCatalogue(t *testing.T) {
	land := Landscape{
		Applications: []Application{app("a1", "Billing")},
		Processes:    []Process{proc(1, "invoice", "Invoice", "a1")},
		Catalogs:     []ProductCatalog{cat("cat_1", "Mobile devices", "phone", "case")},
		Products: []Product{
			prod("phone", "Phone", "cat_1", "", ""),
			prod("case", "Case", "cat_1", "", ""),
		},
	}

	g := DeriveGraph(land, Options{Subject: SubjectProducts, MaxNodes: 2})

	if !g.Clustered {
		t.Fatalf("a landscape over its budget must say it collapsed: %#v", g)
	}
	node := nodeByID(t, g, "catalog:cat_1")
	if node.Children != 2 {
		t.Errorf("the collapsed catalogue stands for %d products; want 2", node.Children)
	}
	if counts := kindsOf(g); counts[KindProduct] != 0 {
		t.Errorf("a collapsed picture still drew %d product(s)", counts[KindProduct])
	}
}

// TestAProductNoCatalogueOffersIsNotDrawn.
//
// The mesh is the dependency picture and not an inventory — the same rule that keeps
// a configured worker nothing uses off the canvas. A product exists whether or not a
// catalogue offers it, and one nobody offers is a record, not part of the landscape.
func TestAProductNoCatalogueOffersIsNotDrawn(t *testing.T) {
	land := Landscape{
		Catalogs: []ProductCatalog{cat("cat_1", "Mobile devices", "phone")},
		Products: []Product{
			prod("phone", "Phone", "cat_1", "", ""),
			prod("shelfware", "Nobody offers this", "cat_1", "", ""),
		},
	}

	g := DeriveGraph(land, Options{Subject: SubjectProducts})

	for _, n := range g.Nodes {
		if n.Name == "Nobody offers this" {
			t.Fatalf("a product no catalogue offers was drawn: %#v", n)
		}
	}
}

// TestACollapsedProductFoldsIntoItsHomeCatalogue.
//
// A product is offered by several catalogues and edited through one (ADR-0315), so
// when the picture collapses there is a right answer to which catalogue stands for
// it — and it is the home, not whichever offer was read first.
func TestACollapsedProductFoldsIntoItsHomeCatalogue(t *testing.T) {
	land := Landscape{
		Catalogs: []ProductCatalog{cat("cat_1", "Sales", "laptop"), cat("cat_2", "Support", "laptop")},
		Products: []Product{prod("laptop", "Laptop", "cat_2", "", "")},
	}

	if node := nodeByID(t, DeriveGraph(land, Options{Subject: SubjectProducts}), "product:laptop"); node.Catalog != "catalog:cat_2" {
		t.Errorf("the product is grouped under %q; want its home catalogue", node.Catalog)
	}

	g := DeriveGraph(land, Options{Subject: SubjectProducts, MaxNodes: 2})
	if !g.Clustered {
		t.Fatalf("want a collapsed graph, got %#v", g)
	}
	if node := nodeByID(t, g, "catalog:cat_2"); node.Children != 1 {
		t.Errorf("the home catalogue stands for %d; want 1", node.Children)
	}
	if node := nodeByID(t, g, "catalog:cat_1"); node.Children != 0 {
		t.Errorf("the offering catalogue stands for %d; want 0 — it is not where the product is edited", node.Children)
	}
}

// The two subjects, kept apart (#1022, corrected).
//
// The first cut drew the catalogue onto the landscape, and that was wrong in a way no
// test caught because it was not a defect in any assertion: an operator reading the
// landscape for a stuck process had a hundred products in the way, and — the part
// that is not taste — every product spent the size budget, so a large catalogue could
// collapse somebody else's landscape to applications without them having asked to see
// it at all. They are two pictures now.

// TestTheLandscapeDrawsNoCatalogue is the correction, stated as a test: a landscape
// carrying a whole catalogue in its facts still draws the estate and nothing else.
func TestTheLandscapeDrawsNoCatalogue(t *testing.T) {
	land := Landscape{
		Applications: []Application{app("a1", "Billing")},
		Processes:    []Process{proc(1, "invoice", "Invoice", "a1")},
		Catalogs:     []ProductCatalog{cat("cat_1", "Mobile devices", "phone")},
		Products:     []Product{prod("phone", "Phone", "cat_1", "invoice", "")},
	}

	g := DeriveGraph(land, Options{})

	counts := kindsOf(g)
	if counts[KindCatalog] != 0 || counts[KindProduct] != 0 {
		t.Errorf("the landscape drew %d catalogue(s) and %d product(s); it draws the estate",
			counts[KindCatalog], counts[KindProduct])
	}
	if counts[KindApplication] != 1 || counts[KindProcess] != 1 {
		t.Errorf("the landscape lost its own subject: %#v", counts)
	}
	for _, e := range g.Edges {
		if e.Kind == EdgeOffers || e.Kind == EdgeComposition || e.Kind == EdgeAggregation {
			t.Errorf("a catalogue edge reached the landscape: %#v", e)
		}
	}
}

// TestTheProductMapDrawsNoEstate is the same rule read the other way. It carries the
// processes the products *bind* and nothing else of the estate — not the workers those
// processes use, not the applications that hold them, not the peers. One hop, because
// the second hop is the landscape's question and the landscape is one click away.
func TestTheProductMapDrawsNoEstate(t *testing.T) {
	called := proc(2, "archive", "Archive", "a1")
	caller := proc(1, "provision-phone", "Provision a phone", "a1",
		Call{ElementID: "c1", CalledProcessID: "archive", TargetKey: 2})
	caller.Workers = []WorkerUse{{ElementID: "s1", Name: "ops-mail", TargetID: "w1"}}
	land := Landscape{
		Applications: []Application{app("a1", "Billing")},
		Processes:    []Process{caller, called},
		Workers:      []Worker{{ID: "w1", Name: "ops-mail", Type: "mail", CanView: true}},
		Targets:      []Target{{ID: "t1", Name: "Production"}},
		Drafts:       []Draft{{ProcessID: "draft-one", Name: "A draft", ApplicationID: "a1", CanView: true}},
		Catalogs:     []ProductCatalog{cat("cat_1", "Mobile devices", "phone")},
		Products:     []Product{prod("phone", "Phone", "cat_1", "provision-phone", "")},
	}

	g := DeriveGraph(land, Options{Subject: SubjectProducts})

	counts := kindsOf(g)
	for kind, n := range map[string]int{
		KindApplication: counts[KindApplication],
		KindWorker:      counts[KindWorker],
		KindDraft:       counts[KindDraft],
		KindTarget:      counts[KindTarget],
	} {
		if n != 0 {
			t.Errorf("the product map drew %d %s node(s); it draws what is offered", n, kind)
		}
	}
	// The one process a product binds, and not the one that process calls.
	if counts[KindProcess] != 1 {
		t.Fatalf("drew %d process nodes; want only the one a product binds — %#v", counts[KindProcess], g.Nodes)
	}
	if !hasEdge(g, "product:phone", "process:1", EdgeUses) {
		t.Errorf("the product is not joined to what provisions it: %#v", g.Edges)
	}
	for _, e := range g.Edges {
		if e.Kind == EdgeCalls || e.Kind == EdgeContains {
			t.Errorf("an estate edge reached the product map: %#v", e)
		}
	}
}

// TestABoundProcessCarriesItsTroubleOntoTheProductMap.
//
// The state is the point of drawing the process at all. "Three tokens are parked on
// the process that provisions the laptop" is the finding somebody opens this picture
// for, and a node that arrived without it would send them to another screen to learn
// the thing this one was drawn to tell them.
func TestABoundProcessCarriesItsTroubleOntoTheProductMap(t *testing.T) {
	stuck := proc(1, "provision-phone", "Provision a phone", "a1")
	stuck.State, stuck.Reason, stuck.Incidents = StateDegraded, "3 parked", 3
	land := Landscape{
		Applications: []Application{app("a1", "Billing")},
		Processes:    []Process{stuck},
		Catalogs:     []ProductCatalog{cat("cat_1", "Mobile devices", "phone")},
		Products:     []Product{prod("phone", "Phone", "cat_1", "provision-phone", "")},
	}

	node := nodeByID(t, DeriveGraph(land, Options{Subject: SubjectProducts}), "process:1")

	if node.State != StateDegraded || node.Incidents != 3 || node.Reason != "3 parked" {
		t.Errorf("the bound process arrived without its trouble: %#v", node)
	}
	if node.Severity != SeverityAttention {
		t.Errorf("severity = %q, want the class degraded maps to", node.Severity)
	}
}

// TestAProductCarriesWhatTheCatalogueSaysItIs is the two facets reaching the
// picture at all (#1067).
//
// They are carried rather than reduced here, and the test says so on purpose: the
// derivation's job is to put the catalogue's own words on the node, and deciding
// what counts as "requires approval" is a reading made where the picture is drawn.
// A derivation that answered that question would bake one reading into the payload
// and leave a second reader no way to ask a different one.
func TestAProductCarriesWhatTheCatalogueSaysItIs(t *testing.T) {
	phone := prod("phone", "Apple iPhone", "cat_1", "", "")
	phone.State, phone.Approval = "active", "superior"
	pager := prod("pager", "Pager", "cat_1", "", "")
	pager.State, pager.Approval = "withdrawn", "none"
	// A rule kind nothing in this package knows, which is the ordinary state of an
	// installation that registered its own approval process.
	board := prod("board", "Server rack", "cat_1", "", "")
	board.State, board.Approval = "draft", "four-eyes-board"
	land := Landscape{
		Catalogs: []ProductCatalog{cat("cat_1", "Mobile devices", "phone", "pager", "board")},
		Products: []Product{phone, pager, board},
	}

	g := DeriveGraph(land, Options{Subject: SubjectProducts})

	for _, want := range []struct{ id, state, approval string }{
		{"product:phone", "active", "superior"},
		{"product:pager", "withdrawn", "none"},
		{"product:board", "draft", "four-eyes-board"},
	} {
		n := nodeByID(t, g, want.id)
		if n.ProductState != want.state || n.ApprovalKind != want.approval {
			t.Errorf("%s carries state %q approval %q; want %q and %q",
				want.id, n.ProductState, n.ApprovalKind, want.state, want.approval)
		}
		// The facets must not have been mistaken for the observation state, which is
		// the field they sit next to and the one collision that would be silent: a
		// withdrawn product reading as a health finding would colour the picture.
		if n.State != StateUnbound || n.Severity != SeverityUnknown {
			t.Errorf("%s reports an observation it cannot have: state %q severity %q",
				want.id, n.State, n.Severity)
		}
	}
}

// TestNothingButAProductCarriesTheCatalogueFacets is the other half, and it is the
// half a filter depends on.
//
// "Carries no approval rule" is how a product without one reads, and it is equally
// how a catalogue reads if anything ever sets the field on one. A picture narrowed
// to "products that need approval" would then drop the catalogue offering them,
// which is a narrowing nobody asked for and no control on screen would explain.
func TestNothingButAProductCarriesTheCatalogueFacets(t *testing.T) {
	phone := prod("phone", "Apple iPhone", "cat_1", "provision-phone", "")
	phone.State, phone.Approval = "active", "superior"
	land := Landscape{
		Processes: []Process{{Key: 1, ProcessID: "provision-phone", Version: 1, Name: "Provision a phone", CanView: true}},
		Catalogs:  []ProductCatalog{cat("cat_1", "Mobile devices", "phone")},
		Products:  []Product{phone},
	}

	g := DeriveGraph(land, Options{Subject: SubjectProducts})

	for _, n := range g.Nodes {
		if n.Kind == KindProduct {
			continue
		}
		if n.ProductState != "" || n.ApprovalKind != "" {
			t.Errorf("a %s node carries a catalogue facet: state %q approval %q — %#v",
				n.Kind, n.ProductState, n.ApprovalKind, n)
		}
	}
}

// TestAProductTheCallerMayNotSeeLeaksNoFacet keeps the two facts behind the same
// wall the product's name is behind.
//
// A restricted product is drawn as a placeholder of another kind entirely, so this
// is not a second place the visibility rule has to be got right — it is the
// assertion that it stays that way. What a catalogue somebody may not read charges
// for, and whether it needs their manager's signature, is as much that catalogue's
// business as what it is called.
func TestAProductTheCallerMayNotSeeLeaksNoFacet(t *testing.T) {
	hidden := prod("secret", "Executive laptop", "cat_2", "", "")
	hidden.CanView = false
	hidden.State, hidden.Approval = "active", "fixed"
	open := cat("cat_1", "Mobile devices", "secret")
	land := Landscape{Catalogs: []ProductCatalog{open}, Products: []Product{hidden}}

	g := DeriveGraph(land, Options{Subject: SubjectProducts})

	for _, n := range g.Nodes {
		if n.ProductState != "" || n.ApprovalKind != "" {
			t.Errorf("a facet of a product this caller may not see reached the payload: %#v", n)
		}
		if n.Name == "Executive laptop" {
			t.Errorf("a restricted product was drawn by name: %#v", n)
		}
	}
}
