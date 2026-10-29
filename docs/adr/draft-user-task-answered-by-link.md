# ADR-DRAFT: A user task can be answered through a link a mail carries

- **Status:** Accepted
- **Implementation:** Landed
- **Date:** 2026-10-29
- **Deciders:** Atlas maintainers
- **Open question:** An answer through the link proves that whoever sent it held the
  mail, and nothing about who they are: a forwarded mail forwards the right to answer.
  For a customer approving a repair estimate that is the bar a mail sets anyway. Whether
  some tasks need more — a code sent on a second channel, or a signed reply
  ([ADR-0504](0504-what-a-mail-reply-proves.md)) — is not settled here.
- **Question checked:** 2026-10

## Context and problem statement

A garage asks its customer to approve a repair estimate, and waits. The customer has no
account and never will: they get a mail and answer it. Atlas could send the mail (the
mail Worker, ADR-0079) and wait for an answer (a receive task), but nothing carried the
answer from the customer back into the instance:

- A public start link (ADR-0029) starts a new instance; it cannot answer a running one.
- A public wizard (ADR-0449) continues a running instance, but its pass is minted by a
  public start and handed to the visitor who started it. Somebody a process reaches
  later has no pass.
- A reply to the mail reaches a watched mailbox (ADR-0438), but routing a reply to the
  instance that asked is only proposed ([ADR-0503](0503-mail-reply-routing.md)), and
  reading "yes" out of free text is a guess.
- The approvals Atlas ships link into the inbox (ADR-0394), which needs an account.

The question: how does a person without an account answer one task of a running
instance, from a mail, safely?

## Decision drivers

- An answer goes through the task's form, so what may be answered is what the model
  says, validated like any other completion.
- Nothing a link scanner does can answer: Microsoft Defender's Safe Links, Gmail and
  many gateways open every link in a mail before the person does.
- No secret in a variable, an event, a log line or a Referer: operators read variables,
  the log is kept, proxies log paths.
- No engine change: one job per element instance, applyToState untouched (I4), nothing
  new on the hot path (I1).
- The mail stays a mail task: templates (ADR-0487), HTML (ADR-0489), retries and
  incidents, the preview outbox (ADR-0150), the Worker split (ADR-0168).

## Considered options

1. **A user task that opts in, and a mail task that carries its link.** The user task says
   `atlas:answeredBy="link"`; a mail task names it with `answerTask` and shows the link
   the engine hands it as `answerLink`.
2. **A "mail user task": one element that sends its own mail and waits for the answer.**
3. **The form embedded in the mail**, posting straight to Atlas.
4. **Answer by reply**, routed by a token in the headers (ADR-0503) and read by a model.

## Decision outcome

Chosen option: **1**.

### The model

```xml
<bpmn:serviceTask id="t_kostenvoranschlag" name="Kostenvoranschlag senden">
  <bpmn:extensionElements>
    <atlas:mailConnector connector="mail" to="=kundeEmail" answerTask="t_kundenfreigabe"
      subject="=&quot;Kostenvoranschlag &quot; + auftragsNr"
      body="=&quot;Bitte hier freigeben: &quot; + answerLink"/>
  </bpmn:extensionElements>
</bpmn:serviceTask>
<bpmn:userTask id="t_kundenfreigabe" name="Kostenvoranschlag freigeben" atlas:answeredBy="link">
  <bpmn:extensionElements><zeebe:formDefinition formId="garage-kostenfreigabe"/></bpmn:extensionElements>
</bpmn:userTask>
```

- **`atlas:answeredBy="link"` on a user task** is the opt-in: the task may be answered by
  whoever holds its link. Nothing else changes about it — it is a job like every user
  task, it may name an assignee or candidate groups, and then it also stands in their
  inbox, so a customer who phones can be answered there. The first answer completes it.
- **`answerTask` on a mail task** names that user task. When the mail is resolved, the
  variables its subject and bodies read carry one more, `answerLink`, for that task in
  this instance — for that resolution only, never written to the instance.
