// sharepointserver-ops.js — what the Modeler knows about the SharePoint Server Worker's
// operations (ADR-draft-sharepoint-server-worker): each operation's label, which
// parameters it requires and takes, and what it returns.
//
// A copy of connector/sharepointserver/catalog, because the panel runs in a browser; it
// is held to the Go table field by field by TestModelerKnowsEverySharePointServerOperation
// (api/sharepointserver_modeler_test.go).

import { t } from "./i18n.js";

// One row per line, in the shape the test reads; keep it that way.
export const SHAREPOINT_SERVER_OPERATIONS = [
  { v: "create-site", l: "pnl.ops.sharepointserver.op.create-site", area: "site", req: ["Site", "Name", "OwnerAlias"], opt: ["SecondaryOwnerAlias", "OwnerEmail", "Description", "Language", "Template", "ContentDatabase", "QuotaTemplate", "Properties"], result: "object" },
  { v: "get-site", l: "pnl.ops.sharepointserver.op.get-site", area: "site", req: ["Site"], opt: [], result: "object" },
  { v: "list-sites", l: "pnl.ops.sharepointserver.op.list-sites", area: "site", req: [], opt: ["Limit"], result: "array" },
  { v: "delete-site", l: "pnl.ops.sharepointserver.op.delete-site", area: "site", req: ["Site"], opt: [], result: "" },
  { v: "restore-site", l: "pnl.ops.sharepointserver.op.restore-site", area: "site", req: ["Site"], opt: ["ContentDatabase"], result: "object" },
  { v: "set-site-owners", l: "pnl.ops.sharepointserver.op.set-site-owners", area: "access", req: ["Site", "OwnerAlias"], opt: ["SecondaryOwnerAlias"], result: "" },
  { v: "update-site-admins", l: "pnl.ops.sharepointserver.op.update-site-admins", area: "access", req: ["Site"], opt: ["Add", "Remove"], result: "", anyOf: true },
  { v: "update-group-members", l: "pnl.ops.sharepointserver.op.update-group-members", area: "access", req: ["Site", "Group"], opt: ["Add", "Remove"], result: "", anyOf: true },
  { v: "set-site-quota", l: "pnl.ops.sharepointserver.op.set-site-quota", area: "storage", req: ["Site", "QuotaGB"], opt: ["WarningPercent"], result: "" },
];

const AREAS = {
  site: "pnl.ops.sharepointserver.area.site",
  access: "pnl.ops.sharepointserver.area.access",
  storage: "pnl.ops.sharepointserver.area.storage",
};

function operation(name) {
  return SHAREPOINT_SERVER_OPERATIONS.find((o) => o.v === name);
}

/** The operation dropdown's options, the area in front of each label. */
export function sharepointServerOperationOptions() {
  return SHAREPOINT_SERVER_OPERATIONS.map((o) => ({ v: o.v, get l() { return `${t(AREAS[o.area])}: ${t(o.l)}`; } }));
}

/** The result variable's FEEL type for the type hints: object, array, or none. */
export function sharepointServerResultType(name) {
  return operation(name)?.result || "";
}

/** A parameters template: the required parameters, each with an example to replace. */
export function sharepointServerParametersPlaceholder(name) {
  const op = operation(name);
  if (!op) return "{}";
  const keys = op.req.length ? op.req : op.opt.slice(0, 1);
  if (!keys.length) return "{}";
  const example = { Site: "=regel.alias", Name: "=titel", OwnerAlias: "=besitzer", Group: "members", QuotaGB: "=number(kontingent)", Limit: 200 };
  return "{\n" + keys.map((k) => `  "${k}": ${JSON.stringify(example[k] ?? "=" + k.toLowerCase())}`).join(",\n") + "\n}";
}

/** The parameters field's hint: what the selected operation requires and takes. */
export function sharepointServerParametersHint(name) {
  const op = operation(name);
  if (!op) return t("pnl.ops.exchange.hint.pickOperation");
  const parts = [];
  parts.push(op.req.length ? t("pnl.ops.exchange.hint.required", { params: op.req.join(", ") }) : t("pnl.ops.exchange.hint.nothingRequired"));
  if (op.anyOf) parts.push(t("pnl.ops.exchange.hint.anyOf", { params: op.opt.join(", ") }));
  else if (op.opt.length) parts.push(t("pnl.ops.exchange.hint.optional", { params: op.opt.join(", ") }));
  parts.push(t("pnl.ops.sharepointserver.hint.values"));
  return parts.join(" ");
}

/** What the result variable receives, for its hint. */
export function sharepointServerResultShape(name) {
  const op = operation(name);
  if (!op || !op.result) return t("pnl.ops.resultNone");
  return op.result === "array" ? t("pnl.ops.sharepointserver.result.list") : t("pnl.ops.sharepointserver.result.object");
}
