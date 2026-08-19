// Package benchmarks holds Atlas's reproducible performance harness (work
// programme B of the v0.2.0 "Proof of Reliability & Performance" initiative).
//
// It has no library code of its own — every benchmark and its harness live in
// _test.go files, run with `go test -bench` — so importing this package does
// nothing and it adds no statements to the repository coverage floor. This file
// exists only to give the directory a documented package home.
//
// The current suite measures the *pure engine* under the *durable* profile: a
// real segmented WAL with a group-commit fsync on every batch and a real Pebble
// state store, over a temporary directory. It does not yet exercise the
// end-to-end HTTP/API path, an in-memory/no-fsync profile, latency percentiles,
// or recovery — those are deliberately deferred to later slices of programme B.
//
// See benchmarks/README.md for the exact commands, the workloads, the metrics
// and how to read them, the environment metadata to record, and the standing
// caveat that any number here is specific to one machine and one commit — never
// a universal product claim.
package benchmarks
