// The confirm text for deleting a DMN reference
// (ADR-0331).
//
// It lives apart from app.js because it is a pure function of what the server
// reports, so it can be driven and asserted in a browser test without standing up
// the whole Console — the same split process-doc.js makes for the documentation
// export. app.js only fetches the impact and puts the text in front of the author.

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
  const plain = "Delete this DMN reference? The model file stays in the model store and appears under Not assigned, where a reference can be put back on it.";
  if (!impact || !impact.resolved) return plain;
  const exclusive = impact.exclusive || [];
  if (!exclusive.length) return plain;

  const blocked = impact.blocked || [];
  const hidden = impact.blockedHidden || 0;
  const lines = [
    `Delete this DMN reference? It is the last model for ${exclusive.length === 1 ? "the decision" : "the decisions"} ${exclusive.join(", ")}.`,
  ];
  if (!blocked.length && !hidden) {
    lines.push("", "Nothing would stop deploying today. The model file stays in the model store and appears under Not assigned.");
    return lines.join("\n");
  }
  const total = blocked.length + hidden;
  lines.push("", `${total} ${total === 1 ? "artefact" : "artefacts"} could then not be deployed:`);
  for (const b of blocked) {
    // Which artefact, and the binding that makes losing the model fatal to it —
    // "deployment" always, "latest" only while nothing is deployed for the decision.
    const where = b.kind === "deployed" ? `deployed v${b.version}` : "draft";
    lines.push(`  • ${b.name || b.processId} (${where}) — ${b.decisionId}, binding ${b.binding}`);
  }
  if (hidden) {
    lines.push(`  • ${hidden} more in ${hidden === 1 ? "an application" : "applications"} you cannot see`);
  }
  lines.push("", "Running instances are not affected. The model file stays in the model store and appears under Not assigned.");
  return lines.join("\n");
}
