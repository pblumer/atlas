// Package feed builds the Console's "What's New" feed from CHANGELOG.md and the
// curated overrides beside it.
//
// It is the Go port of scripts/whats-new/gen.mjs, and the port is the point: the
// feed was the only reason `go build`, `go vet`, the race job and the docs job
// needed Node at all (ADR-0375). Node stays where it is the
// technology — the browser end-to-end suite and the screenshot capture — and
// leaves the four places where it was incidental.
//
// # The contract this package has to keep
//
// The output is a committed file and CI regenerates it to check the commit is
// current, so a port that produced *equivalent* JSON would fail that check on
// every run. It has to produce the **same bytes**: the same key order, the same
// two-space indentation, the same trailing newline, and no HTML escaping — Go's
// encoder escapes `<`, `>` and `&` by default and JavaScript's does not.
//
// Key order is why the types below declare their fields in the order they do.
// JSON.stringify writes an object's keys in insertion order; Go writes a struct's
// in declaration order. The two agree only because these declarations follow the
// JavaScript literals they replace, and a field moved for tidiness would change
// every byte after it.
package feed

import (
	"fmt"
	"regexp"
	"strings"
)

// MaxEntries bounds the feed. The Console shows a compact list; past this nobody
// scrolls, and the rest is the CHANGELOG's job.
const MaxEntries = 12

const (
	repoURL = "https://github.com/pblumer/atlas"
	blobURL = repoURL + "/blob/main"
)

// Bilingual is a value in both languages, with German falling back to English.
type Bilingual struct {
	EN string `json:"en"`
	DE string `json:"de"`
}

// Steps is a tutorial in both languages.
type Steps struct {
	EN []string `json:"en"`
	DE []string `json:"de"`
}

// Link is where an entry points: the pull request or the record it names.
type Link struct {
	Label string `json:"label"`
	URL   string `json:"url"`
}

// Try is the deep link that opens the thing the entry describes.
type Try struct {
	Label Bilingual `json:"label"`
	Route string    `json:"route"`
}

// Entry is one line of the feed. The field order is the wire contract; see the
// package comment.
type Entry struct {
	ID       string    `json:"id"`
	Version  string    `json:"version"`
	Date     *string   `json:"date"`
	Category string    `json:"category"`
	Title    Bilingual `json:"title"`
	Summary  Bilingual `json:"summary"`
	Link     Link      `json:"link"`
	Tags     []string  `json:"tags,omitempty"`
	Tutorial *Steps    `json:"tutorial,omitempty"`
	Try      *Try      `json:"try,omitempty"`
}

// Doc is the whole feed.
//
// GeneratedAt is derived from the CHANGELOG and never from the clock: the output
// is committed and CI re-runs this to check the commit is current, so a wall-clock
// stamp would make every run differ from the commit and fail on the date alone. It
// is the newest dated release section — what the feed actually reflects.
type Doc struct {
	GeneratedAt *string `json:"generatedAt"`
	Entries     []Entry `json:"entries"`
}

// conflictMarker is the one input this generator must not accept.
//
// The feed is derived from CHANGELOG.md, and .gitattributes marks the *output*
// unmergeable so git raises a conflict there instead of interleaving two generated
// files. The documented resolution is to take the merged CHANGELOG and re-run
// this. But the two files change together, so the CHANGELOG is usually conflicted
// at that same moment — and a parser looking for `- **bullets**` reads straight
// past `<<<<<<< HEAD`, which is not one. Both sides' bullets then become two
// entries in a feed that looks perfectly well-formed, and CI's staleness check
// passes, because the committed file really is what this produces from that
// source. The wrongness surfaces only to a reader.
var conflictMarker = regexp.MustCompile(`(?m)^(<{7}|={7}|>{7})( |$)`)

// ErrConflicted says a source file still carries merge markers.
type ErrConflicted struct{ Path string }

func (e *ErrConflicted) Error() string {
	return fmt.Sprintf("%s still has unresolved merge conflicts.\n"+
		"  Resolve it first, then re-run this. Generating from a conflicted\n"+
		"  source would put both sides in the feed as separate entries, and\n"+
		"  nothing downstream would notice.", e.Path)
}

func refuseConflicted(path, text string) error {
	if conflictMarker.MatchString(text) {
		return &ErrConflicted{Path: path}
	}
	return nil
}

// slugify turns a headline into a stable, filename-safe id.
//
// It is the JavaScript original's rule, unchanged and deliberately so: the id is
// the key of a curated override file, and 259 of those are named by it. A port
// that folded a diacritic differently, or cut at a different length, would orphan
// every file whose name it changed — and an orphaned override is an entry that
// renders from the CHANGELOG's own wording, which looks exactly like one nobody
// has curated yet.
func slugify(s string) string {
	s = strings.ToLower(s)
	var b strings.Builder
	dash := false
	for _, r := range s {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			if dash {
				b.WriteByte('-')
				dash = false
			}
			b.WriteRune(r)
			continue
		}
		// Any run of non-alphanumerics becomes one dash, and a leading or trailing
		// run becomes none — which is what replace(/[^a-z0-9]+/g,"-") followed by the
		// trim does, without materialising the intermediate string.
		if b.Len() > 0 {
			dash = true
		}
	}
	out := b.String()
	// slice(0, 60) counts UTF-16 units in JavaScript and bytes here. Every character
	// that survives the filter above is ASCII, so the two agree.
	if len(out) > 60 {
		out = out[:60]
	}
	return out
}

var (
	mdLink     = regexp.MustCompile(`\[([^\]]+)\]\([^)]+\)`)
	mdNoise    = regexp.MustCompile("[`*_]")
	whitespace = regexp.MustCompile(`\s+`)
	sentence   = regexp.MustCompile(`^(.*?[.!?])(\s|$)`)
)

// firstSentence pulls a plain-language lead out of a bullet's prose: markdown
// stripped, then cut at the first sentence end.
//
// A fallback only — a curated summary is preferred, and the point of the
// overrides is that most entries have one.
func firstSentence(prose string) string {
	plain := mdLink.ReplaceAllString(prose, "$1")
	plain = mdNoise.ReplaceAllString(plain, "")
	plain = strings.TrimSpace(whitespace.ReplaceAllString(plain, " "))
	if m := sentence.FindStringSubmatch(plain); m != nil {
		return strings.TrimSpace(m[1])
	}
	return strings.TrimSpace(plain)
}
