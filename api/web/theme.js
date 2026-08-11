// Atlas UI theming — company colors (buildless, ADR-0012 spirit).
//
// The whole app chrome (buttons, links, active nav, focus rings, pills, toggles)
// is painted from three CSS custom properties: --accent, --accent-hover and
// --accent-soft. Overriding those on the document root re-tints the entire UI to
// an organisation's brand colour, without touching any component CSS.
//
// This module is the single source of truth for how a chosen colour derives the
// hover/soft shades, how a theme is persisted, and how it is applied. The tiny
// inline bootstrap in index.html applies the *stored* variable map directly (so
// there is no flash of the default blue before app.js loads); it never needs the
// derivation logic here — that runs only when a colour is chosen and saved.

// localStorage key. The stored value is JSON: { color, vars } — the source hex
// plus the fully-derived CSS variable map the bootstrap applies verbatim.
export const THEME_KEY = "atlas.theme";

// The variables a theme overrides. Kept as a list so applyTheme can cleanly
// clear *all* of them when resetting to the built-in default.
export const THEME_VARS = ["--accent", "--accent-hover", "--accent-soft"];

// The stock Atlas accent — shown as the "default" swatch and the value we reset
// to. Mirrors :root in app.css; if that changes, change this too.
export const DEFAULT_ACCENT = "#0b5cff";

// A short menu of ready-made brand colours so picking one is a single click. The
// custom picker covers everything else.
export const PRESETS = [
  { name: "Atlas", color: DEFAULT_ACCENT },
  { name: "Indigo", color: "#4f46e5" },
  { name: "Violet", color: "#7c3aed" },
  { name: "Teal", color: "#0d7a63" },
  { name: "Emerald", color: "#047857" },
  { name: "Amber", color: "#b45309" },
  { name: "Rose", color: "#be123c" },
  { name: "Slate", color: "#334155" },
];

// normalizeHex accepts "#rgb", "#rrggbb" or the same without the leading "#" and
// returns a lowercase "#rrggbb", or null if it isn't a valid hex colour.
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

// makeTheme builds the persisted theme record for a chosen colour.
export function makeTheme(color) {
  const accent = normalizeHex(color) || DEFAULT_ACCENT;
  return { color: accent, vars: derivePalette(accent) };
}

// readTheme returns the stored theme record, or null if none/invalid.
export function readTheme() {
  try {
    const raw = localStorage.getItem(THEME_KEY);
    if (!raw) return null;
    const t = JSON.parse(raw);
    if (t && t.vars && normalizeHex(t.color)) return t;
  } catch { /* ignore malformed */ }
  return null;
}

// applyTheme sets the accent variables on the document root, or clears them all
// (falling back to app.css's :root default) when passed null.
export function applyTheme(theme) {
  const root = document.documentElement;
  if (!theme || !theme.vars) {
    for (const v of THEME_VARS) root.style.removeProperty(v);
    return;
  }
  for (const [k, val] of Object.entries(theme.vars)) {
    if (THEME_VARS.includes(k)) root.style.setProperty(k, val);
  }
}

// saveTheme persists and applies a chosen colour. Returns the stored record.
export function saveTheme(color) {
  const theme = makeTheme(color);
  try { localStorage.setItem(THEME_KEY, JSON.stringify(theme)); } catch { /* quota/denied */ }
  applyTheme(theme);
  return theme;
}

// clearTheme removes any override and restores the built-in accent.
export function clearTheme() {
  try { localStorage.removeItem(THEME_KEY); } catch { /* ignore */ }
  applyTheme(null);
}

// currentAccent is the accent colour in effect (stored override or the default),
// for seeding the settings controls.
export function currentAccent() {
  const t = readTheme();
  return (t && normalizeHex(t.color)) || DEFAULT_ACCENT;
}
