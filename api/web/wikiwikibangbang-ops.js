// wikiwikibangbang-ops.js — what the Modeler knows about the wikiwikibangbang Worker's
// operations (ADR-draft-wikiwikibangbang-worker): each operation's label, which values
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
// One row per line, in the shape the test reads; keep it that way.
export const WIKI_OPERATIONS = [
  { v: "create-item", l: "Create item", area: "item", needs: ["space", "itemType", "title"], takes: ["parent", "properties", "matchField", "body"], oneOf: false, result: "object", max: [0, 0] },
  { v: "get-item", l: "Read item", area: "item", needs: ["item"], takes: [], oneOf: false, result: "object", max: [0, 0] },
  { v: "update-item", l: "Update item (title, properties, labels)", area: "item", needs: ["item"], takes: ["title", "properties", "labels"], oneOf: true, result: "object", max: [0, 0] },
  { v: "transition-item", l: "Move through workflow", area: "item", needs: ["item", "transition"], takes: [], oneOf: false, result: "object", max: [0, 0] },
  { v: "add-comment", l: "Add comment", area: "item", needs: ["item", "comment"], takes: [], oneOf: false, result: "object", max: [0, 0] },
  { v: "query-items", l: "Query items", area: "item", needs: ["query"], takes: [], oneOf: false, result: "array", max: [100, 1000] },
  { v: "read-page", l: "Read page as Markdown", area: "page", needs: ["item"], takes: [], oneOf: false, result: "string", max: [0, 0] },
  { v: "append-to-page", l: "Append to page", area: "page", needs: ["item", "body"], takes: [], oneOf: false, result: "object", max: [0, 0] },
];

const AREAS = { item: "Item", page: "Page" };

/** The row for an operation, or undefined. */
export function wikiOperation(name) {
  return WIKI_OPERATIONS.find((o) => o.v === name);
}

/** The operation dropdown's options, the area in front of each label. */
export function wikiOperationOptions() {
  return WIKI_OPERATIONS.map((o) => ({ v: o.v, l: `${AREAS[o.area]}: ${o.l}` }));
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
  return `How many items the result may hold, at most ${op.max[1]}. More matches fail the task instead of answering a short list, because a short list is a wrong answer a process would act on. Empty uses ${op.max[0]}.`;
}

const ITEM_SHAPE = "id, key, type, space, parent, title, state, properties, labels and version";

/** What the result variable receives, for its hint. */
export function wikiResultShape(name) {
  switch (name) {
    case "get-item":
      return `The item as one object: ${ITEM_SHAPE}, and its body as the wiki's blocks. Keep the id to address the item later — a key changes when the item moves to another space, the id never does.`;
    case "create-item":
      return `The item as the wiki holds it afterwards: ${ITEM_SHAPE}, without its body. Keep the id to address the item later. With a matching field, an existing item is answered instead of a second one. Leave empty to discard it.`;
    case "update-item":
    case "transition-item":
    case "append-to-page":
      return `The item as the wiki holds it afterwards: ${ITEM_SHAPE}, without its body. Leave empty to discard it.`;
    case "add-comment":
      return "The comment: id, author (the Worker's account), created and text. Leave empty to discard it.";
    case "query-items":
      return `A list of items, each ${ITEM_SHAPE}, without their bodies; empty when nothing matched. FEEL lists are 1-based, so the first is =aufgaben[1].`;
    case "read-page":
      return "The page as Markdown text — title, properties and body, as the wiki exports it — to put into a mail or hand to an agent.";
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
