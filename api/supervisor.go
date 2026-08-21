package api

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"slices"
	"sync"
	"time"

	"github.com/pblumer/atlas/logging"
)

// Worker supervision (ADR-0157 step 7): Atlas launching and restarting the workers
// that do its side-effecting work, so a single-node installation gets them without
// assembling a distributed system by hand.
//
// It comes last in that ADR's sequence for a reason. A restart makes double
// execution likelier, so fencing had to land first (it has: a lease token the
// worker presents). And a button on something nobody can see the state of is worse
// than no button, so the Workers view had to land first too.
//
// Two rules shape this file.
//
// **Supervision is a convenience, never an execution mode.** The child is the same
// `atlas worker` an operator could run themselves, speaking the same HTTP protocol.
// If it were a private path, the supervised one would become the tested one and the
// external one would rot — which is exactly how out-of-process work got to be
// second-class in the first place.
//
// **Nothing from a request ever becomes a command.** The supervisor spawns
// [os.Executable] and only that, with an argv built from typed configuration read
// off the server's own command line. A supervisor that ran operator-supplied
// command lines from the API would be a remote-code-execution endpoint wearing a
// feature's clothes, and no amount of authorization makes that a good trade in an
// engine that already executes model-authored steps. The `--supervise` flag is the
// operator's own shell, at the same trust level as running the worker themselves;
// the API can restart a configured worker, and can do nothing else to it.

// Restart policy. A child that cannot start must not spin the machine, and one that
// has been up must not be punished for a single blip.
const (
	defaultRestartBackoff = 500 * time.Millisecond
	maxRestartBackoff     = 30 * time.Second
	// childLogLines is how much of a child's output is kept for the console. The
	// tail is what an operator wants, and an unbounded log is how a chatty worker
	// fills the server's memory.
	childLogLines = 200
)

// SuperviseSpec is one supervised worker as configured: what to call it and which
// job types it serves. Both come from the server's command line.
type SuperviseSpec struct {
	ID    string
	Kinds []string
	// Connectors are built-in connector kinds this worker serves (--connector on the
	// child). A worker may serve these, model-authored job types, or both.
	Connectors []string
}

// childStatus is one supervised worker as the console sees it.
type childStatus struct {
	ID       string   `json:"id"`
	Kinds    []string `json:"kinds"`
	State    string   `json:"state"`
	PID      int      `json:"pid"`
	Starts   int      `json:"starts"`
	LastExit string   `json:"lastExit,omitempty"`
	Since    int64    `json:"since"`
	Log      []string `json:"log,omitempty"`
}

// child is one supervised worker's live state.
type child struct {
	spec SuperviseSpec
	args []string
	// env adds to the environment the child inherits, evaluated at every spawn so a
	// connector added in the Console is picked up by pressing Restart rather than by
	// restarting Atlas. nil means the child inherits this process's environment
	// unchanged, which is every worker an operator configured by hand.
	env func() []string

	mu       sync.Mutex
	state    string
	pid      int
	starts   int
	lastExit string
	since    int64
	log      *logRing
	cmd      *exec.Cmd
	// lastEnv is the extra environment the running process was spawned with, so a
	// configuration change can be noticed as a difference rather than guessed at.
	lastEnv []string
	// cycle is closed and replaced to ask the run loop to stop the current process
	// and start a fresh one.
	stop chan struct{}
}

// supervisor owns every supervised worker.
type supervisor struct {
	// exe is the program to run. Production always uses os.Executable — the binary
	// supervising is the binary supervised — and only a test points it elsewhere.
	exe     string
	backoff time.Duration
	quit    <-chan struct{}

	mu       sync.Mutex
	children []*child
	wg       sync.WaitGroup
}

// newSupervisor builds an empty supervisor that runs until quit is closed.
func newSupervisor(quit <-chan struct{}) *supervisor {
	exe, err := os.Executable()
	if err != nil {
		exe = "atlas"
	}
	return &supervisor{exe: exe, backoff: defaultRestartBackoff, quit: quit}
}

// add registers a worker to supervise. args is the argv tail the server built; it
// never contains anything a request supplied. env, when non-nil, is the extra
// environment this worker is spawned with — it is where a credential goes, because
// argv is readable by anyone who can list processes and an environment is not.
func (s *supervisor) add(spec SuperviseSpec, args []string, env func() []string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.children = append(s.children, &child{
		spec: spec, args: args, env: env, state: "starting",
		log: newLogRing(childLogLines), stop: make(chan struct{}),
	})
}

// count is how many workers are supervised.
func (s *supervisor) count() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.children)
}

// start runs every registered worker, each in its own goroutine.
func (s *supervisor) start() {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, c := range s.children {
		s.wg.Add(1)
		go func() {
			defer s.wg.Done()
			s.supervise(c)
		}()
	}
}

