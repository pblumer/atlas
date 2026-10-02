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

// What a timed-out script must not leave behind: a descendant still doing work.
//
// The property is ADR-0303's — the deadline kills the interpreter's whole process
// group, not only the interpreter — and the observation it is read through is the
// part this test has now got wrong twice.
//
// It first read the descendant's liveness with kill(pid, 0), which cannot tell a
// live process from an unreaped one; #876 gave the probe /proc so a zombie reads as
// gone. It then failed again in CI, on a commit whose diff contained no Go at all,
// on a head whose parent had passed the same job twenty minutes earlier, and it has
// not reproduced once in the container it was written in — not in a full race build,
// not in ten consecutive focused runs. **The mechanism is not known.** What is known
// is that a pid is a number and not an identity: nothing in the old assertion tied
// 26241 back to the process that pid file was written for, so "that number still
// answers a signal" and "the descendant survived" were being treated as one fact
// when they are two.
//
// So the assertion is the descendant's own evidence instead. It is given a second of
// work and a file to write at the end of it; if the group kill reached it, the file
// is never written. That cannot be confounded by anything the pid namespace does,
// and it fails loudly in the one case the test is for — a descendant that outlives
// the script and goes on running.
//
// The signal probe is kept as a *diagnostic* rather than an assertion. An assertion
// that can fail while the system is correct is unsound whatever its subject, and
// this one demonstrably can; but what it reports is still the only lead on the open
// question, so a failure now says what that process actually is rather than only
// what it is numbered.
func TestTimeoutKillsTheInterpretersWholeProcessGroup(t *testing.T) {
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("sh not available")
	}
	// Two seconds of work for the descendant, and half of it as the bound on the call
	// itself. The two do not overlap on purpose: a shell that survived its own kill
	// blocks in `wait` and trips the elapsed check, a descendant that survived one
	// its shell did not writes the file, and neither failure can be mistaken for the
	// other. The work has to match the `sleep` in timeoutScript.
	const work = 2 * time.Second
	const returnWithin = work / 2

	// The deadline has to land after the shell has forked the descendant and recorded
	// its pid, or the run proves nothing about the kill. At 30ms it usually does, but
	// not on a loaded machine: the shell's redirection created the pid file before
	// echo wrote into it, a kill in between left the file empty, and the test failed
	// parsing "" although the group kill had worked.
	//
	// So the pid is written to a temporary name and renamed into place — the file
	// either holds the whole pid or does not exist — and a run whose deadline fell
	// before that point is repeated with a longer one. Every deadline stays well
	// inside returnWithin, so the elapsed check keeps its meaning.
	//
	// The ladder reaches 600ms because 250ms was not enough on a Windows runner:
	// there `sh` is Git Bash, whose fork is an emulation, and on a loaded machine all
	// three of 30, 100 and 250ms fell before the descendant existed, so the test
	// failed having tested nothing. The work above doubled to keep the longest
	// deadline well inside the bound on the call.
	for _, deadline := range []time.Duration{30 * time.Millisecond, 100 * time.Millisecond, 250 * time.Millisecond, 600 * time.Millisecond} {
		dir := t.TempDir()
		pidFile := filepath.Join(dir, "child.pid")
		outlived := filepath.Join(dir, "outlived")

		ctx, cancel := context.WithTimeout(context.Background(), deadline)
		start := time.Now()
		_, err := execCommand(ctx, "sh", []string{"-c",
			timeoutScript, "sh", pidFile, outlived}, nil, defaultMaxOutput)
		cancel()
		if err == nil {
			t.Fatal("timed command succeeded")
		}
		if elapsed := time.Since(start); elapsed > returnWithin {
			t.Fatalf("execCommand returned after %s; a descendant kept the command alive", elapsed)
		}

		// The pid file proves the descendant was forked before the deadline, which is
		// what makes the rest of this a test of the kill rather than of the timing.
		b, err := os.ReadFile(pidFile)
		if errors.Is(err, os.ErrNotExist) {
			t.Logf("the %s deadline fell before the shell recorded its descendant; "+
				"repeating with a longer one", deadline)
			continue
		}
		if err != nil {
			t.Fatalf("read child pid: %v", err)
		}
		pid, err := strconv.Atoi(strings.TrimSpace(string(b)))
		if err != nil {
			t.Fatalf("child pid %q: %v", b, err)
		}

		// Past the moment a surviving descendant would have finished its second.
		time.Sleep(time.Until(start.Add(work + 400*time.Millisecond)))
		if _, err := os.Stat(outlived); err == nil {
			t.Errorf("descendant process %d ran to completion after its script timed out", pid)
		}
		if err := processExists(pid); err == nil {
			t.Logf("pid %d still answers a signal, %s; the descendant did not finish its "+
				"work, so this is a pid that outlived its meaning rather than a failed kill",
				pid, procSummary(pid))
		}
		return
	}
	t.Fatal("no deadline landed after the shell had recorded its descendant, so the kill " +
		"was never tested")
}

