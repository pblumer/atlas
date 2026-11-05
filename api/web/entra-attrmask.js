// entra-attrmask.js — a per-operation "attribute capture mask" for the Microsoft
// Entra ID Worker Type (ADR-0172). It replaces the raw `attributes` JSON textarea
// for the body-carrying operations with a small form of the important fields, plus
// a "Weitere Attribute (JSON)" escape hatch for anything the mask does not name.
//
// Weg A (ADR-draft): the mask is UI-only. It reads and writes the *same* single
// `attributes` JSON string the compiler already consumes — nothing new is persisted,
// no moddle or compiler change. Every field is text, so a FEEL value (a string
// beginning with "=") works in any field exactly as the inline JSON allowed; the
// build/parse pair does the type coercion (a boolean field's "true"/"false" become
// real JSON booleans, so Graph gets a boolean and not the string "true").

import { t } from "./i18n.js";

// ENTRA_ATTR_SPECS names, per operation, the fields the mask shows and how each maps
// into the request body. `kind`:
//   text  — a plain string value (literal or =FEEL); stored as a JSON string.
//   bool  — "true"/"false" become JSON booleans; "=expr" stays a FEEL string; ""
//           omits the key. So accountEnabled can be a literal or =profil.accountEnabled.
//   password — like text, but the field feeds passwordProfile.password and pairs with
//           the forceChange bool into a nested passwordProfile object.
//   unified — a bool that, when true, emits groupTypes:["Unified"] (a Microsoft-365
//           group); omitted otherwise.
//   select — one of a fixed set of `options` (stored as that JSON string); the empty
//           choice omits the key. A value already in the body that is not one of the
//           options (a hand-authored =FEEL, say) is offered as a further choice, so
//           opening the mask never loses it.
// A field with `nested:"passwordProfile"` is assembled into that sub-object.
export const ENTRA_ATTR_SPECS = {
  "create-user": [
    { key: "displayName", label: "pnl.ops.entra.field.displayName", kind: "text", hint: "pnl.ops.entra.hint.create-user.displayName" },
    { key: "mailNickname", label: "pnl.ops.entra.field.mailNickname", kind: "text", hint: "pnl.ops.entra.hint.create-user.mailNickname" },
    { key: "userPrincipalName", label: "pnl.ops.entra.field.userPrincipalName", kind: "text", hint: "pnl.ops.entra.hint.create-user.userPrincipalName" },
    { key: "password", label: "pnl.ops.entra.field.password", kind: "password", nested: "passwordProfile", hint: "pnl.ops.entra.hint.create-user.password" },
    { key: "forceChange", label: "pnl.ops.entra.field.forceChange", kind: "bool", nested: "passwordProfile", nestedKey: "forceChangePasswordNextSignIn", hint: "pnl.ops.entra.hint.create-user.forceChange" },
    { key: "accountEnabled", label: "pnl.ops.entra.field.accountEnabled", kind: "bool", hint: "pnl.ops.entra.hint.create-user.accountEnabled" },
    { key: "usageLocation", label: "pnl.ops.entra.field.usageLocation", kind: "text", hint: "pnl.ops.entra.hint.create-user.usageLocation" },
  ],
  "update-user": [
    { key: "displayName", label: "pnl.ops.entra.field.displayName", kind: "text" },
    { key: "jobTitle", label: "pnl.ops.entra.field.jobTitle", kind: "text", hint: "pnl.ops.entra.hint.update-user.jobTitle" },
    { key: "department", label: "pnl.ops.entra.field.department", kind: "text", hint: "pnl.ops.entra.hint.update-user.department" },
    { key: "accountEnabled", label: "pnl.ops.entra.field.accountEnabled", kind: "bool", hint: "pnl.ops.entra.hint.update-user.accountEnabled" },
    { key: "usageLocation", label: "pnl.ops.entra.field.usageLocation", kind: "text" },
  ],
  "create-group": [
    { key: "displayName", label: "pnl.ops.entra.field.displayName", kind: "text" },
    { key: "mailNickname", label: "pnl.ops.entra.field.mailNickname", kind: "text" },
    { key: "mailEnabled", label: "pnl.ops.entra.field.mailEnabled", kind: "bool", hint: "pnl.ops.entra.hint.create-group.mailEnabled" },
    { key: "securityEnabled", label: "pnl.ops.entra.field.securityEnabled", kind: "bool", hint: "pnl.ops.entra.hint.create-group.securityEnabled" },
    { key: "unified", label: "pnl.ops.entra.field.unified", kind: "unified", hint: "pnl.ops.entra.hint.create-group.unified" },
  ],
  "update-group": [
    { key: "displayName", label: "pnl.ops.entra.field.displayName", kind: "text" },
    { key: "description", label: "pnl.ops.entra.field.description", kind: "text", hint: "pnl.ops.entra.hint.update-group.description" },
  ],
  "create-channel": [
    { key: "displayName", label: "pnl.ops.entra.field.displayName", kind: "text", hint: "pnl.ops.entra.hint.create-channel.displayName" },
    { key: "description", label: "pnl.ops.entra.field.description", kind: "text" },
  ],
  "assign-role": [
    { key: "roleDefinitionId", label: "pnl.ops.entra.field.roleDefinitionId", kind: "text", hint: "pnl.ops.entra.hint.assign-role.roleDefinitionId" },
  ],
  // invitedUserEmailAddress is the one property Graph cannot do without (its hint says
  // so); inviteRedirectUrl is required by Graph too, but the worker defaults it to
  // https://myapps.microsoft.com when left empty, so the mask leaves it optional.
  "invite-guest": [
    { key: "invitedUserEmailAddress", label: "pnl.ops.entra.field.invitedUserEmailAddress", kind: "text", hint: "pnl.ops.entra.hint.invite-guest.invitedUserEmailAddress" },
    { key: "invitedUserDisplayName", label: "pnl.ops.entra.field.invitedUserDisplayName", kind: "text", hint: "pnl.ops.entra.hint.invite-guest.invitedUserDisplayName" },
    { key: "inviteRedirectUrl", label: "pnl.ops.entra.field.inviteRedirectUrl", kind: "text", hint: "pnl.ops.entra.hint.invite-guest.inviteRedirectUrl" },
    { key: "sendInvitationMessage", label: "pnl.ops.entra.field.sendInvitationMessage", kind: "bool", hint: "pnl.ops.entra.hint.invite-guest.sendInvitationMessage" },
  ],
  // A Viva Engage community: displayName is the one property Graph requires (its hint
  // says so), privacy is Graph's public/private. The owner is not a field here — the
  // task's User is bound as owners@odata.bind by the worker.
  "create-community": [
    { key: "displayName", label: "pnl.ops.entra.field.displayName", kind: "text", hint: "pnl.ops.entra.hint.create-community.displayName" },
    { key: "description", label: "pnl.ops.entra.field.description", kind: "text", hint: "pnl.ops.entra.hint.create-community.description" },
    { key: "privacy", label: "pnl.ops.entra.field.privacy", kind: "select", options: ["public", "private"], hint: "pnl.ops.entra.hint.create-community.privacy" },
  ],
  // The source of authority of a synchronised user: one property, and the worker
  // refuses any other (ADR-0172, amended 2026-11-04).
  "set-sync-behavior": [
    { key: "isCloudManaged", label: "pnl.ops.entra.field.isCloudManaged", kind: "bool", hint: "pnl.ops.entra.hint.set-sync-behavior.isCloudManaged" },
  ],
};

