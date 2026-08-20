package mimimport

import (
	"strings"

	"github.com/pblumer/atlas/expr"
)

// translateCondition converts a MIMWAL ActivityExecutionCondition — an
// XPath-flavoured WAL expression such as
//
//	ConvertToBoolean(//WorkflowData/HasStructureResponsibilities) Or
//	ConvertToBoolean(//WorkflowData/HasDisableInheritanceResponsibilities)
//
// into an equivalent FEEL expression, returning ok=false when the source uses a
// construct this first-pass translator does not confidently understand. The
// candidate is accepted only when it also compiles through the real Atlas FEEL
// engine, so a generated skip gateway never carries a condition the compiler
// would later reject at deploy time.
func translateCondition(src string) (string, bool) {
	feelExpr, ok := walToFEEL(src)
	if !ok {
		return "", false
	}
	if _, err := expr.CompileAuto(feelExpr); err != nil {
		return "", false
	}
	return feelExpr, true
}

// walToFEEL rewrites a WAL condition token by token. It is deliberately strict:
// any token outside the recognised set (an unknown function call, an
// untranslatable literal, a stray symbol) aborts the whole translation so the
// caller falls back to preserving the original for manual review rather than
// emitting a subtly wrong condition.
//
// Recognised constructs:
//   - the logical keywords And / Or (case-insensitive) → and / or;
//   - the WAL functions ConvertToBoolean(x) → (x) and Not(x) → not(x);
//   - comparison operators = != <> < <= > >= (<> → !=);
//   - string, number and boolean literals;
//   - XPath references (//WorkflowData/Foo, //Target/PersonType/Bar,
//     //WorkflowData["Foo"]) mapped to FEEL variable paths;
//   - parentheses.
func walToFEEL(src string) (string, bool) {
	s := strings.TrimSpace(src)
	if s == "" {
		return "", false
	}
	var out strings.Builder
	n := len(s)
	for i := 0; i < n; {
		c := s[i]
		switch {
		case c == ' ', c == '\t', c == '\r', c == '\n':
			i++
		case c == '(' || c == ')':
			out.WriteByte(c)
			i++
		case c == '"' || c == '\'':
			lit, ni, ok := scanString(s, i)
			if !ok {
				return "", false
			}
			out.WriteString(lit)
			i = ni
		case c == '=':
			out.WriteString(" = ")
			i++
			if i < n && s[i] == '=' { // tolerate a WAL "=="
				i++
			}
		case c == '!':
			if i+1 < n && s[i+1] == '=' {
				out.WriteString(" != ")
				i += 2
			} else {
				return "", false
			}
		case c == '<':
			switch {
			case i+1 < n && s[i+1] == '=':
				out.WriteString(" <= ")
				i += 2
			case i+1 < n && s[i+1] == '>':
				out.WriteString(" != ")
				i += 2
			default:
				out.WriteString(" < ")
				i++
			}
		case c == '>':
			if i+1 < n && s[i+1] == '=' {
				out.WriteString(" >= ")
				i += 2
			} else {
				out.WriteString(" > ")
				i++
			}
		case c == '/', c == '[':
			ref, ni, ok := scanRef(s, i)
			if !ok {
				return "", false
			}
			out.WriteString(ref)
			i = ni
		case isDigit(c):
			num, ni := scanNumber(s, i)
			out.WriteString(num)
			i = ni
		case isIdentStart(c):
			word, ni := scanIdent(s, i)
			j := skipWS(s, ni)
			isCall := j < n && s[j] == '('
			switch lw := strings.ToLower(word); {
			case lw == "and":
				out.WriteString(" and ")
				i = ni
			case lw == "or":
				out.WriteString(" or ")
				i = ni
			case lw == "true" || lw == "false":
				out.WriteString(lw)
				i = ni
			case isCall && lw == "converttoboolean":
				i = ni // drop the wrapper; the following (expr) is already boolean
			case isCall && lw == "not":
				out.WriteString("not")
				i = ni
			case isCall:
				return "", false // an unrecognised function call
			case ni < n && (s[ni] == '/' || s[ni] == '['):
				ref, nj, ok := scanRef(s, i) // an identifier-led path, e.g. WorkflowData["Foo"]
				if !ok {
					return "", false
				}
				out.WriteString(ref)
				i = nj
			default:
				out.WriteString(word) // a bare variable reference
				i = ni
			}
		default:
			return "", false
		}
	}
	return strings.TrimSpace(out.String()), true
}

