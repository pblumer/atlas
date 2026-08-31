package main

import (
	"os"
	"strings"
	"testing"
)

// TestMain stops a supervised worker from re-running this whole suite.
//
// The server supervises workers by spawning [os.Executable] with an argv that
// starts `worker …` (ADR-0164), and under `go test` that executable is this test
// binary. Go's flag parsing stops at the first positional argument, so `worker`
// is not the error it looks like: the binary would ignore the rest and run every
// test again — each of which boots a server, which supervises workers, which
// spawn this binary again. A test that starts a server would fork a tree.
//
// Anything `go test` itself passes is a flag (-test.run=…), so the check is on
// the shape of the first argument rather than on a list of subcommand names.
func TestMain(m *testing.M) {
	if len(os.Args) > 1 && !strings.HasPrefix(os.Args[1], "-") {
		os.Exit(0)
	}
	os.Exit(m.Run())
}
