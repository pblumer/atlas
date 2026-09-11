// The approver's page (ADR-draft-portal-approval-page).
//
// An approval is a user task and the Console can work it. That is the wrong place
// for most of the people who get one: the common approver is the line manager the
// directory resolved, who decides perhaps four times a year and for whom a tool
// with Deployments, Instances and Incidents in it is a tool they will ask a
// colleague to use for them.
//
// So this page shows one thing — what was ordered, for whom, by whom — and offers
// two answers. It carries the brand of the catalogue the order came from, because
// the approver is deciding on that customer's behalf and a decision taken in
// somebody else's colours is a decision taken about the wrong customer.
//
// It reads two endpoints and writes one. Everything it shows comes from
// GET /api/v1/approvals, which does the join the approver may not do themselves
// (task → order → release → catalogue); the decision is an ordinary task
// completion, which is what keeps the process in charge of what a decision means.
//
// Language follows the browser, as the portal's does, on the same condition: every
// string below exists in every locale, held by TestApprovalCatalogueIsComplete.

import { applyAccent } from './theme.js';

const STRINGS = {
  de: {
    'appr.title': 'Genehmigungen',
    'appr.none': 'Sie haben nichts zu genehmigen.',
    'appr.none.hint': 'Offene Anträge erscheinen hier, sobald Sie zuständig sind.',
    'appr.for': 'Bestellt für',
    'appr.by': 'Bestellt von',
    'appr.order': 'Auftrag',
    'appr.catalog': 'Katalog',
    'appr.approve': 'Genehmigen',
    'appr.reject': 'Ablehnen',
    'appr.reason': 'Begründung',
    'appr.reason.hint': 'Wird der bestellenden Person mitgeteilt. Bei einer Ablehnung erforderlich.',
    'appr.reason.missing': 'Eine Ablehnung braucht eine Begründung.',
    'appr.working': 'Wird übermittelt …',
    'appr.done': 'Entschieden. Vielen Dank.',
    'appr.failed': 'Das hat nicht geklappt.',
    'appr.retry': 'Erneut versuchen',
    'appr.back': 'Alle Genehmigungen',
    'appr.more': 'Es gibt weitere offene Aufgaben, als diese Seite auf einmal durchsucht.',
    'appr.stale': 'Die verlinkte Genehmigung ist nicht mehr offen oder nicht Ihre.',
  },
  en: {
    'appr.title': 'Approvals',
    'appr.none': 'You have nothing to approve.',
    'appr.none.hint': 'Open requests appear here once they are yours to decide.',
    'appr.for': 'Ordered for',
    'appr.by': 'Ordered by',
    'appr.order': 'Order',
    'appr.catalog': 'Catalogue',
    'appr.approve': 'Approve',
    'appr.reject': 'Refuse',
    'appr.reason': 'Reason',
    'appr.reason.hint': 'Sent to the person who ordered. Required when refusing.',
    'appr.reason.missing': 'A refusal needs a reason.',
    'appr.working': 'Sending …',
    'appr.done': 'Decided. Thank you.',
    'appr.failed': 'That did not work.',
    'appr.retry': 'Try again',
    'appr.back': 'All approvals',
    'appr.more': 'There are more open tasks than this page searches at once.',
    'appr.stale': 'The approval that link named is no longer open, or is not yours.',
  },
};

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

function t(key) {
  return (STRINGS[locale] && STRINGS[locale][key]) || key;
}

function setLocale(next) {
  locale = next;
  try { localStorage.setItem('portal.lang', next); } catch { /* private window */ }
  render();
}

// textOf reads a name in the current locale. A product is named by the catalogue
// that froze it, not by this page, so there is no key to look up.
function textOf(texts, fallback) {
  if (!texts) return fallback;
  return texts[locale] || texts.de || texts.en || Object.values(texts)[0] || fallback;
}

// The typeface stacks the server ships, mirrored because the page paints with
// them. The server refuses a name that is not one of these, so a catalogue cannot
// name a face this page has no stack for.
const TYPEFACES = {
  system: 'system-ui, -apple-system, "Segoe UI", Roboto, sans-serif',
  humanist: '"Segoe UI", Candara, Optima, "Trebuchet MS", sans-serif',
  serif: 'Georgia, Cambria, "Times New Roman", serif',
  mono: 'ui-monospace, "SF Mono", "Cascadia Mono", Menlo, monospace',
};

