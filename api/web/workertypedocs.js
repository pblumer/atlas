// What it takes to make one Worker Type actually work, written down where the choice
// is made: in the service task's properties panel, beside the type an author just
// picked, and in the Console dialog where an operator fills the fields in.
//
// The catalog in editor.js says what a task *states* (a target, an operation, a result
// variable). It deliberately says nothing about what has to exist outside Atlas first —
// an app registration, a service account key, a shared spreadsheet — and that is the
// part a new installation stumbles over: the model is right, the task is right, and
// nothing happens because a checkbox at the provider is missing. That knowledge existed
// only in the handbook, which is another page, in another tab, that an author has to
// know to open.
//
// So each Worker Type carries it here: a one-line `needs` (always visible, it answers
// "does this need a Worker record and a credential at all?"), the ordered `steps` to
// get there, the one `trap` that catches people, and the `anchor` of the handbook
// card that says the same thing at length — the handbook is served by this same
// server (/handbuch.html), so the link works on an installation with no internet.
//
// One entry per catalog id, including the ids that need nothing: "nothing to configure"
// is an answer, and leaving it unsaid is what makes someone go looking for a dialog
// that does not exist. api/workertypedocs_internal_test.go holds catalog, docs and
// handbook to each other, so a new Worker Type cannot ship without its setup.
//
// The strings are markup and are rendered as such. They are static module content —
// never a value from a model, a record or a request — which is why this is the one
// place in the panel that does not escape what it renders.
//
// Every entry carries a `checked` date, and the panel prints it. These steps name menu
// paths in somebody else's product — *IAM & Admin → Service accounts* — which is what
// makes them worth writing and what will silently stop being true when that product is
// rearranged. Nothing here can detect that: the text goes on looking authoritative, and
// the person it misleads is the one least able to tell whether they or the instructions
// are wrong. The date does not fix that; it hands the reader the one fact that lets them
// judge it for themselves, and it gives the staleness a name a test can hold
// (TestSetupDocsAreRecentlyChecked).

import { t } from "./i18n.js";

// HANDBOOK_URL is the manual this server serves itself (api/web/handbuch.html). A
// relative URL on purpose: an air-gapped installation has the same handbook as a
// public one, and neither reaches an external docs site.
const HANDBOOK_URL = "/handbuch.html";

// The vault is the same sentence in a dozen entries, so it is written once. Console ›
// Secrets holds the value; the model and the Worker record hold only its name (I6).
const VAULT = "workerDoc.vault";
const WORKERS = "workerDoc.workers";

