// Package gate holds the CI check over the evidence records in
// workertype/evidence/records and nothing else: its test verifies every record
// against the module, with every registered suite, so a change that moves what a
// record binds fails until the same change renews the record (ADR-0483).
package gate