// applyTheme paints the brand of the catalogue whose order is being decided.
//
// Deliberately keyed to the *selected* approval and not to the page. An approver
// holds requests from several customer groups at once, and there is no one brand
// for that list — the brand belongs to the decision. Nothing is cached: unlike the
// portal, where a returning visitor's catalogue is a fact about them, which
// approval is open here is a fact about the moment, and a cached paint would open
// the page in the colours of whoever was approved last.
function applyTheme(approval) {
  const theme = (approval && approval.theme) || {};
  applyAccent(theme.accent || '');
  document.documentElement.style.setProperty(
    '--font-sans',
    (theme.typeface && TYPEFACES[theme.typeface]) || TYPEFACES.system,
  );
}

// The brand mark, and the order it is looked for in: the catalogue's own — served
// under the approval's own gate, because an approver is not the catalogue's
// audience and the catalogue's own logo route would rightly refuse them — then the
// operator's, then none.
//
// The built-in Atlas glyph is deliberately not the last step, as on the portal.
// This page is shown to somebody deciding on a customer's behalf; the engine's own
// mark has no place on it.
const INSTANCE_MARK = '/api/v1/settings/logo';
let mark = null;

function renderMark(approval) {
  if (!approval) return null;
  if (!mark) {
    // Through an <img> and never inlined, so a script inside an uploaded SVG has
    // no context to run in. Decorative: the heading beside it names the catalogue.
    mark = el('img', { class: 'mark', alt: '', 'aria-hidden': 'true' });
    mark.addEventListener('error', () => {
      if (mark.src.endsWith(INSTANCE_MARK)) mark.hidden = true;
      else mark.src = INSTANCE_MARK;
    });
  }
  const key = String(approval.task.key);
  if (mark.dataset.for !== key) {
    mark.dataset.for = key;
    mark.hidden = false;
    mark.src = `/api/v1/approvals/${encodeURIComponent(key)}/logo`;
  }
  return mark;
}

const state = {
  approvals: [],
  selected: null,
  stale: false,
  reason: '',
  busy: false,
  decided: false,
  truncated: false,
  error: '',
};

async function api(path, options) {
  const res = await fetch(path, { credentials: 'same-origin', ...options });
  if (!res.ok) throw new Error(`${res.status} ${await res.text()}`);
  return { body: res.status === 204 ? null : await res.json(), headers: res.headers };
}

async function load() {
  state.error = '';
  const { body, headers } = await api('/api/v1/approvals');
  state.approvals = body || [];
  state.truncated = headers.get('X-Tasks-Truncated') === 'true';

  // A link from a notification names one approval. That is the arrival this page
  // is built for: one decision, already open, in the right colours.
  //
  // It names the *order line* and not the task, because the notification is sent
  // by the approval process and a task key does not exist until the task the
  // notification is about has activated. An order and a product do exist by then,
  // they are what the approver was told about, and they are stable — a task
  // reassigned or retried keeps them while its key changes.
  const q = new URLSearchParams(location.search);
  const order = q.get('order');
  const item = q.get('item');
  const pick = order && state.approvals.find(
    (a) => a.orderId === order && (!item || a.itemId === item),
  );
  state.selected = pick || (state.approvals.length === 1 ? state.approvals[0] : null);
  // A link that names an approval this person does not hold — decided already,
  // reassigned, or never theirs — falls back to their list rather than to an
  // error. There is nothing they can do about it and the list is what they came
  // for.
  state.stale = Boolean(order && !pick);
  applyTheme(state.selected);
  render();
}

