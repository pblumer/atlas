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
	// LanguagesEnv limits the language handlers registered by an out-of-process
	// script worker. Empty keeps the standalone worker's backwards-compatible
	// default of serving every language.
	LanguagesEnv = "ATLAS_SCRIPT_LANGUAGES"
)

// defaultTimeout bounds a single script's wall-clock runtime when Timeout is unset.
// A runaway or blocking script (an infinite loop, a hung network call) is killed at
// the deadline so it cannot pin the worker; the job then stays pending.
//
// It is deliberately larger than [nettimeout.Default], and the difference is now a
// decision rather than an accident. ADR-0156 listed unifying them as a follow-up,
// on the grounds that both were charged to the same goroutine — which ADR-0157 step
// 6 removed. They bound different things: a worker call is one request to an
// endpoint that is supposed to be fast, while a script is the customer's own code,
// which may legitimately compute for a while and no longer holds anything but its
// own slot in the round. What both share is that neither may run unbounded, because
// a token parked in silence is worse than a job that fails visibly.
const defaultTimeout = 30 * time.Second

// defaultMaxOutput bounds a script's stdout when MaxOutput is unset. Eight megabytes
// is far past any result a process variable can usefully hold and far below what
// hurts this host.
const defaultMaxOutput int64 = 8 << 20

// maxStderr bounds what is kept of a failing interpreter's diagnostics. It is a
// quotation in an error message, not a stream to preserve.
const maxStderr int64 = 32 << 10

// interpreterEnvNames is the small, non-secret part of the worker environment a
// general-purpose interpreter may need to start and resolve ordinary runtime
// dependencies. In particular it excludes every Atlas credential and arbitrary
// deployment variables: model-authored code has no reason to inherit the identity
// its worker uses to call Atlas.
var interpreterEnvNames = []string{
	"PATH",
	"HOME", "USERPROFILE",
	"TMPDIR", "TMP", "TEMP",
	"LANG", "LANGUAGE", "LC_ALL", "LC_CTYPE", "TZ",
	"SYSTEMROOT", "WINDIR", "COMSPEC", "PATHEXT",
	"PSModulePath",
}

// Lang describes how to run one scripting language: the reserved job type its
// worker subscribes to, the default interpreter binary, the interpreter arguments
// that run a bootstrap program, and that bootstrap. The bootstrap is a fixed
// program (never the author's source) that reads the source and variables from the
// environment, exposes the variables, runs the source, and prints the result as
// JSON on stdout (empty output = null). Each language keeps its own idiomatic
// result convention (see the bootstraps).
type Lang struct {
	Name        string // model/UI name, e.g. "python"
	JobTypeName string // reserved job-type name (compiler.*JobType)
	JobType     int32  // reserved job-type index (compiler.*JobTypeIndex)
	Bin         string // default interpreter binary
	Args        func(bootstrap string) []string
	Wrap        string // the bootstrap program
}

