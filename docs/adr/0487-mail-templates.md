# ADR-0487: A mail text is a template in the language form text views already speak

- **Status:** Accepted
- **Implementation:** Landed
- **Date:** 2026-10-25
- **Deciders:** Atlas maintainers
- **Open question:** whether FEEL's `string()` writes every value the same way in
  feelin, the FEEL engine form-js evaluates a form's templates with, and in
  `github.com/pblumer/feel`, which renders a mail on the server. The template language
  is the same on both sides and its rules are held to feelers 3.1.0 by tests; what a
  placeholder prints for a list, a context or a temporal is each engine's own `string()`,
  and nobody has compared the two beyond numbers, strings and dates.
- **Question checked:** 2026-10

## Context and problem statement

A mail task's subject and bodies are literal-or-FEEL values
([ADR-0079](0079-outbound-mail-connector.md), the fx toggle of
[ADR-0067](0067-service-task-connector-catalog.md)). A literal cannot carry a value from
the instance, so every mail that names a customer, an order or a deadline is a FEEL
expression that joins strings:

```
="Guten Tag " + kunde.vorname + ",\n\nIhre Bestellung " + string(bestellung.nummer) +
 " über " + string(betrag) + " CHF ist unterwegs."
```

That is laborious to write and hard to read: the text a recipient will see is scattered
between quotes, a line break is an escape sequence, a number needs `string()`, and a
missing space between two pieces shows only in the sent mail. A list — the positions of
an order — needs a `for` expression and `string join()`. An HTML body
([ADR-0079, amended](0079-outbound-mail-connector.md)) is worse: markup inside FEEL
strings, quotes inside quotes. The Modeler offers no way to see the result before a
mail is sent, and its HTML examples even showed a `{{name}}` placeholder the engine did
not understand.

The question: **how does an author write a mail text with values from the instance in
it, readably, and see what it will say before it is sent?**

Two facts about Atlas bound the answer:

- **Atlas already has a template language.** Forms are form-js schemas
  ([ADR-0028](0028-forms-and-the-tasks-app.md)), and form-js's Text and HTML views
  evaluate their content with feelers (bpmn-io/feelers 3.1.0, bundled in
  `api/web/vendor/form-js`): `{{ FEEL }}` placeholders, `{{#if FEEL}}…{{/if}}` and
  `{{#loop FEEL}}…{{/loop}}` sections. Authors who build forms write it today.
- **A mail is rendered on the server.** The value is computed where the job is resolved
  (`connector/mail/offload.go`, `Resolve`), from the variables in the state store, and
  every FEEL the model carries is compiled at deploy (invariant I5). feelers is a
  JavaScript library.

## Decision drivers

- **One expression language.** Whatever goes between the braces must be FEEL: it is
  what conditions, mappings and every other field are written in, and what the
  Developer View's reference, completion and FEEL assistant already serve.
- **Readable at a glance.** The text the recipient reads should be the text the author
  reads, with the variable parts marked.
- **Compile, don't interpret (I5).** A template is parsed and its FEEL compiled when the
  model is deployed; a malformed one is a deploy error, never a mail that goes out half
  rendered.
- **A deployed model keeps its meaning.** Every definition is recompiled at boot; a
  literal that happens to contain `{{` must not start meaning something else, and must
  never be able to stop the server from starting (ADR-0177, ADR-0393).
- **An HTML body must not let a variable add markup.** A customer name from a web form
  is not trusted; a mail sent from the organisation's address with a link a stranger put
  into it is phishing with the organisation's signature.

## Considered options

1. **Keep FEEL string building**, and improve only the editor.
2. **feelers' syntax with FEEL inside, implemented in Go** — the language form text
   views use.
3. **Go's `text/template` / `html/template`.**
4. **Handlebars or Liquid through a third-party Go library.**

## Decision outcome

Chosen option: **2, feelers' syntax with FEEL inside, implemented in Go**
(`expr/feelers`), because it is the one option that adds no language: the braces are
the ones form authors already write, and what is between them is FEEL.

```
Guten Tag {{kunde.vorname}},

Ihre Bestellung {{bestellung.nummer}} ist unterwegs:
{{#loop bestellung.positionen}}
- {{menge}} × {{artikel}}
{{/loop}}
{{#if bestellung.express}}
Sie wird morgen zugestellt.
{{/if}}
```

- **Opt-in per task.** `<atlas:mailConnector templating="true">` switches the subject,
  `body` and `bodyHtml` to templates. Without it — every task deployed before this
  record — a `{{` is text, exactly as before. Within a task with templating on, a value
  with a leading `=` is still one FEEL expression and a value without `{{` is still a
  literal; templating changes only what a placeholder in a literal means. The attribute
  is refused on an operation that sends no text (`list`, `get`, `move`, …), like every
  other attribute an operation does not use (ADR-0438).
- **The rules are feelers'.** A placeholder renders FEEL's `string()` of its value and
  null as nothing; `{{#if}}` renders when the value is truthy as JavaScript has it (false,
  null, 0 and "" are not); `{{#loop}}` walks a list, renders nothing for null and once
  for any other value, and binds the element's fields, `this` and `parent` — the
  variables outside a loop are reached through `parent`, as in feelers. An opening tag
  swallows the line break after it and a closing tag swallows its own and puts one back
  after a rendered section, which is what lets section tags stand on lines of their own.
  These rules were read from feelers 3.1.0's interpreter and grammar
  (`@bpmn-io/lezer-feelers` 1.0.0) and are pinned case by case in
  `expr/feelers/feelers_test.go`, quirks included (an empty loop leaves its line break).
