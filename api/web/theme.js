// Atlas UI theming — org-wide company colours (buildless, ADR-0012 / ADR-0113).
//
// The whole app chrome (buttons, links, active nav, focus rings, pills, toggles)
// is painted from three CSS custom properties: --accent, --accent-hover and
// --accent-soft. Overriding those on the document root re-tints the entire UI to
// an organisation's brand colour, without touching any component CSS.
//
// The brand accent is **org-wide**: it is stored on the server
// (GET/PUT/DELETE /api/v1/settings/theme) so every user of the instance sees it.
// localStorage is only a client-side cache of the last-applied palette, so the
// tiny inline bootstrap in index.html can paint the brand colour before this
// module (or the network) has loaded — no flash of the default blue. The server
// value always wins: syncFromServer() reconciles the cache on boot.
//
// This module is the single source of truth for how a chosen colour derives the
// hover/soft shades. The server stores only the source accent; the shades are
// derived here.

// localStorage cache key. Value is JSON: { color, vars } — the source hex plus
// the fully-derived CSS variable map the bootstrap applies verbatim.
export const THEME_KEY = "atlas.theme";

// The server endpoint that holds the org-wide accent.
const THEME_URL = "/api/v1/settings/theme";

// The variables a theme overrides. Kept as a list so applyAccent can cleanly
// clear *all* of them when resetting to the built-in default.
export const THEME_VARS = ["--accent", "--accent-hover", "--accent-soft"];

// The stock Atlas accent — shown as the "default" swatch and the value we reset
// to. Mirrors :root in app.css; if that changes, change this too.
export const DEFAULT_ACCENT = "#0b5cff";

// A short menu of ready-made brand colours so picking one is a single click. The
// custom picker covers everything else.
//
// "CD Bund" is the Swiss Confederation federal red (Bundesrot) from the CD Bund
// corporate design (bk.admin.ch/de/corporate-design-grundelemente): Pantone 485,
// i.e. the flag/coat-of-arms red, #D52B1E. It is the one-click colour template the
// federal design asks for; the hover/soft shades derive from it like any other
// accent. Only the accent is themed here (ADR-0113) — the CD Bund's typography and
// layout rules stay out of scope for a colour theme, and the logo is set separately
// (ADR-0148), so a federal office pairs this red with its own uploaded logo.
export const PRESETS = [
  { name: "Atlas", color: DEFAULT_ACCENT },
  { name: "CD Bund", color: "#d52b1e" },
  { name: "Indigo", color: "#4f46e5" },
  { name: "Violet", color: "#7c3aed" },
  { name: "Teal", color: "#0d7a63" },
  { name: "Emerald", color: "#047857" },
  { name: "Amber", color: "#b45309" },
  { name: "Rose", color: "#be123c" },
  { name: "Slate", color: "#334155" },
];

// normalizeHex accepts "#rgb", "#rrggbb" or the same without the leading "#" and
// returns a lowercase "#rrggbb", or null if it isn't a valid hex colour. Mirrors
// the server's normalizeAccent (settings.go).
export function normalizeHex(input) {
  if (typeof input !== "string") return null;
  let h = input.trim().toLowerCase();
  if (h[0] === "#") h = h.slice(1);
  if (/^[0-9a-f]{3}$/.test(h)) h = h.split("").map((c) => c + c).join("");
  if (!/^[0-9a-f]{6}$/.test(h)) return null;
  return "#" + h;
}

function toRgb(hex) {
  const h = normalizeHex(hex) || DEFAULT_ACCENT;
  return [1, 3, 5].map((i) => parseInt(h.slice(i, i + 2), 16));
}

function toHex([r, g, b]) {
  const c = (n) => Math.max(0, Math.min(255, Math.round(n))).toString(16).padStart(2, "0");
  return "#" + c(r) + c(g) + c(b);
}

// mix blends `amount` of `other` into `base` (both hex), amount in [0,1].
function mix(base, other, amount) {
  const a = toRgb(base);
  const b = toRgb(other);
  return toHex(a.map((v, i) => v * (1 - amount) + b[i] * amount));
}

