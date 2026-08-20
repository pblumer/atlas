package model

import "testing"

// The lease epoch rides along with every other job field, and an older record —
// written before the field existed — must still decode, leaving it zero. Zero is
// "no lease has ever been handed out", which is exactly what such a record means.
func TestJobValueLeaseEpochRoundTripsAndIsAppendCompatible(t *testing.T) {
	full := JobValue{
		ProcessInstanceKey: 11, ElementInstanceKey: 22, JobType: 7, Retries: 3,
		Deadline: 99, Assignee: "mailer-1", RetryDueDate: 5, LeaseExpiresAt: 1234, LeaseEpoch: 42,
	}
	var got JobValue
	if err := got.decode(full.encode(nil)); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got != full {
		t.Errorf("round trip = %+v, want %+v", got, full)
	}

	// A record encoded without the epoch (the previous wire shape) decodes to 0.
	old := JobValue{ProcessInstanceKey: 11, ElementInstanceKey: 22, JobType: 7, Retries: 3,
		Deadline: 99, Assignee: "mailer-1", RetryDueDate: 5, LeaseExpiresAt: 1234}
	short := old.encode(nil)
	short = short[:len(short)-8] // drop the trailing epoch word
	var legacy JobValue
	if err := legacy.decode(short); err != nil {
		t.Fatalf("decode legacy: %v", err)
	}
	if legacy.LeaseEpoch != 0 {
		t.Errorf("LeaseEpoch = %d on a record written before the field, want 0", legacy.LeaseEpoch)
	}
	legacy.LeaseEpoch = full.LeaseEpoch
	if legacy != full {
		t.Errorf("legacy record decoded to %+v, want everything else intact", legacy)
	}
}
