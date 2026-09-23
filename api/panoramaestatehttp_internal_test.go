package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/pblumer/atlas/api/httpapi"
	"github.com/pblumer/atlas/api/panorama"
)

// The estate route (ADR-0402, slice 2c) and the access posture it
// serves under: **reach-based** rather than ADR-0402 §1's operator gate. The tests that
// matter here are therefore not about the shape of the payload but about how wide each
// domain on it is — the local one as wide as the reader, a peer as wide as the credential
// that drew it — because that is the whole argument for letting a landscape reader open
// this view at all.

// estateOf reads the route as one principal and decodes the picture a browser would get.
func estateOf(t *testing.T, s *Server, p *httpapi.Principal) panorama.Graph {
	t.Helper()
	r := httptest.NewRequest(http.MethodGet, "/api/v1/panorama/estate", nil)
	if p != nil {
		r = r.WithContext(httpapi.WithPrincipal(r.Context(), p))
	}
	w := httptest.NewRecorder()
	s.handlePanoramaEstate(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("GET estate status = %d, body = %s", w.Code, w.Body.String())
	}
	var g panorama.Graph
	if err := json.Unmarshal(w.Body.Bytes(), &g); err != nil {
		t.Fatalf("decode estate: %v (%s)", err, w.Body.String())
	}
	return g
}

func estateDomainNode(t *testing.T, g panorama.Graph, id string) panorama.Node {
	t.Helper()
	for _, n := range g.Nodes {
		if n.ID == "domain:"+id {
			return n
		}
	}
	t.Fatalf("no domain %q on the estate: %+v", id, g.Nodes)
	return panorama.Node{}
}

// TestTheEstateDrawsThisRuntimeAndEveryTarget is §2's budget over the route: one node per
// domain, this runtime first, and a join only where a promotion recorded one. An estate
// that drew its peers and omitted itself would be a picture of somebody else's estate.
func TestTheEstateDrawsThisRuntimeAndEveryTarget(t *testing.T) {
	s, done := bootAPIWithModels(t, t.TempDir(), false)
	defer done()

	peer := newEstatePeer(t, servingDescriptor, peerGraph("rt-geneva", 31, 0))
	down := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	down.Close()

	for _, tgt := range []deploymentTarget{
		{ID: "t-geneva", Name: "Geneva", BaseURL: peer.server.URL,
			Bindings: map[string]string{"app-orders": "remote-orders"}},
		{ID: "t-lugano", Name: "Lugano", BaseURL: down.URL},
	} {
		if err := s.targets.Save(tgt); err != nil {
			t.Fatalf("save target: %v", err)
		}
	}
	s.forgetLandscape()

	g := estateOf(t, s, &httpapi.Principal{UserID: "usr_a", Username: "a"})

	if len(g.Nodes) != 3 {
		t.Fatalf("the estate has %d nodes, want this runtime plus two targets: %+v", len(g.Nodes), g.Nodes)
	}
	if g.Nodes[0].ID != "domain:"+estateLocalDomainID {
		t.Errorf("the first node is %q; a reader starts in the domain they are standing in", g.Nodes[0].ID)
	}
	for _, n := range g.Nodes {
		if n.Kind != panorama.KindDomain {
			t.Errorf("node %q is a %q; the estate altitude draws domains and nothing smaller", n.ID, n.Kind)
		}
	}

	// The peer that answered reports its own name and its size; the one that did not is
	// still a shape, named by the configuration that is all there is to name it by.
	geneva := estateDomainNode(t, g, "t-geneva")
	if geneva.State != panorama.StateHealthy || geneva.Holds != 31 {
		t.Errorf("the peer that answered = %+v, want healthy and 31 nodes", geneva)
	}
	if geneva.Name != "Geneva (prod)" {
		t.Errorf("name = %q, want the name the peer gave for itself", geneva.Name)
	}
	if lugano := estateDomainNode(t, g, "t-lugano"); lugano.State != panorama.StateUnreachable {
		t.Errorf("the silent peer = %+v, want unreachable", lugano)
	}

	// One edge, and only where a promotion was recorded (§4).
	if len(g.Edges) != 1 {
		t.Fatalf("the estate has %d edges, want the one recorded promotion: %+v", len(g.Edges), g.Edges)
	}
	want := panorama.Edge{
		From: "domain:" + estateLocalDomainID, To: "domain:t-geneva",
		Kind: panorama.EdgePromotes, Promoted: 1,
	}
	if g.Edges[0] != want {
		t.Errorf("edge = %+v, want %+v", g.Edges[0], want)
	}
}

