package api

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/pblumer/atlas/api/panorama"
)

// TestRemoteFactsTellUnreachableFromStale is the distinction this whole slice
// exists to draw, and the reason it could not be drawn before: a source outside
// this process can both fail to answer and have answered a while ago, and those
// are not the same finding.
//
// Unreachable means nothing is known. Stale means something is known and may be
// wrong — an operator reading a stale row is reading history, and has to be told
// that rather than shown a peer that stopped existing an hour ago.
func TestRemoteFactsTellUnreachableFromStale(t *testing.T) {
	target := deploymentTarget{ID: "t1", Name: "Production", BaseURL: "https://peer.invalid"}
	descriptor := nodeDescriptor{ID: "rt-1", Name: "Zurich primary", Environment: "production", Version: "1.2.3"}
	now := time.Unix(1_700_000_000, 0)

	t.Run("answered", func(t *testing.T) {
		fact, runtimeID, runtimeFact := remoteFacts(target,
			remoteNodeObservation{descriptor: descriptor, observedAt: now}, now)
		if fact.State != panorama.StateHealthy || fact.Source != panorama.SourceRemote {
			t.Fatalf("a peer that answered = %+v", fact)
		}
		// It told this server its runtime id, so a model bound to *that node* now
		// observes too — which is why the descriptor is a stable id and not a
		// hostname.
		if runtimeID != descriptor.ID || runtimeFact.State != panorama.StateHealthy {
			t.Errorf("runtime = %q/%+v, want the peer's own id reported healthy", runtimeID, runtimeFact)
		}
		if fact.Detail["node"] != "Zurich primary (production)" {
			t.Errorf("detail = %v, want the peer's own name", fact.Detail)
		}
	})

	t.Run("answered before, refresh failed", func(t *testing.T) {
		seen := now.Add(-2 * time.Minute)
		fact, runtimeID, runtimeFact := remoteFacts(target,
			remoteNodeObservation{descriptor: descriptor, observedAt: seen, lastError: "refused the connection"}, now)
		if fact.State != panorama.StateStale {
			t.Fatalf("a failed refresh over a known peer = %+v, want stale", fact)
		}
		if !strings.Contains(fact.Reason, "history rather than status") {
			t.Errorf("reason = %q, want it to say what a stale row is", fact.Reason)
		}
		if !strings.Contains(fact.Reason, "refused the connection") {
			t.Errorf("reason = %q, want it to carry why the refresh failed", fact.Reason)
		}
		// The runtime goes stale with it. A model bound to that node must not read
		// as healthy because this server saw it two minutes ago.
		if runtimeID != descriptor.ID || runtimeFact.State != panorama.StateStale {
			t.Errorf("runtime = %q/%+v, want it stale too", runtimeID, runtimeFact)
		}
	})

	t.Run("never answered", func(t *testing.T) {
		fact, runtimeID, _ := remoteFacts(target,
			remoteNodeObservation{lastError: "could not be contacted"}, now)
		if fact.State != panorama.StateUnreachable {
			t.Fatalf("a peer that never answered = %+v, want unreachable", fact)
		}
		// Nothing is known, so there is no runtime to report. Reporting one would
		// be inventing a node this server has never spoken to.
		if runtimeID != "" {
			t.Errorf("an unreachable peer produced a runtime observation: %q", runtimeID)
		}
	})

	t.Run("answered too long ago to be worth repeating", func(t *testing.T) {
		seen := now.Add(-remoteNodeKeepFor - time.Minute)
		fact, _, _ := remoteFacts(target,
			remoteNodeObservation{descriptor: descriptor, observedAt: seen, lastError: "could not be contacted"}, now)
		if fact.State != panorama.StateUnreachable {
			t.Fatalf("an answer past its keep window = %+v, want unreachable", fact)
		}
	})
}

