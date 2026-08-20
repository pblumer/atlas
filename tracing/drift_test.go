package tracing

import (
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"strings"
	"testing"
)

// TestTheWriterIsNeverInstrumented is the invariant guard for this slice.
//
// A span is an allocation, a clock read, and a lock on a shared processor. On the HTTP
// surface that is nothing — the work is already per-request and measured in milliseconds.
// On the batch loop it would be all three per batch, on the one goroutine that owns the
// partition, which is precisely what invariants I1 (no hot-path allocation) and I3
// (single writer) exist to prevent.
//
// The rule is easy to state and easy to break by accident, because instrumenting the
// engine is the obvious next thing anyone would reach for when a trace stops at the API
// boundary. So it is checked: the durable core does not import a tracing package, ours
// or OpenTelemetry's. If a future slice needs engine spans, it needs an ADR and a
// benchmark first, not an import.
func TestTheWriterIsNeverInstrumented(t *testing.T) {
	// The engine and everything it writes through. api/ and cmd/ are deliberately absent:
	// that is where tracing belongs.
	core := []string{"engine", "state", "wal", "model", "compiler", "checkpoint"}
	banned := []string{"go.opentelemetry.io/", "github.com/pblumer/atlas/tracing"}

	fset := token.NewFileSet()
	var offenders []string
	for _, dir := range core {
		err := filepath.WalkDir(filepath.Join("..", dir), func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() || !strings.HasSuffix(path, ".go") {
				return nil
			}
			f, perr := parser.ParseFile(fset, path, nil, parser.ImportsOnly)
			if perr != nil {
				return perr
			}
			for _, imp := range f.Imports {
				p := strings.Trim(imp.Path.Value, `"`)
				for _, b := range banned {
					if strings.HasPrefix(p, b) {
						offenders = append(offenders, fset.Position(imp.Pos()).String()+": "+p)
					}
				}
			}
			return nil
		})
		if err != nil {
			t.Fatalf("walk %s: %v", dir, err)
		}
	}
	if len(offenders) > 0 {
		t.Errorf("the durable core imports a tracing package — a span on the batch loop costs an "+
			"allocation, a clock read and a lock on the single writer (invariants I1 and I3):\n\t%s",
			strings.Join(offenders, "\n\t"))
	}
}
