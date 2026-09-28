// Which deployed version of a decision may be removed, said in the Console
// (ADR-0336).
//
// The server decides; this only keeps a reader from clicking into a refusal, and
// says which of the two refusals it would be. It is a pure function of the version
// listing so it can be driven and asserted in a browser test without the Console
// around it — the split dmnref-impact.js already makes for the other deletion.
//
// It deliberately mirrors the server's two guards rather than approximating them.
// If they ever disagree the button is wrong, not the outcome: the delete still goes
// through the refusal, and what a reader loses is the explanation arriving early.

// versionDeleteState reports whether one row of
// GET /api/v1/decision-deployments?decisionId=… may be deleted, and why not when it
// may not.
//
//   - held by a pinned definition: a deployed process resolved its latest-bound
//     reference to this exact key and carries no copy of the model, so removing it
//     would leave a business rule task that cannot evaluate (ADR-0329);
//   - the current version with older ones behind it: removing it would send the next
//     deploy quietly back a version, and free a version number the surviving records
//     no longer account for.
//
// `rows` is the decision's whole version listing, which is what makes the second
// rule answerable: a current version is removable exactly when it is the last one.
export function versionDeleteState(row, rows) {
  const pins = (row && row.pinnedBy) || [];
  if (pins.length) {
    const who = pins.map((p) => {
      const name = p.processId || `#${p.key}`;
      const how = holderNote(p).label;
      return how ? `${name} (${how})` : name;
    }).join(", ");
    return {
      deletable: false,
      why: `Held by ${who} — ${pins.length === 1 ? "that process" : "those processes"} pinned this version when deployed`,
    };
  }
  const others = ((rows || []).length || 1) - 1;
  if (row && row.current && others > 0) {
    return {
      deletable: false,
      why: "The current version cannot go while older ones remain — remove those first",
    };
  }
  return { deletable: true, why: "" };
}

// holderNote says how one definition in a version's `pinnedBy` holds it, because the
// two ways are released differently (ADR-0423):
//
//   - binding "latest": a definition deployed under ADR-0319 froze its latest-bound
//     task on this version. It is not what its author chose; deploying the process
//     again gives it a definition that follows each decision version, and the old
//     one keeps holding this version until it is undeployed;
//   - binding "version": the task names this version (atlas:version) on purpose, and
//     only changing the task releases it.
//
// A listing from a server that does not say which gets the sentence it always had.
export function holderNote(p) {
  const who = `${(p && p.processId) || `#${p && p.key}`} v${(p && p.version) || "?"}`;
  if (p && p.binding === "latest") {
    return {
      label: "frozen",
      title: `${who} froze latest on this version when it was deployed, and keeps evaluating it. ` +
        "Deploy the process again to have it follow each new version; this definition holds the version until it is undeployed.",
    };
  }
  if (p && p.binding === "version") {
    return { label: "fixed", title: `${who} names this version: its business rule task evaluates exactly this one` };
  }
  return { label: "", title: `${who} pinned this version at deploy time` };
}
