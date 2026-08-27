// Giving an AI assistant access to Atlas, as a screen rather than as a curl
// command (ADR-0200).
//
// The API for this landed first — POST /api/v1/oauth-clients and the grant
// endpoints — and for a while the answer to "how do I connect claude.ai" was a
// request body in the install guide. That is an answer for whoever wrote it. The
// operator's actual question is "what do I paste into those three fields", and
// this page answers exactly that: it asks which application, creates the
// credentials, and shows the three values the connector dialog is asking for, each
// with a copy button, once.
//
// Two things it deliberately does before that:
//
//   - **It checks the published origin.** Every URL this page hands out comes from
//     what Atlas publishes in its discovery documents, not from the address bar.
//     Behind a TLS proxy with no --external-url those are http://… and no hosted
//     client can use them — a failure that otherwise surfaces as a connector that
//     "just doesn't work". Better to say it here, where the fix is one flag.
//   - **It says whether self-registration is open**, because then this form is
//     unnecessary: the connector sets itself up from the MCP URL alone.
//
// The approvals half is for everybody, not only administrators. A person's own
// approvals are theirs to see and to withdraw, and a screen is the only place most
// people will ever do that.

import { copyText } from "./clipboard.js";

const esc = (s) => String(s == null ? "" : s).replace(/[&<>"']/g, (c) =>
  ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;", "'": "&#39;" }[c]));

const fmtTime = (unix) => unix ? new Date(unix * 1000).toLocaleString() : "—";

// KNOWN are the applications whose redirect URI we can fill in without asking.
//
// Short on purpose. A wrong callback URL here would be worse than no entry at all:
// the operator would paste credentials that cannot work and have no reason to
// suspect this list. So it holds the ones actually seen connecting to Atlas, and
// everything else asks — which is not a burden, because the connector's own dialog
// shows its redirect URI.
const KNOWN = [
  {
    id: "claude",
    label: "Claude (claude.ai)",
    name: "Claude",
    redirect: "https://claude.ai/api/mcp/auth_callback",
    hint: `In Claude: <b>Settings → Connectors → Add custom connector</b>.`,
  },
];

// discover reads what this server publishes about itself. Both documents are
// public, so this works before anything is registered.
async function discover() {
  const out = { issuer: "", mcp: "", registration: "" };
  try {
    const res = await fetch("/.well-known/oauth-authorization-server");
    if (res.ok) {
      const doc = await res.json();
      out.issuer = doc.issuer || "";
      out.registration = doc.registration_endpoint || "";
    }
  } catch { /* leave empty: the notice below says the documents are unreadable */ }
  try {
    const res = await fetch("/.well-known/oauth-protected-resource/mcp");
    if (res.ok) out.mcp = (await res.json()).resource || "";
  } catch { /* no MCP transport in this build */ }
  return out;
}

// originNotice warns when the origin Atlas publishes is not the one that would
// work. It compares against the address this page was loaded from, which is by
// definition an origin that reaches this server from where somebody is standing.
function originNotice(disc) {
  if (!disc.issuer) {
    return `<div class="warn-note">This server did not answer its own OAuth discovery
      document, so the values below cannot be confirmed. That is unusual — check the
      server log.</div>`;
  }
  if (disc.issuer === location.origin) return "";
  return `<div class="warn-note"><b>The address this server publishes is
    <code>${esc(disc.issuer)}</code></b>, not the <code>${esc(location.origin)}</code>
    you are using. A hosted connector uses the published one, so that has to be an
    address it can actually reach. If the published address is right and you are simply
    coming in by another route, nothing is wrong. If it is not, Atlas terminates no TLS
    and cannot work this out behind a proxy — tell it, with
    <code>--external-url</code> (or <code>ATLAS_EXTERNAL_URL</code>). Everything below
    uses the published address either way, so you see what a connector would see.</div>`;
}

// copyRow is one value the operator has to move into another window: labelled,
// selectable, and copyable on an insecure origin too (see clipboard.js).
function copyRow(label, value, note) {
  return `<div class="copy-row">
    <div class="copy-label">${esc(label)}${note ? ` <span class="muted">${note}</span>` : ""}</div>
    <div class="copy-value"><code>${esc(value)}</code>
      <button class="btn ghost small" data-copy="${esc(value)}" title="Copy to the clipboard">Copy</button></div>
  </div>`;
}

// clientRow and grantRow are at module scope because the wizard inserts a row of
// its own after a successful registration: a table that still says "nothing
// registered yet" directly under the credentials it just issued reads as a failure.
const clientRow = (c) => `<tr data-id="${esc(c.id)}">
    <td><b>${esc(c.name)}</b>
      ${c.selfRegistered
        ? ` <span class="pill warn"><span class="dot"></span>registered itself</span>`
        : ` <span class="pill ok"><span class="dot"></span>added here</span>`}
      <div class="muted" style="font-size:12px">${esc(c.id)}</div></td>
    <td>${(c.redirectUris || []).map((u) => `<div class="chip">${esc(u)}</div>`).join(" ")}</td>
    <td>${esc(fmtTime(c.createdAt))}</td>
    <td style="text-align:right"><button class="btn ghost danger" data-act="del-client"
      title="Remove this application and every approval anybody gave it">Remove</button></td>
  </tr>`;

const grantRow = (g) => `<tr data-id="${esc(g.id)}">
    <td><b>${esc(g.clientName || g.clientId)}</b></td>
    <td><span class="chip">${esc(g.username)}</span></td>
    <td><code>${esc(g.resource)}</code>${String(g.resource || "").endsWith("/mcp")
      ? ` <span class="muted" style="font-size:12px">— the AI tools only</span>`
      : ` <span class="muted" style="font-size:12px">— the whole API</span>`}</td>
    <td>${esc(fmtTime(g.createdAt))}</td>
    <td style="text-align:right"><button class="btn ghost danger" data-act="revoke"
      title="Withdraw this approval; the application stops working on its next request">Withdraw</button></td>
  </tr>`;

export async function viewAIAccess({ api, toast, view, isSuperseded }) {
  const disc = await discover();

  // Administration is admin-only; the approvals below are not. A 403 here is the
  // ordinary answer for a signed-in person who is not an administrator, so it
  // hides the half they may not use rather than failing the page.
  let clients = null;
  let admin = true;
  try {
    clients = await api("GET", "/api/v1/oauth-clients");
  } catch (e) {
    // 403 is the ordinary answer for a signed-in non-administrator; 401 should not
    // reach here at all, because the router gates the whole app on being signed in.
    // Both hide the half this person may not use rather than failing the page —
    // the approvals below are theirs either way.
    if (e.status === 403 || e.status === 401) admin = false;
    else throw e;
  }
  let grants = [];
  try { grants = (await api("GET", "/api/v1/oauth-grants")) || []; } catch { /* leave empty */ }

  if (isSuperseded()) return;

  const mcpURL = disc.mcp || ((disc.issuer || location.origin) + "/mcp");

  const selfRegNote = disc.registration
    ? `<p class="muted">This server also lets an application <b>register itself</b>. A
       connector given only the MCP server URL will set itself up, and you can skip
       this form entirely — it appears below marked as self-registered, and whoever
       approves it is told that nobody vetted it.</p>`
    : `<p class="muted">Applications must be registered here first. (A server can also
       be started with <code>--oauth-dynamic-registration</code> to let them register
       themselves; this one was not.)</p>`;

  const wizard = !admin ? "" : `
    <div class="card" id="connect-card">
      <h2>Connect an assistant</h2>
      <p class="muted">Creates the credentials one AI application needs to ask your
      people for access. It cannot reach anything on its own: each person approves it
      for themselves, and what it may then do is exactly what their account may do.</p>
      ${originNotice(disc)}
      ${selfRegNote}
      <form id="connect-form">
        <div class="step">
          <div class="step-n">1</div>
          <div class="step-body">
            <label class="step-title">Which application?</label>
            ${KNOWN.map((k) => `<label class="field inline">
              <input type="radio" name="which" value="${esc(k.id)}" checked />
              <span>${esc(k.label)}</span></label>`).join("")}
            <label class="field inline">
              <input type="radio" name="which" value="other" />
              <span>Another application</span></label>
            <div id="other-fields" hidden>
              <label class="field">Name, as your people will see it on the approval screen
                <input name="name" placeholder="e.g. Research assistant" /></label>
              <label class="field">Redirect URI
                <input name="redirect" placeholder="https://…/callback" /></label>
              <p class="muted" style="margin-top:-4px">The application shows you this —
              its dialog may call it <i>Callback URL</i> or <i>Redirect URI</i>. It must
              match character for character: it is where the authorization travels back,
              so Atlas matches it whole and never by prefix. <code>https</code> is
              required unless it points at this machine.</p>
            </div>
          </div>
        </div>
        <div class="step">
          <div class="step-n">2</div>
          <div class="step-body">
            <label class="step-title">Create the credentials</label>
            <p class="muted">The secret is shown once, here, and never again — Atlas
            stores only its fingerprint.</p>
            <button class="btn" type="submit">Create</button>
          </div>
        </div>
      </form>
      <div id="connect-result"></div>
    </div>`;

  const clientsCard = !admin ? "" : `
    <div class="card" style="padding:0; margin-top:18px">
      <div class="between" style="padding:16px 18px 0"><h2>Registered applications</h2></div>
      <p class="muted" style="padding:0 18px; margin:6px 0 12px">Removing one withdraws
      every approval given to it, immediately.</p>
      <table data-dt-key="oauth-clients">
        <thead><tr><th>Application</th><th>Redirect URI</th><th>Added</th><th></th></tr></thead>
        <tbody id="client-rows">${(clients || []).map(clientRow).join("")
          || `<tr><td colspan="4" class="muted" style="padding:14px 18px">Nothing registered yet.</td></tr>`}</tbody>
      </table>
    </div>`;

  const grantsCard = `
    <div class="card" style="padding:0; margin-top:18px">
      <div class="between" style="padding:16px 18px 0">
        <h2>${admin ? "Approvals" : "Your approvals"}</h2>
      </div>
      <p class="muted" style="padding:0 18px; margin:6px 0 12px">${admin
        ? "Every approval anybody here has given. Withdrawing one takes effect on that application's next request."
        : "What you have allowed to act as you. Withdrawing one takes effect on its next request."}</p>
      <table data-dt-key="oauth-grants">
        <thead><tr><th>Application</th><th>Approved by</th><th>May reach</th><th>Since</th><th></th></tr></thead>
        <tbody id="grant-rows">${grants.map(grantRow).join("")
          || `<tr><td colspan="5" class="muted" style="padding:14px 18px">No approvals yet.</td></tr>`}</tbody>
      </table>
    </div>`;

  // Everything is rendered inside a container that is new on every render, and the
  // delegated handlers hang off *that* rather than off `view` — which survives
  // navigation, so a listener attached to it would be added again on every reload.
  view.innerHTML = `<div id="ai-access">
    <div class="card">
      <h1>AI access</h1>
      <p class="muted">An AI assistant reaches Atlas through its Model Context Protocol
      tools — deploying, starting and inspecting processes — and it does so <b>as the
      person who approved it</b>. Nothing here grants an application anything on its
      own: it may only ask, and every approval is one person's, revocable by them.</p>
      ${copyRow("MCP server URL", mcpURL, "— what an assistant connects to")}
    </div>
    ${wizard}
    ${clientsCard}
    ${grantsCard}
  </div>`;

  const reload = () => viewAIAccess({ api, toast, view, isSuperseded });

  // One delegated copy handler for every copy button on the page, including the
  // ones the result panel adds later.
  document.getElementById("ai-access").addEventListener("click", async (e) => {
    const btn = e.target.closest("button[data-copy]");
    if (!btn) return;
    if (await copyText(btn.dataset.copy)) toast("Copied");
    else toast("Could not copy — select the value and copy it by hand", "warn");
  });

  if (admin) wireConnectForm({ api, toast, view, mcpURL, reload });

  const clientRows = document.getElementById("client-rows");
  if (clientRows) {
    clientRows.addEventListener("click", async (e) => {
      const btn = e.target.closest("button[data-act='del-client']");
      if (!btn) return;
      const id = btn.closest("tr").dataset.id;
      const c = (clients || []).find((x) => x.id === id);
      if (!window.confirm(`Remove ${c ? c.name : id}?\n\nEvery approval anybody gave it is withdrawn with it.`)) return;
      try {
        await api("DELETE", "/api/v1/oauth-clients/" + encodeURIComponent(id));
        toast("Application removed");
        reload();
      } catch (err) { toast(err.message, "err"); }
    });
  }

  document.getElementById("grant-rows").addEventListener("click", async (e) => {
    const btn = e.target.closest("button[data-act='revoke']");
    if (!btn) return;
    const id = btn.closest("tr").dataset.id;
    const g = grants.find((x) => x.id === id);
    if (!window.confirm(`Withdraw the approval for ${g ? (g.clientName || g.clientId) : id}?`)) return;
    try {
      await api("DELETE", "/api/v1/oauth-grants/" + encodeURIComponent(id));
      toast("Approval withdrawn");
      reload();
    } catch (err) { toast(err.message, "err"); }
  });
}

// wireConnectForm handles the one form on this page: pick an application, create
// the credentials, and show what to paste where.
function wireConnectForm({ api, toast, view, mcpURL, reload }) {
  const form = document.getElementById("connect-form");
  const other = document.getElementById("other-fields");
  const result = document.getElementById("connect-result");

  form.addEventListener("change", () => {
    other.hidden = new FormData(form).get("which") !== "other";
  });

  form.addEventListener("submit", async (e) => {
    e.preventDefault();
    const fd = new FormData(form);
    const which = fd.get("which");
    const preset = KNOWN.find((k) => k.id === which);
    const name = (preset ? preset.name : String(fd.get("name") || "").trim());
    const redirect = (preset ? preset.redirect : String(fd.get("redirect") || "").trim());
    if (!name) { toast("Give the application a name — your people see it when they approve it", "warn"); return; }
    if (!redirect) { toast("The redirect URI is required; the application's own dialog shows it", "warn"); return; }

    const submit = form.querySelector("button[type=submit]");
    submit.disabled = true;
    try {
      const created = await api("POST", "/api/v1/oauth-clients", { name, redirectUris: [redirect] });
      result.innerHTML = `
        <div class="step">
          <div class="step-n">3</div>
          <div class="step-body">
            <label class="step-title">Paste these into ${esc(name)}</label>
            <p class="muted">${preset ? preset.hint : "In the application's own connector dialog."}</p>
            ${copyRow("MCP server URL", mcpURL)}
            ${copyRow("Client ID", created.id)}
            ${copyRow("Client secret", created.secret, "— shown once")}
            <p class="muted" style="margin-top:12px">Then press <b>Connect</b> there. It sends
            you back here to sign in and approve it — and what it may do from then on is
            what your own account may do, until you withdraw the approval below.</p>
            <button class="btn ghost" type="button" id="connect-done">Done — hide the secret</button>
          </div>
        </div>`;
      // Show it in the table below straight away. The response is the same view the
      // listing renders, so this is the row the next load would produce.
      const rows = document.getElementById("client-rows");
      if (rows) {
        if (rows.querySelector("td[colspan]")) rows.innerHTML = "";
        rows.insertAdjacentHTML("afterbegin", clientRow(created));
      }
      result.scrollIntoView({ block: "nearest" });
      document.getElementById("connect-done").addEventListener("click", reload);
    } catch (err) {
      toast(err.message, "err");
    } finally {
      submit.disabled = false;
    }
  });
}
