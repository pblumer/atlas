// Package envname turns a connector name or a secret reference into the
// environment variable it is read from.
//
// It exists because three packages have to agree on that answer and none of them
// can see the others do it. The engine renders a supervised worker's environment
// from its own vault (api), a worker reads the same variables out of its own
// environment (worker), and a connector that cannot resolve a reference has to name
// the variable an operator must set (connector/…). One fold applied three ways is a
// bug an operator meets as "I set the variable and it still says it is missing", so
// the fold lives here once and is imported rather than repeated.
package envname

import "strings"

// Key folds a name into the part of a variable name that stands for it: upper case,
// with every run of characters that cannot appear in an environment variable
// becoming a single underscore, and no underscore at the start.
//
// It is deliberately lossy in the one direction that matters — two names that differ
// only in punctuation fold to one variable — and the callers that build a variable
// per name detect the collision and refuse rather than letting one silently take the
// other's value.
func Key(name string) string {
	var b strings.Builder
	b.Grow(len(name))
	pendingSep := false
	for _, r := range strings.ToUpper(name) {
		if (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') {
			if pendingSep && b.Len() > 0 {
				b.WriteByte('_')
			}
			pendingSep = false
			b.WriteRune(r)
			continue
		}
		pendingSep = true
	}
	return b.String()
}

// ConnectorToken is the variable a connector secret reference resolves from
// (ADR-0041): ATLAS_CONNECTOR_<REF>_TOKEN, with REF folded by [Key].
//
// It is the whole point of this package being importable by a connector. A message
// that quotes the pattern leaves an operator to apply the fold in their head, which
// is exactly where "the variable is set and it still is not found" comes from —
// quoting the result of the fold instead makes the error something to act on.
func ConnectorToken(ref string) string {
	return "ATLAS_CONNECTOR_" + Key(ref) + "_TOKEN"
}
