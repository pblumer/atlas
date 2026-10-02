package catalog

import (
	"strings"
	"testing"
)

// fakeForms and fakePersonal answer the publish warning from tables.
type fakeForms map[string][]FormField

func (f fakeForms) FormFields(id string) ([]FormField, bool) {
	fields, ok := f[id]
	return fields, ok
}

type fakePersonal map[string][]string

func (f fakePersonal) PersonalVariables(id string) ([]string, bool) {
	names, ok := f[id]
	return names, ok
}

// TestAnAnswerThatReachesAProcessInTheClearIsWarnedAbout: the warning names, per
// product and process, the answers the process receives without declaring them
// personal — and nothing a field's own mark, the order's own names, an undeployed
// process or Atlas's own approval makes moot.
func TestAnAnswerThatReachesAProcessInTheClearIsWarnedAbout(t *testing.T) {
	forms := fakeForms{
		"park": {{Key: "kennzeichen"}, {Key: "fahrzeug"}, {Key: "kostenstelle", NotPersonal: true}, {Key: "recipient"}},
	}
	processes := fakePersonal{"prov": {"kennzeichen"}, "own-approval": nil}
	items := []Item{
		{ID: "a", ConfigForm: "park", ProvisionProcess: "prov", DeprovisionProcess: "prov",
			Approval: Approval{Kind: "own-approval"}},
		{ID: "b", ConfigForm: "park", ProvisionProcess: "undeployed", Approval: Approval{Kind: KindFixed}},
		{ID: "c", ConfigForm: "gone", ProvisionProcess: "prov"},
		{ID: "d", ProvisionProcess: "prov"},
	}
	got := AnswerWarnings(items, forms, processes)
	if len(got) != 2 {
		t.Fatalf("warnings = %+v, want one for prov and one for the product's own approval", got)
	}
	if got[0].Item != "a" || !strings.Contains(got[0].Message, "the answers fahrzeug of form park reach prov in the clear") {
		t.Errorf("first warning = %+v", got[0])
	}
	if !strings.Contains(got[1].Message, "the answers fahrzeug, kennzeichen of form park reach own-approval") {
		t.Errorf("second warning = %+v", got[1])
	}
	for _, w := range got {
		named, _, _ := strings.Cut(strings.TrimPrefix(w.Message, "the answers "), " of form")
		if strings.Contains(named, "kostenstelle") || strings.Contains(named, "recipient") {
			t.Errorf("a field marked personal=false, or one the order sets itself, was warned about: %s", named)
		}
	}

	processes["prov"] = []string{"kennzeichen", "fahrzeug"}
	processes["own-approval"] = []string{"kennzeichen", "fahrzeug"}
	if got := AnswerWarnings(items, forms, processes); len(got) != 0 {
		t.Errorf("every answer declared, still warned: %+v", got)
	}
	if AnswerWarnings(items, nil, processes) != nil || AnswerWarnings(items, forms, nil) != nil {
		t.Error("a service without its lookups warned")
	}
	onlyMarked := fakeForms{"park": {{Key: "kostenstelle", NotPersonal: true}}}
	if got := AnswerWarnings(items, onlyMarked, fakePersonal{"prov": nil}); len(got) != 0 {
		t.Errorf("a form whose every field names nobody was warned about: %+v", got)
	}
}
