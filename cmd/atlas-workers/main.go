// Command atlas-workers is atlas's worker-only executable: the out-of-process worker
// of `atlas worker`, built without the server, the engine's processor, the web UI or
// the MCP server (ADR-0535, migration step 2).
//
// It takes the same flags and reads the same environment as `atlas worker`, because
// it runs the same command (internal/workercmd). When it sits beside the atlas
// binary, a server supervising its workers starts this executable instead of itself;
// ATLAS_WORKERS_BINARY=self keeps the old behaviour.
package main

import (
	"errors"
	"fmt"
	"os"

	"github.com/pblumer/atlas/connector/script"
	"github.com/pblumer/atlas/internal/workercmd"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		// A worker with nothing to serve has already said so; its own status lets the
		// supervisor park it rather than restart it into the same emptiness.
		if errors.Is(err, workercmd.ErrNothingToServe) {
			os.Exit(workercmd.ExitNothingToServe)
		}
		fmt.Fprintln(os.Stderr, "atlas-workers:", err)
		os.Exit(1)
	}
}

// run dispatches one invocation. The script worker's strict sandbox re-executes the
// process that hosts it with script.SandboxSubcommand, so this executable answers
// that subcommand exactly as atlas does; everything else is the worker.
func run(args []string) error {
	if len(args) > 0 && args[0] == script.SandboxSubcommand {
		return script.RunSandbox(args[1:])
	}
	return workercmd.Run(args)
}
