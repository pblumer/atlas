package api

import (
	"fmt"
	"net/http"
	"testing"

	"github.com/pblumer/atlas/compiler"
)

// TestOffloadedKindIsLeasableByAWorker is the prerequisite ADR-0165 exposed: a
// connector kind cannot move to a worker while an in-process handler serves it,
// because the pull refuses such a type — that refusal is what keeps work from being
// done twice. Turning the handler off is therefore the operative act of relocating
// a kind, and it has to be something an operator can do.
func TestOffloadedKindIsLeasableByAWorker(t *testing.T) {
	srv := newServerWithOptions(t, WithOffloadedConnectorKinds([]string{connectorKindMail}))

	// Served in-process by default; refused to a worker, and marked in the view.
	plain := newServerForErrors(t)
	if code, _ := pull(t, plain, fmt.Sprintf(`{"type":%q,"worker":"w1"}`, compiler.MailJobType)); code != http.StatusConflict {
		t.Errorf("pull of a served kind: status=%d, want 409", code)
	}

	// Offloaded: no in-process handler, so a worker may lease it. The queue is empty,
	// which is a 200 with no jobs rather than the 409 a served kind gives.
	code, got := pull(t, srv, fmt.Sprintf(`{"type":%q,"worker":"w1"}`, compiler.MailJobType))
	if code != http.StatusOK {
		t.Fatalf("pull of an offloaded kind: status=%d, want 200", code)
	}
	if len(got.Jobs) != 0 {
		t.Errorf("pulled %d jobs from an empty queue", len(got.Jobs))
	}
}

// The Workers view must report the change, or an operator has no way to see which
// kinds this server still runs itself.
func TestOffloadedKindIsReportedAsLeasable(t *testing.T) {
	srv := newServerWithOptions(t, WithOffloadedConnectorKinds([]string{connectorKindMail}))
	for _, row := range workers(t, srv).Types {
		if row.Type != compiler.MailJobType {
			continue
		}
		if row.ServedInProcess {
			t.Error("an offloaded kind still reports as served in-process")
		}
		if !row.Leasable {
			t.Error("an offloaded kind is not reported as leasable, so nothing can take it")
		}
		return
	}
	t.Fatal("no row for the mail job type")
}

// An unknown kind name is a startup error rather than a silent no-op: an operator
// who misspells one would otherwise believe a kind was relocated when it was not,
// and the work would keep running in the engine.
func TestOffloadingAnUnknownKindFails(t *testing.T) {
	if _, err := newServerWithOptionsErr(t, WithOffloadedConnectorKinds([]string{"no-such-kind"})); err == nil {
		t.Error("offloading an unknown connector kind succeeded, want an error naming it")
	}
}

// Every kind can be named, so the flag cannot be a partial list that quietly omits
// one and leaves it running in the engine.
func TestEveryManagedKindCanBeOffloaded(t *testing.T) {
	names := offloadableKindNames()
	srv := newServerWithOptions(t, WithOffloadedConnectorKinds(names))
	srv.do(func() {
		for name, types := range offloadableKinds {
			for _, jt := range types {
				if srv.jobRunner.Handles(jt) {
					t.Errorf("%s still has an in-process handler after every kind was offloaded", name)
				}
			}
		}
	})
}