- **The mail may go before the task or beside it.** The link names the task by its id
  and the scope instance the mail is sent from, not by a job, so it exists before the
  task opens: a mail in front of the task (send, then wait) and one on a parallel branch
  (the shipped approvals' shape) both work, and a reminder — a mail task on a
  non-interrupting timer of the task — carries the same link.
- **The scope tells runs apart.** Two runs of a non-interrupting event subprocess — two
  findings reported back to back — each mail the link of their own task: the link of the
  first opens the first run's task and not the second's. A link opens a task in the scope
  it was sent from or within it, and the compiler refuses a mail that names a task
  outside its scope.

### The link

```
link  = {external URL}/public/answer#{instance key}.{scope key}.{task id, base64url}.{mac}
mac   = HMAC-SHA256(K, instance key ‖ scope key ‖ task id)[:16], hex
K     = the vault's key derived for "atlas/answer-link/v1"
```

The scope key is the element instance the mail task ran in: the instance itself at the
root, one run of an event subprocess inside it.

- **K** is derived from the vault's master key for this purpose alone
  (`Vault.DerivedKey("atlas/answer-link/v1")`, an HMAC of an HMAC of the master key) and
  stored nowhere. A named vault secret was the other choice and was rejected: an
  administrator writes named secrets, and one who set this one could forge links. Nothing
  per link is stored either: the link is a function of K and two names, so the
  at-least-once resend of a mail (ADR-0079) writes the same link, and a check recomputes
  it. Rotating the master key invalidates every link at once; there is no per-link
  revocation, and none is needed while a link opens only an open task. A server run with
  `--vault=false` mints no links, and its mail tasks fail saying so.
- **The secret is in the fragment.** A browser never sends a fragment, so the token is in
  no access log, no proxy log and no Referer; the page reads it and sends it in an
  Authorization header, as a wizard's pass travels.
- **The origin is the operator's** (`--external-url`). A link built from whatever host a
  request came in on could carry an internal address to somebody who cannot reach it —
  the rule the approval notifications follow. Without one the mail task fails, saying so.
- **A link opens one thing:** the open job of that user task, in that scope of that
  instance, while the task is marked `answeredBy="link"` in the version the instance
  runs. Two open ones in the scope — two tokens reached the task — answer the one
  activated first. Every other case
  — a wrong mac, a finished instance, a task not open yet or any more — answers 404
  alike, and the page says there is nothing to answer right now.

### The page

`GET /public/answer` is a static page. It asks `GET /public/answer/task` with the token
and shows the task's name, its documentation and its form, prefilled with the values the
instance holds under the form's own fields (ADR-0275's rule, as for a public wizard's
screen). Submitting posts to `POST /public/answer/task/{taskKey}/complete`: only the
form's fields are accepted, personal values are sealed at that edge (ADR-0314), and the
job is completed the way the Tasks app completes it. **Opening the link never answers**:
a scanner that fetches it gets a page and a 200, and completes nothing. All three routes
are rate limited per address like every public route.

### Rules (stage 5)

| Rule | Severity | When |
|---|---|---|
| `answer.value` | error | `atlas:answeredBy` is not `link` |
| `answer.form` | error | a task answered by link has no form |
| `answer.multi-instance` | error | a task answered by link is multi-instance — one link per task cannot say which |
| `answer.task` | error | `answerTask` names no user task answered by link, or one outside the scope the mail is sent from |
| `answer.operation` | error | `answerTask` on a mailbox operation other than send |
| `answer.unshown` | error | a mail task names a task to answer and none of subject, body and HTML body reads `answerLink` |
| `answer.unsent` | warning | a task answered by link that no mail task names |

They are stage-5 problems, so a definition deployed before them is reloaded and reported,
never refused at boot.

### Consequences

- **Positive:** a customer, a supplier or an applicant answers one step of a running
  process from a mail, through a form, without an account; the diagram shows the mail
  and the step it asks for. No engine change, no new event, no new record.
- **Positive:** a link scanner cannot answer, and the token is in no log.
- **Negative / trade-offs accepted:** whoever holds the mail can answer (the open
  question). A loop that reopens the task reopens the old link too: a mail from the
  first round answers the second. A model that must tell rounds apart sends a new mail
  per round; the page shows what the instance holds now, not what the old mail said.
- **Negative / trade-offs accepted:** a link has no expiry of its own. A task bounded by
  a timer boundary closes its link with it, and that is how a model says how long an
  answer is accepted.
- **Follow-ups:** the Tasks app could mark a task answered by link; ADR-0504's signed
  reply could become `atlas:answeredBy="reply"`.

## Pros and cons of the options

### Option 1 — opt-in task, mail task carries the link
- Good: every mail feature applies unchanged; a reminder is one more mail task; the
  engine's one-job-per-element model holds.
- Bad: two elements where an author might think of one.

### Option 2 — one element that sends and waits
- Good: one shape on the canvas.
- Bad: a user task would own a second job — the mail — and every path that finds "the
  job of this element" (completion, incidents, retries, resolve, migration) would have to
  learn which one it means. A failed mail would raise an incident on a user task. Reminders
  would need a third mechanism.

### Option 3 — the form in the mail
- Good: no page to open.
- Bad: Outlook does not submit forms, Gmail strips them, Apple Mail warns; a form posting
  from a mail carries its token in the mail's HTML, where a scanner posts too.

### Option 4 — answer by reply
- Good: the most natural gesture.
- Bad: routing is not built (ADR-0503); a model reading "ja" out of a reply guesses;
  what a reply proves is open (ADR-0504). It stays the right follow-up for free-text
  answers.

## Links

- relates to ADR-0029 (public start links), ADR-0079 (mail), ADR-0150 (preview outbox),
  ADR-0168 (Resolve/Run split), ADR-0275 (what a holder sees), ADR-0314 (personal data),
  ADR-0394 (approval in the inbox), ADR-0438 (mailbox Worker), ADR-0449 (public wizard
  pass), ADR-0487 (mail templates), ADR-0503 (reply routing), ADR-0504 (what a reply
  proves)
