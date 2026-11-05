package lookup

import (
	"strings"
	"testing"
)

func TestNormalizeSearchMakesOneQueryOfEverySpelling(t *testing.T) {
	for in, want := range map[string]string{
		"":                "",
		"   ":             "",
		"Anna":            "Anna",
		"  Anna   Mü ":    "Anna Mü",
		"anna\tmüller":    "anna müller",
		"Ünal  Ölçer":     "Ünal Ölçer",
		"o'brien":         "o'brien",
		" a.b@example.ch": "a.b@example.ch",
	} {
		if got := NormalizeSearch(in); got != want {
			t.Errorf("NormalizeSearch(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestCleanTextRefusesControlCharacters(t *testing.T) {
	for _, ok := range []string{"", "Anna", "Müller", "O'Brien", "a+b@example.ch", "名前"} {
		if !CleanText(ok) {
			t.Errorf("CleanText(%q) = false, want true", ok)
		}
	}
	for _, bad := range []string{"a\x00b", "a\nb", "\x1b[31m", "a\u0085b", "\x7f"} {
		if CleanText(bad) {
			t.Errorf("CleanText(%q) = true, want false", bad)
		}
	}
}

// The error says what kind of failure it was and, where the provider said, how long to
// wait — and nothing else, because anything else a provider says carries the request.
func TestErrorCarriesOnlyItsCode(t *testing.T) {
	if got := Fail(CodeUnavailable).Error(); got != "lookup: provider_unavailable" {
		t.Errorf("Error() = %q", got)
	}
	e := &Error{Code: CodeThrottled, RetryAfter: 12}
	if got := e.Error(); !strings.Contains(got, "provider_throttled") || !strings.Contains(got, "12s") {
		t.Errorf("Error() = %q, want the code and the wait", got)
	}
}
