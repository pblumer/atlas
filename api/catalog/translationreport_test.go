package catalog

import (
	"net/http"
	"strconv"
	"testing"

	"github.com/pblumer/atlas/api/httpapi"
)

// The report that took a refusal's place.
//
// Publishing no longer refuses a half-translated product, and the whole argument
// for that is in translationgaps_test.go. What the refusal did well was make the
// gap impossible to miss — so the gap is said here instead, and these are the
// properties that make saying it worth anything: it is scoped to what the caller
// maintains, it reads what is being edited rather than what was published, and it
// is quiet about a catalogue that has nothing to translate.

// TestTheTranslationReportFollowsTheCatalogueYouMaintain.
//
// The scoping, and it is the same one the two reports beside it have. A gap names
// a product and a catalogue, so a report that spilled would tell somebody which
// services another department offers and which of them it has not finished.
func TestTheTranslationReportFollowsTheCatalogueYouMaintain(t *testing.T) {
	s := serviceWithAdmin(t)
	mine := makeBilingual(t, s, user("usr_a"), 1)
	theirs := makeBilingual(t, s, user("usr_b"), 2)
	halfNamed(t, s, user("usr_a"), "mine", mine.ID)
	halfNamed(t, s, user("usr_b"), "theirs", theirs.ID)

	got := decode[TranslationReport](t, as(t, s.HandleTranslationGaps, user("usr_a"), "GET", ""))
	if len(got.Catalogs) != 1 || got.Catalogs[0] != mine.ID {
		t.Fatalf("the report covers %v, want only the catalogue I maintain", got.Catalogs)
	}
	for _, g := range got.Gaps {
		if g.Item != "mine" {
			t.Errorf("the report names %q, which is somebody else's product", g.Item)
		}
	}
	if len(got.Gaps) == 0 {
		t.Error("the report found nothing, and the fixture is half-named on purpose — " +
			"a scoping test that scopes an empty answer proves nothing")
	}
}

// TestAnOutsiderGetsAnEmptyTranslationReport: the same answer as maintaining no
// catalogue at all, which is an empty report and not a refusal. Having nothing to
// maintain is not an error.
func TestAnOutsiderGetsAnEmptyTranslationReport(t *testing.T) {
	s := serviceWithAdmin(t)
	cat := makeBilingual(t, s, user("usr_a"), 1)
	halfNamed(t, s, user("usr_a"), "mine", cat.ID)

	rec := as(t, s.HandleTranslationGaps, user("usr_outsider"), "GET", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("an outsider got %d (%s), want an empty report", rec.Code, rec.Body)
	}
	got := decode[TranslationReport](t, rec)
	if len(got.Catalogs) != 0 || len(got.Gaps) != 0 {
		t.Errorf("an outsider was told about %v: %+v", got.Catalogs, got.Gaps)
	}
}

// TestTheReportReadsWhatIsBeingEditedAndNotWhatWasPublished.
//
// Its two siblings read the newest release, because only what is published can
// park an order. This one is a list of work to do, and work to do is about the
// draft: a maintainer who has just added French to a catalogue wants the list
// before publishing, not after.
func TestTheReportReadsWhatIsBeingEditedAndNotWhatWasPublished(t *testing.T) {
	s := serviceWithAdmin(t)
	cat := makeBilingual(t, s, user("usr_a"), 1)
	halfNamed(t, s, user("usr_a"), "mine", cat.ID)

	// Never published, and reported on anyway.
	got := decode[TranslationReport](t, as(t, s.HandleTranslationGaps, user("usr_a"), "GET", ""))
	if len(got.Gaps) == 0 {
		t.Error("a catalogue nobody has published yet is not reported on, so the list " +
			"of work to do arrives only after the work was supposed to be finished")
	}
	if got.Checked != 1 {
		t.Errorf("checked = %d, want the one product the catalogue offers", got.Checked)
	}
}

// TestACompleteCatalogueIsQuiet is what the report exists to say nothing about.
func TestACompleteCatalogueIsQuiet(t *testing.T) {
	s := serviceWithAdmin(t)
	cat := makeBilingual(t, s, user("usr_a"), 1)
	body := `{"id":"whole","homeCatalog":"` + cat.ID + `","state":"active",` +
		`"texts":{"de":"Fernzugriff","fr":"Accès distant"},` +
		`"approval":{"kind":"none"},"provisionProcess":"p","deprovisionProcess":"d"}`
	as(t, s.HandleSaveItem, user("usr_a"), "POST", body)
	as(t, s.HandleUpdateCatalog, user("usr_a"), "PATCH", `{"items":["whole"]}`, "id", cat.ID)

	got := decode[TranslationReport](t, as(t, s.HandleTranslationGaps, user("usr_a"), "GET", ""))
	if len(got.Gaps) != 0 {
		t.Errorf("a fully translated catalogue was reported on: %+v", got.Gaps)
	}
	if got.Checked != 1 {
		t.Errorf("checked = %d; a quiet report has to be tellable from an empty one",
			got.Checked)
	}
}

// makeBilingual creates a catalogue declaring German and French.
func makeBilingual(t *testing.T, s *Service, owner *httpapi.Principal, rank int) Catalog {
	t.Helper()
	body := `{"rank":` + strconv.Itoa(rank) + `,"languages":["de","fr"],"texts":{"de":"Katalog"}}`
	rec := as(t, s.HandleCreateCatalog, owner, "POST", body)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create = %d (%s)", rec.Code, rec.Body)
	}
	return decode[Catalog](t, rec)
}

// halfNamed offers a product named in German and not in French.
func halfNamed(t *testing.T, s *Service, owner *httpapi.Principal, id, catalog string) {
	t.Helper()
	body := `{"id":"` + id + `","homeCatalog":"` + catalog + `","state":"active",` +
		`"texts":{"de":"Fernzugriff"},"approval":{"kind":"none"},` +
		`"provisionProcess":"p","deprovisionProcess":"d"}`
	if rec := as(t, s.HandleSaveItem, owner, "POST", body); rec.Code != http.StatusOK {
		t.Fatalf("save product = %d (%s)", rec.Code, rec.Body)
	}
	if rec := as(t, s.HandleUpdateCatalog, owner, "PATCH",
		`{"items":["`+id+`"]}`, "id", catalog); rec.Code != http.StatusOK {
		t.Fatalf("offer product = %d (%s)", rec.Code, rec.Body)
	}
}
