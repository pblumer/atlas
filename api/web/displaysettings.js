// The Console screen where a person says how they read (ADR-0459).
//
// Three settings, three different questions — how a number is written, which
// instant a timestamp means, which words the translated screens use — and one
// sample block that answers all three at once. The sample is the point of the
// screen: a region named "Switzerland" tells nobody what a badge will print, and
// `25'864.50` tells them exactly.
//
// The preview renders the *pending* choice without applying it. Both formatters
// take an explicit region and zone for that reason: a preview that reformatted
// the console around it would be a setting applied, not a setting shown, and
// there would be no way to look at an option and then decline it.

import { REGIONS, fmtCount } from "./numfmt.js";
import { fmtDateTime, zoneName } from "./datefmt.js";
import { prefs, loadPrefs, savePrefs } from "./prefs.js";
import { localeName, locales, t } from "./i18n.js";

const esc = (s) => String(s == null ? "" : s).replace(/[&<>"']/g, (c) =>
  ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;", "'": "&#39;" }[c]));

// The region's name is the country somebody recognizes; the sample beside it is
// what they are actually choosing. The names come from the catalogue — this is the
// screen where a person picks their language, and a language picker on a screen
// that does not itself follow the choice disproves itself on the first click
// (ADR-0267 adopts the catalogue per screen; this is one of them).
const regionLabel = (id) => t(`display.region.${id}`);

// SAMPLE is the instant the previews are drawn from: a fixed one rather than
// "now", so two options can be compared without the seconds moving underneath,
// and one whose day is past the twelfth so a d/m swap is visible (an American
// reader seeing 10/15 knows at a glance it is not the fifteenth month).
const SAMPLE = Date.UTC(2026, 9, 15, 15, 18, 29);

// ZONE_FALLBACK is for an engine without Intl.supportedValuesOf: enough zones to
// cover the people who run Atlas, rather than a promise of all of them.
const ZONE_FALLBACK = [
  "UTC", "Europe/Zurich", "Europe/Berlin", "Europe/Vienna", "Europe/London",
  "Europe/Paris", "Europe/Madrid", "Europe/Rome", "Europe/Warsaw", "Europe/Istanbul",
  "America/New_York", "America/Chicago", "America/Denver", "America/Los_Angeles",
  "America/Sao_Paulo", "Asia/Dubai", "Asia/Kolkata", "Asia/Singapore", "Asia/Shanghai",
  "Asia/Tokyo", "Australia/Sydney",
];

function zoneOptions(selected) {
  let list = ZONE_FALLBACK;
  try {
    if (typeof Intl.supportedValuesOf === "function") list = Intl.supportedValuesOf("timeZone");
  } catch { /* an engine that does not offer the list keeps the short one */ }
  // UTC first, and explicitly: the browser's own list does not contain it (it holds
  // canonical zones, and UTC is an alias), and UTC is the one zone an operator
  // actually needs — it is what the server's logs and the exported event stream are
  // written in, so "read this screen the way the log reads" has to be one click.
  list = ["UTC", ...list.filter((z) => z !== "UTC")];
  // Whatever is stored stays selectable even if this browser's database has never
  // heard of it: dropping it would silently rewrite somebody's choice on a save.
  if (selected && !list.includes(selected)) list = [selected, ...list];
  return list;
}

// sampleHTML is the three answers, drawn with the pending choice.
function sampleHTML({ region, timeZone }) {
  const count = fmtCount(50002, { region });
  const amount = fmtCount(25864.5, { region });
  const when = fmtDateTime(SAMPLE, { region, timeZone });
  const zone = timeZone || zoneName();
  return `
    <div class="row" style="gap:18px; flex-wrap:wrap">
      <div><div class="ds-label">${esc(t("display.sample.count"))}</div><b id="ds-sample-count">${esc(count)}</b></div>
      <div><div class="ds-label">${esc(t("display.sample.amount"))}</div><b id="ds-sample-amount">${esc(amount)}</b></div>
      <div><div class="ds-label">${esc(t("display.sample.when"))}</div><b id="ds-sample-when">${esc(when)}</b></div>
      <div><div class="ds-label">${esc(t("display.sample.zone"))}</div><b id="ds-sample-zone">${esc(zone)}</b></div>
    </div>`;
}

// scopeNote says where the setting is kept, because the two cases behave
// differently and a person is entitled to know which one they are in: an account
// setting follows them to the next browser, a browser one does not.
function scopeNote(scope) {
  if (scope === "account") return t("display.scope.account");
  if (scope === "browser") return t("display.scope.browser");
  return t("display.scope.offline");
}

// renderDisplaySettings draws the screen into root and wires it up.
export async function renderDisplaySettings(root, { api, toast }) {
  // The stored record is what the screen opens on; the reload confirms it against
  // the server, which matters on a browser that has never seen this account.
  const loaded = await loadPrefs(api);
  let scope = loaded.scope;
  const pending = { region: prefs().region || "international", timeZone: prefs().timeZone, language: prefs().language };
  const languageAtOpen = pending.language;

  const regionRows = Object.keys(REGIONS).map((id) => `
    <label class="ds-option">
      <input type="radio" name="ds-region" value="${esc(id)}"${id === pending.region ? " checked" : ""}>
      <span class="ds-option-name">${esc(regionLabel(id))}</span>
      <span class="ds-option-sample">${esc(fmtCount(25864.5, { region: id }))} · ${esc(fmtDateTime(SAMPLE, { region: id, timeZone: pending.timeZone }))}</span>
    </label>`).join("");

  const languageRows = ["", ...locales()].map((id) => `
      <option value="${esc(id)}"${id === pending.language ? " selected" : ""}>${
        esc(id ? localeName(id) : t("display.language.default"))}</option>`).join("");

  const zoneRows = ["", ...zoneOptions(pending.timeZone)].map((id) => `
      <option value="${esc(id)}"${id === pending.timeZone ? " selected" : ""}>${
        esc(id || t("display.zone.device", { zone: zoneName() }))}</option>`).join("");

  root.innerHTML = `
    <div class="card">
      <div class="between"><h1>${esc(t("display.title"))}</h1></div>
      <p class="muted">${t("display.intro")}</p>
      <p class="muted" id="ds-scope">${esc(scopeNote(scope))}</p>

      <div class="ds-preview">
        <h2>${esc(t("display.preview"))}</h2>
        <div id="ds-sample">${sampleHTML(pending)}</div>
      </div>

      <h2 style="margin-top:18px">${esc(t("display.numbers.title"))}</h2>
      <p class="muted">${t("display.numbers.hint")}</p>
      <div class="ds-options">${regionRows}</div>

      <h2 style="margin-top:18px">${esc(t("display.zone.title"))}</h2>
      <p class="muted">${t("display.zone.hint")}</p>
      <label class="field"><span>${esc(t("display.zone.label"))}</span>
        <select id="ds-zone">${zoneRows}</select></label>

      <h2 style="margin-top:18px">${esc(t("display.language.title"))}</h2>
      <p class="muted">${t("display.language.hint")}</p>
      <label class="field"><span>${esc(t("display.language.label"))}</span>
        <select id="ds-language">${languageRows}</select></label>

      <div class="row" style="margin-top:18px; gap:10px; align-items:center">
        <button class="btn" id="ds-save">${esc(t("display.save"))}</button>
        <button class="btn neutral" id="ds-reset" title="${esc(t("display.resetTitle"))}">${esc(t("display.reset"))}</button>
        <span class="muted" id="ds-status"></span>
      </div>
    </div>`;

  const sample = root.querySelector("#ds-sample");
  const status = root.querySelector("#ds-status");
  const repaint = () => {
    sample.innerHTML = sampleHTML(pending);
    // The per-region samples carry the pending zone too, so changing the zone moves
    // every row rather than only the preview at the top.
    for (const label of root.querySelectorAll(".ds-option")) {
      const id = label.querySelector("input").value;
      label.querySelector(".ds-option-sample").textContent =
        `${fmtCount(25864.5, { region: id })} · ${fmtDateTime(SAMPLE, { region: id, timeZone: pending.timeZone })}`;
    }
    status.textContent = "";
  };

  for (const input of root.querySelectorAll('input[name="ds-region"]')) {
    input.addEventListener("change", () => { pending.region = input.value; repaint(); });
  }
  root.querySelector("#ds-zone").addEventListener("change", (e) => {
    pending.timeZone = e.target.value; repaint();
  });
  root.querySelector("#ds-language").addEventListener("change", (e) => {
    pending.language = e.target.value; status.textContent = "";
  });

  root.querySelector("#ds-reset").addEventListener("click", () => {
    pending.region = "international";
    pending.timeZone = "";
    pending.language = "";
    root.querySelector("#ds-zone").value = "";
    root.querySelector("#ds-language").value = "";
    const first = root.querySelector('input[name="ds-region"][value="international"]');
    if (first) first.checked = true;
    repaint();
  });

  root.querySelector("#ds-save").addEventListener("click", async () => {
    const btn = root.querySelector("#ds-save");
    btn.disabled = true;
    try {
      const saved = await savePrefs(api, pending);
      scope = saved.scope;
      root.querySelector("#ds-scope").textContent = scopeNote(scope);
      // A language change has to reach screens this one cannot re-render, and they
      // read the catalogue at render time. Reloading is the honest way to apply it
      // everywhere at once, and it is announced rather than sprung.
      if (pending.language !== languageAtOpen) {
        status.textContent = t("display.savedReloading");
        setTimeout(() => location.reload(), 600);
        return;
      }
      status.textContent = t("display.saved");
      if (toast) toast(t("display.savedToast"));
    } catch (err) {
      status.textContent = t("display.notSaved", { reason: err.message });
      if (toast) toast(t("display.notSaved", { reason: err.message }), "err");
    } finally {
      btn.disabled = false;
    }
  });
}
