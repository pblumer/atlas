// Live availability checking for an artifact's ID field.
//
// A draft is stored under its process id and a form under its own id (ADR-0021,
// ADR-0028), so those fields are the artifact's *identity*, not a label on it:
// retyping one renames the artifact, and typing an id another artifact already holds
// would land the save on top of it. The server settles both — a save renames rather
// than leaving a duplicate behind, and refuses a collision with 409
// (ADR-0222) — but a refusal at Save arrives after the author has
// typed the id, tabbed away, and carried on believing it took.
//
// So the id is checked while it is being typed: the field turns red and says what holds
// the id, which makes the collision a correction to what is in front of the author
// rather than a failed save minutes later.
//
// The check is advisory. It can go stale between the last keystroke and Save, and an id
// held inside an application the author cannot see comes back as a bare "taken" with no
// name (ADR-0071). The save is the authority; this is what makes needing it rare.

const DEBOUNCE_MS = 300;

// makeIdCheck builds a debounced availability check over a value the caller reads. It
// is the shape both editors need, because only one of them has an <input> to hang it
// on: the BPMN editor checks a text field, the form editor checks the id the Design
// pane's properties panel holds inside the schema.
//
//   kind    — "drafts" or "forms": which store to probe.
//   noun    — what to call one of them in the message ("draft", "form").
//   read()  — the id as it stands now.
//   own()   — the id the artifact is currently stored under; that id never collides
//             with itself. "" means it has never been saved, so every existing id does.
//   onState(state, text) — paint the verdict: "ok" (free, or its own), "taken", or
//             "unknown" (the probe failed — say nothing and let the save decide).
export function makeIdCheck({ api, kind, noun, read, own, onState }) {
  let timer = null;
  let seq = 0; // guards against a slow answer overwriting a newer one
  let disposed = false;
  let last = null; // the id most recently answered for, and what it answered

  const run = async (cached) => {
    if (disposed) return;
    const id = (read() || "").trim();
    const mine = (own() || "").trim();
    if (!id || id === mine) { onState("ok", ""); return; }
    // Editing anything in the form editor re-runs this, not just the id field, so an
    // id already answered for repaints from that answer instead of asking again.
    if (cached && last && last.id === id) { onState(last.state, last.text); return; }
    const n = ++seq;
    let res;
    try {
      res = await api("GET", `/api/v1/${kind}/${encodeURIComponent(id)}/availability`);
    } catch {
      // The probe is a convenience; a failure must not colour a field red on a guess.
      if (n === seq && !disposed) onState("unknown", "");
      return;
    }
    if (n !== seq || disposed) return;
    const state = res && res.available === false ? "taken" : "ok";
    const text = state !== "taken" ? "" : res.usedBy
      ? `Already taken by the ${noun} “${res.usedBy}” — pick another id.`
      : `Already taken by another ${noun} — pick another id.`;
    last = { id, state, text };
    onState(state, text);
  };

  return {
    // check debounces and may answer from the last result; the caller can fire it on
    // every keystroke or change event.
    check() { clearTimeout(timer); timer = setTimeout(() => run(true), DEBOUNCE_MS); },
    // now asks again, right away — for a value that just landed (a load, a save, a
    // refusal the check had not caught up with).
    now: () => run(false),
    dispose() { disposed = true; clearTimeout(timer); },
  };
}

// attachIdCheck wires makeIdCheck onto an id <input>, colouring the field and writing
// the verdict into a note it inserts after the input's <label>. A panel that re-renders
// wholesale discards both; a pending check on a detached input drops itself.
export function attachIdCheck(input, { api, kind, noun, own }) {
  const note = document.createElement("p");
  note.className = "id-check";
  note.hidden = true;
  (input.closest("label") || input).after(note);

  const check = makeIdCheck({
    api, kind, noun, own,
    read: () => input.value,
    onState: (state, text) => {
      if (!input.isConnected) { check.dispose(); return; }
      input.classList.toggle("id-taken", state === "taken");
      if (state === "taken") input.setAttribute("aria-invalid", "true");
      else input.removeAttribute("aria-invalid");
      note.textContent = text || "";
      note.hidden = !text;
    },
  });

  const onInput = () => check.check();
  input.addEventListener("input", onInput);
  check.now(); // reflect whatever the field already holds

  return {
    recheck: check.now,
    dispose() {
      check.dispose();
      input.removeEventListener("input", onInput);
      note.remove();
    },
  };
}