// WORKER_TYPE_DOCS is keyed by the SERVICE_TASK_KINDS id, so the panel looks its entry
// up by exactly what the author picked.
//
// - `anchor`  the handbook section or card id this links to.
// - `title`   what the handbook calls it; the link says so rather than "Handbook".
// - `checked` YYYY-MM: when these steps were last read against the real thing.
// - `needs`   one line, always visible: what must exist before a task of this type runs.
// - `steps`   the ordered setup, provider first, Atlas second, proof last.
// - `trap`    the failure this type is actually reported with — omitted where there is none.
// - `reference` optional { anchor, title }: a handbook card on what the tasks *do* — every
//             operation, its result, the error codes a boundary can catch — linked beside
//             the setup card, for a type whose operations need more than the field hints.
export const WORKER_TYPE_DOCS = {
  worker: {
    anchor: "runbook-jobworker", title: "workerDoc.worker.title",
    checked: "2026-09",
    needs: "workerDoc.worker.needs",
    steps: [
      "workerDoc.worker.step1",
      "workerDoc.worker.step2",
      "workerDoc.worker.step3",
      "workerDoc.worker.step4",
    ],
    trap: "workerDoc.worker.trap",
  },

  rest: {
    anchor: "runbook-rest", title: "workerDoc.rest.title",
    checked: "2026-09",
    needs: "workerDoc.rest.needs",
    steps: [
      "workerDoc.rest.step1",
      "workerDoc.rest.step2",
      "workerDoc.rest.step3",
      "workerDoc.rest.step4",
    ],
    trap: "workerDoc.rest.trap",
  },

  scim: {
    anchor: "runbook-scim", title: "workerDoc.scim.title",
    checked: "2026-09",
    needs: "workerDoc.scim.needs",
    steps: [
      "workerDoc.scim.step1",
      "workerDoc.scim.step2",
      "workerDoc.scim.step3",
      "workerDoc.scim.step4",
    ],
    trap: "workerDoc.scim.trap",
  },

  ldap: {
    anchor: "runbook-ldap", title: "workerDoc.ldap.title",
    checked: "2026-09",
    needs: "workerDoc.ldap.needs",
    steps: [
      "workerDoc.ldap.step1",
      "workerDoc.ldap.step2",
      "workerDoc.ldap.step3",
      "workerDoc.ldap.step4",
    ],
    trap: "workerDoc.ldap.trap",
  },

  soap: {
    anchor: "runbook-soap", title: "workerDoc.soap.title",
    checked: "2026-09",
    needs: "workerDoc.soap.needs",
    steps: [
      "workerDoc.soap.step1",
      "workerDoc.soap.step2",
      "workerDoc.soap.step3",
      "workerDoc.soap.step4",
    ],
    trap: "workerDoc.soap.trap",
  },

  ad: {
    anchor: "runbook-ad", title: "workerDoc.ad.title",
    checked: "2026-09",
    needs: "workerDoc.ad.needs",
    steps: [
      "workerDoc.ad.step1",
      "workerDoc.ad.step2",
      "workerDoc.ad.step3",
      "workerDoc.ad.step4",
    ],
    trap: "workerDoc.ad.trap",
  },

  ldif: {
    // Short name in the label, like Google Sheets: "(LDIF/DSML)" is a second line in a
    // 270px panel, and the card behind the link names both formats in its first sentence.
    anchor: "runbook-ldif", title: "workerDoc.ldif.title",
    checked: "2026-09",
    needs: "workerDoc.ldif.needs",
    steps: [
      "workerDoc.ldif.step1",
      "workerDoc.ldif.step2",
      "workerDoc.ldif.step3",
    ],
  },

  entra: {
    anchor: "runbook-entra", title: "workerDoc.entra.title",
    checked: "2026-09",
    needs: "workerDoc.entra.needs",
    steps: [
      "workerDoc.entra.step1",
      "workerDoc.entra.step2",
      "workerDoc.entra.step3",
      "workerDoc.entra.step4",
      "workerDoc.entra.step5",
    ],
    trap: "workerDoc.entra.trap",
  },

  exchange: {
    anchor: "runbook-exchange", title: "workerDoc.exchange.title",
    checked: "2026-10",
    needs: "workerDoc.exchange.needs",
    steps: [
      "workerDoc.exchange.step1",
      "workerDoc.exchange.step2",
      "workerDoc.exchange.step3",
      "workerDoc.exchange.step4",
      "workerDoc.exchange.step5",
      "workerDoc.exchange.step6",
      "workerDoc.exchange.step7",
    ],
    trap: "workerDoc.exchange.trap",
  },

  teamsphone: {
    anchor: "runbook-teamsphone", title: "workerDoc.teamsphone.title",
    checked: "2026-10",
    needs: "workerDoc.teamsphone.needs",
    steps: [
      "workerDoc.teamsphone.step1",
      "workerDoc.teamsphone.step2",
      "workerDoc.teamsphone.step3",
      "workerDoc.teamsphone.step4",
      "workerDoc.teamsphone.step5",
      "workerDoc.teamsphone.step6",
    ],
    trap: "workerDoc.teamsphone.trap",
    reference: { anchor: "ref-teamsphone", title: "workerDoc.teamsphone.refTitle" },
  },

  // The three database entries quote the connection string exactly as the Console's own
  // field placeholder does, `PASSWORT` included (SQL_DSN_EXAMPLES in workerdialog.js):
  // an operator reads the example here and types into the field there, and two spellings
  // of the same example is one more thing to wonder about.
  mssql: {
    anchor: "runbook-mssql", title: "workerDoc.mssql.title",
    checked: "2026-09",
    needs: "workerDoc.mssql.needs",
    steps: [
      "workerDoc.mssql.step1",
      "workerDoc.mssql.step2",
      "workerDoc.mssql.step3",
      "workerDoc.mssql.step4",
    ],
    trap: "workerDoc.sql.trap",
  },

  mariadb: {
    anchor: "runbook-mariadb", title: "workerDoc.mariadb.title",
    checked: "2026-09",
    needs: "workerDoc.mariadb.needs",
    steps: [
      "workerDoc.mariadb.step1",
      "workerDoc.mariadb.step2",
      "workerDoc.mariadb.step3",
      "workerDoc.sql.mock",
    ],
    trap: "workerDoc.sql.trap",
  },

  postgres: {
    anchor: "runbook-postgres", title: "workerDoc.postgres.title",
    checked: "2026-09",
    needs: "workerDoc.postgres.needs",
    steps: [
      "workerDoc.postgres.step1",
      "workerDoc.postgres.step2",
      "workerDoc.postgres.step3",
      "workerDoc.sql.mock",
    ],
    trap: "workerDoc.sql.trap",
  },

  clio: {
    anchor: "runbook-clio", title: "workerDoc.clio.title",
    checked: "2026-09",
    needs: "workerDoc.clio.needs",
    steps: [
      "workerDoc.clio.step1",
      "workerDoc.clio.step2",
      "workerDoc.clio.step3",
      "workerDoc.clio.step4",
    ],
  },

  // temis is not a service-task kind — it is the business rule task's other decision
  // binding — but it is a Worker an operator configures in the same Console dialog as
  // the rest, so it carries the same entry. The panel that offers it renders this too.
  temis: {
    anchor: "runbook-temis", title: "workerDoc.temis.title",
    checked: "2026-09",
    needs: "workerDoc.temis.needs",
    steps: [
      "workerDoc.temis.step1",
      "workerDoc.temis.step2",
      "workerDoc.temis.step3",
      "workerDoc.temis.step4",
    ],
  },

  mail: {
    anchor: "runbook-mail", title: "workerDoc.mail.title",
    checked: "2026-09",
    needs: "workerDoc.mail.needs",
    steps: [
      "workerDoc.mail.step1",
      "workerDoc.mail.step2",
      "workerDoc.mail.step3",
      "workerDoc.mail.step4",
      "workerDoc.mail.step5",
      "workerDoc.mail.step6",
      "workerDoc.mail.step7",
      "workerDoc.mail.step8",
      "workerDoc.mail.step9",
    ],
    trap: "workerDoc.mail.trap",
  },

  csv: {
    anchor: "runbook-csv", title: "workerDoc.csv.title",
    checked: "2026-09",
    needs: "workerDoc.csv.needs",
    steps: [
      "workerDoc.csv.step1",
      "workerDoc.csv.step2",
      "workerDoc.csv.step3",
    ],
    trap: "workerDoc.csv.trap",
  },

  sharepoint: {
    anchor: "runbook-sharepoint", title: "workerDoc.sharepoint.title",
    checked: "2026-09",
    needs: "workerDoc.sharepoint.needs",
    steps: [
      "workerDoc.sharepoint.step1",
      "workerDoc.sharepoint.step2",
      "workerDoc.sharepoint.step3",
      "workerDoc.sharepoint.step4",
      "workerDoc.sharepoint.step5",
      "workerDoc.sharepoint.step6",
    ],
    trap: "workerDoc.sharepoint.trap",
  },

  remedy: {
    anchor: "runbook-remedy", title: "workerDoc.remedy.title",
    checked: "2026-09",
    needs: "workerDoc.remedy.needs",
    steps: [
      "workerDoc.remedy.step1",
      "workerDoc.remedy.step2",
      "workerDoc.remedy.step3",
      "workerDoc.remedy.step4",
    ],
    trap: "workerDoc.remedy.trap",
  },

  jira: {
    anchor: "runbook-jira", title: "workerDoc.jira.title",
    checked: "2026-09",
    needs: "workerDoc.jira.needs",
    steps: [
      "workerDoc.jira.step1",
      "workerDoc.jira.step2",
      "workerDoc.jira.step3",
      "workerDoc.jira.step4",
      "workerDoc.jira.step5",
    ],
    trap: "workerDoc.jira.trap",
  },

  googlesheets: {
    // The link label is the short name, not the handbook card's full title ("Google
    // Sheets & Drive"): at 270px that one wrapped onto a second line, and the card it
    // opens says the rest.
    anchor: "runbook-googlesheets", title: "workerDoc.googlesheets.title",
    checked: "2026-09",
    needs: "workerDoc.googlesheets.needs",
    steps: [
      "workerDoc.googlesheets.step1",
      "workerDoc.googlesheets.step2",
      "workerDoc.googlesheets.step3",
      "workerDoc.googlesheets.step4",
      "workerDoc.googlesheets.step5",
    ],
    trap: "workerDoc.googlesheets.trap",
  },

  googlecontacts: {
    anchor: "runbook-googlecontacts", title: "workerDoc.googlecontacts.title",
    checked: "2026-10",
    needs: "workerDoc.googlecontacts.needs",
    steps: [
      "workerDoc.googlecontacts.step1",
      "workerDoc.googlecontacts.step2",
      "workerDoc.googlecontacts.step3",
      "workerDoc.googlecontacts.step4",
      "workerDoc.googlecontacts.step5",
      "workerDoc.googlecontacts.step6",
    ],
    trap: "workerDoc.googlecontacts.trap",
  },

  carddav: {
    anchor: "runbook-carddav", title: "workerDoc.carddav.title",
    checked: "2026-10",
    needs: "workerDoc.carddav.needs",
    steps: [
      "workerDoc.carddav.step1",
      "workerDoc.carddav.step2",
      "workerDoc.carddav.step3",
      "workerDoc.carddav.step4",
    ],
    trap: "workerDoc.carddav.trap",
  },

  wikiwikibangbang: {
    anchor: "runbook-wikiwikibangbang", title: "wikiwikibangbang",
    checked: "2026-10",
    needs: `A configured wikiwikibangbang Worker in ${WORKERS} per wiki: the wiki's base URL, and a vault bundle with the API token of a technical account of its own — an editor in the spaces processes write to, with no system role. <i>Walked against wikiwikibangbang run locally at commit 928fec0.</i>`,
    steps: [
      `In the wiki, an administrator creates a <b>technical account</b> for Atlas, e.g. <code>atlas-bot</code>, and grants it the role <b>editor</b> in each space processes write to — and nothing else: an account with the system role <b>admin</b> or <b>erasure</b> is refused. It is not the read-only account the catalogue reads the wiki with.`,
      `Signed in as that account, create an <b>API token</b> of scope <b>full</b> under <i>Account</i> &rarr; <b>API tokens</b> (at most 366 days), and note its expiry: the token stops working then, and the Worker with it. A Worker that only reads may use a token of scope <b>read</b>.`,
      `Store the bundle in the vault: ${VAULT}, e.g. <code>wiki_bot</code> holding <code>{"token": "wwbb_…"}</code>.`,
      `${WORKERS} &rarr; <b>New worker</b>: type <b>wikiwikibangbang</b>, a name, the wiki's base URL as endpoint (<code>https://wiki.example.ch</code>), credential reference <code>wiki_bot</code>. One Worker per wiki and account.`,
    ],
    trap: `Everything the Worker writes is written as its account: the wiki's history and comments name <i>atlas-bot</i>, not the person behind the process. And a user property accepts any text — the wiki does not check that <code>assignee</code> names an account, so a misspelt username assigns the task to nobody, silently.`,
  },

  discord: {
    anchor: "runbook-discord", title: "workerDoc.discord.title",
    checked: "2026-09",
    needs: "workerDoc.discord.needs",
    steps: [
      "workerDoc.discord.step1",
      "workerDoc.discord.step2",
      "workerDoc.discord.step3",
      "workerDoc.discord.step4",
      "workerDoc.discord.step5",
    ],
    trap: "workerDoc.discord.trap",
  },

  cloudevents: {
    anchor: "runbook-cloudevents", title: "workerDoc.cloudevents.title",
    checked: "2026-10",
    needs: "workerDoc.cloudevents.needs",
    steps: [
      "workerDoc.cloudevents.step1",
      "workerDoc.cloudevents.step2",
      "workerDoc.cloudevents.step3",
      "workerDoc.cloudevents.step4",
    ],
    trap: "workerDoc.cloudevents.trap",
  },

  s3: {
    anchor: "runbook-s3", title: "workerDoc.s3.title",
    checked: "2026-09",
    needs: "workerDoc.s3.needs",
    steps: [
      "workerDoc.s3.step1",
      "workerDoc.s3.step2",
      "workerDoc.s3.step3",
      "workerDoc.s3.step4",
      "workerDoc.s3.step5",
    ],
    trap: "workerDoc.s3.trap",
  },

  aitask: {
    anchor: "runbook-ai", title: "workerDoc.aitask.title",
    checked: "2026-09",
    needs: "workerDoc.aitask.needs",
    steps: [
      "workerDoc.aitask.step1",
      "workerDoc.aitask.step2",
      "workerDoc.aitask.step3",
      "workerDoc.aitask.step4",
    ],
    trap: "workerDoc.aitask.trap",
  },

  webscrape: {
    anchor: "runbook-webscrape", title: "workerDoc.webscrape.title",
    checked: "2026-09",
    needs: "workerDoc.webscrape.needs",
    steps: [
      "workerDoc.webscrape.step1",
      "workerDoc.webscrape.step2",
      "workerDoc.webscrape.step3",
    ],
    trap: "workerDoc.webscrape.trap",
  },

  userconnector: {
    anchor: "runbook-userprov", title: "workerDoc.userconnector.title",
    checked: "2026-09",
    needs: "workerDoc.userconnector.needs",
    steps: [
      "workerDoc.userconnector.step1",
      "workerDoc.userconnector.step2",
      "workerDoc.userconnector.step3",
      "workerDoc.userconnector.step4",
    ],
    trap: "workerDoc.userconnector.trap",
  },

  mockup: {
    anchor: "runbook-mockup", title: "workerDoc.mockup.title",
    checked: "2026-09",
    needs: "workerDoc.mockup.needs",
    steps: [
      "workerDoc.mockup.step1",
      "workerDoc.mockup.step2",
      "workerDoc.mockup.step3",
      "workerDoc.mockup.step4",
    ],
  },
};

