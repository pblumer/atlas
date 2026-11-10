package gate

import (
	"context"
	"testing"

	"github.com/pblumer/atlas/workertype/evidence"
	// Every registered suite, so a record verifies here exactly as it renews with
	// the evidence command.
	_ "github.com/pblumer/atlas/workertype/evidence/suites/calendarbooking"
	_ "github.com/pblumer/atlas/workertype/evidence/suites/entrachannel"
)

// TestEveryRecordOnThisBranchIsCurrent: main never carries evidence for code it no
// longer contains. A record whose implementation, suite or results moved fails here,
// and the message names the command that renews it.
func TestEveryRecordOnThisBranchIsCurrent(t *testing.T) {
	if err := evidence.VerifyAll(context.Background(), "../../..", "../records"); err != nil {
		t.Fatal(err)
	}
}
