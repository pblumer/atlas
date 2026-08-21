// Writing text to the clipboard, in one place.
//
// The async Clipboard API is unavailable on an insecure origin, which is where a
// self-hosted Atlas most often lives — a plain http:// address on an internal
// network. So every copy in the console goes through here and falls back to the
// legacy textarea + execCommand path rather than silently doing nothing on exactly
// the deployments that need it most.

// copyText writes text to the clipboard and reports whether it worked. It never
// throws: a caller shows a result, and a failed copy is a message rather than a
// broken button.
export async function copyText(text) {
  try {
    if (navigator.clipboard && navigator.clipboard.writeText) {
      await navigator.clipboard.writeText(text);
      return true;
    }
  } catch { /* fall through to the legacy path */ }
  try {
    const ta = document.createElement("textarea");
    ta.value = text;
    ta.style.position = "fixed";
    ta.style.opacity = "0";
    document.body.appendChild(ta);
    ta.select();
    const ok = document.execCommand("copy");
    document.body.removeChild(ta);
    return ok;
  } catch { return false; }
}
