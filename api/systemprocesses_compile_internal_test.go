package api

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

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
