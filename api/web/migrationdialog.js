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
//
// Every sentence goes through the locale catalogue under "mig.*" (ADR-0267): the
// parameters are escaped before they are put into a message that carries markup.
import { t as tr, plural as trPlural } from "./i18n.js";
import { migrationImpactHTML } from "./version-comparison.js";

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
  const problems = (plan.problems || []).map((p) => `<li><b>${esc(p.elementId || tr("mig.plan.anElement"))}</b> ${esc(p.reason)}</li>`).join("");
  const version = esc(String(plan.toVersion));
  const head = plan.migratable
    ? `<p class="mig-verdict ok">&#10003; ${tr("mig.plan.ok", { version })}</p>`
    : `<p class="mig-verdict bad">&#9888; ${tr(plan.problems && plan.problems.length ? "mig.plan.badWhy" : "mig.plan.bad", { version })}</p>`;
  const why = problems
    ? `<ul class="mig-problems">${problems}</ul>
       <p class="muted" style="margin:6px 0 0;font-size:12px">${esc(tr("mig.plan.nothingChanged"))}</p>`
    : "";
  const movedRows = s.moved.map((p) =>
    `<li><span class="mono">${esc(p.from)}</span> &rarr; <span class="mono">${esc(p.to)}</span></li>`).join("");
  const mapping = s.total
    ? `<div class="mig-map">
        <h4>${esc(tr("mig.map.title"))}</h4>
        ${s.moved.length
      ? `<p class="muted" style="margin:0 0 4px;font-size:12px">${esc(trPlural("mig.map.moved", s.moved.length))}</p>
             <ul class="mig-moved">${movedRows}</ul>`
      : ""}
        <p class="muted" style="margin:4px 0 0;font-size:12px">${esc(trPlural("mig.map.unchanged", s.unchanged))}</p>
      </div>`
    : `<p class="muted" style="margin:6px 0 0;font-size:12px">${esc(tr("mig.map.none"))}</p>`;
  return head + why + mapping;
}

// forkSummary reduces a fork plan to the sentence an operator needs before they commit:
// what crosses with the successor, and what ends with the predecessor. The second half is
// the one that matters — a fork cannot carry work in flight, and finding that out
// afterwards is finding out too late.
export function forkSummary(fork) {
  if (!fork) return null;
  const unmatched = (fork.parked || []).filter((p) => !p.resumeAt).map((p) => p.elementId);
  return {
    variables: fork.variables || 0,
    dataObjects: fork.dataObjects || 0,
    jobs: fork.jobs || 0,
    unmatched,
  };
}

