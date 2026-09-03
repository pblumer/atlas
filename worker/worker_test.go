package worker_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/pblumer/atlas/api"
	"github.com/pblumer/atlas/compiler"
	"github.com/pblumer/atlas/engine"
	"github.com/pblumer/atlas/state"
	"github.com/pblumer/atlas/wal"
	"github.com/pblumer/atlas/worker"
)

const jobWorkerBPMN = `<?xml version="1.0" encoding="UTF-8"?>
<bpmn:definitions xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL"
                  xmlns:zeebe="http://camunda.org/schema/zeebe/1.0" id="defs">
  <bpmn:process id="orders" isExecutable="true">
    <bpmn:startEvent id="s"/>
    <bpmn:serviceTask id="t">
      <bpmn:extensionElements><zeebe:taskDefinition type="send-email" retries="3"/></bpmn:extensionElements>
    </bpmn:serviceTask>
    <bpmn:endEvent id="e"/>
    <bpmn:sequenceFlow id="f1" sourceRef="s" targetRef="t"/>
    <bpmn:sequenceFlow id="f2" sourceRef="t" targetRef="e"/>
  </bpmn:process>
</bpmn:definitions>`

// liveAtlas starts a real Atlas over HTTP with one instance parked on a service
// task, so the worker is exercised against the actual protocol — the lease, the
// fencing token, the completion — rather than a stub of it.
func liveAtlas(t *testing.T, vars string) *httptest.Server {
	t.Helper()
	return liveAtlasWith(t, jobWorkerBPMN, vars)
}

// liveAtlasWith is liveAtlas over a given model and server options.
func liveAtlasWith(t *testing.T, model, vars string, opts ...api.Option) *httptest.Server {
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
	srv, err := api.New(proc, store, dir, opts...)
	if err != nil {
		t.Fatalf("api.New: %v", err)
	}
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(func() {
		ts.Close()
		srv.Close()
		_ = store.Close()
		_ = log.Close()
	})
	post(t, ts, "/api/v1/deployments", model, "application/xml")
	post(t, ts, "/api/v1/processes/1/instances", `{"variables":`+vars+`}`, "application/json")
	return ts
}

// TestWorkerRunsAJobAndCompletesIt is the whole point: a process parks on a service
// task, an out-of-process worker picks the job up by type, does the work, and the
// instance moves on — with no in-process handler involved anywhere.
func TestWorkerRunsAJobAndCompletesIt(t *testing.T) {
	ts := liveAtlas(t, `{"to":"a@example.com"}`)
	seen := make(chan worker.Job, 1)
	w := worker.New(worker.Options{
		Server: ts.URL,
		ID:     "mailer-1",
		Handlers: map[string]worker.Exec{
			"send-email": worker.ExecFunc(func(_ context.Context, j worker.Job) (map[string]any, error) {
				seen <- j
				return map[string]any{"sent": true}, nil
			}),
		},
	})

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	if err := w.RunOnce(ctx); err != nil {
		t.Fatalf("RunOnce: %v", err)
	}

	select {
	case j := <-seen:
		if j.Type != "send-email" {
			t.Errorf("job type = %q, want send-email", j.Type)
		}
		if j.LeaseToken == 0 {
			t.Error("the job carried no lease token, so its completion could not be fenced")
		}
		// The worker is handed what the task sees, so it need not fetch variables
		// separately and race a concurrent write.
		if j.Variables["to"] != "a@example.com" {
			t.Errorf("variables = %v, want to=a@example.com", j.Variables)
		}
	default:
		t.Fatal("the worker never ran the job")
	}

	// The instance finished, and the worker's output landed in it.
	if running := runningInstances(t, ts); running != 0 {
		t.Errorf("%d instances still running, want 0 — the job was not completed", running)
	}
}

