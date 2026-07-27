package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"path/filepath"
	"testing"

	"github.com/pblumer/atlas/compiler"
	"github.com/pblumer/atlas/engine"
	"github.com/pblumer/atlas/state"
	"github.com/pblumer/atlas/wal"
)

// stubExec is a deterministic Exec: it returns a preset result without any
// interpreter, so the server wiring can be tested with no pwsh installed.
type stubExec struct{ result any }

func (s stubExec) Run(context.Context, string, map[string]any) (any, error) { return s.result, nil }

// greetingPwshBPMN is Start → PowerShell script task → End.
const greetingPwshBPMN = `<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL">
  <process id="greeting" isExecutable="true">
    <startEvent id="s"/>
    <scriptTask id="greet">
      <extensionElements><jobScript language="powershell" resultVariable="Greeting">Write-Output "Hallo"</jobScript></extensionElements>
    </scriptTask>
    <endEvent id="e"/>
    <sequenceFlow id="f1" sourceRef="s" targetRef="greet"/>
    <sequenceFlow id="f2" sourceRef="greet" targetRef="e"/>
  </process>
</definitions>`

// newPwshServer boots a full server over a temp dir with the given options and
// returns it plus an HTTP deploy harness.
func newPwshServer(t *testing.T, opts ...Option) (*Server, deployTestHarness) {
	t.Helper()
	dir := t.TempDir()
	log, err := wal.Open(wal.Options{Dir: filepath.Join(dir, "wal")})
	if err != nil {
		t.Fatalf("wal.Open: %v", err)
	}
	store, err := state.Open(filepath.Join(dir, "state"))
	if err != nil {
		t.Fatalf("state.Open: %v", err)
	}
	proc := engine.New(1, log, store, nil)
	if err := proc.Recover(); err != nil {
		t.Fatalf("Recover: %v", err)
	}
	srv, err := New(proc, store, dir, opts...)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(func() { srv.Close(); _ = store.Close(); _ = log.Close() })
	return srv, deployTestHarness{t, srv.Handler()}
}

// deployGreeting deploys the greeting process and returns its definition key.
func deployGreeting(t *testing.T, x deployTestHarness) uint64 {
	t.Helper()
	pid := x.mkProject("Greeting")
	x.saveDraft(pid, greetingPwshBPMN)
	code, b := x.do(http.MethodPost, "/api/v1/projects/"+pid+"/deploy", "")
	if code != http.StatusOK {
		t.Fatalf("deploy status=%d body=%s", code, b)
	}
	var rep projectDeployResp
	if err := json.Unmarshal(b, &rep); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !rep.Deployed || len(rep.Definitions) != 1 {
		t.Fatalf("deploy = %+v, want one definition deployed", rep)
	}
	return rep.Definitions[0].Key
}

// createInstanceStats creates an instance of key and returns its active counts.
func createInstanceStats(t *testing.T, x deployTestHarness, key uint64) (proc, elem int) {
	t.Helper()
	code, b := x.do(http.MethodPost, fmt.Sprintf("/api/v1/processes/%d/instances", key), "{}")
	if code != http.StatusOK {
		t.Fatalf("create instance status=%d body=%s", code, b)
	}
	var ci struct {
		Stats struct {
			ActiveProcessInstances int `json:"activeProcessInstances"`
			ActiveElementInstances int `json:"activeElementInstances"`
		} `json:"stats"`
	}
	if err := json.Unmarshal(b, &ci); err != nil {
		t.Fatalf("decode create: %v", err)
	}
	return ci.Stats.ActiveProcessInstances, ci.Stats.ActiveElementInstances
}

// TestPowerShellWorkerRunsScriptTask is the point of wiring the PowerShell worker
// into the server: with WithScriptWorker, a script task runs in-process and
// the instance completes instead of parking forever.
func TestPowerShellWorkerRunsScriptTask(t *testing.T) {
	srv, x := newPwshServer(t, WithScriptWorker(compiler.PwshJobTypeIndex, stubExec{result: "Hallo Anna"}))
	_ = srv
	key := deployGreeting(t, x)
	if pi, ei := createInstanceStats(t, x, key); pi != 0 || ei != 0 {
		t.Fatalf("stats = %d/%d, want 0/0 (script ran, instance completed)", pi, ei)
	}
}

// TestPowerShellWorkerDisabledByDefault: without the option no worker subscribes
// to the PowerShell job type, so a script task parks on its job (the opt-in
// posture — arbitrary interpreter execution is off unless an operator enables it).
func TestPowerShellWorkerDisabledByDefault(t *testing.T) {
	_, x := newPwshServer(t)
	key := deployGreeting(t, x)
	if pi, ei := createInstanceStats(t, x, key); pi != 1 || ei != 1 {
		t.Fatalf("stats = %d/%d, want 1/1 (no worker; parked on the script job)", pi, ei)
	}
}
