package api

import (
	"fmt"
	"net/http"
	"testing"

	"github.com/pblumer/atlas/compiler"
)

// TestOffloadedKindIsLeasableByAWorker is the prerequisite ADR-0166 exposed: a
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

// TestEveryInProcessHandlerIsOffloadable is the guard the SOAP connector needed and
// did not have. TestEveryManagedKindCanBeOffloaded above walks offloadableKinds and
// checks each listed kind goes away, so it says nothing at all about a job type the
// map never mentions: a connector added with a handler but no entry passes it
// silently, and the kind is then one an operator can neither name nor relocate.
//
// This walks the other direction — from the handlers a booted server actually
// registered back to the map — so the omission fails here instead.
func TestEveryInProcessHandlerIsOffloadable(t *testing.T) {
	// Every optional registration path on, so the script and user-provisioning
	// handlers are present too and not silently skipped.
	srv := newServerWithOptions(t,
		WithUserProvisioning(),
		WithScriptWorker(compiler.PwshJobTypeIndex, nil),
		WithScriptWorker(compiler.PythonJobTypeIndex, nil),
		WithScriptWorker(compiler.JsJobTypeIndex, nil),
	)

	named := map[int32]string{}
	for name, types := range offloadableKinds {
		for _, jt := range types {
			named[jt] = name
		}
	}
	// The user-provisioning connector is deliberately absent from the map: it mutates
	// the run-loop-owned user store directly (ADR-0123), so there is nothing for a
	// worker to hold and no endpoint for it to reach. Every other handler must be
	// namable.
	engineOnly := map[int32]string{
		compiler.UserConnectorJobTypeIndex: "mutates the run-loop-owned user store, so it has no out-of-process form",
	}

	reserved := compiler.ReservedJobTypes()
	srv.do(func() {
		for i, name := range reserved {
			jt := int32(i)
			if !srv.jobRunner.Handles(jt) {
				continue
			}
			if _, ok := named[jt]; ok {
				continue
			}
			if why, ok := engineOnly[jt]; ok {
				t.Logf("%s runs in the engine on purpose: %s", name, why)
				continue
			}
			t.Errorf("%s has an in-process handler but no --offload-connectors kind names it, so an operator cannot move it to a worker (ADR-0164). Add it to offloadableKinds.", name)
		}
	})
}
