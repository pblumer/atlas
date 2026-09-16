package adr_test

import (
	"os"
	"path/filepath"
	"regexp"
	"testing"
)

// The race command is written down in six places, and they have to agree.
//
// `go test -race -timeout=Nm ./...` appears in the Makefile, in CI, and in the
// three documents that tell a contributor what "done" means. The comment beside
// the CI step already says "change one and change the rest" — which is a note to
// whoever reads that file, and reaches nobody editing the Makefile.
//
// The number is not decoration. It fails the step on runner variance when it is
// tight, and it has been raised twice for exactly that: from Go's 10-minute
// default after a timeout at 600s on a run whose only change was a line in an
// unrelated test file, and from 25m after a timeout at 1500s on a runner that was
// two to six times slower across seven packages the diff never touched. A
// contributor whose local flag is the older, smaller one reproduces neither
// failure and is told their change is fine.
//
// This guard says nothing about which number is right. It says the six copies are
// one number.
func TestTheRaceTimeoutIsOneNumberEverywhereItIsWrittenDown(t *testing.T) {
	root := filepath.Join("..", "..")
	if _, err := os.Stat(filepath.Join(root, "go.mod")); err != nil {
		t.Fatalf("expected the repository root two levels up from docs/adr: %v", err)
	}

	// Where the command is stated as the thing to run. Deliberately not a walk of
	// the whole repository: docs/planning and docs/audits record what was run on a
	// past day, and rewriting those to match today would be falsifying a record.
	files := []string{
		"Makefile",
		filepath.Join(".github", "workflows", "ci.yml"),
		"AGENTS.md",
		"CLAUDE.md",
		"DEVELOPMENT.md",
	}
	flag := regexp.MustCompile(`go test -race -timeout=(\d+)m`)

	found := map[string][]string{}
	for _, f := range files {
		body, err := os.ReadFile(filepath.Join(root, f))
		if err != nil {
			t.Fatalf("read %s: %v", f, err)
		}
		ms := flag.FindAllStringSubmatch(string(body), -1)
		if len(ms) == 0 {
			t.Errorf("%s no longer states the race command; a contributor reading it "+
				"runs without the flag and meets a timeout that is not a defect", f)
			continue
		}
		for _, m := range ms {
			found[m[1]] = append(found[m[1]], f)
		}
	}
	if len(found) > 1 {
		for value, where := range found {
			t.Errorf("-timeout=%sm in %v", value, where)
		}
		t.Error("the race timeout is stated with more than one value; a local run and " +
			"CI then disagree about when a package has taken too long")
	}
}