// A command that fails must fail the job rather than drop it: the engine's retry and
// incident machinery is what an operator sees, and a worker that swallowed the error
// would park the token silently forever.
func TestWorkerFailsAJobWhenTheWorkFails(t *testing.T) {
	ts := liveAtlas(t, `{}`)
	w := worker.New(worker.Options{
		Server: ts.URL,
		ID:     "mailer-1",
		Handlers: map[string]worker.Exec{
			"send-email": worker.ExecFunc(func(context.Context, worker.Job) (map[string]any, error) {
				return nil, errors.New("smtp: connection refused")
			}),
		},
	})
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	if err := w.RunOnce(ctx); err != nil {
		t.Fatalf("RunOnce: %v", err)
	}

	// The job is back with one retry spent, and the reason is on it.
	jobs := instanceJobs(t, ts)
	if len(jobs) != 1 {
		t.Fatalf("%d activatable jobs, want the failed one back on offer", len(jobs))
	}
	if jobs[0].Retries != 2 {
		t.Errorf("retries = %d after one failure of three, want 2", jobs[0].Retries)
	}
}

// A worker that handles nothing is a configuration error, not a process that quietly
// polls forever.
func TestWorkerNeedsAtLeastOneHandler(t *testing.T) {
	ts := liveAtlas(t, `{}`)
	w := worker.New(worker.Options{Server: ts.URL, ID: "idle"})
	if err := w.RunOnce(context.Background()); err == nil {
		t.Error("RunOnce with no handlers succeeded, want an error naming the misconfiguration")
	}
}

