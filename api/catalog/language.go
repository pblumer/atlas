package catalog

import (
	"fmt"
	"strings"
)

// ValidLanguageTag reports whether s is a language tag this catalogue model will
// store: a BCP 47 tag, narrowed to the shape a maintainer types
// (ADR-draft-a-language-tag-is-checked-where-it-is-written).
//
// # Why this exists at all
//
// Because a language tag is an opaque map key everywhere else in this tree. Texts
// are `map[string]string`, the Console draws one box per declared language, and
// the portal looks up `texts[locale]` — none of those can tell a tag from a
// sentence, and none of them fails loudly when the key is nonsense. They simply
// find nothing and fall back, which looks like a product that was never
// translated.
//
// That is not hypothetical. A live catalogue was saved with the single language
// `de; en`, because the list is read comma-separated and somebody typed the
// separator they had been taught for a different field. One box appeared, labelled
// `de; en`; both names went into it; and the portal's language switch did nothing
// at all, for every product in that catalogue, with no screen anywhere saying why.
// The cost of the missing check was measured in weeks, and the check is a regexp.
//
// # The shape
//
// A primary subtag of two or three letters, then any number of subtags of one to
// eight letters or digits, separated by single hyphens: `de`, `de-CH`, `zh-Hans`,
// `pt-BR`. Case is accepted as written, because a tag is compared as stored and
// re-casing somebody's list would move the key their texts are already under.
//
// Deliberately narrower than BCP 47, which also admits grandfathered and private
// forms. A tag this refuses and the standard allows is a tag no catalogue in this
// product has ever carried, and the cost of that narrowness is one refusal a
// person can read. The cost of being liberal is the paragraph above.
func ValidLanguageTag(s string) bool {
	if s == "" || s != strings.TrimSpace(s) {
		return false
	}
	parts := strings.Split(s, "-")
	if n := len(parts[0]); n < 2 || n > 3 || !onlyLetters(parts[0]) {
		return false
	}
	for _, sub := range parts[1:] {
		if len(sub) < 1 || len(sub) > 8 || !onlyLettersOrDigits(sub) {
			return false
		}
	}
	return true
}

func onlyLetters(s string) bool {
	for _, r := range s {
		if (r < 'a' || r > 'z') && (r < 'A' || r > 'Z') {
			return false
		}
	}
	return true
}

func onlyLettersOrDigits(s string) bool {
	for _, r := range s {
		letter := (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z')
		if !letter && (r < '0' || r > '9') {
			return false
		}
	}
	return true
}

// LanguageListProblem reports what is wrong with a catalogue's declared language
// list, or the empty string when nothing is.
//
// It names the offending tag rather than the list. A refusal reading "invalid
// languages" against a list of four leaves somebody guessing which one, and the
// guess is what put the bad tag there in the first place.
//
// A duplicate is refused for a reason of its own and not for tidiness: every
// per-language box in the Console is drawn from this list, so a repeated tag draws
// two boxes writing one key, where the second silently wins and the first looks
// like it was ignored.
func LanguageListProblem(langs []string) string {
	seen := make(map[string]bool, len(langs))
	for _, l := range langs {
		if !ValidLanguageTag(l) {
			return fmt.Sprintf("%q is not a language tag; a catalogue's languages are "+
				"tags like \"de\", \"en\" or \"de-CH\", one per entry", l)
		}
		if seen[l] {
			return fmt.Sprintf("the language %q is listed twice; every per-language "+
				"box is drawn from this list, so the second would silently overwrite "+
				"what was typed in the first", l)
		}
		seen[l] = true
	}
	return ""
}
