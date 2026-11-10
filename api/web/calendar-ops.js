// calendar-ops.js — what the Modeler knows about the calendar Worker's operations
// (ADR-0552): each operation's label, the keys its input object
// takes and needs, and whether it returns something.
//
// It is a copy of connector/calendar/catalog, the table the compiler and the worker
// read. A copy is unavoidable — the panel runs in a browser — so it is held to the Go
// table field by field by TestModelerKnowsEveryCalendarOperation
// (api/calendar_modeler_test.go).

import { t } from "./i18n.js";

// One row per line, in the shape the test reads; keep it that way.
export const CALENDAR_OPERATIONS = [
  { v: "find-free-slots", l: "pnl.ops.calendar.op.find-free-slots", needs: ["calendars", "from", "to", "durationMinutes", "timeZone", "openingHours"], takes: ["gridMinutes", "bufferMinutes", "minLeadMinutes", "closedDates", "require", "maxResults"], result: "object" },
  { v: "reserve-slot", l: "pnl.ops.calendar.op.reserve-slot", needs: ["calendar", "start", "end"], takes: ["subject", "timeZone"], result: "object" },
  { v: "confirm-booking", l: "pnl.ops.calendar.op.confirm-booking", needs: ["calendar", "bookingId"], takes: ["subject", "body", "attendees", "timeZone"], result: "object" },
  { v: "cancel-booking", l: "pnl.ops.calendar.op.cancel-booking", needs: ["calendar", "bookingId"], takes: [], result: "" },
  { v: "get-booking", l: "pnl.ops.calendar.op.get-booking", needs: ["calendar", "bookingId"], takes: ["timeZone"], result: "object" },
];

/** The row for an operation, or undefined. */
export function calendarOperation(name) {
  return CALENDAR_OPERATIONS.find((o) => o.v === name);
}

/** The operation dropdown's options. */
export function calendarOperationOptions() {
  return CALENDAR_OPERATIONS.map((o) => ({ v: o.v, get l() { return t(o.l); } }));
}

/** The result variable's FEEL type for the type hints: object, or none. */
export function calendarResultType(name) {
  return calendarOperation(name)?.result || "";
}

/** What the result variable receives, for its hint. */
export function calendarResultShape(name) {
  switch (name) {
    case "find-free-slots":
      return t("pnl.ops.calendar.result.slots");
    case "reserve-slot":
    case "confirm-booking":
      return t("pnl.ops.calendar.result.booking");
    case "get-booking":
      return t("pnl.ops.calendar.result.get");
  }
  return t("pnl.ops.resultNone");
}

/** The input hint: the keys the operation needs and takes. */
export function calendarInputHint(name) {
  const op = calendarOperation(name);
  if (!op) return "";
  return t("pnl.ops.calendar.input", { needs: op.needs.join(", "), takes: op.takes.length ? op.takes.join(", ") : "—" });
}

/** An input object to start from, as =expressions to replace. */
export function calendarInputPlaceholder(name) {
  switch (name) {
    case "find-free-slots":
      return '{\n  "calendars": ["werkstatt@firma.ch"],\n  "from": "=string(today())",\n  "to": "=string(today() + duration(\\"P14D\\"))",\n  "durationMinutes": "=dienstleistung.dauer",\n  "timeZone": "Europe/Zurich",\n  "openingHours": {\n    "mon": [{ "from": "08:00", "to": "12:00" }, { "from": "13:30", "to": "18:00" }],\n    "sat": [{ "from": "09:00", "to": "16:00" }]\n  },\n  "minLeadMinutes": 120\n}';
    case "reserve-slot":
      return '{\n  "calendar": "=termin.calendar",\n  "start": "=termin.start",\n  "end": "=termin.end",\n  "subject": "=dienstleistung.name",\n  "timeZone": "Europe/Zurich"\n}';
    case "confirm-booking":
      return '{\n  "calendar": "=buchung.calendar",\n  "bookingId": "=buchung.bookingId",\n  "subject": "=dienstleistung.name + \\" – \\" + kunde.name",\n  "body": "=kunde.mitteilung",\n  "attendees": [{ "email": "=kunde.email", "name": "=kunde.name" }]\n}';
  }
  return '{\n  "calendar": "=buchung.calendar",\n  "bookingId": "=buchung.bookingId"\n}';
}
