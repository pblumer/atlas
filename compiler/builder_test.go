package compiler

import "testing"

func TestBuilderLinearProcess(t *testing.T) {
	b := NewBuilder(42, "order", 1)
	start := b.AddStartEvent()
	task := b.AddServiceTask("payment", 3)
	end := b.AddEndEvent()
	b.Connect(start, task)
	b.Connect(task, end)

	cp, err := b.Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	if got := cp.StartEvents(); len(got) != 1 || got[0] != start {
		t.Errorf("StartEvents = %v, want [%d]", got, start)
	}
	if cp.Node(start).Type != TypeStartEvent || cp.Node(task).Type != TypeServiceTask || cp.Node(end).Type != TypeEndEvent {
		t.Error("node types not linearized correctly")
	}

	// start → task
	out := cp.Outgoing(start)
	if len(out) != 1 || cp.Flow(out[0]).Target != task {
		t.Errorf("start outgoing = %v, want one flow to task", out)
	}
	// task → end
	out = cp.Outgoing(task)
	if len(out) != 1 || cp.Flow(out[0]).Target != end {
		t.Errorf("task outgoing = %v, want one flow to end", out)
	}
	// end has no outgoing
	if got := cp.Outgoing(end); len(got) != 0 {
		t.Errorf("end outgoing = %v, want none", got)
	}

	detail := cp.ServiceTask(cp.Node(task).Detail)
	if cp.Intern(detail.JobType) != "payment" || detail.Retries != 3 {
		t.Errorf("service task detail = %+v (jobType %q)", detail, cp.Intern(detail.JobType))
	}
}

func TestBuilderRejectsDanglingFlow(t *testing.T) {
	b := NewBuilder(1, "p", 1)
	start := b.AddStartEvent()
	b.Connect(start, 999) // no such node
	if _, err := b.Build(); err == nil {
		t.Error("Build with dangling flow = nil error, want error")
	}
}
