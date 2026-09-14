// Process documentation export (ADR-0143): turn the diagram the Modeler is
// holding into a structured PDF — the graphic, then every element with the prose
// written about it.
//
// This module is deliberately split from the editor. Collecting the prose and
// laying out the document are pure functions over a model, so they can be driven
// and asserted in a browser test without mounting the whole editor; the editor
// only wires a button to them.

import { PdfDocument, bytesToBase64 } from "./pdf.js";
import { collectDecisionDocumentation, renderDecisionBody } from "./decision-doc.js";
import { svgToJpeg, wantsLandscape } from "./doc-graphics.js";

// Re-exported because they were this module's own until the decision document
// started sharing them, and every caller — the editor, the harness — already names
// them here. The definitions moved; the door did not.
export { svgToJpeg, sizeOfSvg, trimToContent, wantsLandscape } from "./doc-graphics.js";

// A readable label for each BPMN type. The raw `bpmn:ExclusiveGateway` is
// meaningless to the audience this document is written for.
const TYPE_LABELS = {
  "bpmn:StartEvent": "Start event",
  "bpmn:EndEvent": "End event",
  "bpmn:IntermediateCatchEvent": "Intermediate catch event",
  "bpmn:IntermediateThrowEvent": "Intermediate throw event",
  "bpmn:BoundaryEvent": "Boundary event",
  "bpmn:Task": "Task",
  "bpmn:UserTask": "User task",
  "bpmn:ServiceTask": "Service task",
  "bpmn:ScriptTask": "Script task",
  "bpmn:BusinessRuleTask": "Business rule task",
  "bpmn:SendTask": "Send task",
  "bpmn:ReceiveTask": "Receive task",
  "bpmn:ManualTask": "Manual task",
  "bpmn:CallActivity": "Call activity",
  "bpmn:SubProcess": "Subprocess",
  "bpmn:Transaction": "Transaction",
  "bpmn:ExclusiveGateway": "Exclusive gateway (XOR)",
  "bpmn:ParallelGateway": "Parallel gateway (AND)",
  "bpmn:InclusiveGateway": "Inclusive gateway (OR)",
  "bpmn:EventBasedGateway": "Event-based gateway",
  "bpmn:SequenceFlow": "Sequence flow",
  "bpmn:Participant": "Pool",
  "bpmn:Lane": "Lane",
};

// The order elements are documented in: the reader follows the process, so the
// entry points come first and the ends last, with the work in between. Named
// sequence flows close the list — a flow is a transition between the steps
// above, so it reads as a footnote to them rather than as a step of its own.
const TYPE_ORDER = [
  "bpmn:StartEvent", "bpmn:UserTask", "bpmn:ServiceTask", "bpmn:ScriptTask",
  "bpmn:BusinessRuleTask", "bpmn:SendTask", "bpmn:ReceiveTask", "bpmn:ManualTask",
  "bpmn:Task", "bpmn:CallActivity", "bpmn:SubProcess", "bpmn:Transaction",
  "bpmn:ExclusiveGateway", "bpmn:InclusiveGateway", "bpmn:ParallelGateway",
  "bpmn:EventBasedGateway", "bpmn:IntermediateCatchEvent",
  "bpmn:IntermediateThrowEvent", "bpmn:BoundaryEvent", "bpmn:EndEvent",
  "bpmn:SequenceFlow",
];

export function typeLabel(type) {
  return TYPE_LABELS[type] || String(type || "").replace(/^bpmn:/, "");
}

// documentationOf reads an element's <bpmn:documentation>. BPMN allows several
// entries; they are joined into one block of prose.
export function documentationOf(bo) {
  const docs = (bo && bo.documentation) || [];
  return docs.map((d) => (d && d.text ? String(d.text) : "")).filter(Boolean).join("\n\n").trim();
}

// codeFieldsOf reads the code an element carries and reproduces it in the
// document (ADR-0143): a script task's job source (PowerShell, Python or
// JavaScript, ADR-0047) and a flow's FEEL condition (ADR-0067). The prose says
// what a step is for; the code says what it actually runs, which is exactly what
// an auditor or a new engineer opening the document needs to see. The source is
// kept with its original whitespace, and its language travels with it.
export function codeFieldsOf(bo) {
  if (!bo) return [];
  const out = [];
  const ext = (bo.extensionElements && bo.extensionElements.values) || [];

  // Atlas carries a script task's job source as an <atlas:jobScript> extension
  // element (source body + language attribute).
  for (const e of ext) {
    if (e && /:JobScript$/.test(e.$type || "") && String(e.source || "").trim()) {
      out.push({ label: "Script", language: String(e.language || "").trim(), source: String(e.source) });
    }
  }
  // A plain BPMN <bpmn:script> body is read too, so an imported model documents
  // its scripts as well — the field is scriptFormat, not a moddle extension.
  if (String(bo.script || "").trim()) {
    out.push({ label: "Script", language: String(bo.scriptFormat || "").trim(), source: String(bo.script) });
  }

  // A FEEL condition on a sequence flow — the branch rule a reader most needs to
  // understand why a token went one way and not the other.
  const cond = bo.conditionExpression;
  if (cond && String(cond.body || "").trim()) {
    out.push({ label: "Condition", language: String(cond.language || "feel").trim() || "feel", source: String(cond.body) });
  }

  return out;
}

