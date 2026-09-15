// The self-service portal (ADR-0312).
//
// A module, so it can reuse theme.js's palette derivation rather than repeat it.
// That reuse is the point: --accent-ink decides whether a button's label is
// readable on whatever colour somebody picked, and a second implementation of it
// is a second place for that to be wrong.
//
// One page with two halves: the catalogue somebody is the audience for, and the
// orders they placed. It reads five endpoints and holds no state the server does
// not already have, which is why it can be reloaded at any point without losing
// anything.
//
// Language follows the browser (ADR-0313),
// on the condition that record names: every string below exists in every locale
// the page offers, held by TestPortalCatalogueIsComplete. A missing key would
// otherwise reach a customer, who cannot act on a review signal.

import { applyAccent } from './theme.js';

const STRINGS = {
  de: {
    'portal.title': 'Leistungsportal',
    'portal.catalog': 'Katalog',
    'portal.orders': 'Meine Bestellungen',
    'portal.none': 'Ihnen ist kein Katalog zugeordnet.',
    'portal.none.hint': 'Wenden Sie sich an die Stelle, die Ihren Zugang eingerichtet hat.',
    'portal.empty': 'Dieser Katalog enthält zurzeit nichts Bestellbares.',
    'portal.includes': 'Enthalten',
    'portal.options': 'Zusätzlich wählbar',
    'portal.order': 'Bestellen',
    'portal.ordering': 'Wird bestellt …',
    'portal.noOrders': 'Sie haben noch nichts bestellt.',
    'portal.placed': 'Bestellt am',
    'portal.approval': 'Genehmigung nötig',
    'portal.back': 'Zurück zu Atlas',
    'portal.held': 'Haben Sie bereits',
    'portal.held.since': 'seit',
    'portal.blockedBy': 'Wartet auf',
    'portal.reason': 'Begründung',
    'portal.retry': 'Erneut versuchen',
    'portal.failed': 'Das hat nicht geklappt.',
    'portal.cancel': 'Stornieren',
    'portal.cancelling': 'Wird storniert …',
    'order.cancelled': 'Storniert',
    'status.cancelled': 'Storniert',
    'portal.return': 'Zurückgeben',
    'portal.returning': 'Wird zurückgegeben …',
    'status.returning': 'Wird zurückgegeben',
    'status.returned': 'Zurückgegeben',
    'portal.return.sure': 'Diese Leistung wirklich zurückgeben? Der Zugang wird entzogen.',
    'status.returnFailed': 'Rücknahme gescheitert',
    'status.pending': 'Wartet',
    'status.running': 'Läuft',
    'status.done': 'Erledigt',
    'status.skipped': 'Bereits vorhanden',
    'status.failed': 'Störung',
    'status.rejected': 'Abgelehnt',
    'status.abandoned': 'Aufgegeben',
    'status.blocked': 'Blockiert',
    'order.running': 'In Arbeit',
    'order.completed': 'Abgeschlossen',
    'order.partial': 'Teilweise erfüllt',
    'order.unfulfilled': 'Nicht erfüllt',
    'nav.catalog': 'Katalog durchsuchen',
    'nav.orders': 'Meine Aufträge',
    'nav.services': 'Meine Leistungen',
    'nav.help': 'Hilfe',
    'col.category': 'Kategorie',
    'col.bundle': 'Bundle',
    'col.offering': 'Marktleistung',
    'col.service': 'Service',
    'act.back': '< zurück',
    'act.discard': 'Auftrag löschen',
    'act.toBasket': 'In den Warenkorb >',
    'act.place': 'bestellen >',
    'act.cancelLines': 'Leistung(en) kündigen',
    'act.changeLine': 'Leistung mutieren',
    'basket.title': 'Warenkorb',
    'basket.empty': 'Der Warenkorb ist leer.',
    'basket.count': 'Im Warenkorb',
    'for.order': 'Bestellen für:',
    'for.approve': 'Genehmigen für:',
    'for.search': 'Person suchen',
    'for.self': 'mich selbst',
    'tbl.company': 'Unternehmen',
    'tbl.person': 'Person',
    'tbl.placed': 'bestellt',
    'tbl.order': 'Auftrag',
    'tbl.status': 'Status',
    'tbl.searchCompany': 'Unternehmen suchen',
    'tbl.searchPerson': 'Person suchen',
    'tbl.searchDate': 'Datum',
    'tbl.searchOrder': 'Auftrag suchen',
    'tbl.searchStatus': 'Status suchen',
    'tbl.noMatch': 'Kein Auftrag entspricht der Suche.',
    'note.noCategories': 'Atlas kennt heute keine Kategorie über dem Bundle. Diese Spalte zeigt deshalb den Katalog selbst; sie wird zur Kategorie, sobald der Katalog eine führt.',
    'note.noCompany': 'Die Spalte Unternehmen bleibt leer: ein Auftrag trägt heute keine Organisation. Er nennt nur, wer bestellt und wer empfängt.',
    'note.included': 'Fest enthalten — nicht abwählbar.',
    'info.title': 'Angaben zum Service',
    'info.id': 'Kennung',
    'info.approval': 'Genehmigung',
    'info.none': 'keine',
    'info.repeatable': 'Mehrfach beziehbar',
    'info.yes': 'ja',
    'info.no': 'nein',
    'services.none': 'Sie beziehen zurzeit keine Leistungen.',
    'fav.mark': 'Als Favorit merken',
    'fav.clear': 'Favorit entfernen',
    'fav.only': 'Nur Favoriten',
    'fav.none': 'Sie haben nichts als Favorit gemerkt.',
    'fav.unresolved': 'Favoriten, die dieser Katalog nicht führt',
    'fav.full': 'Mehr Favoriten als ein Konto führen darf. Entfernen Sie einen, bevor Sie einen weiteren merken.',
  },
  en: {
    'portal.title': 'Service portal',
    'portal.catalog': 'Catalogue',
    'portal.orders': 'My orders',
    'portal.none': 'No catalogue is assigned to you.',
    'portal.none.hint': 'Ask whoever set up your access.',
    'portal.empty': 'This catalogue currently offers nothing.',
    'portal.includes': 'Included',
    'portal.options': 'Also available',
    'portal.order': 'Order',
    'portal.ordering': 'Ordering …',
    'portal.noOrders': 'You have not ordered anything yet.',
    'portal.placed': 'Ordered on',
    'portal.approval': 'Needs approval',
    'portal.back': 'Back to Atlas',
    'portal.held': 'You already have this',
    'portal.held.since': 'since',
    'portal.blockedBy': 'Waiting for',
    'portal.reason': 'Reason',
    'portal.retry': 'Try again',
    'portal.failed': 'That did not work.',
    'portal.cancel': 'Cancel',
    'portal.cancelling': 'Cancelling …',
    'order.cancelled': 'Cancelled',
    'status.cancelled': 'Cancelled',
    'portal.return': 'Return',
    'portal.returning': 'Returning …',
    'status.returning': 'Being returned',
    'status.returned': 'Returned',
    'portal.return.sure': 'Really give this back? The access will be revoked.',
    'status.returnFailed': 'Return failed',
    'status.pending': 'Waiting',
    'status.running': 'In progress',
    'status.done': 'Done',
    'status.skipped': 'Already held',
    'status.failed': 'Failed',
    'status.rejected': 'Refused',
    'status.abandoned': 'Given up on',
    'status.blocked': 'Blocked',
    'order.running': 'In progress',
    'order.completed': 'Completed',
    'order.partial': 'Partly fulfilled',
    'order.unfulfilled': 'Not fulfilled',
    'nav.catalog': 'Browse catalogue',
    'nav.orders': 'My orders',
    'nav.services': 'My services',
    'nav.help': 'Help',
    'col.category': 'Category',
    'col.bundle': 'Bundle',
    'col.offering': 'Offering',
    'col.service': 'Service',
    'act.back': '< back',
    'act.discard': 'Discard order',
    'act.toBasket': 'Add to basket >',
    'act.place': 'order >',
    'act.cancelLines': 'Cancel service(s)',
    'act.changeLine': 'Change service',
    'basket.title': 'Basket',
    'basket.empty': 'The basket is empty.',
    'basket.count': 'In the basket',
    'for.order': 'Order for:',
    'for.approve': 'Approve for:',
    'for.search': 'Find a person',
    'for.self': 'myself',
    'tbl.company': 'Organisation',
    'tbl.person': 'Person',
    'tbl.placed': 'ordered',
    'tbl.order': 'Order',
    'tbl.status': 'Status',
    'tbl.searchCompany': 'Search organisation',
    'tbl.searchPerson': 'Search person',
    'tbl.searchDate': 'Date',
    'tbl.searchOrder': 'Search order',
    'tbl.searchStatus': 'Search status',
    'tbl.noMatch': 'No order matches the search.',
    'note.noCategories': 'Atlas has no category level above the bundle today. This column therefore shows the catalogue itself; it becomes the category as soon as a catalogue carries one.',
    'note.noCompany': 'The organisation column stays empty: an order carries no organisation today. It names only who ordered and who receives.',
    'note.included': 'Always included — cannot be deselected.',
    'info.title': 'About this service',
    'info.id': 'Identifier',
    'info.approval': 'Approval',
    'info.none': 'none',
    'info.repeatable': 'May be held more than once',
    'info.yes': 'yes',
    'info.no': 'no',
    'services.none': 'You currently hold no services.',
    'fav.mark': 'Mark as favourite',
    'fav.clear': 'Remove favourite',
    'fav.only': 'Favourites only',
    'fav.none': 'You have marked nothing as a favourite.',
    'fav.unresolved': 'Favourites this catalogue does not carry',
    'fav.full': 'That is more favourites than one account may keep. Remove one before marking another.',
  },
};

