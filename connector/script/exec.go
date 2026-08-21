package script

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/pblumer/atlas/compiler"
)

// Environment variables the bootstrap programs read. Passing both the variables
// and the user source out-of-band (env, not interpolated into the command line)
// keeps values and script text from being spliced together — no escaping, no
// injection of one into the other.
const (
	varsEnv = "ATLAS_VARS"   // instance variables, as a JSON object
	srcEnv  = "ATLAS_SCRIPT" // the author's script source
)

// defaultTimeout bounds a single script's wall-clock runtime when Timeout is unset.
// A runaway or blocking script (an infinite loop, a hung network call) is killed at
// the deadline so it cannot pin the worker; the job then stays pending.
//
// It is deliberately larger than [nettimeout.Default], and the difference is now a
// decision rather than an accident. ADR-0156 listed unifying them as a follow-up,
// on the grounds that both were charged to the same goroutine — which ADR-0157 step
// 6 removed. They bound different things: a connector call is one request to an
// endpoint that is supposed to be fast, while a script is the customer's own code,
// which may legitimately compute for a while and no longer holds anything but its
// own slot in the round. What both share is that neither may run unbounded, because
// a token parked in silence is worse than a job that fails visibly.
const defaultTimeout = 30 * time.Second

// Lang describes how to run one scripting language: the reserved job type its
// worker subscribes to, the default interpreter binary, the interpreter arguments
// that run a bootstrap program, and that bootstrap. The bootstrap is a fixed
// program (never the author's source) that reads the source and variables from the
// environment, exposes the variables, runs the source, and prints the result as
// JSON on stdout (empty output = null). Each language keeps its own idiomatic
// result convention (see the bootstraps).
type Lang struct {
	Name    string // model/UI name, e.g. "python"
	JobType int32  // reserved job-type index (compiler.*JobTypeIndex)
	Bin     string // default interpreter binary
	Args    func(bootstrap string) []string
	Wrap    string // the bootstrap program
}

// The three supported languages. Adding one is a new Lang here plus its reserved
// job type in the compiler — the worker, engine behavior, and recovery are shared.
var (
	PowerShell = Lang{
		Name: "powershell", JobType: compiler.PwshJobTypeIndex, Bin: "pwsh",
		Args: func(b string) []string { return []string{"-NoProfile", "-NonInteractive", "-Command", b} },
		Wrap: powershellBootstrap,
	}
	Python = Lang{
		Name: "python", JobType: compiler.PythonJobTypeIndex, Bin: "python3",
		Args: func(b string) []string { return []string{"-c", b} },
		Wrap: pythonBootstrap,
	}
	JavaScript = Lang{
		Name: "javascript", JobType: compiler.JsJobTypeIndex, Bin: "node",
		Args: func(b string) []string { return []string{"-e", b} },
		Wrap: javascriptBootstrap,
	}
)

// Langs is every supported language, in a stable order.
var Langs = []Lang{PowerShell, Python, JavaScript}

// LangByName returns the language spec for a (lower-cased) language name, and
// whether it is one of the supported languages.
func LangByName(name string) (Lang, bool) {
	for _, l := range Langs {
		if l.Name == name {
			return l, true
		}
	}
	return Lang{}, false
}

// CmdExec runs script tasks by shelling out to a real interpreter for its Lang. It
// is the production [Exec]; tests use a fake instead so they need no interpreter.
//
// Security posture (ADR-0047): the interpreter runs with -NoProfile /
// -NonInteractive (or the language equivalent), the instance's variables and the
// source arrive as environment values (not interpolated), each run is bounded by
// Timeout (the process is killed at the deadline), and it runs in the worker's
// trust domain — never with the engine's credentials. The eventual isolation
// boundary is an external worker in the customer's environment.
type CmdExec struct {
	Lang    Lang          // language spec
	Bin     string        // interpreter override; empty means Lang.Bin
	Timeout time.Duration // per-script wall-clock limit; <= 0 means defaultTimeout
	// run executes name with args and env and returns stdout. It defaults to
	// os/exec; tests substitute a deterministic fake so they need no interpreter.
	run func(ctx context.Context, name string, args, env []string) ([]byte, error)
}

// New returns a CmdExec for the given language.
func New(l Lang) *CmdExec { return &CmdExec{Lang: l} }