// derivePalette turns one brand colour into the full accent variable map. The
// hover shade is the colour darkened ~18% (matching the stock #0b5cff→#0a4ad1
// step); the soft shade is a ~90% white tint, the pale background used behind
// pills, focus rings and the active drawer item.
export function derivePalette(color) {
  const accent = normalizeHex(color) || DEFAULT_ACCENT;
  return {
    "--accent": accent,
    "--accent-hover": mix(accent, "#000000", 0.18),
    "--accent-soft": mix(accent, "#ffffff", 0.9),
  };
}

// ---------- Applying + local cache ----------

// applyAccent sets the accent variables on the document root, or clears them all
// (falling back to app.css's :root default) when passed a falsy colour.
export function applyAccent(color) {
  const root = document.documentElement;
  const norm = normalizeHex(color);
  if (!norm) {
    for (const v of THEME_VARS) root.style.removeProperty(v);
    return;
  }
  for (const [k, val] of Object.entries(derivePalette(norm))) root.style.setProperty(k, val);
}

// readCache returns the cached { color, vars } record, or null if none/invalid.
export function readCache() {
  try {
    const raw = localStorage.getItem(THEME_KEY);
    if (!raw) return null;
    const t = JSON.parse(raw);
    if (t && t.vars && normalizeHex(t.color)) return t;
  } catch { /* ignore malformed */ }
  return null;
}

function writeCache(color) {
  const norm = normalizeHex(color);
  if (!norm) return;
  try {
    localStorage.setItem(THEME_KEY, JSON.stringify({ color: norm, vars: derivePalette(norm) }));
  } catch { /* quota/denied — the server still holds the source of truth */ }
}

function clearCache() {
  try { localStorage.removeItem(THEME_KEY); } catch { /* ignore */ }
}

// currentAccent is the accent colour in effect locally (cached override or the
// default), for seeding the settings controls synchronously.
export function currentAccent() {
  const t = readCache();
  return (t && normalizeHex(t.color)) || DEFAULT_ACCENT;
}

// applyCurrent re-applies exactly what the cache holds (or clears to default) —
// used to revert an optimistic preview when a server write fails.
export function applyCurrent() {
  const t = readCache();
  applyAccent(t ? t.color : null);
}

// ---------- Server (source of truth) ----------

// getServerAccent reads the org-wide accent, returning "" when none is set. It
// resolves to null on a network/parse failure so callers can distinguish "no
// override" ("") from "couldn't reach the server" (null) and keep the cache.
export async function getServerAccent() {
  try {
    const res = await fetch(THEME_URL);
    if (!res.ok) return null;
    const body = await res.json();
    return normalizeHex(body && body.accent) || "";
  } catch {
    return null;
  }
}

// setServerAccent persists the org-wide accent (admin-only when auth is on). It
// throws an Error carrying the server's message on failure (e.g. a 403 for a
// non-admin), so the caller can surface it and revert.
export async function setServerAccent(color) {
  const norm = normalizeHex(color);
  if (!norm) throw new Error("Not a valid colour");
  const res = await fetch(THEME_URL, {
    method: "PUT",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ accent: norm }),
  });
  if (!res.ok) throw new Error(await errorMessage(res));
  writeCache(norm);
  return norm;
}

// resetServerAccent clears the org-wide override, restoring the built-in accent.
export async function resetServerAccent() {
  const res = await fetch(THEME_URL, { method: "DELETE" });
  if (!res.ok) throw new Error(await errorMessage(res));
  clearCache();
}

async function errorMessage(res) {
  try {
    const body = await res.json();
    if (body && body.error) return body.error;
  } catch { /* fall through */ }
  return res.status === 403
    ? "Changing the theme requires the admin role"
    : `Request failed (${res.status})`;
}

// syncFromServer reconciles the local cache with the org-wide value on boot: the
// server's accent is applied and cached; an empty value clears any local override;
// an unreachable server leaves the cache (already applied by the bootstrap) intact.
export async function syncFromServer() {
  const accent = await getServerAccent();
  if (accent === null) return; // offline — keep whatever the bootstrap applied
  if (accent) {
    applyAccent(accent);
    writeCache(accent);
  } else {
    applyAccent(null);
    clearCache();
  }
}