// The locale, from the browser and narrowed to what the page offers.
//
// The record puts a signed-in visitor's choice on their account rather than in
// this browser, because they arrive from a phone and a desktop. That endpoint
// does not exist yet, so the choice is remembered here in the meantime — which
// is the one place this page knowingly falls short of its own record.
function pickLocale() {
  const url = new URLSearchParams(location.search).get('lang');
  const stored = (() => { try { return localStorage.getItem('portal.lang'); } catch { return null; } })();
  for (const want of [url, stored, ...(navigator.languages || [navigator.language || ''])]) {
    if (!want) continue;
    const base = String(want).toLowerCase().split('-')[0];
    if (STRINGS[base]) return base;
  }
  return 'de';
}

let locale = pickLocale();

// t renders a key. A key with no string shows as itself — in this page that can
// only happen if the completeness test was removed, and looking broken in review
// is better than guessing at a language nobody chose.
function t(key) {
  return (STRINGS[locale] && STRINGS[locale][key]) || key;
}

function setLocale(next) {
  locale = next;
  try { localStorage.setItem('portal.lang', next); } catch { /* private window */ }
  render();
}

// textOf reads a catalogue item's name in the current locale, falling back to
// whatever the catalogue has. A product is named by its catalogue, not by this
// page, so there is no key to look up and no way to be complete about it.
function textOf(texts, fallback) {
  if (!texts) return fallback;
  return texts[locale] || texts.de || texts.en || Object.values(texts)[0] || fallback;
}

// The typeface stacks the server ships, mirrored here because the page paints
// with them. The server refuses a name that is not one of these, so the two
// cannot drift into a catalogue naming a face the page has no stack for.
const TYPEFACES = {
  system: 'system-ui, -apple-system, "Segoe UI", Roboto, sans-serif',
  humanist: '"Segoe UI", Candara, Optima, "Trebuchet MS", sans-serif',
  serif: 'Georgia, Cambria, "Times New Roman", serif',
  mono: 'ui-monospace, "SF Mono", "Cascadia Mono", Menlo, monospace',
};

// applyTheme paints the catalogue's brand, deriving every shade from the one
// colour that is stored.
//
// The derivation lives in theme.js and is reused rather than repeated: eleven
// copies of it is eleven places for one to be wrong, and the one that matters is
// --accent-ink, which decides whether a button's label is readable on whatever
// colour somebody picked (ADR-0263).
//
// The sign-in screen keeps the operator's brand, because a catalogue is resolved
// from who you are and nobody is signed in yet. So a first-time visitor sees the
// operator's face until their catalogue answers; a returning one is painted from
// the cache before the first frame, which is what the cache is for.
function applyTheme(catalog) {
  const theme = (catalog && catalog.theme) || {};
  const root = document.documentElement;

  // applyAccent with nothing clears the overrides, which falls back to the
  // instance brand this page already declared — the right answer for a catalogue
  // that has no face of its own.
  applyAccent(theme.accent || '');
  if (theme.typeface && TYPEFACES[theme.typeface]) {
    root.style.setProperty('--font-sans', TYPEFACES[theme.typeface]);
  }
  try {
    // Remember which catalogue this was painted for. Without the id the cache
    // would repaint a returning visitor in somebody else's brand after their
    // assignment changed — worse than the flash it exists to prevent.
    localStorage.setItem('portal.theme', JSON.stringify({
      id: catalog && catalog.id, accent: theme.accent, typeface: theme.typeface,
    }));
  } catch { /* private window */ }
}

