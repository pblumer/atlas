//go:build aix || darwin || dragonfly || freebsd || linux || netbsd || openbsd || solaris

package script

import (
	"bytes"
	"errors"
	"os"
	"os/exec"
	"strconv"
	"syscall"
)

// configureProcessGroup gives one script and every subprocess it starts a group
// of their own. CommandContext's cancellation then kills the group instead of
// leaving grandchildren behind after the wall-clock deadline.
func configureProcessGroup(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		if cmd.Process == nil {
			return os.ErrProcessDone
		}
		err := syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		if errors.Is(err, syscall.ESRCH) {
			return os.ErrProcessDone
		}
		return err
	}
}

// processExists reports whether pid is still a live process, as the error nil.
// The signal-0 probe alone cannot answer that: a process that has exited but
// that nobody has reaped keeps its entry in the process table, and kill(2) goes
// on addressing it. That is the normal state of a descendant killed with its
// process group — the same signal orphans it onto PID 1, and whether PID 1
// reaps promptly is a property of the environment (an init that does, a
// container started from a plain process that does not), not of the kill. So a
// zombie is reported as gone, which is what it is.
func processExists(pid int) error {
	if err := syscall.Kill(pid, 0); err != nil {
		return err
	}
	if isZombie(pid) {
		return syscall.ESRCH
	}
	return nil
}

// isZombie reads the process state Linux publishes in /proc. Where that file is
// absent — macOS, the BSDs — the answer is "not known to be a zombie", which
// leaves the kill(2) probe as the only signal, exactly as before.
func isZombie(pid int) bool {
	b, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/stat")
	if err != nil {
		return false
	}
	// The comm field ahead of the state is parenthesised and may itself contain
	// spaces and parentheses, so the state is the character two past the *last*
	// ')' rather than a field the line can be split on.
	i := bytes.LastIndexByte(b, ')')
	if i < 0 || i+2 >= len(b) {
		return false
	}
	return b[i+2] == 'Z'
}
