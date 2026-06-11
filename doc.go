// Package chrampfer is a blazing-fast, durable BPMN 2.x workflow engine.
//
// Chrampfer compiles BPMN models into a flat, integer-indexed execution graph,
// records every state transition as an append-only event in a write-ahead log,
// and materializes live state in an embedded key-value store. See the documents
// in docs/ for the architecture, design decisions, and roadmap.
//
// This is the module root. Implementation packages live in subdirectories and
// will be added as the project develops; see ROADMAP.md for status.
package chrampfer
