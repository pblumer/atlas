package api

import (
	"os"
	"regexp"
	"strings"
	"testing"

	"github.com/pblumer/atlas/connector/device/catalog"
)

// deviceRowRe reads one row of DEVICE_OPERATIONS in api/web/device-ops.js.
var deviceRowRe = regexp.MustCompile(`^\s*\{ v: "([a-z.]+)", l: "[^"]+", needs: \[([^\]]*)\], takes: \[([^\]]*)\], result: "([a-z]*)" \},$`)

// TestModelerKnowsEveryDeviceOperation holds the Modeler's copy of the device operation
// table to connector/device/catalog, field by field: a row that differed would hide a
// value the deploy then asks for, or offer one it refuses.
func TestModelerKnowsEveryDeviceOperation(t *testing.T) {
	src, err := os.ReadFile("web/device-ops.js")
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for _, line := range strings.Split(string(src), "\n") {
		if !strings.Contains(line, `{ v: "`) {
			continue
		}
		m := deviceRowRe.FindStringSubmatch(line)
		if m == nil {
			t.Errorf("a row of DEVICE_OPERATIONS no longer has the shape this test reads: %s", strings.TrimSpace(line))
			continue
		}
		name := m[1]
		seen[name] = true
		op, ok := catalog.Lookup(name)
		if !ok {
			t.Errorf("the Modeler offers %q, which the device Worker does not implement", name)
			continue
		}
		result := ""
		if op.Returns {
			result = "object"
		}
		for _, c := range []struct {
			what      string
			js, gocat []string
		}{
			{"needed values", jsList(m[2]), op.Needs},
			{"optional values", jsList(m[3]), op.Takes},
			{"result", []string{m[4]}, []string{result}},
		} {
			if strings.Join(c.js, ",") != strings.Join(c.gocat, ",") {
				t.Errorf("%s: the Modeler says %s are %v, the Worker %v", name, c.what, c.js, c.gocat)
			}
		}
	}
	for _, name := range catalog.Names() {
		if !seen[name] {
			t.Errorf("the Modeler offers no way to author %q, which the device Worker performs", name)
		}
	}
	editor, err := os.ReadFile("web/editor.js")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(editor), `from "./device-ops.js"`) || !strings.Contains(string(editor), "options: deviceOperationOptions()") {
		t.Error("the device entry in editor.js no longer takes its operations from device-ops.js")
	}
	for _, attr := range catalog.Attributes {
		if !strings.Contains(string(editor), `key: "`+attr+`", label:`) {
			t.Errorf("the Modeler has no field for %s", attr)
		}
	}
}

// TestTheDevicePackageOffersEveryOperation: the Repository package is the other place an
// author picks an operation, and it must not offer fewer than the device runs.
func TestTheDevicePackageOffersEveryOperation(t *testing.T) {
	raw, err := os.ReadFile("repository_catalog/device.json")
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range catalog.Names() {
		if !strings.Contains(string(raw), `"value": "`+name+`"`) {
			t.Errorf("the Repository package offers no %q", name)
		}
	}
}
