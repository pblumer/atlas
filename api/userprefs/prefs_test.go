package userprefs

import (
	"strings"
	"testing"
)

// An unset field is today's behaviour, and it has to stay reachable: a person who
// clears the screen back to the defaults must end up with the console they had
// before the screen existed, not with a record that insists on something.
func TestTheEmptyRecordIsValid(t *testing.T) {
	if err := (Prefs{UserID: "u1"}).Validate(); err != nil {
		t.Fatalf("the unset record must be valid: %v", err)
	}
}

func TestValidate(t *testing.T) {
	cases := []struct {
		name string
		in   Prefs
		want string // the substring the refusal must name; "" means accepted
	}{
		{"international", Prefs{Region: "international"}, ""},
		{"swiss", Prefs{Region: "ch"}, ""},
		{"german", Prefs{Region: "de"}, ""},
		{"american", Prefs{Region: "us"}, ""},
		// A region the client invented is refused rather than stored and ignored:
		// stored, it would read back as a choice somebody made.
		{"unknown region", Prefs{Region: "fr"}, "region"},
		{"region case matters", Prefs{Region: "CH"}, "region"},

		{"german interface", Prefs{Language: "de"}, ""},
		{"english interface", Prefs{Language: "en"}, ""},
		// The catalogue decides which languages exist; a language with no catalogue
		// would render as the keys themselves on every translated screen.
		{"language without a catalogue", Prefs{Language: "fr"}, "language"},

		{"a zone the database knows", Prefs{TimeZone: "Europe/Zurich"}, ""},
		{"utc", Prefs{TimeZone: "UTC"}, ""},
		// The browser is what formats, and Intl throws on a zone it does not know —
		// which would break every timestamp on the screen rather than this field.
		{"a zone nobody has", Prefs{TimeZone: "Europe/Zürich"}, "time zone"},
		{"not a zone at all", Prefs{TimeZone: "../../etc/passwd"}, "time zone"},

		// Appearance is the stylesheet's to draw (ADR-0507): the three words it
		// answers, and nothing a later client might invent — stored, an unknown word
		// would read back as a choice the console cannot honour.
		{"light", Prefs{Appearance: "light"}, ""},
		{"dark", Prefs{Appearance: "dark"}, ""},
		{"following the system", Prefs{Appearance: "system"}, ""},
		{"an appearance nobody draws", Prefs{Appearance: "sepia"}, "appearance"},
		{"appearance case matters", Prefs{Appearance: "Dark"}, "appearance"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := c.in.Validate()
			if c.want == "" {
				if err != nil {
					t.Fatalf("Validate() = %v, want accepted", err)
				}
				return
			}
			if err == nil {
				t.Fatalf("Validate() accepted %+v, want a refusal naming %q", c.in, c.want)
			}
			if !strings.Contains(err.Error(), c.want) {
				t.Fatalf("Validate() = %q, want it to name %q", err, c.want)
			}
		})
	}
}

// The zone database travels with the binary (see the tzdata import), so a zone is
// accepted or refused the same way on a developer's laptop and in a container
// built from scratch. Without that, the same record would be valid on one host
// and rejected on another.
func TestZonesResolveWithoutTheHostsDatabase(t *testing.T) {
	t.Setenv("ZONEINFO", "/nonexistent")
	if err := (Prefs{TimeZone: "America/New_York"}).Validate(); err != nil {
		t.Fatalf("a zone must resolve from the embedded database: %v", err)
	}
}
