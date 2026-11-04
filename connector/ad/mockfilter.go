package ad

import (
	"fmt"
	"strconv"
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
// substring with wildcards, the &/|/! that compose them, and Active Directory's two
// bitwise matching rules — LDAP_MATCHING_RULE_BIT_AND (1.2.840.113556.1.4.803) and
// LDAP_MATCHING_RULE_BIT_OR (…804) — because
// (!(userAccountControl:1.2.840.113556.1.4.803:=2)), *only enabled accounts*, is how a
// model asks AD whether an account is usable without reading its flags (ADR-0166,
// amended 2026-11-03). An assertion value is decoded as RFC 4515 writes it, so a DN
// escaped into a filter (\5c, \2a, \28, \29) matches the DN it came from. Everything
// else — ordering (>=, <=), approximate (~=), any other extensible match — is
// **refused rather than ignored**, so a model that needs one learns it here instead
// of quietly getting more entries than it asked for.

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
// compares the string syntaxes an identity model filters on. parts is the assertion
// value split at its unescaped wildcards and then decoded, so one part is an equality
// and more are a substring match — an escaped \2a is a literal asterisk, not a
// wildcard.
type filterItem struct {
	attr     string
	parts    []string
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
		if matchParts(f.parts, v) {
			return true
		}
	}
	return false
}

// filterBits is AD's bitwise matching rule: the attribute holds an integer, and all
// (BIT_AND) or any (BIT_OR) of the asserted bits are set in it.
type filterBits struct {
	attr string
	bits uint64
	all  bool
}

// The two rule OIDs, as AD names them.
const (
	matchingRuleBitAnd = "1.2.840.113556.1.4.803"
	matchingRuleBitOr  = "1.2.840.113556.1.4.804"
)

func (f filterBits) match(attrs map[string][]string) bool {
	_, vals, ok := findAttr(attrs, f.attr)
	if !ok {
		return false
	}
	for _, v := range vals {
		n, err := strconv.ParseInt(strings.TrimSpace(v), 10, 64)
		if err != nil {
			continue
		}
		set := uint64(n) & f.bits
		if (f.all && set == f.bits) || (!f.all && set != 0) {
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
	if inner[i-1] == ':' {
		return parseBitFilter(inner, inner[:i-1], strings.TrimSpace(inner[i+1:]))
	}
	if strings.ContainsAny(inner[i-1:i], "><~") {
		return nil, filterErr(inner, "only equality, presence, wildcards and AD's bitwise matching rules are applied by the mock directory")
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
	parts, err := assertionParts(value)
	if err != nil {
		return nil, filterErr(inner, err.Error())
	}
	return filterItem{attr: attr, parts: parts}, nil
}

// parseBitFilter parses an extensible match attr:rule:=value and admits exactly AD's
// two bitwise rules; any other rule, and the dn-attributes form, is refused.
func parseBitFilter(inner, left, value string) (mockFilter, error) {
	attr, rule, ok := strings.Cut(strings.TrimSpace(left), ":")
	attr, rule = strings.TrimSpace(attr), strings.TrimSpace(rule)
	if !ok || attr == "" || (rule != matchingRuleBitAnd && rule != matchingRuleBitOr) {
		return nil, filterErr(inner, "the only extensible matches the mock directory applies are AD's bitwise rules, attribute:"+
			matchingRuleBitAnd+":=n (all bits set) and attribute:"+matchingRuleBitOr+":=n (any bit set)")
	}
	n, err := strconv.ParseUint(value, 10, 64)
	if err != nil {
		return nil, filterErr(inner, "a bitwise matching rule takes a non-negative integer")
	}
	return filterBits{attr: attr, bits: n, all: rule == matchingRuleBitAnd}, nil
}

// assertionParts splits an assertion value at its unescaped wildcards and decodes RFC
// 4515's escapes — a backslash and two hex digits — in each part.
func assertionParts(value string) ([]string, error) {
	var (
		parts []string
		cur   []byte
	)
	for i := 0; i < len(value); i++ {
		switch c := value[i]; c {
		case '*':
			parts = append(parts, string(cur))
			cur = cur[:0]
		case '\\':
			if i+2 >= len(value) {
				return nil, fmt.Errorf("an escape needs two hex digits after the backslash")
			}
			b, err := strconv.ParseUint(value[i+1:i+3], 16, 8)
			if err != nil {
				return nil, fmt.Errorf("%q is not an escape (a backslash and two hex digits)", value[i:i+3])
			}
			cur = append(cur, byte(b))
			i += 2
		default:
			cur = append(cur, c)
		}
	}
	return append(parts, string(cur)), nil
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

// matchParts matches a decoded assertion against a stored value, case-insensitively:
// one part is an equality, more are a substring match with a wildcard between each
// two.
func matchParts(parts []string, value string) bool {
	if len(parts) == 1 {
		return strings.EqualFold(parts[0], value)
	}
	lower := make([]string, len(parts))
	for i, p := range parts {
		lower[i] = strings.ToLower(p)
	}
	parts = lower
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
