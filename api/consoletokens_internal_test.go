package api

import (
	"regexp"
	"sort"
	"strings"
	"testing"
)

// A custom property that nothing declares paints nothing, and says nothing.
//
// `border-top: 1px solid var(--line)` where no rule sets `--line` is not a
// fallback to some default border: the whole declaration is invalid and the
// browser drops it. The element loses its line, the page still renders, and the
// result looks like a design that never had one. The existing form-theme guard
// catches this across the form-js bridge; this catches it inside the console,
// where three declarations had been painting nothing — two of them added a fortnight
// ago with the avatar field, and unnoticed because a missing hairline reads as
// intent.
//
// Three rules make this non-vacuous, and each was needed:
//
//   - Only a reference *without* a fallback counts. `var(--surface-2, var(--surface))`
//     is a deliberate per-element override with a stated default, and seven of those
//     live in app.css. Flagging them would have made the guard noise.
//   - A token set inline anywhere in web/ counts as declared: editor.js writes
//     `style="--token-color:…"` on each chip, and app.css reads it. That is the same
//     override pattern, expressed from the other side.
//   - The stylesheet is read with comments stripped, because these files explain
//     themselves by quoting the declarations they are about.
func TestTheConsoleReadsNoTokenNobodyWrites(t *testing.T) {
	css := stripComments(readWeb(t, "app.css"))
	declared := declaredProps(css)

	// A token set inline from JavaScript is declared, just not in a stylesheet.
	inline := regexp.MustCompile(`(--[a-zA-Z0-9-]+)\s*:`)
	for _, name := range []string{"app.js", "catalog-admin.js", "editor.js"} {
		for _, m := range inline.FindAllStringSubmatch(stripComments(readWeb(t, name)), -1) {
			declared[m[1]] = true
		}
	}

	// Deliberately bare: a reference that states no fallback.
	bare := regexp.MustCompile(`var\(\s*(--[a-zA-Z0-9-]+)\s*\)`)
	missing := map[string][]string{}
	for _, name := range []string{"app.css", "app.js", "catalog-admin.js"} {
		body := stripComments(readWeb(t, name))
		for _, m := range bare.FindAllStringSubmatch(body, -1) {
			if !declared[m[1]] {
				missing[m[1]] = append(missing[m[1]], name)
			}
		}
	}
	if len(missing) == 0 {
		return
	}
	names := make([]string, 0, len(missing))
	for k := range missing {
		names = append(names, k)
	}
	sort.Strings(names)
	for _, n := range names {
		t.Errorf("%s is read in %s and declared nowhere, so every declaration using it "+
			"is invalid and paints nothing", n, strings.Join(dedupe(missing[n]), ", "))
	}
}

func dedupe(in []string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(in))
	for _, s := range in {
		if !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	return out
}
