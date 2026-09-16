// The approver's page (ADR-0311).
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
    'appr.back': 'Zurück zu Atlas',
    'appr.none': 'Sie haben nichts zu genehmigen.',
    'appr.none.hint': 'Offene Anträge erscheinen hier, sobald Sie zuständig sind.',
    'appr.for': 'Bestellt für',
    'appr.by': 'Bestellt von',
    'appr.order': 'Auftrag',
    'appr.catalog': 'Katalog',
    'appr.price': 'Kosten',
    'appr.price.none': 'Der Katalog nennt keine Kosten.',
    'appr.approve': 'Genehmigen',
    'appr.reject': 'Ablehnen',
    'appr.reason': 'Begründung',
    'appr.reason.hint': 'Wird der bestellenden Person mitgeteilt. Bei einer Ablehnung erforderlich.',
    'appr.reason.missing': 'Eine Ablehnung braucht eine Begründung.',
    'appr.working': 'Wird übermittelt …',
    'appr.done': 'Entschieden. Vielen Dank.',
    'appr.failed': 'Das hat nicht geklappt.',
    'appr.retry': 'Erneut versuchen',
    'appr.all': 'Alle Genehmigungen',
    'appr.more': 'Es gibt weitere offene Aufgaben, als diese Seite auf einmal durchsucht.',
    'appr.stale': 'Die verlinkte Genehmigung ist nicht mehr offen oder nicht Ihre.',
    'appr.search': 'Suchen',
    'appr.search.hint': 'Produkt, Person, Auftrag oder Katalog',
    'appr.sort': 'Sortierung',
    'appr.sort.oldest': 'Älteste zuerst',
    'appr.sort.newest': 'Neueste zuerst',
    'appr.sort.product': 'Nach Produkt',
    'appr.sort.recipient': 'Nach Person',
    'appr.sort.due': 'Frist zuerst',
    'appr.noMatch': 'Keine Genehmigung entspricht der Suche.',
    'appr.clear': 'Suche zurücksetzen',
    'appr.count': 'von',
    'appr.escalated': 'weitergereicht',
    'appr.due': 'Frist',
    'appr.together': 'Alle Positionen dieser Anfrage gemeinsam entscheiden',
    'appr.together.hint': 'Die Begründung gilt dann für alle. Jede Position wird weiterhin einzeln abgeschlossen, weil jede ihren eigenen Prozess hat.',
    'appr.together.also': 'Diese Anfrage umfasst',
    'appr.together.positions': 'Positionen, die Sie entscheiden können.',
    'appr.partial': 'Nicht alle Positionen konnten entschieden werden. Die übrigen bleiben offen:',
  },
  en: {
    'appr.title': 'Approvals',
    'appr.back': 'Back to Atlas',
    'appr.none': 'You have nothing to approve.',
    'appr.none.hint': 'Open requests appear here once they are yours to decide.',
    'appr.for': 'Ordered for',
    'appr.by': 'Ordered by',
    'appr.order': 'Order',
    'appr.catalog': 'Catalogue',
    'appr.price': 'Cost',
    'appr.price.none': 'The catalogue names no cost.',
    'appr.approve': 'Approve',
    'appr.reject': 'Refuse',
    'appr.reason': 'Reason',
    'appr.reason.hint': 'Sent to the person who ordered. Required when refusing.',
    'appr.reason.missing': 'A refusal needs a reason.',
    'appr.working': 'Sending …',
    'appr.done': 'Decided. Thank you.',
    'appr.failed': 'That did not work.',
    'appr.retry': 'Try again',
    'appr.all': 'All approvals',
    'appr.more': 'There are more open tasks than this page searches at once.',
    'appr.stale': 'The approval that link named is no longer open, or is not yours.',
    'appr.search': 'Search',
    'appr.search.hint': 'Product, person, order or catalogue',
    'appr.sort': 'Order',
    'appr.sort.oldest': 'Oldest first',
    'appr.sort.newest': 'Newest first',
    'appr.sort.product': 'By product',
    'appr.sort.recipient': 'By person',
    'appr.sort.due': 'Due first',
    'appr.noMatch': 'No approval matches the search.',
    'appr.clear': 'Clear search',
    'appr.count': 'of',
    'appr.escalated': 'passed on',
    'appr.due': 'Due',
    'appr.together': 'Decide every position of this request together',
    'appr.together.hint': 'The reason then covers all of them. Each position is still completed on its own, because each has its own process.',
    'appr.together.also': 'This request has',
    'appr.together.positions': 'positions you can decide.',
    'appr.partial': 'Not every position could be decided. The rest are still open:',
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
  // query and sort are how an approver finds one decision among forty
  // (ADR-0354). They live here and not in the URL: this page
  // is reached from a mail link that already carries ?order=, and a second set of
  // parameters on the same link would be two ways to say where somebody is.
  query: '',
  sort: 'oldest',
  selected: null,
  stale: false,
  reason: '',
  busy: false,
  decided: false,
  // together is the approver saying "this is one decision about one request"
  // (ADR-0362). Opt-in and never remembered across a
  // selection: a person who ticked it for a twelve-line workplace has not said
  // anything about the next request they open.
  together: false,
  // partial names the positions a collective decision did not get through, which
  // is the one outcome the page must not round off. There is no transaction
  // across twelve process instances, so "eleven of twelve" is a thing that can
  // happen and the approver has to be told which one.
  partial: [],
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

// siblings is every open approval this caller holds on the same order, the
// selected one included (ADR-0362).
//
// An order is the unit because a request is: the approval process runs per line,
// so a workplace ordered as twelve products is twelve tasks, and the person
// deciding them is deciding one request. Lines of a *different* order are not
// here, and the server refuses them too — one reason cannot cover two requests.
function siblings(a) {
  if (!a) return [];
  return state.approvals.filter((o) => o.orderId === a.orderId);
}

// decide completes the task, which is what hands the answer back to the process.
// The page does not write the order: what a decision *means* — start provisioning,
// or record a refusal and tell the orderer — is modelled in the approval process,
// and a page that did it itself would be a second implementation of it.
//
// Two shapes, one decision. Alone it completes the one task, which is the path
// every other task surface uses. Together it posts the order's approvals to the
// collective route, which completes each of them as its own task with the same
// answer — because each is still its own process instance and each still has to
// act on what it was told.
async function decide(approved) {
  const a = state.selected;
  if (!a || state.busy) return;
  const reason = state.reason.trim();
  if (!approved && reason === '') {
    state.error = t('appr.reason.missing');
    render();
    return;
  }
  const batch = state.together ? siblings(a) : [a];
  state.busy = true;
  state.error = '';
  state.partial = [];
  render();
  try {
    if (batch.length > 1) {
      const { body } = await api('/api/v1/approvals/decide', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({
          approved, reason, taskKeys: batch.map((o) => o.task.key),
        }),
      });
      // A partial result is reported rather than rounded off. The server cannot
      // promise twelve completions or none — a completion that went through has
      // already handed its answer to its process — so the page says which ones
      // did not, and those stay in the list.
      state.partial = (body && body.skipped) || [];
    } else {
      await api(`/api/v1/tasks/${encodeURIComponent(String(a.task.key))}/complete`, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ variables: { genehmigt: approved, begruendung: reason } }),
      });
    }
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
  state.together = false;
  state.partial = [];
  applyTheme(a);
  render();
}