// wait blocks until every supervised worker has stopped.
func (s *supervisor) wait() { s.wg.Wait() }

// supervise keeps one worker running until the server quits: start it, wait for it,
// and start it again after a backoff that grows while it keeps failing.
func (s *supervisor) supervise(c *child) {
	failures := 0
	for {
		select {
		case <-s.quit:
			c.set(func() { c.state = "stopped"; c.pid = 0 })
			return
		default:
		}

		stop := c.beginCycle()
		err := s.runOnce(c, stop)
		switch {
		case err == nil:
			failures = 0 // it ran and was asked to stop; not a failure
		case errors.Is(err, idleNothingToServe):
			// Park. Starting it again would only re-run it into the same emptiness,
			// so it waits — for the restart button, or for refresh to notice that its
			// configuration changed, which is what happens the moment an operator
			// configures the kind it was started for.
			select {
			case <-s.quit:
				c.set(func() { c.state = "stopped"; c.pid = 0 })
				return
			case <-c.currentStop():
			}
			failures = 0
			continue
		default:
			failures++
		}

		// Wait before trying again — but a restart asked for during the wait cuts it
		// short. An operator pressing the button has just been told the worker is
		// failing, and "try again now" is what they mean; making them wait out a
		// thirty-second cap would make the button look broken.
		select {
		case <-s.quit:
			c.set(func() { c.state = "stopped"; c.pid = 0 })
			return
		case <-c.currentStop():
		case <-time.After(restartDelay(s.backoff, maxRestartBackoff, failures)):
		}
	}
}

// runOnce starts the child and waits for it to exit, for the server to quit, or for
// a restart to be asked for. It returns nil when the exit was expected.
func (s *supervisor) runOnce(c *child, stop <-chan struct{}) error {
	cmd := exec.Command(s.exe, c.args...)
	// The child inherits this process's environment and is handed the configuration
	// for the connector kinds it serves on top of it. Reading it here rather than at
	// registration is what makes the console's Restart button pick up a connector
	// added since the server started.
	var extra []string
	if c.env != nil {
		if extra = c.env(); len(extra) > 0 {
			cmd.Env = append(os.Environ(), extra...)
		}
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		c.set(func() { c.state = "failed"; c.lastExit = err.Error() })
		return err
	}
	cmd.Stderr = cmd.Stdout
	if err := cmd.Start(); err != nil {
		c.set(func() { c.state = "failed"; c.lastExit = err.Error(); c.starts++ })
		logging.Warn(logging.WorkerSupervisorFailed, "could not start a supervised worker",
			slog.String("id", c.spec.ID), slog.String("error", err.Error()))
		return err
	}
	c.set(func() {
		c.state = "running"
		c.pid = cmd.Process.Pid
		c.starts++
		c.since = time.Now().UnixNano()
		c.cmd = cmd
		c.lastEnv = extra
	})
	logging.Info(logging.WorkerSupervisorStarted, "started a supervised worker",
		slog.String("id", c.spec.ID), slog.Int("pid", cmd.Process.Pid))

	go c.drain(stdout)

	exited := make(chan error, 1)
	go func() { exited <- cmd.Wait() }()

	select {
	case err := <-exited:
		return c.finish(err, false)
	case <-stop:
		kill(cmd)
		<-exited
		return c.finish(nil, true)
	case <-s.quit:
		kill(cmd)
		<-exited
		return c.finish(nil, true)
	}
}

// exitNothingToServe is the status `atlas worker` leaves when it holds no handler at
// all — a mail worker on a server with no mail connector configured yet. It is a
// state, not a crash, and the distinction is the whole reason the worker spends an
// exit code on it: restarting such a worker only re-runs it into the same emptiness,
// which is a backoff loop that never converges and a console full of red.
const exitNothingToServe = ExitNothingToServe

// ExitNothingToServe is that status, exported so `atlas worker` leaves exactly the
// one its supervisor parks on. 78 is sysexits.h's EX_CONFIG — "something was found
// in an unconfigured or misconfigured state" — which is the condition, and it is far
// from the statuses a panic or a signal produces.
const ExitNothingToServe = 78

// idleNothingToServe is what finish returns for that status, so supervise can park
// the child instead of scheduling another start.
var idleNothingToServe = errors.New("nothing to serve; waiting for a configuration change")

