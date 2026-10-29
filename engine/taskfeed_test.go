package engine_test

import (
	"path/filepath"
	"slices"
	"testing"

	"github.com/pblumer/atlas/compiler"
	"github.com/pblumer/atlas/engine"
	"github.com/pblumer/atlas/state"
)

// taskRows reads the feed's task rows of partition 1.
func taskRows(t *testing.T, s *state.Store) []state.FeedEntry {
	t.Helper()
	var out []state.FeedEntry
	if err := s.FeedAfter(1, 0, func(e state.FeedEntry) error {
		if e.Task != nil {
			out = append(out, e)
		}
		return nil
	}); err != nil {
		t.Fatalf("FeedAfter: %v", err)
	}
	return out
}

// TestAUserTasksLifeLeavesOnTheFeed (ADR-0510 §1): a
// user task opened, taken on, given back and completed is four rows on the feed, in
// that order, each with the job as it then stood, its key, the element it waits on and
// the definition its instance ran — and a replay of the log into an empty store
// writes the same rows under the same positions (I4/I6).
func TestAUserTasksLifeLeavesOnTheFeed(t *testing.T) {
	dir := t.TempDir()
	h := openHarness(t, dir)

	cp, jobType, element := assignmentProcess(t, "", "hr")
	p := engine.New(1, h.log, h.store, &manualClock{t: 1_000})
	p.Deploy(cp)
	if err := p.Recover(); err != nil {
		t.Fatalf("Recover: %v", err)
	}
	p.CreateInstance(cp.Key)
	if err := p.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle: %v", err)
	}
	jobKey := singleActivatableJob(t, h.store, jobType)
	for _, step := range []func(){
		func() { p.AssignJob(jobKey, "alice") },
		func() { p.AssignJob(jobKey, "") },
		func() { p.CompleteJob(jobKey) },
	} {
		step()
		if err := p.RunUntilIdle(); err != nil {
			t.Fatalf("RunUntilIdle: %v", err)
		}
	}

	rows := taskRows(t, h.store)
	want := []state.FeedKind{state.FeedTaskCreated, state.FeedTaskAssigned, state.FeedTaskUnassigned, state.FeedTaskCompleted}
	if got := kindsOf(rows); !slices.Equal(got, want) {
		t.Fatalf("task rows = %v, want %v", got, want)
	}
	for i, r := range rows {
		if r.TaskKey != jobKey || r.TaskElement != element || r.Definition != cp.Key || r.Task.CandidateGroups != "hr" {
			t.Errorf("row %d = key %d element %d definition %d groups %q, want %d %d %d hr",
				i, r.TaskKey, r.TaskElement, r.Definition, r.Task.CandidateGroups, jobKey, element, cp.Key)
		}
		if r.Task.CreatedAt == 0 {
			t.Errorf("row %d carries no opening time: %+v", i, r.Task)
		}
		if i > 0 && r.Position <= rows[i-1].Position {
			t.Errorf("row %d is at %d, before the row ahead of it at %d", i, r.Position, rows[i-1].Position)
		}
	}
	if rows[1].Task.Assignee != "alice" || rows[2].Task.Assignee != "" {
		t.Errorf("assignees = %q then %q, want alice then nobody", rows[1].Task.Assignee, rows[2].Task.Assignee)
	}
	if err := h.store.Close(); err != nil {
		t.Fatalf("store.Close: %v", err)
	}

	fresh, err := state.Open(filepath.Join(dir, "state-replayed"))
	if err != nil {
		t.Fatalf("state.Open: %v", err)
	}
	defer func() {
		_ = fresh.Close()
		_ = h.log.Close()
	}()
	q := engine.New(1, h.log, fresh, &manualClock{t: 9_000_000})
	q.Deploy(cp)
	if err := q.Recover(); err != nil {
		t.Fatalf("Recover (replay): %v", err)
	}
	replayed := taskRows(t, fresh)
	if len(replayed) != len(rows) {
		t.Fatalf("replayed task rows = %v, want %v", kindsOf(replayed), kindsOf(rows))
	}
	for i := range rows {
		a, b := rows[i], replayed[i]
		if a.Position != b.Position || a.At != b.At || a.Kind != b.Kind || a.TaskKey != b.TaskKey ||
			a.Definition != b.Definition || *a.Task != *b.Task {
			t.Errorf("row %d replayed as %+v (%+v), was %+v (%+v)", i, b, b.Task, a, a.Task)
		}
	}
}

// TestACancelledInstanceCancelsItsTaskOnTheFeed: a task whose instance is cancelled
// is atlas.task.cancelled, not completed — what a copy elsewhere needs to close its
// card as what it is.
func TestACancelledInstanceCancelsItsTaskOnTheFeed(t *testing.T) {
	h := openHarness(t, t.TempDir())
	defer h.close(t)

	cp, jobType, _ := assignmentProcess(t, "alice", "")
	p := engine.New(1, h.log, h.store, &manualClock{t: 1_000})
	p.Deploy(cp)
	if err := p.Recover(); err != nil {
		t.Fatalf("Recover: %v", err)
	}
	p.CreateInstance(cp.Key)
	if err := p.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle: %v", err)
	}
	jobKey := singleActivatableJob(t, h.store, jobType)
	jv, _, _ := h.store.GetJob(jobKey)
	p.CancelInstance(jv.ProcessInstanceKey)
	if err := p.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle: %v", err)
	}
	rows := taskRows(t, h.store)
	if got, want := kindsOf(rows), []state.FeedKind{state.FeedTaskCreated, state.FeedTaskCancelled}; !slices.Equal(got, want) {
		t.Fatalf("task rows = %v, want %v", got, want)
	}
	if rows[0].Task.Assignee != "alice" || rows[1].TaskKey != jobKey {
		t.Errorf("rows = %+v %+v", rows[0].Task, rows[1])
	}
}

// TestAServiceTasksJobIsNoTaskOnTheFeed: only the user-task type is a task; a job a
// Worker works leaves nothing on the feed.
func TestAServiceTasksJobIsNoTaskOnTheFeed(t *testing.T) {
	h := openHarness(t, t.TempDir())
	defer h.close(t)

	b := compiler.NewBuilder(defKey, "service", 1)
	start := b.AddStartEvent()
	task := b.AddServiceTask("buchen", 3)
	end := b.AddEndEvent()
	b.Connect(start, task)
	b.Connect(task, end)
	cp, err := b.Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	p := engine.New(1, h.log, h.store, &manualClock{t: 1_000})
	p.Deploy(cp)
	if err := p.Recover(); err != nil {
		t.Fatalf("Recover: %v", err)
	}
	p.CreateInstance(cp.Key)
	if err := p.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle: %v", err)
	}
	jobKey := singleActivatableJob(t, h.store, cp.ServiceTask(cp.Node(task).Detail).JobType)
	p.CompleteJob(jobKey)
	if err := p.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle: %v", err)
	}
	if rows := taskRows(t, h.store); len(rows) != 0 {
		t.Fatalf("a service task's job left task rows: %v", kindsOf(rows))
	}
}