// extOf finds an extension element by the local name of its type, case-insensitively
// and whatever prefix a file declares. The moddle resolves a registered extension
// to "zeebe:CalledDecision"; a model authored by hand or by another tool may carry
// the same element spelled differently, and the document should still read it.
function extOf(bo, localName) {
  const values = (bo && bo.extensionElements && bo.extensionElements.values) || [];
  const want = String(localName).toLowerCase();
  return values.find((v) => String(v.$type || "").split(":").pop().toLowerCase() === want) || null;
}

// attrOf reads a moddle property whether the descriptor declared it (a plain
// property) or not (parked in $attrs).
function attrOf(el, name) {
  if (!el) return "";
  const direct = el[name];
  if (direct !== undefined && direct !== null && direct !== "") return String(direct);
  const raw = el.$attrs && el.$attrs[name];
  return raw === undefined || raw === null ? "" : String(raw);
}

// calledDecisionOf describes how a business rule task calls its decision — the
// part that is in the diagram and therefore never missing: which decision, how it
// binds (ADR-0063), what it writes, and the inputs it feeds in. A task backed by a
// temis Worker (ADR-0050) carries no local decision at all; it reports the worker
// instead, because the rules then live in that service and the document must not
// imply otherwise.
//
// Returns null for anything that is not a business rule task.
export function calledDecisionOf(bo) {
  if (!bo || bo.$type !== "bpmn:BusinessRuleTask") return null;
  const worker = extOf(bo, "temisConnector");
  const cd = extOf(bo, "calledDecision");
  const io = extOf(bo, "ioMapping");
  const inputs = ((io && io.inputParameters) || [])
    .map((p) => ({ name: String(p.target || ""), value: String(p.source || "") }))
    .filter((p) => p.name || p.value);
  // A hand-authored model may feed the decision through <decisionInput name= value=>
  // instead, which the compiler also accepts.
  for (const v of (bo.extensionElements && bo.extensionElements.values) || []) {
    if (String(v.$type || "").split(":").pop().toLowerCase() !== "decisioninput") continue;
    const name = attrOf(v, "name");
    const value = attrOf(v, "value");
    if (name || value) inputs.push({ name, value });
  }
  return {
    decisionId: attrOf(cd, "decisionId"),
    // ADR-0063: anything that is not "deployment" is latest, including an omitted
    // attribute — the same rule decisionBinding applies in the compiler.
    binding: attrOf(cd, "bindingType") === "deployment" ? "deployment" : "latest",
    resultVariable: attrOf(cd, "resultVariable"),
    retries: attrOf(cd, "retries"),
    worker: attrOf(worker, "connector"),
    external: !!worker,
    inputs,
  };
}

// Containers and decorations, which never earn a section of their own. Pools and
// lanes are structure rather than steps, and each element already reports the
// lane it sits in; annotations are the *source* of prose, not subjects of it.
const NOT_DOCUMENTABLE = /^bpmn:(Process|Collaboration|Definitions|Participant|Lane|LaneSet|TextAnnotation|Association|MessageFlow|DataObject|DataObjectReference|DataStoreReference|Group)$/;

// isDocumentable decides what earns a section. Every flow node does. A sequence
// flow only does when it carries a name, a condition, or its own prose — an
// unnamed flow says nothing a reader could not see in the picture, and listing
// all of them would bury the elements that matter.
function isDocumentable(bo) {
  const type = bo && bo.$type;
  if (!type || !type.startsWith("bpmn:")) return false;
  if (type === "bpmn:SequenceFlow") {
    return !!(bo.name || bo.conditionExpression || documentationOf(bo));
  }
  return !NOT_DOCUMENTABLE.test(type);
}

