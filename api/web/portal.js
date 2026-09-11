// The self-service portal (ADR-draft-portal-catalogue-order-inventory).
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
// Language follows the browser (ADR-draft-portal-language-follows-the-browser),
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
  chosen: new Set(),
  busy: false,
  error: '',
};

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

async function order(productId, options) {
  state.busy = true;
  state.error = '';
  render();
  try {
    await api('/api/v1/orders', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ releaseId: state.release.id, items: [productId, ...options] }),
    });
    state.chosen.clear();
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

function renderCatalogue() {
  if (!state.catalog) {
    return el('div', { class: 'empty' },
      el('p', {}, t('portal.none')),
      el('p', { class: 'muted' }, t('portal.none.hint')));
  }
  if (!state.release || !(state.release.items || []).length) {
    return el('div', { class: 'empty' }, el('p', {}, t('portal.empty')));
  }

  const by = itemsById(state.release);
  const cards = products(state.release).map((p) => {
    const includes = (state.release.includes || {})[p.id] || [];
    const options = (state.release.options || {})[p.id] || [];
    const picked = [...options].filter((o) => state.chosen.has(`${p.id}:${o}`));

    return el('article', { class: 'card' },
      el('h3', {}, textOf(p.texts, p.id)),
      p.approval && p.approval.kind && p.approval.kind !== 'none'
        ? el('p', { class: 'pill' }, t('portal.approval')) : null,
      includes.length ? el('div', { class: 'parts' },
        el('span', { class: 'muted' }, t('portal.includes')),
        el('ul', {}, includes.map((id) => el('li', {}, textOf((by[id] || {}).texts, id))))) : null,
      options.length ? el('div', { class: 'parts' },
        el('span', { class: 'muted' }, t('portal.options')),
        el('ul', {}, options.map((id) => {
          const key = `${p.id}:${id}`;
          return el('li', {},
            el('label', {},
              el('input', {
                type: 'checkbox',
                ...(state.chosen.has(key) ? { checked: 'checked' } : {}),
                onchange: (e) => {
                  if (e.target.checked) state.chosen.add(key); else state.chosen.delete(key);
                },
              }),
              ' ', textOf((by[id] || {}).texts, id)));
        }))) : null,
      el('button', {
        class: 'primary',
        ...(state.busy ? { disabled: 'disabled' } : {}),
        onclick: () => order(p.id, picked),
      }, state.busy ? t('portal.ordering') : t('portal.order')));
  });
  return el('div', { class: 'cards' }, cards);
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
  if (line.status !== 'done') return false;
  const held = new Set((order.lines || []).filter((l) => l.status === 'done').map((l) => l.itemId));
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

function renderOrders() {
  if (!state.orders.length) {
    return el('div', { class: 'empty' }, el('p', {}, t('portal.noOrders')));
  }
  return el('div', { class: 'cards' }, state.orders.map((o) => el('article', { class: 'card' },
    el('div', { class: 'row' },
      el('strong', {}, t(deriveStatus(o))),
      el('span', { class: 'muted' }, `${t('portal.placed')} ${new Date(o.createdAt / 1e6).toLocaleDateString(locale)}`)),
    cancellable(o)
      ? el('button', {
        class: 'secondary',
        disabled: state.busy,
        onclick: () => cancel(o),
      }, state.busy ? t('portal.cancelling') : t('portal.cancel'))
      : null,
    el('ul', { class: 'lines' }, (o.lines || []).map((l) => el('li', {},
      el('span', { class: `dot ${l.status}` }),
      ' ', l.itemId, ' — ', t(`status.${l.status}`),
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

function render() {
  const root = document.getElementById('app');
  if (!root) return;
  root.replaceChildren(
    el('header', {},
      el('div', { class: 'brand' },
        renderMark(),
        el('h1', {}, state.catalog ? textOf(state.catalog.texts, t('portal.title')) : t('portal.title'))),
      el('div', { class: 'langs' }, Object.keys(STRINGS).map((l) => el('button', {
        class: l === locale ? 'lang on' : 'lang',
        onclick: () => setLocale(l),
      }, l.toUpperCase())))),
    state.error ? el('p', { class: 'error' }, state.error,
      ' ', el('button', { onclick: load }, t('portal.retry'))) : null,
    el('section', {}, el('h2', {}, t('portal.catalog')), renderCatalogue()),
    el('section', {}, el('h2', {}, t('portal.orders')), renderOrders()));
}

document.addEventListener('DOMContentLoaded', () => {
  document.documentElement.lang = locale;
  paintFromCache();
  render();
  load().catch((e) => { state.error = `${t('portal.failed')} ${e.message}`; render(); });
});
