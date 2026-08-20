# ADR-0155: The Secrets panel says what a value has to be

- **Status:** Accepted
- **Date:** 2026-08-19
- **Deciders:** Atlas engine team

## Context and problem statement

An operator whose Gmail connector had stopped working renewed the refresh token,
pasted it into the vault under `gmail_auth`, and got:

```
mail: gmail credential is not valid JSON: invalid character '/' after top-level value
```

The value was a perfectly good refresh token. It was the wrong *shape*: a Gmail
connector's `credentialsRef` resolves to a JSON credential bundle (ADR-0093), of which
the refresh token is one field. The message is the Go JSON decoder reading `1//04…` —
it parses `1` as a number and stops at the slash — so it describes a syntax error in a
value the operator never thought of as JSON at all.

This is the third report in one day with the same shape, after an SMTP endpoint stored
without a port and a parked token that looked like a waiting one (ADR-0150): **the
console accepted configuration whose required form it never stated, and stated the
requirement only once something downstream failed.** The vault makes that worse than
elsewhere, because a secret is deliberately write-only (ADR-0069): after the save
nobody — not the operator, not a support engineer, not the server — can look at the
stored value and say what is wrong with it. The moment of entry is the *only* moment
the mistake can be named.

Two properties of the panel made it particularly quiet:

- **A secret is a name and an opaque string.** Nothing in the form distinguishes "the
  SMTP password" from "a JSON OAuth bundle" from "`kid.secret`". The knowledge exists —
  it is exactly the connector kind that resolves the reference — but the panel never
  asked.
- **Rotation ran through `window.prompt`.** A one-line browser prompt is the wrong
  instrument for a multi-line JSON bundle, cannot be pasted into comfortably, cannot be
  read back before committing, and structurally cannot carry an explanation. The field
  that most needed a hint was the one field in the console incapable of holding one.

## Decision drivers

- **Say it where it is typed** (ADR-0150). A requirement discovered by failure is a
  requirement that was not stated.
- **Write-only means entry is the last chance.** Every other layer has already lost the
  ability to help by the time the value is stored.
- **The knowledge already exists in the system.** The connector record names its
  `credentialsRef`; the kind and provider determine the shape. No new configuration.
- **Don't fork the truth.** The expected shapes must track the Go decoders, not drift
  into a second, prettier description of them.

## Considered options

1. **Improve the server's error message.** Detect a bare token and say "this looks like
   a refresh token; the credential is a JSON bundle".
2. **Validate on the server at set time.** Refuse `PUT /api/v1/secrets/{name}` when the
   value cannot be what the referencing connector needs.
3. **Make the form state the shape, and check the value before sending it.**

## Decision outcome

Chosen option: **"3 — the form states the shape and checks the value"**, because the
question ("what goes in this field?") is a question about the *form*, and the answer
exists before anything is typed. Option 1 improves a message that arrives too late to
whoever is looking; option 2 arrives at the right time but is the wrong layer for it —
see below.

The name is the binding. The moment it matches a connector's `credentialsRef`, the
panel knows which connector resolves it and therefore what the value must be, and says
so: which connector, what shape, and — for the JSON bundles — a skeleton with the
required fields, insertable into the field with one click. `SECRET_SHAPES` maps kind
(and, for mail, provider) to that description; `checkSecretValue` mirrors the server's
own decoders, including the per-method required fields of `newTokenSource` and the
tenant rule that otherwise surfaces as "credential has no token URL". A secret nothing
references makes no claim and is not checked.

Rotation moved from `window.prompt` into an inline panel under the row, and a JSON
bundle is edited in a visible textarea rather than a password box: several lines that
have to be pasted and read back before saving, where masking is what made the shape
unknowable to begin with. It is still never shown after the save — the vault does not
hand it back.

The check **refuses** rather than warns. This is the one place in the console where a
mistake becomes permanently invisible the instant it is accepted, and the refusal names
the connector that defines the expectation, so changing that connector (or using
another name) remains the way out.

Server-side validation (option 2) was deliberately not chosen *for now*: the vault is a
general secret store whose entries need not belong to a connector at all, the binding
is a soft one (a reference can be pointed anywhere at any time), and a hard server rule
would make a legitimate order of operations — store the secret, then create the
connector — fail. The client check fires exactly when the expectation is known, which is
the narrow case that produced this report. If secrets ever gain a declared type, this
moves to the server.

### Consequences

- **Positive:** the shape is visible before the value is typed; the specific mistake
  that produced this report is refused by name; a rotation is no longer done blind,
  because the row says what the secret is for and which connector uses it; and a
  multi-line credential is finally editable in a field built for it.
- **Negative / trade-offs accepted:** `SECRET_SHAPES` is a second description of the Go
  decoders and can drift from them — a field added in `credentialBundle` is a field to
  add here, and only a reader will notice. The check is client-side, so it is guidance
  and not a guarantee; the API still accepts any string. Refusing rather than warning
  can block an operator who means something the catalog does not know, at the price of
  changing the connector first.
- **Follow-ups / risks to watch:** a declared secret type on the record would let the
  server own this and delete the duplication; the same "say the shape where it is
  typed" treatment is still owed to the environment-variable fallback
  (`ATLAS_CONNECTOR_<REF>_TOKEN`), which has no form at all.

## Pros and cons of the options

### Option 1 — a better server message
- Good: one place, applies to every path including the API.
- Bad: still arrives after the fact, in a toast, to whoever happens to be looking; says
  nothing before the mistake is made.

### Option 2 — server-side validation at set time
- Good: authoritative, covers API clients as well as the console.
- Bad: wrong layer for a soft binding — it would refuse storing a secret before its
  connector exists, and make the vault's generality conditional on connector state.

### Option 3 — the form states the shape and checks
- Good: answers the question before it is asked; the only layer that can, since the
  value is invisible afterwards.
- Bad: duplicates the decoders' knowledge in the browser; not a guarantee.

## Links

- relates to ADR-0069 (the write-only encrypted vault this explains)
- relates to ADR-0093 (the credential bundles whose shape it describes)
- relates to ADR-0150 (the same "say it where it is typed" decision, for endpoints)
- relates to ADR-0041 (a connector stores a credential *reference*, never a value)
