// device-ops.js — what the Modeler knows about the device Worker's operations
// (ADR-draft-device-workers-for-the-physical-world): each operation's label, which
// values it needs and takes, and what it returns.
//
// It is a copy of connector/device/catalog, the table the compiler and the device
// read. A copy is unavoidable — the panel runs in a browser — so it is held to the Go
// table field by field by TestModelerKnowsEveryDeviceOperation
// (api/device_modeler_test.go): a row that differed would offer a field the deploy then
// refuses, or hide one the operation needs.
//
import { t } from "./i18n.js";

// One row per line, in the shape the test reads; keep it that way.
export const DEVICE_OPERATIONS = [
  { v: "digital.write", l: "pnl.ops.device.op.digital.write", needs: ["output", "on"], takes: [], result: "" },
  { v: "digital.read", l: "pnl.ops.device.op.digital.read", needs: ["line"], takes: [], result: "object" },
  { v: "pulse", l: "pnl.ops.device.op.pulse", needs: ["output", "durationMs"], takes: ["on"], result: "" },
];

/** The row for an operation, or undefined. */
export function deviceOperation(name) {
  return DEVICE_OPERATIONS.find((o) => o.v === name);
}

/** The operation dropdown's options. */
export function deviceOperationOptions() {
  return DEVICE_OPERATIONS.map((o) => ({ v: o.v, get l() { return t(o.l); } }));
}

/** Whether the operation needs or takes a value — what decides whether its field shows. */
export function deviceUses(name, value) {
  const op = deviceOperation(name);
  return !!op && (op.needs.includes(value) || op.takes.includes(value));
}

/** The result variable's FEEL type for the type hints: object, or none. */
export function deviceResultType(name) {
  return deviceOperation(name)?.result || "";
}

/** What the result variable receives, for its hint. */
export function deviceResultShape(name) {
  return name === "digital.read" ? t("pnl.ops.device.result.read") : "";
}

/** The on hint, which differs between a switch and a pulse. */
export function deviceOnHint(name) {
  return t(name === "pulse" ? "pnl.ops.device.onHint.pulse" : "pnl.ops.device.onHint.write");
}
