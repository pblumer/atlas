package archsketch

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/pblumer/atlas/api/panorama"
)

// ADR-0495 slice D: the business layer — the processes as work, the roles that do it,
// the products they carry out — and the strategy layer — the capabilities the release
// realises and the value streams that use them — drawn as bands above the picture and
// written as ArchiMate business and strategy elements.

// layeredSample is the data sample with a business and a strategy part, in an order
// Normalize settles.
func layeredSample() Snapshot {
	s := dataSample()
	s.Business = &Business{
		Processes: []BusinessProcess{
			{ProcessID: "rechnung", Name: "Rechnung", GroupTasks: 2, PersonTasks: 1},
			{ProcessID: "eingang", Name: "Eingang", RuntimeTasks: 1, OpenTasks: 1},
		},
		Roles: []BusinessRole{
			{Name: "buchhaltung", Kind: RoleCandidateGroup, UsedBy: []Use{{ProcessID: "rechnung", ElementID: "Pruefen"}}},
			{Name: "Kundendienst", Kind: RoleLane, UsedBy: []Use{{ProcessID: "eingang"}}},
			{Name: "buchhaltung", Kind: RoleCandidateGroup, UsedBy: []Use{{ProcessID: "rechnung", ElementID: "Freigeben"}}},
		},
		Products: []Product{
			{ID: "p-abo", Name: "Abonnement", CatalogID: "kat-1", CatalogName: "Dienste", State: "active",
				Bindings: []ProductBinding{{Op: "lifecycle", ProcessID: "rechnung"}},
				Actions: []ProductAction{
					{Key: "kuendigen", Message: "abo.kuendigen", Effect: "deprovision", Triggers: []string{"operator", "customer"},
						Outcomes: []ProductOutcome{{Outcome: "rejected", EventType: "abo.kuendigen.rejected"}, {Outcome: "completed", EventType: "abo.gekuendigt"}}},
					{Key: "bestellen", Message: "abo.bestellen", Effect: "provision"},
				}},
			{ID: "p-konto", Name: "Konto", CatalogID: "kat-2", State: "withdrawn",
				Bindings: []ProductBinding{{Op: "deprovision", ProcessID: "eingang"}, {Op: "provision", ProcessID: "eingang"}}},
		},
	}
	s.Strategy = &Strategy{
		Capabilities: []Capability{
			{Key: "fakturierung", Name: "Fakturierung", State: "active", ConfirmedAt: 1_790_000_000,
				Owner:      &Owner{Name: "Erika Muster", Role: "Leiterin Finanzen"},
				KPIs:       []KPI{{Name: "Durchlaufzeit", Goal: "< 2 Tage"}},
				SLAs:       []SLA{{Name: "Antwortzeit", Threshold: "4h"}},
				RealizedBy: []Realization{{Kind: RealizedByWorker, Worker: "smtp-main"}, {Kind: RealizedByProcess, ProcessID: "rechnung"}}},
			{Key: "auftragsannahme", Name: "Auftragsannahme", RealizedBy: []Realization{{Kind: RealizedByProcess, ProcessID: "eingang"}}},
		},
		ValueStreams: []ValueStream{
			{Key: "order-to-cash", Name: "Order to Cash", Owner: &Owner{Role: "Vertrieb"}, Stages: []Stage{
				{Key: "bestellen", Name: "Bestellen", Capabilities: []string{"auftragsannahme"}},
				{Key: "liefern", Name: "Liefern"},
				{Key: "fakturieren", Name: "Fakturieren", Capabilities: []string{"fakturierung"}},
			}},
		},
	}
	s.Normalize()
	return s
}

