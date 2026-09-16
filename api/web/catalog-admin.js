// Filling a catalogue, as a screen rather than as a sequence of curl calls.
//
// The portal's three models landed with an API and no authoring surface: catalogues,
// products, the edges between them and the release that freezes all of it were
// reachable only by somebody willing to hand-write JSON. That is an answer for
// whoever wrote the API. The product manager's actual question is "how do I put a
// laptop in the catalogue", and this page answers exactly that.
//
// Three things it does deliberately:
//
//   - **It publishes here, and shows every refusal.** Publish is the moment the
//     catalogue is proven: both graphs acyclic, every binding resolved, a text for
//     every declared language, ranks unique. The server answers 422 with the whole
//     list, and this page renders that list rather than a status code — the problems
//     are the work, and hiding them behind "publish failed" would make the screen
//     useless exactly when it matters.
//   - **It binds processes from what is deployed**, never from free text. A product
//     whose provisioning process does not exist is an order that fails at the moment
//     somebody is waiting for a laptop, and a select list cannot make that mistake.
//   - **It keeps the two graphs apart.** Structure (composition, aggregation) says
//     what belongs to what; precedence (requires) says what must exist first. They
//     are different questions, they are validated separately, and a screen that put
//     them in one list would teach the conflation the record had to correct.

