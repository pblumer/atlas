# ADR-0348: A favourite is a bookmark, so it stores an id and resolves nothing

- **Status:** Accepted
- **Implementation:** Landed
- **Date:** 2026-09-15
- **Deciders:** Atlas maintainers
- **Open question:** Whether a mark should survive being unable to resolve for
  long. The list keeps ids a catalogue no longer carries, deliberately, and the
  portal counts them — but nothing ever clears them, so an account that moves
  between catalogues twice accumulates marks that will never resolve again. The
  ceiling bounds the damage; it does not tidy it, and nobody has asked whether it
  should.
- **Question checked:** 2026-09

## Context and problem statement

The story is one line: *als Requester will ich Bundles/Produkte als Favoriten
speichern können.* It is the smallest measure in the plan and the one with the
least to argue about, which is exactly why its two real decisions are worth
recording — they are the kind that get made by accident.

## Decision

A favourite stores a catalogue item id under a principal, and nothing else.

### It is a bookmark and never an entitlement

No release, no catalogue, no variant. A mark says "show me this again", not "I may
have this". Everything that decides whether the person may still *order* the thing
is asked at read time by the routes that already decide it — the catalogue
audience, the product's eligibility, the conflict check — and a favourite changes
none of those answers.

That is what keeps it from ageing into something somebody has to repair. A
catalogue reassignment, a withdrawn product, a release that no longer carries it:
the stored id is unaffected and the portal simply resolves fewer of them.

**The tidier-looking alternative is a trap.** Validating a mark against the
caller's catalogue at write time sounds correct and would mean that a catalogue
reassignment starts *refusing* marks the person already has, and a withdrawn
product makes an existing list unwritable. The list would break on exactly the
events it should survive.

Marks that no longer resolve are **counted and not hidden in silence**: a star that
stopped appearing with no word looks like the page lost it, and the person cannot
tell that from a catalogue that moved under them.

### Yours only, with no parameter for anybody else

Every other portal read carries a `?principal=` for an operator, because somebody
administering an estate legitimately needs to see what another person holds or
owes. **Nothing needs to see what another person bookmarked.** A parameter nobody
needs is a surface to keep closed rather than a convenience to add.

### One product per call, not a list per call

A replace-the-list write would silently drop whatever a second tab marked between
its read and this write. Per-item add and remove is the shape a star actually has
and has no lost update to lose. Marking what is already marked writes nothing and
answers the list — the same reading the inventory's revocation takes — so a star
pressed twice does not churn a stored file.

The list is sorted on write, so the stored bytes are a function of the *set* and
not of the order somebody pressed things in. A file whose contents change without
its meaning changing makes a backup diff unreadable.

### A filter over the columns, not a fourth destination

The portal narrows the cascade rather than pulling favourites onto a screen of
their own: a favourite is still a product in the catalogue, and a separate screen
would hide what it is part of. A whole is kept when it is marked **or when
something under it is**, because hiding a bundle whose service somebody starred
would hide the way to reach the star.

## Consequences

**A ceiling, and it is a product judgement as much as a budget.** A shortcut list
nobody can scan has stopped being a shortcut. It is a budget nonetheless: without
one, an account grows a stored file without bound by pressing a star.

**Not an MCP tool**, and not for a disclosure reason — the routes only ever touch
the caller's own list. A favourite is a navigation aid for a person in front of a
screen; an assistant does not navigate one, and a tool here would let a robot set
a preference into somebody's portal that they did not choose and have no obvious
way to attribute.
