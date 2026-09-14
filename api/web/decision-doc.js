// Decision documentation export (ADR-0324): turn the
// decision the Modeler is holding into a structured PDF — the requirements graph,
// then every decision with its prose, the input data it reads, and its rule table.
//
// This module is deliberately split from the editor, the way process-doc.js is:
// collecting the content and laying out the document are pure functions, so they
// can be driven and asserted in a browser test without mounting the whole editor;
// the editor only wires a button to them.
//
// The content is read out of the **DMN XML**, not out of dmn-js's moddle. That is
// the same choice firstDecisionName already makes in the editor, for the same
// reason: the XML is the thing that gets stored in the record and handed to temis,
// so reading it is reading what the document actually describes, and it costs no
// dependency on an editor's internals.

import { PdfDocument, bytesToBase64 } from "./pdf.js";
import { svgToJpeg, wantsLandscape } from "./doc-graphics.js";

const GREY = [0.42, 0.42, 0.42];

// localName-based lookups, because a DMN file may declare the model namespace
// with any prefix (or none) and three different DMN versions are in the wild.
const kids = (el, name) =>
  Array.from(el.children || []).filter((c) => c.localName === name);
const kid = (el, name) => kids(el, name)[0] || null;
const textOf = (el) => (el ? (el.textContent || "").trim() : "");

// hrefId resolves a "#id" reference inside this model. An href naming another
// document resolves to nothing here, and is reported as empty.
function hrefId(el) {
  const href = el ? el.getAttribute("href") || "" : "";
  return href.startsWith("#") ? href.slice(1) : "";
}

// columnOf reads one decision-table column, input or output. An input column is
// described by what it evaluates; an output column by the variable it writes.
function inputColumn(el) {
  const expr = kid(el, "inputExpression");
  return {
    label: el.getAttribute("label") || "",
    expression: textOf(kid(expr, "text")),
    type: (expr && expr.getAttribute("typeRef")) || "",
  };
}

function outputColumn(el) {
  return {
    label: el.getAttribute("label") || "",
    expression: el.getAttribute("name") || "",
    type: el.getAttribute("typeRef") || "",
  };
}

// collectDecisionDocumentation reads a DMN model into the shape the document and
// the stored record both use. It never throws: a model it cannot parse documents
// nothing rather than failing the export the author asked for.
export function collectDecisionDocumentation(xml) {
  const empty = { modelName: "", decisionId: "", decisions: [] };
  let doc;
  try {
    doc = new DOMParser().parseFromString(xml, "application/xml");
  } catch {
    return empty;
  }
  const root = doc && doc.documentElement;
  if (!root || root.localName !== "definitions") return empty;

  // Input data by id, so a decision can name what it reads rather than an href.
  const inputData = new Map();
  for (const el of kids(root, "inputData")) {
    const variable = kid(el, "variable");
    inputData.set(el.getAttribute("id") || "", {
      label: "",
      expression: el.getAttribute("name") || el.getAttribute("id") || "",
      type: (variable && variable.getAttribute("typeRef")) || "",
    });
  }

  const decisions = [];
  for (const el of kids(root, "decision")) {
    const id = el.getAttribute("id") || "";
    if (!id) continue;
    const entry = {
      id,
      name: el.getAttribute("name") || "",
      description: textOf(kid(el, "description")),
      inputs: [],
      hitPolicy: "",
      inputColumns: [],
      outputColumns: [],
      rules: [],
      literal: "",
    };
    // What this decision reads: the input data wired into it. A requirement on
    // another decision is a dependency rather than an input value, so it is left
    // to the requirements graph rather than listed as a field.
    for (const req of kids(el, "informationRequirement")) {
      const from = inputData.get(hrefId(kid(req, "requiredInput")));
      if (from) entry.inputs.push(from);
    }
    const table = kid(el, "decisionTable");
    if (table) {
      const policy = table.getAttribute("hitPolicy") || "UNIQUE";
      const aggregation = table.getAttribute("aggregation") || "";
      entry.hitPolicy = aggregation ? `${policy} ${aggregation}` : policy;
      entry.inputColumns = kids(table, "input").map(inputColumn);
      entry.outputColumns = kids(table, "output").map(outputColumn);
      entry.rules = kids(table, "rule").map((rule) => ({
        inputs: kids(rule, "inputEntry").map((c) => textOf(kid(c, "text"))),
        outputs: kids(rule, "outputEntry").map((c) => textOf(kid(c, "text"))),
        description: textOf(kid(rule, "description")),
      }));
    } else {
      entry.literal = textOf(kid(kid(el, "literalExpression"), "text"));
    }
    decisions.push(entry);
  }

  return {
    modelName: root.getAttribute("name") || "",
    // The document is filed under one decision — the first the model declares,
    // which is the one the editor is about. A model providing several documents
    // them all, under that one's id.
    decisionId: decisions.length ? decisions[0].id : "",
    decisions,
  };
}

