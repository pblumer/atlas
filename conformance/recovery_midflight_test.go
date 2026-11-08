package conformance

import (
	"bytes"
	"fmt"
	"math"
	"path/filepath"
	"reflect"
	"sort"
	"testing"

	"github.com/pblumer/atlas/compiler"
	"github.com/pblumer/atlas/engine"
	"github.com/pblumer/atlas/model"
	"github.com/pblumer/atlas/state"
	"github.com/pblumer/atlas/wal"
)

// con3WaitState captures state at a *waiting* checkpoint, not just a finished
// instance projection. It intentionally includes record keys, element/scope
// ownership, message subscription count and pending timer/job/incident indexes.
type con3WaitState struct {
	position      uint64
	processes     []string
	elements      []string
	jobs          []string
	timers        []string
	incidents     []string
	variables     []string
	subscriptions int64
}

func con3CaptureWait(s *state.Store) (con3WaitState, error) {
	var v con3WaitState
	var err error
	if v.position, err = s.LastAppliedPosition(); err != nil {
		return v, fmt.Errorf("last WAL position: %w", err)
	}
	scopes := make(map[uint64]struct{})
	if err = s.ActiveProcessInstances(func(key uint64, pi *model.ProcessInstanceValue) error {
		v.processes = append(v.processes, fmt.Sprintf("active:%d:%d:%d:%d", key, pi.ProcessDefKey, pi.State, pi.CreatedAt))
		scopes[key] = struct{}{}
		return nil
	}); err != nil {
		return v, fmt.Errorf("active processes: %w", err)
	}
	if err = s.CompletedProcessInstances(func(key uint64, pi *model.ProcessInstanceValue) error {
		v.processes = append(v.processes, fmt.Sprintf("complete:%d:%d:%d:%d", key, pi.ProcessDefKey, pi.State, pi.CompletedPosition))
		return nil
	}); err != nil {
		return v, fmt.Errorf("completed processes: %w", err)
	}
	if err = s.ActiveElementInstances(func(key uint64, ei *model.ElementInstanceValue) error {
		v.elements = append(v.elements, fmt.Sprintf("%d:%d:%d:%d:%d:%d:%d:%d:%d",
			key, ei.ProcessInstanceKey, ei.ElementId, ei.FlowScopeKey, ei.TokenID,
			ei.BpmnElementType, ei.EventGatewayKey, ei.MultiInstance, ei.SourceFlowId))
		if ei.FlowScopeKey != 0 {
			scopes[ei.FlowScopeKey] = struct{}{}
		}
		return nil
	}); err != nil {
		return v, fmt.Errorf("active elements: %w", err)
	}
	for scope := range scopes {
		if err = s.VariablesOfScope(scope, func(variable *model.VariableValue) error {
			v.variables = append(v.variables, fmt.Sprintf("%d:%s:%d:%s", scope, variable.Name, variable.Kind, variable.Text))
			return nil
		}); err != nil {
			return v, fmt.Errorf("scope variables: %w", err)
		}
	}
	if err = s.AllActivatableJobs(func(key uint64) error {
		job, ok, getErr := s.GetJob(key)
		if getErr != nil {
			return getErr
		}
		if !ok {
			return fmt.Errorf("activatable job %d has no state record", key)
		}
		v.jobs = append(v.jobs, fmt.Sprintf("%d:%d:%d:%d:%d", key,
			job.ProcessInstanceKey, job.ElementInstanceKey, job.JobType, job.Retries))
		return nil
	}); err != nil {
		return v, fmt.Errorf("pending jobs: %w", err)
	}
	if err = s.DueTimers(math.MaxInt64, func(key uint64, timer *model.TimerValue) error {
		v.timers = append(v.timers, fmt.Sprintf("instance:%d:%d:%d:%d:%d",
			key, timer.ProcessInstanceKey, timer.TargetElementId, timer.DueDate, timer.Repetitions))
		return nil
	}); err != nil {
		return v, fmt.Errorf("pending timers: %w", err)
	}
	if err = s.StartTimers(func(key uint64, timer *model.TimerValue) error {
		v.timers = append(v.timers, fmt.Sprintf("start:%d:%d:%d:%d",
			key, timer.ProcessDefKey, timer.DueDate, timer.Repetitions))
		return nil
	}); err != nil {
		return v, fmt.Errorf("start timers: %w", err)
	}
	if err = s.Incidents(func(key uint64, incident *model.IncidentValue) error {
		v.incidents = append(v.incidents, fmt.Sprintf("%d:%d:%d:%s",
			key, incident.ProcessInstanceKey, incident.ElementId, incident.Message))
		return nil
	}); err != nil {
		return v, fmt.Errorf("incidents: %w", err)
	}
	if v.subscriptions, err = s.MessageSubscriptions(); err != nil {
		return v, fmt.Errorf("message subscriptions: %w", err)
	}
	for _, values := range [][]string{v.processes, v.elements, v.jobs, v.timers, v.incidents, v.variables} {
		sort.Strings(values)
	}
	return v, nil
}

