# ADR-DRAFT: A structured catalogue declares Marktleistung and service, and files them under capabilities

- **Status:** Accepted
- **Implementation:** Landed
- **Date:** 2026-11-04
- **Deciders:** Atlas maintainers, catalogue owner
- **Open question:** Whether the capability register needs a name per language. A structured catalogue words a heading from the capability's single name wherever the product leaves a language unworded, so a French reader of a German-named capability reads German until the product supplies the French wording. Whether that is tolerable depends on how many catalogues are kept in several languages, which nobody has counted yet.
- **Question checked:** 2026-11

## Context and problem statement

The catalogue holds products and nothing else. Whether a product is a *Marktleistung* — the whole somebody orders — or a *service* behind one is read off the containment graph: ADR-0383 decides it by depth, and its amendment withdrew the Bundle level so that every root is a Marktleistung and everything below one a service. The shop's cascade draws Kategorie › Produktgruppe › Produkt › Services from that reading.

That made the distinction a picture rather than a fact. The demo catalogue shows what follows:

- a single account with no parts is offered as a Marktleistung, because nothing contains it;
- a product becomes a service the moment somebody draws an edge into it, and stops being one when the edge is removed;
- a heading written on a part is silently never read;
- a part can be ordered on its own through the API, although the shop never offers it;
- the category and the product group are free strings (ADR-0360), so "Identität", "Konto" and "Account" are three headings for one thing, and nothing says which capability of the organisation a heading stands for.

The catalogue owner defined the four terms the catalogue should hold:

1. A **Marktleistung** is like a Lego house: an assembly of services.
2. A **service** is a Lego brick: it always has a lifecycle process of its own and can be provisioned autonomously.
3. A **category** is the top level of a capability, for example *Arbeitsalltag bewältigen*.
4. A **product group** is a capability one level below the category's, for example *Identität und Zugang*.

The question this record answers: how does the catalogue hold those four as facts it can check, without breaking catalogues that were valid before?

## Decision drivers

- A distinction the shop draws must be one the server enforces; otherwise it holds only until somebody uses the API.
- The capability register stays flat (ADR-0305): no `parent` field. "One level below" is a relation the register already holds — `requires` — and not a hierarchy invented for the catalogue.
- Existing catalogues must keep publishing. Every rule below refuses something that was valid yesterday.
- A release stays self-contained (ADR-0312): the shop reads releases and nothing else.

## Considered options

1. **Keep deriving the level from the graph** and document the conventions.
2. **Declare the kind on every item and enforce it everywhere**, migrating every catalogue on upgrade.
3. **Declare the kind on every item, enforce it in catalogues that opt in**, and make their headings capability keys.

## Decision outcome

Chosen option: **3**.

Option 1 is the status quo, and the status quo is the problem. Option 2 cannot be migrated automatically: deriving the kind from depth turns every single account into a Marktleistung without a service, which the new rules refuse, so upgrading would stop every existing catalogue from publishing.

### The model

- `Item.Kind` is `offering` (Marktleistung), `service` or empty. Empty means "read it off the graph", as before. An unknown value is refused in every catalogue.
- `Catalog.Structured` opts a catalogue in. It is a switch somebody turns on after converting the products, not a side effect of upgrading.
- `Release.Structured` carries the switch into the release, because an order is placed against the release and the switch may have moved since.

### What publishing a structured catalogue proves

A **Marktleistung**:

- is contained by nothing;
- includes at least one service by composition (an option alone would provision nothing of its own);
- contains services only, so it holds no nested Marktleistung (the owner decided against packages of Marktleistungen);
- names a category and a product group;
- keeps its own lifecycle binding like every item (checkBindings). The owner decided that the Marktleistung holds the orchestration that completes and dismantles the whole.

A **service**:

- has no parts;
- carries no category, product group or wording for either;
- is contained by at least one Marktleistung in this catalogue. A service nothing contains is a product nobody can order, so it is left out of the catalogue's item list until a Marktleistung uses it;
- keeps its own lifecycle binding. That binding is what makes it provisionable on its own.

Every product without a kind is refused.

### Headings are capability keys

In a structured catalogue, a Marktleistung's `category` is the key of a capability, and its `productGroup` is the key of a capability that the category capability lists in `requires`. Publishing reads the register through `CapabilityLookup`, after the pure computation, in the same way it already asks about deployed processes (ADR-0425). It refuses when:

- a key names no capability;
- a capability is deprecated;
- the product group is not required by the category;
- the register cannot be read. An unchecked heading must not read as a checked one.

The shop still groups by the key, exactly as ADR-0412 arranged. Where a declared language has no wording on the product, the release freezes the capability's name as the wording. The register is therefore not copied into the catalogue, and renaming a capability reaches every product under it with the next publish. ADR-0360 could not offer that rename. Its open question named this as the day a heading becomes a reference.

### What an order holds

`place` refuses a selection that names a service on its own in a structured release. A service may be selected:

- beside a Marktleistung it is part of (included or optional);
- as an option of a Marktleistung the recipient already holds. This is how a sleeve not ticked with the laptop is ordered later (ADR-0312, amendment of 2026-10-28).

### Consequences

- **Positive:** the shop's levels become facts the server enforces. A refusal names the product whose declaration and structure disagree. The catalogue's headings are the organisation's capabilities, so the gap report and the catalogue speak about the same things.
- **Negative / trade-offs accepted:** a single account becomes a Marktleistung with one included service, so two records exist where one did before. A capability carries one name, see the open question. Two products sharing one lifecycle process are not refused: a generic account process bound to many services is legitimate, and "its own" is read as "bound to it", not "unique to it".
- **Follow-ups / risks to watch:** the demo catalogue is to be converted and switched on. The shop needs no change, because a valid structured release has only Marktleistungen as roots. A catalogue that is never switched on keeps every weakness listed above.

## Pros and cons of the options

### Option 1: derive from the graph
- Good: nothing to migrate.
- Bad: every weakness in the context stays, and the API can still order a part directly.

### Option 2: declare and enforce everywhere
- Good: one rule for every catalogue.
- Bad: no automatic migration exists, so the upgrade itself would stop catalogues from publishing.

### Option 3: declare, enforce on opt-in, headings as capability keys
- Good: enforceable, migratable one catalogue at a time, and it answers ADR-0360's open question with the register that already exists.
- Bad: two modes to read until every catalogue is converted.

## Links

- amends ADR-0383 for structured catalogues: the level is declared, and depth remains the reading for the others
- answers part of ADR-0360's open question: a category becomes a reference, here to a capability
- relates to ADR-0305: the register stays flat, and "below" is `requires`
- relates to ADR-0312: three models, frozen releases, and an option offered again for a held whole
- relates to ADR-0412: the key groups, the wording is per language
