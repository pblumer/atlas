package feed

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// override is the curated half of an entry: the layman-friendly, bilingual prose
// and the optional tutorial and deep link, none of which belong in a
// developer-facing CHANGELOG.
//
// Every field a file may carry is declared, and **unknown fields are refused**.
// The original ignored them, and one override pays for that today: it carries
// `route` at the top level instead of inside `try`, so its "Try it" link does
// nothing and nothing anywhere says so. That is the same failure the orphan check
// below exists to stop, one level down — a key that does nothing is
// indistinguishable from a key nobody wrote.
type override struct {
	// Why is a comment. It is the one key allowed to mean nothing, and it is spelled
	// with a leading underscore so that a reader can tell a note from a field.
	Why      json.RawMessage `json:"_why,omitempty"`
	Hidden   bool            `json:"hidden,omitempty"`
	Title    *bilingualIn    `json:"title,omitempty"`
	Summary  *bilingualIn    `json:"summary,omitempty"`
	Tags     []string        `json:"tags,omitempty"`
	Tutorial *stepsIn        `json:"tutorial,omitempty"`
	Link     *Link           `json:"link,omitempty"`
	Try      *tryIn          `json:"try,omitempty"`
}

// bilingualIn is an override value that may be a bare string or {en, de}.
type bilingualIn struct {
	str  *string
	pair *struct {
		EN *string `json:"en"`
		DE *string `json:"de"`
	}
}

func (b *bilingualIn) UnmarshalJSON(data []byte) error {
	if len(data) > 0 && data[0] == '"' {
		var s string
		if err := json.Unmarshal(data, &s); err != nil {
			return err
		}
		b.str = &s
		return nil
	}
	b.pair = &struct {
		EN *string `json:"en"`
		DE *string `json:"de"`
	}{}
	return json.Unmarshal(data, b.pair)
}

// resolve applies the fallback: an absent value takes it, and German takes
// English where it says nothing of its own.
func (b *bilingualIn) resolve(fallbackEN string) Bilingual {
	if b == nil {
		return Bilingual{EN: fallbackEN, DE: fallbackEN}
	}
	if b.str != nil {
		return Bilingual{EN: *b.str, DE: *b.str}
	}
	en := fallbackEN
	if b.pair != nil && b.pair.EN != nil {
		en = *b.pair.EN
	}
	de := en
	if b.pair != nil && b.pair.DE != nil {
		de = *b.pair.DE
	}
	return Bilingual{EN: en, DE: de}
}

// stepsIn is a tutorial that may be one list for both languages or one per
// language.
type stepsIn struct {
	flat []string
	pair *struct {
		EN []string `json:"en"`
		DE []string `json:"de"`
	}
}

func (s *stepsIn) UnmarshalJSON(data []byte) error {
	if len(data) > 0 && data[0] == '[' {
		return json.Unmarshal(data, &s.flat)
	}
	s.pair = &struct {
		EN []string `json:"en"`
		DE []string `json:"de"`
	}{}
	return json.Unmarshal(data, s.pair)
}

func (s *stepsIn) resolve() *Steps {
	if s == nil {
		return nil
	}
	if s.flat != nil {
		return &Steps{EN: s.flat, DE: s.flat}
	}
	en := s.pair.EN
	if en == nil {
		en = []string{}
	}
	de := s.pair.DE
	if de == nil {
		de = en
	}
	return &Steps{EN: en, DE: de}
}

type tryIn struct {
	Label *bilingualIn `json:"label,omitempty"`
	Route string       `json:"route,omitempty"`
}

// readOverrides loads scripts/whats-new/overrides/<id>.json, keyed by id.
//
// A file that is not valid JSON fails the run naming the file: it would otherwise
// drop one entry's curated prose silently, and an entry that renders from the
// CHANGELOG's own wording looks exactly like one nobody has curated yet.
//
// One file per entry rather than one file holding all of them, and the reason is
// recorded where it was learned: a single object made this the most collision-prone
// file in the repository — every branch shipping a user-facing change added a key,
// every branch added it at the top, and git merged the two insertions by
// interleaving their bodies. A directory makes that not a conflict, and the file
// name being the key means a duplicate key cannot be written at all.
func readOverrides(dir string) (map[string]override, error) {
	out := map[string]override{}
	entries, err := os.ReadDir(dir)
	if err != nil {
		// Not fatal: the feed still renders from the CHANGELOG alone, in English.
		fmt.Fprintf(os.Stderr, "whats-new: no usable overrides (%v); using CHANGELOG only\n", err)
		return out, nil
	}
	var names []string
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".json") {
			names = append(names, e.Name())
		}
	}
	// A stable read order, so a malformed file is reported the same way every run.
	sort.Strings(names)
	for _, name := range names {
		path := filepath.Join(dir, name)
		raw, err := os.ReadFile(path)
		if err != nil {
			return nil, err
		}
		// A conflicted override would fail the decode below anyway, but as a syntax
		// error about an unexpected '<' — which sends the reader looking for a typo
		// rather than for the merge they are in the middle of.
		if err := refuseConflicted(path, string(raw)); err != nil {
			return nil, err
		}
		var ov override
		dec := json.NewDecoder(strings.NewReader(string(raw)))
		dec.DisallowUnknownFields()
		if err := dec.Decode(&ov); err != nil {
			return nil, fmt.Errorf("%s: %v\n"+
				"  Every key an override may carry is declared. A key that is not one does\n"+
				"  nothing, and a key that does nothing is indistinguishable from a key\n"+
				"  nobody wrote — which is how a \"Try it\" link goes missing in silence.", name, err)
		}
		out[strings.TrimSuffix(name, ".json")] = ov
	}
	return out, nil
}

// merge folds one override onto one parsed bullet.
func merge(e parsed, ov *override) Entry {
	out := Entry{
		ID:       e.id,
		Version:  e.version,
		Date:     e.date,
		Category: e.category,
		Link:     e.link,
	}
	var (
		title, summary *bilingualIn
		tut            *stepsIn
	)
	if ov != nil {
		title, summary, tut = ov.Title, ov.Summary, ov.Tutorial
		if ov.Link != nil {
			out.Link = *ov.Link
		}
		if len(ov.Tags) > 0 {
			out.Tags = ov.Tags
		}
		if ov.Try != nil && ov.Try.Route != "" {
			out.Try = &Try{Label: ov.Try.Label.resolve("Try it"), Route: ov.Try.Route}
		}
	}
	out.Title = title.resolve(e.title)
	out.Summary = summary.resolve(e.fallbackSummary)
	if steps := tut.resolve(); steps != nil && len(steps.EN) > 0 {
		out.Tutorial = steps
	}
	return out
}