// forkHTML renders the fork alternative: what it would do, where the work would pick up
// again, and what it would cost. It is shown beside the in-place plan rather than
// instead of it, because the two are different operations and the cheaper one is almost
// always right — a fork is what is left when the rebinding cannot hold
// (ADR-0389).
export function forkHTML(fork) {
  if (!fork) return "";
  const s = forkSummary(fork);
  const chosen = new Set(fork.resume || []);
  const candidates = fork.candidates || [];
  const boxes = candidates.length
    ? candidates.map((id) => `<label class="mig-resume-opt"><input type="checkbox" value="${esc(id)}"${
      chosen.has(id) ? " checked" : ""}/> <span class="mono">${esc(id)}</span></label>`).join("")
    : `<p class="muted" style="margin:0;font-size:12px">${esc(tr("mig.fork.noCandidates"))}</p>`;
  const stranded = s.unmatched.length
    ? `<p class="muted" style="margin:6px 0 0;font-size:12px">${
      trPlural("mig.fork.stranded", s.unmatched.length, { ids: s.unmatched.map(esc).join(", ") })}</p>`
    : "";
  const problems = (fork.problems || []).length
    ? `<ul class="mig-problems">${fork.problems.map((p) =>
      `<li>${p.elementId ? `<b>${esc(p.elementId)}</b> ` : ""}${esc(p.reason)}</li>`).join("")}</ul>`
    : "";
  return `
    <h4>${esc(tr("mig.fork.title"))}</h4>
    <p class="muted" style="margin:0 0 8px;font-size:12px">${tr("mig.fork.intro", { version: esc(String(fork.toVersion)) })}</p>
    <p class="muted" style="margin:0 0 8px;font-size:12px">${tr("mig.fork.crosses", {
    variables: esc(trPlural("mig.fork.variables", s.variables)),
    dataObjects: esc(trPlural("mig.fork.dataObjects", s.dataObjects)),
  })}
    ${tr("mig.fork.ends", { jobs: esc(trPlural("mig.fork.jobs", s.jobs)) })}</p>
    <div class="mig-resume">${boxes}</div>
    ${stranded}
    ${problems}`;
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
    toast(tr("mig.err.readVersions", { error: e && e.message ? e.message : String(e) }), "err");
    return false;
  }
  const targets = otherVersions(processes, processId, fromProcessDefKey);
  if (!targets.length) {
    // Not an error and not a dialog: there is genuinely nowhere to go, and saying so is
    // the whole answer.
    toast(tr("mig.onlyOne"), "warn");
    return false;
  }

  const choice = await askMigration({ api, instanceKey, processId, fromVersion, targets });
  if (!choice) return false;
  let got;
  try {
    got = choice.mode === "fork"
      ? await api("POST", `/api/v1/instances/${encodeURIComponent(instanceKey)}/migrate/fork`, {
        targetProcessDefKey: choice.target.key,
        reason: choice.reason,
        resume: choice.resume,
      })
      : await api("POST", `/api/v1/instances/${encodeURIComponent(instanceKey)}/migrate`, {
        targetProcessDefKey: choice.target.key,
        reason: choice.reason,
      });
  } catch (e) {
    toast(migrateError(e), "err");
    return false;
  }
  if (choice.mode === "fork") {
    // The successor's key is the thing an operator needs next: the work is there now,
    // and this is the only moment the connection is in front of them.
    const next = got && got.successorInstanceKey;
    toast(next
      ? tr("mig.forked", { key: next, version: versionLabel(choice.target) })
      : tr("mig.forkedNoKey", { version: versionLabel(choice.target) }), "ok");
  } else {
    toast(tr("mig.migrated", { version: versionLabel(choice.target) }), "ok");
  }
  if (onDone) await onDone();
  return true;
}