// paintFromCache runs before anything is fetched, so a returning visitor sees
// their own brand from the first frame. The server always wins: applyTheme
// overwrites this once the catalogue answers, and an unreachable server leaves
// the cached paint intact — the discipline ADR-0113 established.
function paintFromCache() {
  try {
    const cached = JSON.parse(localStorage.getItem('portal.theme') || 'null');
    if (cached) applyTheme({ id: cached.id, theme: cached });
  } catch { /* nothing cached, or no storage */ }
}

const state = {
  catalog: null,
  release: null,
  orders: [],
  // held is what the inventory says this person already has, as itemId -> since.
  // It is read from the inventory and not derived from the orders on this page:
  // the order that granted a right is deleted by retention long before the right
  // ends, and a catalogue that marked from orders would stop marking on the
  // ninetieth day (ADR-0312).
  held: new Map(),
  chosen: new Set(),
  busy: false,
  error: '',

  // --- What the mockups add: a shell with three destinations, a cascade that
  // remembers where it is, and a basket that survives moving between products.

  // view is which of the three the nav is on.
  view: 'catalog',
  // bundle and offering are where the cascade stands. A column shows nothing
  // until the one to its left is chosen, which is what makes it a cascade rather
  // than four lists — and it is *selection* rather than filtering, because a
  // decomposition read at four levels is read one branch at a time.
  bundle: '',
  offering: '',
  // basket is every item id chosen so far, across products. It is the whole
  // reason this is a two-step order now: the previous page ordered the moment a
  // card's button was pressed, so two bundles were two orders, two approvals and
  // two provisioning runs for one decision somebody made once.
  basket: new Set(),
  // inBasket is whether the basket screen is showing instead of the cascade. Not
  // a fourth nav entry: the mockups make it the next step of the same screen,
  // reached and left by the action row.
  inBasket: false,
  // forWhom is the recipient an order is placed for, empty for oneself. A
  // principal id or username — the server resolves it, and this page deliberately
  // does not try to, because it has no directory of its own to search.
  forWhom: '',
  // filters is the orders table's per-column search, keyed by column.
  filters: { company: '', person: '', date: '', order: '', status: '' },
  // info is the service whose details are open, empty for none.
  info: '',
  // favourites is what this person marked, as ids — a bookmark and never an
  // entitlement (ADR-0348). Held as a Set because every row asks
  // "is this one of mine" while the cascade renders.
  favourites: new Set(),
  // favouritesOnly narrows the cascade to marked products, which is what a
  // shortcut list is for. It is a filter over the columns rather than a fourth
  // destination in the nav: a favourite is still a product in the catalogue, and
  // pulling it onto its own screen would hide what it is part of.
  favouritesOnly: false,
};

// --- The four levels the mockups draw ---------------------------------------
//
// Atlas has no "Kategorie / Bundle / Marktleistung / Service" typing: an item is
// an item, and the hierarchy is the containment graph, of any depth. So the
// columns are derived from *position in that graph* rather than read from a
// field that does not exist:
//
//   Bundle        — an item nothing else contains
//   Marktleistung — what a bundle directly contains
//   Service       — what a Marktleistung directly contains
//
// Kategorie has no source at all. It is rendered as the catalogue itself and
// says so, rather than inventing a grouping: a column filled with a guess is
// worse than one that explains what it is waiting for.

// partsOf returns what an item directly carries, integral parts first.
//
// The two kinds stay apart, because they mean opposite things to a basket: an
// inclusion is a consequence of ordering the whole and never deselectable, an
// option is an offer.
function partsOf(release, id) {
  const inc = ((release.includes || {})[id] || []).map((x) => ({ id: x, integral: true }));
  const opt = ((release.options || {})[id] || []).map((x) => ({ id: x, integral: false }));
  return [...inc, ...opt];
}

// levelsOf returns the four columns for where the cascade currently stands.
function levelsOf(release) {
  const bundles = products(release).map((i) => ({ id: i.id, integral: false }));
  const offerings = state.bundle ? partsOf(release, state.bundle) : [];
  const services = state.offering ? partsOf(release, state.offering) : [];
  return { bundles, offerings, services };
}

// carriedBy reports whether choosing this whole already brings the part, at any
// depth. An integral part is not a separate choice, and offering to add one that
// is already coming would put the same thing in a basket twice.
function carriedBy(release, whole, part) {
  const seen = new Set();
  const walk = (id) => {
    if (seen.has(id)) return false;
    seen.add(id);
    for (const p of (release.includes || {})[id] || []) {
      if (p === part || walk(p)) return true;
    }
    return false;
  };
  return walk(whole);
}

// inBasketNow reports whether an item is chosen, directly or because something
// chosen always carries it.
function inBasketNow(release, id) {
  if (state.basket.has(id)) return true;
  for (const chosen of state.basket) {
    if (carriedBy(release, chosen, id)) return true;
  }
  return false;
}

async function api(path, options) {
  const res = await fetch(path, { credentials: 'same-origin', ...options });
  if (!res.ok) throw new Error(`${res.status} ${await res.text()}`);
  return res.status === 204 ? null : res.json();
}

async function load() {
  state.error = '';
  try {
    state.catalog = await api('/api/v1/portal/catalog');
  } catch {
    // 404 here is the ordinary "you are the audience for nothing" answer, not a
    // failure: the page says so rather than showing an error.
    state.catalog = null;
  }
  applyTheme(state.catalog);
  if (state.catalog) {
    const releases = await api(`/api/v1/catalogs/${state.catalog.id}/releases`);
    state.release = releases && releases.length ? releases[0] : null;
  }
  state.orders = await api('/api/v1/orders');
  const inv = await api('/api/v1/inventory');
  state.held = new Map(((inv && inv.items) || []).map((i) => [i.itemId, i.since]));
  const favs = await api('/api/v1/portal/favourites');
  state.favourites = new Set((favs && favs.itemIds) || []);
  render();
}

