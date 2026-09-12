# ADR-0313: The portal follows the browser's language; the console still does not

- **Status:** Accepted
- **Implementation:** Not started
- **Date:** 2026-09-11
- **Deciders:** Atlas maintainers
- **Open question:** Whether refusing to publish a catalogue with an untranslated
  product is a rule an organisation works with or works around. It is the right rule
  for the reader, and it puts a translator on the critical path of every product
  change. No catalogue has been maintained under it, so whether it produces translated
  catalogues or unpublished ones is not established.
- **Question checked:** 2026-09

## Context and problem statement

The portal serves internal staff and external customers, and must offer its surface in
several languages, preselected from the visitor's browser and changeable by hand.

[ADR-0267](0267-console-speaks-german-first.md) decided the opposite for the console,
in as many words: German is the default **and not the browser's language**, and
`navigator.language` is deliberately not consulted. Its reason is specific and good —
the console is a migration in progress, translated screen by screen, so guessing from
a browser setting "would hand somebody half a translated screen for a reason they
cannot see". It also chose that a missing key renders **as the key**, visibly broken,
so a hole fails in review instead of falling back silently.

Both rules are right for a console being translated by the people who build it. Neither
survives contact with an external customer: a half-German screen is a defect to them,
and `portal.cart.submit` rendered on a page is not a review signal, it is an error
message in a language nobody speaks.

The question: **can the portal consult the browser without reintroducing the failure
ADR-0267 avoided** — and what has to be true for that to be safe?

## Decision drivers

- ADR-0267's reasoning, not merely its conclusion, has to be answered. It is a good
  record; an exception that ignores its argument would be a regression dressed as a
  feature.
- One mechanism, not two. `i18n.js` exists and is 200 lines with no dependency.
- A signed-in person's language choice must survive a change of device.
- Catalogue content and interface text are different things with different owners and
  must not share one completeness rule by accident.

## Considered options

1. **Keep ADR-0267 as is** — the portal defaults to German too, `?lang=` to change.
2. **Consult the browser everywhere**, console included.
3. **Consult the browser in the portal only, conditional on the portal's catalogue
   being complete.**

## Decision outcome

Chosen option: **3**, and the condition is the whole of the argument.

ADR-0267 refuses to guess because guessing can land a visitor on a **partly**
translated screen. That is a statement about the state of the catalogue, not about
browsers. Where every string a surface renders exists in every language that surface
offers, the failure it describes cannot occur — so the rule does not apply, and saying
so is answering the record rather than overriding it. Option 2 fails for exactly the
reason ADR-0267 gives, and it fails today: the console is still mixed.

Three things follow, and the first is what makes the other two honest.

### Completeness is held by a test, not by intent

The portal's message keys must exist in every locale the portal declares, and
`go test` fails when one does not. Without that, this record is a promise; with it, the
condition above is a property.

That test is also the boundary against scope creep: a locale is "declared by the
portal" only once its catalogue is whole. Adding French is adding a complete
catalogue, not adding a flag.

### In the portal, a missing key is not shown to the visitor

If one slips through anyway, the portal falls back to the catalogue's default language
rather than printing the key. This inverts ADR-0267 deliberately, and the inversion has
a reason rather than a preference behind it: a rendered key is a review signal **when
the reader can act on the review**. A customer ordering a laptop cannot. The console
keeps ADR-0267's behaviour unchanged — different reader, different right answer.

The two are not in tension once stated that way, and both live in one `i18n.js`: the
fallback is a property of the surface, not of the mechanism.

### Interface text and catalogue content are checked in different places

Interface text is checked by the test above, at build time. Catalogue content — product
names, descriptions, variant labels — is checked when a catalogue release is published,
which is where
[ADR-0312](0312-portal-catalogue-order-inventory.md)
already validates a release, and where a catalogue's own language list lives.

One rule connects them, and it runs at publish time (I5,
[ADR-0008](0008-feel-expression-strategy.md)): **a catalogue may not declare a language
the portal interface does not have.** Otherwise a customer would get a fully translated
product inside an untranslated page, which is the same half-translated screen ADR-0267
refuses, arriving by the other door.

### Choosing, and remembering the choice

- Not signed in — the registration page, a public product page: the browser's
  preference, narrowed to the languages the portal declares, falling back to the
  instance default.
- Signed in: the language stored on the account, or on first sign-in the browser's
  preference recorded there.
- Changing it writes to the account.

The account, not the browser. ADR-0267 remembers per browser, correctly, because the
console's audience sits at one workstation and the choice is a per-viewer preference
like a theme. A portal visitor has an account by definition and reaches it from a phone
and a desktop; a preference that does not travel with them is one they set twice and
lose on a third device. `?lang=` keeps working for a single page view without writing
anything, as it does today.

### Consequences

- **Positive:** A customer gets their language without asking, and never sees a
  half-translated page or a key. The console is untouched, and its migration continues
  at its own pace. One mechanism serves both, with the difference stated where it
  belongs.
- **Negative / trade-offs accepted:** A new language is a complete catalogue plus a
  passing test, never a quick flag — which is the point, and which will feel like an
  obstruction the first time somebody wants one for a demonstration. A product cannot
  be published untranslated, putting a translator on the critical path of every
  catalogue change. Two storage locations for one preference (account here, browser in
  the console) is a thing to explain to whoever next touches `i18n.js`.
- **Follow-ups / risks to watch:** The open question above. Dates, numbers and plurals
  are as good as `plural()` and the browser's formatters, which ADR-0267 already names
  as a limit; a locale with more than two plural forms needs that addressed before it
  is declared, not after.

## Pros and cons of the options

### Keep ADR-0267 as is
- Good: one rule, no exception to explain, nothing to build.
- Bad: an external customer meets a German page whatever their browser says. For the
  audience this portal exists for, that is the defect, not the safeguard.

### Consult the browser everywhere
- Good: one rule again, and the intuitive one.
- Bad: reintroduces precisely the half-translated console ADR-0267 was written to
  prevent, and does so today.

### Browser in the portal, on condition of completeness
- Good: answers ADR-0267's argument instead of its conclusion; the condition is held by
  a test rather than by care.
- Bad: an exception in a codebase that had one rule, and a publish-time rule that can
  block a release for a missing sentence.

## Links

- excepts [ADR-0267](0267-console-speaks-german-first.md) for the portal, on the condition its own reasoning names, and leaves the console unchanged
- validated at publish time by the release in [ADR-0312](0312-portal-catalogue-order-inventory.md)
- follows I5 as stated in [ADR-0008](0008-feel-expression-strategy.md) — the language check happens when a catalogue is published, not when one is browsed
