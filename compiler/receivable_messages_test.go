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
