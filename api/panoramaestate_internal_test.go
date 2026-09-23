package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/pblumer/atlas/api/panorama"
	"github.com/pblumer/atlas/limits"
)

// The federated estate read (ADR-0402, slice 2b). Every test here
// is one of the record's own rules held against the collector, and the two that matter
// most are the ones about *not* knowing: a peer that answers and does not serve this
// view, and a peer that serves it and would not show it to this credential, are
// different findings from a peer that is down, and each sends an operator somewhere
// else.

// estatePeer is a peer Atlas that answers the two reads the estate makes: the
// descriptor, and the starmap. Each handler is optional — a nil one is a route the peer
// does not serve — and every call is counted, because "was this peer asked at all" is
// half of what these tests assert.
type estatePeer struct {
	server    *httptest.Server
	descripts atomic.Int64
	meshes    atomic.Int64
}

func newEstatePeer(t *testing.T, descriptor string, graph any) *estatePeer {
	t.Helper()
	p := &estatePeer{}
	p.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/node":
			p.descripts.Add(1)
			if descriptor == "" {
				w.WriteHeader(http.StatusNotFound)
				return
			}
			_, _ = w.Write([]byte(descriptor))
		case "/api/v1/panorama/mesh":
			p.meshes.Add(1)
			switch answer := graph.(type) {
			case nil:
				w.WriteHeader(http.StatusNotFound)
			case int:
				w.WriteHeader(answer)
			case string:
				_, _ = w.Write([]byte(answer))
			default:
				_ = json.NewEncoder(w).Encode(answer)
			}
		default:
			t.Errorf("the peer was asked for %q, which the estate read has no business asking for", r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(p.server.Close)
	return p
}

// newEstateServer is a Server with nothing but the two peer caches, which is all a
// federated read touches: no store, no run loop, no engine.
func newEstateServer() *Server {
	return &Server{
		remoteNodes:      newRemoteNodeCache(),
		remoteLandscapes: newRemoteLandscapeCache(),
	}
}

// servingDescriptor is a peer that advertises the starmap read.
const servingDescriptor = `{"id":"rt-geneva","name":"Geneva","environment":"prod",` +
	`"version":"1.2.0","features":["observations.stats","panorama.mesh"]}`

// peerGraph is one peer's landscape as it comes off the wire.
func peerGraph(runtimeID string, nodes, restricted int) panorama.Graph {
	g := panorama.Graph{RuntimeID: runtimeID, Restricted: restricted}
	for i := range nodes {
		g.Nodes = append(g.Nodes, panorama.Node{
			ID: fmt.Sprintf("process:%d", i), Kind: panorama.KindProcess,
			State: panorama.StateHealthy,
		})
	}
	return g
}

// TestAPeerLandscapeIsAskedForOnceInsideItsContract pins the whole cost argument of the
// slice. The estate view asks every peer for a document three orders of magnitude larger
// than a descriptor, so a reader who opens it twice must not cause two rounds of
// fan-out — and the contract is what makes the count on a domain mean "when the peer
// answered" rather than "when somebody opened a tab".
func TestAPeerLandscapeIsAskedForOnceInsideItsContract(t *testing.T) {
	peer := newEstatePeer(t, servingDescriptor, peerGraph("rt-geneva", 12, 0))
	s := newEstateServer()
	target := remoteTarget{
		target:     deploymentTarget{ID: "t1", Name: "Production", BaseURL: peer.server.URL},
		credential: "peer-secret",
	}

	for range 4 {
		domain := s.estateDomain(context.Background(), target, time.Now())
		if domain.State != panorama.StateHealthy {
			t.Fatalf("domain = %+v, want a healthy read", domain)
		}
		if domain.Holds != 12 {
			t.Fatalf("holds = %d, want the 12 nodes the peer reported", domain.Holds)
		}
	}
	if got := peer.meshes.Load(); got != 1 {
		t.Errorf("the peer's starmap was read %d times for four estate reads inside one contract", got)
	}
}

// TestAPeerLandscapeReadPresentsTheTargetsCredential is ADR-0402 §1 at the wire: a
// federated read is made with the credential stored for that target, which is exactly
// why the picture has to say so afterwards.
func TestAPeerLandscapeReadPresentsTheTargetsCredential(t *testing.T) {
	var sent atomic.Value
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v1/panorama/mesh" {
			sent.Store(r.Header.Get("Authorization"))
			_ = json.NewEncoder(w).Encode(peerGraph("rt-geneva", 1, 0))
			return
		}
		_, _ = w.Write([]byte(servingDescriptor))
	}))
	defer peer.Close()

	s := newEstateServer()
	s.estateDomain(context.Background(), remoteTarget{
		target:     deploymentTarget{ID: "t1", Name: "Production", BaseURL: peer.URL},
		credential: "landscape-secret",
	}, time.Now())

	if got, _ := sent.Load().(string); got != "Bearer landscape-secret" {
		t.Errorf("Authorization = %q, want the target's credential", got)
	}
}

