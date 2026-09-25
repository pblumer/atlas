package api

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"maps"
	"net/http"
	"slices"
	"sync"
	"time"

	"github.com/pblumer/atlas/api/panorama"
)

// The federated half of the estate altitude (ADR-0402, slice 2b):
// asking every peer for its own derived landscape and turning the answers into the
// domains [panorama.DeriveEstate] assembles.
//
// The assembly decides what the picture may claim; this decides what is known at all.
// It adds no new channel: the descriptor read of ADR-0189 §6 already brings the
// deadline, the bounded fan-out, the TLS client the promotion path uses, the
// response-size limit and the per-target error isolation — see panoramaremote.go —
// and this is a second read over the same discipline, keyed the same way and cached
// beside it.
//
// Two properties are worth stating because they are easy to lose and expensive to get
// back:
//
//   - **A peer is asked for its landscape only after it has said who it is.** The
//     descriptor is cheap, cached, and the only thing that can tell a version
//     boundary from a fault (§3). Asking a silent server for a quarter of a megabyte
//     spends a request on nothing; asking an older one produces a 404 that reads like
//     a fault.
//
//   - **Every answer is as wide as the credential that fetched it**, never as wide as
//     the reader (§1). So each domain carries the credential that drew it and how much
//     that credential could not see, and neither is optional: what a federated picture
//     must never do is look complete.

const (
	// estateLandscapeFreshFor is how long one peer's landscape answers for without
	// being asked again.
	//
	// The same thirty seconds as [meshCacheTTL] and for the same reason, one hop
	// further out: the view re-reads on its own floor, and a contract shorter than
	// that bounds nothing while costing every reader a fan-out. It is also what makes
	// the timestamp mean something — it says when the peer answered rather than when
	// somebody opened a tab.
	estateLandscapeFreshFor = 30 * time.Second

	// estateLandscapeKeepFor is how long a *failed* refresh keeps reporting the last
	// landscape as stale before it stops being worth repeating. Deliberately the same
	// ceiling as [remoteNodeKeepFor]: the two halves of one peer read must not
	// disagree about when history stops being useful, or a domain would be drawn from
	// a landscape older than the identity it is drawn under.
	estateLandscapeKeepFor = remoteNodeKeepFor

	// estateLocalDomainID addresses the domain the reader is standing in.
	//
	// A fixed handle rather than this runtime's id, because §2 makes the local domain a
	// node like any other and a node needs an id whether or not the runtime read
	// succeeded — an installation that cannot read its own identity still knows it is
	// where the reader is. It cannot collide with a peer: a deployment target's id is
	// minted by newID, which is sixteen hex characters.
	estateLocalDomainID = "local"
)

// remoteLandscape is what one peer's starmap read left behind: the numbers the estate
// altitude needs, and nothing of the graph itself.
//
// Keeping the counts rather than the graph is the decision that makes this cache
// affordable. A landscape at the mesh's own budget is a quarter of a megabyte
// (limits.PeerLandscape), and an estate of eight domains would hold two megabytes of
// somebody else's nodes to draw eight — while the expansion of a domain into that
// landscape is a fetch of its own, at L0, for the one domain a reader opened.
type remoteLandscape struct {
	// runtimeID is what the landscape said it was derived by. Kept beside the
	// descriptor's own id so the two can be held against each other: ADR-0401 §1 makes
	// (runtimeId, key) an entity's estate-wide name, and two different ids from one
	// target means nothing here can be placed.
	runtimeID string
	// holds is how many landscape nodes the answer accounts for, and restricted how
	// many of those were placeholders for what the credential may not see.
	holds      int
	restricted int
	observedAt time.Time
	// lastError is why the most recent attempt failed, or empty when it succeeded.
	lastError string
}

// remoteLandscapeCache is the landscape half of the peer channel.
type remoteLandscapeCache = remoteCache[remoteLandscape]

func newRemoteLandscapeCache() *remoteLandscapeCache { return newRemoteCache[remoteLandscape]() }

