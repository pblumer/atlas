//go:build windows

package api

import (
	"os"
	"os/exec"
	"testing"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

// TestLifetimeHelper is the child the tests below start: a process that would run
// for two minutes unless something ends it. It does nothing when run as a test.
func TestLifetimeHelper(t *testing.T) {
	if os.Getenv("ATLAS_LIFETIME_HELPER") != "1" {
		return
	}
	time.Sleep(2 * time.Minute)
}

// startLifetimeHelper starts the helper and returns a channel closed when it exits.
func startLifetimeHelper(t *testing.T) (*exec.Cmd, <-chan struct{}) {
	t.Helper()
	cmd := exec.Command(os.Args[0], "-test.run=^TestLifetimeHelper$")
	cmd.Env = append(os.Environ(), "ATLAS_LIFETIME_HELPER=1")
	if err := cmd.Start(); err != nil {
		t.Fatalf("start the helper: %v", err)
	}
	exited := make(chan struct{})
	go func() {
		_ = cmd.Wait()
		close(exited)
	}()
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		<-exited
	})
	return cmd, exited
}

// A bound worker ends when the job's last handle closes. In production that handle
// is closed by Windows itself, when the server's process ends for any reason — a
// crash, "End task", a wrapper's hard kill — which is exactly the case the
// supervisor's own stop never reaches. Closing it here is the same event, caused
// by the test instead of by the server's death.
func TestABoundWorkerEndsWhenTheServersJobCloses(t *testing.T) {
	cmd, exited := startLifetimeHelper(t)

	var l childLifetime
	if err := l.bind(cmd.Process); err != nil {
		t.Fatalf("bind: %v", err)
	}
	select {
	case <-exited:
		t.Fatal("the child ended while its job was still open")
	default:
	}

	l.close()
	select {
	case <-exited:
	case <-time.After(10 * time.Second):
		t.Fatal("the child outlived the job it was bound to; a crashed server would leave it running")
	}
}

// One job holds every worker: a second bind joins it rather than replacing it, so
// the first worker is not left unbound by the second one starting.
func TestEveryBoundWorkerSharesTheJob(t *testing.T) {
	first, firstExited := startLifetimeHelper(t)
	second, secondExited := startLifetimeHelper(t)

	var l childLifetime
	for _, cmd := range []*exec.Cmd{first, second} {
		if err := l.bind(cmd.Process); err != nil {
			t.Fatalf("bind %d: %v", cmd.Process.Pid, err)
		}
	}
	l.close()
	for i, exited := range []<-chan struct{}{firstExited, secondExited} {
		select {
		case <-exited:
		case <-time.After(10 * time.Second):
			t.Errorf("worker %d outlived the job", i+1)
		}
	}
}

var procIsProcessInJob = windows.NewLazySystemDLL("kernel32.dll").NewProc("IsProcessInJob")

func processInJob(t *testing.T, pid int, job windows.Handle) bool {
	t.Helper()
	proc, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(pid))
	if err != nil {
		t.Fatalf("open process %d: %v", pid, err)
	}
	defer func() { _ = windows.CloseHandle(proc) }()
	var in int32
	if ok, _, err := procIsProcessInJob.Call(uintptr(proc), uintptr(job), uintptr(unsafe.Pointer(&in))); ok == 0 {
		t.Fatalf("IsProcessInJob: %v", err)
	}
	return in != 0
}

// The supervisor binds every worker it starts — not only a test that calls bind by
// hand. Without this the job would exist and hold nothing.
func TestTheSupervisorBindsEveryWorkerItStarts(t *testing.T) {
	quit := make(chan struct{})
	sup := newSupervisor(quit)
	sup.exe = os.Args[0]
	sup.add(SuperviseSpec{ID: "bound"}, []string{"-test.run=^TestLifetimeHelper$"},
		func() []string { return []string{"ATLAS_LIFETIME_HELPER=1"} })
	sup.start()
	defer func() { close(quit); sup.wait() }()

	waitFor(t, "the worker to report running", func() bool {
		list := sup.list()
		return len(list) == 1 && list[0].State == "running" && list[0].PID != 0
	})
	sup.lifetime.mu.Lock()
	job := sup.lifetime.job
	sup.lifetime.mu.Unlock()
	if job == 0 {
		t.Fatal("the supervisor started a worker without creating the job that ties it to the server")
	}
	if pid := sup.list()[0].PID; !processInJob(t, pid, job) {
		t.Errorf("worker %d is not in the server's job; a crashed server would leave it running", pid)
	}
}