// TestAPeerThatDoesNotServeTheViewIsAVersionBoundary is §3's fifth state. An older build
// is neither unreachable — which sends an operator to look at a network — nor stale,
// which implies there was once an answer. And nothing is asked of it: its descriptor is
// derived from the routes it mounts, so the absent feature *is* the answer, and a 404
// fetched anyway would arrive looking like a fault.
func TestAPeerThatDoesNotServeTheViewIsAVersionBoundary(t *testing.T) {
	peer := newEstatePeer(t,
		`{"id":"rt-old","name":"Bern","features":["observations.stats"]}`, nil)
	s := newEstateServer()

	domain := s.estateDomain(context.Background(), remoteTarget{
		target: deploymentTarget{ID: "t1", Name: "Bern", BaseURL: peer.server.URL},
	}, time.Now())

	if domain.State != panorama.StateUnserved {
		t.Errorf("state = %q, want %q", domain.State, panorama.StateUnserved)
	}
	if domain.Holds != 0 {
		t.Errorf("holds = %d; a peer that does not serve the view reported no landscape", domain.Holds)
	}
	if got := peer.meshes.Load(); got != 0 {
		t.Errorf("the peer was asked for a starmap %d times although it advertises none", got)
	}
	// It is a shape, not a gap: named, and neutral rather than red.
	if domain.Name != "Bern" {
		t.Errorf("name = %q, want the peer's own name", domain.Name)
	}
}

// TestARefusedStarmapIsNotAFaultOfThePeer is the case the field will actually produce:
// the peer is up, serves the route, and the credential this side stored for it does not
// reach the starmap (ADR-0410). It is reported as
// unreachable — nothing is known about that domain's landscape — and never as not-ready,
// which is critical severity and would paint a working installation red over a scope
// this operator controls. The reason has to name the status, because that is what says
// which of the two it is.
func TestARefusedStarmapIsNotAFaultOfThePeer(t *testing.T) {
	peer := newEstatePeer(t, servingDescriptor, http.StatusForbidden)
	s := newEstateServer()

	domain := s.estateDomain(context.Background(), remoteTarget{
		target: deploymentTarget{ID: "t1", Name: "Geneva", BaseURL: peer.server.URL},
	}, time.Now())

	if domain.State != panorama.StateUnreachable {
		t.Errorf("state = %q, want %q", domain.State, panorama.StateUnreachable)
	}
	if !strings.Contains(domain.Reason, "403") {
		t.Errorf("reason = %q, want it to name the status the peer answered with", domain.Reason)
	}
	if domain.RuntimeID != "rt-geneva" {
		t.Errorf("runtimeId = %q; the peer did identify itself, and that much is known",
			domain.RuntimeID)
	}
}