// collectDocumentation reads everything the document needs out of a live bpmn-js
// modeler: the process identity, its own prose, and one entry per element with
// the documentation and text annotations attached to it.
//
// Annotations are matched through <bpmn:association>, which is how BPMN attaches
// a note to an element. An annotation associated with nothing still appears — as
// a general note on the process — because a modeller who wrote it meant it to be
// read.
export function collectDocumentation(modeler) {
  let registry;
  try { registry = modeler.get("elementRegistry"); } catch { return emptyCollection(); }

  const businessObjects = [];
  registry.forEach((el) => {
    if (el.labelTarget) return; // a label shares its target's businessObject
    if (el.businessObject) businessObjects.push(el.businessObject);
  });

  // Annotations by the id of the element they are associated with.
  const annotationsFor = new Map();
  const generalNotes = [];
  const annotationById = new Map();
  for (const bo of businessObjects) {
    if (bo.$type === "bpmn:TextAnnotation") annotationById.set(bo.id, (bo.text || "").trim());
  }
  const attached = new Set();
  for (const bo of businessObjects) {
    if (bo.$type !== "bpmn:Association") continue;
    const src = bo.sourceRef && bo.sourceRef.id;
    const tgt = bo.targetRef && bo.targetRef.id;
    // An association may point either way; the annotation is whichever end is one.
    const [noteId, elementId] = annotationById.has(tgt) ? [tgt, src] : [src, tgt];
    const text = annotationById.get(noteId);
    if (!text || !elementId) continue;
    attached.add(noteId);
    if (!annotationsFor.has(elementId)) annotationsFor.set(elementId, []);
    annotationsFor.get(elementId).push(text);
  }
  for (const [id, text] of annotationById) {
    if (!attached.has(id) && text) generalNotes.push(text);
  }

  // Lane membership, so a documented element says who owns it (ADR-0121).
  const laneOf = new Map();
  for (const bo of businessObjects) {
    if (bo.$type !== "bpmn:Lane") continue;
    for (const node of bo.flowNodeRef || []) {
      if (node && node.id) laneOf.set(node.id, bo.name || bo.id);
    }
  }

  const elements = [];
  for (const bo of businessObjects) {
    if (!isDocumentable(bo)) continue;
    elements.push({
      id: bo.id,
      type: bo.$type,
      name: (bo.name || "").trim(),
      documentation: documentationOf(bo),
      annotations: annotationsFor.get(bo.id) || [],
      lane: laneOf.get(bo.id) || "",
      code: codeFieldsOf(bo),
      // Null for everything but a business rule task, which is the one element
      // whose behaviour lives entirely off the diagram.
      decision: calledDecisionOf(bo),
    });
  }

  // Documented elements sort by the reader's path through the process, then by
  // name so the order is stable between exports of an unchanged model.
  elements.sort((a, b) => {
    const ra = TYPE_ORDER.indexOf(a.type);
    const rb = TYPE_ORDER.indexOf(b.type);
    if (ra !== rb) return (ra < 0 ? TYPE_ORDER.length : ra) - (rb < 0 ? TYPE_ORDER.length : rb);
    return (a.name || a.id).localeCompare(b.name || b.id);
  });

  const root = rootBusinessObject(modeler);
  const process = processBusinessObject(modeler, businessObjects);
  return {
    processId: (process && process.id) || (root && root.id) || "",
    processName: (process && process.name) || (root && root.name) || "",
    processDocumentation: documentationOf(process),
    generalNotes,
    elements,
  };
}

function emptyCollection() {
  return { processId: "", processName: "", processDocumentation: "", generalNotes: [], elements: [] };
}

function rootBusinessObject(modeler) {
  try { return modeler.get("canvas").getRootElement().businessObject || null; } catch { return null; }
}

// processBusinessObject finds the process the document is about. In a plain
// diagram that is the root; in a collaboration the root is the collaboration, so
// the first participant's process stands in.
function processBusinessObject(modeler, businessObjects) {
  const root = rootBusinessObject(modeler);
  if (root && /:Process$/.test(root.$type || "")) return root;
  for (const bo of businessObjects) {
    if (bo.$type === "bpmn:Participant" && bo.processRef) return bo.processRef;
  }
  return root;
}


const GREY = [0.42, 0.42, 0.42];

// BINDING_NOTE says what a binding means, once, where the reader meets it. "latest"
// and "deployment" are precise to somebody who has read ADR-0063 and opaque to
// everybody else, and this document is written for everybody else.
const BINDING_NOTE = {
  latest: "latest — the newest version deployed at the time this process is deployed",
  deployment: "deployment — pinned to the model deployed together with this process",
};