// The three supported languages. Adding one is a new Lang here plus its reserved
// job type in the compiler — the worker, engine behavior, and recovery are shared.
var (
	PowerShell = Lang{
		Name: "powershell", JobTypeName: compiler.PwshJobType, JobType: compiler.PwshJobTypeIndex, Bin: "pwsh",
		Args: func(b string) []string { return []string{"-NoProfile", "-NonInteractive", "-Command", b} },
		Wrap: powershellBootstrap,
	}
	Python = Lang{
		Name: "python", JobTypeName: compiler.PythonJobType, JobType: compiler.PythonJobTypeIndex, Bin: "python3",
		Args: func(b string) []string { return []string{"-c", b} },
		Wrap: pythonBootstrap,
	}
	JavaScript = Lang{
		Name: "javascript", JobTypeName: compiler.JsJobType, JobType: compiler.JsJobTypeIndex, Bin: "node",
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
// trust domain. Run gives the interpreter an allowlisted runtime environment, never
// the worker's Atlas token or deployment secrets. The eventual isolation boundary
// is an external worker in the customer's environment.
type CmdExec struct {
	Lang    Lang          // language spec
	Bin     string        // interpreter override; empty means Lang.Bin
	Timeout time.Duration // per-script wall-clock limit; <= 0 means defaultTimeout
	// Sandbox is the operating-system isolation profile. Empty/off preserves the
	// historical path; strict is the fail-closed Linux profile from
	// ADR-0303.
	Sandbox SandboxMode
	// MaxOutput bounds what one script may write to stdout, in bytes; <= 0 means
	// defaultMaxOutput. The server sets it from the installation's budgets.
	MaxOutput int64
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

func (e *CmdExec) maxOutput() int64 {
	if e.MaxOutput > 0 {
		return e.MaxOutput
	}
	return defaultMaxOutput
}

// Check reports whether the interpreter is resolvable on PATH and whether the host
// can enforce the selected profile at all. The server calls it once at startup so an
// operator whose host lacks the interpreter sees a clear warning, rather than
// watching script tasks park silently.
//
// It deliberately stays a cheap predicate and launches nothing. Whether the
// interpreter can actually start *inside* the profile is a separate question, asked
// once per process by CheckSandboxLanguages, because answering it means spawning the
// sandbox launcher — which only the Atlas executable is.
func (e *CmdExec) Check() error {
	if _, err := exec.LookPath(e.bin()); err != nil {
		return err
	}
	if e.Sandbox == SandboxStrict {
		return sandboxSupport()
	}
	return nil
}

func (e *CmdExec) runner() func(context.Context, string, []string, []string) ([]byte, error) {
	if e.run != nil {
		return e.run
	}
	max := e.maxOutput()
	return func(ctx context.Context, name string, args, env []string) ([]byte, error) {
		return execCommand(ctx, name, args, env, max)
	}
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
	env := interpreterEnvironment(string(varsJSON), source)
	name, args, env, cleanup, err := e.prepareCommand(args, env)
	if err != nil {
		return nil, fmt.Errorf("script: prepare %s sandbox: %w", e.Lang.Name, err)
	}
	if cleanup != nil {
		defer cleanup()
	}
	ctx, cancel := context.WithTimeout(ctx, e.timeout())
	defer cancel()
	out, err := e.runner()(ctx, name, args, env)
	if err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			return nil, fmt.Errorf("script: %s timed out after %s", e.Lang.Name, e.timeout())
		}
		return nil, fmt.Errorf("script: run %s: %w", e.Lang.Name, err)
	}
	return parseOutput(out)
}

// interpreterEnvironment creates a new environment instead of filtering a copy of
// os.Environ. An allowlist stays safe when an operator introduces a newly named
// credential; a denylist would expose it until Atlas learned that name.
func interpreterEnvironment(varsJSON, source string) []string {
	env := make([]string, 0, len(interpreterEnvNames)+2)
	for _, name := range interpreterEnvNames {
		if value, ok := os.LookupEnv(name); ok {
			env = append(env, name+"="+value)
		}
	}
	return append(env, varsEnv+"="+varsJSON, srcEnv+"="+source)
}

// execCommand is the default runner: the only part that touches os/exec. On a
// non-zero exit it surfaces the interpreter's stderr (e.g. a Python traceback or a
// PowerShell error), which is what makes the result useful for debugging a script
// rather than just reporting "exit status 1".
//
// It does not use cmd.Output(), which collects stdout into a bytes.Buffer with no
// ceiling: a script's output is written by code the author controls, so `while true:
// print(x)` is an unbounded allocation on this host, bounded only by the timeout —
// which is far too long to matter at a gigabyte a second. Both streams are collected
// through [capped] instead, so what a runaway script costs is the budget, once.
// The process is left to finish or hit its deadline; the bytes past the ceiling are
// dropped as they arrive, never held.
//
// The deadline ends the script's whole process group, or its Job Object on Windows.
// That alone did not end the call. os/exec reads stdout until EOF, and it stops
// watching the deadline once the interpreter has exited, so a process that had
// inherited stdout and outlived the interpreter held the call for as long as it
// ran: one a finished script left behind, or one that got away from the kill.
// WaitDelay bounds that wait by the script's own timeout. It is measured from the
// interpreter's exit or the deadline, whichever comes first, so it never cuts off
// output before the deadline has passed.
func execCommand(ctx context.Context, name string, args, env []string, max int64) ([]byte, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	group := configureProcessGroup(cmd)
	defer group.release()
	if deadline, ok := ctx.Deadline(); ok {
		cmd.WaitDelay = time.Until(deadline)
	}
	cmd.Env = env
	// stderr is quoted into an error message, so it needs far less room than the
	// result — the same reasoning os/exec applies when it caps ExitError.Stderr.
	stdout, stderr := &capped{max: max}, &capped{max: maxStderr}
	cmd.Stdout, cmd.Stderr = stdout, stderr
	err := cmd.Start()
	if err == nil {
		group.adopt()
		err = cmd.Wait()
	}
	if stdout.dropped {
		return nil, fmt.Errorf("script: output exceeded %d bytes", max)
	}
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		if msg := strings.TrimSpace(stripTerminalEscapes(stderr.String())); msg != "" {
			return stdout.Bytes(), fmt.Errorf("%w: %s", err, msg)
		}
	}
	return stdout.Bytes(), err
}

