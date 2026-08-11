package engine_test

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/pblumer/atlas/compiler"
	"github.com/pblumer/atlas/engine"
	"github.com/pblumer/atlas/state"
	"github.com/pblumer/atlas/wal"
)

// sendTaskProcess builds Start → sendTask(job "send-email", 3 retries) → End. A send task is a
// service task under a different BPMN label (ADR-0112): it creates a job and waits, so it drives
// the same worker path. Returns the compiled process, the send-task job type, and the end id.
func sendTaskProcess(t testing.TB, key uint64) (*compiler.CompiledProcess, int32, int32) {
	t.Helper()
	b := compiler.NewBuilder(key, "send", 1)
	start := b.AddStartEvent()
	send := b.AddSendTask("send-email", 3)
	end := b.AddEndEvent()
	b.Connect(start, send)
	b.Connect(send, end)
	cp, err := b.Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	return cp, cp.SendTask(cp.Node(send).Detail).JobType, end
}

// TestSendTaskCreatesJobAndCompletes proves a send task creates a job on the send task's job type
// and waits; a worker completing it takes the outgoing flow to the end (ADR-0112) — the
// service-task job path, reached through TypeSendTask.
func TestSendTaskCreatesJobAndCompletes(t *testing.T) {
	h := openHarness(t, t.TempDir())
	defer h.close(t)
	cp, jobType, endEvent := sendTaskProcess(t, 90)

	p := engine.New(1, h.log, h.store, &manualClock{})
	p.SetJobNotifier(func(int32) {})
	p.Deploy(cp)
	if err := p.Recover(); err != nil {
		t.Fatalf("Recover: %v", err)
	}
	p.CreateInstance(cp.Key)
	if err := p.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle: %v", err)
	}
	// Parked on the send task's job.
	if pi, ei := counts(t, h.store); pi != 1 || ei != 1 {
		t.Fatalf("parked: process=%d element=%d, want 1 and 1 (waiting send-task job)", pi, ei)
	}
	jobKey := singleActivatableJob(t, h.store, jobType)

	p.CompleteJob(jobKey)
	if err := p.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle (complete): %v", err)
	}
	if pi, ei := counts(t, h.store); pi != 0 || ei != 0 {
		t.Fatalf("after completion: process=%d element=%d, want 0 and 0 (flowed to end)", pi, ei)
	}
	if v := elementVisits(t, h.store, cp.Key)[endEvent]; v != 1 {
		t.Errorf("end visits = %d, want 1 (send task completed and flowed on)", v)
	}
}

// TestSendTaskExhaustedRaisesIncident proves a send-task job whose retries are exhausted raises an
// incident carrying the failure message and parks the token — the incident model (ADR-0061)
// reaches a send task unchanged because it keys off the job, not the element type (ADR-0112).
func TestSendTaskExhaustedRaisesIncident(t *testing.T) {
	h := openHarness(t, t.TempDir())
	defer h.close(t)
	cp, jobType, _ := sendTaskProcess(t, 91)

	p := engine.New(1, h.log, h.store, &manualClock{})
	p.SetJobNotifier(func(int32) {})
	p.Deploy(cp)
	if err := p.Recover(); err != nil {
		t.Fatalf("Recover: %v", err)
	}
	p.CreateInstance(cp.Key)
	if err := p.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle: %v", err)
	}
	jobKey := singleActivatableJob(t, h.store, jobType)

	p.FailJob(jobKey, 0, "smtp down", 0)
	if err := p.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle (fail): %v", err)
	}
	inc := incidents(t, h.store)
	if len(inc) != 1 {
		t.Fatalf("exhausted send-task job raised %d incidents, want 1", len(inc))
	}
	for _, v := range inc {
		if v.JobKey != jobKey || v.Message != "smtp down" {
			t.Fatalf("incident = %+v, want it on the send-task job with the failure message", v)
		}
	}
	if pi, ei := counts(t, h.store); pi != 1 || ei != 1 {
		t.Fatalf("parked on incident: process=%d element=%d, want 1 and 1", pi, ei)
	}
}