// The command contract: the job arrives as JSON on stdin and result variables are
// read back as JSON from stdout — the same "stdout is the result" shape the script
// worker already uses, so one contract covers both.
func TestCmdExecPassesTheJobOnStdinAndReadsVariablesFromStdout(t *testing.T) {
	if _, err := lookPath("sh"); err != nil {
		t.Skip("no sh on this machine")
	}
	e := worker.CmdExec{Name: "sh", Args: []string{"-c",
		`cat > /tmp/atlas-worker-stdin.json; echo '{"greeting":"hello"}'`}}
	got, err := e.Run(context.Background(), worker.Job{
		JobKey: 7, Type: "send-email", Variables: map[string]any{"who": "world"},
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if got["greeting"] != "hello" {
		t.Errorf("variables = %v, want greeting=hello", got)
	}
	stdin := readFile(t, "/tmp/atlas-worker-stdin.json")
	for _, want := range []string{`"jobKey":7`, `"send-email"`, `"who":"world"`} {
		if !strings.Contains(strings.ReplaceAll(stdin, " ", ""), strings.ReplaceAll(want, " ", "")) {
			t.Errorf("the command's stdin %q does not carry %q", stdin, want)
		}
	}
}

// A command that exits non-zero fails the job, and its stderr is the message an
// operator will read on the incident.
func TestCmdExecSurfacesTheCommandsError(t *testing.T) {
	if _, err := lookPath("sh"); err != nil {
		t.Skip("no sh on this machine")
	}
	e := worker.CmdExec{Name: "sh", Args: []string{"-c", `echo "boom" >&2; exit 3`}}
	_, err := e.Run(context.Background(), worker.Job{JobKey: 1, Type: "t"})
	if err == nil {
		t.Fatal("a non-zero exit returned no error")
	}
	if !strings.Contains(err.Error(), "boom") {
		t.Errorf("error = %v, want it to carry the command's stderr", err)
	}
}

// TestWorkerSurvivesAJobTypeThatIsNotDeployedYet is the failure a supervised worker
// hits first: it starts with the server, and the process using its job type may not
// be deployed yet. The engine answers 404 for a type it has never seen — the honest
// answer, and what makes a typo visible — so the worker must treat it as "not yet"
// and keep polling rather than exiting.
func TestWorkerSurvivesAJobTypeThatIsNotDeployedYet(t *testing.T) {
	ts := liveAtlas(t, `{}`)
	w := worker.New(worker.Options{
		Server: ts.URL, ID: "early", Wait: 50 * time.Millisecond, Retry: 10 * time.Millisecond,
		Handlers: map[string]worker.Exec{
			"never-deployed": worker.ExecFunc(func(context.Context, worker.Job) (map[string]any, error) {
				return nil, nil
			}),
		},
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- w.Run(ctx) }()

	select {
	case err := <-done:
		t.Fatalf("the worker exited on an unknown job type instead of waiting for it: %v", err)
	case <-time.After(400 * time.Millisecond):
		// Still polling, which is the point.
	}
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Errorf("Run returned %v after cancellation, want nil", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the worker did not stop when its context was cancelled")
	}
}

// TestWorkerRunsAnOffloadedCsvConnector is ADR-0168's first slice end to end: a
// Worker Type the engine no longer serves, worked by an external process. The
// engine resolved the task — found its detail in the compiled process and read the
// source text up the scope chain — and the worker did the parsing, which needs
// nothing but the values it was handed. No credential is involved, which is exactly
// why this kind goes first: the mechanism is proved before any secret rides on it.
func TestWorkerRunsAnOffloadedCsvConnector(t *testing.T) {
	ts := liveAtlasWith(t, csvConnectorModel, `{"upload":"name,amount\nAda,12\nGrace,7\n"}`,
		api.WithOffloadedConnectorKinds([]string{"csv"}))

	w := worker.New(worker.Options{
		Server: ts.URL, ID: "csv-1", Handlers: mustConnectors(t, "csv"),
	})
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	if err := w.RunOnce(ctx); err != nil {
		t.Fatalf("RunOnce: %v", err)
	}

	if running := runningInstances(t, ts); running != 0 {
		t.Errorf("%d instances still running, want 0 — the worker job was not completed", running)
	}
	vars := instanceVariables(t, ts)
	rows, ok := vars["orders"]
	if !ok {
		t.Fatalf("the result variable was not written; variables = %v", vars)
	}
	list, ok := rows.([]any)
	if !ok || len(list) != 2 {
		t.Fatalf("orders = %v, want the two parsed rows", rows)
	}
	if first, _ := list[0].(map[string]any); first["name"] != "Ada" {
		t.Errorf("first row = %v, want Ada", list[0])
	}
	if vars["rowCount"] != float64(2) {
		t.Errorf("rowCount = %v, want 2", vars["rowCount"])
	}
}

// A worker asked for a Worker Type it does not implement is a configuration
// error, not a process that leases work it cannot do.
func TestBuiltinConnectorsRejectsAnUnknownKind(t *testing.T) {
	// Refused by name rather than by a handler count: script alone contributes one
	// handler per language, so counting would call a correct configuration wrong.
	_, err := worker.BuiltinConnectors(nil, "no-such-kind")
	if err == nil {
		t.Fatal("an unknown Worker Type was accepted")
	}
	if !strings.Contains(err.Error(), "no-such-kind") {
		t.Errorf("error = %v, want it to name the kind it does not implement", err)
	}
}

// A parse failure on the worker is reported as a job failure, so it becomes the
// engine's retry and then an incident. A worker that completed the job with zero
// rows would turn a broken upload into a silently empty import.
func TestOffloadedCsvReportsAParseFailure(t *testing.T) {
	run := mustConnectors(t, "csv")[compiler.CsvImportJobType]
	_, err := run.Run(context.Background(), worker.Job{
		JobKey: 1, Type: compiler.CsvImportJobType,
		Connector: &worker.ConnectorPayload{
			Kind: "csv",
			// A headerless file that names no columns: nothing says what the cells are.
			Fields: map[string]any{"source": "Ada,12\n", "hasHeader": false},
		},
	})
	if err == nil {
		t.Fatal("a CSV job the parser cannot use succeeded, want an error")
	}
	if !strings.Contains(err.Error(), "csv-import") {
		t.Errorf("error = %v, want the parser's own reason", err)
	}
}

const csvConnectorModel = `<?xml version="1.0" encoding="UTF-8"?>
<bpmn:definitions xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL"
                  xmlns:atlas="http://atlas.dev/schema/1.0" id="defs">
  <bpmn:process id="import" isExecutable="true">
    <bpmn:startEvent id="s"/>
    <bpmn:serviceTask id="t">
      <bpmn:extensionElements>
        <atlas:csvConnector source="upload" resultVariable="orders" hasHeader="true"/>
      </bpmn:extensionElements>
    </bpmn:serviceTask>
    <bpmn:endEvent id="e"/>
    <bpmn:sequenceFlow id="f1" sourceRef="s" targetRef="t"/>
    <bpmn:sequenceFlow id="f2" sourceRef="t" targetRef="e"/>
  </bpmn:process>
</bpmn:definitions>`

// TestWorkerRunKeepsWorkingUntilCancelled covers continuous Run rather than the
// one-shot RunOnce every other test here drives. It is the mode a supervised worker
// actually runs in, and until now nothing proved it completes a job at all — only
// that it survives one that fails.
func TestWorkerRunKeepsWorkingUntilCancelled(t *testing.T) {
	ts := liveAtlas(t, `{"to":"a@example.com"}`)
	done := make(chan struct{})
	var once sync.Once
	w := worker.New(worker.Options{
		Server: ts.URL, ID: "mailer-1", Wait: 50 * time.Millisecond, Retry: 10 * time.Millisecond,
		Handlers: map[string]worker.Exec{
			"send-email": worker.ExecFunc(func(context.Context, worker.Job) (map[string]any, error) {
				once.Do(func() { close(done) })
				return map[string]any{"sent": true}, nil
			}),
		},
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	exited := make(chan error, 1)
	go func() { exited <- w.Run(ctx) }()

	select {
	case <-done:
	case <-time.After(20 * time.Second):
		t.Fatal("the worker never took the job")
	}
	waitFor(t, "the instance to move on", func() bool { return runningInstances(t, ts) == 0 })

	// It is still polling after the queue emptied — a worker that exited when it ran
	// out of work would need the supervisor to keep restarting it.
	cancel()
	select {
	case err := <-exited:
		if err != nil {
			t.Errorf("Run returned %v after cancellation, want nil", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the worker did not stop when its context was cancelled")
	}
}

// Run refuses a worker with nothing to handle, the same as RunOnce. Both entry
// points check, because a supervised worker only ever reaches Run.
func TestWorkerRunNeedsAtLeastOneHandler(t *testing.T) {
	w := worker.New(worker.Options{Server: "http://127.0.0.1:1", ID: "idle"})
	if err := w.Run(context.Background()); err == nil {
		t.Error("Run with no handlers succeeded, want an error naming the misconfiguration")
	}
}

// RunOnce returns a poll failure rather than swallowing it: it is the one-shot mode
// ("drain the queue and exit"), so an unreachable server has to be an exit code and
// not a silent success. Continuous Run deliberately does the opposite, and
// TestWorkerSurvivesAJobTypeThatIsNotDeployedYet holds that line.
func TestRunOnceReportsAnUnreachableServer(t *testing.T) {
	w := worker.New(worker.Options{
		// Port 1 is not something a test can accidentally be served by.
		Server: "http://127.0.0.1:1", ID: "orphan",
		Handlers: map[string]worker.Exec{
			"send-email": worker.ExecFunc(func(context.Context, worker.Job) (map[string]any, error) {
				return nil, nil
			}),
		},
	})
	if err := w.RunOnce(context.Background()); err == nil {
		t.Error("RunOnce against an unreachable server succeeded, want the dial error")
	}
}

// The server's refusal reaches the operator verbatim. A worker pulling a type the
// engine still serves itself is the mistake this actually catches — the 409 says so,
// and a bare "status 409" would send someone reading engine source instead.
func TestPollErrorCarriesTheServersMessage(t *testing.T) {
	ts := liveAtlas(t, `{}`)
	w := worker.New(worker.Options{
		Server: ts.URL, ID: "greedy",
		Handlers: map[string]worker.Exec{
			// Served in-process by this server, so the pull is refused.
			compiler.MailJobType: worker.ExecFunc(func(context.Context, worker.Job) (map[string]any, error) {
				return nil, nil
			}),
		},
	})
	err := w.RunOnce(context.Background())
	if err == nil {
		t.Fatal("pulling a type the engine serves itself succeeded, want a refusal")
	}
	if !strings.Contains(err.Error(), "/api/v1/jobs/activate") {
		t.Errorf("error = %v, want it to name the call that failed", err)
	}
	if strings.Contains(err.Error(), "status 409") {
		t.Errorf("error = %v, want the server's own message rather than a bare status", err)
	}
}

// A job on its last attempt fails to zero rather than to minus one. This is about
// what the worker *sends*, so it is proved against a stub that hands out a job with
// no attempts left: a real engine raises the incident at zero and stops offering the
// job, so it never produces this input — which is exactly why the clamp is here and
// why only a stub can reach it. A negative budget would be one the engine cannot
// interpret.
func TestFailingAJobWithNoAttemptsLeftClampsRetriesAtZero(t *testing.T) {
	var (
		mu       sync.Mutex
		failBody map[string]any
	)
	stub := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/activate"):
			_, _ = w.Write([]byte(`{"jobs":[{"jobKey":7,"type":"send-email","retries":0,"leaseToken":42}]}`))
		case strings.HasSuffix(r.URL.Path, "/fail"):
			mu.Lock()
			defer mu.Unlock()
			_ = json.NewDecoder(r.Body).Decode(&failBody)
			_, _ = w.Write([]byte(`{}`))
		default:
			t.Errorf("unexpected call to %s", r.URL.Path)
		}
	}))
	defer stub.Close()

	w := worker.New(worker.Options{
		Server: stub.URL, ID: "mailer-1",
		Handlers: map[string]worker.Exec{
			"send-email": worker.ExecFunc(func(context.Context, worker.Job) (map[string]any, error) {
				return nil, errors.New("smtp: connection refused")
			}),
		},
	})
	// The report is made inside RunOnce, so it is recorded by the time this returns.
	if err := w.RunOnce(context.Background()); err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	mu.Lock()
	defer mu.Unlock()

	retries, ok := failBody["retries"].(float64)
	if !ok {
		t.Fatalf("the fail call carried no retries count: %v", failBody)
	}
	if retries != 0 {
		t.Errorf("retries = %v on a job with none left, want 0", retries)
	}
	if msg, _ := failBody["message"].(string); !strings.Contains(msg, "smtp") {
		t.Errorf("message = %q, want the handler's reason", msg)
	}
}

// A worker whose failure report is itself refused does not spin or crash: the lease
// elapses and the engine offers the job again, which is the at-least-once contract
// doing its job. Without this the worker would be trusting a call that can fail.
func TestAFailureReportThatIsRefusedIsNotFatal(t *testing.T) {
	var (
		mu       sync.Mutex
		reported bool
	)
	stub := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/activate") {
			_, _ = w.Write([]byte(`{"jobs":[{"jobKey":7,"type":"send-email","retries":3,"leaseToken":9}]}`))
			return
		}
		mu.Lock()
		reported = true
		mu.Unlock()
		// The lease moved on, so the report is fenced out — the 409 a real engine gives.
		w.WriteHeader(http.StatusConflict)
		_, _ = w.Write([]byte(`{"error":"the lease is held by another worker"}`))
	}))
	defer stub.Close()

	w := worker.New(worker.Options{
		Server: stub.URL, ID: "mailer-1",
		Handlers: map[string]worker.Exec{
			"send-email": worker.ExecFunc(func(context.Context, worker.Job) (map[string]any, error) {
				return nil, errors.New("smtp: connection refused")
			}),
		},
	})
	// The round completes rather than returning the refusal: a job the worker could
	// not report on is the engine's problem to time out, not a reason to stop working.
	if err := w.RunOnce(context.Background()); err != nil {
		t.Errorf("RunOnce = %v, want the refused report to be absorbed", err)
	}
	mu.Lock()
	defer mu.Unlock()
	if !reported {
		t.Error("the worker never tried to report the failure")
	}
}

