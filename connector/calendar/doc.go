// Package calendar finds free slots in, and books into, the calendars organisations
// already keep their time in — Microsoft 365, Google, CalDAV and Exchange Server — on
// behalf of a BPMN service task (ADR-0552).
//
// # Worker-only
//
// Like the SQL and CardDAV Worker Types, this one has no in-process half: a calendar
// credential reaches every appointment of the accounts it may read, so the engine never
// holds one (ADR-0164, ADR-0168). The engine resolves a [Job] of plain values; [Run]
// executes it against the [Backend] the worker registered under the job's Worker name.
//
// # One contract, several backends
//
// The slot grid, the conflict rule and the result shapes live here, once. A [Backend]
// is the provider's half: read the events of a window, create a placeholder under a
// key, update, delete, read one. Every operation claims a technical capability contract
// under the one profile free-busy-calendar, and the conformance suites run each case
// against every backend (ADR-0480, ADR-0483).
//
// # The conflict rule
//
// No calendar offers "create only if free". reserve-slot creates a placeholder, reads
// the slot, and gives it up when anything else takes any of its time; see [reserve]
// for why at most one of two competing reservations survives, and what the rule needs
// of the provider.
package calendar
