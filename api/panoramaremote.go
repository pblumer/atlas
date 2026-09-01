package api

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/pblumer/atlas/api/panorama"
)

// Remote observations (ADR-0189 §6, P4c): asking a peer Atlas who it is and
// whether it is answering.
//
// This is the other end of the descriptor P4a built. Every constraint the record
// puts on a remote read is here rather than in prose: the calls happen off the API
// run loop, with a deadline, a response-size limit, bounded concurrency, TLS
// verified by the same client the promotion path uses, and per-target error
// isolation — a peer that is down says nothing about the others and never empties
// the view.
//
// It is also what finally makes two of ADR-0189 §6's states producible. Everything
// before this was read from local state while the request was being served, so
// nothing could be out of date and nothing could fail to be contacted. A source
// outside the process can do both, and the difference between them matters:
//
//   - unreachable — nothing is known. The peer could not be contacted and this
//     server has never had an answer from it, or the answer it had is worthless.
//   - stale — something is known and may be wrong. There *was* an answer, its
//     freshness contract has run out, and the refresh failed. An operator reading
//     a stale row is reading history, and has to be told that rather than shown a
//     healthy peer that stopped existing an hour ago.
//
// Collapsing those two into one "unavailable" is the failure this slice exists to
// avoid, and it is why the cache below keeps a failed refresh and a never-answered
// target apart.

const (
	// remoteNodeTimeout bounds one peer descriptor read. It is a display refresh —
	// the same reasoning as targetStatusTimeout, and deliberately the same order:
	// a slow peer must not hold an architecture view open.
	remoteNodeTimeout = 8 * time.Second

	// maxRemoteNodeBytes bounds what a peer can make this server read. A descriptor
	// is a few hundred bytes; anything approaching this is either not a descriptor
	// or not friendly, and either way it is not worth buffering.
	maxRemoteNodeBytes = 64 << 10

	// maxRemoteNodeConcurrency bounds how many peers are asked at once. Unbounded
	// fan-out turns one architecture view into a burst of connections proportional
	// to how many targets an operator has configured, which is a load this server
	// would be inflicting on itself and on everybody else.
	maxRemoteNodeConcurrency = 4

	// remoteNodeFreshFor is the freshness contract: how long an answer from a peer
	// is served without asking again. It is short enough that a peer going down is
	// noticed within a view or two, and long enough that opening three models in a
	// row does not mean three rounds of calls to every target.
	remoteNodeFreshFor = 30 * time.Second

	// remoteNodeKeepFor is how long a *failed* refresh keeps reporting the last
	// answer as stale before giving up on it entirely. Past this the answer is old
	// enough that "this is what it looked like" stops being useful and unreachable
	// is the more honest reading.
	remoteNodeKeepFor = 15 * time.Minute
)

// remoteNodeObservation is one peer's last answer and when it was obtained.
type remoteNodeObservation struct {
	// descriptor is the peer's own account of itself. Empty ID means this server
	// has never had a successful answer from that target.
	descriptor nodeDescriptor
	observedAt time.Time
	// lastError is why the most recent attempt failed, or empty when it succeeded.
	lastError string
}

// remoteNodeCache holds the last answer per target.
//
// It carries its own mutex rather than living on the run loop, because that is the
// point: these entries are written by goroutines waiting on the network, and
// putting them behind the single writer would mean holding it for the duration of
// a remote call — which is the one thing every request on this server is waiting
// for it not to do (I3).
//
// It is bounded by the number of configured deployment targets, which is operator
// configuration rather than user input, and entries for targets that no longer
// exist are dropped on each collection.
type remoteNodeCache struct {
	mu sync.Mutex
	by map[string]remoteNodeObservation
}

func newRemoteNodeCache() *remoteNodeCache {
	return &remoteNodeCache{by: map[string]remoteNodeObservation{}}
}

// get returns the cached observation for a target.
func (c *remoteNodeCache) get(targetID string) (remoteNodeObservation, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	obs, ok := c.by[targetID]
	return obs, ok
}

// put records an observation.
func (c *remoteNodeCache) put(targetID string, obs remoteNodeObservation) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.by[targetID] = obs
}

// retain drops entries for targets that are no longer configured, so deleting a
// target actually forgets it rather than leaving an answer nobody can trace to a
// row on screen.
func (c *remoteNodeCache) retain(live map[string]bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for id := range c.by {
		if !live[id] {
			delete(c.by, id)
		}
	}
}

// remoteTarget is one peer to ask, with the credential to present. It is built on
// the run loop, because resolving a credential reads the vault; the asking happens
// off it.
type remoteTarget struct {
	target     deploymentTarget
	credential string
}