// decide completes the task, which is what hands the answer back to the process.
// The page does not write the order: what a decision *means* — start provisioning,
// or record a refusal and tell the orderer — is modelled in the approval process,
// and a page that did it itself would be a second implementation of it.
async function decide(approved) {
  const a = state.selected;
  if (!a || state.busy) return;
  const reason = state.reason.trim();
  if (!approved && reason === '') {
    state.error = t('appr.reason.missing');
    render();
    return;
  }
  state.busy = true;
  state.error = '';
  render();
  try {
    await api(`/api/v1/tasks/${encodeURIComponent(String(a.task.key))}/complete`, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ variables: { genehmigt: approved, begruendung: reason } }),
    });
    state.decided = true;
    state.busy = false;
    render();
  } catch (e) {
    state.busy = false;
    state.error = `${t('appr.failed')} ${e.message}`;
    render();
  }
}

function el(tag, attrs, ...children) {
  const node = document.createElement(tag);
  for (const [k, v] of Object.entries(attrs || {})) {
    // A nullish or false value means "do not set this attribute". Without that,
    // `disabled: busy ? 'disabled' : null` renders disabled="null", which a browser
    // reads as disabled — both buttons on this page would have been dead from the
    // first paint. setAttribute has no falsy handling of its own and never will.
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

function select(a) {
  state.selected = a;
  state.reason = '';
  state.error = '';
  applyTheme(a);
  render();
}

function renderList() {
  if (!state.approvals.length) {
    return el('div', { class: 'empty' },
      el('p', {}, t('appr.none')),
      el('p', { class: 'muted' }, t('appr.none.hint')));
  }
  return el('ul', { class: 'list' }, state.approvals.map((a) => el('li', {},
    el('button', { class: 'pick', onclick: () => select(a) },
      el('strong', {}, textOf(a.texts, a.itemId)),
      el('span', { class: 'muted' }, ` — ${t('appr.for')} ${a.recipient || '—'}`)))));
}

function renderDecision() {
  const a = state.selected;
  if (state.decided) {
    return el('div', { class: 'empty' },
      el('p', {}, t('appr.done')),
      el('p', {}, el('a', { href: location.pathname }, t('appr.back'))));
  }
  return el('div', { class: 'card' },
    el('h3', {}, textOf(a.texts, a.itemId)),
    el('dl', {},
      el('dt', {}, t('appr.for')), el('dd', {}, a.recipient || '—'),
      el('dt', {}, t('appr.by')), el('dd', {}, a.orderer || '—'),
      el('dt', {}, t('appr.order')), el('dd', {}, a.orderId),
      el('dt', {}, t('appr.catalog')), el('dd', {}, textOf(a.catalogTexts, a.catalogId || '—'))),
    el('label', { class: 'reason' },
      el('span', {}, t('appr.reason')),
      el('textarea', {
        rows: '3',
        oninput: (e) => { state.reason = e.target.value; },
      }, state.reason),
      el('span', { class: 'muted' }, t('appr.reason.hint'))),
    el('div', { class: 'actions' },
      el('button', {
        class: 'primary', disabled: state.busy ? 'disabled' : null,
        onclick: () => decide(true),
      }, state.busy ? t('appr.working') : t('appr.approve')),
      el('button', {
        class: 'secondary', disabled: state.busy ? 'disabled' : null,
        onclick: () => decide(false),
      }, t('appr.reject'))));
}

function render() {
  const root = document.getElementById('app');
  if (!root) return;
  const a = state.selected;
  root.replaceChildren(
    el('header', {},
      el('div', { class: 'brand' },
        renderMark(a),
        el('h1', {}, a ? textOf(a.catalogTexts, t('appr.title')) : t('appr.title'))),
      el('div', { class: 'langs' }, Object.keys(STRINGS).map((l) => el('button', {
        class: l === locale ? 'lang on' : 'lang',
        onclick: () => setLocale(l),
      }, l.toUpperCase())))),
    state.error ? el('p', { class: 'error' }, state.error) : null,
    state.stale ? el('p', { class: 'muted' }, t('appr.stale')) : null,
    state.truncated ? el('p', { class: 'muted' }, t('appr.more')) : null,
    a ? renderDecision() : renderList(),
    a && !state.decided && state.approvals.length > 1
      ? el('p', {}, el('a', { href: location.pathname }, t('appr.back'))) : null);
}

document.addEventListener('DOMContentLoaded', () => {
  document.documentElement.lang = locale;
  render();
  load().catch((e) => { state.error = `${t('appr.failed')} ${e.message}`; render(); });
});
