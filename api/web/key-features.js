// Key features tile for the Console landing page.
//
// A short, bilingual (EN/DE) statement of what Atlas *is*, for someone who opened
// the Console without having read the README. The copy lives in the static asset
// web/key-features.json (guarded by api/keyfeatures_test.go) rather than in code,
// so it can be edited without touching the app shell — the same split the What's
// New feed uses (renderWhatsNew in app.js).
//
// The language is owned by app.js, which shares one preference across the landing
// page: this module renders the language it is handed and reports a click on its
// EN/DE toggle back through onLang, instead of persisting anything itself.
//
// Buildless and self-contained like the rest of api/web (ADR-0012, ADR-0013).

const esc = (s) => String(s).replace(/[&<>"']/g, (c) =>
  ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;", "'": "&#39;" }[c]));

// text resolves a {en, de} field for the active language, falling back to English.
const text = (b, lang) => (b && (b[lang] != null ? b[lang] : b.en)) || "";

// The fetched document, kept so a language switch repaints in place instead of
// going back to the network.
let doc = null;

// renderKeyFeatures fills the slot with the tile. A missing or malformed asset
// leaves the slot empty and silent — the landing page works without it.
export async function renderKeyFeatures(slot, lang, onLang) {
  if (!slot) return;
  if (!doc) {
    try {
      const res = await fetch("/key-features.json", { headers: { Accept: "application/json" } });
      if (!res.ok) return;
      doc = await res.json();
    } catch { return; }
  }
  paintKeyFeatures(slot, lang, onLang);
}

// paintKeyFeatures repaints the tile from what renderKeyFeatures already fetched.
// It is a no-op before that, so a language switch on a page whose asset never
// arrived does nothing rather than failing.
export function paintKeyFeatures(slot, lang, onLang) {
  if (!slot || !doc) return;
  const features = Array.isArray(doc.features) ? doc.features : [];
  if (!features.length) return;

  const items = features.map((f) =>
    `<li class="kf-item"><b class="kf-item-title">${esc(text(f.title, lang))}</b>` +
    `<span class="kf-item-text">${esc(text(f.text, lang))}</span></li>`).join("");

  slot.innerHTML =
    `<div class="card key-features"><details class="kf-root" open>` +
    `<summary class="kf-head"><span class="kf-title">${esc(text(doc.title, lang))}</span>` +
    `<span class="kf-lang">` +
    `<button type="button" data-lang="en" class="${lang === "en" ? "active" : ""}" title="Show these notes in English">EN</button>` +
    `<button type="button" data-lang="de" class="${lang === "de" ? "active" : ""}" title="Show these notes in German">DE</button>` +
    `</span></summary>` +
    `<p class="kf-intro muted">${esc(text(doc.intro, lang))}</p>` +
    `<ul class="kf-grid">${items}</ul>` +
    `</details></div>`;

  // The language toggle lives inside the <summary>; stop the click from also
  // toggling the section open/closed.
  slot.querySelectorAll(".kf-lang button").forEach((b) => b.addEventListener("click", (ev) => {
    ev.preventDefault();
    ev.stopPropagation();
    if (onLang) onLang(b.dataset.lang);
  }));
}
