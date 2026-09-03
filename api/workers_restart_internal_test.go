package api

import (
	"encoding/json"
	"net/http"
	"os/exec"
	"sync"
	"testing"
	"time"
)

// supervisedServer gives a server a supervisor running `sh` rather than the real
// `atlas` binary: under `go test` os.Executable() is the test binary, and launching
// that would re-run the suite as a child. The restart path being exercised is the
// server's, not the child's, so any long-lived process stands in for a worker.
func supervisedServer(t *testing.T, id string, args ...string) (*Server, *supervisor) {
	t.Helper()
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("no sh on this machine")
	}
	srv := newServerForErrors(t)
	quit := make(chan struct{})
	sup := newSupervisor(quit)
	sup.exe = "sh"
	sup.backoff = time.Millisecond
	sup.add(SuperviseSpec{ID: id, Kinds: []string{"send-email"}}, args, nil)
	sup.start()
	srv.supervisor = sup
	t.Cleanup(func() { close(quit); sup.wait() })
	return srv, sup
}

func restart(t *testing.T, srv *Server, id string) (int, []byte) {
	t.Helper()
	return serveInternal(t, srv, http.MethodPost, "/api/v1/workers/"+id+"/restart", "", "")
}

// The console's restart button, end to end over HTTP. The "this server supervises
// nothing" 409 is not repeated here: main's TestRestartWorkerEndpoint already pins
// it, and it wires the supervisor without starting a child. What this adds is the
// half that needs a real process. The supervisor's own loop
// brings the worker back, so a button press and a crash take the identical path —
// which is why the assertion is on the *start count* rather than on some separate
// "restarted" flag: proving it came back the ordinary way is the point.
func TestRestartingASupervisedWorkerCyclesIt(t *testing.T) {
	srv, sup := supervisedServer(t, "mailer-1", "-c", "sleep 30")
	waitFor(t, "the child to report running", func() bool {
		list := sup.list()
		return len(list) == 1 && list[0].State == "running"
	})
	first := sup.list()[0].Starts

	code, raw := restart(t, srv, "mailer-1")
	if code != http.StatusOK {
		t.Fatalf("restart: status=%d body=%s", code, raw)
	}
	var got struct {
		ID         string `json:"id"`
		Restarting bool   `json:"restarting"`
	}
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("decode restart reply: %v (%s)", err, raw)
	}
	if got.ID != "mailer-1" || !got.Restarting {
		t.Errorf("restart reply = %+v, want mailer-1 restarting", got)
	}

	waitFor(t, "the child to be started again", func() bool {
		list := sup.list()
		return len(list) == 1 && list[0].Starts > first && list[0].State == "running"
	})
}

// A name this server does not supervise is a 404 rather than a silent success: the
// operator has just pressed a button, and "nothing happened" must not read as "done".
func TestRestartingAnUnknownWorkerIsNotFound(t *testing.T) {
	srv, _ := supervisedServer(t, "mailer-1", "-c", "sleep 30")
	code, raw := restart(t, srv, "no-such-worker")
	if code != http.StatusNotFound {
		t.Errorf("restart of an unknown worker: status=%d body=%s, want 404", code, raw)
	}
}

// The Workers view reports supervised workers, with the log tail an operator needs
// to see why one is failing. Without this the button above has nothing to act on.
func TestWorkersViewReportsSupervisedWorkers(t *testing.T) {
	srv, _ := supervisedServer(t, "mailer-1", "-c", "echo hello-from-the-worker; sleep 30")

	var got []childStatus
	waitFor(t, "the supervised worker to appear in the view with its output", func() bool {
		code, raw := serveInternal(t, srv, http.MethodGet, "/api/v1/workers", "", "")
		if code != http.StatusOK {
			return false
		}
		var resp struct {
			Supervised []childStatus `json:"supervised"`
		}
		if err := json.Unmarshal(raw, &resp); err != nil {
			return false
		}
		got = resp.Supervised
		return len(got) == 1 && got[0].State == "running" && len(got[0].Log) > 0
	})
	if got[0].ID != "mailer-1" {
		t.Errorf("supervised worker id = %q, want mailer-1", got[0].ID)
	}
	if got[0].PID == 0 {
		t.Error("a running supervised worker reports no pid, so an operator cannot find it")
	}
	if got[0].Log[0] != "hello-from-the-worker" {
		t.Errorf("log tail = %q, want the child's output", got[0].Log)
	}
}

// A server that supervises nothing still answers the view — with an empty list
// rather than a null, so the console renders "none" instead of failing to iterate.
func TestWorkersViewReportsNoSupervisedWorkersAsAnEmptyList(t *testing.T) {
	srv := newServerForErrors(t)
	code, raw := serveInternal(t, srv, http.MethodGet, "/api/v1/workers", "", "")
	if code != http.StatusOK {
		t.Fatalf("GET /workers: status=%d body=%s", code, raw)
	}
	var resp map[string]json.RawMessage
	if err := json.Unmarshal(raw, &resp); err != nil {
		t.Fatalf("decode workers: %v", err)
	}
	if string(resp["supervised"]) != "[]" {
		t.Errorf("supervised = %s, want []", resp["supervised"])
	}
}

// A worker holding a configuration that has since changed is restarted, because an
// operator who edits a mail worker in the Console means for it to take effect. The
// engine's own registries are rebuilt in place; a worker's cannot be, so cycling it
// is what "rebuild" means out there.
func TestAWorkerWhoseConfigurationChangedIsRestarted(t *testing.T) {
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("no sh on this machine")
	}
	srv := newServerForErrors(t)
	quit := make(chan struct{})
	sup := newSupervisor(quit)
	sup.exe, sup.backoff = "sh", time.Millisecond

	var mu sync.Mutex
	env := []string{"ATLAS_MAIL_CONNECTORS=post"}
	sup.add(SuperviseSpec{ID: "connectors", Connectors: []string{connectorKindMail}},
		[]string{"-c", "sleep 30"},
		func() []string { mu.Lock(); defer mu.Unlock(); return append([]string(nil), env...) })
	sup.start()
	srv.supervisor = sup
	t.Cleanup(func() { close(quit); sup.wait() })

	waitFor(t, "the child to report running", func() bool {
		list := sup.list()
		return len(list) == 1 && list[0].State == "running"
	})
	first := sup.list()[0].Starts

	// Nothing changed: refreshing must not cycle a healthy worker, or every unrelated
	// worker edit would cost a restart.
	srv.refreshSupervisedWorkers()
	if got := sup.list()[0].Starts; got != first {
		t.Errorf("starts = %d after an unchanged refresh, want %d", got, first)
	}

	mu.Lock()
	env = []string{"ATLAS_MAIL_CONNECTORS=post,zweite"}
	mu.Unlock()
	srv.refreshSupervisedWorkers()
	waitFor(t, "the child to come back with the new configuration", func() bool {
		return sup.list()[0].Starts > first
	})
}
