package archsketch

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/pblumer/atlas/api/panorama"
)

// ADR-0495 slice C: the data layer — what the processes keep and read, the classes
// they keep it as, and the personal data they declare — drawn as a band and written
// as ArchiMate data and business objects.

// dataSample is the physical sample with a data part, in an order Normalize settles.
func dataSample() Snapshot {
	s := physicalSample()
	s.Data = &Data{
		Objects: []DataObject{
			{ProcessID: "rechnung", Name: "Rechnung", Class: "Invoice", InitialState: "offen",
				ReadBy: []string{"Send", "Book", "Send"},
				WrittenBy: []DataWrite{
					{ElementID: "Book", State: "verbucht", Members: []string{"total", "number"}},
					{ElementID: "Create"},
				}},
			{ProcessID: "eingang", Name: "Positionen", Collection: true},
			{ProcessID: "eingang", Name: "Auftrag", Class: "Order", WrittenBy: []DataWrite{{ElementID: "Start"}}},
		},
		Stores: []DataStore{
			{Name: "Kundenstamm", Class: "Customer", UsedBy: []Use{{ProcessID: "rechnung", ElementID: "Store_A"}}},
			{Name: "Kundenstamm", UsedBy: []Use{{ProcessID: "eingang", ElementID: "Store_B"}, {ProcessID: "rechnung", ElementID: "Store_C"}}},
		},
		Classes: []DataClass{{Name: "Order"}, {Name: "Invoice", Modeled: true}, {Name: "Customer", Modeled: true}, {Name: "Order"}},
		Personal: []PersonalDeclaration{
			{ProcessID: "rechnung", Variables: []string{"email", "name", "email"}, Subject: "customerId",
				HandedTo: []PersonalRecipient{
					{Kind: DependencyWorker, Name: "smtp-main", WorkerType: "mail"},
					{Kind: DependencyWorker, Name: "erp", WorkerType: "rest"},
				}},
		},
	}
	s.Normalize()
	return s
}

func TestTheDataPartIsNormalizedAndBounded(t *testing.T) {
	d := dataSample().Data
	if got := []string{d.Objects[0].Name, d.Objects[1].Name, d.Objects[2].Name}; got[0] != "Auftrag" || got[1] != "Positionen" || got[2] != "Rechnung" {
		t.Errorf("objects = %v, want ordered by process, then name", got)
	}
	inv := d.Objects[2]
	if len(inv.ReadBy) != 2 || inv.ReadBy[0] != "Book" || len(inv.WrittenBy) != 2 || inv.WrittenBy[0].ElementID != "Book" ||
		strings.Join(inv.WrittenBy[0].Members, ",") != "number,total" {
		t.Errorf("Rechnung = %+v, want its reads merged and its writes ordered", inv)
	}
	if len(d.Stores) != 1 || len(d.Stores[0].UsedBy) != 3 || d.Stores[0].Class != "Customer" {
		t.Errorf("stores = %+v, want the one store named by three references, with its class", d.Stores)
	}
	if len(d.Classes) != 3 || d.Classes[0].Name != "Customer" || d.Classes[2].Name != "Order" {
		t.Errorf("classes = %+v, want three, merged and ordered", d.Classes)
	}
	if p := d.Personal[0]; strings.Join(p.Variables, ",") != "email,name" || p.HandedTo[0].Name != "erp" {
		t.Errorf("personal = %+v, want its variables merged and its recipients ordered", p)
	}
	for o, want := range map[*DataObject]string{{}: "Access", {ReadBy: []string{"A"}}: "Read"} {
		if got := o.accessType(); got != want {
			t.Errorf("%+v is accessed as %q, want %q", *o, got, want)
		}
	}
	none := PersonalDeclaration{HandedTo: []PersonalRecipient{}}
	some := PersonalDeclaration{HandedTo: []PersonalRecipient{{Kind: DependencyJobType, Name: "archivieren"}, {Kind: DependencyRuntimeWorker, Name: "=w"}}}
	if none.handedToText() != "handed to no worker" || some.handedToText() != "readable in plaintext by job type archivieren, a worker decided at run time" {
		t.Errorf("recipients read %q and %q", none.handedToText(), some.handedToText())
	}

	// A generated model beyond the bounds keeps the first of each list and counts the rest.
	big := Data{}
	for i := range maxDataObjects + 3 {
		big.Objects = append(big.Objects, DataObject{ProcessID: "p", Name: fmt.Sprintf("o%04d", i)})
	}
	for i := range maxDataStores + 1 {
		big.Stores = append(big.Stores, DataStore{Name: fmt.Sprintf("s%04d", i)})
	}
	for i := range maxDataClasses + 2 {
		big.Classes = append(big.Classes, DataClass{Name: fmt.Sprintf("c%04d", i)})
	}
	vars := make([]string, maxPersonalVariables+4)
	for i := range vars {
		vars[i] = fmt.Sprintf("v%04d", i)
	}
	big.Personal = []PersonalDeclaration{{ProcessID: "p", Variables: vars, Subject: "s"}}
	big.normalize()
	if len(big.Objects) != maxDataObjects || big.Omitted != (DataOmitted{Objects: 3, Stores: 1, Classes: 2, PersonalVariables: 4}) {
		t.Errorf("bounded = %d objects, omitted %+v", len(big.Objects), big.Omitted)
	}
	s := Snapshot{FormatVersion: FormatVersion, MappingVersion: MappingVersion, Data: &big}
	if gaps := strings.Join(s.NotShown(), "\n"); !strings.Contains(gaps, "beyond the snapshot's bounds: 3 data objects, 1 data store, 2 classes, 4 personal variables") {
		t.Errorf("the gaps do not count what the bounds left out:\n%s", gaps)
	}
}