// TestTheEstateIsAsWideAsTheReaderOnTheDomainTheyStandIn is the reach-based posture's
// load-bearing property, and the reason the operator gate of ADR-0402 §1 is not what this
// route has.
//
// The local domain is counted off the landscape the caller would be served, so it inherits
// that derivation's per-request visibility decision (ADR-0071/ADR-0211 §3): an application
// the caller may not see is not on their landscape, so it is not in their count either. The
// estate therefore cannot become a way to learn the size of what somebody may not see on
// the picture one altitude down.
//
// And it does not invent a restriction where the landscape draws none. A restricted
// placeholder is minted only where something drawn *references* a hidden resource — an
// absence that would otherwise read as "this depends on nothing" — while an application
// nothing on the picture points at is simply not drawn. So both readers here report zero
// restricted, and the difference is in the count, which is the honest place for it.
func TestTheEstateIsAsWideAsTheReaderOnTheDomainTheyStandIn(t *testing.T) {
	s, done := bootAPIWithModels(t, t.TempDir(), false)
	defer done()
	// With auth off every caller is an owner, which is the one setting under which this
	// test could not fail.
	s.authEnabled = true

	owner := &httpapi.Principal{UserID: "usr_owner", Username: "owner"}
	other := &httpapi.Principal{UserID: "usr_other", Username: "other"}
	if err := s.projects.Save(project{ID: "app1", Name: "Billing", OwnerID: owner.UserID}); err != nil {
		t.Fatalf("save project: %v", err)
	}
	s.forgetLandscape()

	mine := estateDomainNode(t, estateOf(t, s, owner), estateLocalDomainID)
	theirs := estateDomainNode(t, estateOf(t, s, other), estateLocalDomainID)

	if mine.Holds != theirs.Holds+1 {
		t.Errorf("the two readers count %d and %d nodes; the owner's landscape holds exactly "+
			"one application more, so the domain they stand in has to as well",
			mine.Holds, theirs.Holds)
	}
	if theirs.Holds < 1 {
		t.Errorf("the reader who may see nothing of it counts %d nodes; a domain with no size "+
			"would be the estate hiding that they are standing in one", theirs.Holds)
	}
	if mine.Restricted != 0 || theirs.Restricted != 0 {
		t.Errorf("restricted = %d and %d; nothing drawn on either landscape points at the "+
			"hidden application, so neither may claim a placeholder stood in for it",
			mine.Restricted, theirs.Restricted)
	}
	// And the reader's own domain names no credential, because it was drawn by the
	// reader's own rights rather than by one (§1).
	if mine.DrawnBy != "" {
		t.Errorf("drawnBy = %q on the domain the reader is standing in", mine.DrawnBy)
	}
}

// TestAPeerDomainSaysWhichCredentialDrewIt is the other half of the posture. A peer's
// answer is as wide as the credential this server presented, never as wide as the reader,
// so the picture has to say which credential that was — an incompleteness that is stated
// is a fact, one that is not is a discovery.
func TestAPeerDomainSaysWhichCredentialDrewIt(t *testing.T) {
	s, done := bootAPIWithModels(t, t.TempDir(), false)
	defer done()

	// A peer whose answer carries placeholders: the credential reached part of it.
	peer := newEstatePeer(t, servingDescriptor, peerGraph("rt-geneva", 40, 12))
	if err := s.targets.Save(deploymentTarget{
		ID: "t-geneva", Name: "Geneva prod", BaseURL: peer.server.URL,
		CredentialRef: "vault://peer/geneva",
	}); err != nil {
		t.Fatalf("save target: %v", err)
	}
	s.forgetLandscape()

	g := estateOf(t, s, &httpapi.Principal{UserID: "usr_a", Username: "a"})
	geneva := estateDomainNode(t, g, "t-geneva")
	if geneva.DrawnBy != "Geneva prod" {
		t.Errorf("drawnBy = %q, want the target whose credential made the read", geneva.DrawnBy)
	}
	if geneva.Restricted != 12 {
		t.Errorf("restricted = %d, want the 12 the credential could not see", geneva.Restricted)
	}
	// And the vault reference reaches no payload (ADR-0211 §10).
	body, err := json.Marshal(g)
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{"vault://", peer.server.URL} {
		if bytes.Contains(body, []byte(forbidden)) {
			t.Errorf("the estate payload carries %q", forbidden)
		}
	}
}
