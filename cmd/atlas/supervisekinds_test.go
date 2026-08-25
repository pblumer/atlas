package main

import (
	"strings"
	"testing"

	"github.com/pblumer/atlas/api"
)

// The gap these cover, named as a follow-up by ADR-0181 itself: trying the AD
// connector's mock mode "requires --offload-connectors ad and a worker". Offloading
// alone only parks the jobs — the server supervises a worker for the four default
// kinds and for nothing else, and `--supervise id=type=command` cannot name a
// built-in connector. On a server with --auth an external worker cannot fill the gap
// either: the job pull is authenticated, and the only bearer credentials are this
// server's ephemeral internal token (handed to its own children) and a deploy token
// allowlisted to two endpoints. So the kind's jobs park forever.
//
// --supervise-connector closes it by asking for the same thing the defaults get.

func specIDs(specs []api.SuperviseSpec) string {
	ids := make([]string, 0, len(specs))
	for _, s := range specs {
		ids = append(ids, s.ID)
	}
	return strings.Join(ids, ",")
}

// TestSuperviseConnectorAsksForAWorkerAndStopsRunningItHere is the whole point: the
// named kind gets a worker of its own, and the engine stops working those jobs
// itself — the same pairing the default kinds get, which is what makes the worker
// the one that leases them.
func TestSuperviseConnectorAsksForAWorkerAndStopsRunningItHere(t *testing.T) {
	specs, offload, err := superviseConnectorSpecs([]string{"ad"}, nil)
	if err != nil {
		t.Fatalf("superviseConnectorSpecs: %v", err)
	}
	if len(specs) != 1 {
		t.Fatalf("specs = %v, want exactly one", specIDs(specs))
	}
	got := specs[0]
	if got.ID != "ad" || len(got.Kinds) != 1 || got.Kinds[0] != "ad" ||
		len(got.Connectors) != 1 || got.Connectors[0] != "ad" {
		t.Fatalf("spec = %+v, want a worker serving the ad connector under its own id", got)
	}
	if len(offload) != 1 || offload[0] != "ad" {
		t.Fatalf("offload = %v, want [ad] so the engine stops handling those jobs", offload)
	}
}

// TestAWorkerOnlyKindIsSupervisedWithoutBeingOffloaded draws the line the offload
// list cannot: entra has no in-process handler at all, so there is nothing to take
// away from the engine. Passing it to --offload-connectors is refused at startup as
// an unknown kind, and asking for its worker must not walk into that refusal.
func TestAWorkerOnlyKindIsSupervisedWithoutBeingOffloaded(t *testing.T) {
	specs, offload, err := superviseConnectorSpecs([]string{"entra"}, nil)
	if err != nil {
		t.Fatalf("superviseConnectorSpecs: %v", err)
	}
	if len(specs) != 1 || specs[0].ID != "entra" {
		t.Fatalf("specs = %v, want a worker for entra", specIDs(specs))
	}
	if len(offload) != 0 {
		t.Fatalf("offload = %v, want none: entra runs nowhere but on a worker", offload)
	}
}

// TestAKindAlreadySupervisedIsNotStartedTwice keeps the flag idempotent against the
// defaults. Two workers leasing one kind is not an error the operator would see —
// it is two processes racing for the same jobs.
func TestAKindAlreadySupervisedIsNotStartedTwice(t *testing.T) {
	already := []api.SuperviseSpec{{ID: "mail", Kinds: []string{"mail"}, Connectors: []string{"mail"}}}
	specs, offload, err := superviseConnectorSpecs([]string{"mail", "ad"}, already)
	if err != nil {
		t.Fatalf("superviseConnectorSpecs: %v", err)
	}
	if len(specs) != 1 || specs[0].ID != "ad" {
		t.Fatalf("specs = %v, want only the kind that is not supervised yet", specIDs(specs))
	}
	if len(offload) != 1 || offload[0] != "ad" {
		t.Fatalf("offload = %v, want only [ad]", offload)
	}
}

// TestAnUnknownConnectorKindIsRefused mirrors --offload-connectors: a misspelled
// kind must not read as "asked for and quietly not started", which is indis-
// tinguishable from the parking this flag exists to end.
func TestAnUnknownConnectorKindIsRefused(t *testing.T) {
	_, _, err := superviseConnectorSpecs([]string{"activedirectory"}, nil)
	if err == nil {
		t.Fatal("superviseConnectorSpecs with an unknown kind: want an error, got nil")
	}
	if !strings.Contains(err.Error(), "activedirectory") || !strings.Contains(err.Error(), "ad") {
		t.Fatalf("error = %v, want the offending name and the kinds that exist", err)
	}
}

// TestNoKindsAsksForNothing keeps the flag off by default: the platform owns process
// lifecycle unless an operator says otherwise (ADR-0157).
func TestNoKindsAsksForNothing(t *testing.T) {
	specs, offload, err := superviseConnectorSpecs(nil, nil)
	if err != nil || len(specs) != 0 || len(offload) != 0 {
		t.Fatalf("superviseConnectorSpecs(nil) = %v, %v, %v; want nothing at all", specIDs(specs), offload, err)
	}
}
