// Package worker is Atlas's out-of-process job worker: the binary, run as a client
// of its own HTTP API, leasing jobs of named types and reporting what happened
// (ADR-0157 step 5, over the protocol of ADR-0007).
//
// It exists so that a service task's work does not have to run on the engine's
// single writer. An in-process connector's outbound call is charged to the whole
// engine for its duration; a job this worker takes is charged to the worker.
//
// **What it does not do, and why.** ADR-0157 says the standard worker is "the same
// binary running the *existing* handler code". That turned out not to be reachable
// for the connector kinds: every one of them resolves its configuration from the
// compiled process through a ProcessLookup over the local state store
// (`mail.Handler(store, lookup, registry)` and its siblings), which a process
// outside the engine has neither of. Relocating those needs the connector detail to
// travel with the job — its own slice. What this worker serves instead is the kind
// that has always been meant for an external worker: a **model-authored** job type,
// a `<zeebe:taskDefinition type>` whose work is the customer's own code.
//
// The contract for that code is the one the script connector already established:
// the job arrives as JSON on stdin, and whatever JSON object the command writes to
// stdout becomes the variables the job completes with. A non-zero exit fails the
// job, carrying the command's stderr as the message an operator reads on the
// incident.
package worker

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os/exec"
	"sort"
	"strconv"
	"sync"
	"time"

	"github.com/pblumer/atlas/logging"
)

// Job is one leased job as the engine hands it over.
type Job struct {
	JobKey             uint64         `json:"jobKey"`
	Type               string         `json:"type"`
	ProcessInstanceKey uint64         `json:"processInstanceKey"`
	ElementInstanceKey uint64         `json:"elementInstanceKey"`
	ProcessDefKey      uint64         `json:"processDefKey"`
	ElementID          string         `json:"elementId"`
	Retries            int32          `json:"retries"`
	LeaseToken         uint64         `json:"leaseToken"`
	Variables          map[string]any `json:"variables"`
}

// Exec does one job's work and returns the variables it completed with, or an error
// that fails the job.
type Exec interface {
	Run(ctx context.Context, j Job) (map[string]any, error)
}

// ExecFunc adapts a function to [Exec].
type ExecFunc func(ctx context.Context, j Job) (map[string]any, error)

// Run implements [Exec].
func (f ExecFunc) Run(ctx context.Context, j Job) (map[string]any, error) { return f(ctx, j) }

// Options configures a [Worker].
type Options struct {
	// Server is the base URL of the Atlas to work for, e.g. http://localhost:8080.
	Server string
	// ID names this worker. It rides on every lease and every report, and it is what
	// the Workers view shows — so give each deployment its own.
	ID string
	// Token, when set, is sent as a bearer credential on every request.
	Token string
	// Handlers is what this worker serves, keyed by job type. A worker with none is a
	// misconfiguration rather than a process that polls forever over nothing.
	Handlers map[string]Exec
	// Lease is how long the engine holds a job for this worker. It must comfortably
	// exceed how long the work takes: an elapsed lease puts the job back on offer, and
	// the report that finally arrives is fenced out.
	Lease time.Duration
	// Wait is how long a poll may block waiting for work before coming back empty.
	Wait time.Duration
	// Retry is how long to wait after a failed poll before trying again. A worker
	// must not exit because the server was restarting, or because the process using
	// its job type is not deployed yet — both are ordinary and both resolve.
	Retry time.Duration
	// MaxJobs is how many jobs one poll may lease. Keep it to what this worker can
	// actually run: leased work nobody is running is work nobody else can take either.
	MaxJobs int
	// HTTP is the client to use; nil means a default one bounded by Lease + Wait.
	HTTP *http.Client
}

// Defaults, chosen so an unconfigured worker is safe rather than surprising: it
// takes one job at a time, holds it for five minutes, and parks for thirty seconds
// per poll.
const (
	DefaultLease   = 5 * time.Minute
	DefaultWait    = 30 * time.Second
	DefaultMaxJobs = 1
	DefaultRetry   = 5 * time.Second
)

// Worker leases jobs of the types it handles and reports what happened. Build one
// with [New].
type Worker struct {
	opts  Options
	http  *http.Client
	types []string
}

// New builds a worker, filling in defaults.
func New(opts Options) *Worker {
	if opts.Lease <= 0 {
		opts.Lease = DefaultLease
	}
	if opts.Wait < 0 {
		opts.Wait = 0
	}
	if opts.Wait == 0 {
		opts.Wait = DefaultWait
	}
	if opts.MaxJobs <= 0 {
		opts.MaxJobs = DefaultMaxJobs
	}
	if opts.Retry <= 0 {
		opts.Retry = DefaultRetry
	}
	w := &Worker{opts: opts, http: opts.HTTP}
	if w.http == nil {
		// One request may block for a whole poll and is followed by the work itself, so
		// the client's ceiling is the poll plus the lease rather than a short timeout.
		w.http = &http.Client{Timeout: opts.Wait + opts.Lease + 30*time.Second}
	}
	for t := range opts.Handlers {
		w.types = append(w.types, t)
	}
	sort.Strings(w.types) // stable order, so a run is reproducible
	return w
}

