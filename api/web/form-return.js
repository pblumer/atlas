// The round trip between a step in a diagram and the form it names
// (ADR-0465).
//
// "Open form" and "＋ New form" on a start event, a user task or a repair form leave the
// diagram for the form editor, and the editor needs two things the form itself does not
// say: where back is, and which element to link when the author returns with a form
// created on the way, or with the one they opened under a new id (ADR-0222). The first
// is in the address, as a /from/{draft}/{element} tail, so it survives a reload and
// shows in the URL. The second is a record in sessionStorage, written by the Modeler as
// it leaves and completed by the form editor when it saves — the shape the decision
// editor's round trip has (ADR-0320), kept apart from it because a form is linked by
// one id and a decision is adopted with its inputs.
//
// The record also says which form the element named when the trip began. Returning
// links the saved form only over that, so a record left behind by a trip that never
// came back cannot rewrite a link somebody changed in the meantime.

// TRIP_KEY holds the one trip in flight. sessionStorage rather than a module variable,
// because the way back is a navigation and the author may reload on it; and per tab,
// because a trip belongs to the tab it was started in.
const TRIP_KEY = "atlas.form.trip";

// fromTail is the part of a form editor address that names the way back.
export function fromTail(draftId, elementId) {
  return "/from/" + encodeURIComponent(draftId) + "/" + encodeURIComponent(elementId);
}

// beginFormTrip records that the element is leaving for the form editor, and what it
// names right now ("" for no form). Any earlier record is replaced: a tab makes one trip
// at a time.
export function beginFormTrip({ draftId, elementId, linked }) {
  try {
    sessionStorage.setItem(TRIP_KEY, JSON.stringify({ draftId, elementId, linked: linked || "" }));
  } catch { /* a browser refusing session storage costs the link on return, not the trip */ }
}

// noteFormSaved completes the trip with the id the form was just saved under. Only the
// trip this editor was opened for is completed: an address with a /from/ tail that no
// record stands behind — pasted, or from another tab — gets the way back and nothing
// more, since nobody recorded what the element named when it left.
export function noteFormSaved(from, formId) {
  if (!from || !from.draftId || !formId) return;
  const rec = read();
  if (!rec || rec.draftId !== from.draftId || rec.elementId !== from.elementId) return;
  rec.formId = formId;
  try { sessionStorage.setItem(TRIP_KEY, JSON.stringify(rec)); } catch { /* best-effort */ }
}

// takeFormTrip returns the trip that comes back to this draft, and clears it. One-shot
// on purpose, like the decision editor's takeAdoption: opening any draft ends the trip,
// so a record cannot linger until the day its diagram is opened again.
export function takeFormTrip(draftId) {
  const rec = read();
  try { sessionStorage.removeItem(TRIP_KEY); } catch { /* best-effort */ }
  if (!rec || !rec.elementId || rec.draftId !== draftId) return null;
  return rec;
}

function read() {
  let raw = null;
  try { raw = sessionStorage.getItem(TRIP_KEY); } catch { return null; }
  if (!raw) return null;
  try {
    const rec = JSON.parse(raw);
    return rec && typeof rec === "object" ? rec : null;
  } catch { return null; }
}
