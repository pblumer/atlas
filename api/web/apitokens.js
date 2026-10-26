// Minting, listing and revoking API tokens, as a screen rather than as a curl
// command (ADR-0194, amendment 2026-10-21).
//
// The three routes landed long before this page, and for that whole time the way
// to give a machine a credential was a request body in the install guide, sent with
// a session cookie copied out of the browser's developer tools. This page asks the
// questions that body encodes — which machine, how narrow, for how long — and says
// what the answer means before it is given: the scope in one sentence, the roles the
// token will carry, and a warning when it is to live for ever.
//
// The secret is the one thing on this page that must not outlive it:
//
//   - **It is shown once and kept nowhere.** It lives in the result panel's DOM and
//     nowhere else — not in storage, the URL, the console or a log — and the panel is
//     emptied and removed when the reader presses Done or leaves the view
//     (window.__atlasCleanup, which the router calls on every navigation).
//   - **Or it never arrives at all.** Sealed in the vault (vaultSecret), it stays on
//     the server; that is the right answer for the token a model's own REST tasks
//     present, which no person ever needs to read.
//
// Everything the form says about the server's rules is a copy, held to the original
// by TestTheAPITokenPageAgreesWithTheServer (api/apitokens_page_test.go): the scopes
// a token may be minted with (apiMintableScopes), the roles each one carries
// (tokenRolesFor), and the longest lifetime (maxAPITokenLifetime). The server decides
// regardless; the copies only keep the form from offering what it would refuse.

import { copyText } from "./clipboard.js";
import { fmtDate, fmtDateTime } from "./datefmt.js";
import { locale, plural, t } from "./i18n.js";

