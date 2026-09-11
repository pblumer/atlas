package script

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestParseSandboxMode(t *testing.T) {
	for _, raw := range []string{"", "off", " OFF "} {
		if got, err := ParseSandboxMode(raw); err != nil || got != SandboxOff {
			t.Errorf("ParseSandboxMode(%q) = %q, %v; want off, nil", raw, got, err)
		}
	}
	if got, err := ParseSandboxMode(" strict "); err != nil || got != SandboxStrict {
		t.Errorf("ParseSandboxMode(strict) = %q, %v; want strict, nil", got, err)
	}
	if _, err := ParseSandboxMode("best-effort"); err == nil || !strings.Contains(err.Error(), "best-effort") {
		t.Fatalf("unknown mode error = %v, want it to name best-effort", err)
	}
}

func TestCheckSandboxModes(t *testing.T) {
	if err := CheckSandbox(SandboxOff); err != nil {
		t.Errorf("off: %v", err)
	}
	if err := CheckSandbox(""); err != nil {
		t.Errorf("empty: %v", err)
	}
	if err := CheckSandbox("sometimes"); err == nil || !strings.Contains(err.Error(), "sometimes") {
		t.Fatalf("unknown mode error = %v", err)
	}
	if got, want := CheckSandbox(SandboxStrict), sandboxSupport(); (got == nil) != (want == nil) {
		t.Fatalf("strict support = %v, want same availability as %v", got, want)
	}
}

func TestStrictSandboxWrapsTheInterpreterAndUsesPrivateScratch(t *testing.T) {
	e := &CmdExec{Lang: Python, Bin: "sh", Sandbox: SandboxStrict}
	name, args, env, cleanup, err := e.prepareCommand([]string{"-c", "printf ok"}, []string{
		"PATH=" + os.Getenv("PATH"),
		"HOME=/shared-home",
		"TMPDIR=/shared-tmp",
		varsEnv + `={}`,
		srcEnv + `=result = "ok"`,
	})
	if err != nil {
		t.Fatalf("prepareCommand: %v", err)
	}
	if cleanup == nil {
		t.Fatal("strict sandbox returned no scratch cleanup")
	}

	wantExe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	if name != wantExe {
		t.Errorf("command = %q, want the Atlas executable %q", name, wantExe)
	}
	if len(args) < 6 || args[0] != sandboxSubcommand || args[1] != "--scratch" || args[3] != "--" {
		t.Fatalf("sandbox argv = %q, want internal subcommand, scratch and --", args)
	}
	scratch := args[2]
	if !filepath.IsAbs(scratch) {
		t.Errorf("scratch = %q, want an absolute path", scratch)
	}
	if info, err := os.Stat(scratch); err != nil || !info.IsDir() {
		t.Fatalf("scratch was not created as a directory: %v", err)
	}
	if !filepath.IsAbs(args[4]) {
		t.Errorf("interpreter = %q, want an absolute resolved path", args[4])
	}
	if !slices.Equal(args[5:], []string{"-c", "printf ok"}) {
		t.Errorf("interpreter args = %q, want original args", args[5:])
	}

	gotEnv := environmentMap(env)
	for _, key := range []string{"HOME", "TMPDIR", "TMP", "TEMP"} {
		if gotEnv[key] != scratch {
			t.Errorf("%s = %q, want private scratch %q", key, gotEnv[key], scratch)
		}
	}
	if gotEnv[varsEnv] != `{}` || gotEnv[srcEnv] != `result = "ok"` {
		t.Errorf("script contract changed: %s=%q %s=%q", varsEnv, gotEnv[varsEnv], srcEnv, gotEnv[srcEnv])
	}

	cleanup()
	if _, err := os.Stat(scratch); !os.IsNotExist(err) {
		t.Errorf("scratch still exists after cleanup: %v", err)
	}
}

