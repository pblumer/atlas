package main

import (
	"io"
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
//
// It also takes the terminal away from `atlas import`: a test run from a terminal
// would otherwise find one there and wait for an answer to a question about a
// changed artifact. A test that means to answer says so with answering.
func TestMain(m *testing.M) {
	if len(os.Args) > 1 && !strings.HasPrefix(os.Args[1], "-") {
		os.Exit(0)
	}
	decisionPrompt = func() (io.Reader, bool) { return nil, false }
	os.Exit(m.Run())
}

// answering puts a terminal in front of `atlas import` for one test, with the
// answers it types.
func answering(t *testing.T, answers string) {
	t.Helper()
	prev := decisionPrompt
	decisionPrompt = func() (io.Reader, bool) { return strings.NewReader(answers), true }
	t.Cleanup(func() { decisionPrompt = prev })
}
