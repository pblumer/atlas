package api

import (
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"testing"
)

// A supervised worker runs the dedicated atlas-workers executable when it sits beside
// this binary, this binary's own `worker` mode otherwise, and whatever the operator's
// ATLAS_WORKERS_BINARY names (ADR-0535, migration step 2).
func TestTheSupervisorRunsTheDedicatedWorkerBinaryWhenItIsThere(t *testing.T) {
	dir := t.TempDir()
	self := filepath.Join(dir, "atlas")
	if exe, dedicated := workerExecutable(self, ""); exe != self || dedicated {
		t.Errorf("no sibling: %q, %v; want this binary in worker mode", exe, dedicated)
	}

	sibling := filepath.Join(dir, workersBinaryName())
	if err := os.Mkdir(sibling, 0o755); err != nil {
		t.Fatal(err)
	}
	if exe, dedicated := workerExecutable(self, ""); exe != self || dedicated {
		t.Errorf("a directory by that name: %q, %v; want this binary", exe, dedicated)
	}
	if err := os.Remove(sibling); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(sibling, []byte("#!/bin/sh\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" {
		if exe, dedicated := workerExecutable(self, ""); exe != self || dedicated {
			t.Errorf("a sibling that is not executable: %q, %v; want this binary", exe, dedicated)
		}
		if err := os.Chmod(sibling, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if exe, dedicated := workerExecutable(self, ""); exe != sibling || !dedicated {
		t.Errorf("an executable sibling: %q, %v; want %q", exe, dedicated, sibling)
	}
	if exe, dedicated := workerExecutable(self, " self "); exe != self || dedicated {
		t.Errorf("ATLAS_WORKERS_BINARY=self: %q, %v; want this binary", exe, dedicated)
	}
	if exe, dedicated := workerExecutable(self, "/opt/atlas/atlas-workers"); exe != "/opt/atlas/atlas-workers" || !dedicated {
		t.Errorf("an explicit path: %q, %v; want that path", exe, dedicated)
	}
}

// The worker-only executable takes the worker's flags without the subcommand.
func TestTheDedicatedWorkerBinaryGetsNoSubcommand(t *testing.T) {
	args := supervisedWorkerArgs("http://127.0.0.1:8080", SuperviseSpec{ID: "mail"}, nil)
	self := &supervisor{}
	if got := self.childArgs(args); !slices.Equal(got, args) {
		t.Errorf("this binary: %v, want %v", got, args)
	}
	dedicated := &supervisor{dedicated: true}
	if got := dedicated.childArgs(args); len(got) == 0 || got[0] != "--server" {
		t.Errorf("atlas-workers: %v, want the flags without the worker subcommand", got)
	}
}