// hasMask reports whether an operation has a structured mask. Operations without one
// (assign-license's addLicenses/removeLicenses arrays, say) keep the raw JSON editor.
export function hasMask(operation) {
  return Object.prototype.hasOwnProperty.call(ENTRA_ATTR_SPECS, operation);
}

// entraResultShape describes, per operation, what the result variable receives — the
// gap the Modeler had: it never told an author the shape of `konto`, which differs by
// operation. Used in the operation-aware Output hint so =konto.id can be authored with
// confidence. A body-less operation (Graph answers 204) says so plainly.
export function entraResultShape(operation) {
  switch (operation) {
    case "get-user":
      return t("pnl.ops.entra.result.getUser");
    case "get-user-manager":
      return t("pnl.ops.entra.result.getUserManager");
    case "create-user":
    case "update-user":
      return t("pnl.ops.entra.result.user");
    case "list-users":
      return t("pnl.ops.entra.result.listUsers");
    case "create-group":
    case "get-group":
    case "update-group":
      return t("pnl.ops.entra.result.group");
    case "list-groups":
      return t("pnl.ops.entra.result.listGroups");
    case "list-deleted-groups":
      return t("pnl.ops.entra.result.listDeletedGroups");
    case "list-user-groups":
      return t("pnl.ops.entra.result.listUserGroups");
    case "list-subscribed-skus":
      return t("pnl.ops.entra.result.listSubscribedSkus");
    case "restore-group":
      return t("pnl.ops.entra.result.restoreGroup");
    case "restore-user":
      return t("pnl.ops.entra.result.restoreUser");
    case "invite-guest":
      return t("pnl.ops.entra.result.inviteGuest");
    case "create-community":
      return t("pnl.ops.entra.result.createCommunity");
    case "get-sync-behavior":
      return t("pnl.ops.entra.result.syncBehavior");
    case "delta-users":
    case "delta-groups":
      return t("pnl.ops.entra.result.delta");
    case "create-team":
      return t("pnl.ops.entra.result.createTeam");
    case "create-channel":
      return t("pnl.ops.entra.result.createChannel");
    case "assign-license":
    case "assign-role":
    case "enable":
    case "disable":
    case "delete-user":
    case "delete-group":
    case "reset-password":
    case "add-group-member":
    case "remove-group-member":
    case "add-group-owner":
    case "remove-group-owner":
    case "add-team-member":
    case "add-team-owner":
    case "archive-team":
    case "set-sync-behavior":
    case "permanently-delete-user":
      return t("pnl.ops.entra.result.none");
    default:
      return t("pnl.ops.entra.result.default");
  }
}

