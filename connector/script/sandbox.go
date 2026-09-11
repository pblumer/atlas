package script

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// SandboxMode selects the operating-system boundary around an interpreter.
type SandboxMode string

const (
	// SandboxOff preserves the historical execution contract. It is the initial
	// default because existing models may deliberately use files or the network.
	SandboxOff SandboxMode = "off"
	// SandboxStrict permits only the installed runtime and a private per-run
	// scratch directory, and denies creation of every network socket.
	SandboxStrict SandboxMode = "strict"

	// SandboxEnv configures the built-in script Worker Instance. A supervised
	// worker receives the equivalent typed command-line flag from its parent.
	SandboxEnv = "ATLAS_SCRIPT_SANDBOX"

	sandboxSubcommand = "script-sandbox"
)

// ParseSandboxMode parses the operator-facing script sandbox setting. Empty is
// off for compatibility; there is deliberately no best-effort security mode.
func ParseSandboxMode(raw string) (SandboxMode, error) {
	switch mode := SandboxMode(strings.ToLower(strings.TrimSpace(raw))); mode {
	case "", SandboxOff:
		return SandboxOff, nil
	case SandboxStrict:
		return SandboxStrict, nil
	default:
		return "", fmt.Errorf("script: unknown sandbox mode %q (want off or strict)", strings.TrimSpace(raw))
	}
}

// CheckSandbox reports whether the current operating system can enforce mode.
// Strict callers use it at startup so selecting a boundary can never degrade to
// the historical unsandboxed path without being noticed.
func CheckSandbox(mode SandboxMode) error {
	if mode == "" || mode == SandboxOff {
		return nil
	}
	if mode != SandboxStrict {
		return fmt.Errorf("script: unknown sandbox mode %q", mode)
	}
	return sandboxSupport()
}

// ErrSandboxInterpreter marks an installed interpreter that cannot start inside the
// selected profile. It is deliberately distinct from a missing interpreter: that one
// parks a language's jobs and is worth a warning, while this one is the operator's
// explicit security choice failing to work at all, and ADR-0303's contract is that
// strict fails closed rather than degrading quietly.
var ErrSandboxInterpreter = errors.New("script sandbox: interpreter cannot start inside the sandbox")

// probeTimeout bounds the startup probe. It is not the script timeout: what is being
// timed is an interpreter starting and running an empty program, so this is already
// far past what any healthy host needs, cold start included.
const probeTimeout = 15 * time.Second

// probeSandbox runs the language's own bootstrap with an empty script through the
// complete production path — prepareCommand, the launcher, Landlock, seccomp, exec —
// and reports whether the interpreter survived it.
//
// Proving it by running it is the only check that stays true when a runtime, a
// distribution or a kernel changes what an interpreter touches on the way up.
// Comparing the allowlist against a list of expected paths would only ever confirm
// what somebody already knew, which is exactly what missed CoreCLR's need for /proc.
func (e *CmdExec) probeSandbox() error {
	probe := *e
	probe.Timeout = min(e.timeout(), probeTimeout)
	ctx, cancel := context.WithTimeout(context.Background(), probe.Timeout)
	defer cancel()
	if _, err := probe.Run(ctx, "", nil); err != nil {
		return fmt.Errorf("%w: %s: %w", ErrSandboxInterpreter, e.Lang.Name, err)
	}
	return nil
}

// CheckSandboxLanguages proves at startup that each language this process will serve
// can start under mode. Empty names mean every supported language, matching an
// external worker's default of serving all three.
//
// Only ErrSandboxInterpreter is returned: a language whose interpreter is simply not
// installed keeps parking its jobs, as it always has, because that is a host that was
// never going to run them rather than a sandbox that does not work.
func CheckSandboxLanguages(mode SandboxMode, names []string) error {
	if mode == "" || mode == SandboxOff {
		return nil
	}
	langs := Langs
	if len(names) > 0 {
		langs = make([]Lang, 0, len(names))
		for _, name := range names {
			lang, ok := LangByName(strings.ToLower(strings.TrimSpace(name)))
			if !ok {
				return fmt.Errorf("script: unknown language %q", name)
			}
			langs = append(langs, lang)
		}
	}
	for _, lang := range langs {
		e := New(lang)
		e.Sandbox = mode
		if err := e.Check(); err != nil && errors.Is(err, ErrSandboxInterpreter) {
			return err
		}
	}
	return nil
}

// CheckSandboxDataPath prevents a strict filesystem allowlist from accidentally
// containing Atlas state. The normal locations (/data, /var/lib/atlas and a
// working-directory-relative atlas-data) are outside the runtime roots.
func CheckSandboxDataPath(mode SandboxMode, path string) error {
	if mode == "" || mode == SandboxOff {
		return nil
	}
	resolved, err := resolveExistingPath(path)
	if err != nil {
		return fmt.Errorf("script sandbox: resolve data directory: %w", err)
	}
	for _, root := range sandboxRuntimeRoots() {
		rootResolved, err := resolveExistingPath(root)
		if err == nil && pathWithin(rootResolved, resolved) {
			return fmt.Errorf("script sandbox: data directory %q is inside allowed runtime %q", resolved, rootResolved)
		}
	}
	return nil
}