const esc = (s) => String(s == null ? "" : s).replace(/[&<>"']/g, (c) =>
  ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;", "'": "&#39;" }[c]));

// th is t for a message written as HTML in the catalogue, with every parameter
// escaped: the catalogue is ours, a token's name is not.
const th = (key, params) => {
  const safe = {};
  for (const k of Object.keys(params || {})) safe[k] = esc(params[k]);
  return t(key, safe);
};

// SCOPES are the scopes a token may be minted with, narrowest first, so the form
// reads from least to most privilege and full is the last thing offered rather
// than the first. `reach` says what a token of the scope may be confined to:
// "projects" a landscape token must name, "catalogues" an events token may name,
// "" for every other scope, whose reach this form does not ask for.
export const SCOPES = [
  { id: "metrics", reach: "" },
  { id: "status", reach: "" },
  { id: "diagrams", reach: "" },
  { id: "worker", reach: "" },
  { id: "events", reach: "catalogues" },
  { id: "landscape", reach: "projects" },
  { id: "directory", reach: "" },
  { id: "inventory", reach: "" },
  { id: "full", reach: "" },
];

// SCOPE_ROLES are the roles a token of a confined scope carries whoever mints it
// (scopeRoles in api/routeroles.go). Every other scope carries its minter's roles
// without admin, and this page is an administrator's, so those are
// ADMIN_TOKEN_ROLES: what tokenRolesFor answers for an administrator.
export const SCOPE_ROLES = { events: ["feedreader"] };
export const ADMIN_TOKEN_ROLES = ["modeler", "operator", "user"];

// MAX_DAYS is maxAPITokenLifetime in days; DEFAULT_DAYS is what the form offers.
export const MAX_DAYS = 1825;
export const DEFAULT_DAYS = 365;

// EXPIRY_WARN_DAYS is how far ahead an expiry is warned about. A token that expires
// stops working without a word — every call its machine makes is a 401 from then on,
// and for the shop's system processes that is an incident per order — so the time to
// say so is while a replacement can still be minted and handed over. Thirty days
// leaves room for a change process. /metrics counts the same window
// (apiTokenExpiryWarning, atlas_api_tokens_expiring_soon), so the page and an alert
// warn about the same tokens; a Go test holds the two equal.
export const EXPIRY_WARN_DAYS = 30;

const DAY = 86400;

// rolesFor is the preview of tokenRolesFor(scope, an administrator).
export const rolesFor = (scope) => SCOPE_ROLES[scope] || ADMIN_TOKEN_ROLES;

const scopeOf = (id) => SCOPES.find((s) => s.id === id);

// expired is the server's own test (apiToken.expired): a token whose expiry has
// been reached is refused. The browser's clock stands in for the server's, which
// matters only for a token that expires within minutes of now.
const expired = (tok, nowSec) => !!tok.expiresAt && nowSec >= tok.expiresAt;

// expiringSoon is a token still accepted that has less than EXPIRY_WARN_DAYS left. An
// expired one is not among them: it has already stopped working and says so itself.
export const expiringSoon = (tok, nowSec) =>
  !!tok.expiresAt && nowSec < tok.expiresAt && tok.expiresAt - nowSec < EXPIRY_WARN_DAYS * DAY;

// soonest returns the tokens about to expire, the first to go first.
const soonest = (tokens, nowSec) =>
  tokens.filter((tok) => expiringSoon(tok, nowSec)).sort((a, b) => a.expiresAt - b.expiresAt);

// leftLabel says how long a token has, in whole days: twelve days and an hour is
// "12 days", because the reader plans in days, and under a day is said as such
// rather than as "0 days".
function leftLabel(tok, nowSec) {
  const days = Math.floor((tok.expiresAt - nowSec) / DAY);
  return days < 1 ? t("apitokens.expires.soonUnderADay") : plural("apitokens.expires.soon", days);
}

// soonItem is one line of a warning: the token, how long it has, and the vault secret
// it went to, because that name is what a replacement has to be sealed under.
const soonItem = (tok, nowSec) =>
  `<li><b>${esc(tok.name)}</b> — ${esc(leftLabel(tok, nowSec))}${tok.vaultSecret
    ? ` · ${esc(t("apitokens.vaultChip", { name: tok.vaultSecret }))}` : ""}</li>`;

const chips = (list) => (list || []).map((r) => `<span class="chip">${esc(r)}</span>`).join(" ");

// tokenRow renders one token. It is at module scope because a minted token is
// inserted as a row straight away, exactly as the next load would draw it.
function tokenRow(tok, ctx) {
  const now = Date.now() / 1000;
  const gone = expired(tok, now);
  const name = (id) => ctx.names.get(id) || id;
  let expires;
  if (!tok.expiresAt) {
    expires = `<span class="pill warn" data-expiry="never"><span class="dot"></span>${esc(t("apitokens.expires.never"))}</span>`;
  } else if (gone) {
    expires = `<span class="pill err" data-expiry="expired" title="${esc(t("apitokens.expires.expiredTitle", { when: fmtDateTime(tok.expiresAt * 1000) }))}"><span class="dot"></span>${esc(t("apitokens.expires.expired"))}</span>
      <div class="muted" style="font-size:12px">${esc(fmtDate(tok.expiresAt * 1000))}</div>`;
  } else if (expiringSoon(tok, now)) {
    expires = `<span class="pill warn" data-expiry="soon" title="${esc(t("apitokens.expires.soonTitle", { when: fmtDateTime(tok.expiresAt * 1000) }))}"><span class="dot"></span>${esc(leftLabel(tok, now))}</span>
      <div class="muted" style="font-size:12px">${esc(fmtDate(tok.expiresAt * 1000))}</div>`;
  } else {
    expires = `<span data-expiry="valid">${esc(fmtDate(tok.expiresAt * 1000))}</span>`;
  }
  const who = tok.createdBy ? ctx.people.get(tok.createdBy) || tok.createdBy : "";
  const origin = tok.createdAt
    ? esc(who ? t("apitokens.origin", { when: fmtDateTime(tok.createdAt * 1000), who }) : fmtDateTime(tok.createdAt * 1000))
    : "—";
  return `<tr data-id="${esc(tok.id)}"${gone ? ` class="tok-expired" data-expired="true"` : ""}>
    <td><b>${esc(tok.name)}</b>
      <div class="muted" style="font-size:12px"><code>${esc(tok.id)}</code></div>
      ${tok.vaultSecret ? `<div style="margin-top:4px"><span class="pill vis" data-vault="${esc(tok.vaultSecret)}">${esc(t("apitokens.vaultChip", { name: tok.vaultSecret }))}</span></div>` : ""}</td>
    <td><code>${esc(tok.scope)}</code></td>
    <td>${chips(tok.roles)}</td>
    <td>${(tok.reach || []).length ? chips(tok.reach.map(name)) : `<span class="muted">—</span>`}</td>
    <td data-sort="${tok.expiresAt || Number.MAX_SAFE_INTEGER}">${expires}</td>
    <td data-sort="${tok.createdAt || 0}">${origin}</td>
    <td style="text-align:right"><button class="btn ghost danger" data-act="revoke"
      title="${esc(t("apitokens.revoke.title"))}">${esc(t("apitokens.revoke"))}</button></td>
  </tr>`;
}

const emptyRow = () =>
  `<tr class="tok-empty"><td colspan="7" class="muted" style="padding:14px 18px">${esc(t("apitokens.list.empty"))}</td></tr>`;

// paintSoon fills the warning above the list: which tokens expire within
// EXPIRY_WARN_DAYS and what to do about them. It runs again after every mint and
// revocation, so replacing a token and revoking the old one is what clears it.
function paintSoon(root, tokens) {
  const box = root.querySelector("#tok-soon");
  const now = Date.now() / 1000;
  const soon = soonest(tokens, now);
  box.hidden = !soon.length;
  box.innerHTML = soon.length
    ? `<b>${esc(plural("apitokens.soon.summary", soon.length, { days: EXPIRY_WARN_DAYS }))}</b>
      <ul>${soon.map((tok) => soonItem(tok, now)).join("")}</ul>
      <div>${esc(t("apitokens.soon.hint"))}</div>`
    : "";
}

// renderExpiringTokens tells an administrator on the Console's dashboard which tokens
// expire within EXPIRY_WARN_DAYS. The API-token page says it too, but a warning that
// lives only on a page nobody opens is the silence it exists to break. Silent when
// nothing is about to expire and on any failure: the dashboard works without it.
export async function renderExpiringTokens(slot, { api }) {
  if (!slot) return;
  let tokens;
  try { tokens = await api("GET", "/api/v1/api-tokens"); } catch { return; }
  if (!Array.isArray(tokens)) return;
  const now = Date.now() / 1000;
  const soon = soonest(tokens, now);
  if (!soon.length) return;
  slot.innerHTML = `<div class="card tokens-expiring" id="api-tokens-expiring" role="status">
    <h2>${esc(t("apitokens.dashboard.title"))}</h2>
    <p>${esc(plural("apitokens.dashboard.intro", soon.length, { days: EXPIRY_WARN_DAYS }))}</p>
    <ul>${soon.map((tok) => soonItem(tok, now)).join("")}</ul>
    <p class="muted">${esc(t("apitokens.soon.hint"))}</p>
    <div class="row"><a class="btn ghost" href="#/console/api-tokens">${esc(t("apitokens.dashboard.open"))}</a></div>
  </div>`;
}

// dropSecret empties and removes the result panel. It is the page's cleanup: the
// router calls it on every navigation, so leaving the view takes the secret out of
// the document rather than leaving it in a detached node somebody still holds.
function dropSecret() {
  const panel = document.getElementById("tok-result");
  if (!panel) return;
  panel.querySelectorAll("code").forEach((c) => { c.textContent = ""; });
  panel.remove();
}

export async function viewAPITokens({ api, toast, view, isSuperseded }) {
  let tokens;
  try {
    tokens = (await api("GET", "/api/v1/api-tokens")) || [];
  } catch (e) {
    if (isSuperseded()) return;
    // The menu does not offer this page to anybody else, but a typed URL reaches it:
    // the server refuses, and the page says why instead of showing an error card.
    if (e.status === 403 || e.status === 401) {
      view.innerHTML = `<div class="card"><h1>${esc(t("apitokens.title"))}</h1>
        <p class="muted" id="tok-denied">${esc(t("apitokens.denied"))}</p></div>`;
      return;
    }
    throw e;
  }
  // What the vault already holds, so the form can say a name will be replaced; and
  // whether there is a vault at all. Only 503 means there is none (--vault=false):
  // any other failure still offers the option, and the server decides.
  let secrets;
  try {
    secrets = new Set(((await api("GET", "/api/v1/secrets")) || []).map((s) => s.name));
  } catch (e) {
    secrets = e.status === 503 ? null : new Set();
  }
  // Names for the ids a listing carries. Both are best-effort: an id is a correct
  // answer, only a less readable one.
  const people = new Map();
  try {
    for (const p of (await api("GET", "/api/v1/principals")) || []) people.set(p.id, p.name);
  } catch { /* the ids stand */ }
  const names = new Map();
  const reachLists = {};
  if (tokens.some((tok) => (tok.reach || []).length)) {
    await Promise.all([loadReach(api, "projects", reachLists, names), loadReach(api, "catalogues", reachLists, names)]);
  }
  if (isSuperseded()) return;

  const ctx = { people, names };
  view.innerHTML = `<div id="api-tokens">
    <div class="card">
      <h1>${esc(t("apitokens.title"))}</h1>
      <p class="muted">${esc(t("apitokens.intro"))}</p>
    </div>
    <div class="card" id="tok-new-card" style="margin-top:18px">
      <h2>${esc(t("apitokens.new.title"))}</h2>
      ${newTokenForm(secrets)}
      <div id="tok-result-slot"></div>
    </div>
    <div class="card" style="padding:0; margin-top:18px">
      <div class="between" style="padding:16px 18px 0"><h2>${esc(t("apitokens.list.title"))}</h2></div>
      <p class="muted" style="padding:0 18px; margin:6px 0 12px">${esc(t("apitokens.list.intro"))}</p>
      <div class="warn-note" id="tok-soon" role="status" style="margin:0 18px 12px" hidden></div>
      <table data-dt-key="api-tokens">
        <thead><tr><th>${esc(t("apitokens.col.name"))}</th><th>${esc(t("apitokens.col.scope"))}</th>
          <th>${esc(t("apitokens.col.roles"))}</th><th>${esc(t("apitokens.col.reach"))}</th>
          <th>${esc(t("apitokens.col.expires"))}</th><th>${esc(t("apitokens.col.origin"))}</th><th></th></tr></thead>
        <tbody id="tok-rows">${tokens.map((tok) => tokenRow(tok, ctx)).join("") || emptyRow()}</tbody>
      </table>
    </div>
  </div>`;
  window.__atlasCleanup = dropSecret;

  const root = document.getElementById("api-tokens");
  paintSoon(root, tokens);
  wireForm({ api, toast, root, secrets, ctx, reachLists, tokens });
  wireRevoke({ api, toast, root, tokens });

  // One copy handler for the page; the value is read from the panel when the button
  // is pressed rather than carried in an attribute, so the secret is in the document
  // exactly once.
  root.addEventListener("click", async (e) => {
    const btn = e.target.closest("button[data-copy-secret]");
    if (!btn) return;
    const code = document.getElementById("tok-secret");
    if (code && await copyText(code.textContent)) toast(t("apitokens.secret.copied"));
    else toast(t("apitokens.secret.copyFailed"), "warn");
  });
}

// loadReach fetches what a reach may name — the applications or the catalogues —
// once per page, and records their display names for the listing.
async function loadReach(api, kind, lists, names) {
  if (lists[kind]) return lists[kind];
  try {
    if (kind === "projects") {
      const apps = (await api("GET", "/api/v1/applications")) || [];
      lists[kind] = apps.map((a) => ({ id: a.id, label: a.name || a.id }));
    } else {
      const cats = (await api("GET", "/api/v1/catalogs")) || [];
      lists[kind] = cats.map((c) => ({ id: c.id, label: catalogueLabel(c) }));
    }
  } catch (e) {
    lists[kind] = { error: e.message };
    return lists[kind];
  }
  for (const it of lists[kind]) names.set(it.id, it.label);
  return lists[kind];
}

const catalogueLabel = (c) => {
  const texts = c.texts || {};
  return texts[locale()] || texts.de || texts.en || Object.values(texts)[0] || c.id;
};

function newTokenForm(secrets) {
  const vault = secrets
    ? `<label class="field inline" style="margin-top:14px">
        <input type="checkbox" name="toVault" />
        <span>${esc(t("apitokens.vault.check"))}</span></label>
      <div id="tok-vault" hidden>
        <label class="field">${esc(t("apitokens.vault.name"))}
          <input name="vaultSecret" value="atlas" autocomplete="off" spellcheck="false" /></label>
        <p class="muted" style="margin-top:-4px">${esc(t("apitokens.vault.hint"))}</p>
        <div id="tok-vault-notes"></div>
      </div>`
    : `<p class="muted" id="tok-vault-unavailable" style="margin-top:14px">${esc(t("apitokens.vault.unavailable"))}</p>`;
  return `<form id="tok-form" novalidate>
    <label class="field">${esc(t("apitokens.new.name"))}
      <input name="name" required autocomplete="off" placeholder="${esc(t("apitokens.new.namePlaceholder"))}" /></label>
    <p class="muted" style="margin-top:-4px">${esc(t("apitokens.new.nameHint"))}</p>
    <label class="field">${esc(t("apitokens.new.scope"))}
      <select name="scope" required>
        <option value="">${esc(t("apitokens.new.scopeChoose"))}</option>
        ${SCOPES.map((s) => `<option value="${esc(s.id)}">${esc(s.id)}</option>`).join("")}
      </select></label>
    <p class="muted" id="tok-scope-desc" style="margin-top:-4px">${esc(t("apitokens.new.scopeHint"))}</p>
    <div id="tok-reach"></div>
    <label class="field">${esc(t("apitokens.new.days"))}
      <input name="days" type="number" min="0" max="${MAX_DAYS}" step="1" value="${DEFAULT_DAYS}" /></label>
    <p class="muted" style="margin-top:-4px">${esc(t("apitokens.new.daysHint", { max: MAX_DAYS }))}</p>
    <div id="tok-never" hidden>
      <div class="warn-note">${esc(t("apitokens.new.neverWarn"))}
        <label class="field inline" style="margin:8px 0 0"><input type="checkbox" name="neverConfirm" />
          <span>${esc(t("apitokens.new.neverConfirm"))}</span></label>
      </div>
    </div>
    ${vault}
    <div class="tok-roles" id="tok-roles">
      <div class="copy-label">${esc(t("apitokens.roles.title"))}</div>
      <div id="tok-roles-body" class="muted">${esc(t("apitokens.roles.choose"))}</div>
    </div>
    <button class="btn" type="submit" style="margin-top:14px">${esc(t("apitokens.new.submit"))}</button>
  </form>`;
}

function wireForm({ api, toast, root, secrets, ctx, reachLists, tokens }) {
  const form = root.querySelector("#tok-form");
  const desc = root.querySelector("#tok-scope-desc");
  const reachBox = root.querySelector("#tok-reach");
  const never = root.querySelector("#tok-never");
  const rolesBody = root.querySelector("#tok-roles-body");
  const vaultBox = root.querySelector("#tok-vault");
  const vaultNotes = root.querySelector("#tok-vault-notes");
  let reachFor = ""; // the scope the reach box was last drawn for

  // paint brings every dependent part of the form in line with what is chosen.
  const paint = async () => {
    const scope = form.elements.scope.value;
    const sc = scopeOf(scope);
    desc.textContent = sc ? t("apitokens.scope." + scope) : t("apitokens.new.scopeHint");

    if (!sc) {
      rolesBody.className = "muted";
      rolesBody.textContent = t("apitokens.roles.choose");
    } else {
      rolesBody.className = "";
      rolesBody.innerHTML = `${chips(rolesFor(scope))}
        <div class="muted" style="font-size:12px; margin-top:4px">${esc(SCOPE_ROLES[scope]
          ? t("apitokens.roles.fixed", { scope })
          : t("apitokens.roles.minter"))}</div>`;
    }

    const days = Number(form.elements.days.value);
    never.hidden = !(form.elements.days.value !== "" && days === 0);

    if (vaultBox) {
      const on = form.elements.toVault.checked;
      vaultBox.hidden = !on;
      const name = form.elements.vaultSecret.value.trim();
      const notes = [];
      if (on && name && secrets.has(name)) notes.push(th("apitokens.vault.replaces", { name }));
      if (on && name === "atlas" && sc && scope !== "full") notes.push(esc(t("apitokens.vault.atlasNeedsFull")));
      vaultNotes.innerHTML = notes.map((n) => `<div class="warn-note">${n}</div>`).join("");
    }

    const want = sc ? sc.reach : "";
    if (want !== reachFor) {
      reachFor = want;
      reachBox.innerHTML = want ? `<div class="muted">${esc(t("apitokens.reach.loading"))}</div>` : "";
      if (want) {
        const list = await loadReach(api, want, reachLists, ctx.names);
        if (reachFor !== want) return; // the scope changed while the list loaded
        reachBox.innerHTML = reachPicker(want, list);
      }
    }
  };
  form.addEventListener("input", paint);
  form.addEventListener("change", paint);

  form.addEventListener("submit", async (e) => {
    e.preventDefault();
    const name = form.elements.name.value.trim();
    const scope = form.elements.scope.value;
    const sc = scopeOf(scope);
    const daysRaw = form.elements.days.value;
    const days = Number(daysRaw);
    if (!name) { toast(t("apitokens.new.nameRequired"), "warn"); form.elements.name.focus(); return; }
    if (!sc) { toast(t("apitokens.new.scopeRequired"), "warn"); form.elements.scope.focus(); return; }
    if (daysRaw === "" || !Number.isInteger(days) || days < 0 || days > MAX_DAYS) {
      toast(t("apitokens.new.daysRange", { max: MAX_DAYS }), "warn");
      form.elements.days.focus();
      return;
    }
    if (days === 0 && !form.elements.neverConfirm.checked) {
      toast(t("apitokens.new.neverRequired"), "warn");
      return;
    }
    const body = { name, scope, expiresInDays: days };
    if (sc.reach) {
      const reach = [...form.querySelectorAll("input[name=reach]:checked")].map((i) => i.value);
      if (sc.reach === "projects" && !reach.length) { toast(t("apitokens.reach.required"), "warn"); return; }
      if (reach.length) body.reach = reach;
    }
    if (vaultBox && form.elements.toVault.checked) {
      const vaultSecret = form.elements.vaultSecret.value.trim();
      if (!vaultSecret) { toast(t("apitokens.vault.nameRequired"), "warn"); form.elements.vaultSecret.focus(); return; }
      body.vaultSecret = vaultSecret;
    }

    const submit = form.querySelector("button[type=submit]");
    submit.disabled = true;
    try {
      const created = await api("POST", "/api/v1/api-tokens", body);
      dropSecret(); // a previous token's panel, if its reader never pressed Done
      showResult(root, created);
      // The response is the view the listing renders, without the secret in it.
      const secret = created.token;
      delete created.token;
      tokens.push(created);
      const rows = root.querySelector("#tok-rows");
      const empty = rows.querySelector("tr.tok-empty");
      if (empty) empty.remove();
      rows.insertAdjacentHTML("beforeend", tokenRow(created, ctx));
      paintSoon(root, tokens);
      if (created.vaultSecret && secrets) secrets.add(created.vaultSecret);
      form.reset();
      reachFor = "";
      reachBox.innerHTML = "";
      await paint();
      toast(t(secret ? "apitokens.created" : "apitokens.createdSealed"));
    } catch (err) {
      toast(err.message, "err");
    } finally {
      submit.disabled = false;
    }
  });
}

// reachPicker is the list of applications or catalogues a token may be confined to.
function reachPicker(kind, list) {
  const label = kind === "projects" ? "apitokens.reach.projects" : "apitokens.reach.catalogues";
  const hint = kind === "projects" ? "apitokens.reach.projectsHint" : "apitokens.reach.cataloguesHint";
  let body;
  if (list && list.error) body = `<div class="warn-note">${esc(t("apitokens.reach.loadFailed", { error: list.error }))}</div>`;
  else if (!list || !list.length) body = `<div class="muted">${esc(t("apitokens.reach.none"))}</div>`;
  else {
    body = list.map((it) => `<label class="field inline" style="margin:4px 0">
      <input type="checkbox" name="reach" value="${esc(it.id)}" />
      <span>${esc(it.label)} <span class="muted" style="font-size:12px">${esc(it.id)}</span></span></label>`).join("");
  }
  return `<div class="tok-reach" data-reach="${esc(kind)}">
    <div class="field" style="margin-bottom:4px">${esc(t(label))}</div>
    <p class="muted" style="margin:0 0 6px">${esc(t(hint))}</p>
    ${body}
  </div>`;
}

// showResult draws the panel under the form: the secret with its copy button, or —
// when it was sealed in the vault — where it went.
function showResult(root, created) {
  const slot = root.querySelector("#tok-result-slot");
  if (created.token) {
    slot.innerHTML = `<div id="tok-result" class="tok-result">
      <div class="step-title">${esc(t("apitokens.secret.title", { name: created.name }))}</div>
      <div class="warn-note" id="tok-once">${esc(t("apitokens.secret.once"))}</div>
      <div class="copy-value"><code id="tok-secret"></code>
        <button class="btn" type="button" data-copy-secret>${esc(t("apitokens.secret.copy"))}</button></div>
      <p class="muted" style="margin-top:10px">${esc(t("apitokens.secret.use"))}</p>
      <button class="btn ghost" type="button" id="tok-done">${esc(t("apitokens.secret.done"))}</button>
    </div>`;
    // Set as text rather than written into the markup: it is a value, and it goes
    // into the document in exactly one place.
    slot.querySelector("#tok-secret").textContent = created.token;
  } else {
    slot.innerHTML = `<div id="tok-result" class="tok-result">
      <p id="tok-sealed">${esc(t("apitokens.sealed", { name: created.name, secret: created.vaultSecret || "" }))}</p>
      <button class="btn ghost" type="button" id="tok-done">${esc(t("apitokens.sealed.done"))}</button>
    </div>`;
  }
  slot.querySelector("#tok-done").addEventListener("click", dropSecret);
  slot.scrollIntoView({ block: "nearest" });
}

function wireRevoke({ api, toast, root, tokens }) {
  root.querySelector("#tok-rows").addEventListener("click", async (e) => {
    const btn = e.target.closest("button[data-act='revoke']");
    if (!btn) return;
    const row = btn.closest("tr");
    const id = row.dataset.id;
    const tok = tokens.find((x) => x.id === id) || { name: id };
    let msg = t("apitokens.revoke.confirm", { name: tok.name });
    if (tok.vaultSecret) msg += t("apitokens.revoke.confirmVault", { secret: tok.vaultSecret });
    if (!window.confirm(msg)) return;
    btn.disabled = true;
    try {
      await api("DELETE", "/api/v1/api-tokens/" + encodeURIComponent(id));
      const at = tokens.findIndex((x) => x.id === id);
      if (at >= 0) tokens.splice(at, 1);
      const body = row.parentElement;
      row.remove();
      if (!body.querySelector("tr[data-id]")) body.insertAdjacentHTML("beforeend", emptyRow());
      paintSoon(root, tokens);
      toast(t("apitokens.revoked"));
    } catch (err) {
      btn.disabled = false;
      toast(err.message, "err");
    }
  });
}
