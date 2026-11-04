package catalog

import (
	"slices"
	"strings"

	"github.com/pblumer/atlas/api/capability"
	"github.com/pblumer/atlas/api/runloop"
)

// Headings that are references (ADR-0524).
//
// ADR-0360 made the category a string a product writes on itself and named the day
// it would have to become a reference: when something must talk *about* a category
// rather than show one. The capability register is that something. In a structured
// catalogue the category is the key of a capability — the top level, "Arbeitsalltag
// bewältigen" — and the product group the key of a capability that one requires —
// "Identität und Zugang". The shop still groups by the key, as it always did; what
// changes is that the key must name a record, the chain must be one the register
// holds, and the words a reader sees default to the record's name.
//
// The register is not copied into the catalogue. A release freezes the name it read,
// because the shop reads releases and nothing else, and a capability renamed later
// reaches the shop with the next publish — every product under it at once, which is
// the rename ADR-0360 could not offer.

// CapabilityHeading is what a publish reads of one capability: its name, which
// becomes the heading's wording where the product has none, the capabilities it
// requires, which are the product groups it may head, and whether it is deprecated.
type CapabilityHeading struct {
	Name       string
	Requires   []string
	Deprecated bool
}

// CapabilityLookup reads the capability register. Implemented by the server, which
// owns the register; the catalogue only asks.
type CapabilityLookup interface {
	CapabilityHeading(key string) (CapabilityHeading, bool, error)
}

// HeadingProblems holds every Marktleistung of a structured release to the register:
// its category names a capability, its product group names one, the category
// requires the product group, and neither is deprecated. A release that is not
// structured is not asked anything — its headings are words, as ADR-0360 decided.
//
// A register that cannot be read refuses the publish rather than passing it: a
// release whose headings were never checked would be indistinguishable from one
// whose headings were.
func HeadingProblems(rel Release, caps CapabilityLookup) []Problem {
	if !rel.Structured {
		return nil
	}
	var problems []Problem
	if caps == nil {
		return append(problems, Problem{Message: "this server cannot read the capability " +
			"register, so the headings of a structured catalogue cannot be checked"})
	}
	read := cachedLookup(caps)
	for _, it := range rel.Items {
		if it.Kind != ItemOffering {
			continue
		}
		category, catOK := headingRecord(it, "category", it.Category, read, &problems)
		_, groupOK := headingRecord(it, "product group", it.ProductGroup, read, &problems)
		if catOK && groupOK && !slices.Contains(category.Requires, strings.TrimSpace(it.ProductGroup)) {
			problems = append(problems, Problem{Item: it.ID, Message: "names product group " +
				it.ProductGroup + ", which capability " + it.Category + " does not require; " +
				"a product group is a capability its category requires, so add it to the " +
				"category's requires or choose one that is there"})
		}
	}
	return problems
}

// headingRecord resolves one heading and reports what is wrong with it. A blank key
// is not reported here: checkOffering already said the Marktleistung names none.
func headingRecord(it Item, label, key string, read func(string) (CapabilityHeading, bool, error),
	problems *[]Problem) (CapabilityHeading, bool) {
	key = strings.TrimSpace(key)
	if key == "" {
		return CapabilityHeading{}, false
	}
	h, found, err := read(key)
	switch {
	case err != nil:
		*problems = append(*problems, Problem{Item: it.ID, Message: "names " + label + " " +
			key + ", and the capability register could not be read: " + err.Error()})
	case !found:
		*problems = append(*problems, Problem{Item: it.ID, Message: "names " + label + " " +
			key + ", which is no capability in the register; in a structured catalogue a " +
			label + " is the key of a capability"})
	case h.Deprecated:
		*problems = append(*problems, Problem{Item: it.ID, Message: "names " + label + " " +
			key + ", and that capability is deprecated; the shop would file new orders " +
			"under something the organisation is retiring"})
	default:
		return h, true
	}
	return CapabilityHeading{}, false
}

// WordHeadings gives each Marktleistung's headings a wording in every declared
// language the product left unworded, taken from the capability's name. The
// product's own wording wins: the register keeps one name, and a catalogue offered
// in French says its heading in French. Called on a release's frozen items after
// [HeadingProblems] passed, so every key resolves.
func WordHeadings(items []Item, languages []string, caps CapabilityLookup) {
	if caps == nil {
		return
	}
	read := cachedLookup(caps)
	word := func(key string, texts map[string]string) map[string]string {
		h, found, err := read(strings.TrimSpace(key))
		if err != nil || !found || strings.TrimSpace(h.Name) == "" {
			return texts
		}
		for _, lang := range languages {
			if strings.TrimSpace(texts[lang]) != "" {
				continue
			}
			if texts == nil {
				texts = map[string]string{}
			}
			texts[lang] = h.Name
		}
		return texts
	}
	for i := range items {
		if items[i].Kind != ItemOffering {
			continue
		}
		items[i].CategoryTexts = word(items[i].Category, items[i].CategoryTexts)
		items[i].ProductGroupTexts = word(items[i].ProductGroup, items[i].ProductGroupTexts)
	}
}

// cachedLookup asks the register once per key: a catalogue of two hundred
// Marktleistungen names the same handful of capabilities over and over.
func cachedLookup(caps CapabilityLookup) func(string) (CapabilityHeading, bool, error) {
	type answer struct {
		h     CapabilityHeading
		found bool
		err   error
	}
	seen := map[string]answer{}
	return func(key string) (CapabilityHeading, bool, error) {
		if a, ok := seen[key]; ok {
			return a.h, a.found, a.err
		}
		h, found, err := caps.CapabilityHeading(key)
		seen[key] = answer{h, found, err}
		return h, found, err
	}
}

// Register reads the capability register from its own store, on the run loop that
// owns it. The server hands over the store and the loop; the catalogue keeps the
// reading here, beside the rules that use it, so the server package gains no file
// that reaches into the shop (ADR-0515).
type Register struct {
	Loop  *runloop.Loop
	Store *capability.Store
}

// CapabilityHeading reads one capability. A key that names no record, or cannot
// name one, is not found rather than an error: the store says so for a key that is
// not a valid filename, and HeadingProblems reports both as "no capability".
func (r Register) CapabilityHeading(key string) (CapabilityHeading, bool, error) {
	key = strings.TrimSpace(key)
	if key == "" || r.Store == nil || r.Loop == nil {
		return CapabilityHeading{}, false, nil
	}
	var (
		out   CapabilityHeading
		found bool
		err   error
	)
	r.Loop.Do(func() {
		c, ok, e := r.Store.Get(key)
		if e != nil || !ok {
			found, err = ok, e
			return
		}
		found = true
		out = CapabilityHeading{
			Name:       c.Name,
			Requires:   append([]string(nil), c.Requires...),
			Deprecated: c.State == capability.StateDeprecated,
		}
	})
	return out, found, err
}
