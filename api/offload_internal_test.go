package api

import (
	"fmt"
	"net/http"
	"slices"
	"strings"
	"testing"

	"github.com/pblumer/atlas/compiler"
)

// TestOffloadedKindIsLeasableByAWorker is the prerequisite ADR-0168 exposed: a
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

// A blank entry is skipped rather than refused. `--offload-connectors csv,,rest` and
// a trailing comma are what an operator's shell history produces; failing startup on
// one would be strictness with no safety in it — unlike a *misspelled* kind, a blank
// cannot be mistaken for a relocation that happened.
func TestOffloadingIgnoresBlankEntries(t *testing.T) {
	srv := newServerWithOptions(t, WithOffloadedConnectorKinds([]string{"csv", "", "  ", "rest"}))
	srv.do(func() {
		for _, jt := range []int32{compiler.CsvImportJobTypeIndex, compiler.RestJobTypeIndex} {
			if srv.jobRunner.Handles(jt) {
				t.Errorf("job type %d still has an in-process handler; the blank entries broke the list", jt)
			}
		}
	})
}

// TestEveryDefaultOffloadedKindCanBeServedByItsWorker is the safety property behind
// making the default opt-out. A supervised worker inherits this process's environment
// but not its connector store, so a managed kind — whose endpoint and password live
// in that store — can only be defaulted if the engine hands that configuration to the
// child at spawn. Default a managed kind the engine does not provision and every task
// of it fails on a worker nobody configured, which is the one outcome the opt-out
// default must never produce.
func TestEveryDefaultOffloadedKindCanBeServedByItsWorker(t *testing.T) {
	managed := map[string]bool{}
	for _, k := range managedConnectorKinds {
		managed[k.name] = true
	}
	provisioned := (&Server{}).provisionedConnectorKinds()
	for _, kind := range DefaultOffloadedKinds() {
		if _, ok := offloadableKinds[kind]; !ok {
			t.Errorf("default kind %q is not offloadable at all", kind)
		}
		if _, handed := provisioned[kind]; managed[kind] && !handed {
			t.Errorf("default kind %q is managed but not provisioned: its credentials live in the "+
				"connector store, which a supervised worker cannot read and is not handed", kind)
		}
	}
}

// Active Directory is offloaded by default, and the reason it can be is that the
// engine hands its bind passwords over (ADR-0182).
//
// AD is not a managed kind, so the property above does not cover it: it holds no
// connector record, and its secret is a per-task *reference* the model authors. That
// reference resolves out of the vault, which a supervised worker cannot read any more
// than it can read the connector store — so defaulting AD without provisioning it
// would move every vault-backed directory task to a worker with nothing to bind with,
// which is the same outcome the property above exists to prevent, reached by a
// different route.
func TestActiveDirectoryIsOffloadedByDefaultAndItsBindSecretsAreHandedOver(t *testing.T) {
	if !slices.Contains(DefaultOffloadedKinds(), "ad") {
		t.Error("ad is not offloaded by default; a directory task runs on the engine's own loop")
	}
	if _, handed := (&Server{}).provisionedConnectorKinds()["ad"]; !handed {
		t.Error("ad is defaulted but its bind secrets are not handed to the supervised worker; " +
			"every vault-backed AD task would fail on a worker nobody configured")
	}
}

// The check above is only worth anything if it could fail, and it could not if every
// managed kind were provisioned. Naming the ones that are not is what keeps it a
// real constraint rather than a tautology that grew one.
func TestSomeManagedKindsAreStillNotProvisioned(t *testing.T) {
	provisioned := (&Server{}).provisionedConnectorKinds()
	var unprovisioned []string
	for _, k := range managedConnectorKinds {
		if _, handed := provisioned[k.name]; !handed {
			unprovisioned = append(unprovisioned, k.name)
		}
	}
	if len(unprovisioned) == 0 {
		t.Fatal("every managed kind is provisioned, so the default-set check can no longer fail; " +
			"give it something else to hold, or drop it")
	}
	t.Logf("managed kinds a supervised worker is not handed: %s", strings.Join(unprovisioned, ", "))
}