// stripTerminalEscapes removes the terminal control sequences an interpreter colours
// its diagnostics with. PowerShell 7 writes them even when stderr is a pipe (7.6
// stops once the bootstrap asks for plain text, 7.2 to 7.5 do not), and every place
// the message is read — the Modeler's Test run, an incident, the API — shows text,
// where ESC is an unprintable box and "[31;1m" is noise between every line. It
// knows the sequence shapes of ECMA-48: CSI (colours, cursor moves), the string
// controls ended by BEL or ST (OSC window titles and hyperlinks), and the short
// escapes. A sequence the stderr budget cut off is dropped to the end, so its
// parameter bytes do not leak into the message.
func stripTerminalEscapes(s string) string {
	if strings.IndexByte(s, 0x1b) < 0 {
		return s
	}
	var b strings.Builder
	b.Grow(len(s))
	for i := 0; i < len(s); {
		if s[i] != 0x1b {
			b.WriteByte(s[i])
			i++
			continue
		}
		i = skipEscape(s, i+1)
	}
	return b.String()
}

// skipEscape returns the index just past the escape sequence whose ESC precedes
// s[i]. A byte that cannot continue the sequence is not consumed, so a malformed
// sequence costs only its own bytes and never the text after it.
func skipEscape(s string, i int) int {
	if i >= len(s) {
		return i
	}
	switch c := s[i]; {
	case c == '[': // CSI: parameter and intermediate bytes, then one final byte
		i++
		for i < len(s) && s[i] >= 0x20 && s[i] <= 0x3f {
			i++
		}
		if i < len(s) && s[i] >= 0x40 && s[i] <= 0x7e {
			i++
		}
		return i
	case c == ']' || c == 'P' || c == 'X' || c == '^' || c == '_': // OSC, DCS, SOS, PM, APC
		for i++; i < len(s); i++ {
			if s[i] == 0x07 {
				return i + 1
			}
			if s[i] == 0x1b && i+1 < len(s) && s[i+1] == '\\' {
				return i + 2
			}
		}
		return i
	case c >= 0x20 && c <= 0x2f: // intermediate bytes, e.g. ESC ( B, then one final byte
		for i < len(s) && s[i] >= 0x20 && s[i] <= 0x2f {
			i++
		}
		if i < len(s) && s[i] >= 0x30 && s[i] <= 0x7e {
			i++
		}
		return i
	case c >= 0x30 && c <= 0x7e: // a two-byte escape such as ESC c or ESC 7
		return i + 1
	}
	return i
}

// capped is a buffer that stops growing at max and remembers that it did.
// Writing past the ceiling is not an error: returning one would close the pipe and
// turn a too-chatty script into a write failure whose message says nothing about the
// budget. The caller reads [capped.dropped] and says what actually happened.
//
// The bytes.Buffer is a *field* and not embedded, which is the whole reason this
// type works. os/exec hands a non-*os.File writer to io.Copy, and io.Copy prefers a
// destination's ReadFrom — which an embedded bytes.Buffer would promote, so every
// byte would land in the buffer without Write ever being called and the ceiling
// would silently do nothing. It read as a cap and was not one.
type capped struct {
	buf     bytes.Buffer
	max     int64
	dropped bool
}

