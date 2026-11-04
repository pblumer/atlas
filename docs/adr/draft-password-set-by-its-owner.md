# ADR-DRAFT: A password is set by its owner, through a one-time link

- **Status:** Accepted
- **Implementation:** Landed
- **Date:** 2026-11-04
- **Deciders:** Atlas maintainers
- **Open question:** A link proves that whoever used it held the mailbox, and nothing
  more — the bar every mail-based recovery sets. Whether an instance should be able to
  demand a second factor before a reset (a code on another channel, a passkey) is not
  settled here.
- **Question checked:** 2026-11

## Context and problem statement

The account lifecycle Atlas ships (ADR-0122/0123/0126) handled the one secret in it the
worst way available:

- The administrator who approved a registration **typed an initial password** into the
  approval form (`ba-konto`). It became the process variable `initialpasswort`.
- A process variable is an event in the log (I6). The password was therefore on disk for
  as long as the log is kept, readable by everybody who may read the instance's
  variables, in the variable audit, in a backup, and in whatever an exporter ships.
- The mail task "Zugangsdaten senden" then sent it **in clear text** to the address the
  requester typed — an address nobody had verified.
- "Bitte ändern Sie das Passwort nach dem ersten Login" was a request, not a rule: there
  was no route by which a signed-in person could change their own password at all. Only
  an administrator could (`POST /api/v1/users/{id}/password`).
- A person who forgot their password could only ask an administrator, who then chose a
  new one and passed it on — again a secret known to two people and sent somewhere.

ADR-0044 deliberately had no self-service recovery: there was no transactional mail
sender, and e-mail was optional per account. Both premises have moved. Mail is a Worker
every installation can configure (ADR-0079/0093/0150), and ADR-0523 established how a mail
carries a signed link without the secret touching a variable, an event, a log line or a
Referer. The question is how a password comes into being, and is recovered, so that it is
only ever known to the person it belongs to.

## Decision drivers

- **A password is known to its owner and to nobody else** — not to an administrator, not
  to a process, not to a mailbox.
- **No secret in a variable, an event, a log line, a URL path or a Referer** (ADR-0523's
  rule, which the intake process broke).
- **The mailbox is the recovery anchor** — it already was, implicitly, since the password
  went there; now it is the only thing a link can reach.
- **A link scanner opens every link in a mail** (Defender Safe Links, Gmail): opening a
  link must change nothing.
- **Dogfooding** (ADR-0126): the mails are sent by system processes, so their texts are
  modelled, their failures are incidents, and the preview outbox shows them.
- **No engine change**: nothing new on the hot path (I1), applyToState untouched (I4),
  nothing per link persisted.

## Considered options

1. **Keep the initial password, but out of the variables** — the approval form seals it
   (ADR-0314) and the mail still carries it.
2. **A one-time link the owner sets the password through**, signed with a vault-derived
   key, bound to the account's current credential, carried by a system process's mail.
3. **A stored token table** — random tokens kept hashed in a sidecar, revoked explicitly.
4. **A server-side transactional mail sender** outside the engine.

## Decision outcome

Chosen option: **2**, with the mails sent by system processes (not option 4).

### The link

```
link  = {external URL}/public/password#{user id, base64url}.{expiry, unix s}.{mac}
mac   = HMAC-SHA256(K, user id ‖ expiry ‖ email ‖ password hash)[:16], hex
K     = the vault's key derived for "atlas/password-link/v1"
```

- **One-time without a table.** The MAC covers the account's current password hash. The
  moment a password is set through the link — or any other way — the hash changes and
  every link minted before is dead. An activation link (no hash yet) dies the same way.
- **Bound to the address.** The MAC covers the account's e-mail too: an administrator who
  corrects an address kills the links sent to the old one.
- **Short-lived.** An activation link (the account has no password yet) holds 7 days; a
  reset link (it has one) holds 1 hour. The expiry is in the link and under the MAC.
- **K** is derived from the vault's master key for this purpose alone, like ADR-0523's
  answer key, and stored nowhere. A server run with `--vault=false` mints no links;
  rotating the master key invalidates all of them.
- **The token is in the fragment**: no access log, no proxy log, no Referer. The page
  sends it in an Authorization header.
- **Opening it changes nothing.** `GET /public/password/link` says whom the link is for
  and whether it is an activation or a reset; only `POST /public/password/link` with the
  new password sets it. A scanner that fetches the link gets a page.
- **Setting the password ends every session** of the account, and clears the login
  throttle for it.

Every refusal — a wrong MAC, an expired link, an account that is disabled, deleted, not
local, or whose password or address changed since — answers 404 alike.

### Who may mint one

A mail task carries a password link when it names the account: `passwordFor="=benutzername"`
on `<atlas:mailConnector>`. The subject and bodies then read the link as `passwordLink`,
for that resolution only — never written to the instance. Three rules hold it, and the
last is the one that matters:

1. **Only a system process** (ADR-0122) may carry one; any other process's mail task
   fails at send time, naming the rule.
2. **Stage 5** refuses `passwordFor` on a mailbox operation (`password.operation`) and a
   mail task that names an account but never shows `passwordLink` (`password.unshown`).
