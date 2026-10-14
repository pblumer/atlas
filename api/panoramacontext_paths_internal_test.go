package api

import (
	"strings"
	"testing"

	"github.com/pblumer/atlas/api/panorama"
)

// How a node is matched to its address in a metrics store (ADR-0401), for the
// paths the main tests leave: a peer's runtime found through the target that
// reported it, a target whose base URL names no host or does not parse, a target
// store that cannot be read — and why the event log cannot be asked about a
// capability or about a kind it has never heard of.

// panoramaContextPathsTargets files deployment targets.
func panoramaContextPathsTargets(t *testing.T, s *Server, targets ...deploymentTarget) {
	t.Helper()
	var err error
	s.do(func() {
		for _, tg := range targets {
			if err = s.targets.Save(tg); err != nil {
				return
			}
		}
	})
	if err != nil {
		t.Fatalf("save targets: %v", err)
	}
}

// panoramaContextPathsInstance resolves one query on the loop.
func panoramaContextPathsInstance(s *Server, key, value string) (instance, reason string) {
	s.do(func() { instance, reason = s.metricInstanceFor(panorama.ContextQuery{Key: key, Value: value}) })
	return instance, reason
}

// TestPanoramaContextAPeersRuntimeIsFoundThroughItsTarget. Another node is
// reachable only through the target that reported it, so its address is that
// target's host — without the API port, which is not the metrics listener's.
func TestPanoramaContextAPeersRuntimeIsFoundThroughItsTarget(t *testing.T) {
	srv := newServerForErrors(t)
	panoramaContextPathsTargets(t, srv,
		deploymentTarget{ID: "t-eu", Name: "EU", BaseURL: "https://eu.example:8443"},
		deploymentTarget{ID: "t-bad", Name: "Bad", BaseURL: "http://[::1"},
		deploymentTarget{ID: "t-rel", Name: "Relative", BaseURL: "/atlas"},
	)
	// What an earlier observation of the peers left behind.
	srv.remoteNodes.put("t-eu", remoteNodeObservation{descriptor: nodeDescriptor{ID: "rt-eu"}})
	srv.remoteNodes.put("t-bad", remoteNodeObservation{descriptor: nodeDescriptor{ID: "rt-bad"}})

	if inst, why := panoramaContextPathsInstance(srv, panorama.KeyRuntimeID, "rt-eu"); inst != "eu.example" {
		t.Errorf("rt-eu = %q (%s), want eu.example", inst, why)
	}
	// A base URL that does not parse names no host, so the runtime behind it has no
	// address — said, rather than matched against an empty instance label.
	if inst, why := panoramaContextPathsInstance(srv, panorama.KeyRuntimeID, "rt-bad"); inst != "" ||
		!strings.Contains(why, "No deployment target here has reported this runtime") {
		t.Errorf("rt-bad = %q (%s), want no address", inst, why)
	}
	if inst, why := panoramaContextPathsInstance(srv, panorama.KeyDeploymentTargetID, "t-rel"); inst != "" ||
		!strings.Contains(why, "names no host") {
		t.Errorf("t-rel = %q (%s), want the missing host said", inst, why)
	}
}

// TestPanoramaContextUnreadableTargetsAreSaid. Neither a target nor a peer runtime
// can be addressed without the target list, and the answer says it could not be
// read rather than that nothing is configured.
func TestPanoramaContextUnreadableTargetsAreSaid(t *testing.T) {
	srv := newServerForErrors(t)
	recertifyHTTPPathsBreakDir(t, srv.targets.Dir())

	if inst, why := panoramaContextPathsInstance(srv, panorama.KeyDeploymentTargetID, "t-eu"); inst != "" ||
		!strings.Contains(why, "could not be read") {
		t.Errorf("target = %q (%s), want the read failure said", inst, why)
	}
	if inst, _ := panoramaContextPathsInstance(srv, panorama.KeyRuntimeID, "rt-elsewhere"); inst != "" {
		t.Errorf("runtime = %q, want no address without the targets", inst)
	}
}

// TestPanoramaContextTheEventLogSaysWhatItCannotName. A capability is realised by
// processes and is not one, and a kind the log has never heard of gets the general
// sentence rather than one borrowed from another kind.
func TestPanoramaContextTheEventLogSaysWhatItCannotName(t *testing.T) {
	for _, key := range []string{panorama.KeyCapabilityKey, panorama.KeyValueStreamKey} {
		if why := contextUnidentifiableReason(key); !strings.Contains(why, "processes that realise one") {
			t.Errorf("%s: %q, want the capability explanation", key, why)
		}
	}
	if why := contextUnidentifiableReason("atlas.somethingNew"); why !=
		"The event log records nothing that identifies a resource of this kind." {
		t.Errorf("unknown kind: %q", why)
	}
}