// products returns what a person picks from: the items nothing else includes.
// A part is shown under the whole it belongs to rather than beside it, or the
// catalogue would read as a list of components.
function products(release) {
  const included = new Set();
  for (const parts of Object.values(release.includes || {})) parts.forEach((p) => included.add(p));
  for (const parts of Object.values(release.options || {})) parts.forEach((p) => included.add(p));
  return (release.items || []).filter((i) => !included.has(i.id));
}

function itemsById(release) {
  const by = {};
  for (const i of release.items || []) by[i.id] = i;
  return by;
}

// order places one order for everything in the basket.
//
// One order and not one per product, which is the whole reason the basket
// exists: the previous page ordered the moment a card's button was pressed, so
// two bundles were two orders, two approvals and two provisioning runs for one
// decision somebody made once.
//
// recipient travels only when somebody was named. The server decides whether the
// caller may order for them and whether that person is eligible for what is in
// the basket — this page does not pre-judge either, because it would have to
// guess at rules the release carries.
async function order() {
  if (!state.basket.size) return;
  state.busy = true;
  state.error = '';
  render();
  try {
    await api('/api/v1/orders', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({
        releaseId: state.release.id,
        items: [...state.basket],
        ...(state.forWhom.trim() ? { recipient: state.forWhom.trim() } : {}),
      }),
    });
    state.basket.clear();
    state.chosen.clear();
    state.inBasket = false;
    state.view = 'orders';
    await load();
  } catch (e) {
    state.error = `${t('portal.failed')} ${e.message}`;
  } finally {
    state.busy = false;
    render();
  }
}

function el(tag, attrs, ...children) {
  const node = document.createElement(tag);
  for (const [k, v] of Object.entries(attrs || {})) {
    // A nullish or false value means "do not set this attribute". setAttribute has
    // no falsy handling of its own, so `disabled: busy ? 'disabled' : null` would
    // render disabled="null" — which a browser reads as disabled, permanently.
    // This page spreads a conditional object instead and so never hit it; the
    // guard is here so the next conditional attribute written the obvious way
    // works.
    if (v == null || v === false) continue;
    if (k === 'class') node.className = v;
    else if (k.startsWith('on')) node.addEventListener(k.slice(2), v);
    else node.setAttribute(k, v);
  }
  for (const c of children.flat()) {
    if (c == null || c === false) continue;
    node.append(c.nodeType ? c : document.createTextNode(String(c)));
  }
  return node;
}

// heldPill marks what the person already has, with the day it started.
//
// It marks and does not disable. A card orders a product *and* whatever options
// are ticked under it, so a bundle somebody already holds may still have an
// option they do not — and a button greyed out for the whole card would make that
// option unreachable. The server does the deciding: an item already held and not
// repeatable is ordered as a skipped line, which says "you asked, you had it"
// rather than silently dropping the request.
//
// An item the catalogue marks repeatable gets no pill even when it is held:
// holding a second licence is the ordinary case there, and a mark that means
// nothing trains people to ignore the mark.
function heldPill(item) {
  if (!item || item.multipleAllowed) return null;
  const since = state.held.get(item.id);
  if (since == null) return null;
  return el('p', { class: 'pill ok' },
    `${t('portal.held')} — ${t('portal.held.since')} `
    + new Date(since / 1e6).toLocaleDateString(locale));
}

// --- The cascade -------------------------------------------------------------

// cell renders one row of a column: an optional toggle, the label, and whatever
// sits at the right edge.
function cell(opts) {
  const o = opts || {};
  return el('div', { class: 'cell' },
    o.lead || null,
    o.onOpen
      ? el('button', {
        class: o.open ? 'label on' : 'label',
        onclick: o.onOpen,
      }, o.text)
      : el('span', { class: 'label' }, o.text),
    o.trail || null);
}

// toggle renders the mockups' square −/+ control.
//
// Integral parts get a disabled "−": they are in, and they cannot be taken out.
// Showing them greyed rather than hiding the control keeps the column readable
// as a decomposition — the point of the screen — while saying they are not a
// choice.
function toggle(release, id, integral) {
  const inIt = inBasketNow(release, id);
  if (integral) {
    return el('button', {
      class: 'sq', disabled: 'disabled', title: t('note.included'),
      'aria-label': t('note.included'),
    }, '\u2212');
  }
  return el('button', {
    class: 'sq',
    'aria-pressed': inIt ? 'true' : 'false',
    onclick: () => {
      if (state.basket.has(id)) state.basket.delete(id); else state.basket.add(id);
      render();
    },
  }, inIt ? '\u2212' : '+');
}

// infoButton is the "i" the mockups put at the right edge of the service column.
function infoButton(id) {
  return el('button', {
    class: 'sq info',
    'aria-label': t('info.title'),
    onclick: () => { state.info = state.info === id ? '' : id; render(); },
  }, 'i');
}

// infoPanel is what the "i" opens: what the catalogue actually knows about a
// service. It says nothing the release does not carry — a panel that padded
// itself out with invented detail would be worse than no panel.
function infoPanel(item) {
  const kind = item.approval && item.approval.kind && item.approval.kind !== 'none'
    ? item.approval.kind : t('info.none');
  return el('div', { class: 'card' },
    el('h3', {}, textOf(item.texts, item.id)),
    el('p', { class: 'muted' }, `${t('info.id')}: ${item.id}`),
    el('p', { class: 'muted' }, `${t('info.approval')}: ${kind}`),
    el('p', { class: 'muted' },
      `${t('info.repeatable')}: ${item.multipleAllowed ? t('info.yes') : t('info.no')}`),
    heldPill(item));
}