// resolveCalledDecisions fetches the rules behind every decision the diagram calls,
// so the document can show them
// (ADR-0328). One request per
// distinct decision id, in parallel, and a decision that cannot be resolved simply
// has no entry — the section then documents the call without the table rather than
// failing the export.
//
// The source is the model behind a DMN reference where there is one, because that
// is the authoring version under change control and the model a `deployment`-bound
// task would bundle. A decision that exists only as a deployment (ADR-0322, made an
// ordinary state for a latest-bound task by
// ADR-0327) is read from that
// deployment's own source, and the document says so.
export async function resolveCalledDecisions(collection, api) {
  const wanted = [];
  for (const el of collection.elements || []) {
    const d = el.decision;
    if (!d || d.external || !d.decisionId) continue;
    if (!wanted.includes(d.decisionId)) wanted.push(d.decisionId);
  }
  const out = {};
  if (!wanted.length || typeof api !== "function") return out;

  let catalog = [];
  try { catalog = await api("GET", "/api/v1/decisions"); } catch { catalog = []; }
  if (!Array.isArray(catalog)) catalog = [];
  const refOf = new Map();
  for (const item of catalog) {
    if (item && item.id && item.modelRef && !refOf.has(item.id)) refOf.set(item.id, item.modelRef);
  }

  await Promise.all(wanted.map(async (id) => {
    const modelRef = refOf.get(id) || "";
    try {
      if (modelRef) {
        const xml = await api("GET", `/api/v1/dmn-models/${encodeURIComponent(modelRef)}/xml`);
        out[id] = { source: `Model ${modelRef}.dmn`, collection: collectDecisionDocumentation(String(xml)) };
        return;
      }
      // No reference resolves it, so the only rules that exist are the ones a
      // deployment carries. The newest is the one a latest-bound task would reach.
      const rows = await api("GET", `/api/v1/decision-deployments?decisionId=${encodeURIComponent(id)}`);
      if (!Array.isArray(rows)) return;
      const current = rows.find((r) => r && r.current) || rows[0];
      if (!current || !current.key) return;
      const xml = await api("GET", `/api/v1/decision-deployments/${current.key}/xml`);
      out[id] = {
        source: `Deployed decision v${current.version} (key ${current.key})`,
        collection: collectDecisionDocumentation(String(xml)),
      };
    } catch {
      // One decision that cannot be read costs its table, not the document.
    }
  }));
  return out;
}

// renderCalledDecision draws a business rule task's decision into its section: how
// the diagram calls it, then the rules behind it. The first half is unconditional
// — it comes off the diagram — and the second appears when `decisions` resolved
// that id.
function renderCalledDecision(doc, call, decisions) {
  if (!call) return;
  if (call.external) {
    doc.paragraph(
      "Evaluated by the temis worker " + (call.worker || "(unnamed)") +
      ". The rules live in that service and are not part of this model.",
      { size: 9.5, color: GREY, after: 6 },
    );
    return;
  }
  if (!call.decisionId) {
    doc.paragraph("This business rule task names no decision yet.", { size: 9.5, color: GREY, after: 6 });
    return;
  }

  doc.keyValue("Decision", call.decisionId);
  doc.keyValue("Binding", BINDING_NOTE[call.binding] || call.binding);
  if (call.resultVariable) doc.keyValue("Result variable", call.resultVariable);
  if (call.inputs.length) {
    doc.paragraph("Inputs", { size: 9, bold: true, after: 2 });
    doc.table(
      [{ header: "Decision input", width: 1 }, { header: "Fed from", width: 1.6 }],
      call.inputs.map((i) => [i.name || "—", i.value || "—"]),
    );
  }

  const resolved = decisions && decisions[call.decisionId];
  if (!resolved) {
    doc.paragraph(
      "The rules behind this decision could not be read, so they are not reproduced here.",
      { size: 9.5, color: GREY, after: 6 },
    );
    return;
  }
  const dec = (resolved.collection.decisions || []).find((d) => d.id === call.decisionId);
  if (!dec) {
    doc.paragraph(
      `${resolved.source} does not declare ${call.decisionId}.`,
      { size: 9.5, color: GREY, after: 6 },
    );
    return;
  }
  doc.paragraph("Rules read from: " + resolved.source, { size: 8.5, color: GREY, after: 3 });
  if (dec.description) doc.paragraph(dec.description, { size: 9.5, after: 4 });
  renderDecisionBody(doc, dec);
}