// estateDomains asks every configured peer for its landscape and returns one domain
// per peer, in the order the peers were given.
//
// Off the run loop, like every other peer read: the target list and each credential
// are resolved on the loop by the caller (see landscapeFacts), and nothing here
// touches a store.
//
// Per-target isolation is the same property observeRemoteNodes keeps, in the same
// shape: one slot per peer needs no lock, and a peer that is down cannot change what
// any other peer's domain says or empty the picture.
func (s *Server) estateDomains(ctx context.Context, peers []remoteTarget) []panorama.EstateDomain {
	if len(peers) == 0 {
		return nil
	}
	live := make(map[string]bool, len(peers))
	for _, p := range peers {
		live[p.target.ID] = true
	}
	// Both halves are pruned, not only the one this read writes: a target deleted
	// between two estate reads must leave nothing behind that a later picture could be
	// drawn from.
	s.remoteNodes.retain(live)
	s.remoteLandscapes.retain(live)

	domains := make([]panorama.EstateDomain, len(peers))
	var wg sync.WaitGroup
	gate := make(chan struct{}, maxRemoteNodeConcurrency)
	for i, peer := range peers {
		wg.Add(1)
		go func(i int, peer remoteTarget) {
			defer wg.Done()
			gate <- struct{}{}
			defer func() { <-gate }()
			domains[i] = s.estateDomain(ctx, peer, time.Now())
		}(i, peer)
	}
	wg.Wait()
	return domains
}

// estateDomain resolves one peer into a domain.
//
// The order of the questions is the order of the record's own states, and each answer
// closes exactly one of them:
//
//  1. did it answer at all — [remoteFacts] decides that, shared with the landscape's
//     target rows so the two views cannot come to disagree about a peer;
//  2. does it serve this view — the descriptor's feature list, which is the only thing
//     that can tell §3's version boundary from a fault;
//  3. what does its landscape say — the read below, cached beside the descriptor.
func (s *Server) estateDomain(ctx context.Context, peer remoteTarget, now time.Time) panorama.EstateDomain {
	obs := s.remoteNodeObservation(ctx, peer)
	fact, _, _ := remoteFacts(peer.target, obs, now)

	domain := panorama.EstateDomain{
		ID:        peer.target.ID,
		Name:      estateDomainName(peer.target, obs),
		RuntimeID: obs.descriptor.ID,
		State:     fact.State,
		Reason:    fact.Reason,
		// Copied rather than aliased: the target record belongs to the run loop, and a
		// map handed out here would be read off it while the loop is free to write it.
		Applications: clonedBindings(peer.target.Bindings),
		// Whose reach drew it (§1). The target names the credential exactly — ADR-0129
		// stores one per target — while naming the vault reference would put a secret's
		// name into a document, which ADR-0211 §10 keeps out of what leaves this server
		// and the landscape already refuses for a worker's CredentialsRef.
		DrawnBy: peer.target.Name,
	}

	if fact.State != panorama.StateHealthy {
		// Unreachable or stale, decided from the descriptor. A stale domain is still
		// drawn from what was last known — that is what stale means — so the counts come
		// from the cache where it still holds an answer, and stay zero where it does not.
		if cached, ok := s.remoteLandscapes.get(peer.target.ID); ok &&
			!cached.observedAt.IsZero() && cached.observedAt.After(now.Add(-estateLandscapeKeepFor)) {
			domain.Holds, domain.Restricted = cached.holds, cached.restricted
		}
		return domain
	}

	if !slices.Contains(obs.descriptor.Features, featurePanoramaMesh) {
		// §3's fifth state. Nothing is asked of this peer: its descriptor is derived from
		// the routes it mounts, so an absent feature is the peer saying the route is not
		// there — and a 404 fetched anyway would arrive as a fault.
		domain.State = panorama.StateUnserved
		domain.Reason = "This peer answered and does not serve a starmap read, " +
			"so its landscape is a version boundary rather than a fault."
		return domain
	}

	land := s.remoteLandscapeObservation(ctx, peer, now)
	switch {
	case land.lastError == "":
		domain.Holds, domain.Restricted = land.holds, land.restricted
		domain.Reason = "This peer answered and its landscape was read."
		if land.runtimeID != "" && obs.descriptor.ID != "" && land.runtimeID != obs.descriptor.ID {
			// Two identities from one target: something in front of it is answering the two
			// reads from two servers. ADR-0401 §1 says a consumer must refuse to join keys
			// it cannot place rather than assume they are local, so this refuses — no
			// runtime id, no counts, and a reason that names what happened instead of a
			// picture that averages two installations into one domain.
			return panorama.EstateDomain{
				ID: domain.ID, Name: domain.Name, Applications: domain.Applications,
				DrawnBy: domain.DrawnBy, State: panorama.StateUnreachable,
				Reason: "This peer answered the two reads as two different runtimes, " +
					"so nothing it reported can be placed.",
			}
		}
	case !land.observedAt.IsZero() && land.observedAt.After(now.Add(-estateLandscapeKeepFor)):
		// It answered before and the refresh failed: history, and it says so.
		domain.Holds, domain.Restricted = land.holds, land.restricted
		domain.State = panorama.StateStale
		domain.Reason = fmt.Sprintf(
			"This peer's landscape was last read %ds ago and the refresh failed (%s), "+
				"so this is history rather than status.",
			int(now.Sub(land.observedAt).Seconds()), land.lastError)
	default:
		// The peer is up, says who it is, serves the route — and this server could not
		// read it. Reported as unreachable rather than not-ready, because the peer is not
		// broken: nothing is known about *this domain's landscape*, which is precisely
		// what unreachable means, and not-ready is critical severity that would paint a
		// working installation red over a credential this side controls. The reason
		// carries what an operator acts on, which is usually a credential whose scope
		// does not reach the starmap (ADR-0410).
		domain.State = panorama.StateUnreachable
		domain.Reason = "This peer answered, and its landscape could not be read: " +
			land.lastError + "."
	}
	return domain
}