// TestSendTaskRetryBackoff proves a failed send-task job with retries left and a backoff is held
// off the activatable index until its retry timer fires, then re-activates and completes — the
// ADR-0111 retry backoff, inherited by the send task unchanged (ADR-0112).
func TestSendTaskRetryBackoff(t *testing.T) {
	h := openHarness(t, t.TempDir())
	defer h.close(t)
	clk := &fixedClock{t: 1_000}
	cp, jobType, _ := sendTaskProcess(t, 92)

	p := engine.New(1, h.log, h.store, clk)
	p.SetJobNotifier(func(int32) {})
	p.Deploy(cp)
	if err := p.Recover(); err != nil {
		t.Fatalf("Recover: %v", err)
	}
	p.CreateInstance(cp.Key)
	if err := p.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle: %v", err)
	}
	jobKey := singleActivatableJob(t, h.store, jobType)

	const backoff = int64(30e9)
	p.FailJob(jobKey, 2, "transient", backoff)
	if err := p.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle (fail): %v", err)
	}
	if got := activatableJobs(t, h.store, jobType); len(got) != 0 {
		t.Fatalf("during backoff: activatable=%v, want none (held off the index)", got)
	}
	if n := len(incidents(t, h.store)); n != 0 {
		t.Fatalf("backoff raised %d incidents, want 0", n)
	}

	clk.t = 1_000 + backoff + 1
	if err := p.TickTimers(); err != nil {
		t.Fatalf("TickTimers (due): %v", err)
	}
	if got := activatableJobs(t, h.store, jobType); len(got) != 1 || got[0] != jobKey {
		t.Fatalf("after backoff: activatable=%v, want the job %d re-activated", got, jobKey)
	}

	p.CompleteJob(jobKey)
	if err := p.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle (complete): %v", err)
	}
	if pi := activeProcs(t, h.store); pi != 0 {
		t.Fatalf("after completing the retried job: active=%d, want 0", pi)
	}
}

// TestSendTaskBoundaryTimeout fires an interrupting timer boundary on a send task waiting for its
// job: the task is cancelled and the escalation flow runs — proof the send task is a first-class
// activity that hosts boundary events, the "send, but time out" pattern (ADR-0112).
func TestSendTaskBoundaryTimeout(t *testing.T) {
	h := openHarness(t, t.TempDir())
	defer h.close(t)
	const dur = int64(30e9)

	b := compiler.NewBuilder(93, "send-timeout", 1)
	start := b.AddStartEvent()
	send := b.AddSendTask("send-email", 3)
	timeout := b.AddBoundaryTimerEvent(send, true, dur)
	done := b.AddEndEvent()
	escalated := b.AddEndEvent()
	b.Connect(start, send)
	b.Connect(send, done)
	b.Connect(timeout, escalated)
	cp, err := b.Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	clk := &fixedClock{t: 1_000}

	p := engine.New(1, h.log, h.store, clk)
	p.SetJobNotifier(func(int32) {})
	p.Deploy(cp)
	if err := p.Recover(); err != nil {
		t.Fatalf("Recover: %v", err)
	}
	p.CreateInstance(cp.Key)
	if err := p.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle: %v", err)
	}
	// Parked: the send task and its armed boundary timer.
	if pi, ei := counts(t, h.store); pi != 1 || ei != 2 {
		t.Fatalf("parked: process=%d element=%d, want 1 and 2 (send task + armed boundary)", pi, ei)
	}

	// The timer fires before the job completes: the send task is interrupted.
	clk.t = 1_000 + dur + 1
	if err := p.TickTimers(); err != nil {
		t.Fatalf("TickTimers: %v", err)
	}
	if pi, ei := counts(t, h.store); pi != 0 || ei != 0 {
		t.Fatalf("after timeout: process=%d element=%d, want 0 and 0 (send task interrupted, escalated)", pi, ei)
	}
	v := elementVisits(t, h.store, cp.Key)
	if v[escalated] != 1 || v[done] != 0 {
		t.Errorf("visits escalated=%d done=%d, want 1 and 0", v[escalated], v[done])
	}
}