// TestRemoteFailureReasonNeverRepeatsTheAddress is the disclosure bound. Go's
// transport errors carry the address they dialled, and a deployment target's base
// URL is this operator's own infrastructure map — a Panorama document is opened by
// anyone with modeler access, and ADR-0211 §10 keeps exactly this out of what
// leaves the server.
//
// What survives is the category, which is what an operator acts on anyway: a
// refused connection, an unresolvable name and an untrusted certificate are fixed
// in three different places.
func TestRemoteFailureReasonNeverRepeatsTheAddress(t *testing.T) {
	const secretHost = "atlas-internal.corp.example"
	wrapped := func(inner error) error {
		return &url.Error{Op: "Get", URL: "https://" + secretHost + "/api/v1/node", Err: inner}
	}
	for name, tc := range map[string]struct {
		err  error
		want string
	}{
		"a name that does not resolve": {
			err:  wrapped(&net.DNSError{Err: "no such host", Name: secretHost}),
			want: "could not be resolved",
		},
		"a certificate that does not verify": {
			err:  wrapped(errors.New("x509: certificate signed by unknown authority")),
			want: "does not trust",
		},
		"a refused connection": {
			err:  wrapped(errors.New("dial tcp 10.1.2.3:443: connect: connection refused")),
			want: "refused the connection",
		},
		"a cancelled request": {
			err:  wrapped(context.Canceled),
			want: "cancelled",
		},
		"anything else": {
			err:  wrapped(errors.New("some other transport problem at " + secretHost)),
			want: "could not be contacted",
		},
	} {
		t.Run(name, func(t *testing.T) {
			got := remoteFailureReason(tc.err)
			if !strings.Contains(got, tc.want) {
				t.Errorf("reason = %q, want it to say %q", got, tc.want)
			}
			if strings.Contains(got, secretHost) || strings.Contains(got, "10.1.2.3") {
				t.Errorf("reason repeats the address back: %q", got)
			}
		})
	}

	// A timeout is its own category and names the deadline, because "it is slow" is
	// acted on differently from "it is not there".
	timeout := remoteFailureReason(&url.Error{Op: "Get", URL: "https://" + secretHost,
		Err: &net.DNSError{IsTimeout: true, Err: "i/o timeout"}})
	if !strings.Contains(timeout, "did not answer within") {
		t.Errorf("timeout reason = %q", timeout)
	}
}

// TestRemoteNodeIsNotAskedAgainWithinItsFreshnessContract. The cache is not only a
// cost decision: it is what makes an observation's timestamp mean something. It
// says when the peer actually answered, not when somebody happened to open a view,
// and opening three models in a row must not mean three rounds of calls to every
// configured target.
func TestRemoteNodeIsNotAskedAgainWithinItsFreshnessContract(t *testing.T) {
	var calls atomic.Int64
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.URL.Path != "/api/v1/node" {
			t.Errorf("peer was asked for %q, want the node descriptor", r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer peer-secret" {
			t.Errorf("Authorization = %q, want the target's credential", got)
		}
		_, _ = w.Write([]byte(`{"id":"rt-remote","name":"Geneva","environment":"staging","version":"1.0.0"}`))
	}))
	defer peer.Close()

	s := &Server{remoteNodes: newRemoteNodeCache()}
	target := remoteTarget{
		target:     deploymentTarget{ID: "t1", Name: "Production", BaseURL: peer.URL},
		credential: "peer-secret",
	}

	first := s.remoteNodeObservation(context.Background(), target)
	if first.lastError != "" || first.descriptor.ID != "rt-remote" {
		t.Fatalf("first read = %+v", first)
	}
	for range 5 {
		if again := s.remoteNodeObservation(context.Background(), target); again.observedAt != first.observedAt {
			t.Fatalf("a read inside the contract asked again: %+v", again)
		}
	}
	if got := calls.Load(); got != 1 {
		t.Fatalf("the peer was asked %d times for six reads inside one freshness window", got)
	}

	// Once the contract has run out the peer is asked again — and when that answer
	// is a failure, the last good one is kept so the caller can report it as stale
	// rather than as nothing.
	s.remoteNodes.put(target.target.ID, remoteNodeObservation{
		descriptor: first.descriptor, observedAt: time.Now().Add(-remoteNodeFreshFor - time.Second),
	})
	peer.Close()
	after := s.remoteNodeObservation(context.Background(), target)
	if after.lastError == "" {
		t.Fatal("a failed refresh was recorded as a success")
	}
	if after.descriptor.ID != "rt-remote" {
		t.Errorf("a failed refresh forgot the last good answer: %+v", after)
	}
}