// remoteLandscapeObservation returns what this server currently knows about one peer's
// landscape, refreshing it when the freshness contract has run out.
//
// The same two-state discipline as remoteNodeObservation, for the same reason: a failed
// refresh keeps the last answer *and* the failure, so the caller can report history as
// history instead of either losing it or passing it off as current.
func (s *Server) remoteLandscapeObservation(ctx context.Context, peer remoteTarget,
	now time.Time) remoteLandscape {
	previous, had := s.remoteLandscapes.get(peer.target.ID)
	if had && previous.lastError == "" && now.Sub(previous.observedAt) < estateLandscapeFreshFor {
		return previous
	}

	holds, restricted, runtimeID, err := s.fetchRemoteLandscape(ctx, peer)
	if err == nil {
		fresh := remoteLandscape{
			runtimeID: runtimeID, holds: holds, restricted: restricted, observedAt: now,
		}
		s.remoteLandscapes.put(peer.target.ID, fresh)
		return fresh
	}

	failed := remoteLandscape{lastError: err.Error()}
	if had && !previous.observedAt.IsZero() {
		failed.runtimeID, failed.holds = previous.runtimeID, previous.holds
		failed.restricted, failed.observedAt = previous.restricted, previous.observedAt
	}
	s.remoteLandscapes.put(peer.target.ID, failed)
	return failed
}