// observeRemoteNodes asks every configured peer for its node descriptor and folds
// the answers into the observation facts.
//
// Off the run loop. Every failure is recorded against its own target: one peer
// being down must not remove a healthy peer's row, and must never fail the whole
// document — an architecture view that goes blank because one server is rebooting
// is worse than no view.
func (s *Server) observeRemoteNodes(ctx context.Context, peers []remoteTarget, facts panorama.Facts) {
	live := make(map[string]bool, len(peers))
	for _, p := range peers {
		live[p.target.ID] = true
	}
	s.remoteNodes.retain(live)

	var (
		mu   sync.Mutex
		wg   sync.WaitGroup
		gate = make(chan struct{}, maxRemoteNodeConcurrency)
	)
	for _, peer := range peers {
		wg.Add(1)
		go func(peer remoteTarget) {
			defer wg.Done()
			gate <- struct{}{}
			defer func() { <-gate }()

			obs := s.remoteNodeObservation(ctx, peer)
			targetFact, runtimeID, runtimeFact := remoteFacts(peer.target, obs, time.Now())

			mu.Lock()
			defer mu.Unlock()
			facts.Targets[peer.target.ID] = targetFact
			// A peer that answered also told this server its runtime id, so a model
			// binding *that* node now resolves too — which is the whole reason the
			// descriptor is a stable id rather than a hostname.
			if runtimeID != "" {
				facts.Runtimes[runtimeID] = runtimeFact
			}
		}(peer)
	}
	wg.Wait()
}

// remoteNodeObservation returns what this server currently knows about one peer,
// refreshing it when the freshness contract has run out.
//
// A cached answer inside its contract is served without a call. That is not only a
// cost decision: it is what makes the timestamp on the observation mean something,
// because it says when the peer actually answered rather than when somebody
// happened to open a view.
func (s *Server) remoteNodeObservation(ctx context.Context, peer remoteTarget) remoteNodeObservation {
	now := time.Now()
	previous, had := s.remoteNodes.get(peer.target.ID)
	if had && previous.lastError == "" && now.Sub(previous.observedAt) < remoteNodeFreshFor {
		return previous
	}

	descriptor, err := s.fetchRemoteNode(ctx, peer)
	if err == nil {
		fresh := remoteNodeObservation{descriptor: descriptor, observedAt: now}
		s.remoteNodes.put(peer.target.ID, fresh)
		return fresh
	}

	// The refresh failed. Keep the last good answer if there is one, so the caller
	// can report it as stale rather than as nothing — but keep the failure too, so
	// nobody mistakes it for a fresh success.
	failed := remoteNodeObservation{lastError: err.Error()}
	if had && previous.descriptor.ID != "" {
		failed.descriptor, failed.observedAt = previous.descriptor, previous.observedAt
	}
	s.remoteNodes.put(peer.target.ID, failed)
	return failed
}