// TestACollapsedPeerReportsWhatItStandsFor. A peer over its own size budget collapses
// its landscape to applications and records how many nodes each one stands for
// (ADR-0211 §7). Counting the nodes of that answer would report a domain of nine hundred
// as one of two — inside the field whose whole job is to say how big a domain is (§2).
func TestACollapsedPeerReportsWhatItStandsFor(t *testing.T) {
	collapsed := panorama.Graph{
		RuntimeID: "rt-geneva", Clustered: true,
		Nodes: []panorama.Node{
			{ID: "application:a", Kind: panorama.KindApplication, Children: 400},
			{ID: "application:b", Kind: panorama.KindApplication, Children: 498},
		},
	}
	peer := newEstatePeer(t, servingDescriptor, collapsed)
	s := newEstateServer()

	domain := s.estateDomain(context.Background(), remoteTarget{
		target: deploymentTarget{ID: "t1", Name: "Geneva", BaseURL: peer.server.URL},
	}, time.Now())

	if domain.Holds != 900 {
		t.Errorf("holds = %d, want the 900 nodes the two collapsed applications stand for",
			domain.Holds)
	}
}

// TestADomainSaysWhoseCredentialDrewItAndWhatItCouldNotSee is §1's disclosure rule in
// both halves. Naming the credential says who looked; the restricted count says how much
// of the domain that look did not reach. A federated picture that carried the first and
// not the second would name a credential and imply its reach was total.
func TestADomainSaysWhoseCredentialDrewItAndWhatItCouldNotSee(t *testing.T) {
	peer := newEstatePeer(t, servingDescriptor, peerGraph("rt-geneva", 40, 9))
	s := newEstateServer()

	domain := s.estateDomain(context.Background(), remoteTarget{
		target: deploymentTarget{ID: "t1", Name: "Geneva prod", BaseURL: peer.server.URL},
	}, time.Now())

	if domain.DrawnBy != "Geneva prod" {
		t.Errorf("drawnBy = %q, want the target whose credential made the read", domain.DrawnBy)
	}
	if domain.Restricted != 9 {
		t.Errorf("restricted = %d, want the 9 placeholders the peer's own answer carried",
			domain.Restricted)
	}
	if domain.Holds != 40 {
		t.Errorf("holds = %d, want 40; the placeholders are nodes on that picture too",
			domain.Holds)
	}
}

// TestANeverEmittedVaultReference keeps the credential's *name* out of the picture.
// ADR-0211 §10 keeps this operator's infrastructure out of what leaves the server, and
// the landscape already refuses a worker's CredentialsRef for the same reason — a secret
// reference is a name somebody can ask for.
func TestANeverEmittedVaultReference(t *testing.T) {
	peer := newEstatePeer(t, servingDescriptor, peerGraph("rt-geneva", 2, 0))
	s := newEstateServer()

	domain := s.estateDomain(context.Background(), remoteTarget{
		target: deploymentTarget{
			ID: "t1", Name: "Geneva", BaseURL: peer.server.URL,
			CredentialRef: "vault://peer/geneva-landscape",
		},
		credential: "landscape-secret",
	}, time.Now())

	rendered, err := json.Marshal(panorama.DeriveEstate(
		panorama.EstateDomain{ID: "local", Name: "Zurich"}, []panorama.EstateDomain{domain}))
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{"vault://", "geneva-landscape", "landscape-secret", peer.server.URL} {
		if strings.Contains(string(rendered), forbidden) {
			t.Errorf("the estate carries %q", forbidden)
		}
	}
}

// TestALandscapeThatCouldNotBeRefreshedIsHistory. The two states the peer channel exists
// to keep apart, one hop out: a landscape that was read and cannot be read again is
// history and says so, which is a different thing from a domain nothing is known about.
func TestALandscapeThatCouldNotBeRefreshedIsHistory(t *testing.T) {
	peer := newEstatePeer(t, servingDescriptor, http.StatusBadGateway)
	s := newEstateServer()
	now := time.Now()
	s.remoteLandscapes.put("t1", remoteLandscape{
		runtimeID: "rt-geneva", holds: 77, restricted: 3,
		observedAt: now.Add(-2 * time.Minute),
	})

	domain := s.estateDomain(context.Background(), remoteTarget{
		target: deploymentTarget{ID: "t1", Name: "Geneva", BaseURL: peer.server.URL},
	}, now)

	if domain.State != panorama.StateStale {
		t.Fatalf("state = %q, want %q", domain.State, panorama.StateStale)
	}
	if domain.Holds != 77 || domain.Restricted != 3 {
		t.Errorf("holds/restricted = %d/%d, want the last answer kept", domain.Holds, domain.Restricted)
	}
	if !strings.Contains(domain.Reason, "history") {
		t.Errorf("reason = %q, want it to say what is being shown", domain.Reason)
	}
}

