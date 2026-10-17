// exchange-ops.js — what the Modeler knows about the Microsoft Exchange Worker's
// operations (ADR-draft-exchange-worker): each operation's label, which cmdlet
// parameters it requires and takes, which ones it sets itself, what it returns, and
// against which targets it runs.
//
// It is a copy of connector/exchange/catalog, the table the compiler and the worker
// read. A copy is unavoidable — the panel runs in a browser — so it is held to the Go
// table field by field by TestModelerKnowsEveryExchangeOperation
// (api/exchange_modeler_test.go): a row that differed would tell an author a parameter
// is optional that the deploy then refuses, or hide one the operation takes.
//
// One row per line, in the shape the test reads; keep it that way.
export const EXCHANGE_OPERATIONS = [
  { v: "get-mailbox", l: "Read mailbox", area: "mailbox", req: ["Identity"], opt: [], sets: [], result: "object", targets: ["online", "onprem"] },
  { v: "list-mailboxes", l: "List mailboxes", area: "mailbox", req: [], opt: ["Filter", "RecipientTypeDetails", "Anr", "OrganizationalUnit"], sets: [], result: "array", targets: ["online", "onprem"] },
  { v: "create-shared-mailbox", l: "Create shared mailbox", area: "mailbox", req: ["Name"], opt: ["DisplayName", "Alias", "PrimarySmtpAddress", "OrganizationalUnit", "UserPrincipalName"], sets: ["Shared"], result: "object", targets: ["online", "onprem"] },
  { v: "create-room-mailbox", l: "Create room mailbox", area: "mailbox", req: ["Name"], opt: ["ResourceCapacity", "DisplayName", "Alias", "PrimarySmtpAddress", "OrganizationalUnit", "UserPrincipalName"], sets: ["Room"], result: "object", targets: ["online", "onprem"] },
  { v: "create-equipment-mailbox", l: "Create equipment mailbox", area: "mailbox", req: ["Name"], opt: ["DisplayName", "Alias", "PrimarySmtpAddress", "OrganizationalUnit", "UserPrincipalName"], sets: ["Equipment"], result: "object", targets: ["online", "onprem"] },
  { v: "update-mailbox", l: "Update mailbox", area: "mailbox", req: ["Identity"], opt: [], sets: [], result: "", targets: ["online", "onprem"], open: true },
  { v: "convert-mailbox", l: "Convert mailbox type", area: "mailbox", req: ["Identity", "Type"], opt: [], sets: [], result: "", targets: ["online", "onprem"] },
  { v: "add-email-address", l: "Add e-mail address", area: "mailbox", req: ["Identity", "Address"], opt: [], sets: [], result: "", targets: ["online", "onprem"] },
  { v: "remove-email-address", l: "Remove e-mail address", area: "mailbox", req: ["Identity", "Address"], opt: [], sets: [], result: "", targets: ["online", "onprem"] },
  { v: "remove-mailbox", l: "Remove mailbox", area: "mailbox", req: ["Identity"], opt: [], sets: [], result: "", targets: ["online", "onprem"] },
  { v: "get-mailbox-statistics", l: "Read mailbox statistics", area: "mailbox", req: ["Identity"], opt: [], sets: [], result: "object", targets: ["online", "onprem"] },
  { v: "add-full-access", l: "Grant full access", area: "permission", req: ["Identity", "User"], opt: ["AutoMapping"], sets: ["AccessRights", "InheritanceType"], result: "object", targets: ["online", "onprem"] },
  { v: "remove-full-access", l: "Revoke full access", area: "permission", req: ["Identity", "User"], opt: [], sets: ["AccessRights", "InheritanceType"], result: "", targets: ["online", "onprem"] },
  { v: "list-mailbox-permissions", l: "List mailbox permissions", area: "permission", req: ["Identity"], opt: ["User"], sets: [], result: "array", targets: ["online", "onprem"] },
  { v: "add-send-as", l: "Grant send as", area: "permission", req: ["Identity", "User"], opt: [], sets: ["AccessRights", "ExtendedRights"], result: "object", targets: ["online", "onprem"] },
  { v: "remove-send-as", l: "Revoke send as", area: "permission", req: ["Identity", "User"], opt: [], sets: ["AccessRights", "ExtendedRights"], result: "", targets: ["online", "onprem"] },
  { v: "list-send-as", l: "List send-as permissions (Online)", area: "permission", req: ["Identity"], opt: ["User"], sets: [], result: "array", targets: ["online"] },
  { v: "grant-send-on-behalf", l: "Grant send on behalf", area: "permission", req: ["Identity", "User"], opt: [], sets: [], result: "", targets: ["online", "onprem"] },
  { v: "revoke-send-on-behalf", l: "Revoke send on behalf", area: "permission", req: ["Identity", "User"], opt: [], sets: [], result: "", targets: ["online", "onprem"] },
  { v: "add-folder-permission", l: "Grant folder permission", area: "permission", req: ["Identity", "User", "AccessRights"], opt: ["SharingPermissionFlags", "SendNotificationToUser"], sets: [], result: "object", targets: ["online", "onprem"] },
  { v: "set-folder-permission", l: "Change folder permission", area: "permission", req: ["Identity", "User", "AccessRights"], opt: ["SharingPermissionFlags", "SendNotificationToUser"], sets: [], result: "", targets: ["online", "onprem"] },
  { v: "remove-folder-permission", l: "Revoke folder permission", area: "permission", req: ["Identity", "User"], opt: [], sets: [], result: "", targets: ["online", "onprem"] },
  { v: "list-folder-permissions", l: "List folder permissions", area: "permission", req: ["Identity"], opt: ["User"], sets: [], result: "array", targets: ["online", "onprem"] },
  { v: "create-distribution-group", l: "Create distribution group", area: "group", req: ["Name"], opt: ["DisplayName", "Alias", "PrimarySmtpAddress", "Type", "ManagedBy", "Members", "Notes", "OrganizationalUnit", "MemberJoinRestriction", "MemberDepartRestriction", "RequireSenderAuthenticationEnabled"], sets: [], result: "object", targets: ["online", "onprem"] },
  { v: "get-distribution-group", l: "Read distribution group", area: "group", req: ["Identity"], opt: [], sets: [], result: "object", targets: ["online", "onprem"] },
  { v: "list-distribution-groups", l: "List distribution groups", area: "group", req: [], opt: ["Filter", "Anr", "RecipientTypeDetails", "OrganizationalUnit"], sets: [], result: "array", targets: ["online", "onprem"] },
  { v: "update-distribution-group", l: "Update distribution group", area: "group", req: ["Identity"], opt: [], sets: [], result: "", targets: ["online", "onprem"], open: true },
  { v: "remove-distribution-group", l: "Remove distribution group", area: "group", req: ["Identity"], opt: ["BypassSecurityGroupManagerCheck"], sets: [], result: "", targets: ["online", "onprem"] },
  { v: "add-distribution-group-member", l: "Add group member", area: "group", req: ["Identity", "Member"], opt: ["BypassSecurityGroupManagerCheck"], sets: [], result: "", targets: ["online", "onprem"] },
  { v: "remove-distribution-group-member", l: "Remove group member", area: "group", req: ["Identity", "Member"], opt: ["BypassSecurityGroupManagerCheck"], sets: [], result: "", targets: ["online", "onprem"] },
  { v: "list-distribution-group-members", l: "List group members", area: "group", req: ["Identity"], opt: [], sets: [], result: "array", targets: ["online", "onprem"] },
  { v: "create-mail-contact", l: "Create mail contact", area: "group", req: ["Name", "ExternalEmailAddress"], opt: ["DisplayName", "Alias", "FirstName", "LastName", "PrimarySmtpAddress", "OrganizationalUnit"], sets: [], result: "object", targets: ["online", "onprem"] },
  { v: "get-mail-contact", l: "Read mail contact", area: "group", req: ["Identity"], opt: [], sets: [], result: "object", targets: ["online", "onprem"] },
  { v: "update-mail-contact", l: "Update mail contact", area: "group", req: ["Identity"], opt: [], sets: [], result: "", targets: ["online", "onprem"], open: true },
  { v: "remove-mail-contact", l: "Remove mail contact", area: "group", req: ["Identity"], opt: [], sets: [], result: "", targets: ["online", "onprem"] },
  { v: "get-auto-reply", l: "Read automatic reply", area: "settings", req: ["Identity"], opt: [], sets: [], result: "object", targets: ["online", "onprem"] },
  { v: "set-auto-reply", l: "Set automatic reply", area: "settings", req: ["Identity", "AutoReplyState"], opt: ["InternalMessage", "ExternalMessage", "ExternalAudience", "StartTime", "EndTime"], sets: [], result: "", targets: ["online", "onprem"] },
  { v: "set-forwarding", l: "Set forwarding", area: "settings", req: ["Identity", "ForwardingSmtpAddress"], opt: ["DeliverToMailboxAndForward"], sets: [], result: "", targets: ["online", "onprem"] },
  { v: "clear-forwarding", l: "Clear forwarding", area: "settings", req: ["Identity"], opt: [], sets: ["DeliverToMailboxAndForward", "ForwardingAddress", "ForwardingSmtpAddress"], result: "", targets: ["online", "onprem"] },
  { v: "enable-litigation-hold", l: "Enable litigation hold", area: "settings", req: ["Identity"], opt: ["LitigationHoldDuration", "LitigationHoldOwner", "RetentionComment", "RetentionUrl"], sets: ["LitigationHoldEnabled"], result: "", targets: ["online", "onprem"] },
  { v: "disable-litigation-hold", l: "Disable litigation hold", area: "settings", req: ["Identity"], opt: [], sets: ["LitigationHoldEnabled"], result: "", targets: ["online", "onprem"] },
  { v: "get-calendar-processing", l: "Read calendar processing", area: "settings", req: ["Identity"], opt: [], sets: [], result: "object", targets: ["online", "onprem"] },
  { v: "set-calendar-processing", l: "Set calendar processing", area: "settings", req: ["Identity"], opt: [], sets: [], result: "", targets: ["online", "onprem"], open: true },
  { v: "set-mailbox-quota", l: "Set mailbox quotas", area: "settings", req: ["Identity"], opt: ["IssueWarningQuota", "ProhibitSendQuota", "ProhibitSendReceiveQuota", "UseDatabaseQuotaDefaults", "RecoverableItemsQuota", "RecoverableItemsWarningQuota"], sets: [], result: "", targets: ["online", "onprem"], anyOf: true },
  { v: "run-get-cmdlet", l: "Run a Get- cmdlet (read only)", area: "generic", req: [], opt: [], sets: [], result: "array", targets: ["online", "onprem"], open: true },
];

