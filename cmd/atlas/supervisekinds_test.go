package main

import (
	"strings"
	"testing"

	"github.com/pblumer/atlas/api"
	"github.com/pblumer/atlas/connector/script"
)

// The gap these cover, named as a follow-up by ADR-0181 itself: trying the AD
// worker's mock mode "requires --offload-connectors ad and a worker". Offloading
// alone only parks the jobs — the server supervises a worker for the four default
// kinds and for nothing else, and `--supervise id=type=command` cannot name a
// built-in worker. On a server with --auth an external worker cannot fill the gap
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

// TestDefaultScriptWorkerHonorsLanguageFlags covers the default out-of-process
// path. The language flags used to affect only in-process handlers while the
// supervised script worker silently registered all three languages.
func TestDefaultScriptWorkerHonorsLanguageFlags(t *testing.T) {
	specs := defaultSuperviseSpecs([]string{"csv", "script", "mail"}, map[string]bool{
		"powershell": false,
		"python":     true,
		"javascript": false,
	}, script.SandboxStrict)
	if len(specs) != 3 {
		t.Fatalf("specs = %s, want csv,script,mail", specIDs(specs))
	}
	if got := strings.Join(specs[1].ScriptLanguages, ","); got != "python" {
		t.Errorf("script languages = %q, want python", got)
	}
	if got := specs[1].ScriptSandbox; got != "strict" {
		t.Errorf("script sandbox = %q, want strict", got)
	}
}

// TestDefaultScriptWorkerIsNotStartedWhenEveryLanguageIsDisabled makes the three
// opt-out flags an actual containment switch. Script jobs remain durable and park;
// the other default workers are unaffected.
func TestDefaultScriptWorkerIsNotStartedWhenEveryLanguageIsDisabled(t *testing.T) {
	specs := defaultSuperviseSpecs([]string{"csv", "script", "mail"}, map[string]bool{
		"powershell": false,
		"python":     false,
		"javascript": false,
	}, script.SandboxStrict)
	if got := specIDs(specs); got != "csv,mail" {
		t.Fatalf("specs = %q, want csv,mail", got)
	}
}

// TestExplicitScriptSupervisionCannotBypassDisabledLanguages closes the less
// obvious path: --supervise-connector=script must not undo --python=false and the
// other language switches after the default worker has correctly stayed down.
func TestExplicitScriptSupervisionCannotBypassDisabledLanguages(t *testing.T) {
	enabled := map[string]bool{"python": false, "powershell": false, "javascript": false}
	specs, offload, err := superviseConnectorSpecs([]string{"script"}, nil, enabled, script.SandboxStrict)
	if err != nil {
		t.Fatalf("superviseConnectorSpecs: %v", err)
	}
	if len(specs) != 0 {
		t.Fatalf("specs = %s, want no script worker", specIDs(specs))
	}
	if len(offload) != 1 || offload[0] != "script" {
		t.Fatalf("offload = %v, want script jobs parked", offload)
	}
}

// TestSuperviseConnectorAsksForAWorkerAndStopsRunningItHere is the whole point: the
// named kind gets a worker of its own, and the engine stops working those jobs
// itself — the same pairing the default kinds get, which is what makes the worker
// the one that leases them.
func TestSuperviseConnectorAsksForAWorkerAndStopsRunningItHere(t *testing.T) {
	specs, offload, err := superviseConnectorSpecs([]string{"ad"}, nil, nil)
	if err != nil {
		t.Fatalf("superviseConnectorSpecs: %v", err)
	}
	if len(specs) != 1 {
		t.Fatalf("specs = %v, want exactly one", specIDs(specs))
	}
	got := specs[0]
	if got.ID != "ad" || len(got.Kinds) != 1 || got.Kinds[0] != "ad" ||
		len(got.Connectors) != 1 || got.Connectors[0] != "ad" {
		t.Fatalf("spec = %+v, want a worker serving the ad worker under its own id", got)
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
	specs, offload, err := superviseConnectorSpecs([]string{"entra"}, nil, nil)
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

// TestTheAgentKindIsSupervisedWithoutBeingOffloaded is entra's case with a sharper
// edge (ADR-0254). Entra has no in-process handler because the engine holds no tenant
// credential; the agent kind has none because ADR-0164 forbids it — a round is one
// model call, minutes long and able to hang, and putting it on the core loop is the
// thing that record exists to prevent. So supervising it must never quietly add it to
// the offload list, which would be the engine claiming it had been running it.
func TestTheAgentKindIsSupervisedWithoutBeingOffloaded(t *testing.T) {
	specs, offload, err := superviseConnectorSpecs([]string{"agent"}, nil, nil)
	if err != nil {
		t.Fatalf("superviseConnectorSpecs: %v", err)
	}
	if len(specs) != 1 || specs[0].ID != "agent" {
		t.Fatalf("specs = %v, want a worker for agent", specIDs(specs))
	}
	if len(offload) != 0 {
		t.Fatalf("offload = %v, want none: an agent round has never run in the engine", offload)
	}
}

// TestAKindAlreadySupervisedIsNotStartedTwice keeps the flag idempotent against the
// defaults. Two workers leasing one kind is not an error the operator would see —
// it is two processes racing for the same jobs.
func TestAKindAlreadySupervisedIsNotStartedTwice(t *testing.T) {
	already := []api.SuperviseSpec{{ID: "mail", Kinds: []string{"mail"}, Connectors: []string{"mail"}}}
	specs, offload, err := superviseConnectorSpecs([]string{"mail", "ad"}, already, nil)
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
	_, _, err := superviseConnectorSpecs([]string{"activedirectory"}, nil, nil)
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
	specs, offload, err := superviseConnectorSpecs(nil, nil, nil)
	if err != nil || len(specs) != 0 || len(offload) != 0 {
		t.Fatalf("superviseConnectorSpecs(nil) = %v, %v, %v; want nothing at all", specIDs(specs), offload, err)
	}
}

// TestSupervisingRemedyPairsTheWorkerWithTheOffload is the operator-facing half of
// moving the Remedy worker onto a worker (ADR-0106 amended / ADR-0168). Unlike
// entra, Remedy still has an in-process handler — so asking for its worker must also
// take the kind off the engine, or the two would race for the same jobs.
func TestSupervisingRemedyPairsTheWorkerWithTheOffload(t *testing.T) {
	specs, offload, err := superviseConnectorSpecs([]string{"remedy"}, nil, nil)
	if err != nil {
		t.Fatalf("superviseConnectorSpecs: %v", err)
	}
	if len(specs) != 1 || specs[0].ID != "remedy" ||
		len(specs[0].Connectors) != 1 || specs[0].Connectors[0] != "remedy" {
		t.Fatalf("specs = %v, want a worker configured for the remedy worker", specIDs(specs))
	}
	if len(offload) != 1 || offload[0] != "remedy" {
		t.Fatalf("offload = %v, want [remedy] so the engine stops filing the tickets itself", offload)
	}
}