// fetchRemoteNode performs one descriptor read.
func (s *Server) fetchRemoteNode(ctx context.Context, peer remoteTarget) (nodeDescriptor, error) {
	reqCtx, cancel := context.WithTimeout(ctx, remoteNodeTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, peer.target.BaseURL+"/api/v1/node", nil)
	if err != nil {
		return nodeDescriptor{}, err
	}
	if peer.credential != "" {
		req.Header.Set("Authorization", "Bearer "+peer.credential)
	}
	resp, err := s.targetHTTP().Do(req)
	if err != nil {
		return nodeDescriptor{}, errors.New(remoteFailureReason(err))
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		// A credential minted for deployment cannot read the descriptor — the
		// status scope (ADR-0189 §6) is what a peer read wants — so saying which
		// status came back is the difference between "grant it status" and "the
		// server is down".
		return nodeDescriptor{}, fmt.Errorf("answered HTTP %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxRemoteNodeBytes))
	if err != nil {
		return nodeDescriptor{}, fmt.Errorf("reply could not be read: %w", err)
	}
	var descriptor nodeDescriptor
	if err := json.Unmarshal(body, &descriptor); err != nil {
		return nodeDescriptor{}, fmt.Errorf("reply is not a node descriptor")
	}
	if descriptor.ID == "" {
		// Something answered, and it is not an Atlas node — or it is one whose
		// identity could not be read, which its own route refuses to serve. Either
		// way there is nothing to correlate against.
		return nodeDescriptor{}, fmt.Errorf("reply carries no runtime id")
	}
	return descriptor, nil
}

// remoteFacts turns one peer's observation into the target's fact and, when the
// peer identified itself, a fact for its runtime as well.
//
// The three answers this produces are the point of the whole slice:
//
//   - healthy — the peer answered, and says who it is;
//   - stale — it answered before, its freshness contract has run out, and the
//     refresh failed. What is shown is history, and it says so;
//   - unreachable — nothing is known, either because it never answered or because
//     what it last said is too old to be worth repeating.
func remoteFacts(target deploymentTarget, obs remoteNodeObservation, now time.Time) (
	targetFact panorama.Fact, runtimeID string, runtimeFact panorama.Fact) {
	detail := map[string]string{"target": target.Name}

	if obs.lastError == "" && obs.descriptor.ID != "" {
		detail["node"] = nodeDescriptorName(obs.descriptor)
		detail["version"] = obs.descriptor.Version
		detail["runtimeId"] = obs.descriptor.ID
		fact := panorama.Fact{
			Source: panorama.SourceRemote, State: panorama.StateHealthy,
			Reason: "This peer answered and identified itself.", Detail: detail,
		}
		return fact, obs.descriptor.ID, panorama.Fact{
			Source: panorama.SourceRemote, State: panorama.StateHealthy,
			Reason: "Reached through the deployment target " + target.Name + ".",
			Detail: map[string]string{
				"node": nodeDescriptorName(obs.descriptor), "version": obs.descriptor.Version,
			},
		}
	}

	age := now.Sub(obs.observedAt)
	if obs.descriptor.ID != "" && age < remoteNodeKeepFor {
		detail["node"] = nodeDescriptorName(obs.descriptor)
		detail["lastSeenSecondsAgo"] = fmt.Sprintf("%d", int(age.Seconds()))
		fact := panorama.Fact{
			Source: panorama.SourceRemote, State: panorama.StateStale,
			Reason: fmt.Sprintf(
				"Last answered %ds ago and the refresh failed (%s), so this is history rather than status.",
				int(age.Seconds()), obs.lastError),
			Detail: detail,
		}
		// The runtime is reported stale too, from the same evidence: a model bound
		// to that node must not read as healthy because this server once saw it.
		return fact, obs.descriptor.ID, panorama.Fact{
			Source: panorama.SourceRemote, State: panorama.StateStale,
			Reason: fmt.Sprintf("Last seen %ds ago through %s; the refresh failed.",
				int(age.Seconds()), target.Name),
			Detail: map[string]string{"node": nodeDescriptorName(obs.descriptor)},
		}
	}

	return panorama.Fact{
		Source: panorama.SourceRemote, State: panorama.StateUnreachable,
		Reason: "This peer " + obs.lastError + ".", Detail: detail,
	}, "", panorama.Fact{}
}

// nodeDescriptorName is how a peer is named on screen: what its operator called
// it, qualified by its environment, falling back to the product name. Never its
// base URL — that is this server's configuration of where the peer lives, and a
// document a modeler opens is not where it belongs.
func nodeDescriptorName(d nodeDescriptor) string {
	name := d.Name
	if name == "" {
		name = "Atlas"
	}
	if d.Environment != "" {
		return name + " (" + d.Environment + ")"
	}
	return name
}

// remoteFailureReason turns a transport error into something safe to put in a
// document a modeler opens.
//
// It exists because Go's transport errors carry the address they dialled — a
// *url.Error wraps the whole URL, and the net errors under it carry the resolved
// host and port. That address is this operator's own infrastructure map, and a
// Panorama document is opened by anyone with modeler access; ADR-0211 §10 keeps
// exactly this out of what leaves the server, and the landscape mesh already
// refuses to carry a worker's endpoint for the same reason.
//
// So what survives is the *category* of failure, which is what an operator acts on
// anyway: a refused connection, a name that does not resolve and a certificate
// that does not verify are fixed in three different places, and none of them needs
// the address repeated back — it is in the target's own configuration, one screen
// away, for the people entitled to see it.
func remoteFailureReason(err error) string {
	var urlErr *url.Error
	if errors.As(err, &urlErr) && urlErr.Timeout() {
		return fmt.Sprintf("did not answer within %s", remoteNodeTimeout)
	}
	var dnsErr *net.DNSError
	switch {
	case errors.As(err, &dnsErr):
		return "has a name that could not be resolved"
	case isTLSFailure(err):
		return "presented a certificate this server does not trust"
	case errors.Is(err, context.Canceled):
		return "was not asked: the request was cancelled"
	case strings.Contains(err.Error(), "connection refused"):
		return "refused the connection"
	}
	return "could not be contacted"
}

// isTLSFailure reports whether a transport error is a certificate problem. It
// matches on the wrapped error's text as well as its type because the standard
// library returns several unexported shapes here, and misreporting a TLS failure
// as a generic one sends an operator to check the network when the answer is a
// missing CA — see WithTargetTLSRoots, which is how they would fix it.
func isTLSFailure(err error) bool {
	var certErr *tls.CertificateVerificationError
	if errors.As(err, &certErr) {
		return true
	}
	text := err.Error()
	return strings.Contains(text, "x509:") || strings.Contains(text, "tls:")
}