// scanRef reads an XPath-flavoured reference and maps it to a FEEL variable path.
// It accepts an optional leading // or /, then segments separated by /, where a
// segment is an identifier optionally followed by ["key"] / ['key'] indexers.
// The implicit WorkflowData scope is dropped so //WorkflowData/Foo becomes Foo,
// while a rooted object path like //Target/PersonType/Bar becomes
// Target.PersonType.Bar. Any segment that is not a simple FEEL name aborts.
func scanRef(s string, i int) (string, int, bool) {
	n := len(s)
	for i < n && s[i] == '/' { // leading // or /
		i++
	}
	var segs []string
	expectSeg := true // just consumed a leading or separating '/'
	for i < n {
		if isIdentStart(s[i]) {
			seg, ni := scanIdent(s, i)
			segs = append(segs, seg)
			i = ni
			expectSeg = false
			for i < n && s[i] == '[' { // ["key"] / ['key'] indexers on this segment
				key, nj, ok := scanIndexer(s, i)
				if !ok {
					return "", 0, false
				}
				segs = append(segs, key)
				i = nj
			}
			continue
		}
		if s[i] == '/' {
			i++
			expectSeg = true
			continue
		}
		break
	}
	if len(segs) == 0 || expectSeg {
		return "", 0, false // no segment, or a dangling '/' with none after it
	}
	if len(segs) > 1 && strings.EqualFold(segs[0], "WorkflowData") {
		segs = segs[1:]
	}
	for _, seg := range segs {
		if !isName(seg) {
			return "", 0, false
		}
	}
	return strings.Join(segs, "."), i, true
}

// scanIndexer reads a ["key"] or ['key'] segment and returns the bare key.
func scanIndexer(s string, i int) (string, int, bool) {
	n := len(s)
	if i >= n || s[i] != '[' {
		return "", 0, false
	}
	i++
	if i >= n || (s[i] != '"' && s[i] != '\'') {
		return "", 0, false
	}
	quote := s[i]
	i++
	start := i
	for i < n && s[i] != quote {
		i++
	}
	if i >= n {
		return "", 0, false
	}
	key := s[start:i]
	i++ // closing quote
	if i >= n || s[i] != ']' {
		return "", 0, false
	}
	i++ // closing bracket
	return key, i, true
}

// scanString reads a "..." or '...' literal and returns it as a FEEL
// double-quoted string with inner quotes and backslashes escaped.
func scanString(s string, i int) (string, int, bool) {
	n := len(s)
	quote := s[i]
	i++
	var b strings.Builder
	b.WriteByte('"')
	for i < n && s[i] != quote {
		switch s[i] {
		case '\\', '"':
			b.WriteByte('\\')
		}
		b.WriteByte(s[i])
		i++
	}
	if i >= n {
		return "", 0, false // unterminated
	}
	i++ // closing quote
	b.WriteByte('"')
	return b.String(), i, true
}

// scanNumber reads a run of digits with at most one decimal point.
func scanNumber(s string, i int) (string, int) {
	n := len(s)
	start := i
	dot := false
	for i < n {
		if s[i] == '.' && !dot {
			dot = true
			i++
			continue
		}
		if !isDigit(s[i]) {
			break
		}
		i++
	}
	return s[start:i], i
}

// scanIdent reads a run of identifier characters (letters, digits, underscore).
func scanIdent(s string, i int) (string, int) {
	n := len(s)
	start := i
	for i < n && isIdentPart(s[i]) {
		i++
	}
	return s[start:i], i
}

func skipWS(s string, i int) int {
	for i < len(s) && (s[i] == ' ' || s[i] == '\t' || s[i] == '\r' || s[i] == '\n') {
		i++
	}
	return i
}

func isDigit(c byte) bool      { return c >= '0' && c <= '9' }
func isIdentStart(c byte) bool { return c == '_' || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') }
func isIdentPart(c byte) bool  { return isIdentStart(c) || isDigit(c) }

// isName reports whether seg is a simple FEEL name (identifier-start followed by
// identifier parts) — the only reference segment shape this translator emits.
func isName(seg string) bool {
	if seg == "" || !isIdentStart(seg[0]) {
		return false
	}
	for i := 1; i < len(seg); i++ {
		if !isIdentPart(seg[i]) {
			return false
		}
	}
	return true
}
