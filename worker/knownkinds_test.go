package worker_test

import (
	"os"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/pblumer/atlas/worker"
)

// KnownConnectorKinds is a hand-written list beside a switch that is the real
// implementation, and nothing held them together. That is not a cosmetic drift: the
// list is what `atlas --supervise-connector` validates against *at startup*, so a kind
// the switch serves but the list omits cannot be supervised at all — the server refuses
// to start with "no such kind" and names every kind except the one that was asked for.
//
// It happened to jira. The case was added to BuiltinConnectors and the list was not, so
// the kind could be served by a worker an operator launched by hand and could not be
// served by one the server supervises, which is the path the Console's Workers view
// shows. The failure looked to an operator like a worker that does not exist.
//
// The switch is read from source, the same move the moddle drift tests make over
// compiler/parse.go and the payload-arm test makes over api/handlers.go: a switch case
// is not something reflection can see, and reading it is cheaper than a registry
// refactor whose only purpose would be to make it visible.
func TestKnownConnectorKindsMatchesWhatIsImplemented(t *testing.T) {
	implemented := implementedConnectorKinds(t)
	listed := map[string]bool{}
	for _, k := range worker.KnownConnectorKinds() {
		listed[k] = true
	}

	for kind := range implemented {
		if !listed[kind] {
			t.Errorf("BuiltinConnectors serves %q but KnownConnectorKinds omits it — "+
				"`atlas --supervise-connector %s` would refuse to start, so the kind can never "+
				"have a supervised worker", kind, kind)
		}
	}
	for kind := range listed {
		if !implemented[kind] {
			t.Errorf("KnownConnectorKinds offers %q but BuiltinConnectors has no case for it — "+
				"a worker asked for it leases nothing and says so only at startup", kind)
		}
	}
}

// The list is also what an operator reads out of a refusal, so it must be sorted: a
// name appended at the end is one a reader scanning alphabetically concludes is absent.
func TestKnownConnectorKindsIsSorted(t *testing.T) {
	got := worker.KnownConnectorKinds()
	if !slices.IsSorted(got) {
		t.Errorf("KnownConnectorKinds() = %v, want it sorted: it is printed verbatim in the "+
			"error an operator reads when a kind is refused", got)
	}
}

// Every listed kind is one BuiltinConnectors accepts rather than refuses as unimplemented.
// This is the behavioural half — it catches a name that survives the source read because
// its case was deleted along with the code under it.
func TestEveryKnownConnectorKindIsAccepted(t *testing.T) {
	for _, kind := range worker.KnownConnectorKinds() {
		t.Run(kind, func(t *testing.T) {
			// An empty environment: a credential-bearing kind reports itself
			// unconfigured, which is not an error. Only an unimplemented kind is.
			_, err := worker.BuiltinConnectors(fakeEnv(nil), kind)
			if err != nil && strings.Contains(err.Error(), "does not implement") {
				t.Errorf("BuiltinConnectors refuses %q as unimplemented, but it is offered: %v", kind, err)
			}
		})
	}
}

// switchCaseRe finds one `case "a", "b":` line of BuiltinConnectors' kind switch.
var switchCaseRe = regexp.MustCompile(`(?m)^\t\tcase ("[a-z0-9]+"(?:, "[a-z0-9]+")*):$`)

// kindNameRe pulls each quoted kind out of such a line.
var kindNameRe = regexp.MustCompile(`"([a-z0-9]+)"`)

// implementedConnectorKinds reads the kinds BuiltinConnectors' switch actually serves.
func implementedConnectorKinds(t *testing.T) map[string]bool {
	t.Helper()
	src, err := os.ReadFile("connectors.go")
	if err != nil {
		t.Fatalf("read connectors.go: %v", err)
	}
	body := string(src)
	start := strings.Index(body, "switch kind {")
	if start < 0 {
		t.Fatal("found no kind switch in connectors.go; the pattern must have changed")
	}
	out := map[string]bool{}
	for _, m := range switchCaseRe.FindAllStringSubmatch(body[start:], -1) {
		for _, n := range kindNameRe.FindAllStringSubmatch(m[1], -1) {
			out[n[1]] = true
		}
	}
	if len(out) == 0 {
		t.Fatal("found no kind cases in connectors.go; the pattern must have changed")
	}
	return out
}
