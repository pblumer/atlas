# ADR-DRAFT: The service portal carries its own sign-in

- **Status:** Accepted
- **Implementation:** Landed
- **Date:** 2026-09-18
- **Deciders:** Atlas maintainers

## Context and problem statement

The service portal ([ADR-0312](0312-portal-catalogue-order-inventory.md)) is a page
of its own, not a view of the Console shell. It is reached from a link in a mail,
from a bookmark, and from Atlas' own menu. Its readers are the people a catalogue
is offered to: employees ordering a laptop, not operators.

On an instance started with `--auth`, every route the portal reads needs a session:
the catalogue, the releases, the orders, the inventory, the favourites and the
directory all carry `role: user`, and the boundary answers an anonymous caller 401
before any of them runs. That is correct of the server, and nothing here changes it.

What the page did with those refusals was not correct. The catalogue read was
wrapped in a `catch` that treats any failure as the ordinary "you are the audience
for nothing" answer, so a 401 was drawn as **"Ihnen ist kein Katalog zugeordnet"** —
a statement about entitlement made in answer to a question about authentication.
The orders read was not wrapped at all, so it threw out of `load()` and the page
showed its generic failure line with an HTTP status in it.

So a visitor who opened the portal without a session saw an error, no catalogue,
nothing saying a sign-in was needed, and nowhere to give one. The only way in was
to know that `/index.html` is a different page, that it has a login, and that
coming back afterwards would work — which is knowledge about Atlas' internals, held
by exactly the readers this page is not for.

## Decision drivers

- The portal's readers are not operators. They have no server log, no second screen
  to try and, often, nobody to ask but the desk the portal exists to save.
- A refusal and a failure are different answers, and a page that shows one as the
  other sends somebody to fix the wrong thing.
- Everything a sign-in screen needs before anybody has a session is already public,
  for exactly this reason: the login route, the identity providers, the registration
  setting, the instance theme and the instance mark.
- The portal is a page people leave open. Sessions run out underneath it.

## Considered options

1. **Redirect to the Console's sign-in** and return afterwards.
2. **The portal carries its own sign-in**, against the same endpoints.
3. **Serve the portal page itself behind authentication**, so the server's own
   refusal replaces the page.

## Decision outcome

Chosen option: **the portal carries its own sign-in**.

`GET /api/v1/auth/me` is read first, before anything else on the page, and its
answer decides what is drawn:

- **200** — today's behaviour exactly, whether or not it names a user. An instance
  with enforcement off has nobody to be, which is the single-user mode
  [ADR-0377](0377-portal-without-identity.md) already describes.
- **401** — enforcement is on and nobody is signed in. The sign-in is the whole
  page, and nothing behind it is read.
- **anything else** — the page loads as before. Unreadable is not forbidden, and an
  instance whose session store is broken must not show every customer a login form
  that cannot work.

Three things follow, and each is a decision rather than a consequence.

### It returns to the portal, not to the Console

This is the reason it is a screen rather than a redirect. The Console's sign-in
lands on `#/console`, which is the wrong product for somebody who followed a link
to order a laptop: they hold `user` and no Console role, so they arrive in a shell
whose every entry is missing and have to find their own way back. The portal also
carries its own language switch, and it has to be reachable *before* the sign-in —
a German-speaking customer meeting an English-only form is the case the portal's
whole message catalogue exists to prevent.

The cost is a second sign-in screen in the tree. It is a real cost and it is
bounded: the logic is the server's, reached through the same four public routes,
and what is duplicated is markup and wording — the two things that had to differ
anyway.

### It offers whatever the server offers

An installation that federates its login has no password to type. A password form
alone would be a screen whose only control cannot work, so the providers are read
and drawn above the form, with the form left standing beside them because an
instance may federate one login and still keep local accounts. Where registration
is configured ([ADR-0126](0126-self-service-registration-link.md)) the portal is exactly
where somebody with no account lands, so the link is shown there too. Neither read
may cost the password form: both are wrapped, and an instance that cannot answer
falls back to the one control that works everywhere.

### A federated login lands where it started

Offering a provider here meant fixing where the callback returns to. It has always
redirected to `/`, which was right while the Console's login was the only place one
of these could begin, and which would now sign a portal visitor in and drop them in
the Console — the exact outcome the previous section rejects, reached by the one
path where this page cannot decide it for itself.

So the start records the page that began the login and the callback lands back on
it, successes and failures alike: a refusal reported on a page somebody never
opened is no better than a success delivered there.