// cellText renders a rule cell for the document. An empty entry and the DMN "-"
// both mean "this column does not constrain this rule", so both read as one dash
// — the same rendering the trace matrix uses, so the document and the Console
// agree about what an unconstrained cell looks like.
const cellText = (t) => {
  const s = String(t ?? "").trim();
  return s === "" || s === "-" ? "–" : s;
};

// columnTitle names a decision-table column for the document's header row: the
// label a modeller gave it, falling back to the expression it evaluates, which is
// what an unlabelled column actually means.
const columnTitle = (c) => c.label || c.expression || "—";

// renderDecisionBody draws one decision's content into a document: what it reads,
// its rule table (or its literal expression), and what it writes. It is shared
// with the *process* document, which shows the decision a business rule task runs
// (ADR-draft-the-process-document-shows-the-decision-a-task-runs) — one renderer,
// so a rule table looks the same whichever document a reader is holding.
//
// It draws the body only. The heading, the id line and the prose above it belong
// to the document doing the drawing, because the two frame a decision differently:
// here it is the subject, there it is what a step calls.
export function renderDecisionBody(doc, dec) {
  if (dec.inputs && dec.inputs.length) {
    doc.paragraph("Reads", { size: 9, bold: true, after: 2 });
    doc.table(
      [{ header: "Input", width: 2 }, { header: "Type", width: 1 }],
      dec.inputs.map((i) => [i.expression, i.type || "—"]),
    );
  }

  if (dec.rules.length || dec.inputColumns.length) {
    // The rule table is the decision. It is set as a table rather than as
    // preformatted text because a decision table read as text is a decision
    // table nobody checks.
    doc.paragraph("Rules  ·  hit policy " + dec.hitPolicy, { size: 9, color: GREY, after: 2 });
    const columns = [{ header: "#", width: 0.4 }]
      .concat(dec.inputColumns.map((c) => ({ header: columnTitle(c), width: 1.4 })))
      .concat(dec.outputColumns.map((c) => ({ header: "→ " + columnTitle(c), width: 1.4 })));
    const rows = dec.rules.map((rule, i) =>
      [String(i + 1)]
        .concat(dec.inputColumns.map((_, k) => cellText(rule.inputs[k])))
        .concat(dec.outputColumns.map((_, k) => cellText(rule.outputs[k]))));
    doc.table(columns, rows);
    // A rule's own annotation says why it exists, which the grid has no room
    // for; the ones that carry it follow the table.
    const annotated = dec.rules
      .map((rule, i) => ({ n: i + 1, text: rule.description }))
      .filter((r) => r.text);
    for (const r of annotated) {
      doc.paragraph(`Rule ${r.n}: ${r.text}`, { size: 9, indent: 8, after: 3 });
    }
    if (dec.outputColumns.length) {
      const outs = dec.outputColumns
        .map((c) => c.expression + (c.type ? " (" + c.type + ")" : ""))
        .filter(Boolean)
        .join(", ");
      if (outs) doc.paragraph("Writes: " + outs, { size: 9, color: GREY, after: 4 });
    }
  } else if (dec.literal) {
    // A decision whose logic is a literal expression is shown as what it is.
    doc.codeBlock(dec.literal, { label: "Literal expression", language: "feel" });
  } else {
    doc.paragraph("This decision declares no logic yet.", { size: 9.5, color: GREY, after: 6 });
  }
}

