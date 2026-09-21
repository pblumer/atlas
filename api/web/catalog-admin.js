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

// The console's "pick one of these" dialog, shared rather than reinvented: it is
// what replaced the window.prompt pickers elsewhere, and it is covered by an
// end-to-end test against exactly the list length a prompt could not show.
import { openPickModal } from "./pickmodal.js";

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

// EDGE_KINDS is the whole vocabulary of the catalogue's edges, used to *read* one.
const EDGE_KINDS = [
  { id: "composition", name: "contains", what: "an integral part, always ordered with the whole and not deselectable" },
  { id: "aggregation", name: "optionally contains", what: "offered beside the whole and separately orderable" },
  { id: "requires", name: "requires", what: "precedence: the other must be provisioned first" },
  { id: "excludes", name: "must not be held with", what: "incompatibility: one person may never hold both" },
];

// STRUCTURE_CHOICES are the three states one service can be in with respect to one
// product — the kit's whole vocabulary, in the order somebody reads them.
const STRUCTURE_CHOICES = [
  { id: "none", name: "not part of it", what: "no relation to this product" },
  { id: "composition", name: "included", what: "always ordered with it, and not deselectable" },
  { id: "aggregation", name: "optional", what: "offered beside it, ordered only if ticked" },
];

// STRUCTURE_IDS are the edge kinds the kit owns — every choice above except "not
// part of it", which is the absence of an edge rather than one of its own. Derived
// from the list rather than spelled again, because a fourth answer added above and
// forgotten here would be a save that silently drops it.
const STRUCTURE_IDS = STRUCTURE_CHOICES.map((c) => c.id).filter((c) => c !== "none");

// PAIRWISE_KINDS is what the pairwise form may still *write* (#1022).
//
// Structure is assembled per product now: a catalogue is built out of services that
// each provision themselves, and what a product adds is an arrangement — which of
// them come with it, and which are offered beside it. That is one question asked per
// service, and asking it again as "pick a from, pick a relationship, pick a to"
// would be a second way to say the same thing. Two ways drift, and the one that
// drifts here decides what somebody is actually ordering.
//
// Everything the kit does not own is pairwise, and derived rather than listed for
// the reason STRUCTURE_IDS is: a fourth kind added above and forgotten here would
// be a kind nothing can author. Both of them *are* pairwise — "the account before
// the mailbox" and "never these two together" are statements about two products
// that belong to neither.
const PAIRWISE_KINDS = EDGE_KINDS.filter((k) => !STRUCTURE_IDS.includes(k.id));

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
  let report = null;
  // null means the directory could not be read, which is different from an empty
  // one: the first degrades to typed ids, the second says there are no groups yet.
  let dir = null;
  try {
    cats = (await api("GET", "/api/v1/catalogs")) || [];
    dir = await api("GET", "/api/v1/principals").catch(() => null);
    // Which approval rules reach nobody. null is "could not be read", which the
    // card below says out loud rather than rendering as "nothing is wrong".
    report = await api("GET", "/api/v1/catalog-products/approver-report").catch(() => null);
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
    <td>${(c.groups || []).length
    ? esc((c.groups || []).map((g) => nameOfPrincipal(dir, g)).join(", "))
    : "<span class='muted'>nobody yet</span>"}</td>
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
    <div class="cat-cols">
      <div class="cat-main">
        ${cats.length ? `<table class="table">
          <thead><tr><th>Catalogue</th><th>Rank</th><th>Languages</th><th>Products</th><th>Audience</th><th>Changed</th></tr></thead>
          <tbody>${rows}</tbody></table>`
    : `<div class="empty"><p>No catalogue yet. The one beside it is the first.</p></div>`}
      </div>
      <aside class="cat-side">
    <div class="card">
      <h3 style="margin:0 0 10px">New catalogue</h3>
      <form class="cat-new">
        <label class="field">Name<input name="name" required autocomplete="off"
          placeholder="Workplace"></label>
        <label class="field">Languages<input name="languages" value="de" autocomplete="off"
          placeholder="de, fr"></label>
        <label class="field">Rank<input name="rank" type="number" value="${cats.length + 1}" required></label>
        ${audienceField(dir, [])}
        <button class="btn" type="submit">Create</button>
      </form>
    </div>
      </aside>
    </div>

    ${approverCard(report)}`;

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
        groups: audienceFrom(f), items: [],
      });
      location.hash = `#/catalog/c/${encodeURIComponent(c.id)}`;
    } catch (err) {
      toast(err.message, "err");
    }
  });
}

const list = (s) => String(s || "").split(",").map((x) => x.trim()).filter(Boolean);

// approverCard is the standing list of approval rules that reach nobody.
//
// An approval rule's reference becomes a task's assignee or its candidate groups,
// and neither is checked when the rule is written nor reported when it fires: the
// approval is created, lands in nobody's inbox, and the order waits. The picker in
// the product form stops new ones being written; it shows a broken rule only to
// somebody who happens to open that product, and a rule written last year is
// exactly the one nobody opens.
//
// Three states, and the third is the reason this is a card rather than a line:
//
//   - problems, listed with what each names and why it reaches nobody;
//   - none, said as "none of the N I looked at" — an empty list with no count
//     reads as "nothing was checked", which is the answer somebody wants to see;
//   - unreadable, said out loud. The server refuses rather than guessing when it
//     cannot resolve accounts and groups, and the page must not turn that refusal
//     into a clean bill of health.
function approverCard(report) {
  if (report === null) {
    return `<div class="card" style="margin-top:18px; max-width:860px">
      <h3 style="margin:0 0 6px">Approvers</h3>
      <p class="muted" style="margin:0">This report could not be read, so nothing here says
        whether any approval rule reaches somebody.</p></div>`;
  }
  const problems = report.problems || [];
  const checked = report.checked || 0;
  if (!problems.length) {
    return `<div class="card" style="margin-top:18px; max-width:860px">
      <h3 style="margin:0 0 6px">Approvers</h3>
      <p class="muted" style="margin:0">Every approval rule reaches somebody
        &mdash; ${checked} product${checked === 1 ? "" : "s"} checked.</p></div>`;
  }
  const rows = problems.map((p) => `<tr>
    <td><code>${esc(p.itemId)}</code></td>
    <td><a href="#/catalog/c/${encodeURIComponent(p.homeCatalog)}">${esc(p.homeCatalog)}</a></td>
    <td>${esc(p.kind)}</td>
    <td>${p.ref ? `<code>${esc(p.ref)}</code>` : "<span class='muted'>—</span>"}</td>
    <td>${esc(p.why)}</td></tr>`).join("");
  return `<div class="card" style="margin-top:18px; max-width:860px">
    <h3 style="margin:0 0 6px">Approvers that reach nobody</h3>
    <p class="muted" style="max-width:62ch; margin:0 0 10px">${problems.length} of ${checked}
      products name an approver that resolves to nobody. The approval is still created when one
      is ordered; it lands in no inbox, and the order waits without saying why. Correct each in
      its home catalogue.</p>
    <table class="table">
      <thead><tr><th>Product</th><th>Home</th><th>Kind</th><th>Names</th><th>Why</th></tr></thead>
      <tbody>${rows}</tbody></table></div>`;
}

// ---------- Who, picked rather than typed ----------
//
// The header above says this page binds processes from what is deployed and never
// from free text, because a product naming a process nobody wrote is an order that
// fails while somebody waits for a laptop. The same rule had an exception: every
// field about *people* asked for an opaque id, typed from memory.
//
// It was worse than inconvenient. The audience field's placeholder read
// "kunde-a, kunde-b" — names — while Catalog.ReachedBy compares its entries against
// the group ids a session carries (`grp_…`). Following the placeholder produced a
// catalogue that reaches nobody, with no error anywhere, and the hint beside the
// sharing form sent the reader to Console → Organization, which shows group names
// and not their ids.
//
// The directory (ADR-0073) is what makes the picker possible at all: it is readable
// by any authenticated caller, where GET /api/v1/groups is admin-only and the people
// who fill catalogues are product managers.

// nameOfPrincipal resolves an id through the directory and answers with the id
// itself when it cannot. The id is the truth; the name is the courtesy.
function nameOfPrincipal(dir, id) {
  const hit = (dir || []).find((p) => p.id === id);
  return hit ? hit.name : id;
}

// groupChoices is what an audience may be: groups, and nothing else. ReachedBy
// compares a catalogue's audience against group ids, so a user named there would
// match nothing — and a picker offering one would manufacture the very mistake it
// exists to prevent. The sharing picker below offers both, because membership is a
// different question with a different answer.
function groupChoices(dir) {
  return (dir || []).filter((p) => p.type === "group")
    .slice().sort((a, b) => a.name.localeCompare(b.name));
}

// audienceField is a picker over the directory, and the old id field when there is
// no directory to pick from. Degrading is not a nicety: a picker with no options
// and no explanation is worse than the input it replaced, because it looks like an
// answer ("there are no groups") to a question it never asked.
function audienceField(dir, chosen) {
  const have = new Set(chosen || []);
  if (dir === null) {
    return `<label class="field">Audience (group ids, comma separated; empty reaches nobody)
      <input name="groups-raw" value="${esc((chosen || []).join(", "))}" autocomplete="off"></label>
      <p class="muted" style="margin:0 0 10px">The directory could not be read, so groups are
        named by id here for now.</p>`;
  }
  const choices = groupChoices(dir);
  // A group named here but gone from the directory keeps its box, checked. Dropping
  // it would let saving the form remove an audience silently, and leaving it out
  // unchecked would do the same on the next save.
  const orphans = (chosen || []).filter((id) => !choices.some((g) => g.id === id))
    .map((id) => ({ id, name: `${id} — no longer in the directory` }));
  const boxes = [...choices, ...orphans];
  if (!boxes.length) {
    return `<div class="field">Audience
      <p class="muted" style="margin:6px 0 0">No group exists yet, so this catalogue can reach
        nobody. An administrator creates groups under Console → Organization.</p></div>`;
  }
  return `<div class="field">Audience (none chosen reaches nobody)
    <div class="audience-boxes" style="display:grid; gap:4px; margin-top:6px">
      ${boxes.map((g) => `<label style="display:flex; gap:6px; align-items:center; font-weight:400">
        <input type="checkbox" name="groups" value="${esc(g.id)}"${have.has(g.id) ? " checked" : ""}>
        <span>${esc(g.name)}</span></label>`).join("")}
    </div></div>`;
}

