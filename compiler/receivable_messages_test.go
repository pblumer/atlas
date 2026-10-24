package compiler

import (
	"strings"
	"testing"
)

// TestReceivableMessageNames pins what a definition can be *delivered*, which is
// the question the claim on a message name asks of it (ADR-0205).
//
// Five places carry one, and a check that knew about only some of them would be a
// gate with a hole: restricting it to message start events was the tempting
// simplification, and a catch in a process somebody starts themselves receives the
// payload just as well.
func TestReceivableMessageNames(t *testing.T) {
	b := NewBuilder(1, "empfaenger", 1)

	start := b.AddMessageStartEvent("post-eingegangen", nil, false)
	catch := b.AddMessageCatchEvent("antwort-da", nil)
	receive := b.AddReceiveTask("freigabe-erteilt", nil)
	task := b.AddServiceTask("arbeit", 1)
	b.AddBoundaryMessageEvent(task, true, "abbruch-gewuenscht", nil)
	// A throw sends; it must not appear.
	throw := b.AddMessageThrowEvent("wir-melden-uns", nil)
	end := b.AddEndEvent()

	// An event subprocess triggered by a message. The detail is what carries the
	// name; the subprocess node itself is ordinary.
	sub := b.AddSubProcess()
	b.SetEventSubProcess(sub, EventSubProcessDetail{
		Kind: BoundaryMessage, MessageName: "eskalation-gemeldet",
	})

	// A second start event on a name already used, to prove the answer is a set.
	b.AddMessageStartEvent("post-eingegangen", nil, false)

	b.Connect(start, catch)
	b.Connect(catch, receive)
	b.Connect(receive, task)
	b.Connect(task, throw)
	b.Connect(throw, end)

	cp, err := b.Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	got := cp.ReceivableMessageNames()
	want := []string{
		"abbruch-gewuenscht", "antwort-da", "eskalation-gemeldet",
		"freigabe-erteilt", "post-eingegangen",
	}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("= %v, want %v (sorted, deduplicated)", got, want)
	}
	for _, name := range got {
		if name == "wir-melden-uns" {
			t.Error("a message throw is listed as receivable — a throw sends")
		}
	}
}

// TestReceivableMessageNamesIgnoresOtherTriggers: a boundary event and an event
// subprocess share one struct across timers, signals, errors and the rest, so the
// message name field is populated only for the message kind. Reading it
// unconditionally would invent names out of the zero value — or worse, out of a
// signal's.
func TestReceivableMessageNamesIgnoresOtherTriggers(t *testing.T) {
	b := NewBuilder(1, "andere", 1)
	start := b.AddStartEvent()
	task := b.AddServiceTask("arbeit", 1)
	b.AddBoundaryTimerEvent(task, true, int64(60_000_000_000))
	sub := b.AddSubProcess()
	b.SetEventSubProcess(sub, EventSubProcessDetail{Kind: BoundarySignal, SignalName: "kein-nachrichtenname"})
	end := b.AddEndEvent()
	b.Connect(start, task)
	b.Connect(task, end)

	cp, err := b.Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if got := cp.ReceivableMessageNames(); len(got) != 0 {
		t.Errorf("= %v, want none: nothing here waits on a message", got)
	}
}

// TestReceivableMessageNamesOfAProcessWithNone is the ordinary case, and the one
// the claim check short-circuits on: most definitions receive no message at all.
func TestReceivableMessageNamesOfAProcessWithNone(t *testing.T) {
	b := NewBuilder(1, "schlicht", 1)
	start := b.AddStartEvent()
	end := b.AddEndEvent()
	b.Connect(start, end)

	cp, err := b.Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if got := cp.ReceivableMessageNames(); len(got) != 0 {
		t.Errorf("= %v, want none", got)
	}
}

// TestMessageReceiptsNameTheElement: the same five places as the set above, one entry
// per element, so an architecture sketch can say which element a message reaches
// (ADR-0471). A start is marked only where it creates an instance — at the process
// root — and an event subprocess is listed once, as itself.
func TestMessageReceiptsNameTheElement(t *testing.T) {
	b := NewBuilder(1, "empfaenger", 1)

	start := b.AddMessageStartEvent("post-eingegangen", nil, false)
	b.SetElementBpmnId(start, "Start_Post")
	catch := b.AddMessageCatchEvent("antwort-da", nil)
	b.SetElementBpmnId(catch, "Catch_Antwort")
	receive := b.AddReceiveTask("freigabe-erteilt", nil)
	b.SetElementBpmnId(receive, "Receive_Freigabe")
	task := b.AddServiceTask("arbeit", 1)
	boundary := b.AddBoundaryMessageEvent(task, true, "abbruch-gewuenscht", nil)
	b.SetElementBpmnId(boundary, "Boundary_Abbruch")
	throw := b.AddMessageThrowEvent("wir-melden-uns", nil)
	end := b.AddEndEvent()

	sub := b.AddSubProcess()
	b.SetElementBpmnId(sub, "EventSub_Eskalation")
	b.SetEventSubProcess(sub, EventSubProcessDetail{
		Kind: BoundaryMessage, MessageName: "eskalation-gemeldet",
	})
	timerSub := b.AddSubProcess()
	b.SetEventSubProcess(timerSub, EventSubProcessDetail{Kind: BoundarySignal, SignalName: "kein-nachrichtenname"})

	b.Connect(start, catch)
	b.Connect(catch, receive)
	b.Connect(receive, task)
	b.Connect(task, throw)
	b.Connect(throw, end)

	cp, err := b.Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	got := cp.MessageReceipts()
	want := []MessageReceipt{
		{ElementId: "Start_Post", MessageName: "post-eingegangen", Starts: true},
		{ElementId: "Catch_Antwort", MessageName: "antwort-da"},
		{ElementId: "Receive_Freigabe", MessageName: "freigabe-erteilt"},
		{ElementId: "Boundary_Abbruch", MessageName: "abbruch-gewuenscht"},
		{ElementId: "EventSub_Eskalation", MessageName: "eskalation-gemeldet"},
	}
	if len(got) != len(want) {
		t.Fatalf("= %+v, want %+v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("[%d] = %+v, want %+v", i, got[i], want[i])
		}
	}
}

// TestJobTypeRefsListServiceAndSendTasks: the work a process hands to whatever worker
// subscribes to a type — an architecture sketch draws it as a way out of the
// application (ADR-0471). A connector task names its worker and is not listed here.
func TestJobTypeRefsListServiceAndSendTasks(t *testing.T) {
	b := NewBuilder(1, "versand", 1)
	start := b.AddStartEvent()
	service := b.AddServiceTask("adresse-pruefen", 3)
	b.SetElementBpmnId(service, "Task_Adresse")
	send := b.AddSendTask("paket-melden", 3)
	b.SetElementBpmnId(send, "Send_Paket")
	end := b.AddEndEvent()
	b.Connect(start, service)
	b.Connect(service, send)
	b.Connect(send, end)

	cp, err := b.Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	got := cp.JobTypeRefs()
	want := []JobTypeRef{
		{ElementId: "Task_Adresse", JobType: "adresse-pruefen"},
		{ElementId: "Send_Paket", JobType: "paket-melden"},
	}
	if len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Errorf("= %+v, want %+v", got, want)
	}
}
