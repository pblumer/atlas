// Which deployed version of a decision may be removed, said in the Console
// (ADR-draft-cleaning-up-the-decision-store).
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
    const who = pins.map((p) => p.processId || `#${p.key}`).join(", ");
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