// TestASilentPeerIsAShapeRatherThanAGap is §3 for the ordinary failure. A peer that never
// answered has no runtime id to be named by, and it is still a domain on the picture:
// addressed by the target an operator configured, named by that configuration, and
// carrying the join a promotion recorded — because that join is a local fact and does not
// depend on the peer being up to be true.
func TestASilentPeerIsAShapeRatherThanAGap(t *testing.T) {
	down := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	down.Close()

	s := newEstateServer()
	domain := s.estateDomain(context.Background(), remoteTarget{
		target: deploymentTarget{
			ID: "t1", Name: "Lugano", BaseURL: down.URL,
			Bindings: map[string]string{"app-orders": "remote-orders"},
		},
	}, time.Now())

	if domain.State != panorama.StateUnreachable {
		t.Errorf("state = %q, want %q", domain.State, panorama.StateUnreachable)
	}
	if domain.Name != "Lugano" {
		t.Errorf("name = %q, want the configured name; there is no other one to use", domain.Name)
	}
	if domain.RuntimeID != "" {
		t.Errorf("runtimeId = %q, want none invented for a peer that never answered", domain.RuntimeID)
	}
	if len(domain.Applications) != 1 {
		t.Errorf("applications = %v, want the recorded promotion", domain.Applications)
	}
}

// TestTwoIdentitiesFromOneTargetAreRefused. ADR-0401 §1 makes (runtimeId, key) an
// entity's estate-wide name and requires a consumer to refuse to join keys it cannot
// place rather than assume they are local. A target whose two reads come back as two
// runtimes — something in front of it answering from two servers — is exactly that, so
// the domain carries no identity and no counts, and says why.
func TestTwoIdentitiesFromOneTargetAreRefused(t *testing.T) {
	peer := newEstatePeer(t, servingDescriptor, peerGraph("rt-somewhere-else", 33, 0))
	s := newEstateServer()

	domain := s.estateDomain(context.Background(), remoteTarget{
		target: deploymentTarget{ID: "t1", Name: "Geneva", BaseURL: peer.server.URL},
	}, time.Now())

	if domain.State != panorama.StateUnreachable {
		t.Errorf("state = %q, want %q", domain.State, panorama.StateUnreachable)
	}
	if domain.RuntimeID != "" || domain.Holds != 0 {
		t.Errorf("domain = %+v, want nothing from an answer that cannot be placed", domain)
	}
	if !strings.Contains(domain.Reason, "two different runtimes") {
		t.Errorf("reason = %q, want it to name what happened", domain.Reason)
	}
}

// TestALandscapeOverItsBudgetIsRefusedRatherThanTruncated. A truncated JSON document is
// indistinguishable from a peer replying with nonsense, so the ceiling has to produce a
// refusal rather than a half-read graph that decodes into a smaller domain.
func TestALandscapeOverItsBudgetIsRefusedRatherThanTruncated(t *testing.T) {
	peer := newEstatePeer(t, servingDescriptor, peerGraph("rt-geneva", 400, 0))
	s := newEstateServer()
	s.limits = limits.Default()
	s.limits.PeerLandscape = 1 << 10 // a kilobyte: smaller than any real landscape

	domain := s.estateDomain(context.Background(), remoteTarget{
		target: deploymentTarget{ID: "t1", Name: "Geneva", BaseURL: peer.server.URL},
	}, time.Now())

	if domain.State != panorama.StateUnreachable {
		t.Errorf("state = %q, want the read refused", domain.State)
	}
	if domain.Holds != 0 {
		t.Errorf("holds = %d, want nothing counted from a truncated document", domain.Holds)
	}
}

