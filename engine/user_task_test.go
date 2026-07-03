package engine_test

import (
	"testing"

	"github.com/pblumer/atlas/compiler"
	"github.com/pblumer/atlas/engine"
	"github.com/pblumer/atlas/model"
	"github.com/pblumer/atlas/state"
)

const (
	utDefKey = 11
	utGroup  = "reviewers"
	utForm   = "review-form"
)

// userTaskProcess builds Start → UserTask → End and returns it with the
// interned candidate-group index the task is offered to.
func userTaskProcess(t testing.TB) (*compiler.CompiledProcess, int32) {
	t.Helper()
	b := compiler.NewBuilder(utDefKey, "human", 1)
	start := b.AddStartEvent()
	task := b.AddUserTask(utGroup, utForm)
	end := b.AddEndEvent()
	b.Connect(start, task)
	b.Connect(task, end)
	cp, err := b.Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	group := cp.UserTask(cp.Node(task).Detail).CandidateGroup
	return cp, group
}

func claimableTasks(t *testing.T, s *state.Store, group int32) []uint64 {
	t.Helper()
	var keys []uint64
	if err := s.ClaimableUserTasks(group, func(k uint64) error { keys = append(keys, k); return nil }); err != nil {
		t.Fatalf("ClaimableUserTasks: %v", err)
	}
	return keys
}

func assigneeTasks(t *testing.T, s *state.Store, assignee int32) []uint64 {
	t.Helper()
	var keys []uint64
	if err := s.UserTasksByAssignee(assignee, func(k uint64) error { keys = append(keys, k); return nil }); err != nil {
		t.Fatalf("UserTasksByAssignee: %v", err)
	}
	return keys
}

func userTaskCount(t *testing.T, s *state.Store) int {
	t.Helper()
	n, err := s.ActiveUserTaskCount()
	if err != nil {
		t.Fatalf("ActiveUserTaskCount: %v", err)
	}
	return n
}

// TestExecuteStartUserTaskEnd drives a user task end to end without a restart:
// the instance waits at the task, a human claims then completes it, and the
// claim moves the task from the group's claimable queue to the assignee's index.
func TestExecuteStartUserTaskEnd(t *testing.T) {
	h := openHarness(t, t.TempDir())
	defer h.close(t)
	cp, group := userTaskProcess(t)

	const assignee int32 = 42
	notified := 0
	p := engine.New(1, h.log, h.store, &manualClock{})
	p.Deploy(cp)
	p.SetUserTaskNotifier(func(int32) { notified++ })
	if err := p.Recover(); err != nil {
		t.Fatalf("Recover: %v", err)
	}

	p.CreateInstance(cp.Key)
	if err := p.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle: %v", err)
	}

	// Waiting at the user task: one instance, one element, one claimable task.
	if pi, ei := counts(t, h.store); pi != 1 || ei != 1 {
		t.Fatalf("after start: process=%d element=%d, want 1 and 1", pi, ei)
	}
	claimable := claimableTasks(t, h.store, group)
	if len(claimable) != 1 {
		t.Fatalf("claimable tasks = %d, want 1", len(claimable))
	}
	if n := userTaskCount(t, h.store); n != 1 {
		t.Fatalf("user tasks = %d, want 1", n)
	}
	if notified != 1 {
		t.Errorf("user-task notifications = %d, want 1 (side effect after fsync)", notified)
	}
	if a := assigneeTasks(t, h.store, assignee); len(a) != 0 {
		t.Errorf("assignee tasks before claim = %d, want 0", len(a))
	}

	// A human claims the task: it leaves the group queue and enters the
	// assignee's index, but the element instance keeps waiting.
	p.ClaimUserTask(claimable[0], assignee)
	if err := p.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle (claim): %v", err)
	}
	if c := claimableTasks(t, h.store, group); len(c) != 0 {
		t.Errorf("claimed task still in group queue = %v, want empty", c)
	}
	assigned := assigneeTasks(t, h.store, assignee)
	if len(assigned) != 1 || assigned[0] != claimable[0] {
		t.Fatalf("assignee tasks = %v, want [%d]", assigned, claimable[0])
	}
	if pi, ei := counts(t, h.store); pi != 1 || ei != 1 {
		t.Fatalf("after claim: process=%d element=%d, want 1 and 1", pi, ei)
	}
	ut, ok, err := h.store.GetUserTask(claimable[0])
	if err != nil || !ok {
		t.Fatalf("GetUserTask: ok=%v err=%v", ok, err)
	}
	if ut.State != model.UserTaskClaimed || ut.Assignee != assignee {
		t.Errorf("stored task = {state:%d assignee:%d}, want {claimed %d}", ut.State, ut.Assignee, assignee)
	}

	// Completing the task drives the instance to completion.
	p.CompleteUserTask(claimable[0])
	if err := p.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle (complete): %v", err)
	}
	if pi, ei := counts(t, h.store); pi != 0 || ei != 0 {
		t.Fatalf("after completion: process=%d element=%d, want 0 and 0", pi, ei)
	}
	if n := userTaskCount(t, h.store); n != 0 {
		t.Errorf("leftover user tasks = %d, want 0", n)
	}
	if a := assigneeTasks(t, h.store, assignee); len(a) != 0 {
		t.Errorf("leftover assignee tasks = %d, want 0", len(a))
	}
}

