// What a restore did not take
// (ADR-0357).
//
// It lives apart from app.js because it is a pure function of what the server
// reports, so it can be driven and asserted in a browser test without standing up the
// whole Console — the same split dmnref-impact.js makes for the delete confirm.
// app.js only uploads the archive and puts this in front of the operator.

// restoreSkipNote names the deployed definitions a restore held back.
//
// A definition key is issued by one installation's counter, and the portable archive
// does not carry that counter — so a record from another installation can claim a key
// that already names something here. Taking it would attach this installation's
// instance history to a foreign definition, so it is not taken.
//
// The keys are named rather than counted, on purpose. A count says something went
// wrong; the keys and both sides of each clash say *what*, which is what tells an
// operator whether they meant to restore onto a fresh instance. It is the same
// judgement the delete refusals make: name what is in the way.
export function restoreSkipNote(report) {
  const held = (report && report.collisions) || [];
  if (!held.length) return "";
  const named = held
    .map((c) => `key ${c.key} (here: ${c.here}; archive: ${c.incoming})`)
    .join(", ");
  return ` ${held.length} deployed definition(s) were NOT restored because their keys are already in use here: ${named}.`
    + " A definition key belongs to the installation that issued it — restore onto a fresh instance to bring these across.";
}

// restoreSummary is the whole status line: what landed, whether a restart is needed,
// and what was held back. A restore that took everything reads exactly as it always
// did — the extra sentence appears only when there is something to say.
export function restoreSummary(report) {
  const d = report || {};
  return `Restored ${d.restored || 0} file(s).`
    + (d.restartRequired ? " Restart the server to activate restored deployments." : "")
    + restoreSkipNote(d);
}
