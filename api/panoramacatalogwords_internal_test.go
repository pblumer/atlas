package api

import "testing"

// How a catalogue and a product are named on a picture that has no reader's
// language to go on (catalogWords).
//
// Every other surface that shows a catalogue knows who is looking: the portal has
// the customer's language, the Console has the session's. The starmap is derived
// once per request and drawn for whoever asked, and the names on it come from a
// multilingual record — so something has to choose, and the choice has to be the
// same on two identical requests or the picture reorders itself between reloads.
func TestHowACatalogueIsNamedOnAPictureWithNoLanguage(t *testing.T) {
	for _, tc := range []struct {
		what  string
		texts map[string]string
		langs []string
		want  string
	}{
		{
			what:  "the catalogue's own first declared language, because its maintainer writes in it",
			texts: map[string]string{"de": "Arbeitsplatz", "en": "Workplace"},
			langs: []string{"de", "en"},
			want:  "Arbeitsplatz",
		},
		{
			what:  "the next declared one where the first was never written",
			texts: map[string]string{"en": "Workplace"},
			langs: []string{"de", "en"},
			want:  "Workplace",
		},
		{
			what:  "whitespace is not a name",
			texts: map[string]string{"de": "   ", "en": "Workplace"},
			langs: []string{"de", "en"},
			want:  "Workplace",
		},
		{
			// Deterministically, and that is the point of sorting rather than taking
			// whichever the map iterated to: two identical requests have to produce one
			// picture, or a reload redraws on noise.
			what:  "the alphabetically first language where none was declared",
			texts: map[string]string{"fr": "Poste de travail", "de": "Arbeitsplatz"},
			langs: nil,
			want:  "Arbeitsplatz",
		},
		{
			what:  "the id, because a node drawn with an empty label is one nobody can find",
			texts: nil,
			langs: []string{"de"},
			want:  "cat_1",
		},
		{
			what:  "and the id again where every text is blank",
			texts: map[string]string{"de": "", "en": "  "},
			langs: []string{"de", "en"},
			want:  "cat_1",
		},
	} {
		if got := catalogWords(tc.texts, tc.langs, "cat_1"); got != tc.want {
			t.Errorf("%s: got %q, want %q", tc.what, got, tc.want)
		}
	}
}