func TestTheDataLayerIsRecordedAndSelectedLikeTheOthers(t *testing.T) {
	s := dataSample()
	if s.Recorded() != SelectionOf(LayerApplication, LayerData, LayerTechnology) {
		t.Errorf("recorded = %s, want application, data and technology", s.Recorded())
	}
	// The default selection leaves the data layer off, and the picture says so.
	plain := string(SVG(s, meta()))
	if strings.Contains(plain, ">Data</text>") || !strings.Contains(plain, "Switched off for this picture: data.") {
		t.Error("the default picture draws the data band, or does not say it switched it off")
	}
	if s.Select(DefaultSelection).Data != nil {
		t.Error("a selection without the data layer keeps the data part")
	}
	// An empty data part is still a recorded layer: the release declares no data.
	empty := physicalSample()
	empty.Data = &Data{}
	empty.Normalize()
	svg := string(SVG(empty.Select(SelectionOf(LayerData)), meta()))
	if !strings.Contains(svg, "This release declares no data objects, data stores or personal data.") || !strings.Contains(svg, "Data view") {
		t.Error("an empty data layer alone does not say the release declares no data")
	}
	raw, err := json.Marshal(empty.Data)
	if err != nil || string(raw) != `{"objects":[],"stores":[],"classes":[],"personal":[]}` {
		t.Errorf("an empty data part = %s (err=%v), want empty lists and no omitted counts", raw, err)
	}
}

func TestTheDataBandIsDrawnBelowThePicture(t *testing.T) {
	s := dataSample().Select(SelectionOf(LayerApplication, LayerData, LayerTechnology))
	svg := string(SVG(s, meta()))
	for _, want := range []string{
		">Data</text>",
		"Personal data of rechnung",
		"Data object · personal, about customerId", "email, name",
		"readable in plaintext by erp, smtp-main",
		"Rechnung", "in rechnung · class Invoice", "2 writes, 2 reads · states offen, verbucht",
		"Positionen (collection)", "in eingang · no declared type", "no association reads or writes it",
		"Kundenstamm", "keeps class Customer",
		"Business object", "defined in the information model", "declared type, not in the information model",
		"kept as 1 data object", "kept in 1 store",
		"Personal data: each worker named can read every variable visible at its task in plaintext",
		"which activity reads or writes a data store",
		"layers: application,data,technology",
	} {
		if !strings.Contains(svg, want) {
			t.Errorf("the picture lacks %q", want)
		}
	}
	// The picture above the band is the one the application and technology layers draw.
	if !strings.Contains(svg, "Reached from outside") || !strings.Contains(svg, "smtp-main") {
		t.Error("the band replaced the picture instead of joining it")
	}
	assertWellFormed(t, []byte(svg))

	// The data layer alone is the band, with no node and no columns.
	alone := string(SVG(dataSample().Select(SelectionOf(LayerData)), meta()))
	if strings.Contains(alone, "Reached from outside") || !strings.Contains(alone, "Personal data of rechnung") || !strings.Contains(alone, "Data view") {
		t.Error("the data layer alone is not drawn as the band alone")
	}
	assertWellFormed(t, []byte(alone))

	// The installation alone with the data layer keeps the installation's picture.
	tech := string(SVG(dataSample().Select(SelectionOf(LayerTechnology, LayerData)), meta()))
	if !strings.Contains(tech, "Physical view of") || !strings.Contains(tech, "Personal data of rechnung") {
		t.Error("the installation with the data layer lost one of the two")
	}
}

