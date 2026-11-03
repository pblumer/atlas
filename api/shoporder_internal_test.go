package api

import "testing"

// A shop order task's answers are an object of products, each an object of answers,
// stored as the text a form's answer is: a string as it is, any other value as its
// JSON, a null left out — and anything else is refused rather than half-read.
func TestShopOrderAnswers(t *testing.T) {
	for _, empty := range []string{"", "  ", "null"} {
		got, err := shopOrderAnswers(empty)
		if err != nil || got != nil {
			t.Errorf("%q: %v, %v; want no answers", empty, got, err)
		}
	}
	got, err := shopOrderAnswers(`{"m365-eintritt-lizenz":{"upn":"anna@contoso.ch","lizenzvariante":"E5","plaetze":3,"extern":false,"leer":null},"m365-oe-mitgliedschaften":{"oe":"FIN"}}`)
	if err != nil {
		t.Fatal(err)
	}
	lic := got["m365-eintritt-lizenz"]
	if lic["upn"] != "anna@contoso.ch" || lic["lizenzvariante"] != "E5" || lic["plaetze"] != "3" || lic["extern"] != "false" {
		t.Errorf("licence answers = %v", lic)
	}
	if _, ok := lic["leer"]; ok {
		t.Error("a null answer was kept")
	}
	if got["m365-oe-mitgliedschaften"]["oe"] != "FIN" {
		t.Errorf("unit answers = %v", got["m365-oe-mitgliedschaften"])
	}
	for _, bad := range []string{`[1,2]`, `{"p":"flat"}`, `{"p":`} {
		if _, err := shopOrderAnswers(bad); err == nil {
			t.Errorf("%s: read", bad)
		}
	}
}
