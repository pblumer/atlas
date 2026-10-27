// exchange-ops.js — what the Modeler knows about the Microsoft Exchange Worker's
// operations (ADR-0462): each operation's label, which cmdlet
// parameters it requires and takes, which ones it sets itself, what it returns, and
// against which targets it runs.
//
// It is a copy of connector/exchange/catalog, the table the compiler and the worker
// read. A copy is unavoidable — the panel runs in a browser — so it is held to the Go
// table field by field by TestModelerKnowsEveryExchangeOperation
// (api/exchange_modeler_test.go): a row that differed would tell an author a parameter
// is optional that the deploy then refuses, or hide one the operation takes.

import { t } from "./i18n.js";

// One row per line, in the shape the test reads; keep it that way.
export const EXCHANGE_OPERATIONS = [
  { v: "get-mailbox", l: "pnl.ops.exchange.op.get-mailbox", area: "mailbox", req: ["Identity"], opt: [], sets: [], result: "object", targets: ["online", "onprem"] },
  { v: "list-mailboxes", l: "pnl.ops.exchange.op.list-mailboxes", area: "mailbox", req: [], opt: ["Filter", "RecipientTypeDetails", "Anr", "OrganizationalUnit"], sets: [], result: "array", targets: ["online", "onprem"] },
  { v: "create-shared-mailbox", l: "pnl.ops.exchange.op.create-shared-mailbox", area: "mailbox", req: ["Name"], opt: ["DisplayName", "Alias", "PrimarySmtpAddress", "OrganizationalUnit", "UserPrincipalName"], sets: ["Shared"], result: "object", targets: ["online", "onprem"] },
  { v: "create-room-mailbox", l: "pnl.ops.exchange.op.create-room-mailbox", area: "mailbox", req: ["Name"], opt: ["ResourceCapacity", "DisplayName", "Alias", "PrimarySmtpAddress", "OrganizationalUnit", "UserPrincipalName"], sets: ["Room"], result: "object", targets: ["online", "onprem"] },
  { v: "create-equipment-mailbox", l: "pnl.ops.exchange.op.create-equipment-mailbox", area: "mailbox", req: ["Name"], opt: ["DisplayName", "Alias", "PrimarySmtpAddress", "OrganizationalUnit", "UserPrincipalName"], sets: ["Equipment"], result: "object", targets: ["online", "onprem"] },
  { v: "update-mailbox", l: "pnl.ops.exchange.op.update-mailbox", area: "mailbox", req: ["Identity"], opt: [], sets: [], result: "", targets: ["online", "onprem"], open: true },
  { v: "convert-mailbox", l: "pnl.ops.exchange.op.convert-mailbox", area: "mailbox", req: ["Identity", "Type"], opt: [], sets: [], result: "", targets: ["online", "onprem"] },
  { v: "add-email-address", l: "pnl.ops.exchange.op.add-email-address", area: "mailbox", req: ["Identity", "Address"], opt: [], sets: [], result: "", targets: ["online", "onprem"] },
  { v: "remove-email-address", l: "pnl.ops.exchange.op.remove-email-address", area: "mailbox", req: ["Identity", "Address"], opt: [], sets: [], result: "", targets: ["online", "onprem"] },
  { v: "remove-mailbox", l: "pnl.ops.exchange.op.remove-mailbox", area: "mailbox", req: ["Identity"], opt: [], sets: [], result: "", targets: ["online", "onprem"] },
  { v: "get-mailbox-statistics", l: "pnl.ops.exchange.op.get-mailbox-statistics", area: "mailbox", req: ["Identity"], opt: [], sets: [], result: "object", targets: ["online", "onprem"] },
  { v: "add-full-access", l: "pnl.ops.exchange.op.add-full-access", area: "permission", req: ["Identity", "User"], opt: ["AutoMapping"], sets: ["AccessRights", "InheritanceType"], result: "object", targets: ["online", "onprem"] },
  { v: "remove-full-access", l: "pnl.ops.exchange.op.remove-full-access", area: "permission", req: ["Identity", "User"], opt: [], sets: ["AccessRights", "InheritanceType"], result: "", targets: ["online", "onprem"] },
  { v: "list-mailbox-permissions", l: "pnl.ops.exchange.op.list-mailbox-permissions", area: "permission", req: ["Identity"], opt: ["User"], sets: [], result: "array", targets: ["online", "onprem"] },
  { v: "add-send-as", l: "pnl.ops.exchange.op.add-send-as", area: "permission", req: ["Identity", "User"], opt: [], sets: ["AccessRights", "ExtendedRights"], result: "object", targets: ["online", "onprem"] },
  { v: "remove-send-as", l: "pnl.ops.exchange.op.remove-send-as", area: "permission", req: ["Identity", "User"], opt: [], sets: ["AccessRights", "ExtendedRights"], result: "", targets: ["online", "onprem"] },
  { v: "list-send-as", l: "pnl.ops.exchange.op.list-send-as", area: "permission", req: ["Identity"], opt: ["User"], sets: [], result: "array", targets: ["online"] },
  { v: "grant-send-on-behalf", l: "pnl.ops.exchange.op.grant-send-on-behalf", area: "permission", req: ["Identity", "User"], opt: [], sets: [], result: "", targets: ["online", "onprem"] },
  { v: "revoke-send-on-behalf", l: "pnl.ops.exchange.op.revoke-send-on-behalf", area: "permission", req: ["Identity", "User"], opt: [], sets: [], result: "", targets: ["online", "onprem"] },
  { v: "add-folder-permission", l: "pnl.ops.exchange.op.add-folder-permission", area: "permission", req: ["Identity", "User", "AccessRights"], opt: ["SharingPermissionFlags", "SendNotificationToUser"], sets: [], result: "object", targets: ["online", "onprem"] },
  { v: "set-folder-permission", l: "pnl.ops.exchange.op.set-folder-permission", area: "permission", req: ["Identity", "User", "AccessRights"], opt: ["SharingPermissionFlags", "SendNotificationToUser"], sets: [], result: "", targets: ["online", "onprem"] },
  { v: "remove-folder-permission", l: "pnl.ops.exchange.op.remove-folder-permission", area: "permission", req: ["Identity", "User"], opt: [], sets: [], result: "", targets: ["online", "onprem"] },
  { v: "list-folder-permissions", l: "pnl.ops.exchange.op.list-folder-permissions", area: "permission", req: ["Identity"], opt: ["User"], sets: [], result: "array", targets: ["online", "onprem"] },
  { v: "create-distribution-group", l: "pnl.ops.exchange.op.create-distribution-group", area: "group", req: ["Name"], opt: ["DisplayName", "Alias", "PrimarySmtpAddress", "Type", "ManagedBy", "Members", "Notes", "OrganizationalUnit", "MemberJoinRestriction", "MemberDepartRestriction", "RequireSenderAuthenticationEnabled"], sets: [], result: "object", targets: ["online", "onprem"] },
  { v: "get-distribution-group", l: "pnl.ops.exchange.op.get-distribution-group", area: "group", req: ["Identity"], opt: [], sets: [], result: "object", targets: ["online", "onprem"] },
  { v: "list-distribution-groups", l: "pnl.ops.exchange.op.list-distribution-groups", area: "group", req: [], opt: ["Filter", "Anr", "RecipientTypeDetails", "OrganizationalUnit"], sets: [], result: "array", targets: ["online", "onprem"] },
  { v: "update-distribution-group", l: "pnl.ops.exchange.op.update-distribution-group", area: "group", req: ["Identity"], opt: [], sets: [], result: "", targets: ["online", "onprem"], open: true },
  { v: "remove-distribution-group", l: "pnl.ops.exchange.op.remove-distribution-group", area: "group", req: ["Identity"], opt: ["BypassSecurityGroupManagerCheck"], sets: [], result: "", targets: ["online", "onprem"] },
  { v: "add-distribution-group-member", l: "pnl.ops.exchange.op.add-distribution-group-member", area: "group", req: ["Identity", "Member"], opt: ["BypassSecurityGroupManagerCheck"], sets: [], result: "", targets: ["online", "onprem"] },
  { v: "remove-distribution-group-member", l: "pnl.ops.exchange.op.remove-distribution-group-member", area: "group", req: ["Identity", "Member"], opt: ["BypassSecurityGroupManagerCheck"], sets: [], result: "", targets: ["online", "onprem"] },
  { v: "list-distribution-group-members", l: "pnl.ops.exchange.op.list-distribution-group-members", area: "group", req: ["Identity"], opt: [], sets: [], result: "array", targets: ["online", "onprem"] },
  { v: "create-mail-contact", l: "pnl.ops.exchange.op.create-mail-contact", area: "group", req: ["Name", "ExternalEmailAddress"], opt: ["DisplayName", "Alias", "FirstName", "LastName", "PrimarySmtpAddress", "OrganizationalUnit"], sets: [], result: "object", targets: ["online", "onprem"] },
  { v: "get-mail-contact", l: "pnl.ops.exchange.op.get-mail-contact", area: "group", req: ["Identity"], opt: [], sets: [], result: "object", targets: ["online", "onprem"] },
  { v: "update-mail-contact", l: "pnl.ops.exchange.op.update-mail-contact", area: "group", req: ["Identity"], opt: [], sets: [], result: "", targets: ["online", "onprem"], open: true },
  { v: "remove-mail-contact", l: "pnl.ops.exchange.op.remove-mail-contact", area: "group", req: ["Identity"], opt: [], sets: [], result: "", targets: ["online", "onprem"] },
  { v: "get-auto-reply", l: "pnl.ops.exchange.op.get-auto-reply", area: "settings", req: ["Identity"], opt: [], sets: [], result: "object", targets: ["online", "onprem"] },
  { v: "set-auto-reply", l: "pnl.ops.exchange.op.set-auto-reply", area: "settings", req: ["Identity", "AutoReplyState"], opt: ["InternalMessage", "ExternalMessage", "ExternalAudience", "StartTime", "EndTime"], sets: [], result: "", targets: ["online", "onprem"] },
  { v: "set-forwarding", l: "pnl.ops.exchange.op.set-forwarding", area: "settings", req: ["Identity", "ForwardingSmtpAddress"], opt: ["DeliverToMailboxAndForward"], sets: [], result: "", targets: ["online", "onprem"] },
  { v: "clear-forwarding", l: "pnl.ops.exchange.op.clear-forwarding", area: "settings", req: ["Identity"], opt: [], sets: ["DeliverToMailboxAndForward", "ForwardingAddress", "ForwardingSmtpAddress"], result: "", targets: ["online", "onprem"] },
  { v: "enable-litigation-hold", l: "pnl.ops.exchange.op.enable-litigation-hold", area: "settings", req: ["Identity"], opt: ["LitigationHoldDuration", "LitigationHoldOwner", "RetentionComment", "RetentionUrl"], sets: ["LitigationHoldEnabled"], result: "", targets: ["online", "onprem"] },
  { v: "disable-litigation-hold", l: "pnl.ops.exchange.op.disable-litigation-hold", area: "settings", req: ["Identity"], opt: [], sets: ["LitigationHoldEnabled"], result: "", targets: ["online", "onprem"] },
  { v: "get-calendar-processing", l: "pnl.ops.exchange.op.get-calendar-processing", area: "settings", req: ["Identity"], opt: [], sets: [], result: "object", targets: ["online", "onprem"] },
  { v: "set-calendar-processing", l: "pnl.ops.exchange.op.set-calendar-processing", area: "settings", req: ["Identity"], opt: [], sets: [], result: "", targets: ["online", "onprem"], open: true },
  { v: "set-mailbox-quota", l: "pnl.ops.exchange.op.set-mailbox-quota", area: "settings", req: ["Identity"], opt: ["IssueWarningQuota", "ProhibitSendQuota", "ProhibitSendReceiveQuota", "UseDatabaseQuotaDefaults", "RecoverableItemsQuota", "RecoverableItemsWarningQuota"], sets: [], result: "", targets: ["online", "onprem"], anyOf: true },
  { v: "run-get-cmdlet", l: "pnl.ops.exchange.op.run-get-cmdlet", area: "generic", req: [], opt: [], sets: [], result: "array", targets: ["online", "onprem"], open: true },
];

