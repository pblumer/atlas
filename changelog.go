package atlas

import _ "embed"

// Changelog is CHANGELOG.md as this binary was built from it. The Console's release
// notes are read from it while the server runs (ADR-0444),
// so a changelog entry reaches the Console with nothing generated, committed or
// regenerated in between.
//
// It is embedded here because go:embed reaches only files at or below the directory of
// the package that embeds them, and the module root is the one package CHANGELOG.md
// sits in.
//
//go:embed CHANGELOG.md
var Changelog string

// ADRIndex is the index of the decision records, docs/adr/README.md. The release notes
// read the file name of each record from it, so an entry that cites "ADR-0438" links to
// that record rather than to the directory it is in: a record's file name cannot be
// derived from its number, and most entries cite the number alone. `go test ./docs/adr`
// keeps the index in step with the directory.
//
//go:embed docs/adr/README.md
var ADRIndex string