func TestSandboxOffKeepsTheExistingExecutionPath(t *testing.T) {
	e := &CmdExec{Lang: Python, Bin: "sh", Sandbox: SandboxOff}
	env := []string{"PATH=" + os.Getenv("PATH"), varsEnv + `={}`}
	name, args, gotEnv, cleanup, err := e.prepareCommand([]string{"-c", "printf ok"}, env)
	if err != nil {
		t.Fatalf("prepareCommand: %v", err)
	}
	if name != "sh" || !slices.Equal(args, []string{"-c", "printf ok"}) {
		t.Errorf("command = %q %q, want unchanged interpreter command", name, args)
	}
	if !slices.Equal(gotEnv, env) {
		t.Errorf("environment = %q, want unchanged %q", gotEnv, env)
	}
	if cleanup != nil {
		t.Error("off mode unexpectedly created sandbox cleanup")
	}
}

func TestPrepareCommandRejectsInvalidStrictConfiguration(t *testing.T) {
	if _, _, _, _, err := (&CmdExec{Lang: Python, Sandbox: "sometimes"}).prepareCommand(nil, nil); err == nil {
		t.Error("unknown sandbox mode was accepted")
	}
	if _, _, _, _, err := (&CmdExec{Lang: Python, Bin: "atlas-no-such-interpreter", Sandbox: SandboxStrict}).prepareCommand(nil, nil); err == nil {
		t.Error("missing strict interpreter was accepted")
	}
	if interpreterInSandboxRuntime(filepath.Join(t.TempDir(), "missing")) {
		t.Error("missing interpreter was accepted as a sandbox runtime")
	}
}

func TestPrepareCommandReportsScratchCreationFailure(t *testing.T) {
	notDirectory := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(notDirectory, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("TMPDIR", notDirectory)
	_, _, _, cleanup, err := (&CmdExec{Lang: Python, Bin: "sh", Sandbox: SandboxStrict}).prepareCommand(nil, nil)
	if cleanup != nil {
		cleanup()
	}
	if err == nil || !strings.Contains(err.Error(), "create sandbox scratch") {
		t.Fatalf("scratch creation error = %v", err)
	}
}

func TestRunSandboxValidatesItsInternalProtocol(t *testing.T) {
	scratch := t.TempDir()
	file := filepath.Join(scratch, "not-a-directory")
	if err := os.WriteFile(file, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name string
		args []string
		want string
	}{
		{"shape", nil, "want --scratch"},
		{"relative scratch", []string{"--scratch", "relative", "--", "/usr/bin/python3"}, "not absolute"},
		{"missing scratch", []string{"--scratch", filepath.Join(scratch, "missing"), "--", "/usr/bin/python3"}, "inspect scratch"},
		{"scratch file", []string{"--scratch", file, "--", "/usr/bin/python3"}, "not a directory"},
		{"relative interpreter", []string{"--scratch", scratch, "--", "python3"}, "not absolute"},
		{"outside runtime", []string{"--scratch", scratch, "--", file}, "outside the sandbox runtime"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if err := RunSandbox(tc.args); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("RunSandbox error = %v, want %q", err, tc.want)
			}
		})
	}
}