- **Strict at deploy, lenient at render.** feelers renders a broken template with a
  warning sign because a form must show whatever it is given. A mail must not: an
  unclosed `{{`, a section without its end, an end without its section, an unknown
  section (`{{#each}}`) and FEEL that does not compile are refused at deploy, with the
  line and column. Each placeholder's FEEL goes through the compiler's FEEL gate, so a
  call this build can only answer with null is refused at deploy and kept on reload,
  like every other expression (ADR-0388/0393). At render a placeholder whose expression
  fails or answers null renders as nothing, as a failed FEEL field always has.
- **An HTML body escapes what a placeholder inserts** (`html.EscapeString`) and keeps
  the template's own markup. A `=` HTML body is unchanged: it composes markup in FEEL and
  is not escaped, which is what it has always done and what a body built from a script
  task's HTML still needs.
- **A subject stays one line.** A section's line breaks in a subject template become
  spaces before the header is written.
- **The preview is the server's renderer.** `POST /api/v1/feel/template` renders a value
  as a mail task with templating on would — `=` FEEL or template, the deploy's rules,
  escaping with `html: true` — against sample variables, and answers the text, every
  placeholder that rendered as nothing, or the compile error with its place. It reads and
  writes no state and runs off the run loop, like `POST /api/v1/feel/evaluate`; MCP omits
  it for the same reason.
- **The Modeler.** A *Placeholders* select on the mail task writes the attribute. With
  it on, the subject and body are template fields (tags coloured, FEEL completion inside
  the braces, each variable offered as a whole placeholder outside them) and stay FEEL
  with fx on; the HTML body is an HTML template field. F2 opens them in the Developer View
  ([ADR-0145](0145-developer-view-for-code-fields.md)) with a **Preview** panel: the
  sample variables (prefilled from a live instance, as the Test panel's are), and the
  rendering, kept current as either changes — a text body as text, an HTML body as a
  sandboxed page (`<iframe sandbox="" srcdoc>`, no scripts, no same origin, the posture
  the outbox already gives a rendered mail). An HTML body without placeholders gets the
  same preview of itself.

### Consequences

- **Positive:** a mail text reads as the mail; lists and conditional paragraphs need no
  FEEL gymnastics; one template language across forms and mails; a broken template is a
  deploy error with a place; the author sees the rendered mail, HTML included, before
  deploying; no new dependency.
- **Negative / trade-offs accepted:** Atlas now maintains a second implementation of
  feelers, which must follow the JavaScript one by hand — the tests pin today's rules,
  not tomorrow's; feelers has no `else` and no way to write a literal `{{` other than the
  placeholder `{{"{{"}}`; a placeholder's value is printed by the server's FEEL engine and
  may differ in edge cases from what a form prints (the open question above); the preview
  needs a server, and the Modeler's own highlighting of a placeholder inside an HTML
  attribute is approximate.
- **Follow-ups / risks to watch:** reading a received mail's HTML body and showing it to
  a person in a task form is the second half of the request that produced this record
  and is decided separately; templates for other Worker Types' free-text fields (a Teams
  message, a Jira comment) can reuse `expr/feelers` the day they are asked for.

## Pros and cons of the options

### Option 1 — FEEL string building
- Good: nothing new; already works.
- Bad: the problem the record exists for — unreadable, laborious, no loop without
  `for`/`string join()`, markup in quoted strings.

### Option 2 — feelers syntax, FEEL inside, in Go
- Good: no new language for anyone who builds forms; FEEL stays the only expression
  language; compiled at deploy through the existing FEEL gate; small (one parser, one
  renderer, a few hundred lines); no dependency.
- Bad: a second implementation of a JavaScript library to keep in step; feelers' small
  feature set (no `else`, no partials, no raw insert).

### Option 3 — `text/template` / `html/template`
- Good: the standard library; `html/template` escapes by context (attribute, URL, CSS),
  which is stronger than escaping every insert the same way.
- Bad: a second expression language with its own syntax (`{{.kunde.name}}`, pipelines,
  its own functions) beside FEEL, which modellers would have to learn and the Modeler
  would have to complete and document; values would have to be converted from FEEL into
  Go maps; nothing in Atlas speaks it today.

### Option 4 — Handlebars or Liquid
- Good: familiar to web developers; richer (else, partials, filters).
- Bad: a third-party dependency for the engine; again a second expression language
  (Liquid filters, Handlebars helpers) beside FEEL; the forms would still speak feelers.

## Links

- amends [ADR-0079](0079-outbound-mail-connector.md) (the mail Worker's message fields)
- reuses the language form-js's text views evaluate, adopted with the forms in
  [ADR-0028](0028-forms-and-the-tasks-app.md)
- previews in the Developer View of [ADR-0145](0145-developer-view-for-code-fields.md)
- honours I5 (compiled at deploy), ADR-0177/0393 (a compiler rule never stops a boot)
  and ADR-0388 (the FEEL gate)