func TestTheBusinessAndStrategyPartsAreNormalizedAndBounded(t *testing.T) {
	s := layeredSample()
	b, st := s.Business, s.Strategy
	if b.Processes[0].ProcessID != "eingang" || len(b.Roles) != 2 || b.Roles[0].Name != "buchhaltung" || len(b.Roles[0].UsedBy) != 2 {
		t.Errorf("business = %+v, want processes ordered and the candidate group merged", b)
	}
	if b.Products[0].Name != "Abonnement" || b.Products[0].Actions[0].Key != "bestellen" ||
		b.Products[0].Actions[1].Outcomes[0].Outcome != "completed" || b.Products[0].Actions[1].Triggers[0] != "customer" ||
		b.Products[1].Bindings[0].Op != "provision" {
		t.Errorf("products = %+v, want actions, outcomes, triggers and bindings ordered", b.Products)
	}
	if st.Capabilities[0].Name != "Auftragsannahme" || st.Capabilities[1].RealizedBy[0].Kind != RealizedByProcess {
		t.Errorf("capabilities = %+v, want ordered by name with their realisations ordered", st.Capabilities)
	}
	if got := st.ValueStreams[0].Stages; got[0].Key != "bestellen" || got[2].Key != "fakturieren" {
		t.Errorf("stages = %+v, want the stream's own order", got)
	}
	if s.Recorded() != SelectionOf(LayerStrategy, LayerBusiness, LayerApplication, LayerData, LayerTechnology) {
		t.Errorf("recorded = %s, want every layer", s.Recorded())
	}

	big := Business{}
	for i := range maxBusinessRoles + 2 {
		big.Roles = append(big.Roles, BusinessRole{Name: fmt.Sprintf("g%04d", i), Kind: RoleCandidateGroup})
	}
	for i := range maxRoleUses + 1 {
		big.Roles[0].UsedBy = append(big.Roles[0].UsedBy, Use{ProcessID: "p", ElementID: fmt.Sprintf("t%04d", i)})
	}
	for i := range maxProducts + 1 {
		p := Product{ID: fmt.Sprintf("p%04d", i), Name: fmt.Sprintf("p%04d", i)}
		if i == 0 {
			for j := range maxProductActions + 3 {
				p.Actions = append(p.Actions, ProductAction{Key: fmt.Sprintf("a%04d", j)})
			}
		}
		big.Products = append(big.Products, p)
	}
	big.normalize()
	if big.Omitted != (BusinessOmitted{Roles: 2, RoleUses: 1, Products: 1, ProductActions: 3}) {
		t.Errorf("business omitted = %+v", big.Omitted)
	}
	bigStrategy := Strategy{ValueStreams: []ValueStream{{Key: "v"}}}
	for i := range maxCapabilities + 1 {
		c := Capability{Key: fmt.Sprintf("c%04d", i), Name: fmt.Sprintf("c%04d", i)}
		if i == 0 {
			for j := range maxMeasures + 2 {
				c.KPIs = append(c.KPIs, KPI{Name: fmt.Sprint(j)})
			}
		}
		bigStrategy.Capabilities = append(bigStrategy.Capabilities, c)
	}
	for i := range maxStages + 4 {
		bigStrategy.ValueStreams[0].Stages = append(bigStrategy.ValueStreams[0].Stages, Stage{Key: fmt.Sprint(i)})
	}
	for i := range maxValueStreams {
		bigStrategy.ValueStreams = append(bigStrategy.ValueStreams, ValueStream{Key: fmt.Sprintf("w%04d", i)})
	}
	bigStrategy.normalize()
	if bigStrategy.Omitted != (StrategyOmitted{Capabilities: 1, ValueStreams: 1, Measures: 2, Stages: 4}) {
		t.Errorf("strategy omitted = %+v", bigStrategy.Omitted)
	}
	gaps := strings.Join(Snapshot{Business: &big, Strategy: &bigStrategy}.NotShown(), "\n")
	for _, want := range []string{
		"beyond the snapshot's bounds: 2 roles, 1 task naming a role, 1 product, 3 product actions",
		"beyond the snapshot's bounds: 1 capability, 1 value stream, 2 KPIs and SLAs, 4 stages",
	} {
		if !strings.Contains(gaps, want) {
			t.Errorf("the gaps lack %q:\n%s", want, gaps)
		}
	}
}

func TestAProductIsShownOnlyToWhoMayMaintainItsCatalogue(t *testing.T) {
	b := *layeredSample().Business
	red := b.Redacted(func(id string) bool { return id == "kat-1" })
	if len(red.Products) != 1 || red.Products[0].ID != "p-abo" || red.ProductsWithheld != 1 {
		t.Errorf("redacted = %+v, want the one product of kat-1 and one withheld", red)
	}
	if len(b.Products) != 2 {
		t.Error("the stored layer was changed by a read")
	}
	s := layeredSample()
	s.Business = &red
	svg := string(SVG(s.Select(SelectionOf(LayerBusiness)), meta()))
	if strings.Contains(svg, "Konto") || !strings.Contains(svg, "1 product withheld from this reader, who may not maintain its catalogue.") {
		t.Error("the band shows a withheld product, or does not say one was withheld")
	}
}

