// The confirm text for deleting a DMN reference
// (ADR-0331).
//
// It lives apart from app.js because it is a pure function of what the server
// reports, so it can be driven and asserted in a browser test without standing up
// the whole Console — the same split process-doc.js makes for the documentation
// export. app.js only fetches the impact and puts the text in front of the author.

import { t, plural } from "./i18n.js";

// refDeleteWarning composes the confirm from what the server says deleting this
// reference would break. The generic sentence it replaces was true and useless:
// what a reference decides is not the file on disk, it is whether anything can
// still be *deployed* against the decisions that model provides — and the refusal
// for that arrives weeks later without mentioning the deletion.
//
// It degrades in one direction only. No impact, an unresolved handle, or a
// decision another reference also provides all read as the plain sentence, because
// in each of those cases nothing is actually at risk and a warning would be noise.
export function refDeleteWarning(impact) {
  const plain = t("dmnref.delete.plain");
  if (!impact || !impact.resolved) return plain;
  const exclusive = impact.exclusive || [];
  if (!exclusive.length) return plain;

  const blocked = impact.blocked || [];
  const hidden = impact.blockedHidden || 0;
  const lines = [
    plural("dmnref.delete.last", exclusive.length, { decisions: exclusive.join(", ") }),
  ];
  if (!blocked.length && !hidden) {
    lines.push("", t("dmnref.delete.nothingStops"));
    return lines.join("\n");
  }
  const total = blocked.length + hidden;
  lines.push("", plural("dmnref.delete.blocked", total));
  for (const b of blocked) {
    // Which artefact, and the binding that makes losing the model fatal to it —
    // "deployment" always, "latest" only while nothing is deployed for the decision.
    const where = b.kind === "deployed" ? t("dmnref.delete.deployed", { version: b.version }) : t("dmnref.delete.draft");
    lines.push(t("dmnref.delete.line", { name: b.name || b.processId, where, decision: b.decisionId, binding: b.binding }));
  }
  if (hidden) {
    lines.push(plural("dmnref.delete.hidden", hidden));
  }
  lines.push("", t("dmnref.delete.running"));
  return lines.join("\n");
}
