package conformance

import (
	"bytes"
	"fmt"
	"path/filepath"
	"reflect"
	"sort"
	"strings"

	"github.com/pblumer/atlas/compiler"
	"github.com/pblumer/atlas/engine"
	"github.com/pblumer/atlas/model"
	"github.com/pblumer/atlas/state"
	"github.com/pblumer/atlas/wal"
)

// defKey is the process-definition key every scenario compiles under. Any fixed
// value works; the first instance on partition 1 is then deterministically
// model.NewKey(1, 1).
const defKey uint64 = 7

// driverClock is a deterministic clock: each read advances by one for stable
// ordering, and advance jumps it forward so a Wait step can push a timer past its
// due date. Runs must not depend on wall-clock time, and replayed facts carry
// their own frozen timestamps (invariant I6), so the clock only orders live work.
type driverClock struct{ t int64 }

func (c *driverClock) Now() int64 { c.t++; return c.t }

func (c *driverClock) advance(ns int64) { c.t += ns }

// RunResult is a scenario's observable behavior: the terminal instance state, the
// ordered token path (BPMN element ids a token activated), and the final
// root-scope variables. It is the unit both the golden and replay oracles compare.
type RunResult struct {
	State     model.ProcessInstanceState
	Path      []string
	Variables map[string]string
}

// Run compiles the model, executes it live while applying the driver steps,
// replays its log into a fresh store, and returns the live result. It fails if
// replay diverges from live (invariant I4) or if a completed instance left orphan
// tokens (a structural invariant). Pass nil steps for a self-completing model.
// base must be an empty directory unique to this call.
func Run(base string, modelXML []byte, steps []Step) (RunResult, error) {
	cp, err := compiler.Parse(defKey, 1, bytes.NewReader(modelXML))
	if err != nil {
		return RunResult{}, fmt.Errorf("compile: %w", err)
	}

	liveDir := filepath.Join(base, "live")
	live, err := executeLive(liveDir, cp, steps)
	if err != nil {
		return RunResult{}, err
	}

	replay, err := replayLog(liveDir, filepath.Join(base, "replay"), cp)
	if err != nil {
		return RunResult{}, fmt.Errorf("replay: %w", err)
	}
	if !equalResult(live, replay) {
		return RunResult{}, fmt.Errorf("replay diverged from live (invariant I4):\n live   = %s\n replay = %s",
			strings.ReplaceAll(live.Golden(), "\n", " | "),
			strings.ReplaceAll(replay.Golden(), "\n", " | "))
	}
	return live, nil
}

// executeLive builds a fresh engine, runs the instance to idle, applies each
// driver step (with a RunUntilIdle after it), checks the no-orphan-tokens
// invariant, and captures the result. It closes its store and log so the log can
// be reopened for replay.
func executeLive(dir string, cp *compiler.CompiledProcess, steps []Step) (RunResult, error) {
	log, store, err := openStores(dir)
	if err != nil {
		return RunResult{}, err
	}
	defer closeStores(log, store)

	clock := &driverClock{}
	p := engine.New(1, log, store, clock)
	p.Deploy(cp)
	if err := p.Recover(); err != nil {
		return RunResult{}, fmt.Errorf("recover: %w", err)
	}
	p.CreateInstance(cp.Key)
	if err := p.RunUntilIdle(); err != nil {
		return RunResult{}, fmt.Errorf("run: %w", err)
	}

	d := &driver{p: p, store: store, cp: cp, clock: clock}
	for i, step := range steps {
		if err := d.apply(step); err != nil {
			return RunResult{}, fmt.Errorf("driver step %d of [%s]: %q: %w", i+1, stepList(steps), step.describe(), err)
		}
	}

	res, err := capture(store, cp)
	if err != nil {
		return RunResult{}, err
	}
	// Structural invariant: once an instance has ended, no token may still be
	// active anywhere in it.
	if res.State == model.PICompleted || res.State == model.PITerminated {
		pi, ei, err := activeCounts(store)
		if err != nil {
			return RunResult{}, err
		}
		if pi != 0 || ei != 0 {
			return RunResult{}, fmt.Errorf("orphan tokens after instance ended: active process=%d element=%d, want 0 and 0", pi, ei)
		}
	}
	return res, nil
}

