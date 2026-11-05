package api

import (
	"testing"
	"time"
)

// The event feed keeps its rows as long as the operator said, and a zero or negative
// setting keeps the default rather than keeping nothing.
func TestWithEventFeedRetention(t *testing.T) {
	s := &Server{}
	WithEventFeedRetention(48 * time.Hour)(s)
	if s.eventFeedTTL != 48*time.Hour {
		t.Errorf("eventFeedTTL = %v, want 48h", s.eventFeedTTL)
	}
	WithEventFeedRetention(0)(s)
	if s.eventFeedTTL != 48*time.Hour {
		t.Errorf("a zero retention changed the setting to %v", s.eventFeedTTL)
	}
}
