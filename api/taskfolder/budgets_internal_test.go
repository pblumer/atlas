package taskfolder

import (
	"testing"

	"github.com/pblumer/atlas/limits"
)

// TestAServiceLiteralStillHasBudgets pins what makes the zero value safe. The zero
// Limits is every ceiling at zero, and a ceiling of zero admits nothing — a service
// built as a struct literal would refuse every body as unreadable, which reads as a
// bad request rather than as missing configuration. New always sets them, so this
// branch only ever fires for a literal, which is exactly why it needs a test of its
// own: nothing else would notice if it stopped working.
func TestAServiceLiteralStillHasBudgets(t *testing.T) {
	if got := (&Service{}).budgets(); got != limits.Default() {
		t.Errorf("a Service literal has %+v, want the defaults", got)
	}
	want := limits.Default()
	want.ModelUpload = 4242
	if got := (&Service{Limits: want}).budgets(); got != want {
		t.Errorf("budgets = %+v, want the configured %+v", got, want)
	}
}