// A command whose stdout is not a JSON object is a configuration mistake in the
// worker, and the message has to say which command and why — the operator wrote
// that command line, and "invalid character" alone would not tell them where.
func TestCmdExecRejectsOutputThatIsNotAJSONObject(t *testing.T) {
	if _, err := lookPath("sh"); err != nil {
		t.Skip("no sh on this machine")
	}
	e := worker.CmdExec{Name: "sh", Args: []string{"-c", `echo not-json`}}
	_, err := e.Run(context.Background(), worker.Job{JobKey: 1, Type: "t"})
	if err == nil {
		t.Fatal("output that is not JSON returned no error")
	}
	if !strings.Contains(err.Error(), "sh") || !strings.Contains(err.Error(), "variables") {
		t.Errorf("error = %v, want it to name the command and what it should have written", err)
	}
}

// A command that writes nothing completes the job with no variables, which is the
// ordinary shape of a worker that only has a side effect to perform.
func TestCmdExecTreatsNoOutputAsNoVariables(t *testing.T) {
	if _, err := lookPath("sh"); err != nil {
		t.Skip("no sh on this machine")
	}
	e := worker.CmdExec{Name: "sh", Args: []string{"-c", `exit 0`}}
	vars, err := e.Run(context.Background(), worker.Job{JobKey: 1, Type: "t"})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(vars) != 0 {
		t.Errorf("variables = %v, want none", vars)
	}
}

