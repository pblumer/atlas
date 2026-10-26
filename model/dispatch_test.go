package model

import "testing"

// TestADispatchRoundTrips: every field of a send comes back as it went in, a send that
// reached nobody included (ADR-0472 §1).
func TestADispatchRoundTrips(t *testing.T) {
	for _, in := range []*DispatchValue{
		{Channel: DispatchSignal, Source: DispatchFromProcess, Name: "order.cancelled",
			SenderProcessInstanceKey: 7, SenderProcessDefKey: 3, SenderElementId: 12, Reached: 2, Started: 1},
		{Channel: DispatchMessage, Source: DispatchFromPublish, Name: "invoice.ready", CorrelationKey: "INV-1"},
		{Channel: DispatchMessage, Source: DispatchDirected, Name: "tool.reset", CorrelationKey: "pos-1",
			TargetProcessInstanceKey: 99, TargetProcessDefKey: 4, Reached: 1},
	} {
		v, err := DecodeValue(VTDispatch, AppendValue(nil, in))
		if err != nil {
			t.Fatalf("DecodeValue(%+v): %v", in, err)
		}
		if got := v.(*DispatchValue); *got != *in {
			t.Fatalf("round trip = %+v, want %+v", got, in)
		}
	}
}

// TestTheReceptionsReadRecordsWrittenBeforeTheirSender: a signal reception's sender and
// stale mark are appended fields, so a record written before them decodes them as zero,
// and the fields before them are read as they always were (ADR-0472 §2).
func TestTheReceptionsReadRecordsWrittenBeforeTheirSender(t *testing.T) {
	sig := &SignalSubscriptionValue{ProcessInstanceKey: 1, ElementInstanceKey: 2, SignalName: "go",
		ProcessDefKey: 3, ElementId: 4, SenderProcessInstanceKey: 5, Stale: true}
	full := AppendValue(nil, sig)
	v, err := DecodeValue(VTSignal, full)
	if err != nil || *v.(*SignalSubscriptionValue) != *sig {
		t.Fatalf("signal round trip = %+v, %v", v, err)
	}
	old := full[:len(full)-9] // the record as a version before the fields wrote it
	v, err = DecodeValue(VTSignal, old)
	want := *sig
	want.SenderProcessInstanceKey, want.Stale = 0, false
	if err != nil || *v.(*SignalSubscriptionValue) != want {
		t.Fatalf("signal written before the fields = %+v, %v; want %+v", v, err, want)
	}
}

// TestACutShortDispatchIsAnError: a send record cut anywhere — before its kind, inside
// either name, or in its counts — is refused rather than read as something it is not.
func TestACutShortDispatchIsAnError(t *testing.T) {
	full := AppendValue(nil, &DispatchValue{Channel: DispatchMessage, Name: "n", CorrelationKey: "k"})
	for _, n := range []int{1, 3, 8, len(full) - 1} {
		if _, err := DecodeValue(VTDispatch, full[:n]); err == nil {
			t.Errorf("a send record cut to %d of %d bytes was decoded", n, len(full))
		}
	}
	if vt := (&DispatchValue{}).ValueType(); vt != VTDispatch {
		t.Errorf("a send record is a %s", vt)
	}
}
