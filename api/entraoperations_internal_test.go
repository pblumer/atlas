package api

import (
	"regexp"
	"strings"
	"testing"

	"github.com/pblumer/atlas/connector/entra"
)

// The Modeler's Entra operation list against the worker's own table.
//
// The rules already exist twice and are held together by a test: the compiler's
// entraOps and connector/entra's Ops, guarded by TestEntraOpsMatchTheConnector. The
// Modeler is the third copy and was guarded by nothing — so an operation added to
// both Go tables stayed unreachable from the one surface a modeller uses, and an
// operation removed from them stayed on offer until somebody deployed one and read
// the compiler's refusal.
//
// Neither failure is loud. A missing entry is a feature nobody can find; a stale one
// is a deploy that fails for a reason the screen that offered it does not mention.

// entraChoice matches one `{ v: "op", l: "Label" }` entry of a select field.
var entraChoice = regexp.MustCompile(`\{\s*v:\s*"([a-z0-9-]+)",\s*l:\s*"`)

// TestTheModelerOffersEveryEntraOperation.
func TestTheModelerOffersEveryEntraOperation(t *testing.T) {
	// The Entra section alone, and then the operation select inside it: the same
	// file carries an "operation" select for the SQL products too, and a search
	// across it would read their query/execute choices as Entra's.
	region := webRegion(t, entraSection(t), `key: "operation", label: "Operation"`, "\n      },")
	offered := map[string]bool{}
	for _, m := range entraChoice.FindAllStringSubmatch(region, -1) {
		offered[m[1]] = true
	}
	if len(offered) == 0 {
		t.Fatal("no operation choices found; this guard has lost its subject")
	}
	for _, op := range entra.OpNames() {
		if !offered[op] {
			t.Errorf("the worker has %q and the Modeler does not offer it, so a modeller "+
				"cannot author the operation at all", op)
		}
		delete(offered, op)
	}
	for op := range offered {
		t.Errorf("the Modeler offers %q, which the worker has no operation for: a model "+
			"authored with it is refused at deploy, by a message this screen never showed", op)
	}
}

// TestTheModelerAsksForWhatAnEntraOperationNeeds.
//
// The id fields are shown per operation, from lists written by hand. An operation
// that needs a group and never prompts for one produces the failure this Worker Type
// exists to prevent: a deploy refused for a field the screen did not ask for.
func TestTheModelerAsksForWhatAnEntraOperationNeeds(t *testing.T) {
	src := entraSection(t)
	for _, f := range []struct {
		field  string
		anchor string
		needs  func(entra.Op) bool
	}{
		{field: "userId", anchor: `key: "userId", label: "User"`, needs: func(o entra.Op) bool { return o.NeedsUser }},
		{field: "groupId", anchor: `key: "groupId", label: "Group / Team"`, needs: func(o entra.Op) bool { return o.NeedsGroup }},
	} {
		region := webRegion(t, src, f.anchor, "\n      },")
		for op, spec := range entra.Ops {
			if !f.needs(spec) {
				continue
			}
			if !strings.Contains(region, `"`+op+`"`) {
				t.Errorf("operation %q needs a %s and the Modeler never prompts for one",
					op, f.field)
			}
		}
	}
}

// entraSection is the Entra Worker Type's own block of editor.js. Every guard here
// reads it rather than the file: the fields of twenty Worker Types share key names,
// and a guard that searched the whole file would be satisfied by another type's.
func entraSection(t *testing.T) string {
	t.Helper()
	return webRegion(t, readWeb(t, "editor.js"), `id: "entra", name: "Microsoft Entra ID"`, "\n  {\n    id: \"")
}