// TestRecoverUserTaskAcrossRestart runs until the instance waits on a claimed
// user task, simulates a crash, recovers by replaying the log, and asserts the
// task (including its claim) is rebuilt — then completes it to finish.
func TestRecoverUserTaskAcrossRestart(t *testing.T) {
	dir := t.TempDir()
	cp, group := userTaskProcess(t)
	clock := &manualClock{}
	const assignee int32 = 77

	// First run: start, then claim the task, then stop.
	h1 := openHarness(t, dir)
	p1 := engine.New(1, h1.log, h1.store, clock)
	p1.Deploy(cp)
	if err := p1.Recover(); err != nil {
		t.Fatalf("Recover 1: %v", err)
	}
	p1.CreateInstance(cp.Key)
	if err := p1.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle 1: %v", err)
	}
	claimable := claimableTasks(t, h1.store, group)
	if len(claimable) != 1 {
		t.Fatalf("before claim: claimable = %d, want 1", len(claimable))
	}
	taskKey := claimable[0]
	p1.ClaimUserTask(taskKey, assignee)
	if err := p1.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle (claim): %v", err)
	}

	// Crash.
	h1.close(t)

	// Restart: reopen and recover from the log.
	h2 := openHarness(t, dir)
	defer h2.close(t)
	p2 := engine.New(1, h2.log, h2.store, clock)
	p2.Deploy(cp)
	if err := p2.Recover(); err != nil {
		t.Fatalf("Recover 2: %v", err)
	}

	// State must be rebuilt: the same task, claimed by the same assignee, gone
	// from the group queue.
	if pi, ei := counts(t, h2.store); pi != 1 || ei != 1 {
		t.Fatalf("after recovery: process=%d element=%d, want 1 and 1", pi, ei)
	}
	if c := claimableTasks(t, h2.store, group); len(c) != 0 {
		t.Errorf("after recovery: claimable = %v, want empty (task was claimed)", c)
	}
	assigned := assigneeTasks(t, h2.store, assignee)
	if len(assigned) != 1 || assigned[0] != taskKey {
		t.Fatalf("after recovery: assignee tasks = %v, want [%d]", assigned, taskKey)
	}

	// Completing the recovered task must drive the instance to completion, which
	// also exercises that the key generator resumed without colliding.
	p2.CompleteUserTask(taskKey)
	if err := p2.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle 2: %v", err)
	}
	if pi, ei := counts(t, h2.store); pi != 0 || ei != 0 {
		t.Fatalf("after completion: process=%d element=%d, want 0 and 0", pi, ei)
	}
	if n := userTaskCount(t, h2.store); n != 0 {
		t.Errorf("leftover user tasks = %d, want 0", n)
	}
}
