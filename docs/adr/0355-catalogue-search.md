# ADR-0355: A catalogue is searched over words it carries, in the browser, and a hit says where it lives

- **Status:** Accepted
- **Implementation:** Landed
- **Date:** 2026-09-15
- **Deciders:** Atlas maintainers
- **Open question:** Whether the search should ever reach past the catalogue the
  person was resolved to. A search that found a product in a catalogue somebody is
  not the audience for would answer the question they asked and disclose a shop
  they may not enter, and "you may not have this" is a worse answer than nothing
  for a service that was never theirs to see. It stays inside the one catalogue
  until somebody shows a case where the silence costs more than the disclosure.
- **Question checked:** 2026-09

## Context and problem statement

The portal browses. Four columns cascade from the catalogue to the individual
service, and a person who knows roughly where a thing sits finds it in three
clicks.

The story this record answers is the other person:

> As a user I want to **find a service when I do not know the exact product name**,
> so that I do not have to guess my way down a tree.

Two things stood between that person and the thing they wanted.

**The tree is only navigable if you already know the answer.** "Power BI Pro" sits
under "Productivity Enabling M365 Standard", which sits under "Productivity
Enabling". Somebody who wants a reporting tool has no reason to open either. The
cascade shows what a thing is *part of*, which is exactly the knowledge the
searcher does not have.

**The catalogue only holds the words it displays.** Every text on an item is its
name in some language. Nothing on an item held "Fernzugriff" for the VPN, "KI" for
Copilot, or the name of the product this one replaced — so even a search over every
text would only have answered for somebody who already knew what the thing was
called.

## Decision drivers

- The searcher does not know the name. That is the entire premise; anything that
  requires the name solves a different problem.
- Whatever is added has to be maintainable by a product manager, in one place, for
  a thing they think of once.
- Nothing may reach past the catalogue the person was resolved to. Visibility is
  fail-closed and a search is not an exception to it.
- A release is frozen. Whatever is added travels into it like everything else.

## Considered options

1. **Search the names the catalogue already carries**, and nothing more.
2. **Add a keyword list per item**, searched together with every name.
3. **A search service on the server** — an index, a query route, ranking.

## Decision outcome

Chosen option: **"a keyword list per item, searched in the browser over the release
the page already has"**.

### The keyword list is flat, not per locale

Every other text on an item is a map from language tag to string, and this
deliberately is not.

A synonym list is for **finding**, not for **displaying**. Nothing renders it. And
a searcher's language is not the catalogue's: somebody reading a German catalogue
types "laptop" as readily as "Notebook", an abbreviation like "M365" belongs to no
language at all, and a vendor's own term is spelled the same everywhere. Splitting
the list by locale would hide a term from the one person who needed it, and would
ask a product manager to maintain in two places what they think of once.

The same argument decides how names are searched: **every locale's text, not the
one on screen**. A catalogue carrying an English name for a product is carrying it
whether or not the page is being read in English, and refusing to match a word the
catalogue itself holds would be the search failing at its only job.

The one static check the list admits is that a keyword may not be blank. An empty
string is contained in every query, so one product carrying one would surface for
everything anybody typed — a catalogue where one product answers every search is a
catalogue with no search. It is refused at publish, with everything else I5 proves
there.

### The search runs in the browser

Not because a server search would be hard, but because there is nothing for it to
do. The portal already holds the entire release: it fetched it to draw the cascade.
A route would re-send data the page has, and an index would be a second copy of the
catalogue to keep true.

It also settles the visibility question by construction rather than by a check
somebody has to remember. The page can only search what it was given, and it was
given exactly one catalogue — the highest-ranked one the person's groups reach. A
server-side search would need its own audience filter, correct, forever, in a
second place. There is no second place.

The consequence to be honest about: this does not scale to a catalogue too large
to send to a browser. That limit is the portal's already, not the search's — the
cascade has the same ceiling — and when it binds, both move together.

### Every word must match, and matching is over a joined haystack

"vpn zugang" narrows. A search that grew its answer as somebody typed more would be
teaching them to type less. Matching is a case-insensitive substring per word, over
the item's id, all of its names and all of its keywords joined — so a term hits
whichever field carries it and nobody has to know which one that was.

There is no ranking. With one catalogue's worth of items the honest answer is the
set, and a score would be a number invented to order a list that is short enough to
read.

### A query replaces the cascade; it does not filter it

Filtering the four columns was the obvious shape and is the wrong one. A match
three levels deep would leave nothing on screen to say it was there — the person
would see an empty column and conclude the catalogue does not carry it.

So while a query is set the four columns are replaced by a flat list, and **each
hit carries the path it sits on**: the answer says both *what* and *where*. Choosing
a hit opens the cascade at that item rather than ordering from the list, because a
list that does not show what a thing comes with is the wrong place to add something
to a basket.

The favourites filter does not apply while searching. Somebody who searched wants
the thing they searched for, and hiding it because it was never marked would be the
filter overruling the question.

### Typing repaints the results, not the page

The page renders from state, and a full render replaces the input being typed into,
which sends the caret to the end of the word after every character. The search field
therefore repaints only the results region. This is the same mechanism the order
table's filters already use, and it is stated in a test rather than a comment
because it is invisible to anything that sets a value programmatically and immediate
to anybody who types.

### Consequences

- **Positive:** the story is answered for the person it was written for — the one
  who does not know the name. Nothing was added to the server, no index can drift
  from the catalogue, and the audience rule is satisfied by construction.
- **Negative / trade-offs accepted:** the keyword list is a field a product manager
  has to fill in, and an empty one is the default. A product nobody wrote keywords
  for is findable by its names only, which is the state every catalogue starts in.
  No ranking, no fuzzy matching, no tolerance for a typo.
- **Follow-ups / risks to watch:** the browser-side search shares the portal's
  ceiling on catalogue size. If a catalogue outgrows being sent whole, the cascade
  and the search need a server-side answer together.

### A copy that was not a copy

Writing the test that the keywords travel into the release found that they did not
travel as a copy — and neither did `Eligible`, which had shipped that way. `freeze`
deep-copied the fields whoever wrote it remembered on the day, which is a list that
cannot include the next field anybody adds.

So the guard was made structural: `TestAReleaseSharesNothingWithTheCatalogue` walks
an `Item` by reflection and fails on any slice or map the release shares with the
catalogue, and a companion test fails when a new slice or map is left out of the
fixture. A field added tomorrow is covered without anybody editing a test.

## Pros and cons of the options

### Option 1 — search the names only
- Good: nothing to add, nothing to maintain, no new field.
- Bad: answers only for somebody who knows the name, which is the person who did
  not need to search. It restates the problem as the solution.

### Option 2 — a keyword list, searched in the browser
- Good: answers the story; one field, one place, maintained by the person who knows
  the product; frozen into the release like everything else; visibility holds by
  construction.
- Bad: a field that is empty until somebody fills it; no ranking; bounded by what
  the portal can send to a browser.

### Option 3 — a search service on the server
- Good: scales past the browser; ranking and fuzzy matching become possible.
- Bad: a second copy of the catalogue that can disagree with it; a second audience
  filter that must be right forever; a route and an index built for a problem no
  catalogue has yet.

## Links

- relates to ADR-0312 — the three models, and why the catalogue is one of them
- relates to ADR-0313 — every string in every locale, which is why the search
  reads them all
- relates to ADR-0348 — the portal layout the search replaces while a query is set
