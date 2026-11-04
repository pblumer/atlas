package compiler

import (
	"fmt"
	"regexp"
	"strings"
	"unicode"
)

// The three AD worker attributes the 2026-11-03 amendment of ADR-0166 adds: an inline
// `attributes` template for the operations that write an entry, a `select` that
// narrows what a search returns, and a `foundVariable` that receives a search's
// `found` on its own.
//
// All three exist for the same reason: personal data (ADR-0314). A value a process
// declares personal is ciphertext wherever the engine evaluates an expression, so an
// entry the engine had to build in a variable (entryVariable) could never carry one,
// and a search result a gateway had to read could never be sealed. The template is
// evaluated by the worker at handoff — the edge where ADR-0314 permits plaintext — and
// foundVariable gives the gateway a boolean that is not personal, so the result itself
// can be.

// adEntryShape is the AD worker's rule on an inline attributes template: every value is
// one an LDAP attribute can hold. A string, number, boolean or null is one value (null:
// none); a list is several, each of them scalar. A nested object is refused here, at
// deploy, rather than flattened or stringified at runtime — there is no attribute
// syntax it maps onto, and guessing one would write something nobody authored.
func adEntryShape(obj map[string]any) error {
	for name, v := range obj {
		if strings.TrimSpace(name) == "" {
			return fmt.Errorf("an attribute has an empty name")
		}
		switch t := v.(type) {
		case map[string]any:
			return fmt.Errorf("attribute %q is an object; an LDAP attribute holds values, so give it a string, a number, a boolean, a list of them, or null", name)
		case []any:
			for _, e := range t {
				switch e.(type) {
				case map[string]any, []any:
					return fmt.Errorf("attribute %q lists a %T; each value of a multi-valued attribute is a string, a number or a boolean", name, e)
				}
			}
		}
	}
	return nil
}

// ldapAttrNameRe is an attribute description as RFC 4512 §2.5 spells it: a descriptor
// (a letter, then letters, digits and hyphens) or a numeric OID, followed by any
// options (";binary", ";lang-de"). AD's ranged retrieval (";range=0-*") is not an
// option by that grammar and is not admitted: it changes the shape of what comes back,
// which ADR-0166 leaves to a decision of its own.
var ldapAttrNameRe = regexp.MustCompile(`^(?:[A-Za-z][A-Za-z0-9-]*|[0-9]+(?:\.[0-9]+)+)(?:;[A-Za-z0-9-]+)*$`)

// adSelectList validates a search's authored attribute selection and returns it in a
// canonical form: the names comma-separated with the whitespace gone, in the order
// written. Besides attribute names it admits RFC 4511's two selectors — "*" for every
// user attribute and "+" for the operational ones — because a model that wants
// "everything, plus whenChanged" has no other way to say so. "1.1" (no attributes at
// all) is a numeric OID and passes as one.
//
// A duplicate is refused rather than dropped: LDAP attribute names are
// case-insensitive, so "mail,Mail" is a typo or a misunderstanding, not a request.
func adSelectList(taskID, raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", nil
	}
	seen := map[string]bool{}
	names := make([]string, 0, 4)
	for _, part := range strings.Split(raw, ",") {
		name := strings.TrimSpace(part)
		if name == "" {
			return "", fmt.Errorf("compiler: ad task %q select %q has an empty attribute name; separate names with single commas", taskID, raw)
		}
		if name != "*" && name != "+" && !ldapAttrNameRe.MatchString(name) {
			return "", fmt.Errorf("compiler: ad task %q select names %q, which is not an LDAP attribute name (a letter, then letters, digits and hyphens, or a numeric OID; * and + select all user or operational attributes)", taskID, name)
		}
		key := strings.ToLower(name)
		if seen[key] {
			return "", fmt.Errorf("compiler: ad task %q select names %q twice; LDAP attribute names are case-insensitive", taskID, name)
		}
		seen[key] = true
		names = append(names, name)
	}
	return strings.Join(names, ","), nil
}

// validVariableName reports whether name can name a process variable a worker writes
// and a FEEL expression later reads: a letter or underscore, then letters, digits and
// underscores. A name outside that — "gruppe.found", "ist gefunden" — would be written
// as one variable and read back as a path or as two names, so it is refused where it is
// authored rather than discovered at the gateway that never matches.
func validVariableName(name string) bool {
	if name == "" {
		return false
	}
	for i, r := range name {
		switch {
		case r == '_' || unicode.IsLetter(r):
		case i > 0 && unicode.IsDigit(r):
		default:
			return false
		}
	}
	return true
}