// TestSendTaskMessageKindCorrelates proves the message kind end to end: a <sendTask messageRef>
// compiles to a throw (ADR-0112) that, on activation, correlates the referenced message — waking a
// receive task already waiting on the same (name, key) — then flows straight on. The sender is
// parsed from XML (only the parser produces a message-kind send task); the receiver is the ADR-0102
// receive task. This proves the send task reaches the existing message-throw path unchanged.
func TestSendTaskMessageKindCorrelates(t *testing.T) {
	h := openHarness(t, t.TempDir())
	defer h.close(t)

	// Receiver: parks on a receive task waiting for "reply" with key "k".
	receiver, recvEnd := receiveTaskProcess(t, 95)

	// Sender: Start → sendTask(messageRef "reply", key "k") → End, from XML — the message kind.
	const senderXML = `<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL"
	             xmlns:zeebe="http://camunda.org/schema/zeebe/1.0">
	  <message id="Msg_reply" name="reply">
	    <extensionElements><zeebe:subscription correlationKey='="k"'/></extensionElements>
	  </message>
	  <process id="sender" isExecutable="true">
	    <startEvent id="s"/>
	    <sendTask id="send" messageRef="Msg_reply"/>
	    <endEvent id="e"/>
	    <sequenceFlow id="f1" sourceRef="s" targetRef="send"/>
	    <sequenceFlow id="f2" sourceRef="send" targetRef="e"/>
	  </process>
	</definitions>`
	sender, err := compiler.Parse(96, 1, strings.NewReader(senderXML))
	if err != nil {
		t.Fatalf("Parse sender: %v", err)
	}

	p := engine.New(1, h.log, h.store, &manualClock{})
	p.SetJobNotifier(func(int32) {})
	p.Deploy(receiver)
	p.Deploy(sender)
	if err := p.Recover(); err != nil {
		t.Fatalf("Recover: %v", err)
	}

	// Park the receiver first.
	p.CreateInstance(receiver.Key)
	if err := p.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle (receiver): %v", err)
	}
	if pi, ei := counts(t, h.store); pi != 1 || ei != 1 {
		t.Fatalf("receiver parked: process=%d element=%d, want 1 and 1", pi, ei)
	}

	// Run the sender: its send task throws "reply"/"k", waking the receiver, and flows on.
	p.CreateInstance(sender.Key)
	if err := p.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle (sender): %v", err)
	}
	if pi := activeProcs(t, h.store); pi != 0 {
		t.Fatalf("after send: active=%d, want 0 (sender flowed on, receiver correlated)", pi)
	}
	if v := elementVisits(t, h.store, receiver.Key)[recvEnd]; v != 1 {
		t.Errorf("receiver end visits = %d, want 1 (the send task's message woke the receive task)", v)
	}
}

