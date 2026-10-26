// Which deployed definitions evaluate a decision version that was frozen when they
// were deployed, said where their owner looks (ADR-0423).
//
// A definition deployed under ADR-0319 froze each latest-bound business rule task on
// the decision version that was newest at its deploy, and keeps evaluating it until
// the process is deployed again. Since ADR-0423 latest means the newest version when
// the task runs, and the Modeler says so — which is not what those definitions do.
// The server reports them as `frozenDecisions` on GET /api/v1/processes; this turns
// that into the words the Modeler home, the Modeler and Operations views of a
// deployment, and the decision editor show.
//
// Pure functions of the listing, so a browser test drives them without the pages
// around them — the split decision-cleanup.js already makes.

import { t, plural } from "./i18n.js";

// frozenLine says one frozen decision: what it runs, and what latest is now.
export function frozenLine(d) {
  const at = d.version ? `v${d.version}` : t("frozen.deployedModel");
  if (!d.behind) return t("frozen.line.current", { decision: d.decisionId, at });
  const now = d.latestVersion ? `v${d.latestVersion}` : t("frozen.deployment", { key: d.latestKey });
  return t("frozen.line.behind", { decision: d.decisionId, at, now });
}

// frozenMark is what to show on one deployed definition, or null when it follows
// latest — which is every definition deployed since ADR-0423, so the mark is rare and
// means something where it appears. `behind` says a newer version is deployed that
// this definition does not run; the pages draw that one as a warning.
export function frozenMark(proc) {
  const list = (proc && proc.frozenDecisions) || [];
  if (!list.length) return null;
  const behind = list.some((d) => d.behind);
  return {
    behind,
    label: t(behind ? "frozen.labelBehind" : "frozen.label"),
    title: t("frozen.title", { lines: list.map(frozenLine).join("; ") }),
  };
}

// frozenBehindOn lists the newest deployed version of each process that is frozen on
// one of decisionIds and no longer runs its newest version. The newest version is the
// one new instances start on and the one deploying again replaces; an older frozen
// version only concerns the instances still on it.
export function frozenBehindOn(procs, decisionIds) {
  const ids = new Set(decisionIds || []);
  const newest = new Map();
  for (const p of procs || []) {
    const seen = newest.get(p.processId);
    if (!seen || (p.version || 0) > (seen.version || 0)) newest.set(p.processId, p);
  }
  const out = [];
  for (const p of newest.values()) {
    const decisions = (p.frozenDecisions || []).filter((d) => d.behind && ids.has(d.decisionId));
    if (decisions.length) {
      out.push({ key: p.key, processId: p.processId, name: p.name || p.processId, version: p.version, decisions });
    }
  }
  return out.sort((a, b) => a.name.localeCompare(b.name));
}

// frozenDeployNotice is what the decision editor says right after a deploy that some
// deployed processes will not follow, or "" when every one does. The deploy is the
// moment the author expects every latest-bound task to change its answer, so it is
// where the ones that will not are named.
export function frozenDeployNotice(held) {
  if (!held || !held.length) return "";
  const names = held.map((h) => `${h.name} v${h.version} (${h.decisions.map(frozenLine).join("; ")})`);
  const shown = names.slice(0, 5).join(", ") + (names.length > 5 ? t("frozen.more", { n: names.length - 5 }) : "");
  return plural("frozen.notice", held.length, { list: shown });
}
