//go:build windows

package api

import (
	"os"
	"sync"
	"unsafe"

	"golang.org/x/sys/windows"
)

// childLifetime ties every worker this server supervises to the server's own life
// (ADR-draft-supervised-workers-end-with-the-server).
//
// Stopping the server already stops its workers: the supervisor kills each one when
// quit closes. That covers every exit the server takes part in, and none of the ones
// it does not — a crash, an out-of-memory kill, "End task", a service wrapper that
// gives up waiting. Windows has no parent-death signal, and a worker's poll loop
// retries an unreachable server forever by design (it must outlast a server
// restart), so a worker orphaned that way would keep running, and the restarted
// server would start a second set beside it.
//
// A job object with JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE is the operating system's
// answer: the server holds the only handle, the handle closes when the server's
// process ends however it ends, and closing it ends every process in the job —
// including what a worker started itself, such as a script interpreter, which
// inherits the job.
type childLifetime struct {
	mu  sync.Mutex
	job windows.Handle // zero until the first bind, and again after close
}

// bind puts p in the job, creating the job on first use. An error leaves p running
// unbound: the worker still serves, it only loses the guarantee above.
//
// There is a window between the child starting and this call in which a server
// crash would still orphan it. Closing it would mean starting the child suspended,
// which os/exec cannot do; the window is the few instructions between Start and
// here, and a worker starts nothing of its own until its first lease answers.
func (l *childLifetime) bind(p *os.Process) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.job == 0 {
		job, err := newKillOnCloseJob()
		if err != nil {
			return err
		}
		l.job = job
	}
	proc, err := windows.OpenProcess(windows.PROCESS_SET_QUOTA|windows.PROCESS_TERMINATE, false, uint32(p.Pid))
	if err != nil {
		return err
	}
	defer func() { _ = windows.CloseHandle(proc) }()
	return windows.AssignProcessToJobObject(l.job, proc)
}

// close releases the job, which ends whatever is still in it. The server never calls
// it: its handle is closed by the operating system when the process exits, which is
// the whole point. It is for a supervisor that is done while its process lives on.
func (l *childLifetime) close() {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.job != 0 {
		_ = windows.CloseHandle(l.job)
		l.job = 0
	}
}

// newKillOnCloseJob creates a job that ends its processes when its last handle
// closes. The handle is not inheritable, so a child cannot keep the job alive.
func newKillOnCloseJob() (windows.Handle, error) {
	job, err := windows.CreateJobObject(nil, nil)
	if err != nil {
		return 0, err
	}
	info := windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION{
		BasicLimitInformation: windows.JOBOBJECT_BASIC_LIMIT_INFORMATION{
			LimitFlags: windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE,
		},
	}
	if _, err := windows.SetInformationJobObject(job, windows.JobObjectExtendedLimitInformation,
		uintptr(unsafe.Pointer(&info)), uint32(unsafe.Sizeof(info))); err != nil {
		_ = windows.CloseHandle(job)
		return 0, err
	}
	return job, nil
}