func (c *capped) Write(p []byte) (int, error) {
	if room := c.max - int64(c.buf.Len()); room > 0 {
		if int64(len(p)) <= room {
			return c.buf.Write(p)
		}
		c.buf.Write(p[:room])
	}
	c.dropped = true
	return len(p), nil // accepted and discarded: the writer must not see a broken pipe
}

func (c *capped) Bytes() []byte  { return c.buf.Bytes() }
func (c *capped) String() string { return c.buf.String() }
func (c *capped) Len() int       { return c.buf.Len() }

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
//
// PlainText is asked for before the source runs, because the error PowerShell
// formats when the source does not even parse is written before any line of it
// could ask. The result is unaffected: ConvertTo-Json escapes ESC. PowerShell
// before 7.2 has no $PSStyle, hence the guard.
//
// A script that declares a param() block gets each declared parameter bound from
// the instance variable of that name, matched without regard to case as PowerShell
// matches parameters (ADR-0458). Without the binding the
// block declared locals that shadowed the variables: a mandatory parameter failed
// as missing, and any other ran on its default — empty, silently. Only declared
// names are passed, because an advanced script refuses a parameter it does not
// declare, and a variable is never passed for a parameter it merely resembles.
//
// Only the output stream is the result. PowerShell writes Write-Host, warnings,
// verbose and debug messages to stdout as well, where they broke the JSON the
// result is read from; the bootstrap merges those streams into the pipeline and
// writes them to stderr, which a failure quotes. Each output object is collected
// as it was written, so the result keeps the shape the plain assignment gave it.
// The result is written in a finally block because a script's exit ends the
// interpreter, not only the script: exit 0 after output still returns that output,
// as it does at a prompt, and a non-zero exit keeps its code.
const powershellBootstrap = `$ErrorActionPreference = 'Stop'
if ($PSStyle) { $PSStyle.OutputRendering = 'PlainText' }
$__atlas = if ($env:ATLAS_VARS) { $env:ATLAS_VARS | ConvertFrom-Json -AsHashtable } else { @{} }
foreach ($__k in $__atlas.Keys) { Set-Variable -Name $__k -Value $__atlas[$__k] }
$__script = [ScriptBlock]::Create($env:ATLAS_SCRIPT)
$__bound = @{}
if ($__script.Ast.ParamBlock) {
    $__names = @{}
    foreach ($__k in $__atlas.Keys) { $__names[$__k] = $__k }
    foreach ($__p in $__script.Ast.ParamBlock.Parameters) {
        $__n = $__p.Name.VariablePath.UserPath
        if ($__names.ContainsKey($__n)) { $__bound[$__n] = $__atlas[$__names[$__n]] }
    }
}
$__out = [System.Collections.Generic.List[object]]::new()
try {
    & $__script @__bound 3>&1 4>&1 5>&1 6>&1 | ForEach-Object {
        if ($_ -is [System.Management.Automation.WarningRecord]) { [Console]::Error.WriteLine('WARNING: ' + $_.Message) }
        elseif ($_ -is [System.Management.Automation.VerboseRecord]) { [Console]::Error.WriteLine('VERBOSE: ' + $_.Message) }
        elseif ($_ -is [System.Management.Automation.DebugRecord]) { [Console]::Error.WriteLine('DEBUG: ' + $_.Message) }
        elseif ($_ -is [System.Management.Automation.InformationRecord]) { [Console]::Error.WriteLine([string]$_.MessageData) }
        else { $__out.Add($_) }
    }
} finally {
    $__result = if ($__out.Count -eq 1) { $__out[0] } elseif ($__out.Count) { $__out.ToArray() }
    if ($null -eq $__result) { '' } else { $__result | ConvertTo-Json -Compress -Depth 64 }
}`

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
