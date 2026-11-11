package keep

import (
	"strings"
	"testing"
)

// The forms the language has, each deciding a real directory object.
func TestDecide(t *testing.T) {
	user := map[string]any{
		"id":                "u1",
		"department":        "atlas-test-58o4",
		"displayName":       "atlas-test-58o4 Anna Muster",
		"userPrincipalName": "atlas-test-58o4-anna.muster@contoso.onmicrosoft.com",
		"jobTitle":          nil,
		"accountEnabled":    true,
	}
	cases := []struct {
		keep string
		want Verdict
	}{
		{"department eq 'atlas-test-58o4'", Keep},
		{"department eq 'atlas-test-xxxx'", Drop},
		// Case-insensitive, as Graph's own $filter on directory strings.
		{"department eq 'ATLAS-TEST-58O4'", Keep},
		{"Department EQ 'atlas-test-58o4'", Keep},
		{"startswith(displayName,'atlas-test-58o4')", Keep},
		{"startsWith(displayName, 'ATLAS-test')", Keep},
		{"startswith(displayName,'Anna')", Drop},
		{"department eq 'x' or startswith(userPrincipalName,'atlas-test-58o4-')", Keep},
		{"department eq 'atlas-test-58o4' and startswith(displayName,'nobody')", Drop},
		// and binds tighter than or.
		{"department eq 'x' and department eq 'y' or department eq 'atlas-test-58o4'", Keep},
		{"department eq 'x' and (department eq 'y' or department eq 'atlas-test-58o4')", Drop},
		// null is a value, and it is not a string: it never matches.
		{"jobTitle eq 'Testperson'", Drop},
		// Nor does a boolean, even spelled like one.
		{"accountEnabled eq 'true'", Drop},
		// A doubled quote is a quote.
		{"displayName eq 'O''Brien'", Drop},
	}
	for _, tc := range cases {
		p, err := Parse(tc.keep)
		if err != nil {
			t.Fatalf("Parse(%q): %v", tc.keep, err)
		}
		if got := p.Decide(user); got != tc.want {
			t.Errorf("%q decided %v, want %v", tc.keep, got, tc.want)
		}
	}
	p, _ := Parse("displayName eq 'O''Brien'")
	if p.Decide(map[string]any{"displayName": "o'brien"}) != Keep {
		t.Error("a doubled quote should read as one quote inside the literal")
	}
}

// A property the page does not carry is unknown, not null — the shape of every delta
// page after the first, which carries only what changed. Unknown is never kept, and
// three-valued logic keeps a decision that does not need the missing property.
func TestAMissingPropertyIsUndecided(t *testing.T) {
	changedName := map[string]any{"id": "u1", "displayName": "atlas-test-58o4 Anna Neu"}
	cases := []struct {
		keep string
		want Verdict
	}{
		{"department eq 'atlas-test-58o4'", Undecided},
		{"department eq 'atlas-test-58o4' or startswith(displayName,'atlas-test-58o4')", Keep},
		{"department eq 'atlas-test-58o4' and startswith(displayName,'other')", Drop},
		{"department eq 'atlas-test-58o4' and startswith(displayName,'atlas')", Undecided},
		{"department eq 'atlas-test-58o4' or startswith(displayName,'other')", Undecided},
	}
	for _, tc := range cases {
		p, err := Parse(tc.keep)
		if err != nil {
			t.Fatalf("Parse(%q): %v", tc.keep, err)
		}
		if got := p.Decide(changedName); got != tc.want {
			t.Errorf("%q decided %v on a partial object, want %v", tc.keep, got, tc.want)
		}
	}
}

// A property is found whatever case the author wrote it in, since $select is
// case-insensitive and Graph answers with the canonical spelling.
func TestFieldLookupIgnoresCase(t *testing.T) {
	p, err := Parse("DEPARTMENT eq 'it'")
	if err != nil {
		t.Fatal(err)
	}
	if p.Decide(map[string]any{"department": "IT"}) != Keep {
		t.Error("DEPARTMENT should find department")
	}
}

func TestFieldsListsWhatThePredicateReads(t *testing.T) {
	p, err := Parse("department eq 'a' or (startswith(displayName,'b') and Department eq 'c')")
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(p.Fields(), ","); got != "department,displayName" {
		t.Errorf("Fields() = %q, want each distinct field once, in order", got)
	}
}

// What the language refuses, each with the sentence an author needs at deploy.
func TestParseRefuses(t *testing.T) {
	cases := map[string]string{
		"":                                    "empty",
		"   ":                                 "empty",
		"department ne 'x'":                   `"ne"`,
		"not department eq 'x'":               `"not"`,
		"endswith(mail,'@x.ch')":              `"endswith"`,
		"department gte 'x'":                  "unknown operator",
		"department eq x":                     "single-quoted literal",
		"department eq =stand.abteilung":      "not part of the language",
		`department eq "x"`:                   "double quote",
		"department eq 'x":                    "never closed",
		"department eq ''":                    "empty literal",
		"startswith(displayName,'')":          "empty literal",
		"department eq 'x' department eq 'y'": "should have ended",
		"(department eq 'x'":                  `")" should close`,
		"startswith displayName":              "argument list",
		"startswith(displayName 'x')":         "comma",
		"startswith(displayName,'x'":          `")" should be`,
		"manager/id eq 'x'":                   "not part of the language",
		"department eq 'x' and":               "where a comparison should start",
		"eq eq 'x'":                           "field name",
		"department":                          "operator should follow",
		"@removed eq 'x'":                     "not part of the language",
	}
	for src, want := range cases {
		_, err := Parse(src)
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("Parse(%q) = %v, want an error mentioning %q", src, err, want)
		}
	}
}

func TestParseBounds(t *testing.T) {
	if _, err := Parse(strings.Repeat("(", MaxDepth+1) + "a eq 'b'" + strings.Repeat(")", MaxDepth+1)); err == nil || !strings.Contains(err.Error(), "deeper") {
		t.Errorf("deep nesting: %v", err)
	}
	if _, err := Parse(strings.Repeat("(", MaxDepth) + "a eq 'b'" + strings.Repeat(")", MaxDepth)); err != nil {
		t.Errorf("nesting at the bound should parse: %v", err)
	}
	many := strings.TrimSuffix(strings.Repeat("a eq 'b' or ", MaxComparisons+1), " or ")
	if _, err := Parse(many); err == nil || !strings.Contains(err.Error(), "comparisons") {
		t.Errorf("too many comparisons: %v", err)
	}
	if _, err := Parse("a eq '" + strings.Repeat("x", MaxLength) + "'"); err == nil || !strings.Contains(err.Error(), "bytes long") {
		t.Errorf("too long: %v", err)
	}
	if _, err := Parse("a eq '\xff'"); err == nil || !strings.Contains(err.Error(), "UTF-8") {
		t.Errorf("invalid UTF-8: %v", err)
	}
}

func FuzzParse(f *testing.F) {
	for _, s := range []string{"department eq 'x'", "startswith(a,'b') or (c eq 'd' and e eq 'f')", "((", "'", "a eq 'it''s'"} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, src string) {
		p, err := Parse(src)
		if err != nil {
			return
		}
		// Whatever parses decides every object without panicking.
		p.Decide(map[string]any{})
		p.Decide(map[string]any{"a": "b", "department": nil})
	})
}
