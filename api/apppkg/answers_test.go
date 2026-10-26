package apppkg_test

import (
	"strings"
	"testing"

	"github.com/pblumer/atlas/api/apppkg"
)

// The order a question is answered in (ADR-0467, amended): the person's choice, then
// what the target's table remembers for the packaged name, then the packaged name in
// the target's directory.

var directory = []apppkg.DirectoryEntry{
	{Type: "group", ID: "g-bau", Name: "FB Bau"},
	{Type: "group", ID: "g-alle", Name: "Alle Mitarbeitenden"},
	{Type: "account", ID: "fmm", Name: "Fabienne Meier-Muster"},
}

const shop = `{"catalogs":[{"groups":["{{bau}}","{{alle}}"]}],"products":[{"approval":{"kind":"fixed","ref":"{{fm}}"}}]}`

var questions = map[string]apppkg.Question{"bau": {Kind: "group"}, "alle": {Kind: "group"}, "fm": {Kind: "account"}}

var packaged = map[string]string{"bau": "Fachbereich Bau", "alle": "Alle Mitarbeitenden", "fm": "Fabienne Meier"}

func resolve(t *testing.T, given apppkg.Answers) apppkg.Resolution {
	t.Helper()
	res, err := apppkg.Resolve([]byte(shop), questions, given, func() ([]apppkg.DirectoryEntry, error) { return directory, nil })
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	return res
}

func TestAChoiceIsLearnedAndTheTableAnswersNextTime(t *testing.T) {
	// First import: two names the target does not carry.
	res := resolve(t, apppkg.Answers{Packaged: packaged})
	if len(res.Problems) != 2 || res.Problems[0].Name != "bau" || res.Problems[1].Name != "fm" || len(res.Learned) != 0 {
		t.Fatalf("problems = %+v, learned = %+v", res.Problems, res.Learned)
	}
	// The person chooses; the choices are what the table learns, the name it found
	// by itself is not.
	res = resolve(t, apppkg.Answers{Packaged: packaged, Chosen: map[string]string{"bau": "g-bau", "fm": "FMM"}})
	if len(res.Problems) != 0 || res.Values["bau"] != "g-bau" || res.Values["alle"] != "g-alle" || res.Values["fm"] != "fmm" {
		t.Fatalf("values = %v, problems = %+v", res.Values, res.Problems)
	}
	if len(res.Learned) != 2 || res.Learned[0] != (apppkg.Mapping{Kind: "group", Answer: "Fachbereich Bau", ID: "g-bau", Name: "FB Bau"}) ||
		res.Learned[1] != (apppkg.Mapping{Kind: "account", Answer: "Fabienne Meier", ID: "fmm", Name: "Fabienne Meier-Muster"}) {
		t.Fatalf("learned = %+v", res.Learned)
	}

	// Next import: the table answers both, and nothing is asked or learned.
	res = resolve(t, apppkg.Answers{Packaged: packaged, Mappings: res.Learned})
	if len(res.Problems) != 0 || len(res.Learned) != 0 || strings.Join(res.Remembered, ",") != "bau,fm" || res.Values["fm"] != "fmm" {
		t.Fatalf("with the table: %+v", res)
	}
	if !strings.Contains(strings.Join(res.Notes, "\n"), `bau: "Fachbereich Bau" is group g-bau, as this server remembers`) {
		t.Errorf("notes = %v", res.Notes)
	}
}

func TestTheTableWinsOverANameAndAChoiceOverTheTable(t *testing.T) {
	// The table says INT's "Alle Mitarbeitenden" is this server's FB Bau, though a
	// group of that name exists here: the table is a decision somebody made.
	table := []apppkg.Mapping{{Kind: "group", Answer: "Alle Mitarbeitenden", ID: "g-bau"}}
	res := resolve(t, apppkg.Answers{Packaged: packaged, Mappings: table,
		Chosen: map[string]string{"bau": "g-bau", "fm": "fmm"}})
	if res.Values["alle"] != "g-bau" {
		t.Fatalf("alle = %q, want the table's g-bau", res.Values["alle"])
	}
	res = resolve(t, apppkg.Answers{Packaged: packaged, Mappings: table,
		Chosen: map[string]string{"alle": "Alle Mitarbeitenden", "bau": "g-bau", "fm": "fmm"}})
	if res.Values["alle"] != "g-alle" {
		t.Fatalf("alle chosen = %q, want the choice", res.Values["alle"])
	}
}

func TestAMappingToAPrincipalNoLongerHereIsNotTrusted(t *testing.T) {
	table := []apppkg.Mapping{{Kind: "group", Answer: "Fachbereich Bau", ID: "g-geloescht"}}
	res := resolve(t, apppkg.Answers{Packaged: packaged, Mappings: table, Chosen: map[string]string{"fm": "fmm"}})
	if len(res.Problems) != 1 || res.Problems[0].Name != "bau" || len(res.Remembered) != 0 {
		t.Fatalf("a stale entry: %+v", res)
	}
	if !strings.Contains(strings.Join(res.Notes, "\n"), "no longer here") {
		t.Errorf("notes = %v", res.Notes)
	}
}