// finish records how the child ended. An expected end (shutdown or a requested
// restart) is not a failure and must not feed the backoff; neither is a worker that
// has nothing to serve.
func (c *child) finish(err error, expected bool) error {
	nothingToServe := !expected && exitCode(err) == exitNothingToServe
	c.set(func() {
		c.pid = 0
		c.cmd = nil
		switch {
		case expected:
			c.state = "restarting"
			c.lastExit = ""
		case nothingToServe:
			// Deliberately not "failed": nothing is wrong, there is just nothing here
			// to do yet. The worker's own log line says which kind, and the Workers
			// view's "connectors nothing can serve" card says which names are waiting.
			c.state = "idle"
			c.lastExit = idleNothingToServe.Error()
		case err != nil:
			c.state = "failed"
			c.lastExit = err.Error()
		default:
			c.state = "failed"
			c.lastExit = "exited without an error, which a worker should not do"
		}
	})
	switch {
	case expected:
		return nil
	case nothingToServe:
		return idleNothingToServe
	case err == nil:
		return errors.New("worker exited")
	}
	return err
}

// exitCode is the status a finished command left, or -1 when it did not run or was
// ended by a signal.
func exitCode(err error) int {
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		return ee.ExitCode()
	}
	return -1
}

// kill ends the child. It is a plain kill rather than a signal-and-wait dance
// because `atlas worker` finishes the job in flight on interrupt; the supervisor
// asking for a restart has already decided not to wait for that.
func kill(cmd *exec.Cmd) {
	if cmd.Process != nil {
		_ = cmd.Process.Kill()
	}
}

// drain copies the child's output into its bounded log.
func (c *child) drain(r io.ReadCloser) {
	defer func() { _ = r.Close() }()
	sc := bufio.NewScanner(r)
	for sc.Scan() {
		c.set(func() { c.log.add(sc.Text()) })
	}
}

// beginCycle hands out the channel this run watches for a restart request. It does
// NOT replace it: replacing here is what would leave the running child watching a
// channel nobody can close, since restart closes whatever is current. Installing
// the fresh channel is restart's job, and the next cycle picks it up.
func (c *child) beginCycle() <-chan struct{} {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.stop
}

// currentStop is the restart channel to watch right now. Read under the lock
// because restart replaces it.
func (c *child) currentStop() <-chan struct{} {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.stop
}

func (c *child) set(fn func()) {
	c.mu.Lock()
	defer c.mu.Unlock()
	fn()
}

// restart asks a supervised worker to cycle. The supervisor's own loop brings it
// back, so the console's button and an ordinary crash take the identical path —
// there is no second way for a worker to come up that could drift from the first.
func (s *supervisor) restart(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, c := range s.children {
		if c.spec.ID != id {
			continue
		}
		c.mu.Lock()
		close(c.stop)
		c.stop = make(chan struct{})
		c.mu.Unlock()
		return nil
	}
	return fmt.Errorf("no supervised worker %q", id)
}

// refresh restarts every supervised worker whose configuration has changed since it
// was spawned. An operator who adds a mail connector in the Console means for it to
// work, and the worker holding the old set would leave them pressing Restart to find
// out why it does not.
//
// It restarts on a *difference*, not on the fact that something was saved: editing a
// clio connector must not cycle the mail worker, and neither must saving a mail
// connector without changing anything the worker holds. Nothing is restarted before
// its first start, so this cannot fight the initial launch.
//
// It runs OFF the run loop, because reading a worker's configuration goes onto it.
func (s *supervisor) refresh() {
	s.mu.Lock()
	children := append([]*child(nil), s.children...)
	s.mu.Unlock()
	for _, c := range children {
		if c.env == nil {
			continue
		}
		want := c.env()
		c.mu.Lock()
		stale := c.starts > 0 && !slices.Equal(want, c.lastEnv)
		c.mu.Unlock()
		if stale {
			_ = s.restart(c.spec.ID)
		}
	}
}

// list reports every supervised worker.
func (s *supervisor) list() []childStatus {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]childStatus, 0, len(s.children))
	for _, c := range s.children {
		c.mu.Lock()
		out = append(out, childStatus{
			ID: c.spec.ID, Kinds: c.spec.Kinds, State: c.state, PID: c.pid,
			Starts: c.starts, LastExit: c.lastExit, Since: c.since, Log: c.log.lines(),
		})
		c.mu.Unlock()
	}
	return out
}

// restartDelay is how long to wait before starting a child again: nothing before a
// first start, then a doubling backoff capped so a child that cannot start never
// spins the machine.
func restartDelay(base, max time.Duration, failures int) time.Duration {
	if failures <= 0 {
		return base
	}
	d := base
	for range failures - 1 {
		d *= 2
		if d >= max {
			return max
		}
	}
	if d > max {
		return max
	}
	return d
}

// logRing keeps the last n lines of a child's output.
type logRing struct {
	n      int
	lines_ []string
}

func newLogRing(n int) *logRing { return &logRing{n: n} }

func (r *logRing) add(line string) {
	r.lines_ = append(r.lines_, line)
	if len(r.lines_) > r.n {
		r.lines_ = r.lines_[len(r.lines_)-r.n:]
	}
}

func (r *logRing) lines() []string {
	out := make([]string, len(r.lines_))
	copy(out, r.lines_)
	return out
}