// entraResultType is entraResultShape's machine-readable half: the *type* the result
// variable receives, for the Modeler's Variables panel, where the prose above would not
// fit and a badge does. An operation Graph answers 204 to writes nothing, so it gets no
// type rather than a wrong one — the same reason the placement badge stays silent when
// the server says nothing.
export function entraResultType(operation) {
  switch (operation) {
    case "list-users":
    case "list-groups":
    case "list-deleted-groups":
    case "list-user-groups":
    case "list-subscribed-skus":
      return "array";
    case "assign-license":
    case "assign-role":
    case "enable":
    case "disable":
    case "delete-user":
    case "delete-group":
    case "reset-password":
    case "add-group-member":
    case "remove-group-member":
    case "add-group-owner":
    case "remove-group-owner":
    case "add-team-member":
    case "add-team-owner":
    case "archive-team":
    case "set-sync-behavior":
    case "permanently-delete-user":
      return "";
    default:
      return "object";
  }
}

// specKeys returns the set of top-level JSON keys a spec manages, so parse can split
// the object into mask-owned keys and the "extra" remainder shown in the escape hatch.
function specKeys(spec) {
  const keys = new Set();
  for (const f of spec) {
    if (f.nested) keys.add(f.nested);
    else if (f.kind === "unified") keys.add("groupTypes");
    else keys.add(f.key);
  }
  return keys;
}

// boolToField turns a parsed JSON value into a mask field's text form: a real boolean
// becomes "true"/"false"; a FEEL string (or anything else) is shown verbatim.
function boolToField(v) {
  if (v === true) return "true";
  if (v === false) return "false";
  if (v == null) return "";
  return String(v);
}

