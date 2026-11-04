package main

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/pblumer/atlas/api/importplan"
)

// askGaps settles the capability map's gaps the flags left open
// (ADR-draft-application-bundle-architecture): in a pipeline, a refusal that names
// each and the flags that decide it; at a terminal, one question each, whose default
// is to leave the gap out — the choice that files nothing in the organisation's map.
func (p *importPlan) askGaps(out io.Writer, gaps []int, sc *bufio.Scanner) error {
	if sc == nil {
		lines := make([]string, 0, len(gaps))
		for _, i := range gaps {
			lines = append(lines, p.artifacts[i].label())
		}
		return fmt.Errorf("%d capabilities or value streams are not in the server's capability map and were not decided; nothing was imported:\n  %s\n"+
			"decide each with --adopt REF (file the package's proposal), --map REF=KEY (use a key the map has) or --omit REF, or leave all out with --omit all",
			len(gaps), strings.Join(lines, "\n  "))
	}
	for _, i := range gaps {
		a := &p.artifacts[i]
		prompt := a.label() + " is not in the server's capability map. "
		if a.Proposed {
			prompt += "Adopt the package's proposal (a), "
		}
		if strings.HasPrefix(a.Ref, "capability:") {
			prompt += "name a key the map has, "
		}
		prompt += "or leave it out (Enter)? "
		fmt.Fprint(out, prompt)
		if !sc.Scan() {
			fmt.Fprintln(out)
			return errors.New("no answer was given; nothing was imported")
		}
		a.decided = true
		answer := strings.TrimSpace(sc.Text())
		switch {
		case answer == "":
			a.Outcome = importplan.Omitted
		case a.Proposed && strings.EqualFold(answer, "a"):
			a.Outcome = importplan.Created
		case strings.HasPrefix(a.Ref, "capability:"):
			a.Outcome, a.MappedTo = importplan.Mapped, answer
		default:
			a.Outcome = importplan.Omitted
		}
	}
	return nil
}
