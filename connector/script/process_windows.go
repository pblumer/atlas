//go:build windows

package script

import (
	"errors"
	"os"
	"os/exec"
	"sync"
	"syscall"

	"golang.org/x/sys/windows"
)

// processGroup is what the deadline kills on Windows: a Job Object holding the
// interpreter and, because a job's membership is inherited by every process started
// inside it, everything the script starts. It is the Windows form of the Unix
// process group (ADR-0303), and without it the timeout was not a bound.
//
// Killing only the interpreter left its descendants running. One that inherited the
// interpreter's stdout keeps the pipe open after the interpreter is gone, and os/exec
// reads that pipe until EOF, so a script that had started a long-running program
// returned when that program ended, however long after its deadline.
//
// A process can only join a job once it exists, so the interpreter is adopted right
// after Start. Anything it started before then stays outside the job, but none of it
// comes from the script: the interpreter reads the author's source only after it
// has started. Whatever escapes is still bounded by WaitDelay in execCommand.
type processGroup struct {
	cmd *exec.Cmd
	mu  sync.Mutex
	job windows.Handle // zero until adopt succeeds, and again after release
}

func configureProcessGroup(cmd *exec.Cmd) *processGroup {
	g := &processGroup{cmd: cmd}
	cmd.Cancel = g.cancel
	return g
}

// adopt moves the started interpreter into a job of its own. A host that refuses
// that (it takes an unusual job around Atlas itself) keeps the direct kill the
// interpreter always had: the deadline still ends the interpreter, and WaitDelay
// still bounds the wait for what it left behind.
func (g *processGroup) adopt() {
	g.mu.Lock()
	defer g.mu.Unlock()
	job, err := windows.CreateJobObject(nil, nil)
	if err != nil {
		return
	}
	proc, err := windows.OpenProcess(windows.PROCESS_SET_QUOTA|windows.PROCESS_TERMINATE, false, uint32(g.cmd.Process.Pid))
	if err != nil {
		_ = windows.CloseHandle(job)
		return
	}
	defer windows.CloseHandle(proc)
	if err := windows.AssignProcessToJobObject(job, proc); err != nil {
		_ = windows.CloseHandle(job)
		return
	}
	g.job = job
}

// release closes the job. It does not end what is still running in it: a script
// that finished and left a process behind keeps it, as on Unix, where the group is
// killed only when the deadline passes.
func (g *processGroup) release() {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.job != 0 {
		_ = windows.CloseHandle(g.job)
		g.job = 0
	}
}

// cancel is what the deadline calls. Before Start and after Wait it has nothing to
// end and says so with os.ErrProcessDone, as the Unix one does. os reports a process
// that has been waited for as EINVAL on Windows (the handle is released, and it keeps
// that status for compatibility), and ProcessState cannot be read here instead:
// os/exec may call Cancel while Wait is still assigning it.
func (g *processGroup) cancel() error {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.cmd.Process == nil {
		return os.ErrProcessDone
	}
	if g.job != 0 {
		return windows.TerminateJobObject(g.job, 1)
	}
	err := g.cmd.Process.Kill()
	if errors.Is(err, syscall.EINVAL) {
		return os.ErrProcessDone
	}
	return err
}

func processExists(int) error { return errors.New("process lookup unsupported") }
