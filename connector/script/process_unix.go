//go:build aix || darwin || dragonfly || freebsd || linux || netbsd || openbsd || solaris

package script

import (
	"errors"
	"os"
	"os/exec"
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

func processExists(pid int) error { return syscall.Kill(pid, 0) }