const AREAS = {
  mailbox: "Mailbox",
  permission: "Permission",
  group: "Distribution group / contact",
  settings: "Mailbox setting",
  generic: "Read only",
};

/** The row for an operation, or undefined. */
export function exchangeOperation(name) {
  return EXCHANGE_OPERATIONS.find((o) => o.v === name);
}

/** The operation dropdown's options, the area in front of each label. */
export function exchangeOperationOptions() {
  return EXCHANGE_OPERATIONS.map((o) => ({ v: o.v, l: `${AREAS[o.area]}: ${o.l}` }));
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
  if (!op) return "Pick an operation first.";
  const parts = [];
  parts.push(op.req.length ? `Required: ${op.req.join(", ")}.` : "Nothing is required.");
  if (op.anyOf) parts.push(`At least one of: ${op.opt.join(", ")}.`);
  else if (op.opt.length) parts.push(`Optional: ${op.opt.join(", ")}.`);
  if (op.open) parts.push("Any further parameter of the cmdlet is passed on, under the name Microsoft's cmdlet reference gives it.");
  else parts.push("Nothing else is accepted, so the encoding the operation adds cannot be overridden.");
  if (op.sets.length) parts.push(`The operation sets ${op.sets.join(", ")} itself.`);
  if (op.targets.length === 1) parts.push(op.targets[0] === "online" ? "Exchange Online only." : "Exchange Server only.");
  parts.push("A string starting with = is a FEEL expression; true, false and numbers stay what they are (a switch needs a boolean). A value that evaluates to null is left out, never sent as a clear.");
  return parts.join(" ");
}

/** What the result variable receives, for its hint. */
export function exchangeResultShape(name) {
  const op = exchangeOperation(name);
  if (!op) return "";
  if (op.result === "array") return "An array of objects (empty when there are none), at most maxResults long — a listing past it fails instead of being cut short.";
  if (op.result === "object" && op.v.startsWith("get-")) return "One object with the cmdlet's properties, or null when there is none. An identity that matches several objects fails the task.";
  if (op.result === "object") return "The object the cmdlet created or granted — or every entry, when Exchange answers with several — for a model that keeps it; leave empty to discard.";
  return "This operation returns nothing, and the compiler refuses a result variable on it.";
}