// A command that fails with nothing on stderr still fails the job, and the message
// falls back to the exit status rather than being empty.
func TestCmdExecSurfacesAFailureWithNoStderr(t *testing.T) {
	if _, err := lookPath("sh"); err != nil {
		t.Skip("no sh on this machine")
	}
	e := worker.CmdExec{Name: "sh", Args: []string{"-c", `exit 4`}}
	_, err := e.Run(context.Background(), worker.Job{JobKey: 1, Type: "t"})
	if err == nil {
		t.Fatal("a non-zero exit with no stderr returned no error")
	}
	if !strings.Contains(err.Error(), "sh") {
		t.Errorf("error = %v, want it to name the command", err)
	}
}

// The CSV handler run against a job with no resolved detail says what is actually
// wrong. This is what an operator sees after starting a worker with --connector csv
// against a server that is not offloading the kind: the pull is refused, but if the
// kind were served without the payload the message must point at the server's
// configuration rather than at the file.
func TestOffloadedCsvNeedsTheResolvedDetail(t *testing.T) {
	run := mustConnectors(t, "csv")[compiler.CsvImportJobType]
	_, err := run.Run(context.Background(), worker.Job{JobKey: 1, Type: compiler.CsvImportJobType})
	if err == nil {
		t.Fatal("a CSV job with no worker detail succeeded")
	}
	if !strings.Contains(err.Error(), "offloading") {
		t.Errorf("error = %v, want it to point at the server's offload configuration", err)
	}
}