// shareWhoField offers the directory, or asks for an id when there is none to
// offer. Both halves of a grant come from one choice here — the type and the id —
// because they are one fact about one person, and asking for them separately is
// how "user" ends up in front of a group id.
function shareWhoField(dir, cat) {
  if (dir === null) {
    return `<label class="field">Kind<select name="type">
        <option value="user">One account</option>
        <option value="group">A group — everybody in it</option>
      </select></label>
      <label class="field">Id
        <input name="id" required autocomplete="off" placeholder="usr_… or grp_…">
        <span class="muted">The directory could not be read, so the account or group is
          named by id here for now.</span></label>`;
  }
  // Whoever already holds a grant, and the owner, are not offered again: adding
  // somebody twice is not a second grant, it is a list that disagrees with itself.
  const taken = new Set([cat.ownerId, ...(cat.members || []).map((m) => (m.ref || {}).id)]);
  const choices = dir.filter((p) => !taken.has(p.id))
    .slice().sort((a, b) => a.name.localeCompare(b.name));
  if (!choices.length) {
    return `<p class="muted" style="margin:0 0 10px">Everybody in the directory already
      maintains this catalogue.</p>`;
  }
  return `<label class="field">Who<select name="who" required>
      ${choices.map((p) => `<option value="${esc(p.type)}|${esc(p.id)}">${esc(p.name)}${
    p.type === "group" ? " (group)" : ""}</option>`).join("")}
    </select></label>`;
}

// approvalFrom reads the approver the chosen kind asks for, and nothing else.
//
// Reading only the field in play is what clears a ref when somebody switches from
// "a named person" to "the orderer's superior": the old username would otherwise
// ride along in a rule that has no use for it, and sit in the catalogue looking
// like an answer to a question nobody asked.
function approvalFrom(f) {
  const kind = String(f.get("akind") || "none");
  const field = { fixed: "aref-fixed", role: "aref-role" }[kind];
  return { kind, ref: field ? String(f.get(field) || "").trim() : "" };
}

// audienceFrom reads whichever of the two controls was rendered. The picker names
// its boxes "groups"; the degraded input is "groups-raw", and its absence is what
// says a picker was drawn.
const audienceFrom = (f) =>
  f.get("groups-raw") === null ? f.getAll("groups").map(String) : list(f.get("groups-raw"));

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

// The orderable shapes, as one line of `id = name` each.
//
// A variant is an id and a name per language, and the form's other multilingual
// field — the product's own name — is one box per language. That does not scale
// here: the number of shapes is unbounded, so a box per shape per language is a
// grid nobody can read, and adding a shape would have to add controls to a form
// that is one static string with one submit handler.
//
// So it is the idiom this form already uses for a list of small records, the way
// `targets` is: one line each, split on the first separator, with the syntax stated
// on the control. `=` and not `:`, because a name legitimately carries a colon
// ("15 Zoll: Aluminium") and an id does not.
//
// **Only the languages this catalogue declares are rendered**, exactly as the
// product's name boxes are. A text in a language it does not declare belongs to a
// catalogue that does, and [parseVariants] carries it through untouched rather than
// showing it here to be edited by somebody who cannot read it.
export const variantLines = (variants, langs) =>
  (variants || []).map((v) => {
    const texts = v.texts || {};
    const named = (langs || []).filter((l) => texts[l]);
    // One declared language needs no tag in front of the name: a catalogue with a
    // single language would otherwise carry "de:" on every line it has.
    const names = (langs || []).length <= 1
      ? (named.length ? texts[named[0]] : "")
      : named.map((l) => `${l}:${texts[l]}`).join(" | ");
    return `${v.id || ""} = ${names}`.trimEnd();
  }).join("\n");

// parseVariants reads that back.
//
// The declared languages are rebuilt from the line and the rest of the texts are
// kept: a name removed from a line is removed from the variant, and a name in a
// language this catalogue does not declare survives a save made here. That is the
// same rule the product's own texts follow one level up, and it is the reason this
// takes the stored variants rather than building from the textarea alone.
//
// A line with no `=` is kept as an id with no name rather than dropped, for the
// reason parseTargets keeps a line with no colon: a line this form swallowed would
// be a shape somebody believes they entered. The portal falls back to the id, so
// the omission is visible rather than silent.
const parseVariants = (raw, langs, stored) => {
  const was = {};
  for (const v of stored || []) was[v.id] = v.texts || {};
  return String(raw || "").split("\n")
    .map((line) => line.trim())
    .filter(Boolean)
    .map((line) => {
      const at = line.indexOf("=");
      const id = (at < 0 ? line : line.slice(0, at)).trim();
      const rest = at < 0 ? "" : line.slice(at + 1).trim();
      const texts = { ...(was[id] || {}) };
      const given = {};
      if ((langs || []).length <= 1) {
        // The one declared language, or none at all. A catalogue declaring no
        // language has no name boxes either, so the name lands under an unnamed
        // language and the portal's own fallback still shows it.
        if (rest) given[(langs || [])[0] || ""] = rest;
      } else {
        for (const part of rest.split("|")) {
          const pair = part.trim();
          if (!pair) continue;
          const colon = pair.indexOf(":");
          // A part with no language tag in a multilingual catalogue is the first
          // declared language: it is what somebody types when they mean "the
          // obvious one", and refusing it would lose the name.
          const lang = colon < 0 ? langs[0] : pair.slice(0, colon).trim();
          const name = colon < 0 ? pair : pair.slice(colon + 1).trim();
          if (name) given[lang] = name;
        }
      }
      for (const l of [...(langs || []), ""]) {
        if (given[l]) texts[l] = given[l]; else delete texts[l];
      }
      return { id, texts };
    });
};

// eligibleField is who may RECEIVE this product, as a picker over the directory
// and as an id field when there is no directory to pick from.
//
// A sibling of [audienceField] and deliberately not a call to it, because the two
// mean opposite things when nothing is chosen. A catalogue with no audience reaches
// **nobody** — fail-closed, so a shop being filled in is not open to everybody. A
// product with no eligible group narrows **nothing**: the catalogue's audience
// already decided, and this only ever narrows it further (ADR-0347). One control
// with one wording would state the wrong default for one of the two, and the
// wording is the whole value of the control.
//
// Orphans keep their box for the reason they do there: not drawing a value and
// unticking it save the same result, so a group the directory has lost would be
// dropped by the next save of an unrelated field.
function eligibleField(dir, chosen) {
  const have = new Set(chosen || []);
  const hint = "Empty is the ordinary case and narrows nothing. This never opens "
    + "anything: whoever is outside the catalogue's audience cannot reach the product "
    + "whatever is chosen here. It is the <b>recipient</b> who is checked and never "
    + "the orderer, so a manager ordering for a new hire keeps working &mdash; and an "
    + "order refused by it is refused when it is placed, with the product named.";
  if (dir === null) {
    return `<label class="field wide">Who may receive it (group ids, comma separated)
      <span class="muted" style="display:block; margin:2px 0 6px">${hint}</span>
      <input name="eligible-raw" value="${esc((chosen || []).join(", "))}" autocomplete="off">
      <span class="muted">The directory could not be read, so groups are named by id
        here for now.</span></label>`;
  }
  const choices = groupChoices(dir);
  const orphans = (chosen || []).filter((id) => !choices.some((g) => g.id === id))
    .map((id) => ({ id, name: `${id} — no longer in the directory` }));
  const boxes = [...choices, ...orphans];
  if (!boxes.length) {
    return `<div class="field wide">Who may receive it
      <span class="muted" style="display:block; margin:2px 0 6px">${hint}</span>
      <p class="muted" style="margin:0">No group exists yet, so there is nothing to
        narrow to. An administrator creates groups under Console → Organization.</p></div>`;
  }
  return `<div class="field wide">Who may receive it (none chosen narrows nothing)
    <span class="muted" style="display:block; margin:2px 0 6px">${hint}</span>
    <div class="eligible-boxes" style="display:grid; gap:4px; margin-top:6px">
      ${boxes.map((g) => `<label style="display:flex; gap:6px; align-items:center; font-weight:400">
        <input type="checkbox" name="eligible" value="${esc(g.id)}"${have.has(g.id) ? " checked" : ""}>
        <span>${esc(g.name)}</span></label>`).join("")}
    </div></div>`;
}

// eligibleFrom reads whichever of the two controls was rendered, the way
// audienceFrom does: the picker names its boxes "eligible", the degraded input is
// "eligible-raw", and its absence is what says a picker was drawn.
const eligibleFrom = (f) =>
  f.get("eligible-raw") === null ? f.getAll("eligible").map(String) : list(f.get("eligible-raw"));

