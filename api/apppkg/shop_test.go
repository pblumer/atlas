package apppkg_test

import (
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/pblumer/atlas/api/apppkg"
)

// A shop exported from two catalogues: IT with a release, HR without one. IT names
// a group, an owner and an approver the directory knows, a group it does not, and
// a text that already carries a placeholder; HR offers the same product again and
// one maintained in a catalogue the package leaves out.
var (
	exportParts = []apppkg.Part{
		{ID: "it", Released: true, Document: map[string]any{
			"catalogs": []any{map[string]any{"id": "it", "texts": map[string]any{"de": "IT-Dienste", "en": "IT services"},
				"groups":  []any{"g-bau"},
				"members": []any{map[string]any{"role": "owner", "ref": map[string]any{"type": "account", "id": "fmm"}}}}},
			"products": []any{map[string]any{"id": "vpn", "homeCatalog": "it", "texts": map[string]any{"de": "VPN", "en": "VPN"},
				"price":      "Kosten {{preis}}",
				"approval":   map[string]any{"kind": "fixed", "ref": "fmm"},
				"eligible":   []any{"g-alle", "g-unknown"},
				"configForm": "f-vpn", "actions": []any{map[string]any{"form": "f-extra"}}}},
			"processes":        []any{map[string]any{"id": "p-vpn", "xml": "<bpmn:definitions/>"}, map[string]any{"id": "p-app", "xml": "<x/>"}},
			"missingProcesses": []any{"p-gone", "p-app"},
		}},
		{ID: "hr", Document: map[string]any{
			"catalogs": []any{map[string]any{"id": "hr", "texts": map[string]any{"en": "HR"}}},
			"products": []any{map[string]any{"id": "vpn", "texts": map[string]any{"en": "VPN (HR)"}},
				map[string]any{"id": "laptop", "homeCatalog": "facilities", "texts": map[string]any{"en": "Laptop"}, "eligible": []any{"{{team}}"}}},
		}},
	}
	exportApp = apppkg.Application{Processes: []string{"p-app"}, Forms: []string{"f-vpn"}}
	exportDir = []apppkg.DirectoryEntry{
		{Type: "group", ID: "g-bau", Name: "FB Bau"},
		{Type: "group", ID: "g-bau2", Name: "FB Bau"},
		{Type: "group", ID: "g-alle", Name: "Alle Mitarbeitenden"},
		{Type: "account", ID: "fmm", Name: "Fabienne Meier-Muster"},
	}
)

