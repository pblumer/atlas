// Command adrnum assigns a number to every architecture decision record that is
// still in flight, and makes the repository agree about it.
//
// A record is written on a branch as docs/adr/draft-<slug>.md with no number,
// because "the next free number" has no stable answer on a branch — two branches
// asking at the same time get the same one. This command answers it where it does
// have one, on main: it renames each draft to NNNN-<slug>.md, rewrites its heading,
// appends its row to the ADR index, and rewrites every citation of the draft
// across the tree. See docs/adr/number.go for the full argument.
//
// Usage:
//
//	go run ./docs/adr/cmd/adrnum [repo-root]    # or: make adr-number
//
// It is idempotent: with no drafts present it changes nothing, which is what lets
// the workflow in .github/workflows/adr-number.yml run it on every push to main.
package main

import (
	"fmt"
	"os"

	"github.com/pblumer/atlas/docs/adr"
)

func main() {
	root := "."
	if len(os.Args) > 1 {
		root = os.Args[1]
	}

	assigned, err := adr.AssignNumbers(root)
	if err != nil {
		fmt.Fprintf(os.Stderr, "adrnum: %v\n", err)
		os.Exit(1)
	}
	if len(assigned) == 0 {
		fmt.Println("adrnum: no drafts to number")
		return
	}
	for _, a := range assigned {
		fmt.Printf("ADR-%04d  %s → %s\n", a.Num, a.From, a.To)
	}
	fmt.Printf("adrnum: numbered %d record(s). Review the diff, then commit.\n", len(assigned))
}