// --- Finding one decision among forty ----------------------------------------
//
// The page shows one thing well (ADR-0311) and that is deliberate: the common
// approver decides perhaps four times a year, and a tool with Deployments and
// Incidents in it is one they ask a colleague to use. So this is a search field
// and a sort control rather than a five-column table — the list has one row per
// approval carrying two facts, and a table would be heavier than the page's
// purpose.

// ageKey is how old an approval is, as the server itself measures it.
//
// The job key is monotonic and the approvals endpoint already pages by it, so a
// higher key is a newer task. That is the only age this page can read: a task
// carries no created-at, and inventing one from a clock here would be a number
// nobody can check.
function ageKey(a) {
  return (a.task && a.task.key) || 0;
}

// matches reports whether one approval survives the search.
//
// One field across every column rather than a field per column, because an
// approver looking for "the laptop for Ada" does not know which column they are
// searching — and a five-field row would ask them to.
function matches(a) {
  const q = state.query.trim().toLowerCase();
  if (!q) return true;
  return [
    textOf(a.texts, a.itemId), a.itemId, a.recipient, a.orderer, a.orderId,
    textOf(a.catalogTexts, a.catalogId),
  ].some((v) => String(v || '').toLowerCase().includes(q));
}

const SORTS = {
  // Oldest first is the default, and it is a change from the endpoint's own
  // order. What has waited longest is what nobody has looked at — the argument
  // the recertification and conflict reports both make about their own lists —
  // and a work list read from the top should start there.
  oldest: (a, b) => ageKey(a) - ageKey(b),
  newest: (a, b) => ageKey(b) - ageKey(a),
  product: (a, b) => textOf(a.texts, a.itemId).localeCompare(textOf(b.texts, b.itemId), locale),
  recipient: (a, b) => String(a.recipient || '').localeCompare(String(b.recipient || ''), locale),
  // A due date first, and everything without one after it in age order: a task
  // somebody put a deadline on is a different thing from one nobody did, and
  // sorting the undated in among them would bury the deadlines.
  due: (a, b) => {
    const da = (a.task && a.task.dueDate) || 0;
    const db = (b.task && b.task.dueDate) || 0;
    if (!da !== !db) return da ? -1 : 1;
    if (da !== db) return da - db;
    return ageKey(a) - ageKey(b);
  },
};