// WORKER_KIND_TO_DOC maps a *configured Worker's* kind — the server's word, as the
// Console and the worker store use it — onto the catalog id whose setup it is. Almost
// all of them are the same string; the exceptions are the ones where the catalog is
// named after the task and the record after the capability.
const WORKER_KIND_TO_DOC = { agent: "aitask" };

// docForWorkerKind answers the Console's question ("how is a worker of this kind set
// up?") with the catalog's answer, so the dialog and the panel cannot drift apart.
export function docForWorkerKind(kind) {
  const id = WORKER_KIND_TO_DOC[kind] || kind;
  return WORKER_TYPE_DOCS[id] || null;
}

// handbookHref is the link to one Worker Type's handbook card, on this server.
export function handbookHref(doc) {
  return doc && doc.anchor ? `${HANDBOOK_URL}#${doc.anchor}` : HANDBOOK_URL;
}

// workerTypeDocHTML renders the setup block for one catalog kind: the always-visible
// line (what this needs, and the handbook link), then the steps folded away.
//
// The steps are collapsed because the panel is 270px wide and an author who has already
// configured this type wants the fields, not the recipe. The `needs` line and the link
// are not collapsed, because they are the two things someone who has *not* configured it
// has to see without knowing to look.
export function workerTypeDocHTML(id) {
  return renderDoc(WORKER_TYPE_DOCS[id]);
}