// timeoutScript forks a descendant that works for two seconds and then writes $2,
// and records its pid in $1 — atomically, by writing a temporary name and renaming
// it, so a kill can never leave $1 existing and empty. A variable so that a test can
// hold the rename in place without running the whole kill.
var timeoutScript = `sleep 2 && : > "$2" & echo $! > "$1.tmp" && mv "$1.tmp" "$1"; wait`

// A process holding a script's output does not outlast the script's timeout. A
// script that exits and leaves one behind never met its deadline at all: os/exec
// stops watching the context once the interpreter has exited, and reads stdout until
// EOF, so the call returned when that process ended, however long after the deadline
// that was. On Windows every timed-out script's descendants did the same, since only
// the interpreter used to be killed.
//
// The case only exists once the script has exited and left its process behind, so
// the run proves something only if that happened before the deadline. A deadline
// that falls first kills a script still running, and the call rightly reports that
// kill instead — on a Windows runner, where `sh` is Git Bash and a loaded machine
// took longer than 200ms to start it, that was "exit status 1" from the Job
// Object, and the test failed having tested nothing. So the script marks the
// moment it has left its process behind, and a run whose deadline fell before the
// mark is repeated with a longer one, as TestTimeoutKillsTheInterpretersWholeProcessGroup
// does for its descendant.
func TestAProcessHoldingTheOutputDoesNotOutlastTheTimeout(t *testing.T) {
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("sh not available")
	}
	// The process left behind works for five seconds, and every bound on the call
	// stays well short of that, so a call that waited for it cannot pass.
	const held = 5 * time.Second
	for _, timeout := range []time.Duration{200 * time.Millisecond, 600 * time.Millisecond, 1200 * time.Millisecond} {
		marked := filepath.Join(t.TempDir(), "left-behind")
		ctx, cancel := context.WithTimeout(context.Background(), timeout)
		start := time.Now()
		_, err := execCommand(ctx, "sh", []string{"-c", heldOutputScript, "sh", marked}, nil, defaultMaxOutput)
		cancel()
		if elapsed, limit := time.Since(start), 2*timeout+2*time.Second; elapsed > limit {
			t.Fatalf("execCommand returned after %s with a %s timeout; it waited for the process "+
				"the script left behind, which holds the output for %s", elapsed, timeout, held)
		}
		if _, statErr := os.Stat(marked); errors.Is(statErr, os.ErrNotExist) {
			t.Logf("the %s deadline fell before the script had left its process behind (%v); "+
				"repeating with a longer one", timeout, err)
			continue
		}
		// An output cut off is not a clean run: the process that held it may have had
		// more to write.
		if !errors.Is(err, exec.ErrWaitDelay) {
			t.Errorf("error = %v, want exec.ErrWaitDelay", err)
		}
		return
	}
	t.Fatal("no deadline landed after the script had left its process behind, so the wait " +
		"for it was never tested")
}

// heldOutputScript leaves a process holding stdout for five seconds, marks $1 once
// it has, and exits. The mark comes after the fork and before the exit, so its
// presence says the script got as far as leaving the process behind.
var heldOutputScript = `sleep 5 & : > "$1"; exit 0`

// procSummary is what /proc knows about a pid, for a message that would otherwise be
// a bare number. A recycled pid names a different program here, which is the first
// thing to check the next time this comes up.
func procSummary(pid int) string {
	b, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/stat")
	if err != nil {
		return fmt.Sprintf("no /proc entry (%v)", err)
	}
	i := bytes.LastIndexByte(b, ')')
	if i < 1 || i+2 >= len(b) {
		return fmt.Sprintf("unparsable stat %q", b)
	}
	open := bytes.IndexByte(b[:i], '(')
	if open < 0 {
		return fmt.Sprintf("unparsable stat %q", b)
	}
	return fmt.Sprintf("running %q in state %q", b[open+1:i], b[i+2])
}

// The startup proof resolves what it will launch before it launches anything, and
// resolves nothing at all for a compatible profile — an installation on off must keep
// exactly the boot it had before the sandbox existed.
func TestSandboxLanguagesResolveOnlyForAProfileThatMustBeProved(t *testing.T) {
	for _, mode := range []SandboxMode{"", SandboxOff} {
		langs, err := sandboxLanguages(mode, []string{"python"})
		if err != nil || langs != nil {
			t.Errorf("mode %q resolved %v (err %v), want nothing to launch", mode, langs, err)
		}
	}
	langs, err := sandboxLanguages(SandboxStrict, nil)
	if err != nil || len(langs) != len(Langs) {
		t.Errorf("strict with no filter resolved %d languages (err %v), want all %d", len(langs), err, len(Langs))
	}
	if langs, err := sandboxLanguages(SandboxStrict, []string{" PowerShell "}); err != nil || len(langs) != 1 || langs[0].Name != "powershell" {
		t.Errorf("resolved %v (err %v), want powershell alone", langs, err)
	}
	if _, err := sandboxLanguages(SandboxStrict, []string{"powershell", "klingon"}); err == nil || !strings.Contains(err.Error(), "klingon") {
		t.Errorf("error = %v, want the unknown language named", err)
	}
}