// The orderable window, as two dates.
//
// Nanoseconds in the record and days on the screen, and the conversion is the
// whole of the care this needs. A window is authored as "from this day until that
// day" — a product opens on the first of the month, not at 09:17:43 — so a
// maintainer types dates and the form decides what time of day each one means.
//
// **The end is the end of that day.** Somebody who writes 31.10. means the product
// is orderable on the 31st, and storing midnight would have closed it the moment
// the 30th ended. That is the off-by-one this pairing exists to prevent, and it is
// the reason the two sides are not converted by the same rule.
//
// UTC on both sides, because the record is the server's own Unix time and a
// browser's zone is not the server's. The hint says so rather than leaving a
// maintainer in Zurich to discover it from a product that opened at two in the
// morning.
const dayStart = (date) => date ? Date.parse(`${date}T00:00:00Z`) * 1e6 : 0;
const dayEnd = (date) => date ? Date.parse(`${date}T23:59:59.999Z`) * 1e6 : 0;

// dateOf renders one side back into the box it was typed in. Zero is unbounded and
// renders as an empty box, which is what an unbounded side means.
export const dateOf = (ns) => {
  const n = Number(ns) || 0;
  return n ? new Date(n / 1e6).toISOString().slice(0, 10) : "";
};

// lifecycleFrom reads the two boxes back.
//
// An omitted side stays zero rather than becoming a date, and the whole object is
// omitted when neither side is given: a product with `{from: 0, until: 0}` and one
// with no window at all are the same product, and writing the first would put a
// field in every record that says nothing.
const lifecycleFrom = (f) => {
  const from = dayStart(String(f.get("orderableFrom") || "").trim());
  const until = dayEnd(String(f.get("orderableUntil") || "").trim());
  return from || until ? { from, until } : {};
};

// productBody is what saving the product form posts.
//
// A function and not a block inside the submit handler, for the reason
// workerCreateBody is one: what a create carries is the thing worth proving, and a
// body assembled inside an event listener can only be proved by clicking through
// the Console. The handler above keeps what is genuinely its own — the toasts, the
// picture, the second write that offers a new product.
//
// **Saving a product REPLACES it, and this form does not render every field a
// product has**: the orderable window has no control here. Built from the controls
// alone, the body cleared it on every save and moved the creation date to today —
// silently, because a field it dropped is one it never shows. So the stored record
// is the seed and the form's own fields are laid over it. It also carries the
// revision, which turns a colleague's edit in between from a silent overwrite into
// a refusal (ADR-0376).
//
// The four that used to sit in that list — the shapes, the search terms, the
// eligible groups and the ceiling — are rendered now, and being rendered is what
// makes them the form's to write: a control somebody can empty has to be able to
// empty it, or it is a field that only ever grows.
export function productBody(f, { productID, homeCatalog, langs, stored }) {
  const was = stored || {};
  // Texts are merged rather than rebuilt, for the same reason one level down: a
  // product is shared between catalogues, this form renders one box per language
  // *this* catalogue declares, and a text in a language it does not declare belongs
  // to a catalogue that does. Emptying a box that is rendered still clears that
  // text, or a text could be added and never taken away.
  const texts = { ...(was.texts || {}) };
  for (const l of langs || []) {
    const val = String(f.get(`t-${l}`) || "").trim();
    if (val) texts[l] = val; else delete texts[l];
  }
  // The descriptions follow the names exactly, merge rule included: this form
  // renders one box per language *this* catalogue declares, and a description in a
  // language it does not declare belongs to a catalogue that does and must survive
  // a save made here. Emptying a rendered box still clears it, or a description
  // could be written and never taken back.
  const descriptions = { ...(was.descriptions || {}) };
  for (const l of langs || []) {
    const val = String(f.get(`d-${l}`) || "").trim();
    if (val) descriptions[l] = val; else delete descriptions[l];
  }
  return {
    ...was,
    id: productID, homeCatalog, state: f.get("state"), texts, descriptions,
    approval: approvalFrom(f),
    category: String(f.get("category") || "").trim(),
    productGroup: String(f.get("productGroup") || "").trim(),
    price: String(f.get("price") || "").trim(),
    configForm: f.get("configForm") || "",
    provisionProcess: f.get("provisionProcess") || "",
    deprovisionProcess: f.get("deprovisionProcess") || "",
    multipleAllowed: !!f.get("multipleAllowed"),
    targets: parseTargets(f.get("targets")),
    keywords: list(f.get("keywords")),
    eligible: eligibleFrom(f),
    maxDays: maxDaysFrom(f),
    lifecycle: lifecycleFrom(f),
    // Merged over what is stored, so a name in a language this catalogue does not
    // declare survives a save made here — the rule the texts above follow.
    variants: parseVariants(f.get("variants"), langs, was.variants),
  };
}

// maxDaysFrom reads the ceiling as a whole number of days, and reads anything that
// is not one as no ceiling at all.
//
// Zero and a negative are different statements and only one of them is sayable
// here: zero means the right does not end, and a negative would grant a right that
// ended before it began — publishing refuses it, and the number input cannot
// produce it. What a browser *can* hand over is an empty string or, on a field
// somebody pasted into, text; both become no ceiling rather than NaN, which would
// marshal as null and reach the server as a number nobody typed.
const maxDaysFrom = (f) => {
  const days = Math.trunc(Number(f.get("maxDays")));
  return Number.isFinite(days) && days > 0 ? days : 0;
};

// ---------- One catalogue ----------