// TestSendTaskOperationRefCorrelates proves the operationRef spelling of the message kind runs
// end to end (ADR-0112): a <sendTask operationRef> resolves the operation's inMessageRef and
// throws it, waking a waiting receive task and flowing on — the same path as a messageRef send.
func TestSendTaskOperationRefCorrelates(t *testing.T) {
	h := openHarness(t, t.TempDir())
	defer h.close(t)

	receiver, recvEnd := receiveTaskProcess(t, 97)

	// Sender: a send task referencing an operation whose inMessageRef is "reply" (key "k").
	const senderXML = `<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL"
	             xmlns:zeebe="http://camunda.org/schema/zeebe/1.0">
	  <message id="Msg_reply" name="reply">
	    <extensionElements><zeebe:subscription correlationKey='="k"'/></extensionElements>
	  </message>
	  <interface id="Iface"><operation id="Op_reply"><inMessageRef>Msg_reply</inMessageRef></operation></interface>
	  <process id="sender" isExecutable="true">
	    <startEvent id="s"/>
	    <sendTask id="send" operationRef="Op_reply"/>
	    <endEvent id="e"/>
	    <sequenceFlow id="f1" sourceRef="s" targetRef="send"/>
	    <sequenceFlow id="f2" sourceRef="send" targetRef="e"/>
	  </process>
	</definitions>`
	sender, err := compiler.Parse(98, 1, strings.NewReader(senderXML))
	if err != nil {
		t.Fatalf("Parse sender: %v", err)
	}

	p := engine.New(1, h.log, h.store, &manualClock{})
	p.SetJobNotifier(func(int32) {})
	p.Deploy(receiver)
	p.Deploy(sender)
	if err := p.Recover(); err != nil {
		t.Fatalf("Recover: %v", err)
	}

	p.CreateInstance(receiver.Key)
	if err := p.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle (receiver): %v", err)
	}
	if pi, ei := counts(t, h.store); pi != 1 || ei != 1 {
		t.Fatalf("receiver parked: process=%d element=%d, want 1 and 1", pi, ei)
	}

	p.CreateInstance(sender.Key)
	if err := p.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle (sender): %v", err)
	}
	if pi := activeProcs(t, h.store); pi != 0 {
		t.Fatalf("after operationRef send: active=%d, want 0 (sender flowed on, receiver correlated)", pi)
	}
	if v := elementVisits(t, h.store, receiver.Key)[recvEnd]; v != 1 {
		t.Errorf("receiver end visits = %d, want 1 (the operationRef send woke the receive task)", v)
	}
}

// TestSendTaskRecovers proves a parked send-task job survives a crash: replaying the log into a
// fresh store restores the waiting job, and completing it after recovery finishes the instance —
// the send task inherits the service task's recovery path (ADR-0112, invariant I4).
func TestSendTaskRecovers(t *testing.T) {
	dir := t.TempDir()
	cp, jobType, endEvent := sendTaskProcess(t, 94)

	h1 := openHarness(t, dir)
	p1 := engine.New(1, h1.log, h1.store, &manualClock{})
	p1.SetJobNotifier(func(int32) {})
	p1.Deploy(cp)
	if err := p1.Recover(); err != nil {
		t.Fatalf("Recover 1: %v", err)
	}
	p1.CreateInstance(cp.Key)
	if err := p1.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle: %v", err)
	}
	h1.close(t)

	// Replay into a fresh, empty store: the waiting job must rebuild from the log.
	log2, err := wal.Open(wal.Options{Dir: filepath.Join(dir, "wal")})
	if err != nil {
		t.Fatalf("wal.Open 2: %v", err)
	}
	store2, err := state.Open(filepath.Join(dir, "state2"))
	if err != nil {
		t.Fatalf("state.Open 2: %v", err)
	}
	defer func() { _ = store2.Close(); _ = log2.Close() }()
	p2 := engine.New(1, log2, store2, &manualClock{})
	p2.SetJobNotifier(func(int32) {})
	p2.Deploy(cp)
	if err := p2.Recover(); err != nil {
		t.Fatalf("Recover 2 (replay): %v", err)
	}
	if pi, ei := counts(t, store2); pi != 1 || ei != 1 {
		t.Fatalf("after replay: process=%d element=%d, want 1 and 1 (job rebuilt)", pi, ei)
	}

	// Completing the recovered job finishes the instance.
	jobKey := singleActivatableJob(t, store2, jobType)
	p2.CompleteJob(jobKey)
	if err := p2.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle (complete after recovery): %v", err)
	}
	if pi, ei := counts(t, store2); pi != 0 || ei != 0 {
		t.Fatalf("after completion: process=%d element=%d, want 0 and 0", pi, ei)
	}
	if v := elementVisits(t, store2, cp.Key)[endEvent]; v != 1 {
		t.Errorf("end visits = %d, want 1 (recovered send-task job completed)", v)
	}
}
