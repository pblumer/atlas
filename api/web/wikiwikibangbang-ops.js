// wikiwikibangbang-ops.js — what the Modeler knows about the wikiwikibangbang Worker's
// operations (ADR-0498): each operation's label, which values
// it needs and takes, what it returns, and the bound a query takes by default and at
// most.
//
// It is a copy of connector/wikiwikibangbang/catalog, the table the compiler and the
// worker read. A copy is unavoidable — the panel runs in a browser — so it is held to
// the Go table field by field by TestModelerKnowsEveryWikiOperation
// (api/wikiwikibangbang_modeler_test.go): a row that differed would offer a field the
// deploy then refuses, or hide one the operation needs.
//
// Two values stand for a pair of fields, of which an author fills one: "properties" is
// the inline object or propertiesVariable, and "body" is text or blocks. "oneOf" marks
// the operation that needs at least one of the values it takes.
//
import { t } from "./i18n.js";

// One row per line, in the shape the test reads; keep it that way.
export const WIKI_OPERATIONS = [
  { v: "create-item", l: "pnl.ops.wiki.op.create-item", area: "item", needs: ["space", "itemType", "title"], takes: ["parent", "properties", "matchField", "body"], oneOf: false, result: "object", max: [0, 0] },
  { v: "get-item", l: "pnl.ops.wiki.op.get-item", area: "item", needs: ["item"], takes: [], oneOf: false, result: "object", max: [0, 0] },
  { v: "update-item", l: "pnl.ops.wiki.op.update-item", area: "item", needs: ["item"], takes: ["title", "properties", "labels"], oneOf: true, result: "object", max: [0, 0] },
  { v: "transition-item", l: "pnl.ops.wiki.op.transition-item", area: "item", needs: ["item", "transition"], takes: [], oneOf: false, result: "object", max: [0, 0] },
  { v: "add-comment", l: "pnl.ops.wiki.op.add-comment", area: "item", needs: ["item", "comment"], takes: [], oneOf: false, result: "object", max: [0, 0] },
  { v: "query-items", l: "pnl.ops.wiki.op.query-items", area: "item", needs: ["query"], takes: [], oneOf: false, result: "array", max: [100, 1000] },
  { v: "read-page", l: "pnl.ops.wiki.op.read-page", area: "page", needs: ["item"], takes: [], oneOf: false, result: "string", max: [0, 0] },
  { v: "append-to-page", l: "pnl.ops.wiki.op.append-to-page", area: "page", needs: ["item", "body"], takes: [], oneOf: false, result: "object", max: [0, 0] },
];

const AREAS = { item: "pnl.ops.wiki.area.item", page: "pnl.ops.wiki.area.page" };

/** The row for an operation, or undefined. */
export function wikiOperation(name) {
  return WIKI_OPERATIONS.find((o) => o.v === name);
}

/** The operation dropdown's options, the area in front of each label. */
export function wikiOperationOptions() {
  return WIKI_OPERATIONS.map((o) => ({ v: o.v, get l() { return `${t(AREAS[o.area])}: ${t(o.l)}`; } }));
}

/** Whether the operation needs or takes a value — what decides whether its field shows. */
export function wikiUses(name, value) {
  const op = wikiOperation(name);
  return !!op && (op.needs.includes(value) || op.takes.includes(value));
}

/** The result variable's FEEL type for the type hints: object, array, string, or none. */
export function wikiResultType(name) {
  return wikiOperation(name)?.result || "";
}

/** The maxResults placeholder: the bound a query gets when none is authored. */
export function wikiMaxPlaceholder(name) {
  const op = wikiOperation(name);
  return op && op.max[0] ? String(op.max[0]) : "";
}

/** The maxResults hint: what the bound means, and its ceiling. */
export function wikiMaxHint(name) {
  const op = wikiOperation(name);
  if (!op || !op.max[1]) return "";
  return t("pnl.ops.wiki.maxHint", { max: op.max[1], default: op.max[0] });
}

const ITEM_SHAPE = "pnl.ops.wiki.itemShape";

/** What the result variable receives, for its hint. */
export function wikiResultShape(name) {
  const shape = t(ITEM_SHAPE);
  switch (name) {
    case "get-item":
      return t("pnl.ops.wiki.result.get", { shape });
    case "create-item":
      return t("pnl.ops.wiki.result.create", { shape });
    case "update-item":
    case "transition-item":
    case "append-to-page":
      return t("pnl.ops.wiki.result.saved", { shape });
    case "add-comment":
      return t("pnl.ops.wiki.result.comment");
    case "query-items":
      return t("pnl.ops.wiki.result.query", { shape });
    case "read-page":
      return t("pnl.ops.wiki.result.page");
  }
  return "";
}

/** A properties template to start from, as =expressions to replace. */
export function wikiPropertiesPlaceholder(name) {
  if (name === "update-item") {
    return '{\n  "assignee": "=antrag.besitzer",\n  "due": "=antrag.faellig"\n}';
  }
  return '{\n  "assignee": "=antrag.besitzer",\n  "due": "=antrag.faellig",\n  "source-id": "=string(processInstanceKey)"\n}';
}