The page travels through the browser, which is what makes an open redirect, so it
is an **allowlist** and not a validated path. Exactly two pages in Atlas are served
before anybody is signed in and can therefore start one of these; anything else,
including nothing at all, is the Console. A validated path would have to be right
about scheme-relative URLs, backslashes, encoded newlines and whatever the next
browser accepts — and being wrong once means a link that signs somebody in and
drops them, session and all, wherever its author chose. The allowlist cannot be
wrong about any of that, because the value it returns was never the value it read.

It is carried in a cookie beside the state cookie, scoped and expired identically,
rather than in the pending login record: a login can fail *before* there is any
record to read — a reply naming a state nobody holds, a provider that came back
with an error — and those failures have to reach the same screen as the rest. The
cookie is read back through the allowlist as well, because a browser is not a safe
place to keep anything.

### The throttle is named as itself

After five wrong guesses the server refuses the *attempt* for a quarter of an hour
without looking at the password ([ADR-0197](0197-login-throttle-and-audit-log.md)). Reported as
a credential failure — which is what a screen that only knows "the login failed"
would say — it is how somebody spends that quarter of an hour retyping a password
that is already correct, and then restarts the server, the one action that clears
the throttle's buckets. The Console's sign-in already tells the two apart; it
matters more here, because an operator can read the log and this reader cannot.

A 401 stays deliberately vague. The server refuses an unknown account and a wrong
password identically so that a login cannot be read as a directory, and this screen
does not undo that.

### What the sign-in screen shows about itself

The instance's mark, from `GET /api/v1/settings/logo` — public precisely so a screen
shown before any session can read it. No catalogue is resolved yet, because
resolving one is what the sign-in is for, so there is no catalogue brand to use. A
page that asks for a password while saying nothing about who is asking is the shape
of a phishing page, and the mark is the one thing on that screen a visitor can
recognise.

### The session that runs out

A 401 raised *after* the gate has passed is the same refusal one step later, and
showing it as a failure would be this defect with a different trigger. It returns
to the sign-in, saying that the session ended — which is true then and would be a
lie on a first visit, so the two are distinguished rather than merged.

## Consequences

- **Positive:** the portal is usable on an authenticated instance by somebody who
  knows nothing about Atlas beyond the link they were sent. A refusal reads as a
  question. An expired session is recoverable in place rather than by guessing that
  a reload might help.
- **Negative / trade-offs accepted:** a second sign-in screen exists, so a change to
  how Atlas authenticates a browser now has two screens to reach. The wording is
  duplicated rather than shared, because the Console's is English-only by design and
  the portal's is in the message catalogue every locale is complete in.
- **Follow-ups / risks to watch:** the allowlist is a list of two, named in
  `api/oidclogin.go`. A third pre-auth surface has to be added to it, and will not
  work without noticing — a federated login from it silently lands on the Console.
  That is the safe direction to fail in, and it is the one to remember.

## Pros and cons of the options

### Option 1 — redirect to the Console's sign-in
- Good: one screen, one place for the password form, the providers, the registration
  link and the throttle wording. Nothing new to maintain.
- Bad: it lands the visitor in the Console, which is not their product and shows them
  a shell they hold no role for; it loses the page they were going to; and it hands a
  portal customer an English-only screen on a surface that is translated everywhere
  else.

### Option 2 — the portal carries its own sign-in (chosen)
- Good: the visitor stays on the page they asked for, in the language they chose,
  looking at the instance they were sent to. The server is unchanged.
- Bad: a second screen against the same endpoints, and a second place to notice when
  those endpoints change.

### Option 3 — serve the portal page behind authentication
- Good: no screen to write at all; the server refuses the page and the browser is
  handed the Console's login.
- Bad: it breaks the shape every single-page surface in Atlas has — the shell is
  public and the data behind it is not — and it would still have to answer "and then
  what", because the refusal has nowhere to send somebody back to. It also puts a
  static asset behind a session, which is the one thing `wantPublicRoutes` is written
  to keep from happening by accident.

## Links

- extends [ADR-0312](0312-portal-catalogue-order-inventory.md) — the portal this is
  the front door of.
- relates to [ADR-0377](0377-portal-without-identity.md) — the other mode: an
  instance with no enforcement, where there is nobody to be and the catalogue is
  readable anyway. The gate here is deliberately the server's answer and not that
  mode's flag, so the two cannot be confused.
- relates to [ADR-0197](0197-login-throttle-and-audit-log.md) — the refusal this screen names as
  itself rather than as a wrong password.
- relates to [ADR-0210](0210-federated-authentication.md) — the providers the screen offers
  where any are configured.
- relates to [ADR-0126](0126-self-service-registration-link.md) — where somebody with no
  account starts.
