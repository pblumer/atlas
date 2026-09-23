package catalog

import (
	"sort"
	"strings"
)

// TranslationGaps is every place a catalogue is written in one of its declared
// languages and not another
// (ADR-draft-a-missing-translation-is-reported-not-refused).
//
// It is the half of a rule that used to be a refusal. Publishing demanded a name
// for every declared language, and a description and a heading wording in all of
// them once there was one in any, on the argument that a portal showing one
// audience a product and the other an empty row is worse than no catalogue at
// all. The argument was sound and the premise was not: the portal never shows an
// empty row, it falls back to whatever language the catalogue does have, because
// a name in the wrong language is better than no name. So the refusal was not
// protecting a reader — it was holding a usable catalogue back until the last
// translation arrived, which meant the first language's readers waited on the
// second language's translator.
//
// A publish answers two questions and they are not the same question. **Can this
// be ordered against**, which is what a refusal is for and what [Publish] still
// decides; and **is this as good as it should be**, which is a thing to be told
// rather than blocked by. This is the second one, and it exists because removing
// a gate and putting nothing in its place is how a half-translated catalogue
// becomes invisible again — the failure the gate was built against.
//
// # What counts as a gap
//
// A field is a gap where the product says it in at least one language and not in
// this one — the name, the description, the two headings, and the name of every
// shape it is ordered in. A product nobody described has no description gap, a
// product carrying no heading has no heading gap, and a heading carrying its key
// and no wordings has none either — the key renders in every language, which is what carrying no
// wording means, and which is every product written before the wordings existed.
// Reporting those would name the whole installed base on the first screen
// somebody opens.
//
// A product named in no language at all is not reported here. It is refused by
// [Publish], because there is nothing to fall back to.
//
// # Why per catalogue and per language
//
// The languages are the *catalogue's*, and one product is offered by several
// catalogues declaring different ones (ADR-0315). The same missing French is a
// gap in the catalogue that declares French and no statement at all about the one
// that does not, so a gap names both the catalogue and the item, and a maintainer
// reading the list can open the thing it is about.
//
// Pure, like [Publish] and for the same reason: the same input answers the same
// gaps, so a report can be reproduced from what it was computed over.
func TranslationGaps(in Input) []Problem {
	var out []Problem
	byID := make(map[string]Item, len(in.Items))
	for _, it := range in.Items {
		byID[it.ID] = it
	}

	cats := append([]Catalog(nil), in.Catalogs...)
	sort.Slice(cats, func(a, b int) bool { return cats[a].ID < cats[b].ID })

	for _, c := range cats {
		if in.CatalogID != "" && c.ID != in.CatalogID {
			continue
		}
		ids := append([]string(nil), c.Items...)
		sort.Strings(ids)
		for _, id := range ids {
			it, known := byID[id]
			if !known {
				// An unknown item is Publish's refusal to report, not a gap.
				continue
			}
			for _, lang := range c.Languages {
				out = append(out, gapsOf(c.ID, it, lang)...)
			}
			// The shapes a product is ordered in, once for the product rather than
			// once per language, because one of the two things this reports is not
			// a per-language statement at all — see unnamedShapes.
			out = append(out, unnamedShapes(c.ID, it)...)
		}
	}
	return out
}

// gapsOf is one item against one declared language.
//
// The four fields in the order a reader meets them: the name, the sentence under
// it, and the two headings it is filed under. A heading's wording is only a gap
// where the product carries the heading at all — a wording for a key nothing
// carries is a contradiction, and Publish refuses that rather than noting it.
func gapsOf(catalog string, it Item, lang string) []Problem {
	var out []Problem
	say := func(what string) {
		out = append(out, Problem{Catalog: catalog, Item: it.ID,
			Message: "no " + what + " in " + lang})
	}
	missing := func(in map[string]string) bool {
		return described(in) && strings.TrimSpace(in[lang]) == ""
	}
	if missing(it.Texts) {
		say("name")
	}
	if missing(it.Descriptions) {
		say("description")
	}
	if strings.TrimSpace(it.Category) != "" && missing(it.CategoryTexts) {
		say("category")
	}
	if strings.TrimSpace(it.ProductGroup) != "" && missing(it.ProductGroupTexts) {
		say("product group")
	}
	// The shapes it is ordered in. Not a cosmetic gap like the others: the portal
	// draws these in the basket, where an orderer has to CHOOSE one, so a German
	// word in an English basket is the moment somebody picks.
	//
	// Named per shape, because "no name for the shape small in fr" is a thing to
	// go and fix and "this product has a shape gap" is not.
	for _, v := range it.Variants {
		if missing(v.Texts) {
			say("name for the shape " + v.ID)
		}
	}
	return out
}

// unnamedShapes is the shape carrying no name in any language.
//
// Its own finding rather than a gap per declared language, because it is a
// different piece of work: the shape needs naming, not translating, and saying so
// four times for a four-language catalogue would bury the shapes that are merely
// half-translated. That is also why it is computed once for the product instead
// of inside [gapsOf], which is called per language.
//
// Reported and not refused, unlike a product named nowhere. A product's id is
// minted for a catalogue and means nothing to a reader; a shape's is very often
// the word itself — "black", "silver", "large" — so the portal falling back to it
// is frequently adequate, and refusing the publish would stop catalogues that
// read perfectly well.
func unnamedShapes(catalog string, it Item) []Problem {
	var out []Problem
	for _, v := range it.Variants {
		if described(v.Texts) {
			continue
		}
		out = append(out, Problem{Catalog: catalog, Item: it.ID,
			Message: "the shape " + v.ID + " is named in no language; the portal " +
				"shows its id, which reads as a name only by accident"})
	}
	return out
}