const AREAS = {
  mailbox: "pnl.ops.exchange.area.mailbox",
  permission: "pnl.ops.exchange.area.permission",
  group: "pnl.ops.exchange.area.group",
  settings: "pnl.ops.exchange.area.settings",
  generic: "pnl.ops.exchange.area.generic",
};

/** The row for an operation, or undefined. */
export function exchangeOperation(name) {
  return EXCHANGE_OPERATIONS.find((o) => o.v === name);
}

/** The operation dropdown's options, the area in front of each label. */
export function exchangeOperationOptions() {
  return EXCHANGE_OPERATIONS.map((o) => ({ v: o.v, get l() { return `${t(AREAS[o.area])}: ${t(o.l)}`; } }));
}

/** Whether the operation returns a list — the operations a maxResults bounds. */
export function exchangeIsListing(name) {
  return exchangeOperation(name)?.result === "array";
}

/** The result variable's FEEL type for the type hints: object, array, or none. */
export function exchangeResultType(name) {
  return exchangeOperation(name)?.result || "";
}

/**
 * A parameters template for an operation: its required parameters, each with an
 * example =expression to replace, so an author starts from a call that deploys.
 */
export function exchangeParametersPlaceholder(name) {
  const op = exchangeOperation(name);
  if (!op) return "{}";
  const keys = op.req.length ? op.req : op.opt.slice(0, 1);
  if (!keys.length) return op.v === "run-get-cmdlet" ? '{\n  "Identity": "=postfach"\n}' : "{}";
  const example = { Identity: "=postfach", User: "=mitarbeiter.mail", Member: "=mitarbeiter.mail", Name: "=name",
    Address: "=\"smtp:\" + alias", AccessRights: "Reviewer", Type: "Shared", AutoReplyState: "Enabled",
    ForwardingSmtpAddress: "=weiterleitung", ExternalEmailAddress: "=extern.mail" };
  return "{\n" + keys.map((k) => `  "${k}": ${JSON.stringify(example[k] || "=" + k.toLowerCase())}`).join(",\n") + "\n}";
}

