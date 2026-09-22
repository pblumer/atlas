# ADR-DRAFT: A catalogue heading is a key that groups and a wording per language that shows

- **Status:** Accepted
- **Implementation:** Landed
- **Date:** 2026-09-22
- **Deciders:** Atlas maintainers

## Context and problem statement

ADR-0360 gave a product two headings — `category` and, one level below it,
`productGroup` — and made each a plain string on the record rather than an entity.
It wrote down three costs of that, deliberately and in the field's own
documentation. Two of them still hold and are still right. The third does not:

> It has **no translation**: it reads the same in every language the catalogue
> offers, unlike every product name beside it.

A catalogue declares its languages and a release refuses to publish a product that
carries a name in one declared language and not another. So a catalogue kept in
German, French, English and Italian translates every product name, every variant
name and — since ADR-0362 — every description, and then files all of them under
two German words. The portal's first two columns are the only text on the page
that does not reach the reader in their own language, and nothing anywhere says
so: the German word renders, and it looks deliberate.

Naming the cost was the right call in ADR-0360 and it is what makes this a small
change rather than a rewrite. The question is what the heading becomes.

## Decision drivers

- **A heading must not split in a language nobody publishing it reads.** Whatever
  groups the products has to be one value, not a set of translations that can agree
  in one language and differ in another.
- **Already published releases must not change meaning.** A release is frozen and
  ordered against; every one in the tree today holds `category` as a string.
- **The installed base must keep working untouched.** Every product written before
  this record carries a heading and no translation, and so does every product in a
  single-language catalogue — which is most of them.
- **A maintainer has to be able to write four wordings without four forms.** The
  Console already renders one box per language for the name and one for the
  description; a third and a fourth stack of boxes for two single words is a form
  people stop reading.
- **What the portal shows and what the portal groups by must be separable**, or the
  sort order of the first column is a sort of somebody else's words.

## Considered options

1. **The heading becomes a map per language tag**, like `texts` and
   `descriptions`, and the grouping key is derived from it.
2. **The heading becomes an entity** with an id, its own texts and its own
   lifecycle — the answer ADR-0360 named as the migration path if a cost ever bit.
3. **The string stays as the key and a map is added beside it**: `category` keeps
   meaning "what this product is filed under", `categoryTexts` says how that is
   written for a reader.

## Decision outcome

Chosen option: **"the string stays as the key and a map is added beside it"**,
because it is the only one of the three that is additive at rest.

`Item.Category` and `Item.ProductGroup` keep their type, their JSON name and their
meaning. `Item.CategoryTexts` and `Item.ProductGroupTexts` are new maps, both
`omitempty`, and both empty on every record that exists today. The portal groups by
the key exactly as it always did, and renders the wording where there is one and
the key where there is not. No record was migrated, no release re-frozen, and a
server running the old code and one running the new agree about every catalogue
written before this.

The rules a release proves are the ones `Descriptions` already earned:

- **Optional as a whole.** A heading with no wordings publishes and renders its key
  in every language. That is the installed base and every single-language
  catalogue, and refusing it would refuse them.
- **All-or-nothing once there is one.** A heading worded in German and not French
  is refused for a catalogue declaring both. The fallback would render, so the
  French reader would silently get the German word under a column head that looks
  finished.
- **A wording without a key is refused.** The product would sit in the bucket for
  products carrying no heading — under "Ohne Kategorie" — while holding the word
  for one in four languages. Nothing breaks at runtime, and the intent is lost.
- **One key is worded one way.** Two products under `Arbeitsplatz` where one says
  `Poste de travail` and the other `Bureau` would make one column head say one of
  the two, with nothing on the screen saying a choice was made. It is provable when
  the catalogue is published, so it is refused there (I5).

Two spellings are still two categories. That is the key's identity and it is the
cost ADR-0360 recorded; this record does not touch it.

### The entry convention, and why it is the form's and not the model's

The Console asks for both headings in **one box each**, the wordings in the order
the catalogue declares its languages, separated by semicolons:

```
Arbeitsplatz; Poste de travail; Workplace; Postazione
```

The first is the key. A box holding one wording means "this heading is not
translated" and stores no map at all — which is what makes the box round-trip: what
it renders, saved unchanged, stores what it read. A stray trailing semicolon is
therefore harmless rather than a half-translated heading that publishing refuses.