// prepareCommand turns an interpreter invocation into the internal sandbox
// launcher when strict mode is selected. The launcher receives only typed paths
// and the fixed bootstrap arguments; authored source remains in the environment.
func (e *CmdExec) prepareCommand(args, env []string) (name string, launchArgs, launchEnv []string, cleanup func(), err error) {
	mode := e.Sandbox
	if mode == "" {
		mode = SandboxOff
	}
	if mode == SandboxOff {
		return e.bin(), args, env, nil, nil
	}
	if mode != SandboxStrict {
		return "", nil, nil, nil, fmt.Errorf("script: unknown sandbox mode %q", mode)
	}
	interpreter, err := exec.LookPath(e.bin())
	if err != nil {
		return "", nil, nil, nil, err
	}
	interpreter, err = filepath.Abs(interpreter)
	if err != nil {
		return "", nil, nil, nil, fmt.Errorf("script: resolve interpreter path: %w", err)
	}
	if !interpreterInSandboxRuntime(interpreter) {
		return "", nil, nil, nil, fmt.Errorf("script: interpreter %q is outside the sandbox runtime", interpreter)
	}
	launcher, err := os.Executable()
	if err != nil {
		return "", nil, nil, nil, fmt.Errorf("script: resolve sandbox launcher: %w", err)
	}
	scratch, err := os.MkdirTemp("", "atlas-script-")
	if err != nil {
		return "", nil, nil, nil, fmt.Errorf("script: create sandbox scratch: %w", err)
	}
	remove := func() { _ = os.RemoveAll(scratch) }

	launchArgs = []string{sandboxSubcommand, "--scratch", scratch, "--", interpreter}
	launchArgs = append(launchArgs, args...)
	return launcher, launchArgs, privateScratchEnvironment(env, scratch), remove, nil
}

func privateScratchEnvironment(env []string, scratch string) []string {
	private := map[string]string{
		"HOME": scratch, "USERPROFILE": scratch,
		"TMPDIR": scratch, "TMP": scratch, "TEMP": scratch,
	}
	out := make([]string, 0, len(env)+len(private))
	for _, kv := range env {
		name, _, ok := strings.Cut(kv, "=")
		if _, replaced := private[name]; ok && replaced {
			continue
		}
		out = append(out, kv)
	}
	for _, name := range []string{"HOME", "USERPROFILE", "TMPDIR", "TMP", "TEMP"} {
		out = append(out, name+"="+private[name])
	}
	return out
}

// RunSandbox is the internal launcher entered by the Atlas executable. It is
// exported only because cmd/atlas owns subcommand dispatch; operators should use
// --script-sandbox on serve or worker instead of invoking this directly.
func RunSandbox(args []string) error {
	if len(args) < 4 || args[0] != "--scratch" || args[2] != "--" {
		return errors.New("script sandbox: want --scratch <absolute-directory> -- <interpreter> [args...]")
	}
	scratch := filepath.Clean(args[1])
	if !filepath.IsAbs(scratch) {
		return fmt.Errorf("script sandbox: scratch path %q is not absolute", args[1])
	}
	info, err := os.Stat(scratch)
	if err != nil {
		return fmt.Errorf("script sandbox: inspect scratch: %w", err)
	}
	if !info.IsDir() {
		return fmt.Errorf("script sandbox: scratch %q is not a directory", scratch)
	}
	interpreter := filepath.Clean(args[3])
	if !filepath.IsAbs(interpreter) {
		return fmt.Errorf("script sandbox: interpreter path %q is not absolute", args[3])
	}
	if !interpreterInSandboxRuntime(interpreter) {
		return fmt.Errorf("script sandbox: interpreter %q is outside the sandbox runtime", interpreter)
	}
	return runSandbox(scratch, append([]string{interpreter}, args[4:]...), os.Environ())
}

func interpreterInSandboxRuntime(path string) bool {
	resolved, err := resolveExistingPath(path)
	if err != nil {
		return false
	}
	for _, root := range sandboxRuntimeRoots() {
		rootResolved, err := resolveExistingPath(root)
		if err != nil {
			continue
		}
		if pathWithin(rootResolved, resolved) {
			return true
		}
	}
	return false
}

func pathWithin(root, path string) bool {
	rel, err := filepath.Rel(root, path)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// resolveExistingPath resolves symlinks even when the leaf does not exist yet,
// as is normal for a first-start data directory. It walks to the nearest existing
// ancestor, resolves that, and restores the missing suffix.
func resolveExistingPath(path string) (string, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	cur := filepath.Clean(abs)
	var suffix []string
	for {
		resolved, err := filepath.EvalSymlinks(cur)
		if err == nil {
			for i := len(suffix) - 1; i >= 0; i-- {
				resolved = filepath.Join(resolved, suffix[i])
			}
			return resolved, nil
		}
		parent := filepath.Dir(cur)
		if parent == cur {
			return "", err
		}
		suffix = append(suffix, filepath.Base(cur))
		cur = parent
	}
}
