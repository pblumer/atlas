package model

import "testing"

// TestAFeedCutRoundTrips: the position the feed was pruned through survives the log,
// and a record cut short is refused rather than read as a cut at zero.
func TestAFeedCutRoundTrips(t *testing.T) {
	in := &FeedRetentionValue{Through: 1 << 40}
	v, err := DecodeValue(VTFeedRetention, AppendValue(nil, in))
	if err != nil {
		t.Fatalf("DecodeValue: %v", err)
	}
	if got := v.(*FeedRetentionValue); *got != *in {
		t.Fatalf("round trip = %+v, want %+v", got, in)
	}
	if _, err := DecodeValue(VTFeedRetention, []byte{1, 2, 3}); err == nil {
		t.Fatal("a cut of three bytes decoded")
	}
}