// fetchRemoteLandscape performs one starmap read.
//
// It decodes into the same [panorama.Graph] the peer serves rather than into a shape of
// its own: the wire type is the contract, and a private mirror of it would drift the
// first time a field is added.
func (s *Server) fetchRemoteLandscape(ctx context.Context, peer remoteTarget) (
	holds, restricted int, runtimeID string, err error) {
	reqCtx, cancel := context.WithTimeout(ctx, remoteNodeTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(reqCtx, http.MethodGet,
		peer.target.BaseURL+"/api/v1/panorama/mesh", nil)
	if err != nil {
		return 0, 0, "", err
	}
	if peer.credential != "" {
		req.Header.Set("Authorization", "Bearer "+peer.credential)
	}
	resp, err := s.targetHTTP().Do(req)
	if err != nil {
		return 0, 0, "", fmt.Errorf("%s", remoteFailureReason(err))
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		// Which status, because the three that happen here are fixed in three different
		// places: 401 is a credential this server holds wrongly, 403 is one whose scope
		// does not reach the starmap (the landscape scope is what this read wants), and
		// 404 is a peer whose descriptor advertised a route it does not serve.
		return 0, 0, "", fmt.Errorf("answered HTTP %d", resp.StatusCode)
	}
	// PeerLandscape rather than the descriptor read's Request ceiling: a landscape at the
	// mesh's own node budget is measurably larger than an ordinary peer answer, and a
	// truncated document decodes as nonsense rather than as a limit.
	body, err := io.ReadAll(io.LimitReader(resp.Body, s.budgets().PeerLandscape))
	if err != nil {
		return 0, 0, "", fmt.Errorf("reply could not be read: %w", err)
	}
	var graph panorama.Graph
	if err := json.Unmarshal(body, &graph); err != nil {
		return 0, 0, "", fmt.Errorf("reply is not a starmap")
	}
	return estateHolds(graph), graph.Restricted, graph.RuntimeID, nil
}

// estateHolds is how many landscape nodes one answer accounts for.
//
// Not len(Nodes), and the difference is the honesty of the number. A peer over its own
// size budget collapses its landscape to applications (ADR-0211 §7) and records on each
// one how many nodes it stands for, so counting the nodes of a collapsed answer would
// report a domain of nine hundred as one of thirty-seven — inside the field whose whole
// job is to say how big a domain is (§2).
//
// It is a floor rather than an exact count on a collapsed answer: the collapse drops
// workers, decisions and catalogue nodes entirely instead of folding them into a
// parent, so they are in neither term. A floor is the right error for what this number
// is read for — a reader decides which domain to expand, and "at least nine hundred"
// and "nine hundred and forty" lead to the same decision, while "thirty-seven" leads to
// the wrong one.
func estateHolds(g panorama.Graph) int {
	holds := len(g.Nodes)
	for _, n := range g.Nodes {
		holds += n.Children
	}
	return holds
}

// localEstateDomain is the domain the reader is standing in, read off the landscape
// this server just derived for them.
//
// Drawn by the reader's own rights rather than by a credential, which is why DrawnBy
// stays empty and why the restricted count here is the reader's own: on this one domain
// the picture is filtered by who is looking, and everywhere else by what a credential
// may see.
//
// The descriptor is a parameter rather than a read, because describeNode reads the
// settings store and therefore belongs to the run loop — the same split as the
// credentials in landscapeFacts, named on the loop and used off it.
func localEstateDomain(g panorama.Graph, desc nodeDescriptor) panorama.EstateDomain {
	return panorama.EstateDomain{
		ID:   estateLocalDomainID,
		Name: nodeDescriptorName(desc),
		// From the landscape rather than from the descriptor, although both read the same
		// identity: the id has to belong to the graph the holds were counted in, or a
		// reader joining keys would be trusting two readings to have been the same one.
		RuntimeID:  g.RuntimeID,
		Holds:      estateHolds(g),
		Restricted: g.Restricted,
		State:      panorama.StateHealthy,
		Reason:     "This is the domain you are reading from.",
	}
}

// estateDomainName is what a domain is called on the picture: what the peer's operator
// named it where the peer answered, and what this operator called the target where it
// did not.
//
// A peer that never answered still gets a name, because §3 requires it to be a shape
// rather than a gap, and the only name available then is the local configuration's.
func estateDomainName(target deploymentTarget, obs remoteNodeObservation) string {
	if obs.descriptor.ID != "" {
		return nodeDescriptorName(obs.descriptor)
	}
	return target.Name
}

// clonedBindings copies a target's promotion bindings, returning nil for an empty set
// so a domain nothing was promoted to carries no join rather than an empty one.
//
// The copy is not tidiness: the target record is owned by the run loop, and a map
// handed out by reference would be read here, off the loop, while the loop is free to
// write it on the next promotion (I3).
func clonedBindings(in map[string]string) map[string]string {
	if len(in) == 0 {
		return nil
	}
	return maps.Clone(in)
}
