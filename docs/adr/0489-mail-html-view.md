# ADR-0489: A received mail's HTML body, read on request and shown in a task form

- **Status:** Accepted
- **Implementation:** Landed
- **Date:** 2026-10-25
- **Deciders:** Atlas maintainers
- **Open question:** the HTML reads for Gmail and Microsoft Graph are tested against
  recorded API answers, not against a live mailbox: that Graph answers a message's body
  as HTML when the request names no `outlook.body-content-type`, and that Gmail inlines
  or links the HTML part as it does the text part. ADR-0438's open question about the
  providers' setup still stands beside this one.
- **Question checked:** 2026-10

## Context and problem statement

A mail task reads a message with `operation="get"` and puts what it read into a
variable ([ADR-0438](0438-mailbox-worker.md)): a curated envelope, and with
`includeBody="true"` the body as plain text — an HTML-only message reduced to its text.
The HTML itself never reaches the process. That was deliberate: whatever a process
receives is in the log, every checkpoint, the exporter and every backup.

The request that forces this record: "read a mail and show its content to a person —
not only the text, the HTML too". A process that routes incoming mail to a person
(a support inbox, an invoice desk) needs that person to see the mail as it was sent:
its tables, its emphasis, its layout. Plain text loses most of it.

Three facts bound the answer:

- **The forms cannot show it today.** Task forms are form-js schemas
  ([ADR-0028](0028-forms-and-the-tasks-app.md)). form-js's HTML view escapes every value
  a template inserts, and its Text view passes Markdown through a narrow allow-list; a
  mail's markup becomes source text in the one and loses its styles in the other. And
  either would put a stranger's markup into the page's own DOM.
- **A mail's HTML is hostile by default.** It may carry scripts, forms that post
  elsewhere, a refresh to another address — and remote images, which tell the sender
  when, and from where, the mail was opened. In a task form that would happen each time
  somebody opens the task.
- **A variable has a budget.** One process variable may weigh `limits.Variable` (1 MiB by
  default), stored as JSON — where every `<` and `>` grows to six bytes. A newsletter's
  HTML is often hundreds of kilobytes.

The question: **how does a process read a mail's HTML body, and how is it shown to a
person, without the variable carrying what runs and without the form leaking the
reader?**

## Decision drivers

- **Opt-in, and only where it fits.** Reading the HTML is a choice in the model, on top
  of the text, as the text is on top of the envelope.
- **Clean what is stored, isolate what is shown.** The variable should not carry what
  runs; the page should not trust that it does not.
- **Nothing loads from elsewhere until a person asks.** Remote images are the mail
  client's classic privacy leak; the default is to block them.
- **Showing is not answering.** Completing a task must not write a mail back into the
  log, and the person holding the task must be allowed to read the variable the form
  shows.

## Considered options

1. **The HTML in the variable, opt-in on a get, cleaned on the server and cut to fit; a
   form component that shows it in a sandboxed frame.**
2. **Nothing stored: the form fetches the mail live** from the mailbox when the task is
   opened, through a task-bound route and the Worker.
3. **form-js's HTML view with a raw insert** — patch the escaping away for one property.
4. **Keep the text only** (as ADR-0438 has it).

## Decision outcome

Chosen option: **1**. Option 2 stores nothing, which is its strength, but it needs a
query path through a Worker Instance and an authorization rule for a mailbox the task's
holder has no right to, and the mail may have been moved or deleted by the time somebody
opens the task. Option 3 puts foreign markup into the page's DOM. Option 4 is the
problem this record exists for.

**Reading.**

- `includeBody="html"` on a get reads the text *and* the HTML body. It is refused on a
  list: a list answers up to a hundred messages into one variable, and the one message a
  process needs is read with a get. The Modeler's *Text* select offers it on *Read
  message* only, and a get turned into a list keeps the text and drops the HTML.
- Each provider reads the HTML part: IMAP fetches it (up to 1 MiB on the wire), Gmail
  decodes it like the text part, Graph is asked for the body as it keeps it — HTML for an
  HTML mail — and the text is derived from it, as before. A provider's mailbox offers it
  through `HTMLMailbox.GetHTML`, beside the unchanged `Mailbox.Get`.