func TestTheBusinessAndStrategyBandsAreDrawnAboveThePicture(t *testing.T) {
	s := layeredSample()
	svg := string(SVG(s.Select(SelectionOf(LayerStrategy, LayerBusiness, LayerApplication, LayerTechnology)), meta()))
	for _, want := range []string{
		">Strategy</text>", ">Business</text>",
		"Fakturierung", "realised by process rechnung, worker smtp-main", "active · confirmed 2026-09-21",
		"owner Erika Muster (Leiterin Finanzen)", "no owner recorded",
		"Order to Cash", "[Bestellen] → Liefern → [Fakturieren]", "uses Auftragsannahme at Bestellen, Fakturierung at Fakturieren",
		"owner role Vertrieb",
		"Business process", "realised by process rechnung", "2 tasks for a group · 1 for a named person",
		"1 decided at run time · 1 naming nobody",
		"Business role", "buchhaltung", "candidate group · 2 tasks", "at rechnung/Freigeben, rechnung/Pruefen",
		"Kundendienst", "lane · 1 process", "in eingang",
		"Abonnement", "in Dienste · lifecycle", "actions: bestellen, kuendigen",
		"in kat-2 · provision, deprovision · withdrawn", "no actions declared",
		"layers: strategy,business,application,technology",
		"who a task assigned to one person by name is assigned to",
		"owner beyond name and role, and who confirmed or edited it",
	} {
		if !strings.Contains(svg, want) {
			t.Errorf("the picture lacks %q", want)
		}
	}
	// The bands push the picture down: its column titles sit below both bands.
	band := regexp.MustCompile(`y="(\d+)" font-weight="600" fill="[^"]+">Business</text>`).FindStringSubmatch(svg)
	column := regexp.MustCompile(`y="(\d+)" font-weight="600" fill="[^"]+">Reached from outside</text>`).FindStringSubmatch(svg)
	if band == nil || column == nil || atoi(column[1]) <= atoi(band[1]) {
		t.Errorf("the picture is not drawn below the business band (band %v, columns %v)", band, column)
	}
	assertWellFormed(t, []byte(svg))

	alone := string(SVG(s.Select(SelectionOf(LayerStrategy, LayerBusiness, LayerData)), meta()))
	if strings.Contains(alone, "Reached from outside") || !strings.Contains(alone, "Strategy, business and data view") {
		t.Error("the three band layers alone are not drawn as bands alone")
	}
	if one := string(SVG(s.Select(SelectionOf(LayerStrategy)), meta())); !strings.Contains(one, "Strategy view") {
		t.Error("the strategy layer alone does not say so")
	}
	if two := string(SVG(s.Select(SelectionOf(LayerBusiness, LayerData)), meta())); !strings.Contains(two, "Business and data view") {
		t.Error("two band layers alone do not say so")
	}

	empty := physicalSample()
	empty.Business, empty.Strategy = &Business{}, &Strategy{}
	empty.Normalize()
	none := string(SVG(empty.Select(SelectionOf(LayerStrategy, LayerBusiness)), meta()))
	for _, want := range []string{"No capability in the register is realised by this release",
		"This release names no role and carries out no product."} {
		if !strings.Contains(none, want) {
			t.Errorf("an empty layer does not say %q", want)
		}
	}

	if got := (&Owner{Name: "Ana"}).text(); got != "owner Ana" {
		t.Errorf("an owner without a role reads %q", got)
	}
	if got := (Capability{}).stateText(); got != "no state · never confirmed" {
		t.Errorf("a capability without state reads %q", got)
	}
	if got := (BusinessProcess{}).tasksText(); got != "no user task" {
		t.Errorf("a process without user tasks reads %q", got)
	}
	if got := (Product{}).bindingsText() + "|" + (Product{}).bindingText(); got != "no operation carried out here|no operation carried out here" {
		t.Errorf("a product without bindings reads %q", got)
	}
	if got := (Product{Bindings: []ProductBinding{{Op: "change", ProcessID: "p", Message: "m"}}}).bindingText(); got != "change by process p on message m" {
		t.Errorf("a binding with a message reads %q", got)
	}
}