// star marks or unmarks one product.
//
// It writes through to the server and takes the answer as the new truth rather
// than toggling locally and hoping: a favourites list is the one thing on this
// page a second tab can be changing at the same time, and the route answers with
// the whole list precisely so this does not have to guess.
async function star(id) {
  const marked = state.favourites.has(id);
  // Painted before the request, so a star responds to the press. The answer
  // replaces it either way, so a failure corrects it rather than leaving a lie.
  if (marked) state.favourites.delete(id); else state.favourites.add(id);
  render();
  try {
    const out = await api(`/api/v1/portal/favourites/${encodeURIComponent(id)}`,
      { method: marked ? 'DELETE' : 'PUT' });
    state.favourites = new Set((out && out.itemIds) || []);
  } catch (e) {
    // Put it back and say what happened. A star that silently returned to where
    // it was is the kind of small wrongness somebody stops trusting the page over.
    if (marked) state.favourites.add(id); else state.favourites.delete(id);
    state.error = `${t('portal.failed')} ${e.message}`;
  }
  render();
}

// starButton is the affordance the row carries.
function starButton(id) {
  const on = state.favourites.has(id);
  return el('button', {
    class: 'sq',
    'aria-pressed': on ? 'true' : 'false',
    'aria-label': t(on ? 'fav.clear' : 'fav.mark'),
    title: t(on ? 'fav.clear' : 'fav.mark'),
    onclick: () => star(id),
  }, on ? '\u2605' : '\u2606');
}

// keepFavourites narrows a column when the favourites filter is on.
//
// A whole is kept when it is marked *or* when something under it is: hiding a
// bundle whose service somebody starred would hide the way to reach the star.
function keepFavourites(rel, entries) {
  if (!state.favouritesOnly) return entries;
  return entries.filter((e) => state.favourites.has(e.id)
    || [...state.favourites].some((f) => carriedBy(rel, e.id, f)));
}

function renderCatalogue() {
  if (!state.catalog) {
    return el('div', { class: 'empty' },
      el('p', {}, t('portal.none')),
      el('p', { class: 'muted' }, t('portal.none.hint')));
  }
  if (!state.release || !(state.release.items || []).length) {
    return el('div', { class: 'empty' }, el('p', {}, t('portal.empty')));
  }

  const rel = state.release;
  const by = itemsById(rel);
  const { bundles, offerings, services } = levelsOf(rel);
  const name = (id) => textOf((by[id] || {}).texts, id);

  // Kategorie. One row, the catalogue itself, and a note saying why — see the
  // comment on levelsOf: there is no category in the data, and filling the
  // column with a guess would be the one thing worse than leaving it honest.
  const category = el('div', { class: 'col' },
    el('div', { class: 'colhead' }, t('col.category')),
    cell({ text: textOf(state.catalog.texts, state.catalog.id), open: true }),
    el('p', { class: 'note' }, t('note.noCategories')));

  const bundleCol = el('div', { class: 'col' },
    el('div', { class: 'colhead' }, t('col.bundle')),
    keepFavourites(rel, bundles).map((b) => cell({
      text: name(b.id),
      open: state.bundle === b.id,
      onOpen: () => {
        state.bundle = state.bundle === b.id ? '' : b.id;
        state.offering = '';
        render();
      },
      trail: el('span', {}, starButton(b.id), ' ', toggle(rel, b.id, false)),
    })));

  const offeringCol = el('div', { class: 'col' },
    el('div', { class: 'colhead' }, t('col.offering')),
    keepFavourites(rel, offerings).map((o) => cell({
      text: name(o.id),
      open: state.offering === o.id,
      onOpen: () => {
        state.offering = state.offering === o.id ? '' : o.id;
        render();
      },
      trail: el('span', {}, starButton(o.id), ' ', toggle(rel, o.id, o.integral)),
    })));

  const serviceCol = el('div', { class: 'col' },
    el('div', { class: 'colhead' }, t('col.service')),
    keepFavourites(rel, services).map((sv) => cell({
      text: name(sv.id),
      lead: toggle(rel, sv.id, sv.integral),
      trail: el('span', {}, starButton(sv.id), ' ', infoButton(sv.id)),
    })));

  // Favourites this catalogue does not carry. Counted rather than hidden in
  // silence: a mark that stopped appearing with no word looks like the page lost
  // it, and the person cannot tell that from a catalogue that moved under them.
  const unresolved = [...state.favourites].filter((id) => !by[id]).length;

  return el('div', {},
    el('div', { class: 'favbar' },
      el('label', {},
        el('input', {
          type: 'checkbox', id: 'fav-only',
          ...(state.favouritesOnly ? { checked: 'checked' } : {}),
          onchange: (e) => { state.favouritesOnly = e.target.checked; render(); },
        }),
        ' ', t('fav.only')),
      state.favouritesOnly && !state.favourites.size
        ? el('span', { class: 'muted' }, t('fav.none')) : null,
      unresolved
        ? el('span', { class: 'muted' }, `${t('fav.unresolved')}: ${unresolved}`) : null),
    el('div', { class: 'cascade' }, category, bundleCol, offeringCol, serviceCol),
    state.info && by[state.info] ? el('div', { style: 'margin-top:16px' }, infoPanel(by[state.info])) : null);
}

// renderBasket is the second step of the same screen: what has been chosen,
// before anybody is asked to approve it.
function renderBasket() {
  if (!state.basket.size) {
    return el('div', { class: 'empty' }, el('p', {}, t('basket.empty')));
  }
  const rel = state.release || {};
  const by = itemsById(rel);
  // Every chosen item and everything each one always carries, so the basket
  // shows what will actually be provisioned rather than what was clicked.
  const shown = [];
  const seen = new Set();
  const add = (id, integral) => {
    if (seen.has(id)) return;
    seen.add(id);
    shown.push({ id, integral });
    for (const p of (rel.includes || {})[id] || []) add(p, true);
  };
  for (const id of state.basket) add(id, false);

  return el('div', { class: 'cascade' },
    el('div', { class: 'col' },
      el('div', { class: 'colhead' }, t('col.bundle')),
      shown.filter((x) => !x.integral).map((x) => cell({
        text: textOf((by[x.id] || {}).texts, x.id),
        trail: el('button', {
          class: 'sq',
          'aria-label': t('act.discard'),
          onclick: () => { state.basket.delete(x.id); render(); },
        }, 'X'),
      }))),
    el('div', { class: 'col' },
      el('div', { class: 'colhead' }, t('col.service')),
      shown.filter((x) => x.integral).map((x) => cell({
        text: textOf((by[x.id] || {}).texts, x.id),
        lead: el('button', { class: 'sq', disabled: 'disabled', title: t('note.included') }, '\u2212'),
        trail: infoButton(x.id),
      }))),
    el('div', { class: 'col' }), el('div', { class: 'col' }));
}

