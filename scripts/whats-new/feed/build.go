package feed

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Paths are where one run reads and writes. A root is taken rather than assumed,
// so the guards can run this against a deliberately broken tree without breaking
// the real one.
type Paths struct {
	Changelog string
	Overrides string
	ADRDir    string
	Out       string
}

// PathsUnder is the ordinary layout, relative to a repository root.
func PathsUnder(root string) Paths {
	return Paths{
		Changelog: filepath.Join(root, "CHANGELOG.md"),
		Overrides: filepath.Join(root, "scripts", "whats-new", "overrides"),
		ADRDir:    filepath.Join(root, "docs", "adr"),
		Out:       filepath.Join(root, "api", "web", "whats-new.json"),
	}
}

// Build reads the sources and returns the feed.
func Build(p Paths) (Doc, error) {
	raw, err := os.ReadFile(p.Changelog)
	if err != nil {
		return Doc{}, err
	}
	text := string(raw)
	if err := refuseConflicted(p.Changelog, text); err != nil {
		return Doc{}, err
	}
	overrides, err := readOverrides(p.Overrides)
	if err != nil {
		return Doc{}, err
	}

	parsedBullets := parseChangelog(text, p.ADRDir)

	// An override is keyed by the id derived from a bullet's headline, and a key
	// matching no bullet does nothing at all — the entry still renders, from the
	// CHANGELOG's own wording, with German falling back to English. That is
	// indistinguishable from "no override was written", so a mistyped key ships an
	// untranslated entry and nothing anywhere says so. One did: a key one character
	// short of its 60-character slug put the raw English sentence in both languages.
	// Keys are curated by hand, so an unmatched one is always a mistake.
	known := map[string]bool{}
	for _, e := range parsedBullets {
		known[e.id] = true
	}
	var orphans []string
	for k := range overrides {
		if !strings.HasPrefix(k, "_") && !known[k] {
			orphans = append(orphans, k)
		}
	}
	if len(orphans) > 0 {
		sort.Strings(orphans)
		return Doc{}, fmt.Errorf("%d override key(s) match no CHANGELOG bullet:\n  %s\n"+
			"Each does nothing. Fix the key to the bullet's id, or drop it.",
			len(orphans), strings.Join(orphans, "\n  "))
	}

	entries := make([]Entry, 0, MaxEntries)
	for _, e := range parsedBullets {
		ov, has := overrides[e.id]
		if has && ov.Hidden {
			continue
		}
		var p *override
		if has {
			p = &ov
		}
		entries = append(entries, merge(e, p))
		if len(entries) >= MaxEntries {
			break
		}
	}

	var generatedAt *string
	if m := newestDated.FindStringSubmatch(text); m != nil {
		d := m[1]
		generatedAt = &d
	}
	return Doc{GeneratedAt: generatedAt, Entries: entries}, nil
}

// Encode renders the feed the way the JavaScript original did, byte for byte.
//
// Two settings carry that, and both are easy to lose:
//
//   - **No HTML escaping.** Go's encoder turns `<`, `>` and `&` into `<` and
//     friends by default, on the reasoning that JSON is often embedded in HTML.
//     JavaScript's does not, and this output is served as JSON and read by a
//     fetch — so escaping it would change thousands of bytes and fail the
//     staleness check on prose alone.
//   - **Two-space indentation and a trailing newline**, which is what
//     `JSON.stringify(doc, null, 2) + "\n"` produces.
//
// Key order is carried by the struct declarations; see the package comment.
func Encode(doc Doc) ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(doc); err != nil {
		return nil, err
	}
	// Encode already appends the newline JSON.stringify did not, which is why the
	// original added one by hand. Adding a second here would be the one difference
	// this function exists to avoid.
	return buf.Bytes(), nil
}

// Write builds the feed and writes it, returning how many entries it holds.
func Write(p Paths) (int, error) {
	doc, err := Build(p)
	if err != nil {
		return 0, err
	}
	out, err := Encode(doc)
	if err != nil {
		return 0, err
	}
	if err := os.WriteFile(p.Out, out, 0o644); err != nil {
		return 0, err
	}
	return len(doc.Entries), nil
}