func TestABandTooFullToDrawCountsWhatItLeftOut(t *testing.T) {
	s := dataSample()
	for i := range bandMaxObjects {
		s.Data.Objects = append(s.Data.Objects, DataObject{ProcessID: "eingang", Name: fmt.Sprintf("Objekt %02d", i)})
	}
	for i := range bandMaxClasses {
		s.Data.Classes = append(s.Data.Classes, DataClass{Name: fmt.Sprintf("Klasse %02d", i)})
	}
	s.Normalize()
	svg := string(SVG(s.Select(SelectionOf(LayerData)), meta()))
	if !strings.Contains(svg, "Not drawn for lack of room: 3 data objects, 3 classes") {
		t.Errorf("the band does not count what it left out")
	}
	// Each kind keeps its own rows: the store is drawn although the objects overflow.
	if !strings.Contains(svg, "Kundenstamm") {
		t.Error("the data objects crowded out the store")
	}
}

func TestTheDataLayerIsValidArchiMate(t *testing.T) {
	for name, sel := range map[string]Selection{
		"all three":       SelectionOf(LayerApplication, LayerData, LayerTechnology),
		"data alone":      SelectionOf(LayerData),
		"with technology": SelectionOf(LayerTechnology, LayerData),
	} {
		doc := ArchiMate(dataSample().Select(sel), meta())
		if res := panorama.Validate(doc); len(res.Problems) != 0 {
			t.Fatalf("%s: panorama.Validate reports %+v", name, res.Problems)
		}
		types, rels := readExchange(t, doc)
		for _, r := range rels {
			ok, refusal := panorama.MayConnect(r.typ, types[r.source], types[r.target])
			if !ok && !(types[r.source] == "Path" && refusal.Reason == panorama.RefusedOutOfSubset) {
				t.Errorf("%s: %s from %s (%s) to %s (%s) is refused: %s", name, r.typ, r.source, types[r.source], r.target, types[r.target], refusal.Message)
			}
		}
		text := string(doc)
		for _, want := range []string{
			`xsi:type="ApplicationProcess"`, `xsi:type="DataObject"`, `xsi:type="BusinessObject"`,
			`accessType="ReadWrite"`, `accessType="Write"`,
			`<value xml:lang="en">email, name</value>`,
			"Only the declaration is recorded, never a value.",
		} {
			if !strings.Contains(text, want) {
				t.Errorf("%s: the document lacks %q", name, want)
			}
		}
	}

	// With the application layer, each worker the process hands work to reads its
	// personal data, and the processes are assigned from the component.
	types, rels := readExchange(t, ArchiMate(dataSample().Select(SelectionOf(LayerApplication, LayerData)), meta()))
	reads, assigned := 0, 0
	for _, r := range rels {
		if r.typ == "Access" && types[r.source] == "ApplicationService" && strings.HasPrefix(r.target, "id-personal-") {
			reads++
		}
		if r.typ == "Assignment" && r.source == "id-application" && types[r.target] == "ApplicationProcess" {
			assigned++
		}
	}
	if reads != 2 || assigned != 2 {
		t.Errorf("worker reads of personal data = %d, process assignments = %d; want 2 and 2", reads, assigned)
	}
}

func TestADocumentWithoutTheDataLayerIsUnchangedByIt(t *testing.T) {
	with, without := dataSample(), dataSample()
	without.Data = nil
	// The default selection draws no data, so a snapshot with a data part reads as one
	// without, apart from the line saying the data layer was switched off.
	a := strings.Replace(string(ArchiMate(with, meta())), "Layers shown: application,technology. Switched off for this picture: data. ", "", 1)
	if b := string(ArchiMate(without, meta())); a != b {
		t.Error("the data part changes a document that does not show it")
	}
}