function visibleApprovals() {
  return state.approvals.filter(matches).sort(SORTS[state.sort] || SORTS.oldest);
}

// rowNote is what the row says beyond the product and the person.
//
// Only what the approval actually carries. An assignment record exists once a
// deadline or a person has moved the approval, and its absence is the answer
// "nobody has had to chase this" — so a row without one says nothing rather than
// showing an invented age.
function rowNote(a) {
  const bits = [];
  const due = a.task && a.task.dueDate;
  if (due) bits.push(`${t('appr.due')} ${new Date(due).toLocaleDateString(locale)}`);
  if (a.assignment && (a.assignment.escalations || []).length) {
    bits.push(t('appr.escalated'));
  }
  return bits.join(' · ');
}

function renderControls(shown) {
  return el('div', { class: 'controls' },
    el('input', {
      type: 'search', id: 'appr-search', value: state.query,
      placeholder: t('appr.search.hint'), 'aria-label': t('appr.search'),
      oninput: (e) => {
        state.query = e.target.value;
        // Repaint the list alone, or the caret jumps to the end of the field
        // somebody is typing in the middle of.
        repaintList();
      },
    }),
    el('label', { class: 'sortwrap' },
      el('span', { class: 'muted' }, t('appr.sort')),
      el('select', {
        id: 'appr-sort',
        onchange: (e) => { state.sort = e.target.value; repaintList(); },
      }, Object.keys(SORTS).map((k) => el('option', {
        value: k, ...(state.sort === k ? { selected: 'selected' } : {}),
      }, t(`appr.sort.${k}`))))),
    el('span', { class: 'muted count' }, `${shown} ${t('appr.count')} ${state.approvals.length}`));
}

let listNode = null;

function repaintList() {
  if (!listNode) return;
  const shown = visibleApprovals();
  listNode.replaceChildren(...listBodies(shown));
  const count = document.querySelector('.controls .count');
  if (count) count.textContent = `${shown.length} ${t('appr.count')} ${state.approvals.length}`;
}

function listBodies(shown) {
  if (!shown.length) {
    return [el('li', { class: 'muted' }, t('appr.noMatch'))];
  }
  return shown.map((a) => el('li', {},
    el('button', { class: 'pick', onclick: () => select(a) },
      el('strong', {}, textOf(a.texts, a.itemId)),
      el('span', { class: 'muted' }, ` — ${t('appr.for')} ${a.recipient || '\u2014'}`),
      a.price ? el('span', { class: 'muted' }, ` — ${a.price}`) : null,
      rowNote(a) ? el('span', { class: 'muted note' }, rowNote(a)) : null)));
}

function renderList() {
  if (!state.approvals.length) {
    return el('div', { class: 'empty' },
      el('p', {}, t('appr.none')),
      el('p', { class: 'muted' }, t('appr.none.hint')));
  }
  const shown = visibleApprovals();
  listNode = el('ul', { class: 'list' }, listBodies(shown));
  // The controls are shown from the first row rather than past a threshold: a
  // list that grew a search box at the eleventh approval would be a different
  // page each time somebody arrived.
  return el('div', {}, renderControls(shown.length), listNode);
}

// renderTogether is the collective decision's whole surface: what else is in this
// request, and one checkbox (ADR-0362).
//
// Opt-in, and absent when the request has one position — a checkbox offering to
// decide "all one of them" is a control that teaches somebody to tick boxes
// without reading. The other positions are listed with their prices rather than
// counted, because the thing being ticked is "I have seen what is in this
// request", and a number is not something anybody can have seen.
function renderTogether(rest) {
  return el('div', { class: 'together' },
    el('p', { class: 'muted' },
      `${t('appr.together.also')} ${rest.length + 1} ${t('appr.together.positions')}`),
    el('ul', { class: 'siblings' }, rest.map((o) => el('li', { class: 'muted' },
      textOf(o.texts, o.itemId),
      o.price ? ` — ${o.price}` : ''))),
    el('label', { class: 'togglewrap' },
      el('input', {
        type: 'checkbox', ...(state.together ? { checked: 'checked' } : {}),
        onchange: (e) => { state.together = e.target.checked; render(); },
      }),
      el('span', {}, t('appr.together'))),
    el('p', { class: 'muted' }, t('appr.together.hint')));
}

