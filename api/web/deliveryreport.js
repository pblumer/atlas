// The product manager's delivery dashboard
// (ADR-0515).
//
// One card on the catalogue overview: how the products the reader maintains were
// ordered, delivered against their promise and are held now, for a period they
// choose, with the same rows as CSV. It reads one route and names nobody — the
// route does not send names to this reader, so there are none to leave out.

import { t } from "./i18n.js";

const esc = (s) => String(s ?? "").replace(/[&<>"']/g, (c) =>
  ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;", "'": "&#39;" }[c]));

// days formats a number of working days with one decimal, or a dash for none.
export function days(v) {
  if (v === null || v === undefined) return "—";
  return (Math.round(v * 10) / 10).toFixed(1);
}

// percent formats a share as a whole percentage, or a dash for none.
export function percent(v) {
  if (v === null || v === undefined) return "—";
  return `${Math.round(v * 100)} %`;
}

// shareTone answers how a kept share reads: good at or above 95 %, a warning
// above 80 %, bad below. The thresholds are the usual service-review bands, not a
// promise anybody made; they only colour the number.
export function shareTone(v) {
  if (v === null || v === undefined) return "";
  if (v >= 0.95) return "ok";
  if (v >= 0.8) return "warn";
  return "bad";
}

export function reportQuery(from, to, extra = {}) {
  const q = new URLSearchParams();
  if (from) q.set("from", from);
  if (to) q.set("to", to);
  for (const [k, v] of Object.entries(extra)) if (v) q.set(k, v);
  const s = q.toString();
  return s ? `?${s}` : "";
}

// kpis renders the four tiles every delivery view leads with.
export function kpis(tot) {
  const tone = shareTone(tot.metShare);
  return `<div class="dr-kpis">
    <div class="dr-kpi"><div class="muted">${esc(t("delivery.kpi.ordered"))}</div>
      <div class="dr-v">${tot.ordered || 0}</div></div>
    <div class="dr-kpi"><div class="muted">${esc(t("delivery.kpi.held"))}</div>
      <div class="dr-v">${tot.held || 0}</div></div>
    <div class="dr-kpi"><div class="muted">${esc(t("delivery.kpi.metShare"))}</div>
      <div class="dr-v dr-${tone}">${percent(tot.metShare)}</div></div>
    <div class="dr-kpi"><div class="muted">${esc(t("delivery.kpi.overdue"))}</div>
      <div class="dr-v ${tot.overdue ? "dr-bad" : ""}">${tot.overdue || 0}</div></div>
  </div>`;
}

// productTable renders one row per product.
export function productTable(products) {
  if (!products.length) return `<p class="muted">${esc(t("delivery.none"))}</p>`;
  const rows = products.map((p) => `<tr>
    <td>${esc(p.name)}<div class="muted"><code>${esc(p.itemId)}</code></div></td>
    <td>${p.serviceLevel ? `${esc(p.serviceLevel)} <span class="muted">· ${p.deliveryDays}&nbsp;${esc(t("delivery.days"))}</span>` : `<span class="muted">${esc(t("delivery.noPromise"))}</span>`}</td>
    <td class="num">${p.ordered}</td>
    <td class="num">${p.met}</td>
    <td class="num">${p.missed}</td>
    <td class="num ${p.overdue ? "dr-bad" : ""}">${p.overdue}</td>
    <td class="num">${p.open + p.awaitingApproval}</td>
    <td class="num dr-${shareTone(p.metShare)}">${percent(p.metShare)}</td>
    <td class="num">${days(p.leadMedianDays)}</td>
    <td class="num">${days(p.leadP90Days)}</td>
    <td class="num">${days(p.approvalWaitMedianDays)}</td>
    <td class="num">${p.held}</td>
  </tr>`).join("");
  return `<div class="dr-scroll"><table class="table dr-table">
    <thead><tr>
      <th>${esc(t("delivery.col.product"))}</th><th>${esc(t("delivery.col.level"))}</th>
      <th class="num">${esc(t("delivery.col.ordered"))}</th><th class="num">${esc(t("delivery.col.met"))}</th>
      <th class="num">${esc(t("delivery.col.missed"))}</th><th class="num">${esc(t("delivery.col.overdue"))}</th>
      <th class="num">${esc(t("delivery.col.open"))}</th><th class="num">${esc(t("delivery.col.share"))}</th>
      <th class="num">${esc(t("delivery.col.median"))}</th><th class="num">${esc(t("delivery.col.p90"))}</th>
      <th class="num">${esc(t("delivery.col.approval"))}</th><th class="num">${esc(t("delivery.col.held"))}</th>
    </tr></thead><tbody>${rows}</tbody></table></div>`;
}

// deliveryCard is the empty card the overview draws; wireDelivery fills it.
export function deliveryCard() {
  return `<div class="card dr-card" data-delivery style="margin-top:18px">
    <h3 style="margin:0 0 6px">${esc(t("delivery.title"))}</h3>
    <p class="muted" style="max-width:70ch; margin:0 0 10px">${t("delivery.intro")}</p>
    <form class="dr-period">
      <label class="field">${esc(t("delivery.from"))}<input type="date" name="from"></label>
      <label class="field">${esc(t("delivery.to"))}<input type="date" name="to"></label>
      <button class="btn ghost" type="submit">${esc(t("delivery.show"))}</button>
      <a class="btn ghost" data-delivery-csv download>${esc(t("delivery.csv"))}</a>
    </form>
    <div data-delivery-body><p class="muted">${esc(t("delivery.loading"))}</p></div>
  </div>`;
}

// wireDelivery loads the report into the card and reloads it for another period.
export function wireDelivery({ api, view }) {
  const card = view.querySelector("[data-delivery]");
  if (!card) return;
  const form = card.querySelector(".dr-period");
  const body = card.querySelector("[data-delivery-body]");
  const csv = card.querySelector("[data-delivery-csv]");
  const load = async () => {
    const f = new FormData(form);
    const q = reportQuery(f.get("from"), f.get("to"));
    csv.href = `/api/v1/catalog-products/delivery-report${q}${q ? "&" : "?"}format=csv`;
    body.innerHTML = `<p class="muted">${esc(t("delivery.loading"))}</p>`;
    try {
      const rep = await api("GET", `/api/v1/catalog-products/delivery-report${q}`);
      if (!f.get("from")) form.elements.from.value = rep.from;
      if (!f.get("to")) form.elements.to.value = rep.to;
      body.innerHTML = `<p class="muted" style="margin:0 0 8px">${esc(t("delivery.period", { from: rep.from, to: rep.to, n: (rep.catalogs || []).length }))}</p>
        ${kpis(rep.totals || {})}${productTable(rep.products || [])}`;
    } catch (e) {
      body.innerHTML = `<p class="muted">${esc(t("delivery.failed"))} ${esc(e.message)}</p>`;
    }
  };
  form.addEventListener("submit", (e) => { e.preventDefault(); load(); });
  load();
}
