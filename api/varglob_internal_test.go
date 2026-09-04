package api

import "testing"

// A search term is taken literally unless it says otherwise. This is the whole point
// of the change: "kdnr=MT-100" asked for MT-100, and answering with MT-10001 as well
// is not a helpful widening — it is a different question, and an operator reading a
// list of near-misses has no way to tell which row is the one they asked for.
func TestGlobMatchesLiterallyUnlessAskedNotTo(t *testing.T) {
	cases := []struct {
		pattern, value string
		want           bool
	}{
		{"MT-100", "MT-100", true},
		{"MT-100", "MT-10001", false},
		{"MT-100", "XMT-100", false},
		{"MT-100", "mt-100", true}, // case-insensitive, as the search always was
		{"", "", true},
		{"", "anything", false},

		// * stands for any run of characters, including none.
		{"MT-*", "MT-100", true},
		{"MT-*", "MT-", true},
		{"MT-*", "XMT-100", false},
		{"*100", "MT-100", true},
		{"*100*", "MT-10001", true},
		{"MT*01", "MT-10001", true},
		{"*", "anything at all", true},

		// ? stands for exactly one character, never none.
		{"MT-10?", "MT-100", true},
		{"MT-10?", "MT-10", false},
		{"MT-10?", "MT-1001", false},
		{"?", "a", true},
		{"?", "", false},

		// A backslash makes the next character literal, so a value that really holds a
		// star is reachable. Without this, such a value could not be searched for at all.
		{`10\*`, "10*", true},
		{`10\*`, "10x", false},
		{`10\?`, "10?", true},
		{`a\\b`, `a\b`, true},
		// A trailing backslash is a literal backslash rather than an error: an operator
		// mid-typing should not be told their query is malformed.
		{`ab\`, `ab\`, true},
	}
	for _, c := range cases {
		if got := globMatch(c.pattern, c.value); got != c.want {
			t.Errorf("globMatch(%q, %q) = %v, want %v", c.pattern, c.value, got, c.want)
		}
	}
}

// A pattern's literal head is what an ordered index can seek to. Everything from the
// first wildcard on has to be filtered, but the head still turns a scan of every
// value into a scan of one neighbourhood.
func TestGlobLiteralPrefixIsWhatAnIndexCanSeek(t *testing.T) {
	cases := []struct {
		pattern, head string
		wild          bool
	}{
		{"MT-100", "MT-100", false},
		{"MT-*", "MT-", true},
		{"MT-10?", "MT-10", true},
		{"*100", "", true},
		{"?", "", true},
		{`10\*`, "10*", false},
		{`10\*x*`, "10*x", true},
	}
	for _, c := range cases {
		head, wild := globPrefix(c.pattern)
		if head != c.head || wild != c.wild {
			t.Errorf("globPrefix(%q) = (%q, %v), want (%q, %v)", c.pattern, head, wild, c.head, c.wild)
		}
	}
}

// Backtracking is what makes a * pattern correct, and a naive implementation of it is
// what makes a crafted pattern hang. A search box is reachable by anyone who may look
// at instances, so a pattern of many stars must stay cheap.
func TestGlobDoesNotBlowUpOnManyStars(t *testing.T) {
	pattern := "*a*a*a*a*a*a*a*a*a*a*a*a*a*a*a*a*a*a*a*a*b"
	value := "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	if globMatch(pattern, value) {
		t.Error("pattern matched a value it should not")
	}
}