// replayLog reopens the live log against a brand-new store and rebuilds state by
// recovery alone — the "state after replay" side of invariant I4.
func replayLog(liveDir, replayDir string, cp *compiler.CompiledProcess) (RunResult, error) {
	log, err := wal.Open(wal.Options{Dir: filepath.Join(liveDir, "wal")})
	if err != nil {
		return RunResult{}, fmt.Errorf("reopen wal: %w", err)
	}
	store, err := state.Open(filepath.Join(replayDir, "state"))
	if err != nil {
		_ = log.Close()
		return RunResult{}, fmt.Errorf("open replay store: %w", err)
	}
	defer closeStores(log, store)

	p := engine.New(1, log, store, &driverClock{})
	p.Deploy(cp)
	if err := p.Recover(); err != nil {
		return RunResult{}, fmt.Errorf("recover: %w", err)
	}
	return capture(store, cp)
}

// capture reads the first instance's observable behavior out of a store.
func capture(store *state.Store, cp *compiler.CompiledProcess) (RunResult, error) {
	piKey := model.NewKey(1, 1)

	var path []string
	if err := store.ElementStepHistory(piKey, func(_ int64, _ uint64, elementId int32) error {
		path = append(path, cp.ElementBpmnId(elementId))
		return nil
	}); err != nil {
		return RunResult{}, fmt.Errorf("element steps: %w", err)
	}

	vars := map[string]string{}
	if err := store.VariablesOfScope(piKey, func(v *model.VariableValue) error {
		vars[v.Name] = v.Text
		return nil
	}); err != nil {
		return RunResult{}, fmt.Errorf("variables: %w", err)
	}

	// PIActive unless the instance appears in the completed-history index.
	instanceState := model.PIActive
	if err := store.CompletedProcessInstances(func(k uint64, v *model.ProcessInstanceValue) error {
		if k == piKey {
			instanceState = v.State
		}
		return nil
	}); err != nil {
		return RunResult{}, fmt.Errorf("completed instances: %w", err)
	}

	return RunResult{State: instanceState, Path: path, Variables: vars}, nil
}

func activeCounts(store *state.Store) (procInstances, elementInstances int, err error) {
	if procInstances, err = store.ActiveProcessInstanceCount(); err != nil {
		return 0, 0, fmt.Errorf("active process count: %w", err)
	}
	if elementInstances, err = store.ActiveElementInstanceCount(); err != nil {
		return 0, 0, fmt.Errorf("active element count: %w", err)
	}
	return procInstances, elementInstances, nil
}

func openStores(dir string) (*wal.Log, *state.Store, error) {
	log, err := wal.Open(wal.Options{Dir: filepath.Join(dir, "wal")})
	if err != nil {
		return nil, nil, fmt.Errorf("open wal: %w", err)
	}
	store, err := state.Open(filepath.Join(dir, "state"))
	if err != nil {
		_ = log.Close()
		return nil, nil, fmt.Errorf("open state: %w", err)
	}
	return log, store, nil
}

func closeStores(log *wal.Log, store *state.Store) {
	_ = store.Close()
	_ = log.Close()
}

func equalResult(a, b RunResult) bool {
	return a.State == b.State &&
		reflect.DeepEqual(a.Path, b.Path) &&
		reflect.DeepEqual(a.Variables, b.Variables)
}

// Golden renders the result as the canonical, human-readable golden-file text.
func (r RunResult) Golden() string {
	var b strings.Builder
	fmt.Fprintf(&b, "state: %s\n", r.State)
	if len(r.Path) == 0 {
		b.WriteString("path: (none)\n")
	} else {
		fmt.Fprintf(&b, "path: %s\n", strings.Join(r.Path, " -> "))
	}
	if len(r.Variables) == 0 {
		b.WriteString("vars: (none)\n")
		return b.String()
	}
	b.WriteString("vars:\n")
	for _, name := range sortedKeys(r.Variables) {
		fmt.Fprintf(&b, "  %s=%s\n", name, r.Variables[name])
	}
	return b.String()
}

// Effect is the metamorphic projection: terminal state plus final variables,
// with the control-flow path deliberately dropped. Behaviorally equivalent models
// differ in path but must agree here.
func (r RunResult) Effect() string {
	var b strings.Builder
	fmt.Fprintf(&b, "state=%s", r.State)
	for _, name := range sortedKeys(r.Variables) {
		fmt.Fprintf(&b, " %s=%s", name, r.Variables[name])
	}
	return b.String()
}

func sortedKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
