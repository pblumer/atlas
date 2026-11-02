package servicereport

import (
	"net/http/httptest"
	"testing"
	"time"

	"github.com/pblumer/atlas/api/catalog"
)

// The names a report shows: the reader's language where the catalogue has it, the
// first declared language that does, any language at all, and the id last.
func TestNamesFallBackInOrder(t *testing.T) {
	langs := []string{"de", "fr"}
	for _, c := range []struct {
		texts map[string]string
		lang  string
		want  string
	}{
		{map[string]string{"fr": "Portable", "de": "Notebook"}, "fr", "Portable"},
		{map[string]string{"fr": "Portable"}, "it", "Portable"},
		{map[string]string{"it": "Portatile", "en": "Laptop"}, "rm", "Laptop"},
		{map[string]string{}, "de", "laptop"},
	} {
		if got := itemNameIn(catalog.Item{ID: "laptop", Texts: c.texts}, c.lang, langs); got != c.want {
			t.Errorf("itemNameIn(%v, %s) = %q, want %q", c.texts, c.lang, got, c.want)
		}
	}
	if got := textIn(map[string]string{"de": "Arbeitsplatz"}, "de", langs, "cat"); got != "Arbeitsplatz" {
		t.Errorf("textIn own language = %q", got)
	}
	if got := textIn(map[string]string{"fr": "Poste"}, "it", langs, "cat"); got != "Poste" {
		t.Errorf("textIn declared fallback = %q", got)
	}
	if got := textIn(nil, "it", langs, "cat"); got != "cat" {
		t.Errorf("textIn last resort = %q", got)
	}
	if reportLang(httptest.NewRequest("GET", "/x?lang=fr", nil)) != "fr" ||
		reportLang(httptest.NewRequest("GET", "/x", nil)) != "de" {
		t.Error("reportLang does not read ?lang= or default to German")
	}
	if WorkingDays(time.Unix(0, 0), time.Unix(86400*2, 0), nil) <= 0 {
		t.Error("WorkingDays without a zone reads UTC rather than nothing")
	}
}