func TestAShopIsOneDocumentWithItsPrincipalsAsQuestions(t *testing.T) {
	earlier := map[string]apppkg.Question{"fb-bau": {Kind: "group", Label: map[string]string{"de": "Bauabteilung (von Hand)"}}}
	s, err := apppkg.BuildShop(exportParts, exportApp, exportDir, earlier)
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	if err := apppkg.Decode(s.Document, &doc); err != nil {
		t.Fatalf("katalog.json is not JSON: %v", err)
	}

	// Products: one per id, the first catalogue's wins, sorted by id.
	products, _ := doc["products"].([]any)
	var ids []string
	for _, p := range products {
		ids = append(ids, p.(map[string]any)["id"].(string))
	}
	if s.Products != 2 || !reflect.DeepEqual(ids, []string{"laptop", "vpn"}) {
		t.Fatalf("products = %d %v, want laptop and vpn once each", s.Products, ids)
	}
	vpn := products[1].(map[string]any)
	if vpn["texts"].(map[string]any)["en"] != "VPN" {
		t.Errorf("vpn = %v, want IT's, which offered it first", vpn["texts"])
	}

	// HR has no release, so the import does not publish.
	if s.Publish || doc["publish"] != nil || !hasLine(s.Notes, "catalogue hr has no release here") {
		t.Errorf("publish = %v / %v, notes %q", s.Publish, doc["publish"], s.Notes)
	}
	// Only the process outside the application travels; only the missing one
	// outside it is warned about.
	carried, _ := doc["processes"].([]any)
	if len(carried) != 1 || carried[0].(map[string]any)["id"] != "p-vpn" || !hasLine(s.Notes, "process p-vpn travels in katalog.json") {
		t.Errorf("processes = %v, notes %q", carried, s.Notes)
	}
	if !reflect.DeepEqual(doc["missingProcesses"], []any{"p-gone"}) || !hasLine(s.Warnings, "process p-gone is bound to a product and deployed nowhere") {
		t.Errorf("missing = %v, warnings %q", doc["missingProcesses"], s.Warnings)
	}
	for _, want := range []string{
		"form f-extra is asked by product vpn and is not in the application",
		"product laptop is maintained in catalogue facilities, which the package does not carry",
		`2 groups are named "FB Bau" here; if the target has as many, answer fb-bau with an id`,
		"group g-unknown is not in this server's directory; g-unknown in fragen.json has no answer",
	} {
		if !hasLine(s.Warnings, want) {
			t.Errorf("no warning %q in %q", want, s.Warnings)
		}
	}
	if hasLine(s.Warnings, "form f-vpn") {
		t.Errorf("a form the application carries is warned about: %q", s.Warnings)
	}

	// Principals become questions in place, answered with the exporting server's names.
	text := string(s.Document)
	for _, want := range []string{`"{{fb-bau}}"`, `"ref": "{{fabienne-meier-muster}}"`, `"{{alle-mitarbeitenden}}"`, `"{{g-unknown}}"`, `"{{team}}"`, `"Kosten {{preis}}"`} {
		if !strings.Contains(text, want) {
			t.Errorf("katalog.json lacks %s:\n%s", want, text)
		}
	}
	if strings.Contains(text, `"g-alle"`) || strings.Contains(text, `"fmm"`) {
		t.Errorf("a principal's id is left in katalog.json:\n%s", text)
	}
	if s.Answers["fb-bau"] != "FB Bau" || s.Answers["fabienne-meier-muster"] != "Fabienne Meier-Muster" {
		t.Errorf("answers = %v", s.Answers)
	}
	if _, answered := s.Answers["g-unknown"]; answered {
		t.Error("a principal the directory does not know was answered")
	}
	// A text's own placeholder is asked and answered with itself, so it arrives as it was.
	if s.Questions["preis"].Kind != "text" || s.Answers["preis"] != "{{preis}}" {
		t.Errorf("preis = %+v / %q", s.Questions["preis"], s.Answers["preis"])
	}
	// The earlier export's hand-improved wording is kept; the others name their uses.
	if s.Questions["fb-bau"].Label["de"] != "Bauabteilung (von Hand)" {
		t.Errorf("fb-bau = %+v, want the earlier wording", s.Questions["fb-bau"])
	}
	if got := s.Questions["fabienne-meier-muster"]; got.Kind != "account" ||
		got.Label["de"] != "Konto: Mitglied (owner) des Katalogs «IT-Dienste»; genehmigt «VPN»" {
		t.Errorf("approver question = %+v", got)
	}
	if got := s.Questions["alle-mitarbeitenden"].Label["en"]; got != `Group: may receive "VPN"` {
		t.Errorf("audience question = %q", got)
	}
}

func TestAShopOfReleasedCataloguesPublishesAndCarriesNoProcess(t *testing.T) {
	part := apppkg.Part{ID: "it", Released: true, Document: map[string]any{
		"catalogs": []any{map[string]any{"id": "it"}},
		"products": []any{map[string]any{"id": "mail", "homeCatalog": "it"}},
	}}
	s, err := apppkg.BuildShop([]apppkg.Part{part}, apppkg.Application{}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	if err := apppkg.Decode(s.Document, &doc); err != nil {
		t.Fatal(err)
	}
	if !s.Publish || doc["publish"] != true || doc["processes"] != nil || doc["missingProcesses"] != nil {
		t.Errorf("doc = %v, want it published and nothing carried", doc)
	}
	if len(s.Notes)+len(s.Warnings) != 0 || len(s.Questions) != 0 {
		t.Errorf("notes %q, warnings %q, questions %v, want none", s.Notes, s.Warnings, s.Questions)
	}
}

func hasLine(lines []string, part string) bool {
	return slices.ContainsFunc(lines, func(l string) bool { return strings.Contains(l, part) })
}
