package infomodel

import (
	"fmt"
	"sort"
	"strings"

	"github.com/pblumer/atlas/compiler"
)

// The third lifecycle check of ADR-0259 §3, and the one that could not join the
// other two at CheckDataFlow.
//
// The record placed all three at that seam. Two of them belong there: whether a
// process writes a state its class does not declare, and whether a move it makes is
// one the machine joins, are both answerable from the process in front of you. The
// third is not. "The lifecycle says an order can be cancelled and nothing ever
// cancels one" is false until every process has been looked at, so asking it of one
// compiled process leaves two bad options: pass the other processes into a
// per-process check — where the same finding is repeated once per process and
// attached to whichever one happened to be deployed — or answer it wrong.
//
// So it is asked once, of the set. The seam is the same call site; the scope is not.

// CheckApplication reports what no single process can be asked: a state a class
// declares that nothing this application deploys ever writes.
//
// `cps` is the application's processes as they actually run — the deployed ones plus
// whatever is being deployed or dry-run now, so the process that cancels an order
// clears the finding the moment it arrives rather than after the next deploy.
//
// It is a warning like its two siblings and refuses nothing, for ADR-0230 slice 3's
// reason: a lifecycle is routinely drawn before the process that will write it.
func CheckApplication(cps []*compiler.CompiledProcess, vocab *Vocabulary) []compiler.Problem {
	// Per class: the states this application actually puts an object into. A class
	// present as a key is one some process handles, which is the difference between
	// "nothing cancels an order" and "no process here has ever heard of an order" —
	// only the first is worth saying.
	written := map[string]map[string]bool{}
	for _, cp := range cps {
		if cp == nil {
			continue
		}
		for _, do := range cp.DataObjects() {
			class := cp.Intern(do.ItemType)
			if class == "" {
				continue
			}
			// An unmodelled application resolves no class at all, so this one lookup
			// is also the "nothing to read against" guard the other checks state
			// separately — there is no third case for a `Modeled()` test to catch.
			if c, ok := vocab.Class(class); !ok || c.Lifecycle == nil {
				continue
			}
			states := written[class]
			if states == nil {
				states = map[string]bool{}
				written[class] = states
			}
			// The state instances are created in is reached as surely as any write
			// reaches one: every instance begins there.
			if seeded := cp.Intern(do.InitialState); seeded != "" {
				states[seeded] = true
			}
			name := cp.Intern(do.Name)
			for id := int32(0); int(id) < cp.NodeCount(); id++ {
				for _, a := range cp.DataOutputAssociations(id) {
					if cp.Intern(a.DataObject) != name || a.TargetState < 0 {
						continue
					}
					states[cp.Intern(a.TargetState)] = true
				}
			}
		}
	}

	// Sorted, because a deploy warning that changes order between two runs of the
	// same model reads as a model that changed.
	classes := make([]string, 0, len(written))
	for class := range written {
		classes = append(classes, class)
	}
	sort.Strings(classes)

	var ps []compiler.Problem
	for _, class := range classes {
		c, ok := vocab.Class(class)
		if !ok || c.Lifecycle == nil {
			continue
		}
		var unreached []string
		for _, st := range c.Lifecycle.States {
			if !written[class][st.Name] {
				unreached = append(unreached, st.Name)
			}
		}
		if len(unreached) == 0 {
			continue
		}
		// One sentence about one class. Three findings for three states would read as
		// three problems, and it is one: this lifecycle is ahead of its processes.
		// The finding names no element, because it is about the model rather than
		// about any element of any process — blaming whichever process was deployed
		// would point the reader at the wrong file.
		ps = append(ps, compiler.Problem{
			Severity: compiler.SeverityWarning, Rule: RuleDataUnreachableState,
			Message: fmt.Sprintf(
				"%s declares %s %s that nothing in this application writes. Either a process is missing, or the lifecycle is ahead of what is built — both are worth knowing, and neither refuses a deploy.",
				c.Name, plural(len(unreached), "the state", "the states"), quotedList(unreached)),
		})
	}
	return ps
}

func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}

// quotedList renders names as `"a", "b" and "c"` — the reading a person would speak,
// because this sentence is read rather than parsed.
func quotedList(names []string) string {
	quoted := make([]string, len(names))
	for i, n := range names {
		quoted[i] = fmt.Sprintf("%q", n)
	}
	if len(quoted) == 1 {
		return quoted[0]
	}
	return strings.Join(quoted[:len(quoted)-1], ", ") + " and " + quoted[len(quoted)-1]
}