func TestStrictSandboxRejectsAnInterpreterOutsideSystemRuntime(t *testing.T) {
	bin := filepath.Join(t.TempDir(), "python3")
	if err := os.WriteFile(bin, []byte("#!/bin/sh\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	e := &CmdExec{Lang: Python, Bin: bin, Sandbox: SandboxStrict}
	_, _, _, cleanup, err := e.prepareCommand(nil, nil)
	if cleanup != nil {
		cleanup()
	}
	if err == nil || !strings.Contains(err.Error(), "outside the sandbox runtime") {
		t.Fatalf("strict custom interpreter error = %v, want runtime-path refusal", err)
	}
}

func TestStrictSandboxRejectsADataDirectoryInsideItsRuntimeAllowlist(t *testing.T) {
	if err := CheckSandboxDataPath(SandboxStrict, "/usr/share/atlas-data"); err == nil {
		t.Fatal("strict sandbox accepted Atlas data below /usr")
	}
	if err := CheckSandboxDataPath(SandboxStrict, "/data"); err != nil {
		t.Errorf("strict sandbox rejected isolated /data: %v", err)
	}
	if err := CheckSandboxDataPath(SandboxOff, "/usr/share/atlas-data"); err != nil {
		t.Errorf("off mode changed the historical data path: %v", err)
	}
}

func TestStrictSandboxFailsClosedWhenTheKernelCannotEnforceIt(t *testing.T) {
	if err := sandboxSupport(); err != nil {
		if strings.Contains(err.Error(), "Landlock") {
			scratch := t.TempDir()
			if err := RunSandbox([]string{"--scratch", scratch, "--", "/usr/bin/true"}); err == nil {
				t.Fatal("sandbox launcher continued despite unavailable Landlock")
			}
		}
		err := (&CmdExec{Lang: Python, Bin: "sh", Sandbox: SandboxStrict}).Check()
		if err == nil || !strings.Contains(err.Error(), "sandbox") {
			t.Fatalf("strict Check = %v, want the unavailable sandbox reported", err)
		}
		return
	}
	if err := (&CmdExec{Lang: Python, Bin: "sh", Sandbox: SandboxStrict}).Check(); err != nil {
		t.Fatalf("strict Check on a supported kernel: %v", err)
	}
}

func TestProcessGroupCancelBeforeStartIsHarmless(t *testing.T) {
	cmd := exec.CommandContext(context.Background(), "sh", "-c", "true")
	configureProcessGroup(cmd)
	if err := cmd.Cancel(); !errors.Is(err, os.ErrProcessDone) {
		t.Fatalf("cancel before start = %v, want os.ErrProcessDone", err)
	}
}

func TestProcessGroupCancelAfterExitIsHarmless(t *testing.T) {
	cmd := exec.CommandContext(context.Background(), "sh", "-c", "true")
	configureProcessGroup(cmd)
	if err := cmd.Run(); err != nil {
		t.Fatalf("run: %v", err)
	}
	if err := cmd.Cancel(); !errors.Is(err, os.ErrProcessDone) {
		t.Fatalf("cancel after exit = %v, want os.ErrProcessDone", err)
	}
}

func TestStrictSandboxDeniesHostFilesAndSockets(t *testing.T) {
	if err := sandboxSupport(); err != nil {
		t.Skipf("kernel cannot exercise strict Landlock sandbox: %v", err)
	}
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("python3 not available")
	}
	python, err = filepath.Abs(python)
	if err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(t.TempDir(), "atlas-state-secret")
	if err := os.WriteFile(outside, []byte("must-not-be-readable"), 0o600); err != nil {
		t.Fatal(err)
	}
	scratch, err := os.MkdirTemp("", "atlas-sandbox-test-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(scratch) })

	cmd := exec.Command(os.Args[0], "-test.run=^TestStrictSandboxHelper$")
	cmd.Env = append(os.Environ(),
		"ATLAS_SANDBOX_HELPER=1",
		"ATLAS_SANDBOX_SCRATCH="+scratch,
		"ATLAS_SANDBOX_INTERPRETER="+python,
		"ATLAS_SANDBOX_OUTSIDE="+outside,
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("sandbox helper: %v\n%s", err, out)
	}
	var got map[string]any
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatalf("helper output %q: %v", out, err)
	}
	if got["file"] != "denied" || got["socket"] != "denied" || got["write"] != true || got["cwd"] != scratch {
		t.Fatalf("sandbox observations = %v, want outside file/socket denied and private scratch writable", got)
	}
}

func TestStrictSandboxHelper(t *testing.T) {
	if os.Getenv("ATLAS_SANDBOX_HELPER") != "1" {
		return
	}
	source := `
import json, os, socket, sys
result = {"cwd": os.getcwd()}
try:
    open(sys.argv[1]).read()
    result["file"] = "read"
except PermissionError:
    result["file"] = "denied"
try:
    socket.socket()
    result["socket"] = "opened"
except PermissionError:
    result["socket"] = "denied"
with open("result.txt", "w") as f:
    f.write("ok")
result["write"] = open("result.txt").read() == "ok"
print(json.dumps(result))
`
	err := runSandbox(os.Getenv("ATLAS_SANDBOX_SCRATCH"), []string{
		os.Getenv("ATLAS_SANDBOX_INTERPRETER"), "-c", source, os.Getenv("ATLAS_SANDBOX_OUTSIDE"),
	}, os.Environ())
	if err != nil {
		t.Fatal(err)
	}
}

// A process that has exited but whose parent has not reaped it is a zombie: it
// holds an entry in the process table, and kill(2) keeps addressing it, which is
// what made the liveness probe below answer "still running" about a process that
// was already dead. The scenario is not exotic — every descendant the timeout
// kills is orphaned onto PID 1 by the same signal, so whether it is reaped
// promptly is a property of the environment's init, not of Atlas. Under an init
// that does not reap (a container started from a plain process, a devbox),
// TestTimeoutKillsTheInterpretersWholeProcessGroup failed on a correct kill.
//
// This states the probe's contract directly, with a zombie made on purpose:
// exec.Cmd.Start without Wait leaves exactly one.
func TestProcessExistsReportsAnUnreapedProcessAsGone(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("a process's zombie state is read from /proc, which is Linux")
	}
	cmd := exec.Command("sh", "-c", "exit 0")
	if err := cmd.Start(); err != nil {
		t.Fatalf("start: %v", err)
	}
	pid := cmd.Process.Pid
	defer func() { _ = cmd.Wait() }() // reap it, whatever the test concluded

	// Wait for it to become a zombie, reading /proc directly rather than through
	// the function under test.
	deadline := time.Now().Add(2 * time.Second)
	for !isZombieAccordingToProc(t, pid) {
		if time.Now().After(deadline) {
			t.Fatalf("process %d never reached the zombie state", pid)
		}
		time.Sleep(5 * time.Millisecond)
	}

	if err := processExists(pid); err == nil {
		t.Errorf("processExists(%d) says a zombie is still a live process", pid)
	}
}