// deriveStatus mirrors the server's own rule rather than asking for it: an order
// carries its lines, and its standing is computed from them so the two cannot
// disagree. Doing it here keeps that property — a stored status could.
function deriveStatus(order) {
  const lines = order.lines || [];
  let provisioned = 0;
  let cancelled = 0;
  for (const l of lines) {
    const terminal = l.status === 'blocked'
      ? !!l.terminallyBlocked
      : ['done', 'skipped', 'rejected', 'abandoned', 'cancelled', 'returned'].includes(l.status);
    if (!terminal) return 'order.running';
    if (l.status === 'done' || l.status === 'skipped') provisioned++;
    if (l.status === 'cancelled') cancelled++;
  }
  if (!lines.length || provisioned === lines.length) return 'order.completed';
  // Withdrawn in full is its own answer. "Not fulfilled" is what an order says
  // when it tried and did not manage, and telling somebody that about their own
  // cancellation invites them to ask why it failed.
  if (cancelled === lines.length) return 'order.cancelled';
  return provisioned ? 'order.partial' : 'order.unfulfilled';
}

// cancellable mirrors the server's rule: what can still be taken back is what has
// not happened yet. Shown rather than hidden when nothing can be — an order that
// offers no way to withdraw it, with no word about why, is the case that produces
// the telephone call this whole thing exists to prevent.
function cancellable(order) {
  return (order.lines || []).some((l) => l.status === 'pending' || l.status === 'blocked');
}

// returnable mirrors the server's rule as far as the page can see it: a line is
// held, and nothing still held requires it. The second half is why this reads the
// order's own requires rather than only the line — giving back an account under a
// laptop that still uses it is the mistake the guard exists for, and offering the
// button would invite it before the server refused it.
function returnable(order, line) {
  if (line.status !== 'done' && line.status !== 'returnFailed') return false;
  // Held is wider than "done": a revocation only asked for has not happened, and
  // one that failed plainly has not. Either way the access is still there, and
  // offering to revoke what is underneath would invite the mistake the server
  // then refuses.
  const held = new Set((order.lines || [])
    .filter((l) => ['done', 'returning', 'returnFailed'].includes(l.status))
    .map((l) => l.itemId));
  const requires = order.requires || {};
  for (const [dependent, needs] of Object.entries(requires)) {
    if (held.has(dependent) && (needs || []).includes(line.itemId)) return false;
  }
  return true;
}

// giveBack starts a line's deprovisioning, then reloads. It asks first: revoking
// an access somebody has been using is the one thing on this page with a
// consequence outside Atlas, and a mis-click deletes an account.
async function giveBack(order, line) {
  if (state.busy) return;
  if (!window.confirm(t('portal.return.sure'))) return;
  state.busy = true;
  state.error = '';
  render();
  try {
    await api(`/api/v1/orders/${encodeURIComponent(order.id)}/lines/${encodeURIComponent(line.itemId)}/return`,
      { method: 'POST' });
    state.busy = false;
    await load();
  } catch (e) {
    state.busy = false;
    state.error = `${t('portal.failed')} ${e.message}`;
    render();
  }
}

// cancel withdraws an order, then reloads: what the server did to each line is
// what the page then shows, rather than what the page assumed it would do.
async function cancel(order) {
  if (state.busy) return;
  state.busy = true;
  state.error = '';
  render();
  try {
    await api(`/api/v1/orders/${encodeURIComponent(order.id)}/cancel`, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({}),
    });
    state.busy = false;
    await load();
  } catch (e) {
    state.busy = false;
    state.error = `${t('portal.failed')} ${e.message}`;
    render();
  }
}

// --- The orders table --------------------------------------------------------
//
// The mockups put a search field directly above the column it searches, which is
// the arrangement that needs no legend: what a field filters is the thing it is
// sitting on.

// matchesFilters reports whether one order survives the column searches. Case
// blind and substring, because somebody typing "gen" into a status field means
// "genehmigt" and should not have to know how it is spelled internally.
function matchesFilters(o) {
  const f = state.filters;
  const like = (hay, needle) => !needle
    || String(hay || '').toLowerCase().includes(needle.toLowerCase());
  const placed = new Date(o.createdAt / 1e6).toLocaleDateString(locale);
  return like(o.recipient, f.person)
    && like(placed, f.date)
    && like(o.id, f.order)
    && like(t(deriveStatus(o)), f.status)
    // The organisation column has no source; a filter over nothing must not
    // silently hide every row, so an empty field is the only one that matches.
    && (!f.company);
}

function filterCell(key, label) {
  return el('td', {},
    el('input', {
      type: 'search', value: state.filters[key], placeholder: label, 'aria-label': label,
      oninput: (e) => {
        state.filters[key] = e.target.value;
        // Re-rendering replaces the node, so the caret would jump to the end of a
        // field somebody is editing in the middle. Patch the rows and leave the
        // filter row alone.
        repaintOrderRows();
      },
    }));
}

let orderRowsNode = null;

function repaintOrderRows() {
  if (!orderRowsNode) return;
  orderRowsNode.replaceChildren(...orderRowBodies());
}

function orderRowBodies() {
  const rows = state.orders.filter(matchesFilters);
  if (!rows.length) {
    return [el('tr', {}, el('td', { colspan: '6', class: 'muted' }, t('tbl.noMatch')))];
  }
  return rows.map((o) => el('tr', {},
    // Organisation: rendered because the layout has the column, empty because an
    // order carries no organisation. The note under the table says so once,
    // rather than each row implying the data went missing.
    el('td', { class: 'muted' }, ''),
    el('td', {}, o.recipient || ''),
    el('td', {}, new Date(o.createdAt / 1e6).toLocaleDateString(locale)),
    el('td', {}, o.id),
    el('td', {},
      cancellable(o)
        ? el('button', {
          class: 'sq',
          'aria-label': t('portal.cancel'),
          title: t('portal.cancel'),
          disabled: state.busy,
          onclick: () => cancel(o),
        }, 'X')
        : null),
    el('td', {},
      t(deriveStatus(o)),
      el('ul', { class: 'lines' }, (o.lines || []).map((l) => el('li', {},
        el('span', { class: `dot ${l.status}` }),
        ' ', l.itemId, ' \u2014 ', t(`status.${l.status}`),
        l.blockedBy && l.blockedBy.length
          ? el('span', { class: 'muted' }, ` (${t('portal.blockedBy')}: ${l.blockedBy.join(', ')})`) : null,
        l.reason ? el('span', { class: 'muted' }, ` (${t('portal.reason')}: ${l.reason})`) : null,
        returnable(o, l)
          ? el('button', {
            class: 'linkish',
            disabled: state.busy,
            onclick: () => giveBack(o, l),
          }, state.busy ? t('portal.returning') : t('portal.return'))
          : null))))));
}