**The semicolons are a convention of that one form and nothing else.** What is
stored is a map per language tag. Had the list itself been stored, the meaning of
every product would hang on the order of a list kept on the catalogue, and adding a
fifth language would shift the wordings of every product at once. Stored as a map,
reordering the catalogue's languages changes what a *newly typed* list means and
nothing that is already saved — which is a hazard a maintainer can see, in the
sentence above the box that names the order.

### Consequences

- **Positive:** a catalogue kept in four languages reads in four languages, in the
  two columns that were the last German-only text on the page. The wordings travel
  into the release like the name does, so an order placed today keeps its headings
  when the catalogue is re-worded tomorrow.
- **Positive:** the first column sorts by what the reader sees. Sorting the keys
  would have handed a French reader a column ordered by German words, in an order
  nothing on the page explains — a defect that only appears once there is anything
  to translate, which is why it is named here rather than found later.
- **Negative / trade-offs accepted:** two fields for one concept, and a reader of
  the record has to know which one groups. It is written on both fields. The
  alternative — one field that is both — is option 1, and it buys that tidiness
  with a category that splits per language.
- **Negative / trade-offs accepted:** the key is now a value no reader need ever
  see, so it can drift from every wording a product carries. That is deliberate: a
  stable slug survives the maintainer deciding the German was wrong. It also means
  a key nobody reads can be misspelled without anybody noticing, which two spellings
  already could.
- **Negative / trade-offs accepted:** the entry form is positional, which is the one
  thing about this a maintainer has to be told rather than shown.
- **Negative / trade-offs accepted:** a heading containing a semicolon cannot be
  written in the Console any more. `Hardware; Zubehör` as one heading is now two
  wordings. It is still storable over REST and MCP, which write the fields
  directly, so this is a limit of the entry convention and not of the record. A
  heading is one or two words and a semicolon inside one is rare enough that
  spending the separator on it would cost the feature; if it turns out not to be,
  the escape belongs in the form and not in the model.
- **Follow-ups / risks to watch:** the headings still have no ordering of their
  own, and this record makes the absence slightly more visible — a catalogue whose
  first column reads in four languages invites somebody to ask why it cannot decide
  which heading comes first. If that is wanted, it arrives as the entity ADR-0360
  named, and this record's key is what it would be migrated from.

## Pros and cons of the options

### Option 1 — the heading becomes a map
- Good: one field for one concept, and the same shape as every other translated
  text on the record.
- Bad: **there is no key.** Grouping by the whole map makes two products one
  category when their German agrees and two when their French differs, so a
  category splits in a language the person publishing it does not read.
- Bad: grouping by one chosen language's entry re-invents the key and hands the
  choice of it to the catalogue's language list — which is the wrong object to hang
  it on. A product is referenced by catalogues rather than owned by one
  (ADR-0315), so the same product read through a catalogue that declares German
  first and one that declares French first would be filed under two different keys,
  with nothing anywhere saying which. Reordering a catalogue's languages would
  regroup every product in it at once, silently.
- Bad: every stored record and every frozen release holds a string, so the type
  needs an unmarshaller that reads both — and it would have to decide, with no
  catalogue in hand, which language an old string was written in.

### Option 2 — the heading becomes an entity
- Good: it answers everything at once — translation, ordering, two spellings folded
  into one, a heading that exists before a product is filed under it.
- Bad: it is the decision ADR-0360 refused, and the reasons have not changed. An
  entity carries its own texts, ordering, visibility and lifecycle, each of which
  is a thing to publish, migrate and get wrong, for what is the word above a column.
- Bad: it is a migration of every catalogue in every installation, to fix the one
  cost of three that actually bit.

### Option 3 — a key and a wording beside it (chosen)
- Good: additive at rest. Nothing stored changes, nothing frozen changes meaning,
  and the old and new code agree about every existing catalogue.
- Good: grouping stays a fact about the catalogue rather than about the reader.
- Bad: two fields for one concept.

## Links

- amends ADR-0360 — the two headings as strings, and the third cost it recorded
- relates to ADR-0315 — a product is referenced by catalogues rather than owned by
  one, which is why a save writes only the languages the edited catalogue declares
- relates to ADR-0383 — a position's level is read from the graph once; both
  headings are attributes of the offering, so both columns read them off the
  products nothing contains

The all-or-nothing rule this record follows is the one `Item.Descriptions` carries,
which has no record of its own — it is documented on the field and enforced in
`Publish`.