- **One place cleans every HTML body** before it becomes a variable, whichever mailbox
  read it (`fitHTML`, `htmlclean.go`): it is parsed and rendered back, never edited as a
  string. Dropped with their content: `script`, `noscript`, `template`, `iframe`,
  `frame`, `object`, `embed`, `applet`, `base`, `link`, `meta`, `svg`, `math`, media
  elements, form controls. Unwrapped, keeping their content: `form`, `button`,
  `fieldset`, `label`. Removed attributes: every `on…` handler, `srcdoc`, `srcset`,
  `ping`, addresses with a scheme other than `http`, `https`, `mailto`, `tel`, `cid` (and
  `data:image/…` on an image), and styles that try to run something. Comments go. Links
  open in a new tab, `rel="noopener noreferrer"`. Styles stay: a mail is laid out with
  them.
- The cleaned HTML is cut at 256 KiB, then cut further until the whole answer, measured
  as stored, fits the variable budget; a cut falls before a tag and the result is parsed
  again, so it is a whole document. `htmlTruncated` says so. The text is never what gives
  way.
- A Worker Instance receives `includeHtml` in the job; one older than the field ignores it
  and answers the text, which is what such a task meant before.

**Showing.**

- A form component of Atlas's own, type `mailview` (*E-mail view* in the form editor's
  palette, `api/web/vendor/form-js/src/atlas/mailview.jsx`). It names the variable by its
  `key`, like a field, and shows the envelope as text, the HTML version or the text
  version (a toggle when there are both), and notes when either was cut.
- It is **not keyed** for form-js, so form-js never submits it: completing the task does
  not rewrite the mail. On the server its key **counts as a binding the form reads** —
  the task's holder may read that variable — and **not as one it writes**: a public start
  form does not let a caller set it.
- The HTML is shown in an `<iframe sandbox="allow-popups allow-popups-to-escape-sandbox">`
  with `srcdoc`: no script, no same origin, no forms, no navigation of the page; a link
  opens a new tab. The frame's document starts with its own Content-Security-Policy —
  `default-src 'none'; style-src 'unsafe-inline'; img-src data:` — and
  `referrer: no-referrer`. **Remote images are blocked**; a note says why, and *Load
  images* re-renders the frame with `img-src` widened to `https:` and `http:`. Fonts and
  everything else stay blocked; the page's own address is never sent as a referrer.
- The page passes `atlasLocale`; the Tasks app passes its own, and the view is German
  without one.

### Consequences

- **Positive:** a person sees a received mail as it was sent, safely, in the task that
  is about it; nothing the mail carries runs or phones home until asked; the variable
  carries no script; one cleaner for every provider; the form never writes the mail back.
- **Negative / trade-offs accepted:** the HTML is in the log and the backups, as the text
  already was — opt-in, per task; inline images referenced by `cid:` are not shown,
  because attachments are never read into the envelope; the frame has a fixed, resizable
  height, since a sandboxed frame cannot be measured; a very large mail is shown cut.
- **Follow-ups / risks to watch:** `includeHtml` on an inbound mail watch (today a watch
  delivers the text and a task reads the HTML with a get by `mail.messageId`); inline
  `cid:` images; remembering *Load images* per sender.

## Pros and cons of the options

### Option 1 — stored, cleaned, sandboxed
- Good: works with every provider and every place a form renders; the reader sees what
  the process read; isolation does not depend on the cleaning, and the cleaning does not
  depend on the isolation.
- Bad: the HTML is persisted; a budget has to cut large mails.

### Option 2 — fetched live
- Good: nothing persisted; always the full mail.
- Bad: a query path through a Worker Instance and a new authorization rule for a mailbox
  the reader may not hold; the mail may be gone; a task opened offline from the mailbox
  shows nothing.

### Option 3 — form-js HTML view, raw
- Good: no new component.
- Bad: foreign markup in the page's own DOM; form-js would have to be patched, which the
  vendoring rules forbid.

### Option 4 — text only
- Good: smallest, already built.
- Bad: what was asked for is not possible.

## Links

- extends [ADR-0438](0438-mailbox-worker.md) (what a message exposes) and
  [ADR-0478](0478-mail-documents-leave-the-mailbox.md) (documents stay out of variables)
- adds a component to the forms of [ADR-0028](0028-forms-and-the-tasks-app.md), vendored
  as [ADR-0473](0473-form-remote-data-sources.md) vendors its select
- the rendering posture of the outbox ([ADR-0150](0150-preview-mail-provider-and-visible-incidents.md))
  and of uploaded images ([ADR-0148](0148-org-wide-brand-logo.md))