// fieldToBool coerces a bool field's text back to JSON: "true"/"false" → booleans, a
// leading "=" (or any other non-empty value) stays a string so a FEEL boolean works,
// "" means "omit the key".
function fieldToBool(s) {
  const t = (s || "").trim();
  if (t === "") return undefined;
  if (t === "true") return true;
  if (t === "false") return false;
  return t; // =expr or a literal the author typed
}

// parseAttributes splits an `attributes` JSON string into the mask's field values and
// the leftover "extra" object (everything the mask does not own), pretty-printed for
// the escape hatch. A string that is not a JSON object (empty, malformed, or authored
// as something exotic) yields empty fields and is handed to the escape hatch verbatim,
// so the mask never silently drops what it cannot parse.
export function parseAttributes(jsonStr, spec) {
  const fields = {};
  let obj = null;
  const raw = (jsonStr || "").trim();
  if (raw !== "") {
    try {
      const parsed = JSON.parse(raw);
      if (parsed && typeof parsed === "object" && !Array.isArray(parsed)) obj = parsed;
    } catch (_) { /* not an object — fall through to raw escape hatch */ }
  }
  if (obj === null) {
    return { fields, extra: raw === "" ? "" : raw };
  }
  const owned = specKeys(spec);
  for (const f of spec) {
    if (f.nested) {
      const sub = obj[f.nested];
      if (sub && typeof sub === "object") {
        const nk = f.nestedKey || f.key;
        if (f.kind === "bool") fields[f.key] = boolToField(sub[nk]);
        else if (nk in sub) fields[f.key] = sub[nk] == null ? "" : String(sub[nk]);
      }
    } else if (f.kind === "unified") {
      const gt = obj.groupTypes;
      fields[f.key] = Array.isArray(gt) && gt.indexOf("Unified") !== -1 ? "true" : "";
    } else if (f.kind === "bool") {
      fields[f.key] = boolToField(obj[f.key]);
    } else {
      fields[f.key] = f.key in obj ? (obj[f.key] == null ? "" : String(obj[f.key])) : "";
    }
  }
  const extra = {};
  for (const k of Object.keys(obj)) if (!owned.has(k)) extra[k] = obj[k];
  return { fields, extra: Object.keys(extra).length ? JSON.stringify(extra, null, 2) : "" };
}

// buildAttributes assembles the mask fields and the escape-hatch JSON back into one
// `attributes` object, mask fields winning over the extra object on a key clash. It
// returns a JSON string, or "" when nothing is set (so an untouched task stays blank
// and the compiler's "needs a body" check still speaks). A malformed escape hatch is
// preserved by throwing, so the caller can surface it rather than silently dropping it.
export function buildAttributes(fields, spec, extraStr) {
  const out = {};
  const extra = (extraStr || "").trim();
  if (extra !== "") {
    const parsed = JSON.parse(extra); // may throw — caller decides how to surface
    if (parsed && typeof parsed === "object" && !Array.isArray(parsed)) Object.assign(out, parsed);
  }
  const nested = {};
  for (const f of spec) {
    const val = (fields[f.key] || "").trim();
    if (f.nested) {
      if (val === "" && f.kind !== "bool") continue;
      const nk = f.nestedKey || f.key;
      const coerced = f.kind === "bool" ? fieldToBool(val) : val;
      if (coerced === undefined) continue;
      (nested[f.nested] = nested[f.nested] || {})[nk] = coerced;
    } else if (f.kind === "unified") {
      if (val === "true") out.groupTypes = ["Unified"];
    } else if (f.kind === "bool") {
      const b = fieldToBool(val);
      if (b !== undefined) out[f.key] = b;
    } else if (val !== "") {
      out[f.key] = val;
    }
  }
  for (const k of Object.keys(nested)) {
    // A passwordProfile with only forceChange and no password is not a valid body;
    // drop it rather than emit a half object.
    if (k === "passwordProfile" && !("password" in nested[k])) continue;
    out[k] = nested[k];
  }
  return Object.keys(out).length ? JSON.stringify(out) : "";
}