func TestTheBandsCountWhatTheyCannotDraw(t *testing.T) {
	s := layeredSample()
	for i := range bandMaxCapabilities {
		s.Strategy.Capabilities = append(s.Strategy.Capabilities, Capability{Key: fmt.Sprintf("k%02d", i), Name: fmt.Sprintf("Fähigkeit %02d", i)})
	}
	for i := range bandMaxProducts {
		s.Business.Products = append(s.Business.Products, Product{ID: fmt.Sprintf("x%02d", i), Name: fmt.Sprintf("Produkt %02d", i)})
	}
	s.Normalize()
	svg := string(SVG(s.Select(SelectionOf(LayerStrategy, LayerBusiness)), meta()))
	for _, want := range []string{"Not drawn for lack of room: 2 capabilities", "Not drawn for lack of room: 2 products"} {
		if !strings.Contains(svg, want) {
			t.Errorf("the bands lack %q", want)
		}
	}
}

func TestTheBusinessAndStrategyLayersAreValidArchiMate(t *testing.T) {
	for name, sel := range map[string]Selection{
		"every layer":     SelectionOf(LayerStrategy, LayerBusiness, LayerApplication, LayerData, LayerTechnology),
		"bands alone":     SelectionOf(LayerStrategy, LayerBusiness),
		"with technology": SelectionOf(LayerTechnology, LayerStrategy, LayerBusiness),
	} {
		doc := ArchiMate(layeredSample().Select(sel), meta())
		if res := panorama.Validate(doc); len(res.Problems) != 0 {
			t.Fatalf("%s: panorama.Validate reports %+v", name, res.Problems)
		}
		types, rels := readExchange(t, doc)
		for _, r := range rels {
			ok, refusal := panorama.MayConnect(r.typ, types[r.source], types[r.target])
			outOfSubset := (types[r.source] == "Path" || types[r.source] == "Product") && refusal != nil && refusal.Reason == panorama.RefusedOutOfSubset
			if !ok && !outOfSubset {
				t.Errorf("%s: %s from %s (%s) to %s (%s) is refused: %s", name, r.typ, r.source, types[r.source], r.target, types[r.target], refusal.Message)
			}
		}
		text := string(doc)
		for _, want := range []string{
			`xsi:type="Capability"`, `xsi:type="ValueStream"`, `xsi:type="BusinessProcess"`, `xsi:type="BusinessRole"`, `xsi:type="Product"`,
			`<value xml:lang="en">Erika Muster (Leiterin Finanzen)</value>`,
			`<value xml:lang="en">2026-09-21</value>`,
			"kuendigen (message abo.kuendigen; completed publishes abo.gekuendigt; rejected publishes abo.kuendigen.rejected)",
		} {
			if !strings.Contains(text, want) {
				t.Errorf("%s: the document lacks %q", name, want)
			}
		}
	}

	// With the application layer the worker that realises a capability is joined to it.
	types, rels := readExchange(t, ArchiMate(layeredSample().Select(SelectionOf(LayerApplication, LayerStrategy, LayerBusiness)), meta()))
	count := map[string]int{}
	for _, r := range rels {
		count[r.typ+" "+types[r.source]+" "+types[r.target]]++
	}
	for rel, want := range map[string]int{
		"Realization ApplicationService Capability":          1,
		"Realization ApplicationProcess Capability":          2,
		"Serving Capability ValueStream":                     2,
		"Composition ValueStream ValueStream":                3,
		"Realization ApplicationProcess BusinessProcess":     2,
		"Assignment BusinessRole BusinessProcess":            2,
		"Association Product BusinessProcess":                2,
		"Assignment ApplicationComponent ApplicationProcess": 2,
	} {
		if count[rel] != want {
			t.Errorf("%s: %d, want %d", rel, count[rel], want)
		}
	}
}

func atoi(s string) int {
	n, _ := strconv.Atoi(s)
	return n
}
