package pwsh

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"time"
)

// varsEnv is the environment variable the wrapper reads the instance's variables
// from, as a JSON object. Passing them out-of-band (env, not interpolated into
// the script) keeps variable values from being spliced into the script text.
const varsEnv = "ATLAS_VARS"

// defaultTimeout bounds a single script's wall-clock runtime when CmdExec.Timeout
// is unset. A runaway or blocking script (an infinite loop, a hung network call)
// is killed at the deadline so it cannot pin the worker indefinitely; the job then
// stays pending, like any other worker error.
const defaultTimeout = 30 * time.Second

// CmdExec runs script tasks by shelling out to a real PowerShell interpreter. It
// is the production [Exec]; tests use a fake instead so they need no pwsh.
//
// Security posture (ADR-0047): the interpreter is invoked with -NoProfile and
// -NonInteractive, the instance's variables are passed as JSON via the varsEnv
// environment variable (not interpolated), and the process runs in the worker's
// trust domain — never with the engine's credentials. Timeouts and resource
// limits belong to the caller via ctx and the host; running in an external gRPC
// worker in the customer's environment is the eventual isolation boundary.
type CmdExec struct {
	// Bin is the interpreter binary; empty means "pwsh" on the PATH.
	Bin string
	// Timeout is the per-script wall-clock limit; <= 0 means defaultTimeout. When
	// it elapses the interpreter process is killed and the job stays pending.
	Timeout time.Duration
	// run executes name with args and env and returns stdout. It defaults to
	// os/exec; tests substitute a deterministic fake so they need no interpreter.
	run func(ctx context.Context, name string, args, env []string) ([]byte, error)
}

// NewCmdExec returns a CmdExec that shells out to pwsh on the PATH.
func NewCmdExec() *CmdExec { return &CmdExec{} }

func (e *CmdExec) bin() string {
	if e.Bin != "" {
		return e.Bin
	}
	return "pwsh"
}

func (e *CmdExec) timeout() time.Duration {
	if e.Timeout > 0 {
		return e.Timeout
	}
	return defaultTimeout
}

// Check reports whether the interpreter is resolvable on PATH. The server calls
// it once at startup so an operator who enabled the worker without installing
// pwsh sees a clear warning, rather than watching script tasks park silently.
func (e *CmdExec) Check() error {
	_, err := exec.LookPath(e.bin())
	return err
}

func (e *CmdExec) runner() func(context.Context, string, []string, []string) ([]byte, error) {
	if e.run != nil {
		return e.run
	}
	return execCommand
}

// Run marshals input to JSON, invokes the interpreter on the wrapped source, and
// decodes its stdout as the script's result. A non-zero exit (or a runner error)
// leaves the job pending; an empty stdout decodes to a null result.
func (e *CmdExec) Run(ctx context.Context, source string, input map[string]any) (any, error) {
	varsJSON, err := json.Marshal(input)
	if err != nil {
		return nil, fmt.Errorf("pwsh: encode variables: %w", err)
	}
	args := []string{"-NoProfile", "-NonInteractive", "-Command", wrapSource(source)}
	env := append(os.Environ(), varsEnv+"="+string(varsJSON))
	// Bound the run: the deadline cancels ctx, which CommandContext uses to kill a
	// script that overruns, so a runaway interpreter can't pin the worker forever.
	ctx, cancel := context.WithTimeout(ctx, e.timeout())
	defer cancel()
	out, err := e.runner()(ctx, e.bin(), args, env)
	if err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			return nil, fmt.Errorf("pwsh: script timed out after %s", e.timeout())
		}
		return nil, fmt.Errorf("pwsh: run: %w", err)
	}
	return parseOutput(out)
}

// execCommand is the default runner: the only part that touches os/exec.
func execCommand(ctx context.Context, name string, args, env []string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Env = env
	return cmd.Output()
}

// wrapSource builds the PowerShell program run for a script task. It reads the
// instance's variables from varsEnv into a hashtable, exposes each as a variable
// the script can reference by name, runs the authored source, and emits the last
// result as compact JSON on stdout (a null result as empty output). Stop-on-error
// makes a failing script a non-zero exit, which leaves the job pending.
func wrapSource(source string) string {
	return `$ErrorActionPreference = 'Stop'
$__atlas = if ($env:` + varsEnv + `) { $env:` + varsEnv + ` | ConvertFrom-Json -AsHashtable } else { @{} }
foreach ($__k in $__atlas.Keys) { Set-Variable -Name $__k -Value $__atlas[$__k] }
$__result = & {
` + source + `
}
if ($null -eq $__result) { '' } else { $__result | ConvertTo-Json -Compress -Depth 64 }`
}

// parseOutput decodes an interpreter's stdout as the script's result. Empty output
// is a null result; numbers keep their exact decimal text (UseNumber) so they
// survive as FEEL numbers rather than lossy floats. Non-JSON output is an error,
// which leaves the job pending.
func parseOutput(out []byte) (any, error) {
	trimmed := bytes.TrimSpace(out)
	if len(trimmed) == 0 {
		return nil, nil
	}
	dec := json.NewDecoder(bytes.NewReader(trimmed))
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err != nil {
		return nil, fmt.Errorf("pwsh: decode script output %q: %w", string(trimmed), err)
	}
	return v, nil
}
