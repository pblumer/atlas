package gate

import (
	"context"
	"testing"

	"github.com/pblumer/atlas/workertype/evidence"
)

// TestEveryRecordOnThisBranchIsCurrent: main never carries evidence for code it no
// longer contains. A record whose implementation, suite or results moved fails here,
// and the message names the command that renews it.
func TestEveryRecordOnThisBranchIsCurrent(t *testing.T) {
	if err := evidence.VerifyAll(context.Background(), "../../..", "../records"); err != nil {
		t.Fatal(err)
	}
}