// workerKindDocHTML renders the same block for a *configured Worker's* kind, which is
// what the Console dialog has in its hand. Same text either way: an operator filling in
// the credential reference and an author picking the type are asking one question.
export function workerKindDocHTML(kind) {
  return renderDoc(docForWorkerKind(kind));
}

// monthName spells a YYYY-MM date out ("September 2026"). Written out rather than
// printed as 09/2026, which reads as a day and a month to half the world.
const MONTHS = ["workerDoc.month.january", "workerDoc.month.february", "workerDoc.month.march", "workerDoc.month.april", "workerDoc.month.may", "workerDoc.month.june",
  "workerDoc.month.july", "workerDoc.month.august", "workerDoc.month.september", "workerDoc.month.october", "workerDoc.month.november", "workerDoc.month.december"];

function monthName(yyyymm) {
  const [year, month] = String(yyyymm).split("-");
  const name = MONTHS[Number(month) - 1];
  return name ? `${t(name)} ${year}` : String(yyyymm);
}

// renderDoc is the one markup for both surfaces; a Worker Type nobody documented
// renders nothing rather than an empty box (the Go guard makes sure there is none).
function renderDoc(doc) {
  if (!doc) return "";
  const places = { vault: t(VAULT), workers: t(WORKERS) };
  const steps = (doc.steps || []).map((s) => `<li>${t(s, places)}</li>`).join("");
  const trap = doc.trap ? `<p class="wtdoc-trap"><b>${t("workerDoc.watchOut")}</b> ${t(doc.trap, places)}</p>` : "";
  // Inside the fold on purpose: it qualifies the steps, and the reader deciding how far
  // to trust them is the reader who has opened them.
  const checked = doc.checked
    ? `<p class="wtdoc-checked">${t("workerDoc.checked", { date: `<time datetime="${doc.checked}">${monthName(doc.checked)}</time>` })}</p>`
    : "";
  return `<div class="wtdoc">
    <p class="wtdoc-needs">${t(doc.needs, places)}</p>
    <details class="wtdoc-more">
      <summary>${t("workerDoc.howTo")}</summary>
      <ol class="wtdoc-steps">${steps}</ol>
      ${trap}
      ${checked}
    </details>
    <a class="wtdoc-link" href="${handbookHref(doc)}" target="_blank" rel="noopener"
       title="${t("workerDoc.handbookTitle")}">${t("workerDoc.handbook", { title: t(doc.title) })} <span class="ext" aria-hidden="true">&#8599;</span></a>
    ${doc.reference ? `<a class="wtdoc-link" href="${HANDBOOK_URL}#${doc.reference.anchor}" target="_blank" rel="noopener"
       title="${t("workerDoc.referenceTitle")}">${t("workerDoc.handbook", { title: t(doc.reference.title) })} <span class="ext" aria-hidden="true">&#8599;</span></a>` : ""}
  </div>`;
}
