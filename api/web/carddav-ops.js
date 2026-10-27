// carddav-ops.js — what the Modeler knows about the CardDAV Worker's operations
// (ADR-0490): each operation's label, which values it needs and takes,
// what it returns, and the bound a listing takes by default and at most.
//
// It is a copy of connector/carddav/catalog, the table the compiler and the worker read.
// A copy is unavoidable — the panel runs in a browser — so it is held to the Go table
// field by field by TestModelerKnowsEveryCardDAVOperation (api/carddav_modeler_test.go):
// a row that differed would offer a field the deploy then refuses, or hide one the
// operation needs.

import { t } from "./i18n.js";

// One row per line, in the shape the test reads; keep it that way.
export const CARDDAV_OPERATIONS = [
  { v: "find-contacts", l: "pnl.ops.carddav.op.find-contacts", area: "contact", needs: ["query"], takes: ["field", "group"], result: "array", max: [100, 1000] },
  { v: "get-contact", l: "pnl.ops.carddav.op.get-contact", area: "contact", needs: ["contactId"], takes: [], result: "object", max: [0, 0] },
  { v: "search-contacts", l: "pnl.ops.carddav.op.search-contacts", area: "contact", needs: ["query"], takes: [], result: "array", max: [10, 100] },
  { v: "list-contacts", l: "pnl.ops.carddav.op.list-contacts", area: "contact", needs: [], takes: ["group"], result: "array", max: [500, 10000] },
  { v: "create-contact", l: "pnl.ops.carddav.op.create-contact", area: "contact", needs: ["contact"], takes: [], result: "object", max: [0, 0] },
  { v: "update-contact", l: "pnl.ops.carddav.op.update-contact", area: "contact", needs: ["contactId", "contact"], takes: [], result: "object", max: [0, 0] },
  { v: "delete-contact", l: "pnl.ops.carddav.op.delete-contact", area: "contact", needs: ["contactId"], takes: [], result: "", max: [0, 0] },
  { v: "add-to-group", l: "pnl.ops.carddav.op.add-to-group", area: "group", needs: ["contactId", "group"], takes: [], result: "", max: [0, 0] },
  { v: "remove-from-group", l: "pnl.ops.carddav.op.remove-from-group", area: "group", needs: ["contactId", "group"], takes: [], result: "", max: [0, 0] },
];

const AREAS = { contact: "pnl.ops.carddav.area.contact", group: "pnl.ops.carddav.area.group" };

/** The row for an operation, or undefined. */
export function cardDAVOperation(name) {
  return CARDDAV_OPERATIONS.find((o) => o.v === name);
}

/** The operation dropdown's options, the area in front of each label. */
export function cardDAVOperationOptions() {
  return CARDDAV_OPERATIONS.map((o) => ({ v: o.v, get l() { return `${t(AREAS[o.area])}: ${t(o.l)}`; } }));
}

/** Whether the operation needs or takes a value — what decides whether its field shows. */
export function cardDAVUses(name, field) {
  const op = cardDAVOperation(name);
  return !!op && (op.needs.includes(field) || op.takes.includes(field));
}

/** The result variable's FEEL type for the type hints: object, array, or none. */
export function cardDAVResultType(name) {
  return cardDAVOperation(name)?.result || "";
}

/** The maxResults placeholder: the bound an operation gets when none is authored. */
export function cardDAVMaxPlaceholder(name) {
  const op = cardDAVOperation(name);
  return op && op.max[0] ? String(op.max[0]) : "";
}

/** The maxResults hint: what the bound means for this operation, and its ceiling. */
export function cardDAVMaxHint(name) {
  const op = cardDAVOperation(name);
  if (!op || !op.max[1]) return "";
  if (op.v === "search-contacts") {
    return t("pnl.ops.carddav.maxHint.search", { max: op.max[1], default: op.max[0] });
  }
  return t("pnl.ops.contacts.maxHint", { max: op.max[1], default: op.max[0] });
}

/** What the result variable receives, for its hint. */
export function cardDAVResultShape(name) {
  switch (name) {
    case "get-contact":
      return t("pnl.ops.carddav.result.get");
    case "find-contacts":
    case "search-contacts":
    case "list-contacts":
      return t("pnl.ops.contacts.result.list");
    case "create-contact":
    case "update-contact":
      return t("pnl.ops.carddav.result.saved");
  }
  return t("pnl.ops.resultNone");
}

/** The query field's hint for the operation that uses it. */
export function cardDAVQueryHint(name) {
  switch (name) {
    case "find-contacts":
      return t("pnl.ops.contacts.query.find");
    case "search-contacts":
      return t("pnl.ops.carddav.query.search");
  }
  return "";
}

/** A contact template to start from: the fields a create usually sets, as =expressions to replace. */
export function cardDAVContactPlaceholder(name) {
  if (name === "update-contact") {
    return '{\n  "email": "=formular.mail",\n  "fields": { "Kundennummer": "=kundennummer" }\n}';
  }
  return '{\n  "givenName": "=formular.vorname",\n  "familyName": "=formular.nachname",\n  "email": "=formular.mail",\n  "organization": "=formular.firma",\n  "groups": ["Kunden"]\n}';
}