/** The parameters field's hint: what the selected operation requires, takes and sets. */
export function exchangeParametersHint(name) {
  const op = exchangeOperation(name);
  if (!op) return t("pnl.ops.exchange.hint.pickOperation");
  const parts = [];
  parts.push(op.req.length ? t("pnl.ops.exchange.hint.required", { params: op.req.join(", ") }) : t("pnl.ops.exchange.hint.nothingRequired"));
  if (op.anyOf) parts.push(t("pnl.ops.exchange.hint.anyOf", { params: op.opt.join(", ") }));
  else if (op.opt.length) parts.push(t("pnl.ops.exchange.hint.optional", { params: op.opt.join(", ") }));
  if (op.open) parts.push(t("pnl.ops.exchange.hint.open"));
  else parts.push(t("pnl.ops.exchange.hint.closed"));
  if (op.sets.length) parts.push(t("pnl.ops.exchange.hint.sets", { params: op.sets.join(", ") }));
  if (op.targets.length === 1) parts.push(op.targets[0] === "online" ? t("pnl.ops.exchange.hint.onlineOnly") : t("pnl.ops.exchange.hint.serverOnly"));
  parts.push(t("pnl.ops.exchange.hint.values"));
  return parts.join(" ");
}

/** What the result variable receives, for its hint. */
export function exchangeResultShape(name) {
  const op = exchangeOperation(name);
  if (!op) return "";
  if (op.result === "array") return t("pnl.ops.exchange.result.list");
  if (op.result === "object" && op.v.startsWith("get-")) return t("pnl.ops.exchange.result.get");
  if (op.result === "object") return t("pnl.ops.exchange.result.object");
  return t("pnl.ops.resultNone");
}
