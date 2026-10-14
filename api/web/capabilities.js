// The business architecture in the Console (ADR-0305, roadmap B8).
//
// The capability register, its value streams and the gap report have had HTTP routes
// and MCP tools since they landed, and no screen: the one built area of Atlas a person
// could only read through curl or an agent. These views are that screen, and they are
// built around the reading the register exists for. The landing view is the gap report
// — what the map says the organisation must be able to do, against what this server
// actually runs — because a capability list nobody compares with anything is a
// document that ages, and the comparison is what keeps it honest.
//
// Three rules shape them.
//
//   - The map is read here, not edited. Creating and changing a record stays with the
//     API and MCP for now; the one write is the confirmation (B10), and it lives on the
//     detail page, after the record, because confirming a row in a list of findings
//     would assert a reading nobody did.
//   - The gap report has no severity, and neither has this page. Findings are grouped
//     by kind in the order the server publishes, and a kind with nothing in it says so
//     in one line rather than disappearing.
//   - Every word is the catalogue's (ADR-0267), German first. The server sends keys,
//     kinds, states and coordinates; the sentences a reader meets are composed here,
//     never taken from the report's English detail.

import { enhanceTable } from "./table.js";
import { t, plural, locale } from "./i18n.js";

const esc = (s) => String(s ?? "").replace(/[&<>"']/g, (c) =>
  ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", "'": "&#39;", '"': "&quot;" }[c]));
const enc = encodeURIComponent;

// tx is t() for a message whose placeholders carry markup — a link, a code span. The
// catalogue text is escaped first and the already-escaped fragments go in after, so a
// translation can never inject markup and a fragment is never escaped twice.
const tx = (key, html) => esc(t(key)).replace(/\{(\w+)\}/g, (m, name) => (name in html ? html[name] : m));

export const capabilityHref = (key) => `#/panorama/capabilities/${enc(key)}`;
export const valueStreamHref = (key) => `#/panorama/value-streams/${enc(key)}`;

// Dates follow the catalogue's language rather than the browser's, so a German screen
// does not carry an American date because of a setting nobody made (ADR-0267).
const fmtDate = (unix) => (unix ? new Date(unix * 1000).toLocaleDateString(locale()) : "");
const code = (s) => `<code>${esc(s)}</code>`;
const stateLabel = (s) => t(`ba.state.${s}`);
const tagChips = (tags) => (tags || []).map((g) => `<span class="chip ba-tag">${esc(g)}</span>`).join(" ");
const ownerText = (o) => [o && o.name, o && o.role].filter(Boolean).join(", ");

// The vocabularies come from the server's authoring subset, so the order of the
// finding kinds and of the states is the server's and not a second copy of it.
async function readSubset(api) {
  return api("GET", "/api/v1/business-architecture/subset");
}

function failed(body, e) {
  body.innerHTML = `<div class="card empty" style="margin-top:16px"><p>${esc(t("ba.error", { error: e.message }))}</p></div>`;
}

// ---------- the landing view: gaps, and the list one tab away ----------

export async function mountCapabilityMap(root, { api }) {
  root.innerHTML = `<div class="ba-root">
    <div class="between">
      <div>
        <h1>${esc(t("ba.title"))}</h1>
        <p class="muted" style="margin:0">${esc(t("ba.intro"))}</p>
      </div>
      <a class="btn neutral" href="#/panorama/value-streams">${esc(t("ba.streams.title"))} →</a>
    </div>
    <div class="ba-tabs" role="tablist">
      <button type="button" role="tab" data-tab="gaps" aria-selected="true">${esc(t("ba.tab.gaps"))} <span class="ba-count" data-count="gaps"></span></button>
      <button type="button" role="tab" data-tab="list" aria-selected="false">${esc(t("ba.tab.capabilities"))} <span class="ba-count" data-count="list"></span></button>
    </div>
    <div id="ba-body"><p class="muted" style="margin-top:16px">${esc(t("ba.loading"))}</p></div>
  </div>`;
  const body = root.querySelector("#ba-body");

  let subset, report, caps, streams;
  try {
    [subset, report, caps, streams] = await Promise.all([
      readSubset(api),
      api("GET", "/api/v1/business-architecture/gaps"),
      api("GET", "/api/v1/capabilities"),
      api("GET", "/api/v1/value-streams"),
    ]);
  } catch (e) {
    failed(body, e);
    return;
  }
  if (!root.isConnected) return; // navigated away while it was read
  caps = caps || [];
  streams = streams || [];

  const findings = report.findings || [];
  root.querySelector('[data-count="gaps"]').textContent = String(findings.length);
  root.querySelector('[data-count="list"]').textContent = String(caps.length);

  const gaps = document.createElement("div");
  gaps.className = "ba-panel";
  gaps.dataset.panel = "gaps";
  const list = document.createElement("div");
  list.className = "ba-panel";
  list.dataset.panel = "list";
  list.hidden = true;
  body.replaceChildren(gaps, list);

  if (!caps.length && !streams.length) {
    gaps.innerHTML = `<div class="card empty" style="margin-top:16px">
      <h2>${esc(t("ba.empty.title"))}</h2><p class="muted">${esc(t("ba.empty.text"))}</p></div>`;
  } else {
    renderGaps(gaps, { subset, report, caps, streams });
  }
  renderList(list, { subset, caps });

  for (const tab of root.querySelectorAll(".ba-tabs [data-tab]")) {
    tab.addEventListener("click", () => {
      for (const other of root.querySelectorAll(".ba-tabs [data-tab]")) {
        other.setAttribute("aria-selected", String(other === tab));
      }
      gaps.hidden = tab.dataset.tab !== "gaps";
      list.hidden = tab.dataset.tab !== "list";
    });
  }
}

function renderGaps(el, { subset, report, caps, streams }) {
  const capByKey = new Map(caps.map((c) => [c.key, c]));
  const streamByKey = new Map(streams.map((v) => [v.key, v]));
  const capLink = (key) => {
    const c = capByKey.get(key);
    return `<a href="${capabilityHref(key)}"><b>${esc(c ? c.name : key)}</b></a>`;
  };
  const streamLink = (key) => {
    const v = streamByKey.get(key);
    return `<a href="${valueStreamHref(key)}"><b>${esc(v ? v.name : key)}</b></a>`;
  };
  const unconfirmed = (at) => (at
    ? esc(t("ba.finding.unconfirmed.lapsed", { date: fmtDate(at) }))
    : esc(t("ba.finding.unconfirmed.never")));

  // row says one finding in the reader's language from its coordinates.
  const row = (f) => {
    switch (f.kind) {
      case "capability.unrealized":
        return capLink(f.capabilityKey);
      case "realization.missing":
        return `${capLink(f.capabilityKey)}: ` + (f.workerRef
          ? tx("ba.finding.realization.missing.worker", { worker: code(f.workerRef) })
          : tx("ba.finding.realization.missing.process", { process: code(f.processId), application: code(f.applicationKey) }));
      case "process.unclaimed":
        return tx("ba.finding.process.unclaimed.row", { process: code(f.processId), application: code(f.applicationKey) });
      case "requires.unknown":
        return `${capLink(f.capabilityKey)} ` + tx("ba.finding.requires.unknown.row", { required: code(f.requiredKey) });
      case "stage.empty":
        return `${streamLink(f.valueStreamKey)} › ` + tx("ba.finding.stage.empty.row", { stage: code(f.stageKey) });
      case "stage.unknown":
        return `${streamLink(f.valueStreamKey)} › ` +
          tx("ba.finding.stage.unknown.row", { stage: code(f.stageKey), required: code(f.requiredKey) });
      case "process.shared":
        return `${capLink(f.capabilityKey)} ` + tx("ba.finding.process.shared.row", { process: code(f.processId) });
      case "call.undeclared":
        return `${capLink(f.capabilityKey)} ` + tx("ba.finding.call.undeclared.row",
          { process: code(f.processId), element: code(f.elementId), required: capLink(f.requiredKey) });
      case "capability.unconfirmed": {
        const c = capByKey.get(f.capabilityKey);
        return `${capLink(f.capabilityKey)}: ${unconfirmed(c && c.confirmedAt)}`;
      }
      case "value-stream.unconfirmed": {
        const v = streamByKey.get(f.valueStreamKey);
        return `${streamLink(f.valueStreamKey)}: ${unconfirmed(v && v.confirmedAt)}`;
      }
      default:
        // A kind this Console does not know yet: say which, rather than nothing. The
        // drift test on the catalogue is what keeps this branch unreachable.
        return code(f.kind);
    }
  };

  const checked = report.checked || {};
  const head = [
    esc(t("ba.gaps.checked", {
      capabilities: checked.capabilities || 0, streams: checked.valueStreams || 0,
      processes: checked.processes || 0, calls: checked.calls || 0,
    })),
    report.restricted ? esc(plural("ba.gaps.restricted", report.restricted)) : "",
    report.horizonMonths ? esc(plural("ba.gaps.horizon", report.horizonMonths)) : "",
  ].filter(Boolean).join(" ");

  // The server's order, then anything it reported that the subset did not list.
  const kinds = [...(subset.gapFindingKinds || [])];
  for (const f of report.findings || []) if (!kinds.includes(f.kind)) kinds.push(f.kind);

  const sections = [];
  const clean = [];
  for (const kind of kinds) {
    const of = (report.findings || []).filter((f) => f.kind === kind);
    if (!of.length) {
      clean.push(t(`ba.finding.${kind}.title`));
      continue;
    }
    sections.push(`<section class="card ba-kind" data-kind="${esc(kind)}">
      <h2>${esc(t(`ba.finding.${kind}.title`))} <span class="chip">${of.length}</span></h2>
      <p class="muted">${esc(t(`ba.finding.${kind}.hint`))}</p>
      <ul class="ba-findings">${of.map((f) => `<li>${row(f)}</li>`).join("")}</ul>
    </section>`);
  }

  el.innerHTML = `<p class="muted ba-checked">${head}</p>
    ${sections.length
      ? `<p class="ba-note">${esc(t("ba.gaps.noSeverity"))}</p>${sections.join("")}`
      : `<div class="card empty ba-none"><p>${esc(t("ba.gaps.none"))}</p></div>`}
    ${sections.length && clean.length
      ? `<p class="muted ba-clean">${esc(t("ba.gaps.clean", { kinds: clean.join(", ") }))}</p>`
      : ""}`;
}

function renderList(el, { subset, caps }) {
  const order = new Map((subset.states || []).map((s, i) => [s, i]));
  const tags = [...new Set(caps.flatMap((c) => c.tags || []))].sort();
  el.innerHTML = `<div class="ba-filters">
      <select data-filter="tag" aria-label="${esc(t("ba.list.tags"))}">
        <option value="">${esc(t("ba.list.allTags"))}</option>
        ${tags.map((g) => `<option value="${esc(g)}">${esc(g)}</option>`).join("")}
      </select>
      <label><input type="checkbox" data-filter="unrealized"> ${esc(t("ba.list.onlyUnrealized"))}</label>
      <label><input type="checkbox" data-filter="stale"> ${esc(t("ba.list.onlyStale"))}</label>
      <span class="muted" data-role="count"></span>
    </div>
    <div data-role="table"></div>
    <p class="muted ba-scope">${esc(t("ba.list.noHierarchy"))}</p>`;

  const tagSel = el.querySelector('[data-filter="tag"]');
  const onlyUnrealized = el.querySelector('[data-filter="unrealized"]');
  const onlyStale = el.querySelector('[data-filter="stale"]');
  const holder = el.querySelector('[data-role="table"]');
  const count = el.querySelector('[data-role="count"]');

  const row = (c) => `<tr data-key="${esc(c.key)}">
    <td><div class="artifact-name"><a href="${capabilityHref(c.key)}"><b>${esc(c.name)}</b></a></div>
      <div class="muted">${code(c.key)}</div>
      ${c.summary ? `<div class="ba-summary">${esc(c.summary)}</div>` : ""}</td>
    <td data-sort="${order.has(c.state) ? order.get(c.state) : 99}"><span class="chip ba-state ${esc(c.state)}">${esc(stateLabel(c.state))}</span></td>
    <td class="muted">${esc(ownerText(c.owner))}</td>
    <td data-filter="${esc((c.tags || []).join(" "))}">${tagChips(c.tags)}</td>
    <td data-sort="${c.realizationCount || 0}">${c.realized
      ? String(c.realizationCount)
      : `<span class="pill warn">${esc(t("ba.list.none"))}</span>`}</td>
    <td class="muted" data-sort="${c.requiresCount || 0}">${c.requiresCount || 0}</td>
    <td data-sort="${c.confirmedAt || 0}">${c.confirmedAt ? esc(fmtDate(c.confirmedAt)) : `<span class="muted">${esc(t("ba.list.never"))}</span>`}
      ${c.stale ? `<span class="pill warn">${esc(t("ba.list.due"))}</span>` : ""}</td>
    <td class="muted" data-sort="${c.updatedAt || 0}">${esc(fmtDate(c.updatedAt))}</td>
  </tr>`;

  const draw = () => {
    const shown = caps.filter((c) =>
      (!tagSel.value || (c.tags || []).includes(tagSel.value)) &&
      (!onlyUnrealized.checked || !c.realized) &&
      (!onlyStale.checked || c.stale));
    count.textContent = plural("ba.list.count", shown.length);
    if (!shown.length) {
      holder.innerHTML = `<div class="card empty"><p class="muted">${esc(t(caps.length ? "ba.list.noMatch" : "ba.empty.text"))}</p></div>`;
      return;
    }
    holder.innerHTML = `<div class="card" style="padding:0">
      <table data-dt-key="ba-capabilities">
        <thead><tr>
          <th>${esc(t("ba.list.name"))}</th><th>${esc(t("ba.list.state"))}</th><th>${esc(t("ba.list.owner"))}</th>
          <th>${esc(t("ba.list.tags"))}</th><th>${esc(t("ba.list.realizations"))}</th><th>${esc(t("ba.list.requires"))}</th>
          <th>${esc(t("ba.list.confirmed"))}</th><th>${esc(t("ba.list.updated"))}</th>
        </tr></thead>
        <tbody>${shown.map(row).join("")}</tbody>
      </table></div>`;
    enhanceTable(holder.querySelector("table"), {
      key: "ba-capabilities",
      columns: [{}, { type: "number" }, {}, {}, { type: "number" }, { type: "number" }, { type: "number" }, { type: "number" }],
    });
  };
  for (const input of [tagSel, onlyUnrealized, onlyStale]) input.addEventListener("change", draw);
  draw();
}

// ---------- one capability ----------

export async function mountCapabilityDetail(root, { api, toast, key }) {
  root.innerHTML = `<div class="ba-root">
    <a class="muted" href="#/panorama/capabilities">${esc(t("ba.detail.back"))}</a>
    <div id="ba-body"><p class="muted" style="margin-top:16px">${esc(t("ba.loading"))}</p></div>
  </div>`;
  const body = root.querySelector("#ba-body");

  let cov;
  try {
    cov = await api("GET", `/api/v1/capabilities/${enc(key)}/coverage`);
  } catch (e) {
    if (e.status === 404) {
      body.innerHTML = `<div class="card empty" style="margin-top:16px"><p>${esc(t("ba.notFound"))}</p></div>`;
      return;
    }
    failed(body, e);
    return;
  }
  if (!root.isConnected) return;
  const c = cov.capability || {};

  const realization = (r) => {
    const kind = `<span class="chip">${esc(t(`ba.realization.${r.kind}`))}</span>`;
    if (r.kind === "system" || r.kind === "manual") {
      return `<li>${kind} ${esc(r.note || "")} <span class="muted">— ${esc(t("ba.detail.unchecked"))}</span></li>`;
    }
    const target = r.kind === "worker"
      ? code(r.workerRef) + (r.workerType ? ` <span class="muted">${esc(r.workerType)}</span>` : "")
      : `${r.name ? `<b>${esc(r.name)}</b> ` : ""}${code(r.processId)} <span class="muted">${esc(t("ba.detail.inApplication", { application: r.applicationName || r.applicationKey }))}</span>`;
    let facts = "";
    if (r.restricted) facts = `<span class="pill">${esc(t("ba.detail.restricted"))}</span>`;
    else if (!r.resolved) facts = `<span class="pill warn">${esc(t("ba.detail.notFound"))}</span>`;
    else if (r.kind === "process") {
      facts = [
        r.version ? esc(t("ba.detail.version", { version: r.version })) : "",
        esc(plural("ba.detail.instances", r.activeInstances || 0)),
        r.inactive ? `<span class="pill warn">${esc(t("ba.detail.inactive"))}</span>` : "",
      ].filter(Boolean).join(" · ");
    }
    return `<li>${kind} ${target} ${facts ? `<span class="muted">${facts}</span>` : ""}${r.note ? `<div class="muted">${esc(r.note)}</div>` : ""}</li>`;
  };

  // A requirement the map does not have is only a key: there is no record to link to and
  // no name to show beside it.
  const dependency = (d) => (!d.known
    ? `<li>${code(d.key)} <span class="pill warn">${esc(t("ba.detail.notInMap"))}</span></li>`
    : `<li><a href="${capabilityHref(d.key)}"><b>${esc(d.name || d.key)}</b></a> ${code(d.key)}
    ${d.known && d.state ? `<span class="chip ba-state ${esc(d.state)}">${esc(stateLabel(d.state))}</span>` : ""}
    ${d.known && !d.realized ? `<span class="pill warn">${esc(t("ba.detail.notRealized"))}</span>` : ""}</li>`);
  const dependent = (d) => `<li><a href="${capabilityHref(d.key)}"><b>${esc(d.name || d.key)}</b></a> ${code(d.key)}</li>`;
  const stream = (s) => `<li><a href="${valueStreamHref(s.key)}"><b>${esc(s.name || s.key)}</b></a> ›
    ${(s.stages || []).map((st) => esc(st.name || st.key)).join(", ")}</li>`;
  const iface = (i) => `<li><b>${esc(i.name)}</b> <span class="muted">${esc(i.kind)}</span>${i.description ? ` — ${esc(i.description)}` : ""}</li>`;
  const resource = (r) => `<li><b>${esc(r.name)}</b> <span class="muted">${esc(r.kind)}</span>${r.ref ? ` ${code(r.ref)}` : ""}</li>`;
  const kpi = (k) => `<li><b>${esc(k.name)}</b> ${code(k.metric)}${k.goal ? ` · ${esc(t("ba.detail.goal", { goal: k.goal }))}` : ""}</li>`;
  const sla = (s) => `<li><b>${esc(s.name)}</b> ${code(s.metric)} · ${esc(t("ba.detail.threshold", { threshold: s.threshold }))}${s.window ? ` · ${esc(s.window)}` : ""}</li>`;
  const listOr = (items, fn, none) => (items && items.length
    ? `<ul class="ba-list">${items.map(fn).join("")}</ul>`
    : `<p class="muted">${esc(t(none))}</p>`);
  const section = (title, inner) => `<section class="card ba-section"><h2>${esc(t(title))}</h2>${inner}</section>`;
  const optional = (title, items, fn) => (items && items.length ? section(title, `<ul class="ba-list">${items.map(fn).join("")}</ul>`) : "");

  body.innerHTML = `
    <div class="ba-head">
      <h1>${esc(c.name || key)} <span class="chip ba-state ${esc(c.state)}">${esc(stateLabel(c.state))}</span></h1>
      <div class="muted">${code(c.key || key)} ${tagChips(c.tags)}</div>
      ${c.summary ? `<p>${esc(c.summary)}</p>` : ""}
      ${c.scope ? `<p><b>${esc(t("ba.detail.scope"))}:</b> ${esc(c.scope)}</p>` : ""}
      <p><b>${esc(t("ba.detail.owner"))}:</b> ${ownerText(c.owner) ? esc(ownerText(c.owner)) : `<span class="muted">${esc(t("ba.detail.ownerNone"))}</span>`}</p>
    </div>
    ${section("ba.detail.realizations", listOr(cov.realizations, realization, "ba.detail.realizationsNone"))}
    <section class="card ba-section" data-role="measure">
      <div class="between"><h2>${esc(t("ba.measure.title"))}</h2>
        <label class="muted">${esc(t("ba.measure.window"))}
          <select data-role="window">${[7, 30, 90].map((n) =>
            `<option value="${n}"${n === 30 ? " selected" : ""}>${esc(t("ba.measure.days", { n }))}</option>`).join("")}</select>
        </label></div>
      <div data-role="figures"><p class="muted">${esc(t("ba.loading"))}</p></div>
    </section>
    ${section("ba.detail.requires", listOr(cov.requires, dependency, "ba.detail.requiresNone"))}
    ${section("ba.detail.requiredBy", listOr(cov.requiredBy, dependent, "ba.detail.requiredByNone"))}
    ${section("ba.detail.streams", listOr(cov.valueStreams, stream, "ba.detail.streamsNone"))}
    ${optional("ba.detail.inputs", c.inputs, iface)}
    ${optional("ba.detail.outputs", c.outputs, iface)}
    ${optional("ba.detail.resources", c.resources, resource)}
    ${optional("ba.detail.kpis", c.kpis, kpi)}
    ${optional("ba.detail.slas", c.slas, sla)}
    <section class="card ba-section" data-role="confirm"></section>
    <p class="muted ba-scope">${esc(t("ba.detail.changed", {
      created: fmtDate(c.createdAt), createdBy: c.createdBy || "—",
      updated: fmtDate(c.updatedAt), updatedBy: c.updatedBy || "—", revision: c.revision || 0,
    }))}</p>`;

  mountMeasurement(body.querySelector('[data-role="measure"]'), { api, key });
  mountConfirmation(body.querySelector('[data-role="confirm"]'), {
    api, toast, view: cov.confirmation || {},
    path: `/api/v1/capabilities/${enc(key)}/confirmation`,
  });
}

function mountMeasurement(el, { api, key }) {
  const figures = el.querySelector('[data-role="figures"]');
  const select = el.querySelector('[data-role="window"]');
  const secs = (n) => {
    if (!n && n !== 0) return "—";
    const s = Math.round(n);
    if (s < 120) return `${s} s`;
    if (s < 7200) return `${Math.round(s / 60)} min`;
    if (s < 172800) return `${(s / 3600).toFixed(1)} h`;
    return `${(s / 86400).toFixed(1)} d`;
  };
  // A reader who changes the window twice gets the second answer: each read takes a
  // ticket, and an answer for an older ticket is dropped rather than drawn over a newer one.
  let ticket = 0;
  const load = async () => {
    const mine = ++ticket;
    figures.innerHTML = `<p class="muted">${esc(t("ba.loading"))}</p>`;
    let m;
    try {
      m = await api("GET", `/api/v1/capabilities/${enc(key)}/measurement?windowDays=${enc(select.value)}`);
    } catch (e) {
      if (mine !== ticket) return;
      figures.innerHTML = `<p class="muted">${esc(e.status === 501 ? t("ba.measure.unavailable") : t("ba.error", { error: e.message }))}</p>`;
      return;
    }
    if (!el.isConnected || mine !== ticket) return;
    if (m.unrealizable) {
      figures.innerHTML = `<p class="muted">${esc(t("ba.measure.unrealizable"))}</p>`;
      return;
    }
    const proc = (p) => {
      if (p.restricted) return `<li>${code(p.processId)} <span class="pill">${esc(t("ba.detail.restricted"))}</span></li>`;
      if (!p.deployed) return `<li>${code(p.processId)} <span class="pill warn">${esc(t("ba.measure.notDeployed"))}</span></li>`;
      const cs = p.cases || {};
      const outcomes = (p.outcomes || []).map((o) => `${esc(o.name || o.elementId)} ${esc(o.count)}`).join(", ");
      return `<li><b>${esc(p.name || p.processId)}</b> ${code(p.processId)}
        <div class="ba-figures">
          <span>${esc(t("ba.measure.cases"))}: <b>${esc(cs.cases || 0)}</b></span>
          <span>${esc(t("ba.measure.mean"))}: <b>${cs.cases ? secs(cs.meanSeconds) : "—"}</b></span>
          <span>${esc(t("ba.measure.range"))}: <b>${cs.cases ? `${secs(cs.minSeconds)} / ${secs(cs.maxSeconds)}` : "—"}</b></span>
          ${outcomes ? `<span>${esc(t("ba.measure.outcomes"))}: ${outcomes}</span>` : ""}
        </div></li>`;
    };
    const slaRow = (s) => `<li>${esc(t("ba.measure.sla", {
      name: s.name, share: `${Math.round((s.share || 0) * 100)} %`, threshold: secs(s.thresholdSeconds),
      within: s.within || 0, cases: s.cases || 0,
    }))}</li>`;
    const notMeasured = (n) => `<li class="muted">${esc(t(n.kind === "sla" ? "ba.measure.notMeasured.sla" : "ba.measure.notMeasured.kpi", { name: n.name }))}</li>`;
    figures.innerHTML = `<ul class="ba-list">${(m.processes || []).map(proc).join("")}</ul>
      ${(m.slas || []).length ? `<ul class="ba-list">${m.slas.map(slaRow).join("")}</ul>` : ""}
      ${(m.notMeasured || []).length ? `<ul class="ba-list">${m.notMeasured.map(notMeasured).join("")}</ul>` : ""}
      <p class="muted ba-scope">${esc(t("ba.measure.basis"))}</p>`;
  };
  select.addEventListener("change", load);
  load();
}

// mountConfirmation draws a record's confirmation and the form that renews it. The same
// for a capability and a value stream: both carry the ConfirmationView the server
// computes, with the horizon it applied (B10).
function mountConfirmation(el, { api, toast, view, path }) {
  const draw = (v) => {
    const said = v.at
      ? esc(t(v.with ? "ba.confirm.atWith" : "ba.confirm.at", { date: fmtDate(v.at), by: v.by || "—", with: v.with || "" }))
      : esc(t("ba.confirm.never"));
    // The horizon is an installation setting and travels with the answer that applied
    // it; where a view has only the verdict, it says the verdict alone.
    const status = v.horizonMonths
      ? `<span class="pill${v.stale ? " warn" : ""}">${esc(v.stale
        ? plural("ba.confirm.stale", v.horizonMonths)
        : plural("ba.confirm.fresh", v.horizonMonths))}</span>`
      : (v.stale ? `<span class="pill warn">${esc(t("ba.confirm.due"))}</span>` : "");
    el.innerHTML = `<h2>${esc(t("ba.confirm.title"))}</h2>
      <p>${said} ${status}</p>
      ${v.note ? `<p class="muted">${esc(t("ba.confirm.note", { note: v.note }))}</p>` : ""}
      ${v.selfConfirmed ? `<p class="muted">${esc(t("ba.confirm.self"))}</p>` : ""}
      <p class="muted">${esc(t("ba.confirm.hint"))}</p>
      <div class="ba-confirm">
        <input type="text" data-field="with" placeholder="${esc(t("ba.confirm.with"))}" aria-label="${esc(t("ba.confirm.with"))}">
        <input type="text" data-field="note" placeholder="${esc(t("ba.confirm.noteLabel"))}" aria-label="${esc(t("ba.confirm.noteLabel"))}">
        <button type="button" class="btn" data-role="confirm">${esc(t("ba.confirm.button"))}</button>
      </div>`;
    el.querySelector('[data-role="confirm"]').addEventListener("click", async (ev) => {
      const button = ev.currentTarget;
      button.disabled = true;
      try {
        const next = await api("POST", path, {
          with: el.querySelector('[data-field="with"]').value.trim(),
          note: el.querySelector('[data-field="note"]').value.trim(),
        });
        if (toast) toast(t("ba.confirm.done"), "ok");
        draw(next || {});
      } catch (e) {
        button.disabled = false;
        if (toast) toast(t("ba.confirm.failed", { error: e.message }), "err");
        else el.insertAdjacentHTML("beforeend", `<p class="muted">${esc(t("ba.confirm.failed", { error: e.message }))}</p>`);
      }
    });
  };
  draw(view);
}

// ---------- value streams ----------

export async function mountValueStreams(root, { api }) {
  root.innerHTML = `<div class="ba-root">
    <div class="between">
      <div>
        <h1>${esc(t("ba.streams.title"))}</h1>
        <p class="muted" style="margin:0">${esc(t("ba.streams.intro"))}</p>
      </div>
      <a class="btn neutral" href="#/panorama/capabilities">${esc(t("ba.title"))} →</a>
    </div>
    <div id="ba-body"><p class="muted" style="margin-top:16px">${esc(t("ba.loading"))}</p></div>
  </div>`;
  const body = root.querySelector("#ba-body");
  let streams;
  try {
    streams = (await api("GET", "/api/v1/value-streams")) || [];
  } catch (e) {
    failed(body, e);
    return;
  }
  if (!root.isConnected) return;
  if (!streams.length) {
    body.innerHTML = `<div class="card empty" style="margin-top:16px"><p class="muted">${esc(t("ba.streams.empty"))}</p></div>`;
    return;
  }
  const row = (v) => `<tr data-key="${esc(v.key)}">
    <td><div class="artifact-name"><a href="${valueStreamHref(v.key)}"><b>${esc(v.name)}</b></a></div>
      <div class="muted">${code(v.key)}</div>
      ${v.description ? `<div class="ba-summary">${esc(v.description)}</div>` : ""}</td>
    <td class="muted">${esc(ownerText(v.owner))}</td>
    <td data-filter="${esc((v.tags || []).join(" "))}">${tagChips(v.tags)}</td>
    <td data-sort="${v.stageCount || 0}">${v.stageCount || 0}</td>
    <td data-sort="${v.capabilityCount || 0}">${v.capabilityCount || 0}</td>
    <td data-sort="${v.confirmedAt || 0}">${v.confirmedAt ? esc(fmtDate(v.confirmedAt)) : `<span class="muted">${esc(t("ba.list.never"))}</span>`}
      ${v.stale ? `<span class="pill warn">${esc(t("ba.list.due"))}</span>` : ""}</td>
    <td class="muted" data-sort="${v.updatedAt || 0}">${esc(fmtDate(v.updatedAt))}</td>
  </tr>`;
  body.innerHTML = `<div class="card" style="padding:0; margin-top:16px">
    <table data-dt-key="ba-value-streams">
      <thead><tr>
        <th>${esc(t("ba.streams.name"))}</th><th>${esc(t("ba.list.owner"))}</th><th>${esc(t("ba.list.tags"))}</th>
        <th>${esc(t("ba.streams.stages"))}</th><th>${esc(t("ba.streams.capabilities"))}</th>
        <th>${esc(t("ba.list.confirmed"))}</th><th>${esc(t("ba.list.updated"))}</th>
      </tr></thead>
      <tbody>${streams.map(row).join("")}</tbody>
    </table></div>`;
  enhanceTable(body.querySelector("table"), {
    key: "ba-value-streams",
    columns: [{}, {}, {}, { type: "number" }, { type: "number" }, { type: "number" }, { type: "number" }],
  });
}

export async function mountValueStreamDetail(root, { api, toast, key }) {
  root.innerHTML = `<div class="ba-root">
    <a class="muted" href="#/panorama/value-streams">${esc(t("ba.streams.back"))}</a>
    <div id="ba-body"><p class="muted" style="margin-top:16px">${esc(t("ba.loading"))}</p></div>
  </div>`;
  const body = root.querySelector("#ba-body");
  let v, caps, streams;
  try {
    // The stream names capabilities by key, and the capability list says what each is
    // called. The stream record carries its confirmation but not the verdict on it;
    // the stream list does, and is a design-time read where the gap report would walk
    // the deployed landscape for one flag.
    [v, caps, streams] = await Promise.all([
      api("GET", `/api/v1/value-streams/${enc(key)}`),
      api("GET", "/api/v1/capabilities"),
      api("GET", "/api/v1/value-streams"),
    ]);
  } catch (e) {
    if (e.status === 404) {
      body.innerHTML = `<div class="card empty" style="margin-top:16px"><p>${esc(t("ba.streams.notFound"))}</p></div>`;
      return;
    }
    failed(body, e);
    return;
  }
  if (!root.isConnected) return;
  const capByKey = new Map((caps || []).map((c) => [c.key, c]));
  const capItem = (k) => {
    const c = capByKey.get(k);
    if (!c) return `<li>${code(k)} <span class="pill warn">${esc(t("ba.detail.notInMap"))}</span></li>`;
    return `<li><a href="${capabilityHref(k)}"><b>${esc(c.name)}</b></a>
      <span class="chip ba-state ${esc(c.state)}">${esc(stateLabel(c.state))}</span>
      ${c.realized ? "" : `<span class="pill warn">${esc(t("ba.detail.notRealized"))}</span>`}</li>`;
  };
  const stage = (st, i) => `<li class="ba-stage">
    <div><span class="muted">${i + 1}.</span> <b>${esc(st.name || st.key)}</b> ${code(st.key)}</div>
    ${st.description ? `<div class="muted">${esc(st.description)}</div>` : ""}
    ${(st.capabilities || []).length
      ? `<ul class="ba-list">${st.capabilities.map(capItem).join("")}</ul>`
      : `<span class="pill warn">${esc(t("ba.streams.stageNone"))}</span>`}
  </li>`;
  const kpi = (k) => `<li><b>${esc(k.name)}</b> ${code(k.metric)}${k.goal ? ` · ${esc(t("ba.detail.goal", { goal: k.goal }))}` : ""}</li>`;

  body.innerHTML = `
    <div class="ba-head">
      <h1>${esc(v.name || key)}</h1>
      <div class="muted">${code(v.key || key)} ${tagChips(v.tags)}</div>
      ${v.description ? `<p>${esc(v.description)}</p>` : ""}
      <p><b>${esc(t("ba.detail.owner"))}:</b> ${ownerText(v.owner) ? esc(ownerText(v.owner)) : `<span class="muted">${esc(t("ba.detail.ownerNone"))}</span>`}</p>
    </div>
    <section class="card ba-section"><h2>${esc(t("ba.streams.stages"))}</h2>
      <ol class="ba-stages">${(v.stages || []).map(stage).join("")}</ol></section>
    ${(v.kpis || []).length ? `<section class="card ba-section"><h2>${esc(t("ba.detail.kpis"))}</h2><ul class="ba-list">${v.kpis.map(kpi).join("")}</ul></section>` : ""}
    <section class="card ba-section" data-role="confirm"></section>
    <p class="muted ba-scope">${esc(t("ba.detail.changed", {
      created: fmtDate(v.createdAt), createdBy: v.createdBy || "—",
      updated: fmtDate(v.updatedAt), updatedBy: v.updatedBy || "—", revision: v.revision || 0,
    }))}</p>`;

  const summary = (streams || []).find((s) => s.key === key) || {};
  mountConfirmation(body.querySelector('[data-role="confirm"]'), {
    api, toast, view: { ...(v.confirmation || {}), stale: !!summary.stale },
    path: `/api/v1/value-streams/${enc(key)}/confirmation`,
  });
}
