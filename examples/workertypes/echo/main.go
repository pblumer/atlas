// Command echo is the smallest Worker Type that can be installed on atlas as an
// artifact: a separate executable built against workersdk alone, which atlas
// launches, verifies and supervises (ADR-0535, migration step 3).
//
// It serves one job type, com.example.echo, completing each job with the variables
// it was given plus the name of the instance that echoed them. Build it, write its
// manifest with the binary's digest, and install both:
//
//	go build -o echo ./examples/workertypes/echo
//	curl -F manifest=@manifest.json -F artifact=@echo \
//	     -H "Authorization: Bearer $TOKEN" http://localhost:8080/api/v1/worker-types/installed
//
// Installing an unsigned artifact needs a server started with
// --allow-unsigned-worker-types, which is for development only.
package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/pblumer/atlas/workersdk"
)

// JobType is the one job type this Worker Type serves.
const JobType = "com.example.echo"

func main() {
	opts := workersdk.LaunchOptions(os.Getenv)
	opts.Handlers = map[string]workersdk.Exec{JobType: workersdk.ExecFunc(echo(opts.ID))}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := workersdk.New(opts).Run(ctx); err != nil && ctx.Err() == nil {
		fmt.Fprintln(os.Stderr, "echo:", err)
		os.Exit(1)
	}
}

// echo completes a job with its own variables and the echoing instance's id.
func echo(id string) func(context.Context, workersdk.Job) (map[string]any, error) {
	return func(_ context.Context, j workersdk.Job) (map[string]any, error) {
		out := map[string]any{"echoedBy": id}
		for k, v := range j.Variables {
			out[k] = v
		}
		return out, nil
	}
}