// TestRemoteNodeRefusesARepliesThatIsNotADescriptor: something answered, and it is
// not an Atlas node — or it is one whose identity could not be read, which its own
// route refuses to serve. Either way there is nothing to correlate against, and
// treating it as a peer would put a healthy row on screen for a server that is not
// there.
func TestRemoteNodeRefusesAReplyThatIsNotADescriptor(t *testing.T) {
	for name, handler := range map[string]http.HandlerFunc{
		"not json":           func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("<html>hello")) },
		"json without an id": func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(`{"product":"Atlas"}`)) },
		"an http error": func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusForbidden)
		},
	} {
		t.Run(name, func(t *testing.T) {
			peer := httptest.NewServer(handler)
			defer peer.Close()
			s := &Server{remoteNodes: newRemoteNodeCache()}
			_, err := s.fetchRemoteNode(context.Background(), remoteTarget{
				target: deploymentTarget{ID: "t1", BaseURL: peer.URL},
			})
			if err == nil {
				t.Fatal("a reply that is not a descriptor was accepted as one")
			}
		})
	}

	// A forbidden reply says which status came back, because "grant this credential
	// the status scope" and "the server is down" are fixed in different places.
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusForbidden)
	}))
	defer peer.Close()
	s := &Server{remoteNodes: newRemoteNodeCache()}
	_, err := s.fetchRemoteNode(context.Background(), remoteTarget{
		target: deploymentTarget{ID: "t1", BaseURL: peer.URL},
	})
	if err == nil || !strings.Contains(err.Error(), "403") {
		t.Errorf("error = %v, want it to name the status the peer answered with", err)
	}
}

// TestOneDownPeerDoesNotHideTheOthers is the per-target isolation ADR-0189 §6
// requires in its own words: "a failed target does not hide healthy targets or make
// the whole model appear healthy". An architecture view that goes blank because one
// server is rebooting is worse than no view.
func TestOneDownPeerDoesNotHideTheOthers(t *testing.T) {
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"id":"rt-up","name":"Up"}`))
	}))
	defer up.Close()
	down := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	down.Close() // closed before it is asked

	s := &Server{remoteNodes: newRemoteNodeCache()}
	facts := panorama.Facts{Targets: map[string]panorama.Fact{}, Runtimes: map[string]panorama.Fact{}}
	s.observeRemoteNodes(context.Background(), []remoteTarget{
		{target: deploymentTarget{ID: "t-up", Name: "Up", BaseURL: up.URL}},
		{target: deploymentTarget{ID: "t-down", Name: "Down", BaseURL: down.URL}},
	}, facts)

	if facts.Targets["t-up"].State != panorama.StateHealthy {
		t.Errorf("the healthy peer = %+v", facts.Targets["t-up"])
	}
	if facts.Targets["t-down"].State != panorama.StateUnreachable {
		t.Errorf("the down peer = %+v", facts.Targets["t-down"])
	}
	if facts.Runtimes["rt-up"].State != panorama.StateHealthy {
		t.Errorf("the healthy peer's runtime = %+v", facts.Runtimes["rt-up"])
	}
}

// TestRemoteNodeCacheForgetsADeletedTarget: an answer nobody can trace to a row on
// screen is an answer that should not be held. Deleting a target actually forgets
// it rather than leaving its last descriptor in memory.
func TestRemoteNodeCacheForgetsADeletedTarget(t *testing.T) {
	cache := newRemoteNodeCache()
	cache.put("gone", remoteNodeObservation{descriptor: nodeDescriptor{ID: "rt-gone"}})
	cache.put("kept", remoteNodeObservation{descriptor: nodeDescriptor{ID: "rt-kept"}})

	cache.retain(map[string]bool{"kept": true})
	if _, ok := cache.get("gone"); ok {
		t.Error("a deleted target's answer is still held")
	}
	if _, ok := cache.get("kept"); !ok {
		t.Error("a live target's answer was dropped")
	}
}

// TestRemoteObservationIsSafeUnderConcurrentViews. The cache is written by
// goroutines waiting on the network — that is why it carries a lock rather than
// living on the run loop — so two architecture views opening at once must not race.
// The -race build is what actually checks this; the assertion is that it also
// stays correct.
func TestRemoteObservationIsSafeUnderConcurrentViews(t *testing.T) {
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"id":"rt-remote","name":"Geneva"}`))
	}))
	defer peer.Close()

	s := &Server{remoteNodes: newRemoteNodeCache()}
	peers := []remoteTarget{{target: deploymentTarget{ID: "t1", Name: "Production", BaseURL: peer.URL}}}

	var wg sync.WaitGroup
	results := make([]panorama.Facts, 8)
	for i := range results {
		results[i] = panorama.Facts{Targets: map[string]panorama.Fact{}, Runtimes: map[string]panorama.Fact{}}
		wg.Add(1)
		go func(facts panorama.Facts) {
			defer wg.Done()
			s.observeRemoteNodes(context.Background(), peers, facts)
		}(results[i])
	}
	wg.Wait()

	for i, facts := range results {
		if facts.Targets["t1"].State != panorama.StateHealthy {
			t.Errorf("view %d = %+v", i, facts.Targets["t1"])
		}
	}
}
