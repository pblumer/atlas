package ad

import (
	"fmt"
	"strings"
)

// The mock directory's LDAP filter, and why it is only part of one.
//
// A DirSync pass carries the filter the model authored, and the mock has to answer it
// with the entries it actually selects — a filter that were ignored would hand a
// process the whole naming context while the real directory hands it a handful, which
// is exactly the difference a mockup run exists to catch.
//
// What is supported is what an identity model authors: equality, presence, a
// substring with wildcards, and the &/|/! that compose them. Everything else —
// ordering (>=, <=), approximate (~=), extensible matches — is **refused rather than
// ignored**, so a model that needs one learns it here instead of quietly getting more
// entries than it asked for.

// mockFilter matches an entry's attributes.
type mockFilter interface {
	match(attrs map[string][]string) bool
}

// filterAll matches every entry: an empty filter, or (objectClass=*).
type filterAll struct{}

func (filterAll) match(map[string][]string) bool { return true }

// filterAnd, filterOr and filterNot are the composers.
type filterAnd []mockFilter

func (f filterAnd) match(attrs map[string][]string) bool {
	for _, sub := range f {
		if !sub.match(attrs) {
			return false
		}
	}
	return true
}

type filterOr []mockFilter

func (f filterOr) match(attrs map[string][]string) bool {
	for _, sub := range f {
		if sub.match(attrs) {
			return true
		}
	}
	return false
}

type filterNot struct{ sub mockFilter }

func (f filterNot) match(attrs map[string][]string) bool { return !f.sub.match(attrs) }

// filterItem is one assertion: an attribute is present, equals a value, or matches a
// pattern with wildcards. Matching is case-insensitive, which is how a directory
// compares the string syntaxes an identity model filters on.
type filterItem struct {
	attr     string
	value    string
	presence bool
}

func (f filterItem) match(attrs map[string][]string) bool {
	_, vals, ok := findAttr(attrs, f.attr)
	if !ok || len(vals) == 0 {
		return false
	}
	if f.presence {
		return true
	}
	for _, v := range vals {
		if matchPattern(f.value, v) {
			return true
		}
	}
	return false
}

// parseFilter compiles an LDAP filter, or reports why it cannot.
func parseFilter(filter string) (mockFilter, error) {
	s := strings.TrimSpace(filter)
	if s == "" {
		return filterAll{}, nil
	}
	f, rest, err := parseFilterExpr(s)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(rest) != "" {
		return nil, filterErr(filter, "trailing text after the closing parenthesis")
	}
	return f, nil
}

// parseFilterExpr parses one parenthesized filter and returns what follows it.
func parseFilterExpr(s string) (mockFilter, string, error) {
	s = strings.TrimSpace(s)
	if !strings.HasPrefix(s, "(") {
		return nil, "", filterErr(s, "want a parenthesized filter, e.g. (objectClass=user)")
	}
	end := matchingParen(s)
	if end < 0 {
		return nil, "", filterErr(s, "unbalanced parentheses")
	}
	inner, rest := strings.TrimSpace(s[1:end]), s[end+1:]
	switch {
	case strings.HasPrefix(inner, "&"), strings.HasPrefix(inner, "|"):
		subs, err := parseFilterList(inner[1:])
		if err != nil {
			return nil, "", err
		}
		if strings.HasPrefix(inner, "&") {
			return filterAnd(subs), rest, nil
		}
		return filterOr(subs), rest, nil
	case strings.HasPrefix(inner, "!"):
		sub, tail, err := parseFilterExpr(inner[1:])
		if err != nil {
			return nil, "", err
		}
		if strings.TrimSpace(tail) != "" {
			return nil, "", filterErr(inner, "! takes exactly one filter")
		}
		return filterNot{sub: sub}, rest, nil
	default:
		item, err := parseFilterItem(inner)
		if err != nil {
			return nil, "", err
		}
		return item, rest, nil
	}
}

// parseFilterList parses the one-or-more filters inside an & or an |.
func parseFilterList(s string) ([]mockFilter, error) {
	var out []mockFilter
	for rest := strings.TrimSpace(s); rest != ""; {
		f, tail, err := parseFilterExpr(rest)
		if err != nil {
			return nil, err
		}
		out = append(out, f)
		rest = strings.TrimSpace(tail)
	}
	if len(out) == 0 {
		return nil, filterErr(s, "& and | need at least one filter")
	}
	return out, nil
}

// parseFilterItem parses one assertion, refusing the match types the mock does not
// apply rather than approximating them.
func parseFilterItem(inner string) (mockFilter, error) {
	i := strings.Index(inner, "=")
	if i <= 0 {
		return nil, filterErr(inner, "want attribute=value")
	}
	if strings.ContainsAny(inner[i-1:i], "><~:") {
		return nil, filterErr(inner, "only equality, presence and wildcards are applied by the mock directory")
	}
	attr := strings.TrimSpace(inner[:i])
	value := strings.TrimSpace(inner[i+1:])
	if attr == "" || value == "" {
		return nil, filterErr(inner, "want attribute=value")
	}
	if value == "*" {
		if strings.EqualFold(attr, attrObjectClass) {
			// (objectClass=*) is LDAP's "everything", including an entry that was
			// seeded without one.
			return filterAll{}, nil
		}
		return filterItem{attr: attr, presence: true}, nil
	}
	return filterItem{attr: attr, value: value}, nil
}

// matchingParen returns the index of the parenthesis closing the one at s[0], or -1.
func matchingParen(s string) int {
	depth := 0
	for i, r := range s {
		switch r {
		case '(':
			depth++
		case ')':
			depth--
			if depth == 0 {
				return i
			}
		}
	}
	return -1
}

// matchPattern matches an LDAP assertion value against a stored one,
// case-insensitively, honouring * as the wildcard it is in a substring filter.
func matchPattern(pattern, value string) bool {
	if !strings.Contains(pattern, "*") {
		return strings.EqualFold(pattern, value)
	}
	parts := strings.Split(strings.ToLower(pattern), "*")
	v := strings.ToLower(value)
	if head := parts[0]; head != "" {
		if !strings.HasPrefix(v, head) {
			return false
		}
		v = v[len(head):]
	}
	if tail := parts[len(parts)-1]; tail != "" {
		if !strings.HasSuffix(v, tail) {
			return false
		}
		v = v[:len(v)-len(tail)]
	}
	for _, mid := range parts[1 : len(parts)-1] {
		if mid == "" {
			continue
		}
		at := strings.Index(v, mid)
		if at < 0 {
			return false
		}
		v = v[at+len(mid):]
	}
	return true
}

// filterErr is the one shape of filter complaint, so every one of them names the
// filter and says what the mock could not do with it.
func filterErr(filter, why string) error {
	return fmt.Errorf("ad: mock: filter %q: %s", strings.TrimSpace(filter), why)
}