func (e *CmdExec) bin() string {
	if e.Bin != "" {
		return e.Bin
	}
	return e.Lang.Bin
}

func (e *CmdExec) timeout() time.Duration {
	if e.Timeout > 0 {
		return e.Timeout
	}
	return defaultTimeout
}

// Check reports whether the interpreter is resolvable on PATH. The server calls it
// once at startup so an operator whose host lacks the interpreter sees a clear
// warning, rather than watching script tasks park silently.
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

// Run passes the source and the variables to the interpreter's bootstrap via the
// environment, runs it under a deadline, and decodes its stdout as the result. A
// non-zero exit or a runner error leaves the job pending; an empty stdout decodes
// to a null result; overrunning the timeout kills the process and errors.
func (e *CmdExec) Run(ctx context.Context, source string, input map[string]any) (any, error) {
	if input == nil {
		input = map[string]any{} // marshal to {} not null, so a bootstrap's dict/Object.keys never sees null
	}
	varsJSON, err := json.Marshal(input)
	if err != nil {
		return nil, fmt.Errorf("script: encode variables: %w", err)
	}
	args := e.Lang.Args(e.Lang.Wrap)
	env := append(os.Environ(), varsEnv+"="+string(varsJSON), srcEnv+"="+source)
	ctx, cancel := context.WithTimeout(ctx, e.timeout())
	defer cancel()
	out, err := e.runner()(ctx, e.bin(), args, env)
	if err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			return nil, fmt.Errorf("script: %s timed out after %s", e.Lang.Name, e.timeout())
		}
		return nil, fmt.Errorf("script: run %s: %w", e.Lang.Name, err)
	}
	return parseOutput(out)
}

// execCommand is the default runner: the only part that touches os/exec. On a
// non-zero exit it surfaces the interpreter's stderr (e.g. a Python traceback or a
// PowerShell error), which is what makes the result useful for debugging a script
// rather than just reporting "exit status 1".
func execCommand(ctx context.Context, name string, args, env []string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Env = env
	out, err := cmd.Output()
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		if msg := strings.TrimSpace(string(ee.Stderr)); msg != "" {
			return out, fmt.Errorf("%w: %s", err, msg)
		}
	}
	return out, err
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
		return nil, fmt.Errorf("script: decode output %q: %w", string(trimmed), err)
	}
	return v, nil
}

// powershellBootstrap runs the author's source (from srcEnv) with the instance's
// variables (from varsEnv) exposed as $name, and emits the script's output stream
// as compact JSON. Stop-on-error makes a failing script a non-zero exit.
const powershellBootstrap = `$ErrorActionPreference = 'Stop'
$__atlas = if ($env:ATLAS_VARS) { $env:ATLAS_VARS | ConvertFrom-Json -AsHashtable } else { @{} }
foreach ($__k in $__atlas.Keys) { Set-Variable -Name $__k -Value $__atlas[$__k] }
$__result = & ([ScriptBlock]::Create($env:ATLAS_SCRIPT))
if ($null -eq $__result) { '' } else { $__result | ConvertTo-Json -Compress -Depth 64 }`

// pythonBootstrap exposes the instance's variables as globals, runs the author's
// source, and emits the value the script assigned to `result` as JSON (empty when
// unset/None).
const pythonBootstrap = `import json, os, sys
_vars = json.loads(os.environ.get('ATLAS_VARS') or '{}')
_ns = dict(_vars)
exec(os.environ.get('ATLAS_SCRIPT') or '', _ns)
_r = _ns.get('result')
sys.stdout.write('' if _r is None else json.dumps(_r))`

// javascriptBootstrap exposes the instance's variables as arguments (so each is in
// scope by name), runs the author's source, and emits the value the script
// assigned to `result` as JSON (empty when unset/undefined/null).
const javascriptBootstrap = `const vars = JSON.parse(process.env.ATLAS_VARS || '{}');
const src = process.env.ATLAS_SCRIPT || '';
const fn = new Function(...Object.keys(vars), src + '\n; return typeof result !== "undefined" ? result : undefined;');
const r = fn(...Object.values(vars));
process.stdout.write(r === undefined || r === null ? '' : JSON.stringify(r));`