function renderOrders() {
  if (!state.orders.length) {
    return el('div', { class: 'empty' }, el('p', {}, t('portal.noOrders')));
  }
  orderRowsNode = el('tbody', {}, orderRowBodies());
  return el('div', {},
    el('div', { class: 'tablewrap' },
      el('table', { class: 'table' },
        el('thead', {},
          el('tr', { class: 'filters' },
            filterCell('company', t('tbl.searchCompany')),
            filterCell('person', t('tbl.searchPerson')),
            filterCell('date', t('tbl.searchDate')),
            filterCell('order', t('tbl.searchOrder')),
            el('td', {}),
            filterCell('status', t('tbl.searchStatus'))),
          el('tr', {},
            el('th', {}, t('tbl.company')),
            el('th', {}, t('tbl.person')),
            el('th', {}, t('tbl.placed')),
            el('th', {}, t('tbl.order')),
            el('th', {}, ''),
            el('th', {}, t('tbl.status')))),
        orderRowsNode)),
    el('p', { class: 'note' }, t('note.noCompany')));
}

// --- What somebody currently holds ------------------------------------------
//
// Read from the inventory, not assembled from the orders on this page: the order
// that granted a right is deleted by retention long before the right ends
// (ADR-0312), so a list built from orders would start losing services on the
// ninetieth day.
// depthOf is how far down the containment graph an item sits: 0 for something
// nothing contains, 1 for its parts, and so on. It is what puts a held service
// in the column it belongs to rather than all of them in one list.
//
// Shortest path wins, because a part can belong to more than one whole and the
// column it reads best in is the highest place it appears.
function depthOf(release, id) {
  const parentOf = new Map();
  for (const [whole, parts] of Object.entries(release.includes || {})) {
    for (const part of parts) if (!parentOf.has(part)) parentOf.set(part, whole);
  }
  for (const [whole, parts] of Object.entries(release.options || {})) {
    for (const part of parts) if (!parentOf.has(part)) parentOf.set(part, whole);
  }
  let d = 0;
  let at = id;
  const seen = new Set();
  while (parentOf.has(at) && !seen.has(at)) {
    seen.add(at);
    at = parentOf.get(at);
    d += 1;
    if (d > 8) break;
  }
  return d;
}

function renderServices() {
  const rel = state.release || {};
  const by = itemsById(rel);
  const ids = [...state.held.keys()];
  if (!ids.length) {
    return el('div', { class: 'empty' }, el('p', {}, t('services.none')));
  }
  // Which order granted each right, where one on this page still does — that is
  // what makes a return possible from here at all. It is deliberately not how the
  // *list* is built: the inventory is, because the order behind a right is
  // deleted by retention long before the right ends (ADR-0312), and a list
  // assembled from orders would start losing services on the ninetieth day.
  const lineOf = new Map();
  for (const o of state.orders) {
    for (const l of o.lines || []) lineOf.set(l.itemId, { order: o, line: l });
  }

  const row = (id) => {
    const found = lineOf.get(id);
    const can = found && returnable(found.order, found.line);
    return cell({
      text: textOf((by[id] || {}).texts, id),
      trail: el('span', {},
        can
          ? el('button', {
            class: 'sq',
            'aria-label': t('act.cancelLines'),
            title: t('act.cancelLines'),
            disabled: state.busy,
            onclick: () => giveBack(found.order, found.line),
          }, 'X')
          : null,
        ' ', infoButton(id)),
    });
  };

  // Laid out across the same four levels the catalogue uses, so somebody reading
  // what they hold sees it in the shape they ordered it in. The last column
  // carries everything at depth two or deeper: a decomposition may go further
  // than four levels, and pushing the rest off the screen would hide rights.
  const at = (want) => ids.filter((id) => (want === 2
    ? depthOf(rel, id) >= 2
    : depthOf(rel, id) === want));

  return el('div', {},
    el('div', { class: 'cascade' },
      el('div', { class: 'col' },
        el('div', { class: 'colhead' }, t('col.category')),
        cell({ text: state.catalog ? textOf(state.catalog.texts, state.catalog.id) : '' })),
      el('div', { class: 'col' },
        el('div', { class: 'colhead' }, t('col.bundle')),
        at(0).map(row)),
      el('div', { class: 'col' },
        el('div', { class: 'colhead' }, t('col.offering')),
        at(1).map(row)),
      el('div', { class: 'col' },
        el('div', { class: 'colhead' }, t('col.service')),
        at(2).map(row))),
    state.info && by[state.info] ? el('div', { style: 'margin-top:16px' }, infoPanel(by[state.info])) : null);
}

// The brand mark, and the order it is looked for in: the catalogue's own, then
// the operator's, then none at all.
//
// Nothing in the catalogue record says whether a mark exists — the file on the
// server is the fact, and a second copy of that fact is a second copy to be wrong
// after a restore that brought the record and not the image. So the page asks for
// the image and reads a 404 as "there is none", which is what the console already
// does with the instance's own (logo.js).
//
// The built-in Atlas glyph is deliberately *not* the last step, though the console
// falls back to it. This is a customer-facing page: when neither the catalogue nor
// the operator has a mark it shows none, rather than branding somebody's service
// catalogue with the name of the engine underneath it.
const INSTANCE_MARK = '/api/v1/settings/logo';
let mark = null;