// TestOneDownPeerDoesNotEmptyTheEstate is the per-target isolation of ADR-0189 §6,
// carried to the estate: one server rebooting must not remove a domain that answered, and
// must never fail the whole picture.
func TestOneDownPeerDoesNotEmptyTheEstate(t *testing.T) {
	up := newEstatePeer(t, servingDescriptor, peerGraph("rt-geneva", 5, 0))
	down := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	down.Close()

	s := newEstateServer()
	domains := s.estateDomains(context.Background(), []remoteTarget{
		{target: deploymentTarget{ID: "t-up", Name: "Geneva", BaseURL: up.server.URL}},
		{target: deploymentTarget{ID: "t-down", Name: "Lugano", BaseURL: down.URL}},
	})

	if len(domains) != 2 {
		t.Fatalf("got %d domains, want one per configured target", len(domains))
	}
	if domains[0].State != panorama.StateHealthy || domains[0].Holds != 5 {
		t.Errorf("the peer that answered = %+v", domains[0])
	}
	if domains[1].State != panorama.StateUnreachable {
		t.Errorf("the peer that did not = %+v", domains[1])
	}
}

// TestADeletedTargetIsForgottenByBothHalves. A target removed from the configuration must
// leave nothing behind that a later picture could be drawn from — and the landscape half
// is as capable of being that leftover as the descriptor half.
func TestADeletedTargetIsForgottenByBothHalves(t *testing.T) {
	s := newEstateServer()
	s.remoteNodes.put("gone", remoteNodeObservation{observedAt: time.Now()})
	s.remoteLandscapes.put("gone", remoteLandscape{holds: 12, observedAt: time.Now()})

	up := newEstatePeer(t, servingDescriptor, peerGraph("rt-geneva", 1, 0))
	s.estateDomains(context.Background(), []remoteTarget{
		{target: deploymentTarget{ID: "t-up", Name: "Geneva", BaseURL: up.server.URL}},
	})

	if _, ok := s.remoteNodes.get("gone"); ok {
		t.Error("a deleted target's descriptor is still remembered")
	}
	if _, ok := s.remoteLandscapes.get("gone"); ok {
		t.Error("a deleted target's landscape is still remembered")
	}
}

// TestTheJoinIsCopiedOffTheRunLoopsRecord. The target record belongs to the run loop, and
// a promotion writes its bindings. A domain assembled off the loop that aliased that map
// would be read here while the loop is free to write it (I3) — a data race, and the race
// detector only finds it if something actually writes.
func TestTheJoinIsCopiedOffTheRunLoopsRecord(t *testing.T) {
	peer := newEstatePeer(t, servingDescriptor, peerGraph("rt-geneva", 1, 0))
	bindings := map[string]string{"app-orders": "remote-orders"}
	s := newEstateServer()

	domain := s.estateDomain(context.Background(), remoteTarget{
		target: deploymentTarget{
			ID: "t1", Name: "Geneva", BaseURL: peer.server.URL, Bindings: bindings,
		},
	}, time.Now())

	bindings["app-invoices"] = "remote-invoices" // the next promotion, on the loop
	if len(domain.Applications) != 1 {
		t.Errorf("the domain's join followed the record: %v", domain.Applications)
	}
}

