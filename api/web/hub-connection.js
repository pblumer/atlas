// The way to atlas hub, station by station (ADR-0499, amended 2026-11-06).
//
// The Console does not ask "is the registry there?" and wait. It reads
// GET /api/v1/blueprints/connection, which loads the signed index exactly as the
// list does and reports each station of the real request as it is passed or where
// it stops: the name resolved, the TCP connection on 443, the TLS handshake, the
// index read, its signature verified. This module draws that way as a route from
// this server to the hub — live, with a timer on the station being waited on, the
// time and the facts each station established, and, where it stops, what that
// usually means. A firewall that drops shows as a connection that times out; one
// that refuses, as a refusal; a TLS inspection, as a certificate from an issuer
// nobody expected; a wrong pinned key, as a signature no key made.
//
// It lives in its own module so the e2e harness can drive it without the Console.

import { t } from "./i18n.js";

const esc = (value) => String(value == null ? "" : value).replace(/[&<>"']/g,
  (c) => ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;", "'": "&#39;" }[c]));

// The station names, as the server sends them, and how each is labelled.
const LABELS = {
  resolve: "blueprint.route.resolve",
  connect: "blueprint.route.connect",
  tls: "blueprint.route.tls",
  index: "blueprint.route.index",
  signature: "blueprint.route.signature",
};

// What a cause is called and what it usually means. A timeout at the connection
// is told apart: that is the signature of a firewall that drops.
const CAUSES = {
  timeout: ["blueprint.cause.timeout", "blueprint.hint.timeout"],
  dropped: ["blueprint.cause.dropped", "blueprint.hint.dropped"],
  dns_not_found: ["blueprint.cause.dnsNotFound", "blueprint.hint.dnsNotFound"],
  dns_failed: ["blueprint.cause.dnsFailed", "blueprint.hint.dnsFailed"],
  dns_empty: ["blueprint.cause.dnsEmpty", "blueprint.hint.dnsEmpty"],
  nonpublic_address: ["blueprint.cause.nonpublic", "blueprint.hint.nonpublic"],
  refused: ["blueprint.cause.refused", "blueprint.hint.refused"],
  unreachable: ["blueprint.cause.unreachable", "blueprint.hint.unreachable"],
  connect_failed: ["blueprint.cause.connectFailed", "blueprint.hint.connectFailed"],
  unknown_authority: ["blueprint.cause.unknownAuthority", "blueprint.hint.unknownAuthority"],
  hostname_mismatch: ["blueprint.cause.hostnameMismatch", "blueprint.hint.hostnameMismatch"],
  certificate_expired: ["blueprint.cause.expired", "blueprint.hint.expired"],
  not_tls: ["blueprint.cause.notTLS", "blueprint.hint.notTLS"],
  tls_failed: ["blueprint.cause.tlsFailed", "blueprint.hint.tlsFailed"],
  redirect: ["blueprint.cause.redirect", "blueprint.hint.redirect"],
  http_status: ["blueprint.cause.httpStatus", "blueprint.hint.httpStatus"],
  too_large: ["blueprint.cause.tooLarge", "blueprint.hint.tooLarge"],
  read_failed: ["blueprint.cause.readFailed", "blueprint.hint.readFailed"],
  signature_unavailable: ["blueprint.cause.signatureUnavailable", "blueprint.hint.signatureUnavailable"],
  no_pinned_key: ["blueprint.cause.noPinnedKey", "blueprint.hint.noPinnedKey"],
  index_invalid: ["blueprint.cause.indexInvalid", "blueprint.hint.indexInvalid"],
};

// The network stations: where they stop, how Atlas connects is part of the answer.
const NETWORK = new Set(["resolve", "connect", "tls", "index"]);

const ms = (n) => (n < 1000 ? `${n} ms` : `${(n / 1000).toFixed(1).replace(".", t("blueprint.route.decimal"))} s`);
const size = (n) => (n < 1024 ? `${n} B` : `${(n / 1024).toFixed(1).replace(".", t("blueprint.route.decimal"))} KB`);
const short = (key) => (key ? String(key).replace(/^sha256:/, "").slice(0, 12) + "…" : "");

// facts says in one line what a passed station established.
function facts(step) {
  switch (step.stage) {
    case "resolve": {
      const list = step.addresses || [];
      const more = list.length > 1 ? " " + t("blueprint.route.more", { n: list.length - 1 }) : "";
      return `${step.host || ""} → ${list[0] || "?"}${more}`;
    }
    case "connect": return step.address || "";
    case "tls": return [step.tlsVersion, step.issuer ? t("blueprint.route.issuer", { issuer: step.issuer }) : ""].filter(Boolean).join(" · ");
    case "index": return `HTTP ${step.status || 200} · ${size(step.bytes || 0)}`;
    case "signature": return t("blueprint.route.key", { key: short(step.key) });
    default: return "";
  }
}

// causeOf names the cause a stopped station gives, a dropped connection apart.
const causeOf = (step) => (step.stage === "connect" && step.cause === "timeout" ? "dropped" : step.cause);

// finding is the explanation under the route for the station it stopped at.
function finding(step, host) {
  const [title, hint] = CAUSES[causeOf(step)] || ["blueprint.cause.unknown", "blueprint.hint.unknown"];
  const vars = {
    host: step.host || host || "", addresses: (step.addresses || []).join(", "), address: step.address || "",
    issuer: step.issuer || "?", status: step.status || "?", location: step.location || "?", tried: step.tried || 1,
  };
  const facts = [];
  if ((step.addresses || []).length) facts.push(`${esc(t("blueprint.route.answer"))}: <code>${esc(vars.addresses)}</code>`);
  if (step.address) facts.push(`${esc(t("blueprint.route.target"))}: <code>${esc(step.address)}</code>`);
  if (step.issuer) facts.push(`${esc(t("blueprint.route.presented"))}: <code>${esc(step.issuer)}</code>`);
  if (step.location) facts.push(`${esc(t("blueprint.route.location"))}: <code>${esc(step.location)}</code>`);
  if (step.status) facts.push(`HTTP <code>${esc(step.status)}</code>`);
  // The station above already names the cause; the finding says what it means.
  return `<div class="hub-finding" role="alert" data-cause="${esc(causeOf(step))}" aria-label="${esc(t(title, vars))}">
      <p>${esc(t(hint, vars))}</p>
      ${facts.length ? `<p class="small">${facts.join(" · ")}</p>` : ""}
      ${NETWORK.has(step.stage) ? `<p class="small muted">${esc(t("blueprint.route.how"))}</p>` : ""}
      ${step.error ? `<p class="small">${esc(t("blueprint.route.error"))}: <code>${esc(step.error)}</code></p>` : ""}
    </div>`;
}

// openRoute draws the route into container and returns its update(event), fed
// the stream's lines one by one. It owns nothing else on the page.
export function openRoute(container) {
  let stations = [];
  let host = "";
  let version = "";
  const steps = new Map();
  let done = null;
  let waitingSince = performance.now();
  let timer = null;

  const state = (stage, i) => {
    const step = steps.get(stage);
    if (step) return step.ok ? "ok" : "fail";
    if (done || [...steps.values()].some((s) => !s.ok)) return "skipped";
    return i === steps.size ? "active" : "pending";
  };

  const render = () => {
    const total = [...steps.values()].reduce((sum, s) => sum + (s.ms || 0), 0) || 1;
    let offset = 0;
    const rows = stations.map((stage, i) => {
      const step = steps.get(stage);
      const st = state(stage, i);
      let detail = "";
      if (st === "ok") detail = esc(facts(step));
      else if (st === "fail") detail = esc(t(CAUSES[causeOf(step)]?.[0] || "blueprint.cause.unknown", { status: step.status || "?" }));
      else if (st === "active") detail = `<span data-wait>${esc(t("blueprint.route.waiting", { s: ms(0) }))}</span>${stage === "connect" ? ` <span class="muted">· ${esc(t("blueprint.route.limit"))}</span>` : ""}`;
      else if (st === "skipped") detail = esc(t("blueprint.route.notReached"));
      // The bar is where this station's time falls in the whole load.
      const bar = step ? `<span class="hub-bar" style="margin-left:${(offset / total) * 100}%;width:${Math.max(1.5, ((step.ms || 0) / total) * 100)}%"></span>` : "";
      if (step) offset += step.ms || 0;
      // Where the way ends, what that usually means stands right there, before
      // the stations it never reached.
      return `<li class="hub-stop" data-stage="${esc(stage)}" data-state="${st}">
          <span class="hub-dot" aria-hidden="true"></span>
          <span class="hub-name">${esc(t(LABELS[stage] || stage))}</span>
          <span class="hub-ms">${step ? esc(ms(step.ms || 0)) : ""}</span>
          <span class="hub-detail small">${detail}</span>
          <span class="hub-track" aria-hidden="true">${bar}</span>
          ${st === "fail" ? `<span class="hub-note">${finding(step, host)}</span>` : ""}
        </li>`;
    }).join("");
    const destination = done
      ? (done.ok ? "ok" : "skipped")
      : ([...steps.values()].some((s) => !s.ok) ? "skipped" : "pending");
    const reach = done && done.ok
      ? esc(t("blueprint.route.releases", { n: done.releases, m: done.compatible, version: done.atlasVersion }))
      : esc(t("blueprint.route.notReached"));
    container.innerHTML = `<ol class="hub-route" aria-label="${esc(t("blueprint.route.title"))}">
        <li class="hub-stop hub-end" data-state="ok"><span class="hub-dot" aria-hidden="true"></span>
          <span class="hub-name">${esc(t("blueprint.route.origin"))}</span><span class="hub-ms"></span>
          <span class="hub-detail small">atlas ${esc(version)}</span><span class="hub-track"></span></li>
        ${rows}
        <li class="hub-stop hub-end" data-stage="hub" data-state="${destination}"><span class="hub-dot" aria-hidden="true"></span>
          <span class="hub-name">${esc(t("blueprint.route.destination"))}</span>
          <span class="hub-ms">${destination === "ok" ? esc(ms(done.ms || 0)) : ""}</span>
          <span class="hub-detail small">${esc(host)} · ${destination === "ok" ? reach : esc(t("blueprint.route.notReached"))}</span>
          <span class="hub-track"></span></li>
      </ol>`;
  };

  // The station being waited on counts up, so a connection a firewall drops is
  // seen as five seconds of nothing, not as a spinner.
  const tick = () => {
    const el = container.querySelector("[data-wait]");
    if (el) el.textContent = t("blueprint.route.waiting", { s: ms(Math.round(performance.now() - waitingSince)) });
  };

  const stopTimer = () => {
    if (timer) clearInterval(timer);
    timer = null;
  };

  return {
    update(event) {
      if (event.type === "start") {
        stations = event.stations || [];
        host = event.host || "";
        version = event.atlasVersion || "";
        waitingSince = performance.now();
        stopTimer();
        timer = setInterval(tick, 100);
      } else if (event.type === "step") {
        steps.set(event.stage, event);
        waitingSince = performance.now();
      } else if (event.type === "done") {
        done = event;
        stopTimer();
      }
      render();
    },
    // stop ends the timer when the stream broke off before its done line.
    stop: stopTimer,
  };
}

// summary is the one line shown above the listing once the way was passed: where,
// how fast, how it was secured, and how many releases this atlas may install.
export function routeSummary(done, steps, host) {
  const tls = steps.find((s) => s.stage === "tls");
  const sig = steps.find((s) => s.stage === "signature");
  return [
    t("blueprint.route.connected", { host, time: ms(done.ms || 0) }),
    tls && tls.tlsVersion,
    sig && t("blueprint.route.key", { key: short(sig.key) }),
    t("blueprint.route.releases", { n: done.releases, m: done.compatible, version: done.atlasVersion }),
  ].filter(Boolean).join(" · ");
}

// connectToHub reads the connection stream and hands each line to onEvent as it
// arrives. Resolves to the done line, or throws when the route answered no stream
// (no registry configured, no permission) or the stream broke off without one.
export async function connectToHub(onEvent, fetchImpl = fetch) {
  const res = await fetchImpl("/api/v1/blueprints/connection", { cache: "no-store" });
  if (!res.ok || !res.body) {
    const data = await res.json().catch(() => ({}));
    throw new Error(data.error || "HTTP " + res.status);
  }
  const reader = res.body.getReader();
  const decoder = new TextDecoder();
  let buffer = "";
  let done = null;
  const take = (line) => {
    if (!line.trim()) return;
    const event = JSON.parse(line);
    if (event.type === "done") done = event;
    onEvent(event);
  };
  for (;;) {
    const { value, done: end } = await reader.read();
    if (end) break;
    buffer += decoder.decode(value, { stream: true });
    for (let at = buffer.indexOf("\n"); at >= 0; at = buffer.indexOf("\n")) {
      take(buffer.slice(0, at));
      buffer = buffer.slice(at + 1);
    }
  }
  take(buffer + decoder.decode());
  if (!done) throw new Error(t("blueprint.route.brokenOff"));
  return done;
}