3. **The mail goes to the account's own address and to nobody else.** At send time the
   resolved To must be exactly that address, with no Cc and no Bcc. A model — even a
   modified system process — cannot send somebody's link anywhere but to the mailbox
   that already is their recovery anchor.

The preview provider (ADR-0150) is read by operators; it **withholds** the link, so an
operator cannot request a reset for an administrator and read the link off the outbox.
An administrator who needs the link on an instance without real mail asks for it
directly (below).

### The flows

- **Registration** (`proc_benutzer_aufnahme`, replacing ADR-0126's shape after the start):
  the requester is first mailed a confirmation link (ADR-0523, `email_bestaetigen`
  answered by link, bounded by a 72-hour timer). Only a confirmed request announces
  `atlas.user.requested` and waits for approval, so administrators no longer see requests
  for addresses nobody holds. The approval form (`ba-konto`) has **no password field**.
  `userConnector create` makes the account without a password, and "Zugang aktivieren"
  mails the activation link.
- **Forgot password**: the login screen posts a username or address to
  `POST /public/password/forgot`, which **always answers 202 with the same body**. Only
  when it names an enabled local account with an address does the server start the
  system process `proc_passwort_link` (variables `benutzername`, `anlass`), at most once
  per account per five minutes. `GET /api/v1/auth/password-recovery` tells the login
  screen whether the instance can do this (vault, external URL, process deployed).
- **An administrator** never sees or chooses a password in the Console:
  `POST /api/v1/users/{id}/password-link` either mails the account its link (through
  `proc_passwort_link`, `anlass="einladung"`) or returns it to the administrator to hand
  over in person, for an instance without mail. Both are audited.
- **A signed-in person** changes their own password with `POST /api/v1/auth/password`
  (current and new password, throttled like a login); their other sessions end.
- **Operator recovery** stays `atlas reset-password` (ADR-0044's escape hatch) for the
  locked-out administrator.

### What a process may no longer do

`<atlas:userConnector>` may not carry a password: `create` makes an account awaiting
activation, and `set-password` is refused. Both are stage-5 errors (`user.password`), so a
definition deployed before this is reloaded and reported, never refused at boot; at run
time the worker refuses a non-empty password and `set-password` with an incident.

### Consequences

- **Positive:** no password is ever stored outside the bcrypt hash, typed by anybody but
  its owner, or sent anywhere. The log of every new instance is free of credentials.
- **Positive:** a registration reaches an administrator only once its address is proven.
- **Positive:** a person can change and recover their own password.
- **Negative / trade-offs accepted:** the mailbox is the recovery anchor. Whoever controls
  it can reset the account — which was already true while the password was mailed.
- **Negative / trade-offs accepted:** a reset link stays valid for its hour until it is
  used; requesting a second does not revoke the first (both die on use). Option 3 would
  revoke it, at the price of durable per-link state.
- **Negative / trade-offs accepted:** registration and recovery now need `--external-url`
  and the vault besides a mail Worker; without them the mail task raises an incident
  saying which is missing.
- **Negative / trade-offs accepted:** `POST /public/password/forgot` does a little more
  work for an account that exists than for one that does not; the answer is identical,
  the timing is not perfectly so. The per-address rate limit bounds what can be measured.
- **Negative / trade-offs accepted:** the change of password ends sessions but not OAuth
  grants (ADR-0200); revoking them stays the administrator's act.
- **Migration:** the log of instances that ran the old intake still holds the initial
  passwords typed there; an append-only log cannot forget them (I6). Accounts created
  that way should be sent a reset link. Instances of the old version still waiting at
  "Antrag freigeben" should be migrated to the new version; if they are not, their
  account is created without a password and the old mail goes out without one.
- **Compatibility:** `POST /api/v1/users` no longer requires a password (an account
  without one awaits activation). `POST /api/v1/users/{id}/password` stays for scripts and
  is deprecated; it now ends the account's sessions.

## Pros and cons of the options

### Option 1 — seal the initial password
- Good: smallest change.
- Bad: the password still exists in two heads and one mailbox, and still goes in clear
  text to an unverified address. It fixes the log and nothing else.

### Option 2 — signed one-time link (chosen)
- Good: nothing per link is stored; use, a password change or an address change revokes
  it; the same machinery serves activation, recovery and the administrator's invitation.
- Bad: no explicit revocation of an unused link before it expires.

### Option 3 — stored tokens
- Good: explicit revocation, a list of outstanding links.
- Bad: a new durable store for something an HMAC already answers; another place a
  secret (even hashed) lives.

### Option 4 — server-side mail sender
- Good: no process instance per reset.
- Bad: a second mail path beside the Worker, with its own configuration, retries and
  failure reporting; texts not modelled; nothing in the preview outbox.

## Links

- supersedes, in part, ADR-0044 (no self-service recovery) and ADR-0126 (the intake's
  shape after its start form)
- builds on ADR-0523 (links in mails, the fragment, scanner safety), ADR-0122/0123
  (system project, user provisioning), ADR-0150 (preview outbox), ADR-0197 (login
  throttle)