// attachEntraAttributeMask renders the mask into `host` for the current operation and
// keeps it in sync with the underlying textarea (`ta`), whose value is the canonical
// `attributes` JSON string the panel saves. getOperation() returns the live operation
// (it can change via the operation <select>'s reRender). onChange is called after every
// edit so the panel persists (saveKindFields → upsertExt). Returns { destroy }.
//
// The textarea stays in the DOM but hidden: it remains the single value the save path
// reads, and the mask simply writes assembled JSON into it. Anything the mask cannot
// parse (a hand-authored non-object body) leaves the fields empty and rides along in
// the escape hatch, so switching an existing raw-JSON task to the mask never loses it.
export function attachEntraAttributeMask(host, ta, getOperation, onChange, esc) {
  const escFn = esc || ((s) => String(s).replace(/[&<>"]/g, (c) => ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;" }[c])));
  const op = getOperation();
  const spec = ENTRA_ATTR_SPECS[op];
  if (!spec) return { destroy() {} };

  const state = parseAttributes(ta.value, spec);

  const wrap = document.createElement("div");
  wrap.className = "entra-mask";
  let html = "";
  for (const f of spec) {
    const id = "f-emask-" + f.key;
    const cur = state.fields[f.key] || "";
    let control;
    if (f.kind === "select") {
      // The authored value stays choosable even when it is none of the options.
      const opts = ["", ...f.options];
      if (cur !== "" && opts.indexOf(cur) === -1) opts.push(cur);
      control = `<select id="${id}">` +
        opts.map((o) => `<option value="${escFn(o)}"${o === cur ? " selected" : ""}>${escFn(o === "" ? "—" : o)}</option>`).join("") +
        `</select>`;
    } else {
      control = `<input id="${id}" type="text" value="${escFn(cur)}" ` +
        `placeholder="${escFn(f.kind === "bool" ? t("pnl.ops.entra.boolPlaceholder") : "")}"/>`;
    }
    html += `<label class="emask-row"><span>${escFn(t(f.label))}</span>` + control +
      (f.hint ? `<small class="muted">${escFn(t(f.hint))}</small>` : "") +
      `</label>`;
  }
  html += `<label class="emask-row emask-extra"><span>${escFn(t("pnl.ops.entra.extra.label"))}</span>` +
    `<textarea id="f-emask-extra" rows="3" spellcheck="false" placeholder='{ "jobTitle": "…" }'>${escFn(state.extra || "")}</textarea>` +
    `<small class="muted">${escFn(t("pnl.ops.entra.extra.hint"))}</small></label>`;
  html += `<div class="emask-err" id="f-emask-err" role="alert" hidden></div>`;
  wrap.innerHTML = html;
  host.appendChild(wrap);

  const errEl = wrap.querySelector("#f-emask-err");
  const inputs = spec.map((f) => ({ f, el: wrap.querySelector("#f-emask-" + f.key) }));
  const extraEl = wrap.querySelector("#f-emask-extra");

  const recompute = () => {
    const fields = {};
    for (const { f, el } of inputs) fields[f.key] = el.value;
    try {
      ta.value = buildAttributes(fields, spec, extraEl.value);
      errEl.hidden = true;
      errEl.textContent = "";
    } catch (e) {
      // Keep the last good textarea value; flag the escape hatch as the culprit so a
      // malformed extra JSON does not overwrite the saved body with garbage.
      errEl.hidden = false;
      errEl.textContent = t("pnl.ops.entra.extra.invalid", { error: e && e.message ? e.message : "" });
      return;
    }
    onChange();
  };

  for (const { f, el } of inputs) el.addEventListener(f.kind === "select" ? "change" : "input", recompute);
  extraEl.addEventListener("input", recompute);

  return {
    destroy() { wrap.remove(); },
  };
}
