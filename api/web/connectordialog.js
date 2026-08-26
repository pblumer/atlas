// The connector configuration dialog, in one place (ADR-0160). A connector is
// reconfigured from two very different places — Organization › Connectors, where an
// operator is administering integrations, and an incident, where a token is parked
// *because* this connector did not work — and both need the same fields, the same
// per-kind rules about which of them apply, and the same "check it before you save"
// button. So the dialog lives here and both call it.
//
// It edits an existing record only. Creating a connector is a different act (it picks
// a kind and a name, and the name is the binding every model references, ADR-0036/
// 0041), and it keeps its inline form in the Console; what the two share is the shape
// of a kind's fields, exported as connectorShape so the rules cannot drift apart.
//
// Deleting one lives here too (ADR-0163). It is not a dialog on the same record, but
// it asks the same kind of question — what does this connector's configuration mean
// for the models that resolve through it — and putting it here is what makes it
// reachable from a test at all: app.js boots the whole console on import, so anything
// left in it is only ever exercised by hand.

const esc = (s) => String(s).replace(/[&<>"']/g, (c) =>
  ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;", "'": "&#39;" }[c]));

// connectorShape says which fields a connector of this kind and provider actually
// uses, and what to call them. It is the one description of that: a native mail
// provider and SharePoint default their API base and authenticate with a vault
// credential bundle, SMTP needs a host:port it can dial, the preview transport dials
// nothing at all (which is the point of it, ADR-0150), and temis/clio take an
// endpoint plus an optional token reference.
//
// `credRef` is "required", "optional", or "none"; `endpoint` and `sender` are booleans.
export function connectorShape(kind, provider) {
  const mail = kind === "mail";
  const preview = mail && provider === "preview";
  const native = mail && provider !== "smtp" && !preview;
  const sharepoint = kind === "sharepoint";
  const remedy = kind === "remedy";
  const entra = kind === "entra";
  // The three SQL products. Their whole configuration is one secret — a connection
  // string has no public half — so there is no endpoint to author: what the Console
  // shows is a redacted label the server derived from the string itself.
  const sql = kind === "postgres" || kind === "mariadb" || kind === "mssql";
  // Kinds that default their API base and authenticate with a credential bundle
  // instead of dialing a host:port. Remedy is not one: it needs both.
  const bundle = native || sharepoint || entra || sql;
  return {
    mail,
    sql,
    provider: mail,
    endpoint: !bundle && !preview,
    // A mail connector always has a sender: it is the default From address, and the
    // preview transport frames the message exactly as it would be sent, so it needs
    // one too.
    sender: mail,
    // A SQL connector is created by pasting the connection string, which the server
    // seals into the vault and replaces with a reference — so the reference is one of
    // two ways in, not the only one, and the form must not insist on it.
    credRef: preview ? "none" : (sql ? "optional" : (bundle || remedy ? "required" : "optional")),
    endpointPlaceholder: mail
      ? "smtp.office365.com:587"
      : (remedy ? "https://helix.example.com:8008" : "https://temis.internal"),
    credRefLabel: remedy
      ? "Credential reference (vault {username,password})"
      : (sql ? "\u2026 or a credential reference (a vault key already holding the DSN)"
        : (entra ? "Credential reference (vault {tenantId, clientId, clientSecret})"
          : (bundle ? "Credential reference (vault auth bundle)" : "Token reference (optional)"))),
    credRefPlaceholder: remedy
      ? "remedy_creds (vault {username,password})"
      : (sql ? "postgres_pb_pw (a vault key holding the whole connection string)"
        : (entra ? "entra_blumer (vault {tenantId, clientId, clientSecret})"
          : (sharepoint ? "sharepoint_auth (vault JSON bundle)" : (native ? "gmail_auth (vault JSON bundle)" : "risk_token")))),
    hint: sql
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

// editConnectorFlow opens the dialog on an existing connector, PATCHes what changed,
// and reports what the operator chose. `intro` is the caller's reason for opening it
// (an incident says which task is parked on this connector); `extraLabel`, when given,
// adds a second confirm button — the incident's "Save & retry", which saves and then
// wants the job tried again.
//
// Resolves {saved, extra} after a successful save, or null when nothing was written
// (cancelled, or the save failed and was reported). `okToast` is the success message;
// pass "" when the caller reports the outcome of the whole action instead.
export async function editConnectorFlow({ api, toast, connector, intro = "", extraLabel = "", okToast = "Connector updated" }) {
  const choice = await askConnector({ api, connector, intro, extraLabel });
  if (!choice) return null;
  try {
    await api("PATCH", "/api/v1/connectors/" + encodeURIComponent(connector.id), choice.patch);
  } catch (e) {
    const msg = e && e.message ? e.message : String(e);
    // Managing connectors is admin-only when auth is on (ADR-0041); say that rather
    // than leaving a bare 403 to be read as "the change was wrong".
    toast(/403|forbidden/i.test(msg) ? "Changing a connector needs an admin account" : "Could not save the connector: " + msg, "warn");
    return null;
  }
  // A caller that follows the save with something else (the incident's retry) says so
  // itself, in one message, rather than stacking two toasts on one action.
  if (okToast) toast(okToast, "ok");
  return { saved: true, extra: choice.extra };
}

// askConnector renders the dialog and resolves to {patch, extra}, or null. The fields
// re-shape as the provider changes, because "which of these do I have to fill in" is a
// property of the provider and finding that out from a rejected save is a worse way to
// learn it.
function askConnector({ api, connector, intro, extraLabel }) {
  return new Promise((resolve) => {
    const c = connector || {};
    const ov = document.createElement("div");
    ov.className = "modal-ov";
    ov.innerHTML = `
      <div class="modal confirm-modal conn-modal" role="dialog" aria-modal="true" aria-labelledby="conn-edit-title">
        <div class="modal-head"><h2 id="conn-edit-title">Connector &middot; ${esc(c.name || "")}</h2></div>
        <div class="modal-body">
          ${intro ? `<p class="inc-modal-msg">${esc(intro)}</p>` : ""}
          ${c.problem ? `<p class="conn-problem"><b>Not usable right now:</b> ${esc(c.problem)}</p>` : ""}
          <p class="muted" style="margin:0 0 10px">The name is what every model references, so it is fixed here — changing it would leave those tasks looking for a connector that no longer exists. Everything else takes effect at once; a parked task retries against the new configuration.</p>
          <div class="conn-fields">
            <label class="field"><span>Kind</span><input value="${esc(c.kind || "")}" disabled/></label>
            <label class="field conn-f-provider"><span>Provider</span><select id="conn-provider">
              ${PROVIDERS.map(([v, l]) => `<option value="${v}"${(c.provider || "smtp") === v ? " selected" : ""}>${l}</option>`).join("")}
            </select></label>
            <label class="field conn-f-endpoint" style="flex:1 1 220px"><span class="conn-endpoint-label">Endpoint</span><input id="conn-endpoint" value="${esc(c.endpoint || "")}"/></label>
            <label class="field conn-f-sender" style="flex:1 1 200px"><span>Sender</span><input id="conn-sender" value="${esc(c.sender || "")}" placeholder="bot@example.com"/></label>
            <label class="field conn-f-credref" style="flex:1 1 200px"><span class="conn-credref-label">Token reference</span><input id="conn-credref" value="${esc(c.credentialsRef || "")}"/></label>
          </div>
          <label class="conn-enabled"><input type="checkbox" id="conn-enabled"${c.enabled ? " checked" : ""}/> <span>Enabled — a disabled connector is skipped, and its tasks park</span></label>
          <p class="muted conn-hint" style="margin:8px 0 0;font-size:12.5px"></p>
          <p class="conn-test-result" style="margin:8px 0 0;font-size:12.5px" hidden></p>
        </div>
        <div class="modal-foot">
          <button class="btn neutral" data-conn-cancel title="Close without saving">Cancel</button>
          <button class="btn neutral conn-f-test" data-conn-test title="Connect and authenticate with what is typed above — nothing is saved and no message is sent">Test connection</button>
          <button class="btn${extraLabel ? " neutral" : ""}" data-conn-save title="Save the connector changes">Save</button>
          ${extraLabel ? `<button class="btn" data-conn-extra title="Save the changes and retry the parked task">${esc(extraLabel)}</button>` : ""}
        </div>
      </div>`;
    document.body.appendChild(ov);

    const providerSel = ov.querySelector("#conn-provider");
    const endpointIn = ov.querySelector("#conn-endpoint");
    const senderIn = ov.querySelector("#conn-sender");
    const credRefIn = ov.querySelector("#conn-credref");
    const enabledIn = ov.querySelector("#conn-enabled");
    const testOut = ov.querySelector(".conn-test-result");

    const shape = () => connectorShape(c.kind, c.kind === "mail" ? providerSel.value : c.provider);
    const sync = () => {
      const sh = shape();
      const show = (sel, on) => { const el = ov.querySelector(sel); if (el) el.style.display = on ? "" : "none"; };
      show(".conn-f-provider", sh.provider);
      show(".conn-f-endpoint", sh.endpoint);
      show(".conn-f-sender", sh.sender);
      show(".conn-f-credref", sh.credRef !== "none");
      // Only mail can be checked without saving; the other kinds have no test today.
      show(".conn-f-test", sh.mail);
      endpointIn.placeholder = sh.endpointPlaceholder;
      ov.querySelector(".conn-credref-label").textContent = sh.credRefLabel;
      credRefIn.placeholder = sh.credRefPlaceholder;
      ov.querySelector(".conn-hint").innerHTML = sh.hint;
    };
    providerSel.addEventListener("change", sync);
    sync();

    ov.querySelector("[data-conn-test]").addEventListener("click", async (e) => {
      const btn = e.currentTarget;
      testOut.hidden = false;
      testOut.className = "conn-test-result muted";
      testOut.textContent = "Checking…";
      btn.disabled = true;
      try {
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
    });

    const close = (value) => { ov.remove(); document.removeEventListener("keydown", onKey); resolve(value); };
    const onKey = (e) => { if (e.key === "Escape") close(null); };
    // The patch carries every field this kind and provider actually use — the server
    // re-runs the kind's full validation on the resulting record (ADR-0160), so it has
    // to see what the operator is looking at. A field the shape hides is *omitted*,
    // not sent empty: a SharePoint connector's endpoint is an optional override of the
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
    document.addEventListener("keydown", onKey);
    ov.querySelector("[data-conn-cancel]").addEventListener("click", () => close(null));
    ov.querySelector("[data-conn-save]").addEventListener("click", () => submit(false));
    const extraBtn = ov.querySelector("[data-conn-extra]");
    if (extraBtn) extraBtn.addEventListener("click", () => submit(true));
    ov.addEventListener("click", (e) => { if (e.target === ov) close(null); });
    (c.kind === "mail" ? providerSel : endpointIn).focus();
  });
}

// connectorUsageHTML says what a connector is *for*, read off the deployed models
// rather than remembered: the processes whose tasks resolve through it, and how many
// instances are running on them. It is what makes Delete a decision rather than a
// click (ADR-0163) — and it is the same list the server refuses the delete with, so
// the row and the refusal cannot tell different stories.
export function connectorUsageHTML(usedBy) {
  if (!usedBy || !usedBy.length) {
    return `<div class="muted conn-usage">Referenced by no deployed process</div>`;
  }
  const live = usedBy.reduce((n, u) => n + (u.activeInstances || 0), 0);
  const links = usedBy.map((u) =>
    `<a href="#/operations/p/${esc(String(u.processDefKey))}" title="${esc((u.elements || []).join(", "))}">${esc(u.name || u.processId)} v${esc(String(u.version))}</a>`).join(", ");
  return `<div class="conn-usage">Used by ${links}${
    live ? ` &middot; <b>${live}</b> running instance${live === 1 ? "" : "s"}` : ""}</div>`;
}

// deleteConnectorFlow asks, then deletes — and when the server refuses because deployed
// models still reference the connector (ADR-0163), asks the *second* question with the
// answer in hand: these processes, this many instances running, their tasks will park
// with "no connector registered" until it exists again. Resolves true when the
// connector was actually deleted, so the caller reloads only then.
//
// Two prompts rather than one pre-flight check: the usage shown on the row may be
// stale by the time the button is pressed, and the server is the one that decides.
export async function deleteConnectorFlow({ api, connector }) {
  const c = connector || {};
  if (!window.confirm(`Delete connector "${c.name}"?`)) return false;
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
      `Deleting it parks those tasks with "no connector registered as ${c.name}" until a connector of the same name and kind exists again.\n\nDelete anyway?`)) {
      return false;
    }
    await api("DELETE", url + "?force=true");
    return true;
  }
}