// buildDecisionDocumentationPdf lays out the document. It is a pure function of
// the collected content plus the rasterised graph, so a test can assert what a
// document says without a browser canvas.
export function buildDecisionDocumentationPdf(spec) {
  const { collection, diagram, title, note, version, createdAt, createdBy, modelRef } = spec;
  const heading = title || collection.modelName || collection.decisionId;
  const doc = new PdfDocument({
    title: heading,
    footer: heading + (version ? " · v" + version : ""),
  });

  doc.heading(heading, 1);
  doc.paragraph("Decision documentation", { size: 11, color: GREY, after: 12 });

  if (collection.modelName) doc.keyValue("Model", collection.modelName);
  if (modelRef) doc.keyValue("Model handle", modelRef + ".dmn");
  doc.keyValue("Decision ID", collection.decisionId || "—");
  if (version) doc.keyValue("Documentation version", "v" + version);
  doc.keyValue("Created", createdAt || new Date().toLocaleString());
  if (createdBy) doc.keyValue("Created by", createdBy);
  doc.keyValue("Decisions documented", String(collection.decisions.length));
  if (note) doc.keyValue("Note", note);
  doc.rule();

  if (diagram && diagram.bytes) {
    // A wide requirements graph gets its own landscape sheet rather than being
    // squeezed into an unreadable strip; the document then returns to portrait.
    const landscape = wantsLandscape(diagram, collection.decisions.length);
    if (landscape) doc.landscapePage();
    doc.heading("Decision requirements", 2);
    doc.image(diagram.bytes, diagram.width, diagram.height);
    if (landscape) doc.portraitPage();
  }

  doc.heading("Decisions", 2);
  if (collection.decisions.length === 0) {
    doc.paragraph("This model declares no decision.", { color: GREY });
  }

  for (const dec of collection.decisions) {
    // A decision's section is its title, its id line, and at least the first line
    // of what is written about it. Keeping those together is what stops a name
    // stranded at the foot of a page from its own description.
    doc.heading(dec.name || dec.id, 3, { keep: 72 });
    doc.paragraph("id: " + dec.id, { size: 8.5, color: GREY, after: 3 });
    if (dec.description) {
      doc.paragraph(dec.description, { size: 10, after: 6 });
    } else {
      doc.paragraph("No description.", { size: 9.5, color: GREY, after: 6 });
    }

    renderDecisionBody(doc, dec);
    doc.y += 4;
  }

  return doc.bytes();
}

// exportDecisionDocumentation is the whole act: read the model, rasterize the
// requirements graph, build the document, and publish it as the next version of
// this decision. Returns the created version as the API rendered it.
export async function exportDecisionDocumentation({ modeler, api, xml, title, note, createdBy, modelRef }) {
  const collection = collectDecisionDocumentation(xml);
  if (!collection.decisionId) throw new Error("this model declares no decision to document");

  // The picture comes from the DRG view, which is the one that draws the graph. A
  // model open on a decision table has no graph SVG to save, so the view is
  // switched first and the author is put back where they were by the caller.
  let diagram = null;
  try {
    const { svg } = await modeler.saveSVG();
    diagram = await svgToJpeg(svg);
  } catch {
    // A document without the picture is still the rules, which is the part that
    // matters; failing the whole export over a raster would be the wrong trade.
    diagram = null;
  }

  const bytes = buildDecisionDocumentationPdf({
    collection, diagram, title, note, createdBy, modelRef,
    createdAt: new Date().toLocaleString(),
  });

  return api("POST", `/api/v1/decisions/${encodeURIComponent(collection.decisionId)}/documentation`, {
    title: title || collection.modelName || collection.decisionId,
    note: note || "",
    modelName: collection.modelName,
    modelRef: modelRef || "",
    xml,
    decisions: collection.decisions,
    pdfBase64: bytesToBase64(bytes),
  });
}