// Run works until ctx is cancelled. Each type is polled by its own goroutine, so a
// slow queue never starves another.
func (w *Worker) Run(ctx context.Context) error {
	if len(w.types) == 0 {
		return errors.New("worker: no job types to handle; give it at least one --handle type=command")
	}
	var wg sync.WaitGroup
	for _, jobType := range w.types {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for ctx.Err() == nil {
				err := w.pollOnce(ctx, jobType)
				if err == nil || ctx.Err() != nil {
					continue
				}
				// A failed poll is not a reason to exit. The server may be restarting,
				// or the process that uses this job type may not be deployed yet — the
				// engine answers 404 for a type it has never seen, which is the honest
				// answer and what makes a typo visible, and both resolve on their own.
				// So it is logged and retried; only cancellation ends the loop. A
				// supervised worker that exited here would flap against its supervisor
				// for as long as the deploy took.
				logging.Warn(logging.WorkerPollFailed, "a job poll failed; retrying",
					slog.String("worker", w.opts.ID), slog.String("type", jobType),
					slog.String("error", err.Error()))
				select {
				case <-ctx.Done():
				case <-time.After(w.opts.Retry):
				}
			}
		}()
	}
	wg.Wait()
	return nil
}

// RunOnce polls every handled type once and works whatever it is given. It is what
// a test drives, and what a one-shot invocation ("drain the queue and exit") wants.
func (w *Worker) RunOnce(ctx context.Context) error {
	if len(w.types) == 0 {
		return errors.New("worker: no job types to handle; give it at least one --handle type=command")
	}
	for _, jobType := range w.types {
		if err := w.pollOnce(ctx, jobType); err != nil {
			return err
		}
	}
	return nil
}

// pollOnce leases up to MaxJobs of one type and works each of them.
func (w *Worker) pollOnce(ctx context.Context, jobType string) error {
	jobs, err := w.activate(ctx, jobType)
	if err != nil {
		return err
	}
	for _, j := range jobs {
		w.work(ctx, j)
	}
	return nil
}

// work runs one job's handler and reports the outcome. A failure is reported to the
// engine rather than returned: the retry and incident machinery is what an operator
// sees, and a worker that swallowed the error would park the token silently.
func (w *Worker) work(ctx context.Context, j Job) {
	vars, err := w.opts.Handlers[j.Type].Run(ctx, j)
	if err != nil {
		if failErr := w.fail(ctx, j, err.Error()); failErr != nil && ctx.Err() == nil {
			// Nothing else to do: the lease will elapse and the job is offered again.
			return
		}
		return
	}
	_ = w.complete(ctx, j, vars)
}

func (w *Worker) activate(ctx context.Context, jobType string) ([]Job, error) {
	body := map[string]any{
		"type": jobType, "worker": w.opts.ID,
		"leaseMs": w.opts.Lease.Milliseconds(), "waitMs": w.opts.Wait.Milliseconds(),
		"maxJobs": w.opts.MaxJobs,
	}
	var out struct {
		Jobs []Job `json:"jobs"`
	}
	if err := w.call(ctx, "/api/v1/jobs/activate", body, &out); err != nil {
		return nil, err
	}
	return out.Jobs, nil
}

func (w *Worker) complete(ctx context.Context, j Job, vars map[string]any) error {
	body := map[string]any{"worker": w.opts.ID, "leaseToken": j.LeaseToken}
	if len(vars) > 0 {
		body["variables"] = vars
	}
	return w.call(ctx, "/api/v1/jobs/"+strconv.FormatUint(j.JobKey, 10)+"/complete", body, nil)
}

func (w *Worker) fail(ctx context.Context, j Job, message string) error {
	// One attempt is spent by this failure; at zero the engine raises an incident.
	retries := j.Retries - 1
	if retries < 0 {
		retries = 0
	}
	body := map[string]any{
		"worker": w.opts.ID, "leaseToken": j.LeaseToken,
		"retries": retries, "message": message,
	}
	return w.call(ctx, "/api/v1/jobs/"+strconv.FormatUint(j.JobKey, 10)+"/fail", body, nil)
}

// call posts a JSON body and decodes a JSON reply into out (nil to discard it).
func (w *Worker) call(ctx context.Context, path string, body, out any) error {
	payload, err := json.Marshal(body)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, w.opts.Server+path, bytes.NewReader(payload))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	if w.opts.Token != "" {
		req.Header.Set("Authorization", "Bearer "+w.opts.Token)
	}
	resp, err := w.http.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		var msg struct {
			Error string `json:"error"`
		}
		_ = json.NewDecoder(resp.Body).Decode(&msg)
		if msg.Error != "" {
			return fmt.Errorf("worker: %s: %s", path, msg.Error)
		}
		return fmt.Errorf("worker: %s: status %d", path, resp.StatusCode)
	}
	if out == nil {
		return nil
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

// CmdExec runs an operating-system command per job: the job arrives as JSON on
// stdin, and the JSON object the command writes to stdout becomes the variables the
// job completes with (no output completes with none). A non-zero exit fails the job
// and carries the command's stderr, which is what an operator reads on the incident.
type CmdExec struct {
	Name string
	Args []string
	// Env is added to the process's own environment.
	Env []string
}

// Run implements [Exec].
func (c CmdExec) Run(ctx context.Context, j Job) (map[string]any, error) {
	payload, err := json.Marshal(j)
	if err != nil {
		return nil, err
	}
	cmd := exec.CommandContext(ctx, c.Name, c.Args...)
	cmd.Stdin = bytes.NewReader(payload)
	cmd.Env = append(cmd.Environ(), c.Env...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		if msg := bytes.TrimSpace(stderr.Bytes()); len(msg) > 0 {
			return nil, fmt.Errorf("%s: %w: %s", c.Name, err, msg)
		}
		return nil, fmt.Errorf("%s: %w", c.Name, err)
	}
	trimmed := bytes.TrimSpace(stdout.Bytes())
	if len(trimmed) == 0 {
		return nil, nil
	}
	var vars map[string]any
	dec := json.NewDecoder(bytes.NewReader(trimmed))
	dec.UseNumber()
	if err := dec.Decode(&vars); err != nil {
		return nil, fmt.Errorf("%s: its output is not a JSON object of variables: %w", c.Name, err)
	}
	return vars, nil
}
