package api

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// Every REST call a system process makes has to reach a route this server serves.
//
// Two defects found this test rather than the other way round, both in the
// portal's fulfilment model and both total: the approval's process id was built by
// string concatenation and named nothing that was deployed, and the task that
// starts a provisioning run posted to `/api/v1/instances`, which does not exist —
// instances are started by definition key, under a different path.
//
// Neither was visible anywhere. The model is XML, so it parses; the compiler has
// no opinion about a connector's headers, so it compiles; the paths are strings
// built partly by FEEL, so nothing typechecks them. The first sign would have been
// an incident on a customer's order.
//
// A path a model calls is as much a contract as a function it calls. This walks
// both sides of it.

var (
	// restTask isolates one service task's extension block, so a method and a path
	// are read from the same task rather than from anywhere in the file.
	restTask = regexp.MustCompile(`(?s)<bpmn:serviceTask\b.*?</bpmn:serviceTask>`)
	// staticHeader is a path written out: <zeebe:header key="path" value="…"/>.
	staticHeader = regexp.MustCompile(`key="path"\s+value="([^"]*)"`)
	// feelPath is a path assembled by an input mapping onto the "path" variable. It
	// is matched against the *raw* XML, where the expression's own quotes are still
	// escaped — unescaping first would end the attribute at the first one.
	feelPath = regexp.MustCompile(`<zeebe:input\s+source="([^"]*)"\s+target="path"\s*/>`)
	// methodHeader is the verb, from the same block.
	methodHeader = regexp.MustCompile(`key="method"\s+value="([^"]*)"`)
	// feelLiteral picks the quoted pieces out of a FEEL concatenation, once the
	// expression itself has been unescaped.
	feelLiteral = regexp.MustCompile(`"([^"]*)"`)
	// routeParam is how both sides are normalised: a path parameter's *name* is not
	// part of the contract, only that a segment is filled in.
	routeParam = regexp.MustCompile(`\{[^}]*\}`)
)

// pathShape reduces a path to what has to match: literal segments, with every
// filled-in segment written the same way on both sides.
func pathShape(p string) string { return routeParam.ReplaceAllString(p, "{}") }

// feelPathShape turns a FEEL expression that builds a path into the same shape.
// `"/api/v1/orders/" + orderId + "/next"` is two literals with something between
// them, which is exactly what `/api/v1/orders/{id}/next` is.
func feelPathShape(expr string) string {
	lits := feelLiteral.FindAllStringSubmatch(expr, -1)
	parts := make([]string, 0, len(lits))
	for _, l := range lits {
		parts = append(parts, l[1])
	}
	joined := strings.Join(parts, "{}")
	// A concatenation reads "…/orders/" + id + "/next": joining leaves the slashes
	// that were already in the literals, so collapse the doubled ones.
	return strings.ReplaceAll(pathShape(joined), "//", "/")
}

func TestEverySystemProcessCallsARouteThatExists(t *testing.T) {
	served := map[string]bool{}
	for _, r := range accessTestServer(t).apiRoutes() {
		served[r.method+" "+pathShape(r.pattern)] = true
	}

	entries, err := os.ReadDir(systemProcessesDir)
	if err != nil {
		t.Fatalf("read %s: %v", systemProcessesDir, err)
	}
	calls := 0
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".bpmn") {
			continue
		}
		raw, err := os.ReadFile(filepath.Join(systemProcessesDir, e.Name()))
		if err != nil {
			t.Fatalf("read %s: %v", e.Name(), err)
		}
		for _, block := range restTask.FindAllString(string(raw), -1) {
			m := methodHeader.FindStringSubmatch(block)
			if m == nil {
				continue // not a REST task
			}
			var shape string
			switch {
			case feelPath.MatchString(block):
				shape = feelPathShape(strings.ReplaceAll(
					feelPath.FindStringSubmatch(block)[1], "&#34;", `"`))
			case staticHeader.MatchString(block):
				shape = pathShape(staticHeader.FindStringSubmatch(block)[1])
			default:
				continue
			}
			if !strings.HasPrefix(shape, "/api/") {
				continue // a call to somebody else's server is not this table's business
			}
			calls++
			if !served[m[1]+" "+shape] {
				t.Errorf("%s calls %s %s, which this server does not serve.\n"+
					"A path a model calls is a contract like any other; nothing else "+
					"would report this until an instance ran.", e.Name(), m[1], shape)
			}
		}
	}

	// A rule that checks nothing reports the same "ok" as a rule everything
	// satisfies. The portal's four models alone make more than this.
	if calls < 5 {
		t.Errorf("only %d REST calls found in the system processes; the scan or the "+
			"patterns above have gone stale, and a green result here would mean nothing", calls)
	}
}