// Detail the worker cannot read is reported as such, rather than being parsed as an
// empty job that would import zero rows and complete as if it had worked.
func TestOffloadedCsvRejectsDetailItCannotRead(t *testing.T) {
	run := mustConnectors(t, "csv")[compiler.CsvImportJobType]
	_, err := run.Run(context.Background(), worker.Job{
		JobKey: 1, Type: compiler.CsvImportJobType,
		Connector: &worker.ConnectorPayload{
			Kind:   "csv",
			Fields: map[string]any{"hasHeader": "not-a-bool"},
		},
	})
	if err == nil {
		t.Fatal("unreadable worker detail succeeded, want an error")
	}
	if !strings.Contains(err.Error(), "csv") {
		t.Errorf("error = %v, want it to name the kind", err)
	}
}

// A --server the worker cannot build a request from is reported, not panicked on.
// The operator wrote that flag, so the message has to be about the call being made.
func TestWorkerReportsAServerAddressItCannotUse(t *testing.T) {
	w := worker.New(worker.Options{
		Server: "://not-a-url", ID: "confused",
		Handlers: map[string]worker.Exec{
			"send-email": worker.ExecFunc(func(context.Context, worker.Job) (map[string]any, error) {
				return nil, nil
			}),
		},
	})
	if err := w.RunOnce(context.Background()); err == nil {
		t.Error("RunOnce against an unusable server address succeeded, want an error")
	}
}