// TestTheLocalDomainIsDrawnByTheReader. On the one domain a reader is standing in, the
// picture is filtered by who is looking rather than by what a credential may see — so it
// names no credential, and its restricted count is the reader's own.
func TestTheLocalDomainIsDrawnByTheReader(t *testing.T) {
	local := localEstateDomain(
		peerGraph("rt-zurich", 120, 4),
		nodeDescriptor{ID: "rt-zurich", Name: "Zurich", Environment: "prod"})

	if local.ID != estateLocalDomainID {
		t.Errorf("id = %q, want the fixed local handle", local.ID)
	}
	if local.Name != "Zurich (prod)" {
		t.Errorf("name = %q, want this installation's own name", local.Name)
	}
	if local.RuntimeID != "rt-zurich" {
		t.Errorf("runtimeId = %q, want the id of the graph the holds were counted in", local.RuntimeID)
	}
	if local.Holds != 120 || local.Restricted != 4 {
		t.Errorf("holds/restricted = %d/%d, want the local landscape's own", local.Holds, local.Restricted)
	}
	if local.DrawnBy != "" {
		t.Errorf("drawnBy = %q; this domain was drawn by the reader, not by a credential", local.DrawnBy)
	}
	if local.State != panorama.StateHealthy {
		t.Errorf("state = %q; the domain the reader is standing in answered", local.State)
	}
}

// TestNoPeersIsNoFanOut. An installation with no deployment targets is an estate of one,
// and it must not pay a round of anything to find that out.
func TestNoPeersIsNoFanOut(t *testing.T) {
	if got := newEstateServer().estateDomains(context.Background(), nil); got != nil {
		t.Errorf("domains = %v, want none", got)
	}
}

// TestAStaleDomainIsDrawnFromWhatWasLastKnown. When the *descriptor* read is what went
// stale, the domain is history on both halves: the identity from the last descriptor and
// the size from the last landscape. Leaving the counts at zero would draw a domain that
// looks empty rather than one that looks out of date, which is the collapse of the two
// states this channel exists to prevent.
func TestAStaleDomainIsDrawnFromWhatWasLastKnown(t *testing.T) {
	down := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	down.Close()

	s := newEstateServer()
	now := time.Now()
	s.remoteNodes.put("t1", remoteNodeObservation{
		descriptor: nodeDescriptor{ID: "rt-geneva", Name: "Geneva", Features: []string{featurePanoramaMesh}},
		observedAt: now.Add(-remoteNodeFreshFor - time.Second),
	})
	s.remoteLandscapes.put("t1", remoteLandscape{
		runtimeID: "rt-geneva", holds: 88, restricted: 2, observedAt: now.Add(-time.Minute),
	})

	domain := s.estateDomain(context.Background(), remoteTarget{
		target: deploymentTarget{ID: "t1", Name: "Geneva", BaseURL: down.URL},
	}, now)

	if domain.State != panorama.StateStale {
		t.Fatalf("state = %q, want %q", domain.State, panorama.StateStale)
	}
	if domain.Holds != 88 || domain.Restricted != 2 {
		t.Errorf("holds/restricted = %d/%d, want the last landscape kept",
			domain.Holds, domain.Restricted)
	}
	if domain.RuntimeID != "rt-geneva" {
		t.Errorf("runtimeId = %q, want the last identity kept", domain.RuntimeID)
	}
}

// TestAReplyThatIsNotAStarmapIsRefused. Something answered on the starmap route and it is
// not a starmap — a proxy's error page, an HTML login form, a truncated document. Counting
// whatever decoded out of it would put an invented size on the picture, which is worse
// than saying the domain could not be read.
func TestAReplyThatIsNotAStarmapIsRefused(t *testing.T) {
	for name, reply := range map[string]string{
		"an html page":      "<html><body>Please log in",
		"a truncated graph": `{"nodes":[{"id":"process:1"`,
		"a bare number":     "17",
	} {
		t.Run(name, func(t *testing.T) {
			peer := newEstatePeer(t, servingDescriptor, reply)
			s := newEstateServer()
			domain := s.estateDomain(context.Background(), remoteTarget{
				target: deploymentTarget{ID: "t1", Name: "Geneva", BaseURL: peer.server.URL},
			}, time.Now())

			if domain.State != panorama.StateUnreachable {
				t.Errorf("state = %q, want the reply refused", domain.State)
			}
			if domain.Holds != 0 {
				t.Errorf("holds = %d, want nothing counted from a reply that is not a starmap",
					domain.Holds)
			}
		})
	}
}