// buildDocumentationPdf lays out the document: a cover naming the process and the
// version, the diagram, then one section per element carrying its prose. Returns
// the finished bytes.
export function buildDocumentationPdf(spec) {
  const {
    collection, diagram, title, note, version, createdAt, createdBy, deploymentVersion,
    // The decisions behind this process's business rule tasks, keyed by decision id
    // (resolveCalledDecisions). Absent is a legitimate state — the collector's
    // output alone still documents every call — so nothing here requires it.
    decisions,
  } = spec;
  const doc = new PdfDocument({
    title: title || collection.processName || collection.processId,
    footer: (title || collection.processId) + (version ? " · v" + version : ""),
  });

  doc.heading(title || collection.processName || collection.processId, 1);
  doc.paragraph("Process documentation", { size: 11, color: GREY, after: 12 });

  doc.keyValue("Process ID", collection.processId || "—");
  if (collection.processName) doc.keyValue("Name", collection.processName);
  if (version) doc.keyValue("Documentation version", "v" + version);
  if (deploymentVersion) doc.keyValue("Deployed version", "v" + deploymentVersion);
  doc.keyValue("Created", createdAt || new Date().toLocaleString());
  if (createdBy) doc.keyValue("Created by", createdBy);
  doc.keyValue("Elements documented", String(collection.elements.length));
  if (note) doc.keyValue("Note", note);
  doc.rule();

  if (collection.processDocumentation) {
    doc.heading("About this process", 2);
    doc.paragraph(collection.processDocumentation, { after: 8 });
  }

  if (diagram && diagram.bytes) {
    // A large, wide diagram gets its own landscape sheet so it is not squeezed
    // into an unreadable strip; the document then returns to portrait.
    const landscape = wantsLandscape(diagram, collection.elements.length);
    if (landscape) doc.landscapePage();
    doc.heading("Diagram", 2);
    doc.image(diagram.bytes, diagram.width, diagram.height);
    if (landscape) doc.portraitPage();
  }

  doc.heading("Elements", 2);
  if (collection.elements.length === 0) {
    doc.paragraph("This process has no documentable elements.", { color: GREY });
  }
  for (const el of collection.elements) {
    // An element's section is its title, the type/id line, and at least the
    // first line of what is written about it. Keeping those together is what
    // stops a name stranded at the foot of a page from its own description.
    doc.heading(el.name || el.id, 3, { keep: 58 });
    const meta = [typeLabel(el.type), "id: " + el.id];
    if (el.lane) meta.push("lane: " + el.lane);
    doc.paragraph(meta.join("  ·  "), { size: 8.5, color: GREY, after: 3 });
    if (el.documentation) {
      doc.paragraph(el.documentation, { size: 10, after: el.annotations.length ? 4 : 8 });
    } else if (el.annotations.length === 0) {
      doc.paragraph("No documentation.", { size: 9.5, color: GREY, after: 8 });
    }
    for (const note of el.annotations) {
      doc.paragraph("Note: " + note, { size: 9.5, indent: 12, after: 4 });
    }
    // The code the step runs — a script, a branch condition — set verbatim so a
    // reader can audit it, not merely read its name.
    for (const code of el.code || []) {
      doc.codeBlock(code.source, { label: code.label, language: code.language });
    }
    // A business rule task's code is its decision, which lives outside the diagram
    // — the one element the rule above could not reach until now.
    renderCalledDecision(doc, el.decision, decisions);
    doc.y += 4;
  }

  if (collection.generalNotes.length) {
    doc.heading("Unattached notes", 2);
    doc.paragraph(
      "Annotations in the diagram that are not associated with a particular element.",
      { size: 9.5, color: GREY, after: 6 },
    );
    for (const note of collection.generalNotes) doc.paragraph("• " + note, { indent: 8, after: 4 });
  }

  return doc.bytes();
}

// exportDocumentation is the whole act: read the model, rasterize the diagram,
// build the document, and publish it as the next version of this process. Returns
// the created version as the API rendered it.
export async function exportDocumentation({ modeler, api, title, note, createdBy }) {
  const collection = collectDocumentation(modeler);
  if (!collection.processId) throw new Error("the diagram has no process to document");

  const { svg } = await modeler.saveSVG();
  const diagram = await svgToJpeg(svg);
  const { xml } = await modeler.saveXML({ format: true });
  const decisions = await resolveCalledDecisions(collection, api);

  const bytes = buildDocumentationPdf({
    collection, diagram, title, note, createdBy, decisions,
    createdAt: new Date().toLocaleString(),
  });

  return api("POST", `/api/v1/processes/${encodeURIComponent(collection.processId)}/documentation`, {
    title: title || collection.processName || collection.processId,
    note: note || "",
    processName: collection.processName,
    xml,
    elements: collection.elements,
    pdfBase64: bytesToBase64(bytes),
  });
}
