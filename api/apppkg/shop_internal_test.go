package apppkg

import "testing"

func TestQuestionNamesAndTexts(t *testing.T) {
	for in, want := range map[string]string{
		"Alle Mitarbeitenden": "alle-mitarbeitenden", "Prüfer & Größen": "pruefer-groessen",
		"  --  ": "", "Team 42/Nord": "team-42-nord", "Öl-Ämter": "oel-aemter",
	} {
		if got := slug(in); got != want {
			t.Errorf("slug(%q) = %q, want %q", in, got, want)
		}
	}
	item := map[string]any{"id": "p1", "texts": map[string]any{"fr": "Produit"}}
	if textIn(item, "de") != "Produit" || textIn(map[string]any{"id": "p2"}, "en") != "p2" ||
		textIn(map[string]any{"texts": map[string]any{"de": "Nur Deutsch"}}, "en") != "Nur Deutsch" {
		t.Error("textIn falls back wrongly")
	}
	dir := []DirectoryEntry{{Type: "user", ID: "x", Name: "Ein Mensch"}}
	if e, ok := findByID(dir, "group", "x"); !ok || e.Type != "user" {
		t.Errorf("an id of another kind = %+v, %v", e, ok)
	}
	q := newQuestioner(dir, nil)
	if got := q.ask("group", "{{schon}}", "", ""); got != "{{schon}}" {
		t.Errorf("a value that is already a question = %q", got)
	}
	if got := q.ask("group", "", "", ""); got != "" {
		t.Errorf("an empty value = %q", got)
	}
}
