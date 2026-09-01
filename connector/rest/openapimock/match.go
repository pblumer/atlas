package openapimock

import (
	"fmt"
	"sort"
	"strings"
)

// Matching a request path against the paths a document describes.
//
// This is deliberately not [net/http.ServeMux]. A mux pattern would express most
// OpenAPI templates — /pets/{petId} is the same string in both — but the paths come
// from a file somebody else wrote, and a mux panics when two registered patterns
// conflict. A document holding both /pets/{id} and /pets/{petId}, or a segment like
// /reports/{year}-{month}.csv that a mux cannot express at all, would take the process
// down at startup instead of being served. So the matcher is ours: it accepts every
// template OpenAPI allows, and orders them so the literal path wins.

// part is one piece of a path segment: a literal run of text, or a {parameter}.
type part struct {
	text  string
	param bool
}

// segment is one path segment compiled into the parts it matches, with the score that
// ranks it against a competing template: a literal segment beats a mixed one, which
// beats a bare parameter.
type segment struct {
	parts []part
	score int
}

// compilePath turns "/reports/{year}-{month}.csv" into its segments.
func compilePath(path string) ([]segment, error) {
	var out []segment
	for _, raw := range strings.Split(strings.TrimPrefix(path, "/"), "/") {
		parts, err := compileSegment(raw, path)
		if err != nil {
			return nil, err
		}
		seg := segment{parts: parts, score: 1}
		switch {
		case !slicesAny(parts, func(p part) bool { return p.param }):
			seg.score = 2
		case !slicesAny(parts, func(p part) bool { return !p.param }):
			seg.score = 0
		}
		out = append(out, seg)
	}
	return out, nil
}

// compileSegment splits one segment into literal and parameter parts.
func compileSegment(raw, path string) ([]part, error) {
	var parts []part
	for raw != "" {
		open := strings.IndexByte(raw, '{')
		if open < 0 {
			parts = append(parts, part{text: raw})
			break
		}
		close := strings.IndexByte(raw, '}')
		if close < open {
			return nil, fmt.Errorf("path %q: unbalanced braces", path)
		}
		if open > 0 {
			parts = append(parts, part{text: raw[:open]})
		}
		parts = append(parts, part{text: raw[open+1 : close], param: true})
		raw = raw[close+1:]
	}
	if len(parts) == 0 {
		parts = append(parts, part{})
	}
	return parts, nil
}

// matches reports whether a request path — already split into segments — is this
// template's.
func matches(template []segment, request []string) bool {
	if len(template) != len(request) {
		return false
	}
	for i, seg := range template {
		if !matchSegment(seg.parts, request[i]) {
			return false
		}
	}
	return true
}

// matchSegment matches one segment's parts against one piece of a request path. A
// parameter matches the shortest non-empty run up to whatever literal follows it,
// which is what makes {year}-{month}.csv resolve the way a reader expects.
func matchSegment(parts []part, value string) bool {
	at := 0
	for i, p := range parts {
		if !p.param {
			if !strings.HasPrefix(value[at:], p.text) {
				return false
			}
			at += len(p.text)
			continue
		}
		if at >= len(value) {
			return false // a parameter has to match something
		}
		if i+1 == len(parts) {
			at = len(value)
			continue
		}
		next := strings.Index(value[at+1:], parts[i+1].text)
		if next < 0 {
			return false
		}
		at += 1 + next
	}
	return at == len(value)
}

// sortOperations puts the operations in match order: the most specific template first,
// so /pets/mine is reached before /pets/{petId}. Templates that cannot be told apart
// that way fall back to path and method, which keeps the order — and therefore what
// the mock serves — the same on every run.
func sortOperations(ops []Operation) {
	sort.SliceStable(ops, func(i, j int) bool {
		a, b := ops[i], ops[j]
		for k := 0; k < len(a.segments) && k < len(b.segments); k++ {
			if a.segments[k].score != b.segments[k].score {
				return a.segments[k].score > b.segments[k].score
			}
		}
		if len(a.segments) != len(b.segments) {
			return len(a.segments) > len(b.segments)
		}
		if a.Path != b.Path {
			return a.Path < b.Path
		}
		return a.Method < b.Method
	})
}

// splitPath splits a request path into the segments [matches] compares, treating a
// trailing slash as absent: /pets and /pets/ are one path to everybody but a router.
func splitPath(path string) []string {
	trimmed := strings.TrimPrefix(path, "/")
	if trimmed != "/" {
		trimmed = strings.TrimSuffix(trimmed, "/")
	}
	return strings.Split(trimmed, "/")
}

// slicesAny reports whether any element satisfies pred.
func slicesAny[T any](items []T, pred func(T) bool) bool {
	for _, item := range items {
		if pred(item) {
			return true
		}
	}
	return false
}
