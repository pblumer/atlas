// atlasreport-ops.js — what the Modeler knows about the Atlas report Worker's operations
// (ADR-0555 §3): each operation's label, the keys its input object takes and needs, and
// that each returns a report in the atlas.report/1 shape a form's chart draws.
//
// It is a copy of connector/atlasreport/catalog, the table the compiler and the worker
// read. A copy is unavoidable — the panel runs in a browser — so it is held to the Go
// table field by field by TestModelerKnowsEveryAtlasReportOperation
// (api/atlasreport_modeler_test.go).

import { t } from "./i18n.js";

// One row per line, in the shape the test reads; keep it that way.
export const ATLASREPORT_OPERATIONS = [
  { v: "instances-summary", l: "pnl.ops.atlasreport.op.instances-summary", needs: [], takes: ["process", "title", "language"], result: "object" },
  { v: "process-statistics", l: "pnl.ops.atlasreport.op.process-statistics", needs: ["process"], takes: ["from", "to", "buckets", "bucket", "title", "language"], result: "object" },
  { v: "incident-summary", l: "pnl.ops.atlasreport.op.incident-summary", needs: [], takes: ["process", "title", "language"], result: "object" },
  { v: "delivery-report", l: "pnl.ops.atlasreport.op.delivery-report", needs: [], takes: ["catalog", "from", "to", "title", "language"], result: "object" },
];

/** The row for an operation, or undefined. */
export function atlasReportOperation(name) {
  return ATLASREPORT_OPERATIONS.find((o) => o.v === name);
}

/** The operation dropdown's options. */
export function atlasReportOperationOptions() {
  return ATLASREPORT_OPERATIONS.map((o) => ({ v: o.v, get l() { return t(o.l); } }));
}

/** The result variable's FEEL type for the type hints: every operation answers a report. */
export function atlasReportResultType(name) {
  return atlasReportOperation(name)?.result || "";
}

/** What the result variable receives, for its hint: the datasets the operation fills. */
export function atlasReportResultShape(name) {
  return atlasReportOperation(name) ? t("pnl.ops.atlasreport.result." + name) : "";
}

/** The input hint: the keys the operation needs and takes. */
export function atlasReportInputHint(name) {
  const op = atlasReportOperation(name);
  if (!op) return "";
  return t("pnl.ops.atlasreport.input", { needs: op.needs.length ? op.needs.join(", ") : "—", takes: op.takes.length ? op.takes.join(", ") : "—" });
}

/** An input object to start from. */
export function atlasReportInputPlaceholder(name) {
  switch (name) {
    case "process-statistics":
      return '{\n  "process": "bestellung",\n  "buckets": 8,\n  "bucket": "week",\n  "title": "Bestellungen der letzten acht Wochen",\n  "language": "de"\n}';
    case "delivery-report":
      return '{\n  "catalog": "it-services",\n  "from": "=string(today() - duration(\\"P30D\\"))",\n  "to": "=string(today())",\n  "language": "de"\n}';
  }
  return '{\n  "title": "Monatsbericht",\n  "language": "de"\n}';
}
