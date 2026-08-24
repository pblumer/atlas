// Atlas UI branding — org-wide brand logo (buildless, ADR-0148).
//
// A customer can replace the built-in "A" letter mark with their own logo. The
// logo is **org-wide**: it is stored on the server (GET/PUT/DELETE
// /api/v1/settings/logo) so every user of the instance — and every visitor to the
// login screen — sees it. This mirrors the org-wide brand colour (theme.js): the
// server is the source of truth, and localStorage caches only *whether* a logo is
// set so the boot path can paint the right mark before the network responds.
//
// The logo image itself is always rendered through an <img> (never inlined into the
// DOM), so a hostile <script> inside an uploaded SVG never executes; the server
// additionally serves it under a strict CSP. Keep it that way.

// The endpoint holding the org-wide logo image.
export const LOGO_URL = "/api/v1/settings/logo";

// localStorage flag: "1" when a logo is set. Only a boolean is cached — the bytes
// come from LOGO_URL (and the browser's HTTP cache), never from localStorage.
const LOGO_FLAG = "atlas.logo";

// The accepted upload formats and the client-side size cap. These mirror the
// server (settings.go): the server re-validates, so this is just fast, friendly
// feedback before a doomed upload.
export const LOGO_TYPES = ["image/png", "image/svg+xml"];
export const LOGO_MAX_BYTES = 512 * 1024;

// hasLogoCached reports the last-known presence of a logo, for the synchronous boot
// paint before syncLogoFromServer reconciles with the server.
export function hasLogoCached() {
  try { return localStorage.getItem(LOGO_FLAG) === "1"; } catch { return false; }
}

function cacheLogoPresent(present) {
  try {
    if (present) localStorage.setItem(LOGO_FLAG, "1");
    else localStorage.removeItem(LOGO_FLAG);
  } catch { /* quota/denied — the server still holds the source of truth */ }
}

// applyLogo swaps every brand mark (the ".mark" boxes in the top bar and drawer)
// between the uploaded logo and the built-in "A". When a logo is present each mark
// renders an <img> pointing at LOGO_URL; a load failure (e.g. the logo was cleared
// on another device) reverts that mark to the letter, so the UI degrades cleanly.
// `refresh` forces a cache-busted reload of an already-shown logo — passed after
// an admin replaces the image, where the URL is unchanged but the bytes are not.
// Boot and the server sync leave it false, so a normal load stays cacheable.
export function applyLogo(present, refresh = false) {
  for (const mark of document.querySelectorAll(".mark")) {
    if (present) {
      const existing = mark.querySelector("img");
      if (existing) {
        // Already showing a logo. Only a deliberate refresh (a re-upload) cache-busts;
        // Cache-Control is no-cache, but reassigning an identical src may not reload.
        if (refresh) existing.src = LOGO_URL + "?t=" + Date.now();
      } else {
        const img = document.createElement("img");
        img.className = "mark-img";
        img.alt = "";
        img.setAttribute("aria-hidden", "true");
        // A load failure (e.g. the logo was cleared on another device) reverts this
        // mark to the letter, so a stale "present" cache never leaves an empty box.
        img.onerror = () => { cacheLogoPresent(false); applyLogo(false); };
        img.src = LOGO_URL; // first paint: plain URL so the browser may cache it
        mark.textContent = "";
        mark.appendChild(img);
        mark.classList.add("has-logo");
      }
    } else if (mark.classList.contains("has-logo")) {
      mark.classList.remove("has-logo");
      mark.textContent = "A";
    }
  }
}

// syncLogoFromServer reconciles the cached flag with the server on boot: a
// reachable endpoint tells us definitively whether a logo is set; an unreachable
// server leaves whatever the boot paint applied. Safe to call after the shell
// exists.
export async function syncLogoFromServer() {
  let present;
  try {
    const res = await fetch(LOGO_URL, { cache: "no-cache" });
    present = res.ok; // 200 → set, 404 → none
  } catch {
    return; // offline — keep the cached paint
  }
  cacheLogoPresent(present);
  applyLogo(present);
}

// setServerLogo uploads a chosen File as the org-wide logo (admin-only when auth is
// on). It validates type and size client-side first, then PUTs the raw bytes with
// the file's content type. On success it updates the cache and repaints; it throws
// an Error carrying the server's message on failure (e.g. a 403 for a non-admin).
export async function setServerLogo(file) {
  if (!file) throw new Error("No file chosen");
  if (!LOGO_TYPES.includes(file.type)) throw new Error("Logo must be a PNG or SVG image");
  if (file.size > LOGO_MAX_BYTES) throw new Error("Logo must be 512 KiB or smaller");
  const res = await fetch(LOGO_URL, {
    method: "PUT",
    headers: { "Content-Type": file.type },
    body: file,
  });
  if (!res.ok) throw new Error(await errorMessage(res));
  cacheLogoPresent(true);
  applyLogo(true, true); // force fresh bytes into any mark already showing a logo
}

// deleteServerLogo clears the org-wide logo, restoring the built-in mark.
export async function deleteServerLogo() {
  const res = await fetch(LOGO_URL, { method: "DELETE" });
  if (!res.ok) throw new Error(await errorMessage(res));
  cacheLogoPresent(false);
  applyLogo(false);
}

async function errorMessage(res) {
  try {
    const body = await res.json();
    if (body && body.error) return body.error;
  } catch { /* fall through */ }
  return res.status === 403
    ? "Changing the logo requires the admin role"
    : `Request failed (${res.status})`;
}
