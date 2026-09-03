// Moving a running instance onto another version of its process, from the Operations
// surface (ADR-0162, slice 4).
//
// The engine and the API already refuse a migration that cannot hold, and answer "what
// would this do?" without writing anything. This is the surface that makes both usable:
// the operator picks a target version, sees the derived mapping and every reason it
// would be refused *before* committing, gives a reason, and confirms.
//
// It lives in its own module for the reason the worker dialog does: app.js boots the
// whole console on import, so anything left in it is only ever exercised by hand. Here
// it is reachable from a test.

const esc = (s) => String(s).replace(/[&<>"']/g, (c) =>
  ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;", "'": "&#39;" }[c]));

// versionLabel names a deployed version the way an operator reads one.
const versionLabel = (v) => `v${v.version}${v.versionTag ? ` · ${v.versionTag}` : ""}`;

// otherVersions is every deployed version of the same process except the one the
// instance is on, newest first — the set a migration can target. A process with only
// one deployed version has nowhere to go, and the caller says so rather than opening a
// dialog with an empty picker.
export function otherVersions(processes, processId, currentDefKey) {
  return (processes || [])
    .filter((p) => p.processId === processId && String(p.key) !== String(currentDefKey))
    .sort((a, b) => b.version - a.version);
}

// planSummary reduces a plan's element mapping to what an operator actually needs to
// read. Most pairs are an element matched to itself by id and say nothing; the ones
// worth showing are those whose id changed, which is where an override was needed or a
// rename was picked up. The count of the rest is the reassurance that the silence is
// coverage rather than an empty answer.
export function planSummary(plan) {
  const pairs = (plan && plan.mapping) || [];
  const moved = pairs.filter((p) => p.from !== p.to);
  return { total: pairs.length, moved, unchanged: pairs.length - moved.length };
}

// planHTML renders a plan: whether it would go through, what would be refused and why,
// and the mapping worth reading. The refusals lead — they are the answer to the question
// an operator actually has, and a plan that cannot go through is the case this endpoint
// exists for.
export function planHTML(plan) {
  if (!plan) return "";
  const s = planSummary(plan);
  const problems = (plan.problems || []).map((p) => `<li><b>${esc(p.elementId || "an element")}</b> ${esc(p.reason)}</li>`).join("");
  const head = plan.migratable
    ? `<p class="mig-verdict ok">&#10003; This instance can move to v${esc(String(plan.toVersion))}.</p>`
    : `<p class="mig-verdict bad">&#9888; This instance cannot move to v${esc(String(plan.toVersion))}${
      plan.problems && plan.problems.length ? ":" : "."}</p>`;
  const why = problems
    ? `<ul class="mig-problems">${problems}</ul>
       <p class="muted" style="margin:6px 0 0;font-size:12px">Nothing has been changed. A migration is refused rather than
       half-applied — a token left on an element that means something else in the target version corrupts an instance in a
       way no later fix repairs.</p>`
    : "";
  const movedRows = s.moved.map((p) =>
    `<li><span class="mono">${esc(p.from)}</span> &rarr; <span class="mono">${esc(p.to)}</span></li>`).join("");
  const mapping = s.total
    ? `<div class="mig-map">
        <h4>Element mapping</h4>
        ${s.moved.length
      ? `<p class="muted" style="margin:0 0 4px;font-size:12px">${s.moved.length} element${s.moved.length === 1 ? "" : "s"} mapped across a changed id:</p>
             <ul class="mig-moved">${movedRows}</ul>`
      : ""}
        <p class="muted" style="margin:4px 0 0;font-size:12px">${s.unchanged} element${s.unchanged === 1 ? "" : "s"} matched by
        id, unchanged. Elements pair by their BPMN id — the identity a modeler controls and the only one stable across an
        ordinary edit.</p>
      </div>`
    : `<p class="muted" style="margin:6px 0 0;font-size:12px">No elements could be paired between the two versions.</p>`;
  return head + why + mapping;
}

// migrateInstanceFlow opens the dialog on one running instance. Resolves true when the
// instance was migrated, false otherwise (cancelled, refused, or the call failed — each
// already reported). `onDone` is called after a successful migration so the caller can
// reload whatever it is showing.
export async function migrateInstanceFlow({ api, toast, instanceKey, processId, fromVersion, fromProcessDefKey, onDone }) {
  let processes;
  try {
    processes = await api("GET", "/api/v1/processes");
  } catch (e) {
    toast("could not read the deployed versions: " + (e && e.message ? e.message : e), "err");
    return false;
  }
  const targets = otherVersions(processes, processId, fromProcessDefKey);
  if (!targets.length) {
    // Not an error and not a dialog: there is genuinely nowhere to go, and saying so is
    // the whole answer.
    toast("Only one version of this process is deployed — deploy the fixed model first, then migrate.", "warn");
    return false;
  }

  const choice = await askMigration({ api, instanceKey, processId, fromVersion, targets });
  if (!choice) return false;
  try {
    await api("POST", `/api/v1/instances/${encodeURIComponent(instanceKey)}/migrate`, {
      targetProcessDefKey: choice.target.key,
      reason: choice.reason,
    });
  } catch (e) {
    toast(migrateError(e), "err");
    return false;
  }
  toast(`Instance migrated to ${versionLabel(choice.target)}`, "ok");
  if (onDone) await onDone();
  return true;
}

// migrateError turns a failed migration into something an operator can act on. The
// refusal case carries the plan, so the message names what is wrong rather than
// reporting a status code; the admin gate is worth naming too, since it is a
// configuration answer rather than a problem with the instance.
function migrateError(e) {
  const msg = (e && e.message) || String(e);
  if (e && e.status === 403) return "migrating an instance is admin-only when authentication is on";
  if (e && e.status === 409 && e.body && Array.isArray(e.body.problems) && e.body.problems.length) {
    const first = e.body.problems[0];
    const more = e.body.problems.length - 1;
    return `refused: ${first.elementId ? first.elementId + " " : ""}${first.reason}` +
      (more > 0 ? ` (and ${more} more)` : "");
  }
  return "migration failed: " + msg;
}

// askMigration is the dialog itself: pick a target, read what it would do, say why.
// The plan is re-fetched on every change of target, because it is the answer to *that*
// target — showing a stale one beside a new selection is the one way this dialog could
// mislead about live process state.
function askMigration({ api, instanceKey, processId, fromVersion, targets }) {
  return new Promise((resolve) => {
    const ov = document.createElement("div");
    ov.className = "modal-ov";
    ov.innerHTML = `
      <div class="modal confirm-modal mig-modal" role="dialog" aria-modal="true" aria-labelledby="mig-title">
        <div class="modal-head"><h2 id="mig-title">Migrate instance &middot; ${esc(String(instanceKey))}</h2></div>
        <div class="modal-body">
          <p class="muted" style="margin:0 0 10px">This instance is running on <b>v${esc(String(fromVersion))}</b> of
          <b>${esc(processId)}</b>. Moving it to another deployed version keeps its variables, its tasks and everything
          already done — it is rebound, not restarted. Pick the version to move it to; what that would do is shown before
          anything is written.</p>
          <label class="field"><span>Move to</span>
            <select id="mig-target">
              ${targets.map((t, i) => `<option value="${esc(String(t.key))}"${i === 0 ? " selected" : ""}>${esc(versionLabel(t))}</option>`).join("")}
            </select>
          </label>
          <div id="mig-plan" class="mig-plan"><p class="muted" style="margin:0;font-size:12px">Reading the plan…</p></div>
          <label class="field" style="margin-top:10px"><span>Reason</span>
            <input id="mig-reason" placeholder="Why this instance is being moved" autocomplete="off"/>
          </label>
          <p class="muted" style="margin:6px 0 0;font-size:12px">Recorded with your name and the time, and shown on this
          instance's replay at the point it happened.</p>
          <p class="mig-err" style="margin:8px 0 0;font-size:12.5px" hidden></p>
        </div>
        <div class="modal-foot">
          <button class="btn neutral" data-mig-cancel title="Close without migrating">Cancel</button>
          <button class="btn" data-mig-go title="Move this instance to the selected version" disabled>Migrate</button>
        </div>
      </div>`;
    document.body.appendChild(ov);

    const targetSel = ov.querySelector("#mig-target");
    const planEl = ov.querySelector("#mig-plan");
    const reasonIn = ov.querySelector("#mig-reason");
    const goBtn = ov.querySelector("[data-mig-go]");
    const errEl = ov.querySelector(".mig-err");
    const byKey = new Map(targets.map((t) => [String(t.key), t]));

    let plan = null;
    let planSeq = 0;

    const close = (value) => { ov.remove(); resolve(value); };
    // A migration is only offered when the plan says it holds *and* a reason is given.
    // The button is the gate rather than a later error, because a refusal an operator
    // could have seen first is a round trip they should not have had to make.
    const sync = () => {
      goBtn.disabled = !(plan && plan.migratable && reasonIn.value.trim());
    };

    const loadPlan = async () => {
      const seq = ++planSeq;
      plan = null;
      sync();
      planEl.innerHTML = `<p class="muted" style="margin:0;font-size:12px">Reading the plan…</p>`;
      let got;
      try {
        got = await api("POST", `/api/v1/instances/${encodeURIComponent(instanceKey)}/migrate/plan`, {
          targetProcessDefKey: byKey.get(targetSel.value).key,
        });
      } catch (e) {
        if (seq !== planSeq) return; // a newer target was picked while this was in flight
        planEl.innerHTML = `<p class="mig-verdict bad">Could not read the plan: ${esc(migrateError(e))}</p>`;
        sync();
        return;
      }
      if (seq !== planSeq) return;
      plan = got;
      planEl.innerHTML = planHTML(got);
      sync();
    };

    targetSel.addEventListener("change", loadPlan);
    reasonIn.addEventListener("input", () => { errEl.hidden = true; sync(); });
    ov.querySelector("[data-mig-cancel]").addEventListener("click", () => close(null));
    ov.addEventListener("click", (e) => { if (e.target === ov) close(null); });
    ov.addEventListener("keydown", (e) => { if (e.key === "Escape") close(null); });
    goBtn.addEventListener("click", () => {
      const reason = reasonIn.value.trim();
      if (!reason) {
        errEl.textContent = "A reason is required — it is what makes the migration auditable.";
        errEl.hidden = false;
        return;
      }
      close({ target: byKey.get(targetSel.value), reason });
    });

    loadPlan();
    setTimeout(() => reasonIn.focus(), 0);
  });
}

// migrateProcessFlow moves every running instance of one deployed version onto another,
// in the bounded batches the server hands out. Each instance is its own command and its
// own event, so a refusal on one does not roll back the rest — which is why this reports
// both numbers rather than a single success.
//
// There is deliberately no dry run here: a plan is per-instance, and a batch's answer is
// its own refusal list, which the operator reads afterwards and works through one by
// one. `versions` is the process's deployed versions; `runningOf` answers how many
// instances a version has, so the picker can say which of them is worth draining.
export async function migrateProcessFlow({ api, toast, processId, processName, versions, runningOf, onDone }) {
  const deployed = (versions || []).slice().sort((a, b) => b.version - a.version);
  if (deployed.length < 2) {
    toast("Only one version of this process is deployed — deploy the fixed model first, then migrate.", "warn");
    return false;
  }
  const choice = await askBatchMigration({ processId, processName, versions: deployed, runningOf });
  if (!choice) return false;

  let migrated = 0;
  const refused = [];
  try {
    // The server caps each call and reports whether more are waiting, exactly as the
    // bulk terminate does. The guard is a backstop against a server that never stops
    // saying "remaining" — it bounds the loop, it is not the expected exit.
    for (let guard = 0; guard < 1000; guard++) {
      const res = await api("POST", `/api/v1/processes/${encodeURIComponent(choice.from.key)}/migrate-instances`, {
        targetProcessDefKey: choice.to.key,
        reason: choice.reason,
      });
      migrated += res.migrated || 0;
      for (const r of res.refused || []) refused.push(r);
      if (!res.remaining) break;
    }
  } catch (e) {
    toast(migrateError(e), "err");
    return false;
  }

  if (!migrated && !refused.length) {
    toast(`No running instances on ${versionLabel(choice.from)}`, "warn");
    return false;
  }
  if (refused.length) {
    // Both numbers, and the reasons: a batch that half-worked is the normal outcome
    // when a model changed enough to strand some tokens, and hiding the refusals would
    // leave those instances silently behind on the old version.
    await showBatchOutcome({ migrated, refused, from: choice.from, to: choice.to });
  } else {
    toast(`Migrated ${migrated} instance${migrated === 1 ? "" : "s"} to ${versionLabel(choice.to)}`, "ok");
  }
  if (onDone) await onDone();
  return true;
}

// askBatchMigration picks the two versions and the reason. Source first: draining a
// version is the act, and which one is holding instances is the thing an operator is
// looking at.
function askBatchMigration({ processId, processName, versions, runningOf }) {
  return new Promise((resolve) => {
    const running = (v) => (runningOf ? runningOf(v) : 0);
    const optionFor = (v) => {
      const n = running(v);
      return `<option value="${esc(String(v.key))}">${esc(versionLabel(v))}${n ? ` — ${n} running` : " — none running"}</option>`;
    };
    // Default: drain the oldest version that still holds instances onto the newest one,
    // which is the shape of the job nearly every time this is opened.
    const withRunning = versions.filter((v) => running(v) > 0);
    const defaultFrom = (withRunning.length ? withRunning[withRunning.length - 1] : versions[versions.length - 1]).key;

    const ov = document.createElement("div");
    ov.className = "modal-ov";
    ov.innerHTML = `
      <div class="modal confirm-modal mig-modal" role="dialog" aria-modal="true" aria-labelledby="migb-title">
        <div class="modal-head"><h2 id="migb-title">Migrate running instances &middot; ${esc(processName || processId)}</h2></div>
        <div class="modal-body">
          <p class="muted" style="margin:0 0 10px">Moves every running instance of one deployed version onto another. Each
          instance keeps its variables, its tasks and everything already done.</p>
          <div class="row" style="gap:10px;flex-wrap:wrap">
            <label class="field" style="flex:1 1 200px"><span>From</span>
              <select id="migb-from">${versions.map(optionFor).join("")}</select>
            </label>
            <label class="field" style="flex:1 1 200px"><span>To</span>
              <select id="migb-to">${versions.map(optionFor).join("")}</select>
            </label>
          </div>
          <p class="muted" style="margin:8px 0 0;font-size:12px">Each instance is checked on its own. Any that cannot be
          moved — a token on an element the new version does not have, an element that changed type — is left exactly where
          it is and listed afterwards, so nothing is half-applied.</p>
          <label class="field" style="margin-top:10px"><span>Reason</span>
            <input id="migb-reason" placeholder="Why these instances are being moved" autocomplete="off"/>
          </label>
          <p class="mig-err" style="margin:8px 0 0;font-size:12.5px" hidden></p>
        </div>
        <div class="modal-foot">
          <button class="btn neutral" data-migb-cancel title="Close without migrating">Cancel</button>
          <button class="btn" data-migb-go title="Move every running instance of the source version" disabled>Migrate instances</button>
        </div>
      </div>`;
    document.body.appendChild(ov);

    const fromSel = ov.querySelector("#migb-from");
    const toSel = ov.querySelector("#migb-to");
    const reasonIn = ov.querySelector("#migb-reason");
    const goBtn = ov.querySelector("[data-migb-go]");
    const errEl = ov.querySelector(".mig-err");
    const byKey = new Map(versions.map((v) => [String(v.key), v]));

    fromSel.value = String(defaultFrom);
    toSel.value = String(versions[0].key);

    const close = (value) => { ov.remove(); resolve(value); };
    const sync = () => {
      const same = fromSel.value === toSel.value;
      errEl.hidden = !same;
      if (same) errEl.textContent = "Pick two different versions — an instance cannot be migrated onto the version it is already on.";
      goBtn.disabled = same || !reasonIn.value.trim();
    };

    fromSel.addEventListener("change", sync);
    toSel.addEventListener("change", sync);
    reasonIn.addEventListener("input", sync);
    ov.querySelector("[data-migb-cancel]").addEventListener("click", () => close(null));
    ov.addEventListener("click", (e) => { if (e.target === ov) close(null); });
    ov.addEventListener("keydown", (e) => { if (e.key === "Escape") close(null); });
    goBtn.addEventListener("click", () => {
      const reason = reasonIn.value.trim();
      if (!reason || fromSel.value === toSel.value) { sync(); return; }
      close({ from: byKey.get(fromSel.value), to: byKey.get(toSel.value), reason });
    });

    sync();
    setTimeout(() => reasonIn.focus(), 0);
  });
}

// showBatchOutcome reports a batch that did not take every instance with it. It is a
// dialog rather than a toast because the refusals are a work list — each named instance
// is still on the old version and still needs a decision.
function showBatchOutcome({ migrated, refused, from, to }) {
  return new Promise((resolve) => {
    const rows = refused.map((r) => {
      const why = (r.problems || []).map((p) => `${p.elementId ? p.elementId + " " : ""}${p.reason}`).join("; ");
      return `<li><a href="#/operations/i/${esc(String(r.instanceKey))}" class="mono">${esc(String(r.instanceKey))}</a>
        <span class="muted">${esc(why || "could not be migrated")}</span></li>`;
    }).join("");
    const ov = document.createElement("div");
    ov.className = "modal-ov";
    ov.innerHTML = `
      <div class="modal confirm-modal mig-modal" role="dialog" aria-modal="true" aria-labelledby="migo-title">
        <div class="modal-head"><h2 id="migo-title">Migrated ${migrated} &middot; ${refused.length} left behind</h2></div>
        <div class="modal-body">
          <p style="margin:0 0 10px">${migrated} instance${migrated === 1 ? "" : "s"} moved from
          <b>${esc(versionLabel(from))}</b> to <b>${esc(versionLabel(to))}</b>.
          ${refused.length} could not be, and ${refused.length === 1 ? "is" : "are"} still running on
          <b>${esc(versionLabel(from))}</b> exactly as before:</p>
          <ul class="mig-problems mig-refused">${rows}</ul>
          <p class="muted" style="margin:8px 0 0;font-size:12px">Open one to see where its token is. A refusal is not a
          failure to apply — nothing was written for these, so they are unchanged rather than half-migrated.</p>
        </div>
        <div class="modal-foot">
          <button class="btn" data-migo-ok title="Close">Close</button>
        </div>
      </div>`;
    document.body.appendChild(ov);
    const close = () => { ov.remove(); resolve(); };
    ov.querySelector("[data-migo-ok]").addEventListener("click", close);
    ov.addEventListener("click", (e) => { if (e.target === ov) close(); });
    ov.addEventListener("keydown", (e) => { if (e.key === "Escape") close(); });
    setTimeout(() => ov.querySelector("[data-migo-ok]").focus(), 0);
  });
}
