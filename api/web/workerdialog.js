// The worker configuration dialog, in one place (ADR-0160). A configured worker is
// reconfigured from two very different places — Console › Workers, where an operator
// is administering integrations, and an incident, where a token is parked *because*
// this worker did not work — and both need the same fields, the same per-type rules
// about which of them apply, and the same "check it before you save" button. So the
// dialog lives here and both call it.
//
// It edits an existing record only. Creating a worker is a different act (it picks a
// Worker Type and a name, and the name is the binding every model references,
// ADR-0036/0041), and it keeps its inline form in the Console; what the two share is
// the shape of a Worker Type's fields, exported as workerShape so the rules cannot
// drift apart.
//
// Deleting one lives here too (ADR-0163). It is not a dialog on the same record, but
// it asks the same kind of question — what does this worker's configuration mean
// for the models that resolve through it — and putting it here is what makes it
// reachable from a test at all: app.js boots the whole console on import, so anything
// left in it is only ever exercised by hand.

import { openDialog } from "./dialog.js";

const esc = (s) => String(s).replace(/[&<>"']/g, (c) =>
  ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;", "'": "&#39;" }[c]));

// workerCreateBody builds the POST /api/v1/connectors body from the add form.
//
// It lives here rather than in app.js for the reason the delete flow does: app.js
// boots the whole console on import and cannot be exercised in isolation, and this is
// a part worth exercising. "Which fields does this create carry" is a decision, not a
// transcription — the form keeps every field in the DOM and only *hides* the ones a
// kind does not use, so a hidden field still has a value and FormData still reports it.
export function workerCreateBody(form) {
  const f = form instanceof FormData ? form : new FormData(form);
  const get = (k) => String(f.get(k) || "").trim();
  const body = {
    name: get("name"),
    kind: get("kind") || "temis",
    endpoint: get("endpoint"),
    sender: get("sender"),
    credentialsRef: get("credentialsRef"),
  };
  if (body.kind === "mail") body.provider = get("provider") || "smtp";
  // A connection string is a SQL worker's whole configuration and belongs to no
  // other kind, so the gate is the kind — not whether the field happens to hold
  // something. Asking only "is it non-empty" is what let a DSN typed for a kind picked
  // earlier, or one a password manager put into a type="password" input nobody can
  // see, refuse a jira create with a message about databases.
  if (workerShape(body.kind, body.provider).sql) {
    const conn = get("connectionString");
    if (conn) body.connectionString = conn;
  }
  return body;
}

// workerShape says which fields a worker of this type and provider actually
// uses, and what to call them. It is the one description of that: a native mail
// provider and SharePoint default their API base and authenticate with a vault
// credential bundle, SMTP needs a host:port it can dial, the preview transport dials
// nothing at all (which is the point of it, ADR-0150), and temis/clio take an
// endpoint plus an optional token reference.
//
// `credRef` is "required", "optional", or "none"; `endpoint` and `sender` are booleans.
// One connection-string example per database product, because the three share this
// form and share nothing else about a DSN. SQL Server takes a sqlserver:// URL,
// PostgreSQL a postgres:// one, and MariaDB a form that is not a URL at all — the
// MySQL driver's own `user:pass@tcp(host:port)/db`. The example is the only thing on
// screen that says which is expected, so showing one product's to another is showing
// the operator the wrong syntax and leaving the driver to reject it later, in a
// sentence that names none of the six parts a connection string has.
//
// The hosts are example.com and the password is obviously a placeholder: an example
// nobody could paste unchanged is an example nobody pastes half-changed.
const SQL_DSN_EXAMPLES = {
  mssql: "sqlserver://atlas:PASSWORT@sql.example.com:1433?database=hr",
  mariadb: "atlas:PASSWORT@tcp(mariadb.example.com:3306)/hr?parseTime=true",
  postgres: "postgres://atlas:PASSWORT@db.example.com:5432/hr?sslmode=require",
};

export function workerShape(kind, provider) {
  const mail = kind === "mail";
  const preview = mail && provider === "preview";
  const native = mail && provider !== "smtp" && !preview;
  const sharepoint = kind === "sharepoint";
  const remedy = kind === "remedy";
  // Jira is Remedy's shape exactly: a site to reach and a credential bundle to reach
  // it with, neither derivable from the other.
  const jira = kind === "jira";
  const entra = kind === "entra";
  // Google Sheets is SharePoint's shape: Google's API bases are the same for everyone,
  // so there is no endpoint to author — the credential bundle *is* the configuration.
  const googlesheets = kind === "googlesheets";
  // Active Directory is Remedy's shape: an LDAP URL to dial and a bind account to dial
  // it with, neither derivable from the other. It is the newest kind to stop carrying
  // its directory in the model (ADR-0206).
  const ad = kind === "ad";
  // The three SQL products. Their whole configuration is one secret — a connection
  // string has no public half — so there is no endpoint to author: what the Console
  // shows is a redacted label the server derived from the string itself.
  const sql = kind === "postgres" || kind === "mariadb" || kind === "mssql";
  // Kinds that default their API base and authenticate with a credential bundle
  // instead of dialing a host:port. Remedy is not one: it needs both.
  const bundle = native || sharepoint || entra || sql || googlesheets;
  return {
    mail,
    sql,
    // The example for this product's connection string, empty for a kind that has
    // none. Both the create form and the edit dialog read it from here, so the two
    // cannot disagree about what a SQL Server DSN looks like.
    dsnPlaceholder: SQL_DSN_EXAMPLES[kind] || "",
    // Which types can be checked without saving. A mail worker connects and
    // authenticates; a SQL worker dials its connection string. The rest have no
    // check yet, and the server says so by name rather than silently doing nothing.
    test: mail || sql,
    provider: mail,
    endpoint: !bundle && !preview,
    // A mail worker always has a sender: it is the default From address, and the
    // preview transport frames the message exactly as it would be sent, so it needs
    // one too.
    sender: mail,
    // A SQL worker is created by pasting the connection string, which the server
    // seals into the vault and replaces with a reference — so the reference is one of
    // two ways in, not the only one, and the form must not insist on it.
    credRef: preview ? "none" : (sql ? "optional" : (bundle || remedy || jira || ad ? "required" : "optional")),
    endpointPlaceholder: mail
      ? "smtp.office365.com:587"
      : (ad ? "ldaps://dc.example.com:636"
        : (remedy ? "https://helix.example.com:8008" : (jira ? "https://acme.atlassian.net" : "https://temis.internal"))),
    credRefLabel: ad
      ? "Credential reference (vault {bindDN, password})"
      : googlesheets
      ? "Credential reference (vault Google auth bundle)"
      : jira
      ? "Credential reference (vault {email, apiToken} or {token})"
      : remedy
      ? "Credential reference (vault {username,password})"
      : (sql ? "\u2026 or a credential reference (a vault key already holding the DSN)"
        : (entra ? "Credential reference (vault {tenantId, clientId, clientSecret})"
          : (bundle ? "Credential reference (vault auth bundle)" : "Token reference (optional)"))),
    credRefPlaceholder: ad
      ? "ad_prod_bind (vault {bindDN, password})"
      : googlesheets
      ? "google_sheets_auth (vault JSON bundle)"
      : jira
      ? "jira_acme (vault {email, apiToken} or {token})"
      : remedy
      ? "remedy_creds (vault {username,password})"
      : (sql ? kind + "_hr_dsn (a vault key holding the whole connection string)"
        : (entra ? "entra_blumer (vault {tenantId, clientId, clientSecret})"
          : (sharepoint ? "sharepoint_auth (vault JSON bundle)" : (native ? "gmail_auth (vault JSON bundle)" : "risk_token")))),
    hint: googlesheets
      ? "The credential reference names a JSON auth bundle in the vault \u2014 never a secret value. A <b>service account</b> is the normal shape: <code>{\"method\": \"serviceAccount\", \"clientEmail\": \"\u2026@\u2026.iam.gserviceaccount.com\", \"privateKey\": \"-----BEGIN PRIVATE KEY-----\u2026\"}</code>, copied out of the JSON key file Google hands out. A service account owns nothing by itself: <b>share each spreadsheet or folder with its address</b>, exactly as you would with a colleague, or it will read a 403 where you see a document."
      : ad
      ? "The directory's <b>LDAP URL</b> and a vault bundle holding the service account: <code>{\"bindDN\": \"cn=svc-atlas,ou=Dienstkonten,dc=example,dc=com\", \"password\": \"\u2026\"}</code>. Use <b>ldaps://</b> unless you enable StartTLS \u2014 Active Directory refuses to set a password over an unencrypted channel, so an <code>ldap://</code> directory works for everything except the one thing a joiner needs. A model names this worker and says nothing else about the directory."
      : sql
      ? "The <b>whole connection string</b> is the credential \u2014 it is sealed into the vault and the record keeps only a reference, so the Console can never show it back. Atlas supervises a worker for this kind, and it picks the database up as soon as you save; no restart and no start parameter. To replace a connection string later, overwrite its vault key under <b>Secrets</b>."
      : (!mail
      ? ""
      : (preview
        ? "Needs nothing else: messages are framed exactly as they would be sent and land in <b>Operations &rsaquo; Outbox</b> instead of going out. The way to try a mail task before you own a mail server."
        : (native
          ? "The credential reference names a JSON auth bundle in the vault — never a secret value. A Google OAuth client still in <i>Testing</i> expires its refresh token after 7 days."
          : "Host and port of the submission server. Without a port, 587 is assumed (465 for <code>smtps://</code>)."))),
  };
}

const PROVIDERS = [
  ["smtp", "SMTP"],
  ["gmail", "Gmail API"],
  ["microsoft", "Microsoft Graph"],
  ["preview", "Preview (in-app outbox)"],
];

// editWorkerFlow opens the dialog on an existing worker, PATCHes what changed,
// and reports what the operator chose. `intro` is the caller's reason for opening it
// (an incident says which task is parked on this worker); `extraLabel`, when given,
// adds a second confirm button — the incident's "Save & retry", which saves and then
// wants the job tried again.
//
// Resolves {saved, extra} after a successful save, or null when nothing was written
// (cancelled, or the save failed and was reported). `okToast` is the success message;
// pass "" when the caller reports the outcome of the whole action instead.
export async function editWorkerFlow({ api, toast, worker, intro = "", extraLabel = "", okToast = "Worker updated" }) {
  const choice = await askWorker({ api, worker, intro, extraLabel });
  if (!choice) return null;
  try {
    await api("PATCH", "/api/v1/connectors/" + encodeURIComponent(worker.id), choice.patch);
  } catch (e) {
    const msg = e && e.message ? e.message : String(e);
    // Managing workers is admin-only when auth is on (ADR-0041); say that rather
    // than leaving a bare 403 to be read as "the change was wrong".
    toast(/403|forbidden/i.test(msg) ? "Changing a worker needs an admin account" : "Could not save the worker: " + msg, "warn");
    return null;
  }
  // A caller that follows the save with something else (the incident's retry) says so
  // itself, in one message, rather than stacking two toasts on one action.
  if (okToast) toast(okToast, "ok");
  return { saved: true, extra: choice.extra };
}

// askWorker renders the dialog and resolves to {patch, extra}, or null. The fields
// re-shape as the provider changes, because "which of these do I have to fill in" is a
// property of the provider and finding that out from a rejected save is a worse way to
// learn it.
function askWorker({ api, worker, intro, extraLabel }) {
  return new Promise((resolve) => {
    const c = worker || {};
    const body = document.createElement("div");
    body.innerHTML = `
          ${intro ? `<p class="inc-modal-msg">${esc(intro)}</p>` : ""}
          ${c.problem ? `<p class="conn-problem"><b>Not usable right now:</b> ${esc(c.problem)}</p>` : ""}
          <p class="muted" style="margin:0 0 10px">The name is what every model references, so it is fixed here — changing it would leave those tasks looking for a worker that no longer exists. Everything else takes effect at once; a parked task retries against the new configuration.</p>
          <div class="conn-fields">
            <label class="field"><span>Kind</span><input value="${esc(c.kind || "")}" disabled/></label>
            <label class="field conn-f-provider"><span>Provider</span><select id="conn-provider">
              ${PROVIDERS.map(([v, l]) => `<option value="${v}"${(c.provider || "smtp") === v ? " selected" : ""}>${l}</option>`).join("")}
            </select></label>
            <label class="field conn-f-endpoint" style="flex:1 1 220px"><span class="conn-endpoint-label">Endpoint</span><input id="conn-endpoint" value="${esc(c.endpoint || "")}"/></label>
            <label class="field conn-f-sender" style="flex:1 1 200px"><span>Sender</span><input id="conn-sender" value="${esc(c.sender || "")}" placeholder="bot@example.com"/></label>
            <label class="field conn-f-credref" style="flex:1 1 200px"><span class="conn-credref-label">Token reference</span><input id="conn-credref" value="${esc(c.credentialsRef || "")}"/></label>
          </div>
          <label class="conn-enabled"><input type="checkbox" id="conn-enabled"${c.enabled ? " checked" : ""}/> <span>Enabled — a disabled worker is skipped, and its tasks park</span></label>
          <p class="muted conn-hint" style="margin:8px 0 0;font-size:12.5px"></p>
          <p class="conn-test-result" style="margin:8px 0 0;font-size:12.5px" hidden></p>
`;

    const providerSel = body.querySelector("#conn-provider");
    const endpointIn = body.querySelector("#conn-endpoint");
    const senderIn = body.querySelector("#conn-sender");
    const credRefIn = body.querySelector("#conn-credref");
    const enabledIn = body.querySelector("#conn-enabled");
    const testOut = body.querySelector(".conn-test-result");

    const shape = () => workerShape(c.kind, c.kind === "mail" ? providerSel.value : c.provider);
    // The dialog, once opened. sync runs before that (to lay the fields out for the
    // kind) and again after (the Test button lives in the foot, so it only exists once
    // the dialog does) — hence the scope rather than a fixed root.
    let dlg = null;
    const sync = () => {
      const sh = shape();
      const root = dlg ? dlg.el : body;
      const show = (sel, on) => { const el = root.querySelector(sel); if (el) el.style.display = on ? "" : "none"; };
      show(".conn-f-provider", sh.provider);
      show(".conn-f-endpoint", sh.endpoint);
      show(".conn-f-sender", sh.sender);
      show(".conn-f-credref", sh.credRef !== "none");
      show(".conn-f-test", sh.test);
      endpointIn.placeholder = sh.endpointPlaceholder;
      root.querySelector(".conn-credref-label").textContent = sh.credRefLabel;
      credRefIn.placeholder = sh.credRefPlaceholder;
      root.querySelector(".conn-hint").innerHTML = sh.hint;
    };
    providerSel.addEventListener("change", sync);
    sync();

    const onTest = async (e) => {
      const btn = e.currentTarget;
      testOut.hidden = false;
      testOut.className = "conn-test-result muted";
      testOut.textContent = "Checking…";
      btn.disabled = true;
      try {
        // A SQL worker is checked through the vault reference it already has: the
        // dialog never shows a connection string back, so there is nothing typed here
        // to check instead. Which is the case that matters — an operator opens this
        // dialog because a worker *stopped* working.
        const res = await api("POST", "/api/v1/connectors/test", {
          name: c.name, kind: c.kind, provider: providerSel.value,
          endpoint: endpointIn.value.trim(), sender: senderIn.value.trim(),
          credentialsRef: credRefIn.value.trim(),
        });
        testOut.className = "conn-test-result " + (res.ok ? "ok" : "err");
        testOut.textContent = (res.ok ? "✓ " : "✕ ") + (res.detail || (res.ok ? "Works." : "Failed."));
      } catch (err) {
        testOut.className = "conn-test-result err";
        testOut.textContent = "✕ " + (err && err.message ? err.message : err);
      } finally { btn.disabled = false; }
    };

    const close = (value) => dlg.close(value);
    // The patch carries every field this kind and provider actually use — the server
    // re-runs the kind's full validation on the resulting record (ADR-0160), so it has
    // to see what the operator is looking at. A field the shape hides is *omitted*,
    // not sent empty: a SharePoint worker's endpoint is an optional override of the
    // Graph API base that the dialog does not offer, and sending "" for it would
    // silently delete it. Where a value genuinely has to go (switching to the preview
    // transport, which dials nothing) the server clears it, because that is a rule
    // about the provider and not about which fields a form drew.
    const submit = (extra) => {
      const sh = shape();
      const patch = { enabled: enabledIn.checked };
      if (sh.endpoint) patch.endpoint = endpointIn.value.trim();
      if (sh.credRef !== "none") patch.credentialsRef = credRefIn.value.trim();
      if (sh.mail) {
        patch.provider = providerSel.value;
        patch.sender = senderIn.value.trim();
      }
      close({ patch, extra });
    };
    dlg = openDialog({
      title: `Worker · ${c.name || ""}`,
      label: "Edit worker",
      body,
      className: "confirm-modal conn-modal",
      onClose: (value) => resolve(value),
      actions: [
        { label: "Cancel", kind: "neutral", value: null, attrs: { "data-conn-cancel": "" },
          title: "Close without saving" },
        { label: "Test connection", kind: "neutral", keepOpen: true,
          attrs: { "data-conn-test": "", "class": "btn neutral conn-f-test" },
          title: "Connect and authenticate with what is typed above — nothing is saved and no message is sent",
          onSelect: null },
        { label: "Save", kind: extraLabel ? "neutral" : "primary", keepOpen: true,
          attrs: { "data-conn-save": "" }, title: "Save the worker changes",
          onSelect: () => submit(false) },
        ...(extraLabel ? [{ label: extraLabel, keepOpen: true, attrs: { "data-conn-extra": "" },
          title: "Save the changes and retry the parked task", onSelect: () => submit(true) }] : []),
      ],
    });
    // Test is the one action that talks to the server and stays open, and it needs the
    // button itself (it disables it while the call is out), so it is wired here rather
    // than through onSelect.
    dlg.el.querySelector("[data-conn-test]").addEventListener("click", onTest);
    sync(); // again, now that the foot's Test button exists to be shown or hidden
    // The first field that is actually editable for this kind: mail leads with its
    // provider, everything else with its endpoint.
    (c.kind === "mail" ? providerSel : endpointIn).focus();
  });
}

// workerUsageHTML says what a worker is *for*, read off the deployed models
// rather than remembered: the processes whose tasks resolve through it, and how many
// instances are running on them. It is what makes Delete a decision rather than a
// click (ADR-0163) — and it is the same set the server refuses the delete with, so
// the row and the refusal cannot tell different stories.
//
// It is a *count*, with the list one click behind it in openWorkerUsage. Inline,
// the list was the row: a mail worker twenty-one deployed definitions reference drew
// fourteen wrapped lines of links, and the endpoint, the status pill and the actions
// beside them were pushed apart by it — on the one row where something is actually
// wrong, that is the row you cannot read. The numbers are what a scan needs; which
// processes is what a decision needs, and a decision has a click to spare.
export function workerUsageHTML(usedBy) {
  if (!usedBy || !usedBy.length) {
    return `<div class="muted conn-usage">Referenced by no deployed process</div>`;
  }
  const { defs, procs, live } = usageCounts(usedBy);
  // Definitions and processes are different numbers and mean different things: a model
  // deployed five times is five definitions of one process, and saying "used by 21
  // processes" for eight of them would overstate the blast radius. They collapse into
  // one phrase only when they agree.
  const head = procs === defs
    ? `Used by <b>${defs}</b> deployed process${defs === 1 ? "" : "es"}`
    : `Used by <b>${procs}</b> process${procs === 1 ? "" : "es"} &middot; <b>${defs}</b> deployed versions`;
  const running = live
    ? ` &middot; <b>${live}</b> running instance${live === 1 ? "" : "s"}`
    : "";
  return `<div class="conn-usage"><button type="button" class="conn-usage-btn" data-usage
    title="Which deployed processes resolve through this worker — and what deleting it would park">${head}${running}<span class="conn-usage-more" aria-hidden="true">&rsaquo;</span></button></div>`;
}

// usageCounts reduces the raw list to the three numbers the row has room for: how many
// deployed definitions resolve through the worker, how many distinct processes those
// are, and how many instances are running on them.
function usageCounts(usedBy) {
  return {
    defs: usedBy.length,
    procs: new Set(usedBy.map((u) => u.processId || u.name || String(u.processDefKey))).size,
    live: usedBy.reduce((n, u) => n + (u.activeInstances || 0), 0),
  };
}

// groupUsage collapses the definitions into one entry per process, newest version
// first. The server sends one row per deployed definition in definition-key order —
// which for a model redeployed all afternoon is the same name repeated with a rising
// version, and reading that list is counting. Grouped, the answer to "does anything
// still run on it" is the group's own line.
//
// Group order stays the server's (first appearance); only the versions inside a group
// are reversed, so the current one leads.
function groupUsage(usedBy) {
  const groups = [];
  const byKey = new Map();
  for (const u of usedBy) {
    const key = u.processId || u.name || String(u.processDefKey);
    let g = byKey.get(key);
    if (!g) {
      g = { key, name: u.name || u.processId || key, processId: u.processId || "", versions: [], live: 0 };
      byKey.set(key, g);
      groups.push(g);
    }
    g.versions.push(u);
    g.live += u.activeInstances || 0;
  }
  for (const g of groups) g.versions.sort((a, b) => (b.version || 0) - (a.version || 0));
  return groups;
}

// openWorkerUsage opens the list the row's count stands for: every deployed process
// that resolves through this worker, grouped by process, each version linking to its
// Operations page and carrying the elements whose tasks resolve through it and the
// instances running on it right now.
//
// It reads what is already on the record — the same usedBy the row counted and the same
// set the delete refusal names — so it opens instantly and cannot disagree with either.
// Returns the overlay so a caller (or a test) can close it.
export function openWorkerUsage({ worker }) {
  const c = worker || {};
  const usedBy = c.usedBy || [];
  const groups = groupUsage(usedBy);
  const { defs, procs, live } = usageCounts(usedBy);
  // A filter earns its line only once scanning stops being enough. Below that it is one
  // more thing to look past on the way to a list you can already see all of.
  const filtered = groups.length > 6;

  const verHTML = (u) => `<a class="usage-ver" href="#/operations/p/${esc(String(u.processDefKey))}"
      title="Open this version in Operations">
      <span class="usage-ver-v">v${esc(String(u.version))}</span>
      <span class="usage-el">${(u.elements || []).length ? esc((u.elements || []).join(", ")) : "—"}</span>
      ${u.activeInstances ? `<span class="pill ok usage-live">${u.activeInstances} running</span>` : ""}
    </a>`;
  const groupHTML = (g) => `<div class="usage-group" data-usage-text="${esc(
      `${g.name} ${g.processId} ${g.versions.map((u) => "v" + u.version + " " + (u.elements || []).join(" ")).join(" ")}`.toLowerCase())}">
      <div class="usage-group-head">
        <b>${esc(g.name)}</b>
        ${g.processId && g.processId !== g.name ? `<span class="chip">${esc(g.processId)}</span>` : ""}
        <span class="muted small">${g.versions.length} deployed version${g.versions.length === 1 ? "" : "s"}${
          g.live ? ` &middot; ${g.live} running` : ""}</span>
      </div>
      ${g.versions.map(verHTML).join("")}
    </div>`;

  const ov = document.createElement("div");
  const body = document.createElement("div");
  body.innerHTML = `
    <p class="muted usage-intro">${procs === defs
      ? `<b>${defs}</b> deployed process${defs === 1 ? "" : "es"}`
      : `<b>${procs}</b> process${procs === 1 ? "" : "es"} in <b>${defs}</b> deployed versions`} resolve
      through this worker${live ? `, with <b>${live}</b> instance${live === 1 ? "" : "s"} running on them` : ""}.
      Each version links to its Operations page and names the tasks that resolve here.</p>
    ${filtered ? `<input type="text" class="usage-filter" data-usage-filter placeholder="Filter processes…" autocomplete="off" spellcheck="false"/>` : ""}
    <div data-usage-list>${groups.map(groupHTML).join("")}</div>
    <p class="usage-empty" data-usage-none hidden>No process matches that.</p>`;

  const dlg = openDialog({
    title: `Used by · ${c.name || ""}`,
    label: "Used by",
    body,
    className: "usage-modal",
    actions: [
      { spacer: `Deleting this worker parks these tasks with “no worker registered as ${c.name || ""}” until one of the same name and kind exists again.` },
      { label: "Done", value: null, attrs: { "data-usage-done": "" }, title: "Close this dialog" },
    ],
  });

  // A version link navigates the console underneath; leaving the dialog standing over
  // the page it just moved to would be a dialog about a worker you can no longer see.
  for (const a of body.querySelectorAll(".usage-ver")) a.addEventListener("click", () => dlg.close(null));

  const filter = body.querySelector("[data-usage-filter]");
  if (filter) {
    const none = body.querySelector("[data-usage-none]");
    filter.addEventListener("input", () => {
      const q = filter.value.trim().toLowerCase();
      let shown = 0;
      for (const g of body.querySelectorAll(".usage-group")) {
        const hit = !q || (g.dataset.usageText || "").includes(q);
        g.hidden = !hit;
        if (hit) shown++;
      }
      none.hidden = shown > 0;
    });
  }
  return dlg.el;
}

// deleteWorkerFlow asks, then deletes — and when the server refuses because deployed
// models still reference the worker (ADR-0163), asks the *second* question with the
// answer in hand: these processes, this many instances running, their tasks will park
// with "no worker registered" until it exists again. Resolves true when the
// worker was actually deleted, so the caller reloads only then.
//
// Two prompts rather than one pre-flight check: the usage shown on the row may be
// stale by the time the button is pressed, and the server is the one that decides.
export async function deleteWorkerFlow({ api, worker }) {
  const c = worker || {};
  if (!window.confirm(`Delete worker "${c.name}"?`)) return false;
  const url = "/api/v1/connectors/" + encodeURIComponent(c.id);
  try {
    await api("DELETE", url);
    return true;
  } catch (err) {
    const used = err && err.body && err.body.usedBy;
    if (!used || !used.length) throw err;
    const lines = used.map((u) => `  \u2022 ${u.name || u.processId} v${u.version}${
      u.activeInstances ? ` (${u.activeInstances} running)` : ""} \u2014 ${(u.elements || []).join(", ")}`).join("\n");
    if (!window.confirm(
      `"${c.name}" is referenced by ${used.length} deployed process${used.length === 1 ? "" : "es"}:\n\n${lines}\n\n` +
      `Deleting it parks those tasks with "no worker registered as ${c.name}" until a worker of the same name and Worker Type exists again.\n\nDelete anyway?`)) {
      return false;
    }
    await api("DELETE", url + "?force=true");
    return true;
  }
}
