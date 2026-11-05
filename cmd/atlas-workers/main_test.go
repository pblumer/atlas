package main

import (
	"errors"
	"strings"
	"testing"

	"github.com/pblumer/atlas/internal/workercmd"
)

// The strict script sandbox re-executes the worker's own executable with the sandbox
// subcommand; atlas-workers must answer it rather than read it as worker flags.
func TestTheSandboxSubcommandReachesTheSandbox(t *testing.T) {
	err := run([]string{"script-sandbox"})
	if err == nil || !strings.Contains(err.Error(), "script sandbox") {
		t.Errorf("run(script-sandbox) = %v, want the sandbox's own usage error", err)
	}
}

// Anything else is the worker, which with nothing to serve says so.
func TestEverythingElseIsTheWorker(t *testing.T) {
	if err := run([]string{"--server", "http://127.0.0.1:1"}); !errors.Is(err, workercmd.ErrNothingToServe) {
		t.Errorf("run(worker flags) = %v, want ErrNothingToServe", err)
	}
}