function renderDecision() {
  const a = state.selected;
  if (state.decided) {
    return el('div', { class: 'empty' },
      el('p', {}, t('appr.done')),
      // What did not go through, named. An approver told "decided" while three
      // positions are still open would find out from the orderer.
      state.partial.length
        ? el('div', {},
          el('p', {}, t('appr.partial')),
          el('ul', { class: 'siblings' }, state.partial.map((o) => el('li', { class: 'muted' },
            `${o.itemId || o.taskKey} — ${o.error || ''}`))))
        : null,
      el('p', {}, el('a', { href: location.pathname }, t('appr.all'))));
  }
  const rest = siblings(a).filter((o) => o.task.key !== a.task.key);
  const count = state.together && rest.length ? ` (${rest.length + 1})` : '';
  return el('div', { class: 'card' },
    el('h3', {}, textOf(a.texts, a.itemId)),
    el('dl', {},
      el('dt', {}, t('appr.for')), el('dd', {}, a.recipient || '—'),
      el('dt', {}, t('appr.by')), el('dd', {}, a.orderer || '—'),
      el('dt', {}, t('appr.order')), el('dd', {}, a.orderId),
      el('dt', {}, t('appr.catalog')), el('dd', {}, textOf(a.catalogTexts, a.catalogId || '—')),
      // The figure the order froze, as the catalogue wrote it. An approver
      // deciding without it is deciding half the question — and a page that
      // reformatted it would be inventing a money model the catalogue does not
      // have (ADR-0361).
      el('dt', {}, t('appr.price')),
      el('dd', a.price ? {} : { class: 'muted' }, a.price || t('appr.price.none'))),
    rest.length ? renderTogether(rest) : null,
    el('label', { class: 'reason' },
      el('span', {}, t('appr.reason')),
      el('textarea', {
        rows: '3',
        oninput: (e) => { state.reason = e.target.value; },
      }, state.reason),
      el('span', { class: 'muted' }, t('appr.reason.hint'))),
    el('div', { class: 'actions' },
      // The count is on the buttons and not only beside the checkbox: the button
      // is what somebody presses, and it is the last thing they read before the
      // decision is irreversible.
      el('button', {
        class: 'primary', disabled: state.busy ? 'disabled' : null,
        onclick: () => decide(true),
      }, state.busy ? t('appr.working') : `${t('appr.approve')}${count}`),
      el('button', {
        class: 'secondary', disabled: state.busy ? 'disabled' : null,
        onclick: () => decide(false),
      }, `${t('appr.reject')}${count}`)));
}

// paint replaces the page's children, dropping the ones that are not there.
//
// replaceChildren is not el(): it turns a non-node argument into a *text node*,
// so a `cond ? node : null` argument renders the word "null" on screen whenever
// the condition is false. This page has three such slots — an error, a stale
// link, a truncation notice — and none of them is usually filled, so an ordinary
// load has always shown "nullnullnull" above the list and "null" below it.
//
// Filtering here rather than at each call site, because the next conditional
// child written the obvious way would reintroduce it. The portal carried the same
// defect and is fixed the same way.
function paint(root, ...children) {
  root.replaceChildren(...children.flat().filter((c) => c != null && c !== false));
}

function render() {
  const root = document.getElementById('app');
  if (!root) return;
  const a = state.selected;
  paint(root,
    el('header', {},
      el('div', { class: 'brand' },
        renderMark(a),
        el('h1', {}, a ? textOf(a.catalogTexts, t('appr.title')) : t('appr.title'))),
      el('div', { class: 'headright' },
        // Same reason as the portal's: an approver arrives here from a mail link or
        // from the menu, and a page of its own with no way back strands whoever
        // followed it.
        el('a', { class: 'backlink', href: '/index.html' }, '\u2190 ', t('appr.back')),
        el('div', { class: 'langs' }, Object.keys(STRINGS).map((l) => el('button', {
          class: l === locale ? 'lang on' : 'lang',
          onclick: () => setLocale(l),
        }, l.toUpperCase()))))),
    state.error ? el('p', { class: 'error' }, state.error) : null,
    state.stale ? el('p', { class: 'muted' }, t('appr.stale')) : null,
    state.truncated ? el('p', { class: 'muted' }, t('appr.more')) : null,
    a ? renderDecision() : renderList(),
    a && !state.decided && state.approvals.length > 1
      ? el('p', {}, el('a', { href: location.pathname }, t('appr.all'))) : null);
}

document.addEventListener('DOMContentLoaded', () => {
  document.documentElement.lang = locale;
  render();
  load().catch((e) => { state.error = `${t('appr.failed')} ${e.message}`; render(); });
});