// isZombieAccordingToProc reads the state field of /proc/<pid>/stat. The comm
// field before it is parenthesised and may itself contain spaces, so the state
// is the character two past the last ')'.
func isZombieAccordingToProc(t *testing.T, pid int) bool {
	t.Helper()
	b, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid))
	if err != nil {
		return false
	}
	i := bytes.LastIndexByte(b, ')')
	if i < 0 || i+2 >= len(b) {
		t.Fatalf("unparsable /proc/%d/stat: %q", pid, b)
	}
	return b[i+2] == 'Z'
}

func TestTimeoutKillsTheInterpretersWholeProcessGroup(t *testing.T) {
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("sh not available")
	}
	pidFile := filepath.Join(t.TempDir(), "child.pid")
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	start := time.Now()
	_, err := execCommand(ctx, "sh", []string{"-c", `sleep 5 & echo $! > "$1"; wait`, "sh", pidFile}, nil, defaultMaxOutput)
	if err == nil {
		t.Fatal("timed command succeeded")
	}
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Fatalf("execCommand returned after %s; a descendant kept the command alive", elapsed)
	}
	b, err := os.ReadFile(pidFile)
	if err != nil {
		t.Fatalf("read child pid: %v", err)
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(b)))
	if err != nil {
		t.Fatalf("child pid %q: %v", b, err)
	}
	deadline := time.Now().Add(time.Second)
	for processExists(pid) == nil && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if err := processExists(pid); err == nil {
		t.Errorf("descendant process %d survived its script timeout", pid)
	}
}

func environmentMap(env []string) map[string]string {
	out := make(map[string]string, len(env))
	for _, kv := range env {
		if i := strings.IndexByte(kv, '='); i >= 0 {
			out[kv[:i]] = kv[i+1:]
		}
	}
	return out
}