function renderMark() {
  if (!state.catalog) return null;
  if (!mark) {
    // Rendered through an <img> and never inlined, so a script inside an uploaded
    // SVG has no context to run in; the server serves it sandboxed as well.
    // Decorative: the heading beside it already names the catalogue, so a screen
    // reader that announced the mark too would read it twice.
    mark = el('img', { class: 'mark', alt: '', 'aria-hidden': 'true' });
    mark.addEventListener('error', () => {
      if (mark.src.endsWith(INSTANCE_MARK)) mark.hidden = true;
      else mark.src = INSTANCE_MARK;
    });
  }
  // Assigning src re-requests the image, and render runs on every repaint — so it
  // is assigned when the catalogue changes and not when the basket does.
  if (mark.dataset.for !== state.catalog.id) {
    mark.dataset.for = state.catalog.id;
    mark.hidden = false;
    mark.src = `/api/v1/catalogs/${encodeURIComponent(state.catalog.id)}/logo`;
  }
  return mark;
}

// --- The shell ---------------------------------------------------------------

// renderNav is the row every mockup screen carries: three destinations, the help
// affordance beside the first, and whoever is signed in at the far right.
function renderNav() {
  const go = (view) => () => {
    state.view = view;
    state.inBasket = false;
    state.info = '';
    render();
  };
  const link = (view, key) => el('button', {
    class: state.view === view && !state.inBasket ? 'navlink on' : 'navlink',
    'aria-current': state.view === view && !state.inBasket ? 'page' : null,
    onclick: go(view),
  }, t(key));

  return el('nav', { class: 'nav' },
    link('catalog', 'nav.catalog'),
    // The round "?" the mockups draw beside the first entry. It goes to the
    // handbook rather than opening a panel of its own: the explaining is written
    // there already, and a second copy would be a second thing to keep true.
    el('a', { class: 'help', href: '/handbuch.html', target: '_blank', rel: 'noopener',
      title: t('nav.help'), 'aria-label': t('nav.help'),
      style: 'display:flex;align-items:center;justify-content:center;text-decoration:none' }, '?'),
    link('orders', 'nav.orders'),
    link('services', 'nav.services'),
    el('span', { class: 'spacer' }),
    el('span', { class: 'who' },
      el('span', {}, state.forWhom.trim() || t('for.self')),
      el('span', { class: 'avatar', 'aria-hidden': 'true' }, '\u25cb')));
}

// renderForWhom is the mockups' "Bestellen für: [Person suchen]".
//
// A plain field and not a picker, because this page has no directory to search:
// the server resolves a principal id, a username, a directory id or a mail
// address, and offering a dropdown would mean shipping a person search that
// answers for everybody in the estate — which is an organisation chart, and the
// reason the reminder route keeps that answer to operators.
function renderForWhom() {
  return el('div', { class: 'forwhom' },
    el('label', { class: 'muted', for: 'forwhom' }, t('for.order')),
    el('input', {
      id: 'forwhom', type: 'search', value: state.forWhom, placeholder: t('for.search'),
      oninput: (e) => { state.forWhom = e.target.value; },
    }));
}

// renderActions is the row along the bottom. Which buttons it carries is the
// step, not the screen: the cascade offers the basket, the basket offers the
// order, and both offer the way back the mockups put on every screen.
function renderActions() {
  if (state.view !== 'catalog') {
    return el('div', { class: 'actions' },
      el('a', { class: 'backlink', href: '/index.html' }, t('act.back')));
  }
  const count = state.basket.size;
  return el('div', { class: 'actions' },
    el('button', {
      class: 'secondary',
      onclick: () => {
        if (state.inBasket) { state.inBasket = false; render(); return; }
        window.location.href = '/index.html';
      },
    }, t('act.back')),
    el('button', {
      class: 'secondary',
      disabled: !count || state.busy,
      onclick: () => { state.basket.clear(); state.inBasket = false; render(); },
    }, `${t('act.discard')} \u2715`),
    state.inBasket
      ? el('button', {
        class: 'primary',
        disabled: !count || state.busy,
        onclick: order,
      }, state.busy ? t('portal.ordering') : t('act.place'))
      : el('button', {
        class: 'primary',
        disabled: !count,
        onclick: () => { state.inBasket = true; state.info = ''; render(); },
      }, `${t('act.toBasket')}${count ? ` (${count})` : ''}`));
}

function currentView() {
  if (state.view === 'orders') return renderOrders();
  if (state.view === 'services') return renderServices();
  return state.inBasket ? renderBasket() : renderCatalogue();
}

// paint replaces the page's children, dropping the ones that are not there.
//
// replaceChildren is not el(): it turns a non-node argument into a *text node*,
// so a `cond ? node : null` argument renders the word "null" on screen whenever
// the condition is false. This page has carried that since it was written — the
// error slot is conditional and there is usually no error, so a stray "null" sat
// under the header on every ordinary load.
//
// Filtering here rather than at each call site, because the next conditional
// child written the obvious way would reintroduce it.
function paint(root, ...children) {
  root.replaceChildren(...children.flat().filter((c) => c != null && c !== false));
}

function render() {
  const root = document.getElementById('app');
  if (!root) return;
  paint(root,
    el('header', {},
      el('div', { class: 'brand' },
        renderMark(),
        el('h1', {}, state.catalog ? textOf(state.catalog.texts, t('portal.title')) : t('portal.title'))),
      el('div', { class: 'headright' },
        // The way back. This page is reached from Atlas' own menu and from a link in
        // a mail, and it is a page of its own rather than a view of the shell — so
        // without this the only way out is the browser's back button, and a visitor
        // who arrived by link has no back to press.
        el('a', { class: 'backlink', href: '/index.html' }, '\u2190 ', t('portal.back')),
        el('div', { class: 'langs' }, Object.keys(STRINGS).map((l) => el('button', {
          class: l === locale ? 'lang on' : 'lang',
          onclick: () => setLocale(l),
        }, l.toUpperCase()))))),
    renderNav(),
    state.error ? el('p', { class: 'error' }, state.error,
      ' ', el('button', { onclick: load }, t('portal.retry'))) : null,
    state.view === 'catalog' ? renderForWhom() : null,
    el('h2', {}, t(state.inBasket ? 'basket.title'
      : state.view === 'orders' ? 'nav.orders'
        : state.view === 'services' ? 'nav.services' : 'nav.catalog')),
    currentView(),
    renderActions());
}

document.addEventListener('DOMContentLoaded', () => {
  document.documentElement.lang = locale;
  paintFromCache();
  render();
  load().catch((e) => { state.error = `${t('portal.failed')} ${e.message}`; render(); });
});