// TestCON3MidflightRecoveryAndContinuation checks that a fresh process recovered
// from WAL at a live wait has the same durable families and can still finish.
// It exercises the original persisted facts, not a clone of the in-memory state;
// the simulated restart does not depend on sleeps, wall clocks or goroutine order.
func TestCON3MidflightRecoveryAndContinuation(t *testing.T) {
	cases := []struct {
		name       string
		prefix     int
		checkpoint string
	}{
		{"service-task", 0, "job"},
		{"message-catch", 0, "message"},
		{"timer-catch", 0, "timer"},
		{"incident", 1, "incident"},
		{"event-gateway-message", 0, "race"},
		{"boundary-message-noninterrupting", 0, "job"},
	}
	registered := make(map[string]Scenario, len(Scenarios))
	for _, scenario := range Scenarios {
		registered[scenario.Name] = scenario
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			sc, ok := registered[tc.name]
			if !ok {
				t.Fatalf("missing scenario %s", tc.name)
			}
			data, err := sc.load()
			if err != nil {
				t.Fatal(err)
			}
			defs, err := compiler.ParseAll(defKey, 1, bytes.NewReader(data))
			if err != nil {
				t.Fatalf("compile: %v", err)
			}
			root, err := pickRoot(defs, sc.Root)
			if err != nil {
				t.Fatal(err)
			}

			liveDir := filepath.Join(t.TempDir(), "live")
			log, store, err := openStores(liveDir)
			if err != nil {
				t.Fatal(err)
			}
			clock := &driverClock{}
			p := engine.New(1, log, store, clock)
			deployAll(p, defs, root)
			if err := p.Recover(); err != nil {
				t.Fatalf("prime Recover: %v", err)
			}
			d := &driver{p: p, store: store, cp: root, clock: clock, deployables: defs}
			if err := d.begin(sc.Start); err != nil {
				t.Fatalf("initial run: %v", err)
			}
			for _, step := range sc.Driver[:tc.prefix] {
				if err := d.apply(step); err != nil {
					t.Fatalf("pre-checkpoint step: %v", err)
				}
			}
			waiting, err := con3CaptureWait(store)
			if err != nil {
				t.Fatal(err)
			}
			if waiting.position == 0 || len(waiting.processes) == 0 ||
				len(waiting.elements) == 0 {
				t.Fatalf("checkpoint is not an active durable instance: %+v", waiting)
			}
			switch tc.checkpoint {
			case "job":
				if len(waiting.jobs) == 0 {
					t.Fatal("no durable activatable job at checkpoint")
				}
			case "message":
				if waiting.subscriptions == 0 {
					t.Fatal("no durable message subscription at checkpoint")
				}
			case "timer":
				if len(waiting.timers) == 0 {
					t.Fatal("no durable timer at checkpoint")
				}
			case "incident":
				if len(waiting.incidents) == 0 {
					t.Fatal("no durable incident at checkpoint")
				}
			case "race":
				if len(waiting.timers) == 0 || waiting.subscriptions == 0 {
					t.Fatal("event gateway did not arm both competing durable waits")
				}
			}
			closeStores(log, store) // simulate process exit after fsync

			replayLog, err := wal.Open(wal.Options{Dir: filepath.Join(liveDir, "wal")})
			if err != nil {
				t.Fatalf("reopen durable log: %v", err)
			}
			recovered, err := state.Open(filepath.Join(t.TempDir(), "replayed-state"))
			if err != nil {
				_ = replayLog.Close()
				t.Fatalf("open fresh replay state: %v", err)
			}
			defer closeStores(replayLog, recovered)
			restartClock := &driverClock{}
			restarted := engine.New(1, replayLog, recovered, restartClock)
			deployAll(restarted, defs, root)
			if err := restarted.Recover(); err != nil {
				t.Fatalf("Recover mid-flight: %v", err)
			}
			after, err := con3CaptureWait(recovered)
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(after, waiting) {
				t.Fatalf("mid-flight replay diverged\nlive=%+v\nreplay=%+v", waiting, after)
			}
			if err := restarted.Recover(); err != nil {
				t.Fatalf("repeat Recover: %v", err)
			}
			again, err := con3CaptureWait(recovered)
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(again, waiting) {
				t.Fatalf("repeated recovery duplicated pending work\nfirst=%+v\nsecond=%+v", waiting, again)
			}

			continued := &driver{p: restarted, store: recovered, cp: root,
				clock: restartClock, deployables: defs}
			for _, step := range sc.Driver[tc.prefix:] {
				if err := continued.apply(step); err != nil {
					t.Fatalf("after-restart step: %v", err)
				}
			}
			got, err := capture(recovered, root)
			if err != nil {
				t.Fatal(err)
			}
			// A separate uninterrupted execution is the reference for the same
			// model/driver; it itself checks final WAL replay.
			uninterrupted, err := Run(t.TempDir(), data, sc.Root, sc.Start, sc.Driver)
			if err != nil {
				t.Fatalf("uninterrupted reference: %v", err)
			}
			if !equalResult(got, uninterrupted) {
				t.Fatalf("restart changed outcome\nrestarted=%s\nnormal=%s",
					got.Golden(), uninterrupted.Golden())
			}
			if got.State != model.PICompleted {
				t.Fatalf("continued run state=%v, want completed", got.State)
			}
		})
	}
}
