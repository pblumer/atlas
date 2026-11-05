package workerproto

import (
	"strings"
	"testing"

	"github.com/pblumer/atlas/workersdk"
)

func TestAPollWithoutAVersionIsVersionOne(t *testing.T) {
	if err := Check(0); err != nil {
		t.Errorf("Check(0) = %v; a client from before the handshake must still be served", err)
	}
	if err := Check(1); err != nil {
		t.Errorf("Check(1) = %v", err)
	}
}

func TestAnUnknownVersionIsRefusedNamingTheSupportedOnes(t *testing.T) {
	err := Check(99)
	if err == nil {
		t.Fatal("Check(99) = nil, want a refusal")
	}
	if !strings.Contains(err.Error(), "accepts "+HeaderValue) {
		t.Errorf("Check(99) = %v, want it to name %q", err, HeaderValue)
	}
}

// The server accepts the current version and at most one before it: the compatibility
// rule in the package comment, held so that adding a version cannot silently widen it.
func TestTheServerAcceptsTheCurrentVersionAndOneBefore(t *testing.T) {
	got := Supported()
	if got[len(got)-1] != Current {
		t.Errorf("Supported() = %v, want it to end at Current (%d)", got, Current)
	}
	if len(got) > 2 || (len(got) == 2 && got[0] != Current-1) {
		t.Errorf("Supported() = %v, want Current and at most Current-1", got)
	}
	got[0] = 42
	if Supported()[0] == 42 {
		t.Error("Supported() hands out its own slice")
	}
}

// The SDK this repository ships must speak a version this server accepts: a server
// release that dropped it would lock out its own `atlas worker`.
func TestTheSDKSpeaksAVersionThisServerAccepts(t *testing.T) {
	if err := Check(workersdk.ProtocolVersion); err != nil {
		t.Errorf("workersdk.ProtocolVersion %d: %v", workersdk.ProtocolVersion, err)
	}
}