// migrateError turns a failed migration into something an operator can act on. The
// refusal case carries the plan, so the message names what is wrong rather than
// reporting a status code; the admin gate is worth naming too, since it is a
// configuration answer rather than a problem with the instance.
function migrateError(e) {
  const msg = (e && e.message) || String(e);
  if (e && e.status === 403) return tr("mig.err.forbidden");
  if (e && e.status === 409 && e.body && Array.isArray(e.body.problems) && e.body.problems.length) {
    const first = e.body.problems[0];
    const more = e.body.problems.length - 1;
    const problem = `${first.elementId ? first.elementId + " " : ""}${first.reason}`;
    return more > 0 ? tr("mig.err.refusedMore", { problem, more }) : tr("mig.err.refused", { problem });
  }
  return tr("mig.err.failed", { error: msg });
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
        <div class="modal-head"><h2 id="mig-title">${esc(tr("mig.one.title", { key: String(instanceKey) }))}</h2></div>
        <div class="modal-body">
          <p class="muted" style="margin:0 0 10px">${tr("mig.one.intro", { version: esc(String(fromVersion)), process: esc(processId) })}</p>
          <label class="field"><span>${esc(tr("mig.one.target"))}</span>
            <select id="mig-target">
              ${targets.map((t, i) => `<option value="${esc(String(t.key))}"${i === 0 ? " selected" : ""}>${esc(versionLabel(t))}</option>`).join("")}
            </select>
          </label>
          <div id="mig-plan" class="mig-plan"><p class="muted" style="margin:0;font-size:12px">${esc(tr("mig.one.readingPlan"))}</p></div>
          <div id="mig-impact" class="mig-impact" hidden></div>
          <div id="mig-fork" class="mig-fork" hidden></div>
          <label class="field" style="margin-top:10px"><span>${esc(tr("mig.reason"))}</span>
            <input id="mig-reason" placeholder="${esc(tr("mig.one.reasonPlaceholder"))}" autocomplete="off"/>
          </label>
          <p class="muted" style="margin:6px 0 0;font-size:12px">${esc(tr("mig.one.recorded"))}</p>
          <p class="mig-err" style="margin:8px 0 0;font-size:12.5px" hidden></p>
        </div>
        <div class="modal-foot">
          <button class="btn neutral" data-mig-cancel title="${esc(tr("mig.cancelTitle"))}">${esc(tr("mig.cancel"))}</button>
          <button class="btn neutral" data-mig-fork title="${esc(tr("mig.one.forkTitle"))}" hidden disabled>${esc(tr("mig.one.fork"))}</button>
          <button class="btn" data-mig-go title="${esc(tr("mig.one.goTitle"))}" disabled>${esc(tr("mig.one.go"))}</button>
        </div>
      </div>`;
    document.body.appendChild(ov);

    const targetSel = ov.querySelector("#mig-target");
    const planEl = ov.querySelector("#mig-plan");
    const forkEl = ov.querySelector("#mig-fork");
    const impactEl = ov.querySelector("#mig-impact");
    const reasonIn = ov.querySelector("#mig-reason");
    const goBtn = ov.querySelector("[data-mig-go]");
    const forkBtn = ov.querySelector("[data-mig-fork]");
    const errEl = ov.querySelector(".mig-err");
    const byKey = new Map(targets.map((t) => [String(t.key), t]));

    let plan = null;
    let planSeq = 0;

    const close = (value) => { ov.remove(); resolve(value); };
    // Which elements the fork would resume at: the server's proposal, as the operator
    // has since ticked it. Every option offered came from the plan's candidates, which
    // the server already validated, so this needs no second opinion here.
    const chosenResume = () =>
      Array.from(forkEl.querySelectorAll("input[type=checkbox]:checked")).map((el) => el.value);
    // A migration is only offered when the plan says it holds *and* a reason is given.
    // The button is the gate rather than a later error, because a refusal an operator
    // could have seen first is a round trip they should not have had to make. The fork
    // gates on the same reason plus somewhere to resume, and is only offered at all once
    // the plan has said what it would cost.
    const sync = () => {
      const reason = reasonIn.value.trim();
      goBtn.disabled = !(plan && plan.migratable && reason);
      const fork = plan && plan.fork;
      forkBtn.hidden = !fork;
      forkBtn.disabled = !(fork && reason && chosenResume().length);
    };

    const loadPlan = async () => {
      const seq = ++planSeq;
      plan = null;
      sync();
      impactEl.hidden = true;
      planEl.innerHTML = `<p class="muted" style="margin:0;font-size:12px">${esc(tr("mig.one.readingPlan"))}</p>`;
      let got;
      try {
        got = await api("POST", `/api/v1/instances/${encodeURIComponent(instanceKey)}/migrate/plan`, {
          targetProcessDefKey: byKey.get(targetSel.value).key,
        });
      } catch (e) {
        if (seq !== planSeq) return; // a newer target was picked while this was in flight
        planEl.innerHTML = `<p class="mig-verdict bad">${esc(tr("mig.one.planFailed", { error: migrateError(e) }))}</p>`;
        sync();
        return;
      }
      if (seq !== planSeq) return;
      plan = got;
      planEl.innerHTML = planHTML(got);
      // What the target version changes, and at the instance's own positions first
      // (ADR-draft-compare-process-versions). The plan carries no "same model" flag: an
      // empty list is read as one, which holds for everything the engine reads — the
      // list leaves out only what the compiler ignores.
      if (Array.isArray(got.changes)) {
        impactEl.innerHTML = migrationImpactHTML({
          cmp: { changes: got.changes, counts: got.counts, sameModel: got.changes.length === 0 },
          at: new Map((got.at || []).map((id) => [id, null])),
          from: got.fromVersion, to: got.toVersion,
        });
        impactEl.hidden = false;
      }
      forkEl.innerHTML = forkHTML(got.fork);
      forkEl.hidden = !got.fork;
      // Dimmed while the instance could simply be rebound: the fork is the answer to a
      // refusal, and an operator who has one should not be invited to end an instance.
      forkEl.classList.toggle("alt", !!got.migratable);
      sync();
    };

    targetSel.addEventListener("change", loadPlan);
    forkEl.addEventListener("change", sync);
    reasonIn.addEventListener("input", () => { errEl.hidden = true; sync(); });
    ov.querySelector("[data-mig-cancel]").addEventListener("click", () => close(null));
    ov.addEventListener("click", (e) => { if (e.target === ov) close(null); });
    ov.addEventListener("keydown", (e) => { if (e.key === "Escape") close(null); });
    goBtn.addEventListener("click", () => {
      const reason = reasonIn.value.trim();
      if (!reason) {
        errEl.textContent = tr("mig.one.reasonRequired");
        errEl.hidden = false;
        return;
      }
      close({ mode: "migrate", target: byKey.get(targetSel.value), reason });
    });
    forkBtn.addEventListener("click", () => {
      const reason = reasonIn.value.trim();
      if (!reason) {
        errEl.textContent = tr("mig.one.reasonRequiredFork");
        errEl.hidden = false;
        return;
      }
      close({ mode: "fork", target: byKey.get(targetSel.value), reason, resume: chosenResume() });
    });

    loadPlan();
    setTimeout(() => reasonIn.focus(), 0);
  });
}

// runningByDefinition keeps the per-*version* running counts a process-level rollup
// sums away, keyed by the definition key as a string. Migrating drains one deployed
// version onto another, so its picker has to say which version is actually holding
// instances — a choice the summed number cannot answer (ADR-0162). `rows` is what
// GET /api/v1/instances/summary answers.
export function runningByDefinition(rows) {
  const byDef = new Map();
  for (const r of rows || []) byDef.set(String(r.processDefKey), r.active || 0);
  return byDef;
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
// `fromKey` preselects the source: a caller that is showing one version — the live
// view — has already said which version it means, and the picker must not second-guess
// it.
export async function migrateProcessFlow({ api, toast, processId, processName, versions, runningOf, fromKey, onDone }) {
  const deployed = (versions || []).slice().sort((a, b) => b.version - a.version);
  if (deployed.length < 2) {
    toast(tr("mig.onlyOne"), "warn");
    return false;
  }
  const choice = await askBatchMigration({ api, processId, processName, versions: deployed, runningOf, fromKey });
  if (!choice) return false;

  let migrated = 0;
  const refused = [];
  const route = `/api/v1/processes/${encodeURIComponent(choice.from.key)}/migrate-instances`;
  try {
    // The server caps each call and says whether more are waiting, and where the next
    // call continues: past the last instance this one looked at. Without that cursor a
    // refused instance — which stays where it is — is selected again by every call,
    // and a page of them is all the walk ever sees. The guard is a backstop against a
    // server that never stops saying "remaining"; it bounds the loop, it is not the
    // expected exit.
    let after = "";
    for (let guard = 0; guard < 1000; guard++) {
      const res = await api("POST", after ? `${route}?after=${encodeURIComponent(after)}` : route, {
        targetProcessDefKey: choice.to.key,
        reason: choice.reason,
      });
      migrated += res.migrated || 0;
      for (const r of res.refused || []) refused.push(r);
      if (!res.remaining) break;
      // A call that says more remain without saying where to continue cannot be
      // repeated safely: from the front it would meet the same refusals again.
      if (!res.nextCursor || res.nextCursor === after) break;
      after = res.nextCursor;
      // Tens of thousands of instances are a hundred calls or more, and a dialog that
      // has closed with nothing on screen for that long reads as a hang.
      toast(refused.length
        ? tr("mig.batch.progressLeft", { version: versionLabel(choice.to), moved: migrated, left: refused.length })
        : tr("mig.batch.progress", { version: versionLabel(choice.to), moved: migrated }));
    }
  } catch (e) {
    // What moved before the failure has moved — each instance is its own event — so
    // the count is part of the message, and the caller's view is re-read either way.
    toast(migrated
      ? trPlural("mig.batch.failedAfter", migrated, { error: migrateError(e) })
      : migrateError(e), "err");
    if (migrated && onDone) await onDone();
    return false;
  }

  if (!migrated && !refused.length) {
    toast(tr("mig.batch.none", { version: versionLabel(choice.from) }), "warn");
    return false;
  }
  if (refused.length) {
    // Both numbers, and the reasons: a batch that half-worked is the normal outcome
    // when a model changed enough to strand some tokens, and hiding the refusals would
    // leave those instances silently behind on the old version.
    await showBatchOutcome({ migrated, refused, from: choice.from, to: choice.to });
  } else {
    toast(trPlural("mig.batch.done", migrated, { version: versionLabel(choice.to) }), "ok");
  }
  if (onDone) await onDone();
  return true;
}

// askBatchMigration picks the two versions and the reason. Source first: draining a
// version is the act, and which one is holding instances is the thing an operator is
// looking at.
function askBatchMigration({ api, processId, processName, versions, runningOf, fromKey }) {
  return new Promise((resolve) => {
    const running = (v) => (runningOf ? runningOf(v) : 0);
    // Without counts the options say nothing about them: "none running" on every
    // version would be a claim, and a false one exactly when the counts could not be read.
    const optionFor = (v) => {
      const n = running(v);
      const label = !runningOf
        ? versionLabel(v)
        : n ? trPlural("mig.batch.optionRunning", n, { version: versionLabel(v) }) : tr("mig.batch.optionNone", { version: versionLabel(v) });
      return `<option value="${esc(String(v.key))}">${esc(label)}</option>`;
    };
    // Default: the version the caller named, else drain the oldest version that still
    // holds instances onto the newest one, which is the shape of the job nearly every
    // time this is opened from the overview. The target stays the newest either way: when
    // that is the source too, the dialog asks for a target rather than proposing to move
    // instances back to an older version on the operator's behalf.
    const withRunning = versions.filter((v) => running(v) > 0);
    const named = fromKey != null && versions.find((v) => String(v.key) === String(fromKey));
    const defaultFrom = named
      ? named.key
      : (withRunning.length ? withRunning[withRunning.length - 1] : versions[versions.length - 1]).key;

    const ov = document.createElement("div");
    ov.className = "modal-ov";
    ov.innerHTML = `
      <div class="modal confirm-modal mig-modal" role="dialog" aria-modal="true" aria-labelledby="migb-title">
        <div class="modal-head"><h2 id="migb-title">${esc(tr("mig.batch.title", { process: processName || processId }))}</h2></div>
        <div class="modal-body">
          <p class="muted" style="margin:0 0 10px">${esc(tr("mig.batch.intro"))}</p>
          <div class="row" style="gap:10px;flex-wrap:wrap">
            <label class="field" style="flex:1 1 200px"><span>${esc(tr("mig.batch.from"))}</span>
              <select id="migb-from">${versions.map(optionFor).join("")}</select>
            </label>
            <label class="field" style="flex:1 1 200px"><span>${esc(tr("mig.batch.to"))}</span>
              <select id="migb-to">${versions.map(optionFor).join("")}</select>
            </label>
          </div>
          <p class="muted" style="margin:8px 0 0;font-size:12px">${esc(tr("mig.batch.checked"))}</p>
          <div id="migb-impact" class="mig-impact" hidden></div>
          <label class="field" style="margin-top:10px"><span>${esc(tr("mig.reason"))}</span>
            <input id="migb-reason" placeholder="${esc(tr("mig.batch.reasonPlaceholder"))}" autocomplete="off"/>
          </label>
          <p class="mig-err" style="margin:8px 0 0;font-size:12.5px" hidden></p>
        </div>
        <div class="modal-foot">
          <button class="btn neutral" data-migb-cancel title="${esc(tr("mig.cancelTitle"))}">${esc(tr("mig.cancel"))}</button>
          <button class="btn" data-migb-go title="${esc(tr("mig.batch.goTitle"))}" disabled>${esc(tr("mig.batch.go"))}</button>
        </div>
      </div>`;
    document.body.appendChild(ov);

    const fromSel = ov.querySelector("#migb-from");
    const toSel = ov.querySelector("#migb-to");
    const reasonIn = ov.querySelector("#migb-reason");
    const goBtn = ov.querySelector("[data-migb-go]");
    const errEl = ov.querySelector(".mig-err");
    const impactEl = ov.querySelector("#migb-impact");
    const byKey = new Map(versions.map((v) => [String(v.key), v]));

    // What the target version changes, and where the source version's instances stand
    // on a changed or removed element (ADR-draft-compare-process-versions): the
    // comparison of the two versions beside the source's live token counts. Read
    // afresh for every pair, and only the newest reading is shown.
    let impactSeq = 0;
    const loadImpact = async () => {
      const mine = ++impactSeq;
      const from = byKey.get(fromSel.value);
      const to = byKey.get(toSel.value);
      if (!api || !from || !to || from === to) {
        impactEl.hidden = true;
        return;
      }
      impactEl.hidden = false;
      impactEl.innerHTML = `<p class="muted" style="margin:0;font-size:12px">${esc(tr("cmp.loading"))}</p>`;
      const [cmp, counts] = await Promise.all([
        api("GET", `/api/v1/processes/${encodeURIComponent(to.key)}/comparison?with=${encodeURIComponent(from.key)}`).catch(() => null),
        api("GET", `/api/v1/processes/${encodeURIComponent(from.key)}/runtime/counts`).catch(() => null),
      ]);
      if (mine !== impactSeq || !ov.isConnected) return;
      if (!cmp || typeof cmp !== "object" || Array.isArray(cmp)) {
        impactEl.innerHTML = `<p class="muted" style="margin:0;font-size:12px">${esc(tr("cmp.impact.failed"))}</p>`;
        return;
      }
      // Counts that could not be read are not "no instance anywhere": the list is then
      // shown whole, and the dialog says why.
      const at = counts && Array.isArray(counts.elements)
        ? new Map(counts.elements
          .filter((e) => e.tokens > 0 && e.elementId !== processId)
          .map((e) => [e.elementId, e.tokens]))
        : null;
      impactEl.innerHTML = migrationImpactHTML({ cmp, at, from: from.version, to: to.version });
    };

    fromSel.value = String(defaultFrom);
    toSel.value = String(versions[0].key);

    const close = (value) => { ov.remove(); resolve(value); };
    const sync = () => {
      const same = fromSel.value === toSel.value;
      errEl.hidden = !same;
      if (same) errEl.textContent = tr("mig.batch.same");
      goBtn.disabled = same || !reasonIn.value.trim();
    };

    fromSel.addEventListener("change", () => { sync(); loadImpact(); });
    toSel.addEventListener("change", () => { sync(); loadImpact(); });
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
    loadImpact();
    setTimeout(() => reasonIn.focus(), 0);
  });
}

// showBatchOutcome reports a batch that did not take every instance with it. It is a
// dialog rather than a toast because the refusals are a work list — each named instance
// is still on the old version and still needs a decision.
// How many refusals the outcome lists by name. A model change that strands every token
// refuses every instance, and fifty thousand rows is not a work list anyone reads — it
// is a page the browser struggles to draw. Past this the dialog says how many more there
// are and where they all are: still on the source version, which lists them.
export const OUTCOME_LIST_MAX = 200;

function showBatchOutcome({ migrated, refused, from, to }) {
  return new Promise((resolve) => {
    const listed = refused.slice(0, OUTCOME_LIST_MAX);
    const unlisted = refused.length - listed.length;
    const rows = listed.map((r) => {
      const why = (r.problems || []).map((p) => `${p.elementId ? p.elementId + " " : ""}${p.reason}`).join("; ");
      return `<li><a href="#/operations/i/${esc(String(r.instanceKey))}" class="mono">${esc(String(r.instanceKey))}</a>
        <span class="muted">${esc(why || tr("mig.out.couldNot"))}</span></li>`;
    }).join("");
    const ov = document.createElement("div");
    ov.className = "modal-ov";
    ov.innerHTML = `
      <div class="modal confirm-modal mig-modal" role="dialog" aria-modal="true" aria-labelledby="migo-title">
        <div class="modal-head"><h2 id="migo-title">${esc(tr("mig.out.title", { migrated, refused: refused.length }))}</h2></div>
        <div class="modal-body">
          <p style="margin:0 0 10px">${trPlural("mig.out.moved", migrated, { from: esc(versionLabel(from)), to: esc(versionLabel(to)) })}
          ${trPlural("mig.out.stayed", refused.length, { from: esc(versionLabel(from)) })}</p>
          <ul class="mig-problems mig-refused">${rows}</ul>
          ${unlisted ? `<p class="mig-unlisted" style="margin:6px 0 0;font-size:12.5px">${
    tr("mig.out.more", { n: unlisted, from: esc(versionLabel(from)) })}</p>` : ""}
          <p class="muted" style="margin:8px 0 0;font-size:12px">${esc(tr("mig.out.note"))}</p>
        </div>
        <div class="modal-foot">
          <button class="btn" data-migo-ok title="${esc(tr("mig.out.close"))}">${esc(tr("mig.out.close"))}</button>
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
