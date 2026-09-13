package api

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pblumer/atlas/api/catalog"
	"github.com/pblumer/atlas/api/order"
	"github.com/pblumer/atlas/compiler"
)

// A system process ships inside the binary and is deployed on the way up. One
// that does not compile is therefore a server that does not start — found by an
// operator on an upgrade, which is the worst place to find it, and it is also
// silent in review because BPMN is XML and XML always parses.
//
// So every system process is compiled here, by the compiler that will compile it
// at runtime rather than by an approximation of it.
func TestEverySystemProcessCompiles(t *testing.T) {
	entries, err := os.ReadDir(systemProcessesDir)
	if err != nil {
		t.Fatalf("read %s: %v", systemProcessesDir, err)
	}

	seen := 0
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".bpmn") {
			continue
		}
		seen++
		t.Run(e.Name(), func(t *testing.T) {
			b, err := os.ReadFile(filepath.Join(systemProcessesDir, e.Name()))
			if err != nil {
				t.Fatalf("read: %v", err)
			}
			deployables, err := compiler.ParseAll(1000, 1, bytes.NewReader(b))
			if err != nil {
				t.Fatalf("compile: %v", err)
			}
			if len(deployables) == 0 {
				t.Fatal("compiled to nothing: a file with no executable process is a file " +
					"that ships, deploys, and does not exist")
			}
		})
	}

	if seen == 0 {
		t.Fatalf("no .bpmn files under %s — this test passes vacuously if the "+
			"directory moves, so it says so instead", systemProcessesDir)
	}
}

// TestEveryApprovalKindNamesADeployedProcess is the test the first cut did not
// have, and the bug it would have caught was total: the fulfilment model built an
// approval's process id by concatenating the catalogue's kind onto a prefix
// ("atlas-genehmigung-" + "fixed"), and the three processes this binary ships are
// named in German ("atlas-genehmigung-fix"). Not one approval could ever have
// started. The model parsed, the processes compiled, every test passed.
//
// A mapping between two vocabularies is a thing that has to be checked against
// both. So this walks every ApprovalKind the catalogue can store, asks an order
// line what process decides it, and insists that process is one this binary
// deploys.
func TestEveryApprovalKindNamesADeployedProcess(t *testing.T) {
	deployed := map[string]bool{}
	entries, err := os.ReadDir(systemProcessesDir)
	if err != nil {
		t.Fatalf("read %s: %v", systemProcessesDir, err)
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".bpmn") {
			continue
		}
		b, err := os.ReadFile(filepath.Join(systemProcessesDir, e.Name()))
		if err != nil {
			t.Fatalf("read %s: %v", e.Name(), err)
		}
		deployables, err := compiler.ParseAll(1000, 1, bytes.NewReader(b))
		if err != nil {
			t.Fatalf("compile %s: %v", e.Name(), err)
		}
		for _, d := range deployables {
			deployed[d.Process.ProcessId()] = true
		}
	}

	kinds := []catalog.ApprovalKind{catalog.KindFixed, catalog.KindRole, catalog.KindSuperior}
	for _, k := range kinds {
		l := order.Line{Approval: order.Approval{Kind: string(k)}}
		pid := l.ApprovalProcess()
		if pid == "" {
			t.Errorf("approval kind %q resolves to no process at all", k)
			continue
		}
		if !deployed[pid] {
			t.Errorf("approval kind %q routes to process %q, which this binary does not ship.\n"+
				"Every order line with that kind would fail to start its approval, and "+
				"nothing else would say so.", k, pid)
		}
	}

	// And the kind that must resolve to nothing, because a line without an approval
	// is provisioned directly. A process id here would send it to an approver.
	if got := (order.Line{Approval: order.Approval{Kind: string(catalog.KindNone)}}).ApprovalProcess(); got != "" {
		t.Errorf("an unapproved line routes to %q; it must go straight to provisioning", got)
	}
}
