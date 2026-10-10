package catalog

import "testing"

// TestATagWithAForeignCharacterIsRefused completes TestALanguageTagThatIsNotOneIsRefused
// for the two shapes it does not reach: a primary subtag of the right length that is
// not letters, and a later subtag carrying a character that is neither letter nor
// digit.
func TestATagWithAForeignCharacterIsRefused(t *testing.T) {
	for _, bad := range []string{"d3", "1de", "de-CH!", "zh-Ha.s"} {
		if ValidLanguageTag(bad) {
			t.Errorf("%q is accepted as a language tag", bad)
		}
	}
	// Digits are fine after the primary subtag: a region may be a UN M.49 code.
	if !ValidLanguageTag("es-419") {
		t.Error(`"es-419" (Spanish, Latin America) is refused, and it is a tag`)
	}
}
