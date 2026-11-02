package catalog

import "testing"

// What a product promises about its delivery
// (ADR-0515).
//
// A service level is a name and a number of working days. The catalogue checks the
// two things a reader would otherwise be misled by — a promise with no name, and a
// promise of no time at all — and freezes the rest with the release, for the reason
// the price is frozen: a delivery measured against a promise must be measured
// against the one in force when it was ordered.

func promising(id string, sl *ServiceLevel) Item {
	it := item(id)
	it.ServiceLevel = sl
	return it
}

func TestAServiceLevelNeedsANameAndADeliveryTime(t *testing.T) {
	contains(t, problemsOf(promising("laptop", &ServiceLevel{Name: "  ", DeliveryDays: 3})),
		"service level without a name")
	contains(t, problemsOf(promising("laptop", &ServiceLevel{Name: "Standard"})),
		"delivery time of at least one working day")
	contains(t, problemsOf(promising("laptop", &ServiceLevel{Name: "Standard", DeliveryDays: -2})),
		"delivery time of at least one working day")
	contains(t, problemsOf(promising("laptop", &ServiceLevel{Name: "Standard", DeliveryDays: 400})),
		"at most 365 working days")

	for _, sl := range []*ServiceLevel{nil, {Name: "Standard", DeliveryDays: 3},
		{Name: "Express", DeliveryDays: 1, NameTexts: map[string]string{"de": "Express", "fr": "Express"}}} {
		if got := problemsOf(promising("laptop", sl)); len(got) != 0 {
			t.Errorf("service level %+v does not publish: %+v", sl, got)
		}
	}
}

func TestTheServiceLevelTravelsIntoTheReleaseAsACopy(t *testing.T) {
	sl := &ServiceLevel{Name: "Standard", DeliveryDays: 3, NameTexts: map[string]string{"de": "Standard"}}
	rel, problems := Publish(Input{
		Catalogs: []Catalog{{ID: "cat", Rank: 1, Languages: []string{"de"}, Items: []string{"laptop"}}},
		Items:    []Item{promising("laptop", sl)},
	})
	if len(problems) != 0 {
		t.Fatalf("publish refused: %v", problems)
	}
	got := rel.Items[0].ServiceLevel
	if got == nil || got.Name != "Standard" || got.DeliveryDays != 3 || got.NameTexts["de"] != "Standard" {
		t.Fatalf("the release carries %+v; the promise has to be frozen with it", got)
	}
	// The draft changing afterwards must not reach into the frozen copy.
	sl.DeliveryDays, sl.NameTexts["de"] = 10, "Langsam"
	if got.DeliveryDays != 3 || got.NameTexts["de"] != "Standard" {
		t.Errorf("the release shares its service level with the draft: %+v", got)
	}
}

func TestServiceLevelCopyIsDeep(t *testing.T) {
	if (*ServiceLevel)(nil).Copy() != nil {
		t.Error("the copy of no service level is a service level")
	}
	in := &ServiceLevel{Name: "Standard", DeliveryDays: 2, NameTexts: map[string]string{"de": "Standard"}}
	out := in.Copy()
	out.NameTexts["de"] = "anders"
	if in.NameTexts["de"] != "Standard" {
		t.Error("the copy shares its texts with the original")
	}
}

func TestServiceLevelNameInFallsBackToTheName(t *testing.T) {
	sl := &ServiceLevel{Name: "Standard", NameTexts: map[string]string{"fr": "Standard FR", "it": " "}}
	for lang, want := range map[string]string{"fr": "Standard FR", "it": "Standard", "de": "Standard"} {
		if got := sl.NameIn(lang); got != want {
			t.Errorf("NameIn(%q) = %q, want %q", lang, got, want)
		}
	}
	if (*ServiceLevel)(nil).NameIn("de") != "" {
		t.Error("no service level has a name")
	}
}