const esc = (s) => String(s == null ? "" : s).replace(/[&<>"']/g, (c) =>
  ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;", "'": "&#39;" }[c]));

const fmtTime = (unix) => unix ? new Date(unix * 1000).toLocaleString() : "—";

// publishRefusal renders why a publish was refused.
//
// The server proves a catalogue at publish and answers 422 with every problem at
// once — each one naming the catalogue or the item it belongs to. That list *is*
// the work: "no text for declared language de" tells a product manager what to
// type, and a status code tells them to ask somebody. So the list is rendered as
// a list, and the fallback is only for a failure that is not a refusal at all.
function publishRefusal(err) {
  const problems = ((err.body || {}).problems) || [];
  if (!problems.length) {
    // Not a refusal: a 403, a 500, a network fault. err.message is what there is,
    // and when even that is empty — HTTP/2 carries no reason phrase, so statusText
    // is "" — the status number is more use than a blank box.
    return `<pre style="white-space:pre-wrap; margin:8px 0 0">${
      esc(err.message || `HTTP ${err.status || "?"}`)}</pre>`;
  }
  const where = (p) => (p.item ? `item ${p.item}` : p.catalog ? `catalogue ${p.catalog}` : "");
  return `<p style="margin:8px 0 0">${problems.length} ${
    problems.length === 1 ? "problem" : "problems"} to fix:</p>
    <ul style="margin:6px 0 0">${problems.map((p) => {
    const w = where(p);
    return `<li>${w ? `<b>${esc(w)}</b> — ` : ""}${esc(p.message)}</li>`;
  }).join("")}</ul>`;
}

// The vocabularies, spelled as the server spells them (api/catalog/catalog.go).
// They are duplicated here rather than fetched because they are part of this
// screen's shape — a kind the server does not know would be refused on save, and
// STATES/KINDS/EDGE_KINDS are pinned by a test against the Go source so the two
// cannot drift.
const STATES = [
  { id: "draft", name: "Draft", what: "not orderable; still being worked on" },
  { id: "active", name: "Active", what: "orderable, within its lifecycle window" },
  { id: "withdrawn", name: "Withdrawn", what: "no longer offered; existing orders still resolve through it" },
];

const APPROVAL_KINDS = [
  { id: "none", name: "No approval", ref: "", what: "provisioned as soon as it is ordered" },
  { id: "fixed", name: "A named person", ref: "username", what: "always the same approver" },
  { id: "role", name: "A group", ref: "group", what: "whoever in the group picks it up" },
  { id: "superior", name: "The orderer's superior", ref: "", what: "resolved through the directory, and escalates up the chain" },
];

const EDGE_KINDS = [
  { id: "composition", name: "contains", what: "an integral part, always ordered with the whole and not deselectable" },
  { id: "aggregation", name: "optionally contains", what: "offered beside the whole and separately orderable" },
  { id: "requires", name: "requires", what: "precedence: the other must be provisioned first" },
];

// textOf reads a multilingual name, preferring the catalogue's first language and
// falling back to the id — a product with no text yet is still a product, and a row
// that rendered as an empty cell would be unfindable.
function textOf(texts, langs, fallback) {
  const t = texts || {};
  for (const l of langs || []) if (t[l]) return t[l];
  const any = Object.values(t).find(Boolean);
  return any || fallback;
}

// ---------- The listing ----------

export async function viewCatalogs({ api, toast, view, isSuperseded }) {
  let cats = [];
  try {
    cats = (await api("GET", "/api/v1/catalogs")) || [];
  } catch (e) {
    if (isSuperseded()) return;
    throw e;
  }
  if (isSuperseded()) return;

  const rows = cats.map((c) => `<tr>
    <td><a href="#/catalog/c/${encodeURIComponent(c.id)}">${esc(textOf(c.texts, c.languages, c.id))}</a>
      <div class="muted">${esc(c.id)}</div></td>
    <td>${c.rank}</td>
    <td>${esc((c.languages || []).join(", ")) || "—"}</td>
    <td>${(c.items || []).length}</td>
    <td>${esc((c.groups || []).join(", ")) || "<span class='muted'>nobody yet</span>"}</td>
    <td>${fmtTime(c.updatedAt)}</td>
  </tr>`).join("");

  view.innerHTML = `
    <div class="row">
      <h2 style="margin:0">Catalogues</h2>
    </div>
    <p class="muted" style="max-width:62ch">A catalogue is what one audience is offered.
      Which one a person sees is decided by their groups and its rank — the highest rank
      they reach wins — so two catalogues may not share a rank. A catalogue naming no
      group reaches <b>nobody</b>: the dangerous default is the one where a catalogue
      somebody is still filling is already open to everybody.</p>
    ${cats.length ? `<table class="table">
      <thead><tr><th>Catalogue</th><th>Rank</th><th>Languages</th><th>Products</th><th>Audience</th><th>Changed</th></tr></thead>
      <tbody>${rows}</tbody></table>`
    : `<div class="empty"><p>No catalogue yet. The one below is the first.</p></div>`}

    <div class="card" style="margin-top:18px; max-width:640px">
      <h3 style="margin:0 0 10px">New catalogue</h3>
      <form class="cat-new">
        <label class="field">Name<input name="name" required autocomplete="off"
          placeholder="Workplace"></label>
        <label class="field">Languages<input name="languages" value="de" autocomplete="off"
          placeholder="de, fr"></label>
        <label class="field">Rank<input name="rank" type="number" value="${cats.length + 1}" required></label>
        <label class="field">Audience (groups; empty reaches nobody)<input name="groups"
          autocomplete="off" placeholder="kunde-a, kunde-b"></label>
        <button class="primary" type="submit">Create</button>
      </form>
    </div>`;

  view.querySelector(".cat-new").addEventListener("submit", async (e) => {
    e.preventDefault();
    const f = new FormData(e.target);
    const langs = list(f.get("languages"));
    if (!langs.length) { toast("A catalogue needs at least one language", "err"); return; }
    // The name is stored under the first language: a catalogue with a name in no
    // language it offers would fail to publish, and asking for the pair here is
    // simpler than explaining the refusal later.
    const texts = {}; texts[langs[0]] = String(f.get("name")).trim();
    try {
      const c = await api("POST", "/api/v1/catalogs", {
        texts, languages: langs, rank: Number(f.get("rank")),
        groups: list(f.get("groups")), items: [],
      });
      location.hash = `#/catalog/c/${encodeURIComponent(c.id)}`;
    } catch (err) {
      toast(err.message, "err");
    }
  });
}

const list = (s) => String(s || "").split(",").map((x) => x.trim()).filter(Boolean);

// The target references, as one line of `system:reference` each.
//
// One per line rather than comma-separated, because a reference is frequently a
// distinguished name and a distinguished name is full of commas. The system is
// split on the *first* colon for the same reason in the other direction: an LDAP
// URL or a scoped SKU carries colons of its own, and splitting on the last one
// would silently move half the reference into the system name.
const targetLines = (targets) =>
  (targets || []).map((t) => `${t.system || ""}:${t.ref || ""}`).join("\n");

const parseTargets = (raw) => String(raw || "").split("\n")
  .map((line) => line.trim())
  .filter(Boolean)
  .map((line) => {
    const at = line.indexOf(":");
    // A line with no colon is kept as a reference with no system rather than
    // dropped. Publishing then refuses it by name, which is how the author finds
    // out — a line this form quietly swallowed would be a reference somebody
    // believes they entered.
    return at < 0
      ? { system: "", ref: line }
      : { system: line.slice(0, at).trim(), ref: line.slice(at + 1).trim() };
  });

// ---------- One catalogue ----------

export async function viewCatalogDetail({ api, toast, view, isSuperseded, me, enforced }, id) {
  let cat, items, releases, processes, forms;
  try {
    [cat, items, releases, processes, forms] = await Promise.all([
      api("GET", `/api/v1/catalogs/${encodeURIComponent(id)}`),
      api("GET", "/api/v1/catalog-products"),
      api("GET", `/api/v1/catalogs/${encodeURIComponent(id)}/releases`),
      api("GET", "/api/v1/processes"),
      // The forms a product may ask its orderer to fill in. Offered from what
      // exists, never as free text, for the reason the processes beside it are: a
      // product naming a form nobody wrote is a basket somebody cannot get past.
      api("GET", "/api/v1/forms").catch(() => []),
    ]);
  } catch (e) {
    if (isSuperseded()) return;
    throw e;
  }
  if (isSuperseded()) return;

  items = items || [];
  releases = releases || [];
  const langs = cat.languages || [];
  const offered = cat.items || [];
  const byID = {};
  for (const it of items) byID[it.id] = it;

  // Deployed processes, by id, newest version first. A product binds an id and not
  // a version: what runs is whatever is deployed when the line is reached, which is
  // the same rule the order's approval process follows.
  const procIDs = [...new Set((processes || []).map((p) => p.processId || p.id).filter(Boolean))].sort();
  const formList = (forms || []).map((f) => ({ id: f.id, name: f.name || f.id }))
    .sort((a, b) => a.name.localeCompare(b.name));

  view.innerHTML = `
    <div class="row">
      <h2 style="margin:0">${esc(textOf(cat.texts, langs, cat.id))}</h2>
      <span class="muted">${esc(cat.id)}</span>
    </div>
    <p><a href="#/catalog">← All catalogues</a></p>

    <div class="card" style="margin:0 0 18px; max-width:640px">
      <h3 style="margin:0 0 10px">What this catalogue is</h3>
      <form class="cat-meta">
        ${langs.map((l) => `<label class="field">Name (${esc(l)})<input name="t-${esc(l)}"
          value="${esc((cat.texts || {})[l] || "")}" autocomplete="off"></label>`).join("")}
        <label class="field">Languages<input name="languages" value="${esc(langs.join(", "))}" autocomplete="off"></label>
        <label class="field">Rank<input name="rank" type="number" value="${cat.rank}"></label>
        <label class="field">Audience (groups; empty reaches nobody)<input name="groups"
          value="${esc((cat.groups || []).join(", "))}" autocomplete="off"></label>
        <p class="muted" style="margin:0 0 10px">${(cat.groups || []).length
    ? "Everybody in these groups reaches this catalogue, unless a higher-ranked one reaches them first."
    : "<b>No group named, so nobody reaches this catalogue</b> — the portal will tell them no catalogue is assigned to them."}</p>
        <button class="primary" type="submit">Save</button>
      </form>
    </div>

    ${appearanceCard(cat, me, enforced)}

    <h3>Products</h3>
    <p class="muted" style="max-width:62ch">A product is edited through its home catalogue.
      Everything offered here is orderable once this catalogue is published — a product in
      <b>draft</b> or <b>withdrawn</b> state is not.</p>
    ${offered.length ? `<table class="table">
      <thead><tr><th>Product</th><th>State</th><th>Approval</th><th>Provisioned by</th><th></th></tr></thead>
      <tbody>${offered.map((iid) => productRow(byID[iid], iid, langs)).join("")}</tbody></table>`
    : `<div class="empty"><p>Nothing offered yet.</p></div>`}

    <div class="row" style="margin-top:10px">
      <button class="primary" data-act="new-product">New product</button>
      ${items.length > offered.length ? `<button data-act="add-existing">Offer an existing product</button>` : ""}
    </div>
    <div class="product-editor"></div>

    <h3 style="margin-top:26px">How the products relate</h3>
    <p class="muted" style="max-width:62ch">Two different questions, kept apart.
      <b>Structure</b> says what belongs to what. <b>Precedence</b> says what has to exist
      first, and it is what the fulfilment order is computed from. Both must be free of
      cycles, and publishing proves it.</p>
    ${edgeTable(cat.edges || [], byID, langs)}
    ${offered.length > 1 ? edgeForm(offered, byID, langs) : `<p class="muted">Two products are needed before one can relate to another.</p>`}

    ${sharingCard(cat, me, enforced)}

    <h3 style="margin-top:26px">Releases</h3>
    <p class="muted" style="max-width:62ch">Publishing freezes everything above into a release.
      An order names one release and is immune to every edit made afterwards, which is why a
      catalogue can be reworked while approvals are still pending.</p>
    <div class="row"><button class="primary" data-act="publish">Publish</button></div>
    <div class="publish-report"></div>
    ${releases.length ? `<table class="table" style="margin-top:12px">
      <thead><tr><th>Release</th><th>Published</th><th>Products</th></tr></thead>
      <tbody>${releases.map((r) => `<tr><td>${esc(r.id)}</td><td>${fmtTime(r.createdAt)}</td>
        <td>${(r.items || []).length}</td></tr>`).join("")}</tbody></table>`
    : `<p class="muted">Never published. Until it is, the portal shows this catalogue to nobody.</p>`}`;

  wire({ api, toast, view }, cat, items, byID, langs, procIDs, formList,
    mayShare(cat, me, enforced), mayTheme(me, enforced));
}

function productRow(it, iid, langs) {
  if (!it) {
    return `<tr><td>${esc(iid)}</td><td colspan="3" class="muted">offered but not defined —
      publishing will refuse this</td>
      <td><button data-act="drop" data-id="${esc(iid)}" class="linkish">remove</button></td></tr>`;
  }
  const ap = it.approval || {};
  const kind = APPROVAL_KINDS.find((k) => k.id === ap.kind) || APPROVAL_KINDS[0];
  return `<tr>
    <td>${esc(textOf(it.texts, langs, it.id))}<div class="muted">${esc(it.id)}</div></td>
    <td>${esc((STATES.find((s) => s.id === it.state) || {}).name || it.state || "—")}</td>
    <td>${esc(kind.name)}${ap.ref ? ` <span class="muted">(${esc(ap.ref)})</span>` : ""}</td>
    <td>${esc(it.provisionProcess || "—")}</td>
    <td><button data-act="edit" data-id="${esc(it.id)}" class="linkish">edit</button>
      <button data-act="drop" data-id="${esc(it.id)}" class="linkish">remove</button></td>
  </tr>`;
}

function edgeTable(edges, byID, langs) {
  const rows = (kind) => edges.filter((e) => e.kind === kind).map((e) => {
    const k = EDGE_KINDS.find((x) => x.id === e.kind);
    return `<tr><td>${esc(textOf((byID[e.from] || {}).texts, langs, e.from))}</td>
      <td class="muted">${esc(k ? k.name : e.kind)}</td>
      <td>${esc(textOf((byID[e.to] || {}).texts, langs, e.to))}</td>
      <td><button class="linkish" data-act="unedge" data-edge="${esc(e.from)}|${esc(e.kind)}|${esc(e.to)}">remove</button></td></tr>`;
  }).join("");
  const structure = rows("composition") + rows("aggregation");
  const precedence = rows("requires");
  return `
    <h4 style="margin:14px 0 4px">Structure</h4>
    ${structure ? `<table class="table"><tbody>${structure}</tbody></table>`
    : `<p class="muted">Nothing contains anything else.</p>`}
    <h4 style="margin:14px 0 4px">Precedence</h4>
    ${precedence ? `<table class="table"><tbody>${precedence}</tbody></table>`
    : `<p class="muted">Nothing has to exist before anything else.</p>`}`;
}

function edgeForm(offered, byID, langs) {
  const opts = offered.map((i) =>
    `<option value="${esc(i)}">${esc(textOf((byID[i] || {}).texts, langs, i))}</option>`).join("");
  return `<form class="edge-new card" style="margin-top:12px; max-width:640px">
    <h4 style="margin:0 0 10px">Relate two products</h4>
    <label class="field">From<select name="from">${opts}</select></label>
    <label class="field">Relationship<select name="kind">
      ${EDGE_KINDS.map((k) => `<option value="${k.id}" title="${esc(k.what)}">${esc(k.name)} — ${esc(k.what)}</option>`).join("")}
    </select></label>
    <label class="field">To<select name="to">${opts}</select></label>
    <button class="primary" type="submit">Add</button>
  </form>`;
}

// mayShare mirrors the server's rule rather than guessing at it: sharing is the
// owner's, an editor may change the catalogue and not who else can (ADR-0071).
// --- How a catalogue looks ---------------------------------------------------
//
// A catalogue's appearance is per catalogue and has been since it was built: the
// portal and the approval page paint themselves from it, and the API has carried
// it all along. No screen offered it. So the one thing that makes a catalogue
// somebody *else's* — their colour, their typeface, their mark at the top — was
// reachable only by whoever was willing to write JSON by hand, which is the exact
// state this page exists to end.
//
// Administration and not catalogue maintenance, like the server has it: an editor
// may change what a catalogue offers and may not change whose it looks like. The
// form is drawn for an administrator only, because offering a form that always
// ends in 403 is its own kind of lie.

// TYPEFACES are the stacks the binary ships, spelled as the server spells them
// (api/catalog/theme.go). A list and not a URL: a web font would reach a third
// party on every portal page load, carrying the visitor's address there.
const TYPEFACES = [
  { id: "system", name: "System", what: "whatever the reader's device uses" },
  { id: "humanist", name: "Humanist", what: "Segoe UI, Candara, Optima" },
  { id: "serif", name: "Serif", what: "Georgia, Cambria, Times" },
  { id: "mono", name: "Monospace", what: "SF Mono, Cascadia, Menlo" },
];

// mayTheme mirrors the server's gate, which is stricter than the one on the rest
// of this page: an editor may change what the catalogue offers, and only an
// administrator may change what it looks like.
function mayTheme(me, enforced) {
  if (!enforced) return true;
  return !!me && (me.roles || []).includes("admin");
}

function appearanceCard(cat, me, enforced) {
  const theme = cat.theme || {};
  const accent = theme.accent || "";
  const logoURL = `/api/v1/catalogs/${encodeURIComponent(cat.id)}/logo`;

  if (!mayTheme(me, enforced)) {
    return `<h3 style="margin-top:26px">How this catalogue looks</h3>
      <p class="muted" style="max-width:62ch">${accent || theme.typeface
    ? `Its own appearance: ${esc(accent || "the instance colour")}, ${
      esc(theme.typeface || "the instance typeface")}.`
    : "The instance's own appearance."} Changing it is an administrator's.</p>`;
  }

  return `<h3 style="margin-top:26px">How this catalogue looks</h3>
    <p class="muted" style="max-width:62ch">The portal and the approval page paint
      themselves from this, so a customer sees their own brand rather than yours. Leave
      both empty and the catalogue wears the instance's appearance. Setting it is an
      administrator's; an editor may change what the catalogue offers and not whose it
      looks like.</p>
    <div class="card" style="margin:0 0 18px; max-width:640px">
      <form class="cat-theme">
        <label class="field">Accent colour
          <span class="row" style="gap:8px; align-items:center">
            <input type="color" name="accentpick" value="${esc(accent || "#0b5cff")}"
              aria-label="Pick the accent colour" style="width:44px; padding:2px">
            <input name="accent" value="${esc(accent)}" autocomplete="off" spellcheck="false"
              placeholder="#rrggbb — empty wears the instance colour" style="flex:1">
          </span></label>
        <label class="field">Typeface<select name="typeface">
          <option value=""${theme.typeface ? "" : " selected"}>The instance's</option>
          ${TYPEFACES.map((f) => `<option value="${esc(f.id)}"${
    theme.typeface === f.id ? " selected" : ""}>${esc(f.name)} — ${esc(f.what)}</option>`).join("")}
        </select></label>
        <div class="row">
          <button class="primary" type="submit">Save appearance</button>
          ${accent || theme.typeface
    ? `<button type="button" data-act="theme-clear">Wear the instance's</button>` : ""}
        </div>
      </form>
    </div>

    <div class="card" style="margin:0 0 18px; max-width:640px">
      <h4 style="margin:0 0 10px">Brand mark</h4>
      <p class="muted">Shown at the top of the portal for whoever reaches this catalogue.
        Without one it falls back to the instance's. PNG or SVG.</p>
      <p><img class="cat-logo" src="${esc(logoURL)}" alt=""
        style="max-height:64px; max-width:240px" hidden></p>
      <p class="muted cat-logo-none" hidden>No mark of its own.</p>
      <div class="row">
        <input type="file" class="logo-file" accept="image/png,image/svg+xml"
          aria-label="Choose a brand mark">
        <button type="button" data-act="logo-upload">Upload</button>
        <button type="button" data-act="logo-remove">Remove</button>
      </div>
    </div>`;
}

// With enforcement off there is nobody to be, so everybody is.
//
// It decides what to *offer*, never what is allowed — the server refuses either
// way. Showing a form that always ends in 403 is its own kind of lie.
function mayShare(cat, me, enforced) {
  if (!enforced) return true;
  if (!me) return false;
  if ((me.roles || []).includes("admin")) return true;
  return !!cat.ownerId && cat.ownerId === me.id;
}

const MEMBER_ROLES = [
  { id: "viewer", name: "Viewer", what: "may read it, and may include its products in another catalogue" },
  { id: "editor", name: "Editor", what: "may change it and publish it — but not change this list" },
];

function sharingCard(cat, me, enforced) {
  const can = mayShare(cat, me, enforced);
  const members = cat.members || [];
  const rows = members.map((m) => {
    const r = MEMBER_ROLES.find((x) => x.id === m.role);
    const ref = m.ref || {};
    return `<tr><td>${esc(ref.type === "group" ? "Group" : "User")}</td>
      <td><code>${esc(ref.id || "")}</code></td>
      <td>${esc(r ? r.name : m.role)}</td>
      <td>${can ? `<button class="linkish" data-act="unshare"
        data-ref="${esc(ref.type || "user")}|${esc(ref.id || "")}">remove</button>` : ""}</td></tr>`;
  }).join("");

  return `<h3 style="margin-top:26px">Who maintains this catalogue</h3>
    <p class="muted" style="max-width:62ch">The role says somebody may maintain catalogues at
      all; this says which ones. Sharing is the owner's: an editor may change this catalogue
      and publish it, and may not change this list.</p>
    <p class="muted">Owner: <code>${esc(cat.ownerId || "—")}</code>${
      cat.ownerId ? "" : " <span>(created before ownership, or with authentication off)</span>"}</p>
    ${members.length ? `<table class="table">
      <thead><tr><th>Kind</th><th>Id</th><th>May</th><th></th></tr></thead>
      <tbody>${rows}</tbody></table>`
    : `<p class="muted">Nobody else. Only the owner and administrators maintain it.</p>`}
    ${can ? `<form class="share-new card" style="margin-top:12px; max-width:640px">
      <h4 style="margin:0 0 10px">Let somebody else maintain it</h4>
      <label class="field">Kind<select name="type">
        <option value="user">One account</option>
        <option value="group">A group — everybody in it</option>
      </select></label>
      <label class="field">Id
        <input name="id" required autocomplete="off" placeholder="usr_… or the group id">
        <span class="muted">The account or group id, not the name. An administrator reads it
          from Console → Organization.</span></label>
      <label class="field">May<select name="role">
        ${MEMBER_ROLES.map((r) => `<option value="${r.id}">${esc(r.name)} — ${esc(r.what)}</option>`).join("")}
      </select></label>
      <button class="primary" type="submit">Add</button>
    </form>`
    : `<p class="muted">You maintain this catalogue but do not own it, so who else may is
      the owner's to change.</p>`}`;
}

// productForm renders the editor for one product, or for a new one.
function productForm(it, cat, langs, procIDs, formList, items) {
  const v = it || { state: "draft", approval: { kind: "none" }, texts: {} };
  const ap = v.approval || {};
  const opt = (id, sel, label) =>
    `<option value="${esc(id)}"${id === sel ? " selected" : ""}>${esc(label)}</option>`;
  const procSelect = (name, sel) => `<select name="${name}">
      <option value="">— none —</option>
      ${procIDs.map((p) => opt(p, sel || "", p)).join("")}
      ${sel && !procIDs.includes(sel) ? opt(sel, sel, `${sel} (not deployed)`) : ""}
    </select>`;
  return `<div class="card" style="margin:14px 0; max-width:720px">
    <h3 style="margin:0 0 10px">${it ? "Edit product" : "New product"}</h3>
    <form class="product-form" data-editing="${esc(it ? it.id : "")}">
      <label class="field">Id${it ? "" : " (short, stable, never renamed)"}
        <input name="id" value="${esc(v.id || "")}" ${it ? "readonly" : "required"} autocomplete="off"
          placeholder="laptop"></label>
      ${langs.map((l) => `<label class="field">Name (${esc(l)})<input name="t-${esc(l)}"
        value="${esc((v.texts || {})[l] || "")}" autocomplete="off"></label>`).join("")}
      <label class="field">State<select name="state">
        ${STATES.map((s) => opt(s.id, v.state || "draft", `${s.name} — ${s.what}`)).join("")}
      </select></label>
      <label class="field">Approval<select name="akind">
        ${APPROVAL_KINDS.map((k) => opt(k.id, ap.kind || "none", `${k.name} — ${k.what}`)).join("")}
      </select></label>
      <label class="field">Approver (a username for a named person, a group for a group; empty otherwise)
        <input name="aref" value="${esc(ap.ref || "")}" autocomplete="off"></label>
      <label class="field">Category
        <span class="muted" style="display:block; margin:2px 0 6px">The heading this
          product sits under in the portal &mdash; <code>Arbeitsplatz</code>,
          <code>Kommunikation</code>. A heading and nothing else: it has no ordering of
          its own (the portal sorts alphabetically), no translation, and two spellings
          are two headings. Leave it empty and the product sits under the portal's
          heading for those that carry none.</span>
        <input name="category" value="${esc(v.category || "")}" autocomplete="off"
          list="known-categories" placeholder="Arbeitsplatz">
        <datalist id="known-categories">${
  [...new Set(items.map((i) => (i.category || "").trim()).filter(Boolean))].sort()
    .map((c) => `<option value="${esc(c)}"></option>`).join("")}</datalist></label>
      <label class="field">Cost
        <span class="muted" style="display:block; margin:2px 0 6px">Written as you want it
          read — <code>CHF 1'200.&ndash;</code>, <code>49.&ndash; / Monat</code>,
          <code>im Grundpaket enthalten</code>. It is <b>shown and never computed</b>:
          nothing adds these up, because a total would need a currency, a rate and a date
          that are your finance rules and not the catalogue's. It is frozen into the
          release, so an approver's figure stays the figure they decided on. Leave it
          empty to say nothing about cost.</span>
        <input name="price" value="${esc(v.price || "")}" autocomplete="off"
          placeholder="CHF 1'200.&ndash;"></label>
      <label class="field">Details the orderer fills in
        <span class="muted" style="display:block; margin:2px 0 6px">An Atlas form, for what
          this product needs that its name does not say — a cost centre, a site, an
          employee number. It is shown in the basket and its answers travel with the
          order line, so an approver reads them and a provisioning process can act on
          them. Most products need none.</span>
        <select name="configForm">
          <option value="">— none —</option>
          ${formList.map((f) => opt(f.id, v.configForm || "",
    f.name === f.id ? f.id : `${f.name} (${f.id})`)).join("")}
          ${v.configForm && !formList.some((f) => f.id === v.configForm)
    ? opt(v.configForm, v.configForm, `${v.configForm} (no such form)`) : ""}
        </select></label>
      <label class="field">Provisioned by${procSelect("provisionProcess", v.provisionProcess)}</label>
      <label class="field">Revoked by${procSelect("deprovisionProcess", v.deprovisionProcess)}</label>
      <label class="field inline"><input type="checkbox" name="multipleAllowed"
        ${v.multipleAllowed ? "checked" : ""}> May be held more than once
        <span class="muted">— two licences, two mailboxes</span></label>
      <label class="field">Known in the target systems as
        <span class="muted" style="display:block; margin:2px 0 6px">One per line, as
          <code>system:reference</code> — <code>ad:CN=VPN-Users</code>,
          <code>entra:ENTERPRISEPACK</code>. This is what a commissioning load joins a right
          it found to this product by, and it is compared literally, case and all. Leave it
          empty for anything nothing outside Atlas grants: a load will then never name this
          product, which is the right answer and not a gap. Two products claiming one
          reference is refused when the catalogue is published — a right that matches both
          is attributed to neither.</span>
        <textarea name="targets" rows="3" spellcheck="false"
          placeholder="ad:CN=VPN-Users">${esc(targetLines(v.targets))}</textarea></label>
      <div class="row">
        <button class="primary" type="submit">Save</button>
        <button type="button" data-act="cancel-product">Cancel</button>
      </div>
    </form>
  </div>`;
}

// wireAppearance is the appearance card's half of the page.
//
// Kept out of wire()'s click handler because the logo does not go through api():
// that helper JSON-encodes its body, and a brand mark is raw PNG or SVG bytes with
// the Content-Type carrying the format. Bending the shared helper for one caller
// would put a third meaning on its fourth argument, which is already a boolean
// named isXML.
function wireAppearance({ api, toast, view }, id, reload) {
  const form = view.querySelector(".cat-theme");
  if (!form) return;
  const accent = form.querySelector("input[name=accent]");
  const picker = form.querySelector("input[name=accentpick]");

  // The picker writes the field, and never the other way round: the field is what
  // is sent, and it is the only one of the two that can say "empty", which is how
  // a catalogue goes back to wearing the instance's colour. A picker has no empty.
  picker.addEventListener("input", () => { accent.value = picker.value; });
  accent.addEventListener("input", () => {
    if (/^#[0-9a-fA-F]{6}$/.test(accent.value.trim())) picker.value = accent.value.trim().toLowerCase();
  });

  const putTheme = async (body) => {
    try {
      await api("PUT", `/api/v1/catalogs/${encodeURIComponent(id)}/theme`, body);
      toast("Saved");
      reload();
    } catch (err) { toast(err.message, "err"); }
  };

  form.addEventListener("submit", (e) => {
    e.preventDefault();
    const f = new FormData(e.target);
    putTheme({
      accent: String(f.get("accent") || "").trim().toLowerCase(),
      typeface: String(f.get("typeface") || ""),
    });
  });

  // The mark. Its presence is not a field on the catalogue, so the image itself is
  // the answer: it loads or it 404s, and the note below it says which.
  const img = view.querySelector(".cat-logo");
  const none = view.querySelector(".cat-logo-none");
  if (img) {
    const has = () => { img.hidden = false; none.hidden = true; };
    const hasNot = () => { img.hidden = true; none.hidden = false; };
    img.addEventListener("load", has);
    img.addEventListener("error", hasNot);
    // The request is already in flight by the time this runs, and a cached answer
    // can land before the listeners do. complete says it finished; naturalWidth
    // says whether it finished with an image.
    if (img.complete) (img.naturalWidth ? has : hasNot)();
  }

  view.addEventListener("click", async (e) => {
    const b = e.target.closest("button[data-act]");
    if (!b) return;

    if (b.dataset.act === "theme-clear") {
      // Both fields, not a missing body: the server stores what it is given, so
      // two empty strings are how an appearance is taken away.
      putTheme({ accent: "", typeface: "" });
      return;
    }

    if (b.dataset.act === "logo-upload") {
      const file = view.querySelector(".logo-file").files[0];
      if (!file) { toast("Choose a PNG or SVG first", "err"); return; }
      b.disabled = true;
      try {
        // No size check here. The server carries the limit, it is configurable, and
        // a number copied into this page would be a second copy that goes stale
        // silently — its refusal names the actual figure.
        const res = await fetch(`/api/v1/catalogs/${encodeURIComponent(id)}/logo`, {
          method: "PUT", body: file,
          headers: { "Content-Type": file.type || "application/octet-stream" },
        });
        if (!res.ok) {
          const text = await res.text();
          let data = null;
          try { data = text ? JSON.parse(text) : null; } catch { /* keep text */ }
          throw new Error((data && data.error) || text || `HTTP ${res.status}`);
        }
        toast("Uploaded");
        reload();
      } catch (err) { toast(err.message, "err"); } finally { b.disabled = false; }
      return;
    }

    if (b.dataset.act === "logo-remove") {
      b.disabled = true;
      try {
        await api("DELETE", `/api/v1/catalogs/${encodeURIComponent(id)}/logo`);
        toast("Removed");
        reload();
      } catch (err) { toast(err.message, "err"); } finally { b.disabled = false; }
    }
  });
}

function wire({ api, toast, view }, cat, items, byID, langs, procIDs, formList, canShare, canTheme) {
  const id = cat.id;
  const reload = () => { const h = location.hash; location.hash = "#/catalog"; location.hash = h; };
  // patch changes a catalogue with no precondition. That is right for a form whose
  // every field is on the screen: what it overwrites is what somebody is looking at.
  const patch = async (body) => {
    await api("PATCH", `/api/v1/catalogs/${encodeURIComponent(id)}`, body);
  };

  // patchList is for the other kind of caller, and the catalogue has six of them.
  // `items`, `edges` and `members` are replaced whole when sent, and every place
  // that sends one computes it out of `cat` — the snapshot this page was rendered
  // from. Adding a product posts every product plus that one, so two maintainers
  // adding one a second apart means the second write is the first one's
  // disappearance, with no error and no trace.
  //
  // So it states the revision the page was rendered at and the server refuses if
  // the catalogue has moved past it (ADR-0376). Deliberately `cat.revision` and not
  // a fresh read: a precondition re-read at write time is no precondition at all.
  const patchList = async (body) => {
    await api("PATCH", `/api/v1/catalogs/${encodeURIComponent(id)}`,
      { ...body, revision: cat.revision });
  };

  // patchFailed turns the server's refusal into what a person can do. The 409 names
  // the revision that was read, which is a sentence for an API caller; a person has
  // a page that is out of date, and reload() brings them the current one.
  const patchFailed = (err) => {
    if (err.status === 409) {
      toast("Somebody else changed this catalogue while this page was open. " +
        "Nothing was saved — the page now shows their version, so try again.", "err");
      reload();
      return;
    }
    toast(err.message, "err");
  };
  const editor = view.querySelector(".product-editor");

  view.querySelector(".cat-meta").addEventListener("submit", async (e) => {
    e.preventDefault();
    const f = new FormData(e.target);
    const texts = {};
    for (const l of langs) {
      const val = String(f.get(`t-${l}`) || "").trim();
      if (val) texts[l] = val;
    }
    try {
      await patch({
        texts, languages: list(f.get("languages")), rank: Number(f.get("rank")),
        groups: list(f.get("groups")),
      });
      toast("Saved");
      reload();
    } catch (err) { toast(err.message, "err"); }
  });

  if (canTheme) wireAppearance({ api, toast, view }, id, reload);

  view.addEventListener("click", async (e) => {
    const b = e.target.closest("button[data-act]");
    if (!b) return;
    const act = b.dataset.act;

    if (act === "new-product") {
      editor.innerHTML = productForm(null, cat, langs, procIDs, formList, items);
      wireProductForm();
      return;
    }
    if (act === "edit") {
      editor.innerHTML = productForm(byID[b.dataset.id], cat, langs, procIDs, formList, items);
      wireProductForm();
      return;
    }
    if (act === "cancel-product") { editor.innerHTML = ""; return; }

    if (act === "add-existing") {
      const free = items.filter((it) => !(cat.items || []).includes(it.id));
      const pick = window.prompt(
        `Which product should this catalogue also offer?\n\n${free.map((f) => `${f.id} — ${textOf(f.texts, langs, f.id)}`).join("\n")}`);
      if (!pick) return;
      if (!free.some((f) => f.id === pick.trim())) { toast("No product with that id", "err"); return; }
      try { await patchList({ items: [...(cat.items || []), pick.trim()] }); reload(); }
      catch (err) { patchFailed(err); }
      return;
    }

    if (act === "drop") {
      // Removing takes it out of this catalogue; the product itself stays, because
      // another catalogue may offer it and an order placed through an old release
      // still has to resolve.
      const next = (cat.items || []).filter((x) => x !== b.dataset.id);
      const edges = (cat.edges || []).filter((x) => x.from !== b.dataset.id && x.to !== b.dataset.id);
      try { await patchList({ items: next, edges }); reload(); }
      catch (err) { patchFailed(err); }
      return;
    }

    if (act === "unedge") {
      const [from, kind, to] = b.dataset.edge.split("|");
      const edges = (cat.edges || []).filter((x) => !(x.from === from && x.kind === kind && x.to === to));
      try { await patchList({ edges }); reload(); }
      catch (err) { patchFailed(err); }
      return;
    }

    if (act === "unshare") {
      if (!canShare) return;
      const [type, refID] = b.dataset.ref.split("|");
      const members = (cat.members || []).filter(
        (m) => !((m.ref || {}).type === type && (m.ref || {}).id === refID));
      try { await patchList({ members }); reload(); }
      catch (err) { patchFailed(err); }
      return;
    }

    if (act === "publish") {
      const report = view.querySelector(".publish-report");
      report.innerHTML = "";
      b.disabled = true;
      try {
        const rel = await api("POST", `/api/v1/catalogs/${encodeURIComponent(id)}/releases`);
        toast(`Published ${rel.id}`);
        reload();
      } catch (err) {
        // The refusal is the useful part: the server answers with every problem at
        // once, and a reader needs all of them, not the first.
        //
        // A refused publish is 422 with {"problems":[…]} and carries no "error" key,
        // which is the shape this page must read. Reading err.message instead showed
        // a card with an empty box under it — the screen said "not published" and
        // withheld the entire reason, which is the one thing its own comment above
        // says it must never do.
        report.innerHTML = `<div class="card" style="margin-top:12px; border-color:var(--danger)">
          <b>Not published.</b>
          <p class="muted" style="margin:6px 0 0">Nothing was frozen; the catalogue is unchanged.</p>
          ${publishRefusal(err)}</div>`;
      } finally { b.disabled = false; }
    }
  });

  const shareNew = view.querySelector(".share-new");
  if (shareNew) {
    shareNew.addEventListener("submit", async (e) => {
      e.preventDefault();
      const f = new FormData(e.target);
      const refID = String(f.get("id") || "").trim();
      if (!refID) { toast("An id is needed", "err"); return; }
      const members = [...(cat.members || []),
        { ref: { type: f.get("type"), id: refID }, role: f.get("role") }];
      try { await patchList({ members }); reload(); }
      catch (err) { patchFailed(err); }
    });
  }

  const edgeNew = view.querySelector(".edge-new");
  if (edgeNew) {
    edgeNew.addEventListener("submit", async (e) => {
      e.preventDefault();
      const f = new FormData(e.target);
      const from = f.get("from"), to = f.get("to"), kind = f.get("kind");
      if (from === to) { toast("A product cannot relate to itself", "err"); return; }
      const edges = [...(cat.edges || []), { from, to, kind }];
      try { await patchList({ edges }); reload(); }
      catch (err) { patchFailed(err); }
    });
  }

  function wireProductForm() {
    editor.querySelector(".product-form").addEventListener("submit", async (e) => {
      e.preventDefault();
      const f = new FormData(e.target);
      const editing = e.target.dataset.editing;
      const pid = String(editing || f.get("id") || "").trim();
      if (!pid) { toast("A product needs an id", "err"); return; }

      // Saving a product REPLACES it, and this form does not render every field a
      // product has: there is no control here for variants, the orderable window,
      // the search keywords, the eligible groups or the ceiling on how long the
      // right may last. Built from the controls alone, the body cleared all five on
      // every save and moved the creation date to today — silently, because the
      // fields it dropped are the ones it never shows.
      //
      // So the stored record is the seed and the form's own fields are laid over
      // it. It also carries the revision, which turns a colleague's edit in between
      // from a silent overwrite into a refusal (ADR-0376).
      const stored = byID[pid] || {};

      // Texts are merged rather than rebuilt, for the same reason one level down: a
      // product is shared between catalogues, this form renders one box per
      // language *this* catalogue declares, and a text in a language it does not
      // declare belongs to a catalogue that does. Emptying a box that is rendered
      // still clears that text, or a text could be added and never taken away.
      const texts = { ...(stored.texts || {}) };
      for (const l of langs) {
        const val = String(f.get(`t-${l}`) || "").trim();
        if (val) texts[l] = val; else delete texts[l];
      }
      const body = {
        ...stored,
        id: pid, homeCatalog: id, state: f.get("state"), texts,
        approval: { kind: f.get("akind"), ref: String(f.get("aref") || "").trim() },
        category: String(f.get("category") || "").trim(),
        price: String(f.get("price") || "").trim(),
        configForm: f.get("configForm") || "",
        provisionProcess: f.get("provisionProcess") || "",
        deprovisionProcess: f.get("deprovisionProcess") || "",
        multipleAllowed: !!f.get("multipleAllowed"),
        targets: parseTargets(f.get("targets")),
      };
      try {
        await api("POST", "/api/v1/catalog-products", body);
        // A new product is offered by the catalogue it was created in: creating one
        // that nothing offers is the likeliest way to lose work here.
        //
        // This is a second write, and it carries the catalogue's revision like every
        // other list write — so it has its own refusal, reported in its own words.
        // Letting the outer catch take it would tell somebody their *product* was
        // changed by a colleague, when the product saved fine and it is the
        // catalogue that moved; they would go looking for a conflict that is not
        // there, and never learn the product is stored but unoffered.
        if (!editing && !(cat.items || []).includes(pid)) {
          try {
            await patchList({ items: [...(cat.items || []), pid] });
          } catch (listErr) {
            if (listErr.status !== 409) throw listErr;
            toast(`The product was saved, but somebody else changed this catalogue ` +
              `meanwhile, so "${pid}" was not added to what it offers. Add it from ` +
              `the product list.`, "err");
            reload();
            return;
          }
        }
        toast("Saved");
        reload();
      } catch (err) {
        // The server's refusal is written for a caller that can state a revision.
        // A person has none; they have a page that is out of date, so say the thing
        // they can act on and put the current record in front of them. reload()
        // returns to the catalogue rather than reopening this form, and the message
        // says so — telling somebody their form was refreshed when it was closed
        // sends them looking for a change that is not on the screen.
        if (err.status === 409) {
          toast("Somebody else changed this product while you were editing it. " +
            "Nothing was saved — the page now shows their version, so open the " +
            "product again and reapply your change.", "err");
          reload();
          return;
        }
        toast(err.message, "err");
      }
    });
  }
}