export async function viewCatalogDetail({ api, apiBytes, toast, view, isSuperseded, me, enforced }, id) {
  let cat, items, releases, processes, forms, dir, people, unpublished;
  try {
    [cat, items, releases, processes, forms, dir, people, unpublished] = await Promise.all([
      api("GET", `/api/v1/catalogs/${encodeURIComponent(id)}`),
      api("GET", "/api/v1/catalog-products"),
      api("GET", `/api/v1/catalogs/${encodeURIComponent(id)}/releases`),
      api("GET", "/api/v1/processes"),
      // The forms a product may ask its orderer to fill in. Offered from what
      // exists, never as free text, for the reason the processes beside it are: a
      // product naming a form nobody wrote is a basket somebody cannot get past.
      api("GET", "/api/v1/forms").catch(() => []),
      // The principals directory (ADR-0073), for every place this page used to ask
      // somebody to type an id. null is "could not read it", which the pickers
      // degrade on; it is deliberately not [] , which would read as "nobody exists".
      api("GET", "/api/v1/principals").catch(() => null),
      // The accounts a task can be assigned to (ADR-0045), which is the one list
      // that carries a *username* — the principals directory carries display names
      // and ids, and an approval for a named person is matched by username.
      api("GET", "/api/v1/users/assignable").catch(() => null),
      // What publishing would change for the people ordering. .catch(() => null)
      // and deliberately not an empty answer: null is "not known" and draws
      // nothing, where an empty difference is drawn as "the portal is serving this
      // as it stands" — a claim a read that failed is in no position to make.
      api("GET", `/api/v1/catalogs/${encodeURIComponent(id)}/unpublished`).catch(() => null),
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
  // What this catalogue could still offer. Deliberately the list and not a
  // comparison of two counts: an id may be offered and no longer defined — the
  // product row says "offered but not defined" for exactly that — and one such
  // entry makes the counts equal while products nobody has offered are sitting
  // there, so the button to offer them was not drawn at all.
  const offerable = items.filter((it) => !offered.includes(it.id));

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

    <!-- What a catalogue is, and what it looks like: two questions about the catalogue
         itself rather than about anything in it, so they are read side by side at the
         top of the page instead of one under the other down the left edge. .grid2 is
         the console's own two-column pair, so this follows its breakpoint rather than
         inventing a third. -->
    <div class="grid2" style="margin:0 0 18px">
      <section>
      <h3 style="margin:0 0 10px">What this catalogue is</h3>
      <p class="muted" style="max-width:62ch">Its name, the languages it is offered in,
        its rank against the other catalogues, and who reaches it.</p>
      <div class="card">
      <form class="cat-meta">
        ${langs.map((l) => `<label class="field">Name (${esc(l)})<input name="t-${esc(l)}"
          value="${esc((cat.texts || {})[l] || "")}" autocomplete="off"></label>`).join("")}
        <label class="field">Languages<input name="languages" value="${esc(langs.join(", "))}" autocomplete="off"></label>
        <label class="field">Rank<input name="rank" type="number" value="${cat.rank}"></label>
        ${audienceField(dir, cat.groups)}
        <p class="muted" style="margin:0 0 10px">${(cat.groups || []).length
    ? "Everybody in these groups reaches this catalogue, unless a higher-ranked one reaches them first."
    : "<b>No group named, so nobody reaches this catalogue</b> — the portal will tell them no catalogue is assigned to them."}</p>
        <button class="btn" type="submit">Save</button>
      </form>
      </div>
      </section>
      <section>${appearanceCard(cat, me, enforced)}</section>
    </div>

    <h3>Products</h3>
    <p class="muted" style="max-width:62ch">A product is edited through its home catalogue.
      Everything offered here is orderable once this catalogue is published — a product in
      <b>draft</b> or <b>withdrawn</b> state is not.</p>
    <div class="product-cols cat-cols">
      <div class="product-list cat-main">
        ${offered.length ? `<div class="product-table"><table class="table">
          <thead><tr><th>Product</th><th>State</th><th>Approval</th><th>Provisioned by</th><th></th></tr></thead>
          <tbody>${offered.map((iid) => productRow(byID[iid], iid, langs, offered.length > 1)).join("")}</tbody></table></div>`
    : `<div class="empty"><p>Nothing offered yet.</p></div>`}

        <div class="row" style="margin-top:10px">
          <button class="btn" data-act="new-product">New product</button>
          ${offerable.length ? `<button class="btn ghost" data-act="add-existing">Offer an existing product</button>` : ""}
        </div>
      </div>
      <!-- One column, two panels, and never both at once: editing a product and
           arranging what it is made of are two questions about the same row, and a
           row has one answer open at a time. Both keep their own container so the
           code that fills each one says which it means. -->
      <aside class="product-side cat-side">
        <div class="product-editor"></div>
        <div class="assemble-editor"></div>
      </aside>
    </div>

    <h3 style="margin-top:26px">How the products relate</h3>
    <p class="muted" style="max-width:62ch">Three different questions, kept apart.
      <b>Structure</b> says what belongs to what, and is assembled per product.
      <b>Precedence</b> says what has to exist first, and it is what the fulfilment order
      is computed from; structure and precedence must both be free of cycles, and
      publishing proves it. <b>Incompatibility</b> says what one person may never hold
      together — the clerk who may create a supplier must not also approve payments to
      it. Neither right is wrong there; the combination is, and an order that would
      produce it is refused rather than reported afterwards.</p>
    <div class="cat-cols">
      <div class="cat-main">${edgeTable(cat.edges || [], byID, langs)}</div>
      ${offered.length > 1 ? `<aside class="cat-side">${edgeForm(offered, byID, langs)}</aside>`
    : `<p class="muted">Two products are needed before one can relate to another.</p>`}
    </div>

    ${sharingCard(cat, me, enforced, dir)}

    <h3 style="margin-top:26px">Releases</h3>
    <p class="muted" style="max-width:62ch">Publishing freezes everything above into a release.
      An order names one release and is immune to every edit made afterwards, which is why a
      catalogue can be reworked while approvals are still pending.</p>
    ${unpublishedCard(unpublished, langs)}
    <div class="row"><button class="btn" data-act="publish">Publish</button></div>
    <div class="publish-report"></div>
    ${releases.length ? `<table class="table" style="margin-top:12px">
      <thead><tr><th>Release</th><th>Published</th><th>Products</th></tr></thead>
      <tbody>${releases.map((r) => `<tr><td>${esc(r.id)}</td><td>${fmtTime(r.createdAt)}</td>
        <td>${(r.items || []).length}</td></tr>`).join("")}</tbody></table>`
    : `<p class="muted">Never published. Until it is, the portal shows this catalogue to nobody.</p>`}`;

  wire({ api, toast, view }, cat, items, byID, langs, procIDs, formList,
    mayShare(cat, me, enforced), mayTheme(me, enforced), dir, people);
}

// ---------- What publishing would change ----------
//
// The portal serves a release — a frozen copy — and every screen above serves the
// live catalogue. Both are right, and between one publish and the next they say
// different things with nothing on either saying so.
//
// One direction of that is invisible rather than merely unstated. Take a product
// out of a catalogue and it leaves this screen at once; the portal goes on
// offering it from the release. It is then absent from every screen its
// maintainer has and present on the one they do not, so the case most worth
// knowing about is the only one nothing could show. The release table underneath
// listed dates and left the reader to work out whether today's catalogue is one
// of them, which is not a question a date answers.
//
// The quiet case is drawn too, and for the same reason: "the portal is offering
// this exactly as it stands" is an answer somebody needs, and a panel that speaks
// up only when something is wrong cannot be told apart from one that failed to
// check.

// unpublishedCard states the difference between the newest release and the
// catalogue above it. langs picks the language a name is read in, the same way the
// product table picks it.
function unpublishedCard(diff, langs) {
  // Not known — an older server, or a read that failed. Nothing is drawn: silence
  // reads as "no answer here", where either sentence below would be a claim about
  // the portal that this page cannot support.
  if (!diff) return "";
  // Never published is already said under the release table, and in that state
  // every offered product counts as added — a list nobody needs to read to learn
  // that the portal shows this catalogue to nobody at all.
  if (!diff.released) return "";

  const against = `<span class="muted">against ${esc(diff.releaseId)}, published
    ${esc(fmtTime(diff.releasedAt))}</span>`;
  const added = diff.added || [], removed = diff.removed || [], changed = diff.changed || [];
  if (!added.length && !removed.length && !changed.length) {
    return `<div class="card portal-current" style="margin:12px 0">
      <div class="row"><span class="pill ok">Nothing to publish</span>
      The portal is offering this catalogue exactly as it stands.</div>
      <p class="muted" style="margin:6px 0 0">${against}</p></div>`;
  }

  // The state is worth saying on a product about to be added, and only there: a
  // draft or withdrawn product is offered but not orderable, so publishing it
  // changes the catalogue and changes nothing for the person ordering. Said here,
  // that is one sentence; found afterwards, it is a republish.
  const names = (list, withState) => `<ul style="margin:0; padding-left:18px">${list
    .map((x) => `<li>${esc(textOf(x.texts, langs, x.id))}
      <span class="muted">${esc(x.id)}</span>${withState && x.state && x.state !== "active"
    ? ` <span class="pill warn">${esc((STATES.find((st) => st.id === x.state) || {}).name || x.state)}</span>`
    : ""}</li>`).join("")}</ul>`;

  const block = (list, kind, title, note, withState) => list.length
    ? `<div class="portal-${kind}" style="margin:12px 0 0"><b>${title} (${list.length})</b>
        <p class="muted" style="margin:2px 0 6px; max-width:62ch">${note}</p>
        ${names(list, withState)}</div>`
    : "";

  // Removed first, because it is the one the reader cannot find anywhere else.
  return `<div class="card portal-behind" style="margin:12px 0; border-color:#b26b00">
    <div class="row"><span class="pill warn">Unpublished changes</span>${against}</div>
    ${block(removed, "removed", "Still offered on the portal", `Not in this catalogue any more.
      The release goes on offering them, and this is the only screen that says so —
      publishing is what takes them away from the people ordering.`, false)}
    ${block(added, "added", "Not on the portal yet", `Offered here and absent from the release.
      Publishing puts them in front of the people ordering; one that is still in draft
      or withdrawn is published along with the rest and stays unorderable.`, true)}
    ${block(changed, "changed", "Edited since the release", `Offered in both. The portal is showing
      the name, description, price or state the product had when it was published.`, false)}
  </div>`;
}

function productRow(it, iid, langs, canAssemble) {
  if (!it) {
    return `<tr><td>${esc(iid)}</td><td colspan="3" class="muted">offered but not defined —
      publishing will refuse this</td>
      <td class="row-actions"><button class="btn ghost danger" data-act="drop" data-id="${esc(iid)}">remove</button></td></tr>`;
  }
  const ap = it.approval || {};
  const kind = APPROVAL_KINDS.find((k) => k.id === ap.kind) || APPROVAL_KINDS[0];
  return `<tr>
    <td>${esc(textOf(it.texts, langs, it.id))}<div class="muted">${esc(it.id)}</div></td>
    <td>${esc((STATES.find((s) => s.id === it.state) || {}).name || it.state || "—")}</td>
    <td>${esc(kind.name)}${ap.ref ? ` <span class="muted">(${esc(ap.ref)})</span>` : ""}</td>
    <td>${esc(it.provisionProcess || "—")}</td>
    <td class="row-actions"><button class="btn ghost" data-act="edit" data-id="${esc(it.id)}">edit</button>
      ${canAssemble ? `<button class="btn ghost" data-act="assemble" data-id="${esc(it.id)}">assemble</button>` : ""}
      <button class="btn ghost danger" data-act="drop" data-id="${esc(it.id)}">remove</button></td>
  </tr>`;
}

// ---------- The construction kit ----------
//
// A catalogue is built out of services that each provision themselves; what a
// *product* adds is an arrangement — which of those services come with it, and
// which are offered beside it. Said as edges, that arrangement is a set of triples
// spread across a table sorted by relationship, and assembling one product means
// finding its rows among everybody else's and adding them one at a time.
//
// The kit asks the arrangement as the question somebody actually has, once per
// service and all at once: not part of it, included, or optional. The three answers
// are exhaustive and mutually exclusive, which is what makes them radios — and one
// save writes the whole arrangement, because "included" and "not part of it" are
// the same control and a screen that could only add would be the edge table again.
//
// It is deliberately structure only. Precedence is a statement about two products
// and belongs to neither of them, so it stays where a pairwise form can ask it.

// assembleKit draws the arrangement of one product as it stands.
function assembleKit(pid, offered, byID, langs, edges) {
  const name = (i) => textOf((byID[i] || {}).texts, langs, i);
  // A thing cannot be part of itself, so it is not among its own parts. The row
  // would be refused on save anyway; offering it and then refusing it is a worse
  // screen than never offering it.
  const parts = offered.filter((i) => i !== pid);
  const standing = (other) => {
    const e = (edges || []).find((x) =>
      x.from === pid && x.to === other && STRUCTURE_IDS.includes(x.kind));
    return e ? e.kind : "none";
  };
  // A cell carries the control and not the word above it. The column says which
  // answer it is, once, and repeating that in every cell cost the table about 180px
  // of width — which is the difference between a kit that fits beside the product
  // list and one that scrolls sideways in it. The name a cell loses on screen it
  // keeps for a reader who cannot see the column: aria-label names the product and
  // the answer together, so a radio is never announced as a bare choice, and the
  // title still explains what the answer means on hover.
  const rows = parts.map((other) => {
    const now = standing(other);
    return `<tr><td>${esc(name(other))}<div class="muted">${esc(other)}</div></td>
      ${STRUCTURE_CHOICES.map((c) => `<td><label class="field inline" title="${esc(c.what)}">
        <input type="radio" name="part-${esc(other)}" value="${esc(c.id)}"${
  c.id === now ? " checked" : ""} aria-label="${esc(name(other))}: ${esc(c.name)}"></label></td>`).join("")}</tr>`;
  }).join("");
  // No margin spelled here: the card is read in two layouts — beside the list in a
  // column of its own, and stacked under it on a narrow screen — and an inline style
  // would win over both, standing the panel twelve pixels off the row it belongs to.
  return `<form class="assemble card" data-product="${esc(pid)}">
    <h4 style="margin:0 0 4px">What ${esc(name(pid))} is made of</h4>
    <p class="muted" style="max-width:62ch; margin:0 0 10px">Every other product this catalogue
      offers, and where each one stands with respect to this one. <b>Included</b> is ordered
      with it and cannot be deselected; <b>optional</b> is offered beside it and ordered only
      if it is ticked. Saving writes the whole arrangement at once.</p>
    <table class="table">
      <thead><tr><th>Product</th>${STRUCTURE_CHOICES.map((c) =>
    `<th title="${esc(c.what)}">${esc(c.name)}</th>`).join("")}</tr></thead>
      <tbody>${rows}</tbody></table>
    <div class="row" style="margin-top:10px">
      <button class="btn" type="button" data-act="assemble-save">Save the arrangement</button>
      <button class="btn neutral" type="button" data-act="assemble-cancel">Cancel</button>
    </div>
  </form>`;
}

// contains reports whether `whole` already carries `part`, directly or through
// another product, following structure edges only.
//
// It is the question a loop is made of: putting B inside A is a cycle exactly when
// B already contains A. Publishing proves the same thing over the whole graph and
// refuses — but three screens and one publish later, about a catalogue somebody has
// since added to, which is why the kit asks it at the moment the choice is made.
function contains(edges, whole, part) {
  const seen = new Set();
  const walk = (at) => {
    if (at === part) return true;
    if (seen.has(at)) return false;
    seen.add(at);
    return (edges || []).some((e) =>
      e.from === at && STRUCTURE_IDS.includes(e.kind) && walk(e.to));
  };
  return walk(whole);
}

// pairKey names an unordered pair, so the two directions of one symmetric fact
// answer to the same key.
const pairKey = (a, b) => [a, b].sort().join("\u0000");

function edgeTable(edges, byID, langs) {
  const name = (id) => esc(textOf((byID[id] || {}).texts, langs, id));
  const row = (e, act, data) => {
    const k = EDGE_KINDS.find((x) => x.id === e.kind);
    return `<tr><td>${name(e.from)}</td>
      <td class="muted">${esc(k ? k.name : e.kind)}</td>
      <td>${name(e.to)}</td>
      <td class="row-actions"><button class="btn ghost danger" data-act="${act}" ${data}>remove</button></td></tr>`;
  };
  const rows = (kind) => edges.filter((e) => e.kind === kind)
    .map((e) => row(e, "unedge",
      `data-edge="${esc(e.from)}|${esc(e.kind)}|${esc(e.to)}"`)).join("");

  // An incompatibility is the only symmetric kind: "A must not be held with B" is
  // exactly "B must not be held with A", and publishing writes both directions into
  // the release whichever way round it was authored. So one fact is drawn as one
  // row even where both directions were stored, and removing it removes both —
  // taking away one direction would leave the other, and the row nobody could
  // account for would come straight back.
  const seen = new Set();
  const incompatibility = edges.filter((e) => e.kind === "excludes").filter((e) => {
    const key = pairKey(e.from, e.to);
    if (seen.has(key)) return false;
    seen.add(key);
    return true;
  }).map((e) => row(e, "unexclude",
    `data-pair="${esc(e.from)}|${esc(e.to)}"`)).join("");

  const structure = rows("composition") + rows("aggregation");
  const precedence = rows("requires");
  return `
    <h4 style="margin:14px 0 4px">Structure</h4>
    ${structure ? `<table class="table"><tbody>${structure}</tbody></table>`
    : `<p class="muted">Nothing contains anything else.</p>`}
    <h4 style="margin:14px 0 4px">Precedence</h4>
    ${precedence ? `<table class="table"><tbody>${precedence}</tbody></table>`
    : `<p class="muted">Nothing has to exist before anything else.</p>`}
    <h4 style="margin:14px 0 4px">Incompatibility</h4>
    ${incompatibility ? `<table class="table"><tbody>${incompatibility}</tbody></table>`
    : `<p class="muted">Nothing is incompatible with anything else.</p>`}`;
}

function edgeForm(offered, byID, langs) {
  const opts = offered.map((i) =>
    `<option value="${esc(i)}">${esc(textOf((byID[i] || {}).texts, langs, i))}</option>`).join("");
  // No width and no margin here: like every form on this page that is read both beside
  // its list and stacked under it, only the stylesheet knows which layout is in force.
  return `<form class="edge-new card">
    <h4 style="margin:0 0 10px">Relate two products</h4>
    <label class="field">From<select name="from">${opts}</select></label>
    <label class="field">Relationship<select name="kind">
      ${PAIRWISE_KINDS.map((k) => `<option value="${k.id}" title="${esc(k.what)}">${esc(k.name)} — ${esc(k.what)}</option>`).join("")}
    </select></label>
    <label class="field">To<select name="to">${opts}</select></label>
    <button class="btn" type="submit">Add</button>
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
    return `<h3 style="margin:0 0 10px">How this catalogue looks</h3>
      <p class="muted" style="max-width:62ch">${accent || theme.typeface
    ? `Its own appearance: ${esc(accent || "the instance colour")}, ${
      esc(theme.typeface || "the instance typeface")}.`
    : "The instance's own appearance."} Changing it is an administrator's.</p>`;
  }

  return `<h3 style="margin:0 0 10px">How this catalogue looks</h3>
    <p class="muted" style="max-width:62ch">The portal and the approval page paint
      themselves from this, so a customer sees their own brand rather than yours. Leave
      both empty and the catalogue wears the instance's appearance. Setting it is an
      administrator's; an editor may change what the catalogue offers and not whose it
      looks like.</p>
    <div class="card">
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
          <button class="btn" type="submit">Save appearance</button>
          ${accent || theme.typeface
    ? `<button class="btn neutral" type="button" data-act="theme-clear">Wear the instance's</button>` : ""}
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
        <button class="btn neutral" type="button" data-act="logo-upload">Upload</button>
        <button class="btn ghost danger" type="button" data-act="logo-remove">Remove</button>
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

function sharingCard(cat, me, enforced, dir) {
  const can = mayShare(cat, me, enforced);
  const members = cat.members || [];
  const rows = members.map((m) => {
    const r = MEMBER_ROLES.find((x) => x.id === m.role);
    const ref = m.ref || {};
    return `<tr><td>${esc(ref.type === "group" ? "Group" : "User")}</td>
      <td>${esc(nameOfPrincipal(dir, ref.id || ""))}
        <div class="muted"><code>${esc(ref.id || "")}</code></div></td>
      <td>${esc(r ? r.name : m.role)}</td>
      <td class="row-actions">${can ? `<button class="btn ghost danger" data-act="unshare"
        data-ref="${esc(ref.type || "user")}|${esc(ref.id || "")}">remove</button>` : ""}</td></tr>`;
  }).join("");

  return `<h3 style="margin-top:26px">Who maintains this catalogue</h3>
    <p class="muted" style="max-width:62ch">The role says somebody may maintain catalogues at
      all; this says which ones. Sharing is the owner's: an editor may change this catalogue
      and publish it, and may not change this list.</p>
    <p class="muted">Owner: ${cat.ownerId
    ? `${esc(nameOfPrincipal(dir, cat.ownerId))} <code>${esc(cat.ownerId)}</code>`
    : "<code>—</code>"}${
      cat.ownerId ? "" : " <span>(created before ownership, or with authentication off)</span>"}</p>
    <div class="cat-cols">
      <div class="cat-main">${members.length ? `<table class="table">
        <thead><tr><th>Kind</th><th>Id</th><th>May</th><th></th></tr></thead>
        <tbody>${rows}</tbody></table>`
    : `<p class="muted">Nobody else. Only the owner and administrators maintain it.</p>`}
      </div>
      ${can ? `<aside class="cat-side"><form class="share-new card">
      <h4 style="margin:0 0 10px">Let somebody else maintain it</h4>
      ${shareWhoField(dir, cat)}
      <label class="field">May<select name="role">
        ${MEMBER_ROLES.map((r) => `<option value="${r.id}">${esc(r.name)} — ${esc(r.what)}</option>`).join("")}
      </select></label>
      <button class="btn" type="submit">Add</button>
    </form></aside>`
    : `<p class="muted">You maintain this catalogue but do not own it, so who else may is
      the owner's to change.</p>`}
    </div>`;
}

// productForm renders the editor for one product, or for a new one.
//
// It is read in two passes, because it answers two questions to two different
// readers, and used to interleave them. A product manager writing a laptop into
// the catalogue asks "what will people see?" — a name, a heading, a price. Only
// then does anybody ask "and what happens when somebody orders it?" — who
// approves, which process runs, what the target systems call it. The old form
// alternated between the two four times down a single column, so answering either
// question meant reading past the other.
//
// Two columns, over the grid the console already has (.grid2's breakpoint, reused
// rather than re-chosen). Short fields pair up; anything carrying an explanation
// keeps the full width, because prose in a half column is a column of syllables.
//
// The palette is the console's own tokens throughout. Nothing here introduces a
// colour: the sections are separated by --border, their hints are --muted, and a
// theme change reaches this form because it never spelled a colour out.
// savePicture puts a chosen picture on the product, or takes the existing one
// away. Does nothing when the form says nothing about it, which is the ordinary
// save.
//
// A failure here is reported and not raised: the product is saved by the time this
// runs, and a page that reported "not saved" because an image did not upload would
// send somebody looking for a change that is in fact stored.
async function savePicture({ api, apiBytes, toast }, pid, f) {
  const file = f.get("picture");
  const path = `/api/v1/catalog-products/${encodeURIComponent(pid)}/picture`;
  try {
    if (file && file.size > 0) {
      await apiBytes("PUT", path, file);
      return;
    }
    if (f.get("dropPicture")) await api("DELETE", path);
  } catch (err) {
    toast(`The product was saved, but its picture was not: ${err.message}`, "err");
  }
}

// partOfNote says which products in THIS catalogue carry this one, because that is
// what decides whether the two headings below are read at all.
//
// The portal's cascade reads Kategorie › Produktgruppe › Produkt › Services. The
// two upper columns are collected from the products nothing contains and the two
// lower ones from the containment graph, so a product that is a part is reached
// through the product carrying it and its own heading is never read. The form
// offers both fields on every product regardless, and its hint claimed the heading
// was where the product sits — so a maintainer could fill in a column that had
// already stopped reading the field.
//
// **It is a note and not a hidden field.** Containment belongs to a catalogue and
// not to the product: an item is referenced by several catalogues, each with its
// own edges, so the same product is legitimately a part here and offered on its own
// next door. Hiding the controls would hide a heading that another catalogue reads.
// So the honest answer is to say where this one stands, in this catalogue, and
// leave the decision with the person reading it.
//
// Empty for a new product, which nothing can carry yet, and empty for a root —
// where the fields do what they say and a note would be noise.
function partOfNote(it, cat, items, langs) {
  if (!it) return "";
  const byID = {};
  for (const i of items || []) byID[i.id] = i;
  const wholes = (cat.edges || [])
    .filter((e) => e.to === it.id && (e.kind === "composition" || e.kind === "aggregation"))
    .map((e) => esc(textOf((byID[e.from] || {}).texts, langs, e.from)));
  if (!wholes.length) return "";
  const carriers = wholes.length === 1
    ? `<b>${wholes[0]}</b>`
    : wholes.slice(0, -1).map((w) => `<b>${w}</b>`).join(", ")
      + ` and <b>${wholes[wholes.length - 1]}</b>`;
  const one = wholes.length === 1;
  return `<p class="form-sec-hint">In this catalogue ${carriers} ${one ? "carries" : "carry"}
    this product, so the portal offers it as a <b>Service</b> behind
    ${one ? "it" : "them"} and not as a Marktleistung of its own. The two headings below
    are read off the products nothing contains, so a requester reaches this product under
    ${one ? `${wholes[0]}&rsquo;s` : "the carrying product&rsquo;s"} heading and not under
    what is written here. It still counts in another catalogue that offers this product on
    its own. To have it offered in its own right, open
    ${one ? `${wholes[0]}&rsquo;s` : "the carrying product&rsquo;s"} row and answer
    &ldquo;${esc(STRUCTURE_CHOICES[0].name)}&rdquo; for this product under
    <b>assemble</b>.</p>`;
}

function productForm(it, cat, langs, procIDs, formList, items, dir, people) {
  const v = it || { state: "draft", approval: { kind: "none" }, texts: {} };
  const ap = v.approval || {};
  const opt = (id, sel, label) =>
    `<option value="${esc(id)}"${id === sel ? " selected" : ""}>${esc(label)}</option>`;
  const procSelect = (name, sel) => `<select name="${name}">
      <option value="">— none —</option>
      ${procIDs.map((p) => opt(p, sel || "", p)).join("")}
      ${sel && !procIDs.includes(sel) ? opt(sel, sel, `${sel} (not deployed)`) : ""}
    </select>`;
  const section = (title, hint) => `<h4 class="form-sec">${esc(title)}</h4>
    <p class="form-sec-hint">${hint}</p>`;
  // No width and no margin spelled here: the card is read in two layouts — beside the
  // list in a column of its own, and stacked under it on a narrow screen — and only
  // the stylesheet knows which one is in force. An inline style would win over both.
  return `<div class="card">
    <h3 style="margin:0 0 10px">${it ? "Edit product" : "New product"}</h3>
    <form class="product-form" data-editing="${esc(it ? it.id : "")}">
      ${section("What the catalogue shows",
    "The product as somebody browsing it meets it. Everything here is read by whoever orders.")}
      <label class="field">Id${it ? "" : " (short, stable, never renamed)"}
        <input name="id" value="${esc(v.id || "")}" ${it ? "readonly" : "required"} autocomplete="off"
          placeholder="laptop"></label>
      ${langs.map((l) => `<label class="field">Name (${esc(l)})<input name="t-${esc(l)}"
        value="${esc((v.texts || {})[l] || "")}" autocomplete="off"></label>`).join("")}
      <label class="field wide">Description
        <span class="muted" style="display:block; margin:2px 0 6px">What the thing
          <i>is</i>, for somebody who read the name and is still not sure. Optional:
          most products do not need one. <b>Write it in every language or in
          none</b> &mdash; a release refuses a product described to one audience and
          not another, because the second audience gets an empty panel rather than a
          shorter one.</span>
        ${langs.map((l) => `<textarea name="d-${esc(l)}" rows="3" class="wide"
          placeholder="${esc(l)}">${esc((v.descriptions || {})[l] || "")}</textarea>`).join("")}</label>
      ${partOfNote(it, cat, items, langs)}
      <label class="field wide">Category
        <span class="muted" style="display:block; margin:2px 0 6px">The heading the
          portal groups this product under &mdash; <code>Arbeitsplatz</code>,
          <code>Kommunikation</code>. A heading and nothing else: it has no ordering of
          its own (the portal sorts alphabetically), no translation, and two spellings
          are two headings. Leave it empty and the product sits under the portal's
          heading for those that carry none. <b>Read off the products nothing
          contains</b>: the portal reaches a part through the product that carries it,
          so a heading written on a part is never read there.</span>
        <input name="category" value="${esc(v.category || "")}" autocomplete="off"
          list="known-categories" placeholder="Arbeitsplatz">
        <datalist id="known-categories">${
  [...new Set(items.map((i) => (i.category || "").trim()).filter(Boolean))].sort()
    .map((c) => `<option value="${esc(c)}"></option>`).join("")}</datalist></label>
      <label class="field wide">Product group
        <span class="muted" style="display:block; margin:2px 0 6px">One level below the
          category, and the portal reads the two as a chain: <b>Kategorie &rsaquo;
          Produktgruppe &rsaquo; Produkt &rsaquo; Services</b>. A string like the heading
          above, with the same costs &mdash; no ordering of its own, no translation, two
          spellings are two groups. The group has no record and therefore no category of
          its own: the chain is assembled from the products that carry both, so a group
          whose products sit in two categories appears under both. Leave it empty and the
          product sits under the portal's group for those that carry none. Read off the
          same products the heading above is.</span>
        <input name="productGroup" value="${esc(v.productGroup || "")}" autocomplete="off"
          list="known-groups" placeholder="Mobile Geräte">
        <datalist id="known-groups">${
  [...new Set(items.map((i) => (i.productGroup || "").trim()).filter(Boolean))].sort()
    .map((g) => `<option value="${esc(g)}"></option>`).join("")}</datalist></label>
      <label class="field wide">Search terms
        <span class="muted" style="display:block; margin:2px 0 6px">Words somebody might
          search for that are <b>not</b> the product's name &mdash; synonyms, the vendor's
          own term, the abbreviation everybody uses, the thing it replaced. Comma
          separated. The portal searches the id, every name the product carries and these;
          the story this serves is finding a service <i>when the exact name is not
          known</i>, which is the person a search over names alone cannot help. One flat
          list and <b>not one per language</b>: a synonym list is for finding, and a
          searcher's language is not the catalogue's &mdash; somebody reading a German
          catalogue types <code>laptop</code> as readily as <code>Notebook</code>.</span>
        <input name="keywords" value="${esc((v.keywords || []).join(", "))}"
          autocomplete="off" placeholder="Notebook, mobiles Gerät, M365"></label>
      <label class="field wide">Cost
        <span class="muted" style="display:block; margin:2px 0 6px">Written as you want it
          read — <code>CHF 1'200.&ndash;</code>, <code>49.&ndash; / Monat</code>,
          <code>im Grundpaket enthalten</code>. It is <b>shown and never computed</b>:
          nothing adds these up, because a total would need a currency, a rate and a date
          that are your finance rules and not the catalogue's. It is frozen into the
          release, so an approver's figure stays the figure they decided on. Leave it
          empty to say nothing about cost.</span>
        <input name="price" value="${esc(v.price || "")}" autocomplete="off"
          placeholder="CHF 1'200.&ndash;"></label>
      <label class="field inline wide"><input type="checkbox" name="multipleAllowed"
        ${v.multipleAllowed ? "checked" : ""}> May be held more than once
        <span class="muted">— two licences, two mailboxes</span></label>
      <label class="field wide">Orderable shapes
        <span class="muted" style="display:block; margin:2px 0 6px">One per line, as
          <code>id = name</code> &mdash; <code>gross = 15 Zoll</code>. A laptop's size, a
          licence tier: the same product, ordered in one of several shapes.${langs.length > 1
    ? ` This catalogue declares ${langs.length} languages, so name each shape per
          language as <code>gross = ${langs.map((l) => `${esc(l)}:…`).join(" | ")}</code>;
          a name with no language in front of it is filed under
          <code>${esc(langs[0])}</code>.` : ""}
          They are <b>unordered on purpose</b> &mdash; &ldquo;higher&rdquo; is meaningful
          for a tier and meaningless for Windows against Linux &mdash; so the basket asks
          the orderer, and <b>refuses to place the order until a shape is chosen</b> for
          every position that has any. How many may be ticked is not asked here:
          &ldquo;may be held more than once&rdquo; above already answers it. Leave it empty for a product with one
          shape, which is most of them.</span>
        <textarea name="variants" rows="3" spellcheck="false"
          placeholder="gross = 15 Zoll">${esc(variantLines(v.variants, langs))}</textarea></label>
      <div class="field wide">Picture
        <span class="muted" style="display:block; margin:2px 0 6px">A photograph of the
          thing or the vendor's mark, shown to whoever is choosing — PNG, JPEG or SVG,
          served back exactly as uploaded. It is <b>not frozen into a release</b>: a
          better photograph of the same laptop is not a different laptop, so a new
          picture appears on old orders too. Most of a catalogue reads as a list of
          names; this is the one thing that makes it read as a shop.</span>
        ${it ? `<img class="product-picture" alt=""
          src="/api/v1/catalog-products/${encodeURIComponent(v.id)}/picture?t=${Date.now()}"
          onerror="this.remove()">` : ""}
        <input type="file" name="picture" accept="image/png,image/jpeg,image/svg+xml">
        ${it ? `<label class="field inline"><input type="checkbox" name="dropPicture">
          Remove the picture this product has</label>` : ""}</div>

      ${section("How an order is handled",
    "What happens after somebody puts it in the basket. None of it is shown in the catalogue, " +
    "except that an approval is needed at all.")}
      <label class="field wide">Orderable window
        <span class="muted" style="display:block; margin:2px 0 6px">The days between
          which this product may be ordered. Both ends are <b>inclusive</b> and either
          may be left empty: empty on the left is &ldquo;from whenever it is published&rdquo;,
          empty on the right is &ldquo;until somebody withdraws it&rdquo;, and empty on both
          is the ordinary product. The window is what lets a catalogue be
          <b>published ahead of the date it opens</b> &mdash; the product is visible,
          and the portal will not put it in a basket before the first day or after the
          last. An order outside it is <b>refused by the server</b>, not only hidden by
          the portal. Dates are the server's own (UTC), so a window that matters to the
          hour is not what this field is for.</span>
        <div class="row">
          <label>from <input name="orderableFrom" type="date"
            value="${esc(dateOf((v.lifecycle || {}).from))}"></label>
          <label>until <input name="orderableUntil" type="date"
            value="${esc(dateOf((v.lifecycle || {}).until))}"></label>
        </div></label>
      <label class="field">State<select name="state">
        ${STATES.map((s) => opt(s.id, v.state || "draft", `${s.name} — ${s.what}`)).join("")}
      </select></label>
      <label class="field">Approval<select name="akind">
        ${APPROVAL_KINDS.map((k) => opt(k.id, ap.kind || "none", `${k.name} — ${k.what}`)).join("")}
      </select></label>
      ${approverField(ap, dir, people)}
      ${eligibleField(dir, v.eligible)}
      <label class="field wide">Details the orderer fills in
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
      <label class="field wide">How long the right may last
        <span class="muted" style="display:block; margin:2px 0 6px">In days, or
          <code>0</code> for a right that does not end &mdash; which is the ordinary case.
          A <b>ceiling declared as policy</b> ("nobody holds this for more than ninety
          days") and not a date somebody chose: an order cannot yet name a shorter end
          within it. It reaches a grant through the release like the bindings above, so a
          ceiling relaxed next week does not lengthen a right granted this week under the
          stricter one. It never applies to a right found by a commissioning load: that
          start is the day the right was discovered, and a ceiling measured from it would
          schedule the whole estate to expire on the anniversary of the switch-on.</span>
        <input name="maxDays" type="number" min="0" step="1" inputmode="numeric"
          value="${esc(String(v.maxDays || 0))}" autocomplete="off"></label>
      <label class="field wide">Known in the target systems as
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
      ${maintainersNote(cat, dir)}
      <div class="row">
        <button class="btn" type="submit">Save</button>
        <button class="btn neutral" type="button" data-act="cancel-product">Cancel</button>
      </div>
    </form>
  </div>`;
}

// approverField is the approver control, and there are three of them because the
// question has three answers: a username for a named person, a group for a group,
// and nothing at all for the two kinds that resolve without one. Only the one the
// chosen kind asks for is shown.
//
// **The value differs per kind, and that is not a preference.** genehmigung-fix.bpmn
// puts the ref in `assignee`, and holdsTask compares an assignee against the
// caller's *username*; genehmigung-rolle.bpmn puts it in `candidateGroups`, matched
// against group *ids* first and names only after. So a named person is a username
// and a group is an id — an id in the first or a name in the second matches nobody,
// and the order waits for an approval that reaches no inbox. That is the most
// expensive typo left in this screen, because nothing reports it: the approval is
// created, it simply sits there.
//
// APPROVAL_KINDS already carries which sort each kind wants, in its `ref` field, so
// the table above is the one place that says it.
function approverField(ap, dir, people) {
  const kind = ap.kind || "none";
  const ref = ap.ref || "";
  const box = (forKind, body) => `<label class="field wide aref-for" data-kind="${forKind}"${
    kind === forKind ? "" : " hidden"}>Approver${body}</label>`;

  // A username, from the list a task assignee is picked from (ADR-0045) — not from
  // the principals directory, which carries display names and ids and cannot answer
  // "what is this person's username".
  const person = people === null
    ? `<input name="aref-fixed" value="${esc(kind === "fixed" ? ref : "")}" autocomplete="off"
         placeholder="username">
       <span class="muted" style="display:block; margin:4px 0 0">The account list could not be
         read, so the approver is named by username here for now.</span>`
    : `<select name="aref-fixed">
         <option value="">— nobody yet —</option>
         ${people.map((u) => `<option value="${esc(u.username)}"${
      kind === "fixed" && u.username === ref ? " selected" : ""}>${
      esc(u.displayName ? `${u.displayName} (${u.username})` : u.username)}</option>`).join("")}
         ${kind === "fixed" && ref && !people.some((u) => u.username === ref)
      ? `<option value="${esc(ref)}" selected>${esc(ref)} — no such account</option>` : ""}
       </select>
       <span class="muted" style="display:block; margin:4px 0 0">Always this person. The
         approval lands in their inbox by username.</span>`;

  const groups = dir === null
    ? `<input name="aref-role" value="${esc(kind === "role" ? ref : "")}" autocomplete="off"
         placeholder="grp_…">
       <span class="muted" style="display:block; margin:4px 0 0">The directory could not be
         read, so the group is named by id here for now.</span>`
    : `<select name="aref-role">
         <option value="">— no group yet —</option>
         ${groupChoices(dir).map((g) => `<option value="${esc(g.id)}"${
      kind === "role" && g.id === ref ? " selected" : ""}>${esc(g.name)}</option>`).join("")}
         ${kind === "role" && ref && !groupChoices(dir).some((g) => g.id === ref)
      ? `<option value="${esc(ref)}" selected>${esc(ref)} — no such group</option>` : ""}
       </select>
       <span class="muted" style="display:block; margin:4px 0 0">Whoever in the group picks it
         up. Stored as the group's id, so renaming the group does not lose the approver.</span>`;

  return box("fixed", person) + box("role", groups);
}

// maintainersNote answers, where it is asked, a question this form has no field
// for: who besides me may look after this product.
//
// There is no deputy on a product, and that is a decision rather than a gap.
// Item.HomeCatalog records it: an item is referenced by catalogues rather than
// owned by one, so access cannot be inherited from "the catalogue it is in", and a
// per-item member list is the per-artifact ACL ADR-0071 weighed and refused — a
// grant per product makes sharing a bundle N actions and the management surface
// explodes. Maintenance is the home catalogue's, and a deputy is an editor there.
//
// So the form does not offer a control. It names the people the answer already has,
// and says where it is changed — which is what somebody looking for a missing field
// actually needs.
function maintainersNote(cat, dir) {
  const editors = (cat.members || []).filter((m) => m.role === "editor")
    .map((m) => nameOfPrincipal(dir, (m.ref || {}).id || ""));
  const who = [cat.ownerId ? nameOfPrincipal(dir, cat.ownerId) : null, ...editors].filter(Boolean);
  return `<p class="form-sec-hint wide">Maintained by ${who.length
    ? `<b>${who.map(esc).join("</b>, <b>")}</b>`
    : "whoever administers this installation"} — everybody who may maintain
    <b>${esc(textOf(cat.texts, cat.languages, cat.id))}</b>. A product has no deputy of
    its own: it is referenced by several catalogues and maintained through its home one,
    so a stand-in is an editor of the catalogue, added under <i>Who maintains this
    catalogue</i> below.</p>`;
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

  // Which approver control is in play follows from the kind, and the form is
  // re-rendered from scratch each time a product is opened — so the listener sits on
  // the view rather than on the select, and survives every re-render.
  view.addEventListener("change", (e) => {
    const sel = e.target.closest('select[name="akind"]');
    if (!sel) return;
    const form = sel.closest("form");
    if (!form) return;
    for (const box of form.querySelectorAll(".aref-for")) {
      box.hidden = box.dataset.kind !== sel.value;
    }
  });

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

function wire({ api, toast, view }, cat, items, byID, langs, procIDs, formList, canShare, canTheme, dir, people) {
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
  const assembler = view.querySelector(".assemble-editor");

  // ---- Where the editor panel sits ----
  //
  // The panel is a column beside the list (app.css), and it opens level with the row
  // it was opened from: a product edited from row thirty would otherwise put its form
  // thirty rows further down the page, which is the scroll this layout exists to
  // remove. CSS cannot know where a row ended up — the shared table enhancer sorts
  // and filters the tbody underneath it — so the offset is measured here and handed
  // over as --editor-top. app.css reads it only where the two columns exist at all;
  // on a narrow viewport the property is ignored and the panel is stacked under the
  // list, exactly as it used to be.
  const cols = view.querySelector(".product-cols");
  const list = view.querySelector(".product-list");
  // The column itself, which is what carries the offset: the two panels inside it are
  // containers and only one of them holds anything at a time.
  const side = view.querySelector(".product-side");
  // The row the open panel belongs to, kept so the alignment survives what the list
  // does afterwards.
  let anchor = null;

  const open = () => !!(editor.firstChild || assembler.firstChild);

  const align = () => {
    if (!anchor || !cols || !side || !open()) return;
    // offsetParent is null for a row a filter has hidden. Measuring against a hidden
    // row would snap the panel to the top of the list while its product is still
    // open in it, so the last good offset stands until the row is on screen again.
    if (anchor.offsetParent === null) return;
    const top = anchor.getBoundingClientRect().top - cols.getBoundingClientRect().top;
    side.style.setProperty("--editor-top", `${Math.max(0, Math.round(top))}px`);
  };
  // Sorting a column reorders the rows and a filter hides some: either moves the row
  // the panel is aligned to, and both arrive as ordinary events on the list. One
  // frame later the table has been rebuilt, so this re-measures rather than predicts.
  const realign = () => requestAnimationFrame(align);
  if (list) {
    list.addEventListener("click", realign);
    list.addEventListener("input", realign);
  }
  // A viewport change moves the row with no event on the list at all, and a narrow
  // one takes the second column away entirely. Observed rather than bound to
  // window.resize so it ends with the view: the element goes when the page is
  // re-rendered and the observer goes with it, where a window listener would outlive
  // both and go on measuring nodes nobody can see.
  if (cols && typeof ResizeObserver === "function") new ResizeObserver(realign).observe(cols);

  // markEditing keeps the highlight on exactly one row: the panel says which product
  // it is editing, and a second highlight would make that a guess.
  const markEditing = (row) => {
    for (const tr of view.querySelectorAll(".product-list tr.editing")) tr.classList.remove("editing");
    if (row) row.classList.add("editing");
  };

  // keepInPlace holds the row still while the layout changes under it.
  //
  // Opening the panel takes a column off the list, so every cell that was on one line
  // and is now on two makes the rows above the reader taller — and a row at the
  // bottom of a long list is then pushed a screenful down by text they are not even
  // looking at. The panel is level with its row either way (align() measures after
  // the reflow), but the *page* has moved, which reads as the list jumping away from
  // the click. Measured before and after, the difference is exactly how far the row
  // travelled, and scrolling by it puts it back under the cursor. app.css turns the
  // browser's own scroll anchoring off here so this is the only correction applied
  // and the two cannot fight over the same pixels.
  const keepInPlace = (row, wasAt) => {
    if (!row || wasAt === null || row.offsetParent === null) return;
    const moved = row.getBoundingClientRect().top - wasAt;
    if (Math.abs(moved) > 1) window.scrollBy(0, moved);
  };

  // stacked says the column is not there: below the layout's breakpoint the panel
  // renders under the list, where nothing is level with anything and the reader has to
  // be taken to it. Read off the layout rather than from a media query repeated here,
  // because the breakpoint is app.css's and a second copy of it drifts.
  const stacked = () => !cols || getComputedStyle(cols).display !== "flex";

  // openPanel puts one panel in the column, empties the other, marks the row the two
  // belong to and aligns them. One function for the product's form and for the kit,
  // because they are one act and one place: a row has one answer open at a time, and
  // a panel carrying the previous product's highlight — or the previous product's
  // offset — is worse than no highlight at all.
  const openPanel = (into, html, row) => {
    const wasAt = row && row.offsetParent !== null ? row.getBoundingClientRect().top : null;
    editor.innerHTML = "";
    assembler.innerHTML = "";
    into.innerHTML = html;
    markEditing(row && row.tagName === "TR" ? row : null);
    anchor = row || null;
    align();
    keepInPlace(row, wasAt);
    // Stacked, the panel is below the list and can be a screen away; beside it, it is
    // already level with the row that was clicked and scrolling would undo that.
    if (stacked()) into.scrollIntoView({ block: "nearest" });
  };
  const openEditor = (html, row) => { openPanel(editor, html, row); wireProductForm(); };
  const openAssembler = (html, row) => openPanel(assembler, html, row);

  const closePanel = () => {
    // Closing gives the width back and reflows the list the same way, so the row is
    // held still on the way out too.
    const row = anchor;
    const wasAt = row && row.offsetParent !== null ? row.getBoundingClientRect().top : null;
    editor.innerHTML = "";
    assembler.innerHTML = "";
    side.style.removeProperty("--editor-top");
    markEditing(null);
    anchor = null;
    keepInPlace(row, wasAt);
  };

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
        groups: audienceFrom(f),
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
      // A new product has no row yet, so the panel opens level with the button that
      // asked for it — which is where the reader is looking.
      openEditor(productForm(null, cat, langs, procIDs, formList, items, dir, people),
        b.closest(".row"));
      return;
    }
    if (act === "edit") {
      openEditor(productForm(byID[b.dataset.id], cat, langs, procIDs, formList, items, dir, people),
        b.closest("tr"));
      return;
    }
    if (act === "cancel-product") { closePanel(); return; }

    if (act === "assemble") {
      openAssembler(assembleKit(
        b.dataset.id, cat.items || [], byID, langs, cat.edges || []), b.closest("tr"));
      return;
    }
    if (act === "assemble-cancel") { closePanel(); return; }

    if (act === "assemble-save") {
      const form = assembler.querySelector(".assemble");
      if (!form) return;
      const pid = form.dataset.product;
      const f = new FormData(form);
      // Everything this product is made of, as the form now says it. A choice of
      // "none" is the absence of an edge and not an edge of its own, so it simply
      // does not appear.
      const chosen = [];
      for (const [field, value] of f.entries()) {
        if (!field.startsWith("part-")) continue;
        if (!STRUCTURE_IDS.includes(String(value))) continue;
        chosen.push({ from: pid, to: field.slice("part-".length), kind: String(value) });
      }
      // Everything else stays exactly as it was: another whole's arrangement, and
      // every edge of a kind the kit does not own — precedence and incompatibility,
      // this product's own included. The kit was asked one question and may only
      // answer that one; a save that replaced the edge list wholesale would delete
      // what this screen never showed. The filter is structural rather than a list
      // of kinds to spare, so a kind added later is kept without being remembered.
      const kept = (cat.edges || []).filter((e) =>
        !(e.from === pid && STRUCTURE_IDS.includes(e.kind)));
      const edges = [...kept, ...chosen];

      // Refuse a loop here, naming the product that closes it. The rule is the
      // publish rule and the reason to apply it now is the reader: at this moment
      // they know which choice they just made, and at publish they have a list of
      // problems about a catalogue they have since edited.
      const looped = chosen.find((e) => contains(kept, e.to, pid));
      if (looped) {
        const nameOf = (i) => textOf((byID[i] || {}).texts, langs, i);
        toast(`${nameOf(looped.to)} already contains ${nameOf(pid)}, so it cannot also be ` +
          `part of it. Nothing was saved — a catalogue with a loop cannot be published.`, "err");
        return;
      }
      try { await patchList({ edges }); toast("Saved"); reload(); }
      catch (err) { patchFailed(err); }
      return;
    }

    if (act === "add-existing") {
      // A dialog with a real list, because the prompt this replaces was not one:
      // it printed the products as lines of text and asked for an id back, so
      // nothing in it could be clicked, a typo was answered with "No product with
      // that id", and a browser truncates a prompt body past a handful of lines —
      // which cuts off the newest products, the ones somebody is most likely to be
      // looking for. The same failure the application picker had, and the same fix.
      const free = items.filter((it) => !(cat.items || []).includes(it.id))
        .map((f) => ({ value: f.id, label: `${textOf(f.texts, langs, f.id)} — ${f.id}` }))
        .sort((a, b) => a.label.localeCompare(b.label));
      // The button is drawn from this same list, so an empty one means the page is
      // stale rather than that there is nothing to offer.
      if (!free.length) { toast("Every product is already offered here"); return; }
      const picked = await openPickModal({
        title: "Offer an existing product",
        label: "Product",
        options: free,
        hint: "Offering it here does not copy it: the product stays edited through its home catalogue.",
        okLabel: "Offer",
      });
      if (!picked) return;
      try { await patchList({ items: [...(cat.items || []), picked.option.value] }); reload(); }
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

    if (act === "unexclude") {
      const [a, b2] = b.dataset.pair.split("|");
      // Both directions, because the row is one fact: taking away the one that was
      // drawn would leave the mirror, and the row would come straight back with
      // nothing to say why.
      const edges = (cat.edges || []).filter((x) => !(x.kind === "excludes"
        && ((x.from === a && x.to === b2) || (x.from === b2 && x.to === a))));
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
      // One control or two, depending on whether the directory could be read.
      const who = f.get("who");
      const cut = who === null ? -1 : String(who).indexOf("|");
      const refType = who === null ? String(f.get("type") || "user") : String(who).slice(0, cut);
      const refID = who === null
        ? String(f.get("id") || "").trim()
        : String(who).slice(cut + 1);
      if (!refID) { toast("Somebody has to be chosen", "err"); return; }
      const members = [...(cat.members || []),
        { ref: { type: refType, id: refID }, role: f.get("role") }];
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
      const have = cat.edges || [];
      // An incompatibility is symmetric, so the mirror of one already recorded is
      // the same fact said backwards rather than a second one. Added anyway it
      // would draw one row, be removed as a pair, and leave whoever authored it
      // wondering where the other went.
      const already = have.some((e) => e.kind === kind
        && ((e.from === from && e.to === to)
          || (kind === "excludes" && e.from === to && e.to === from)));
      if (already) { toast("That is already recorded", "err"); return; }
      const edges = [...have, { from, to, kind }];
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

      const body = productBody(f, {
        productID: pid, homeCatalog: id, langs, stored: byID[pid] || {},
      });
      try {
        await api("POST", "/api/v1/catalog-products", body);
        // The picture is its own request, because it is bytes and the product is a
        // record. It follows the save rather than preceding it, so a product that
        // was refused never acquires a picture — and a picture that fails to upload
        // is reported on its own, against a product that is already stored.
        await savePicture({ api, apiBytes, toast }, pid, f);
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