// mustConnectors builds the built-in handlers for kinds that need no configuration.
func mustConnectors(t *testing.T, kinds ...string) map[string]worker.Exec {
	t.Helper()
	execs, err := worker.BuiltinConnectors(nil, kinds...)
	if err != nil {
		t.Fatalf("BuiltinConnectors(%v): %v", kinds, err)
	}
	return execs.Handlers
}

// TestWorkerRunsAnOffloadedADConnectorInMockMode is the whole ask in one test: the
// Active Directory worker running as a worker, and that worker serving it without
// a domain controller.
//
// The engine resolved the task — the DN out of a FEEL expression, the entry out of a
// process variable — and offloaded the kind, so nothing about AD ran on the run loop.
// The worker performed the create against a directory in its own memory and completed
// the job, which is how an identity model is exercised end to end before anybody is
// allowed near a real forest.
func TestWorkerRunsAnOffloadedADConnectorInMockMode(t *testing.T) {
	ts := liveAtlasWith(t, adConnectorModel,
		`{"userDN":"cn=Arno,ou=users,dc=example,dc=com","account":{"sAMAccountName":"arno","cn":"Arno"}}`,
		api.WithOffloadedConnectorKinds([]string{"ad"}))

	execs, err := worker.BuiltinConnectors(mockADEnv, "ad")
	if err != nil {
		t.Fatalf("BuiltinConnectors: %v", err)
	}
	w := worker.New(worker.Options{Server: ts.URL, ID: "ad-1", Handlers: execs.Handlers})
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	if err := w.RunOnce(ctx); err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	if running := runningInstances(t, ts); running != 0 {
		t.Errorf("%d instances still running, want 0 — the AD job was not completed", running)
	}
}

// mockADEnv is a worker environment with mock mode on and nothing else set: an AD
// worker needs no configuration beyond it.
func mockADEnv(name string) string {
	if name == "ATLAS_AD_MOCK" {
		return "1"
	}
	return ""
}

const adConnectorModel = `<?xml version="1.0" encoding="UTF-8"?>
<bpmn:definitions xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL"
                  xmlns:atlas="http://atlas.dev/schema/1.0" id="defs">
  <bpmn:process id="joiner" isExecutable="true">
    <bpmn:startEvent id="s"/>
    <bpmn:serviceTask id="t">
      <bpmn:extensionElements>
        <atlas:adConnector url="ldaps://dc.example.com:636" operation="create-user"
                           dn="=userDN" entryVariable="account"/>
      </bpmn:extensionElements>
    </bpmn:serviceTask>
    <bpmn:endEvent id="e"/>
    <bpmn:sequenceFlow id="f1" sourceRef="s" targetRef="t"/>
    <bpmn:sequenceFlow id="f2" sourceRef="t" targetRef="e"/>
  </bpmn:process>
</bpmn:definitions>`
