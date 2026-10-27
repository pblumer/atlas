// fold.js — every topic of a Console page folds away under its heading, and the
// browser remembers the choice (ADR-draft-every-topic-folds).
//
// A topic is a card whose first element is its heading: an <h2> on its own, or an <h2>
// in the row it shares with the card's actions (a .between). That one rule finds the
// topics of every page, the ones app.js renders and the ones its modules do, so a page
// gets folding by being written the way pages already are and nothing has to remember
// to ask for it. A card headed by an <h1> is the page's title and stays; an <h3> heads
// a form or a part of a topic, not a topic. A card that must not fold says so with
// class "no-fold".
//
// The toggle and the memory are sectionHead's in app.js: the same caret button in the
// heading, the same localStorage namespace, open until somebody folds it. A section
// drawn with sectionHead is left to its own toggle.
//
// This is a leaf: no side effects at import, nothing from app.js, so a module that
// re-renders a card can import it, and so can an e2e harness that mounts one view.

import { t } from "./i18n.js";

const STORE = "atlas.sec.";
const CONTROL = "a[href], button, input, select, textarea, [role='button']";

function remembered(key) {
  try { return localStorage.getItem(STORE + key) !== "0"; } catch { return true; }
}
function remember(key, open) {
  try { localStorage.setItem(STORE + key, open ? "1" : "0"); } catch { /* storage off: folds for this visit only */ }
}

// pageOf names the page a card is on, for its key: the route's first two segments. So
// "#/console/security" is one page, and every application's "#/modeler/p/<id>" is one
// page too: a topic folded in one application is folded in the next.
export function pageOf(hash) {
  return String(hash || "").replace(/^#\/?/, "").replace(/\?.*$/, "")
    .split("/").slice(0, 2).join("/") || "console";
}

// The heading's words name the topic when the card has no id. Digits are dropped
// because a heading that counts what it holds would otherwise be a new topic, and
// forget its state, every time the count changed.
const slug = (s) => String(s).toLowerCase().replace(/\d+/g, "").trim()
  .replace(/\s+/g, "-").replace(/[^\p{L}\p{N}-]/gu, "").replace(/-+/g, "-").slice(0, 60);

function headOf(card) {
  const head = card.firstElementChild;
  if (!head || !head.nextElementSibling) return null; // a heading alone has nothing to fold
  const h2 = head.tagName === "H2" ? head : head.querySelector(":scope > h2");
  if (!h2) return null;
  const own = h2.querySelector(".section-toggle");
  if (own && !own.hasAttribute("data-fold-toggle")) return null; // sectionHead's
  return { head, h2 };
}

function toggleOf(card) {
  const found = headOf(card);
  return found && found.h2.querySelector("[data-fold-toggle]");
}

function setOpen(card, open, persist) {
  card.toggleAttribute("data-folded", !open);
  const btn = toggleOf(card);
  if (btn) btn.setAttribute("aria-expanded", String(open));
  if (persist) remember(card.dataset.foldKey, open);
}

const watched = new WeakSet();

function foldCard(card, page) {
  const found = headOf(card);
  if (!found) {
    // What was a topic was re-rendered into something that is not one, a loading line
    // say: nothing may stay hidden behind a heading that is no longer there.
    card.removeAttribute("data-folded");
    return;
  }
  const { head, h2 } = found;
  if (!card.dataset.foldKey) {
    card.dataset.foldKey = page + "/" + (card.dataset.fold || card.id || slug(h2.textContent));
  }
  head.classList.add("fold-head");
  if (!h2.querySelector("[data-fold-toggle]")) {
    const btn = document.createElement("button");
    btn.type = "button";
    btn.className = "section-toggle";
    btn.setAttribute("data-fold-toggle", "");
    btn.title = t("section.toggle");
    if (h2.querySelector(CONTROL)) {
      // A button cannot hold a link or another button, so a heading that carries one
      // gets the caret beside its words instead of around them.
      btn.setAttribute("aria-label", h2.textContent.trim());
      h2.prepend(btn);
    } else {
      btn.append(...h2.childNodes);
      h2.append(btn);
    }
    btn.addEventListener("click", () => setOpen(card, card.hasAttribute("data-folded"), true));
  }
  // A card with no padding of its own has a heading row that pads itself and leaves
  // its bottom to the body; folded, that row is all there is, so it pads its bottom too.
  card.classList.toggle("fold-flush", getComputedStyle(card).paddingBottom === "0px");
  setOpen(card, remembered(card.dataset.foldKey), false);

  if (watched.has(card)) return;
  watched.add(card);
  // A page that redraws a card's contents in place, after a save say, keeps its fold:
  // the card's own children are watched, never its subtree, so a busy table inside it
  // costs nothing here.
  new MutationObserver(() => foldCard(card, page)).observe(card, { childList: true });
  // A control in a folded card's heading opens it before it acts: what "New secret" or
  // "New policy" does appears below the heading, and a form opened where nobody can
  // see it is a form that did not open (openSection in app.js, for the same reason).
  card.addEventListener("click", (e) => {
    if (!card.hasAttribute("data-folded")) return;
    const ctl = e.target.closest(CONTROL);
    if (!ctl || ctl.hasAttribute("data-fold-toggle")) return;
    const headNow = card.querySelector(":scope > .fold-head");
    if (headNow && headNow.contains(ctl)) setOpen(card, true, true);
  }, true);
}

// foldCards gives every topic under root its toggle and its remembered state. It is
// safe to run again on the same cards: a card is enhanced once and only re-synced.
export function foldCards(root, page) {
  for (const card of root.querySelectorAll(".card:not(.no-fold)")) foldCard(card, page);
}
