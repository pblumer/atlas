package api

import "strings"

// Wildcards in an instance search (ADR-0241, ADR-0244).
//
// A search term is literal. "kdnr=MT-100" asks for MT-100 and gets MT-100 — not
// MT-10001, which is a different customer. The search used to widen every term into
// a substring match, which reads as helpful until the list comes back holding rows
// the operator did not ask for and cannot tell apart from the one they did. Worse,
// it was not even consistent: a name the model declares searchable was answered
// exactly from the index, and any other name by substring, so the same query behaved
// differently depending on a property of the model the operator cannot see.
//
// Widening is now something the operator asks for, in the two shapes everyone
// already knows from shells and file pickers: * for any run of characters and ? for
// exactly one. A backslash escapes either, so a value that really contains a star is
// still reachable.

// globMatch reports whether value satisfies pattern. Matching is case-insensitive,
// as every comparison in the instance search has always been.
//
// The walk is the two-pointer one rather than recursion: it backtracks to the last *
// instead of branching at it, so a pattern full of stars costs O(len(pattern) ×
// len(value)) and not an exponential search. A search box is reachable by anyone who
// may look at instances, and "*a*a*a*a*…" must not be a way to occupy a request.
func globMatch(pattern, value string) bool {
	p, v := strings.ToLower(pattern), strings.ToLower(value)
	// star is where the last * sat in the pattern, and mark where the value stood when
	// it was taken — the pair the walk returns to when a later literal fails.
	pi, vi, star, mark := 0, 0, -1, 0
	for vi < len(v) {
		switch {
		case pi < len(p) && p[pi] == '?':
			pi++
			vi++
		case pi < len(p) && p[pi] != '*' && literalAt(p, pi) == v[vi]:
			pi += escapedLen(p, pi)
			vi++
		case pi < len(p) && p[pi] == '*':
			star, pi = pi, pi+1
			mark = vi
		case star >= 0:
			// The last * takes one more character and the walk resumes after it.
			pi = star + 1
			mark++
			vi = mark
		default:
			return false
		}
	}
	// Trailing stars may still absorb nothing; anything else left over cannot match.
	for pi < len(p) && p[pi] == '*' {
		pi++
	}
	return pi == len(p)
}

// escapedLen is how many pattern bytes the literal at i occupies: two for an escape
// sequence, one otherwise. A trailing backslash is a literal backslash rather than an
// error — an operator mid-typing should not be told their query is malformed.
func escapedLen(p string, i int) int {
	if p[i] == '\\' && i+1 < len(p) {
		return 2
	}
	return 1
}

// literalAt is the byte the literal at i stands for, seeing through an escape.
func literalAt(p string, i int) byte {
	if p[i] == '\\' && i+1 < len(p) {
		return p[i+1]
	}
	return p[i]
}

// globPrefix splits a pattern into the literal head an ordered index can seek to and
// whether anything wild follows. "MT-10?" seeks to "MT-10" and filters the rest;
// "*100" has no head at all and must read the whole name's range. Either way the
// answer stays bounded by the search cap — the head only decides how much of the
// index is touched to reach it.
func globPrefix(pattern string) (string, bool) {
	var head strings.Builder
	for i := 0; i < len(pattern); {
		if pattern[i] == '*' || pattern[i] == '?' {
			return head.String(), true
		}
		n := escapedLen(pattern, i)
		head.WriteByte(literalAt(pattern, i))
		i += n
	}
	return head.String(), false
}
