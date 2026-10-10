// Package lifecyclestrand holds the test for strang.py, the tool that merges a
// product's provisioning and deprovisioning processes into one per-position strand
// (ADR-0428). The tool is Python; what it produces is held to what the compiler and
// the catalogue's publish check ask of a per-position lifecycle process.
package lifecyclestrand

import (
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/pblumer/atlas/compiler"
)

// merge runs strang.py on the testdata pair and compiles what it wrote.
func merge(t *testing.T) (*compiler.CompiledProcess, []byte) {
	t.Helper()
	py, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("python3 is not installed; strang.py cannot run here")
	}
	out := filepath.Join(t.TempDir(), "strand.bpmn")
	cmd := exec.Command(py, "strang.py", "testdata/provision.bpmn", "testdata/deprovision.bpmn",
		"--product", "demo-account", "--process-id", "proc_demo_account_strang", "-o", out)
	if b, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("strang.py: %v\n%s", err, b)
	}
	xml, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	f, err := os.Open(out)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	cp, err := compiler.Parse(1, 1, f)
	if err != nil {
		t.Fatalf("the merged strand does not compile: %v", err)
	}
	return cp, xml
}

// TestTheStrandIsWhatAPerPositionProductBinds: the two operations are message
// starts and nothing starts it by hand (ADR-0425, ADR-0426), the return is a
// correlated catch the order can deliver to (ADR-0428), and no cycle runs without
// waiting.
func TestTheStrandIsWhatAPerPositionProductBinds(t *testing.T) {
	cp, _ := merge(t)

	var starts []string
	for _, ms := range cp.MessageStartEvents() {
		starts = append(starts, ms.MessageName)
	}
	slices.Sort(starts)
	if !slices.Equal(starts, []string{"demo-account.deprovision", "demo-account.provision"}) {
		t.Errorf("message starts = %v, want the provision start and the return without an instance", starts)
	}
	for _, id := range cp.StartEvents() {
		if cp.Node(id).Type == compiler.TypeStartEvent {
			t.Error("the strand has a plain start event; a start by hand would run both operations")
		}
	}

	catches := cp.MessageCatchPoints()
	if len(catches) != 1 || catches[0].MessageName != "demo-account.deprovision" || !catches[0].Correlated {
		t.Errorf("catch points = %+v, want one correlated catch of demo-account.deprovision", catches)
	}
	if c := cp.WaitlessCycle(); c != nil {
		t.Errorf("the strand has a cycle that waits for nothing: %v", c)
	}
}

// TestOnlyTheGrantingEndWaitsForTheReturn: the end reached after the "done" report
// becomes the wait for the return; an end that grants nothing (a rejection) still
// ends the instance, because there is no right to hold.
func TestOnlyTheGrantingEndWaitsForTheReturn(t *testing.T) {
	cp, xml := merge(t)
	var ends []string
	for id := int32(0); id < int32(cp.NodeCount()); id++ {
		if cp.Node(id).Type == compiler.TypeEndEvent {
			ends = append(ends, cp.ElementBpmnId(id))
		}
	}
	slices.Sort(ends)
	if !slices.Equal(ends, []string{"d_ende", "p_ende_abgelehnt"}) {
		t.Errorf("end events = %v, want the rejection and the end of the return", ends)
	}
	for _, s := range []string{`id="ausgegeben"`, `id="warten_rueckgabe"`, `id="gw_rueckgabe"`} {
		if !strings.Contains(string(xml), s) {
			t.Errorf("the strand has no %s", s)
		}
	}
}
