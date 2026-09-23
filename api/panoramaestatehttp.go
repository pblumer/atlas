package api

import (
	"net/http"

	"github.com/pblumer/atlas/api/httpapi"
	"github.com/pblumer/atlas/api/panorama"
)

// The estate altitude's surface (ADR-0402, slice 2c): the route
// that serves L-1, one node per domain.
//
// **Who may open it, and why it is not the gate §1 wrote.** That section's chosen
// posture was an operator gate — the estate behind the right that configures deployment
// targets — on the argument that whoever configured the targets already holds the
// credentials whose reach is in question, so the view grants no reach they did not have.
// Its own open question then found the reader who most needs this view is a
// cross-departmental architect who is *not* that operator, which made the gate "likely
// insufficient for the customer who most needs this".
//
// So the reach record was built first (ADR-0410), and
// this route takes the posture that record makes available: **the same right that reads
// the landscape reads the estate, and every domain on the picture is exactly as wide as
// the credential that drew it.** Three properties make that safe rather than merely
// permissive, and each is a property of code rather than of intent:
//
//   - **The local domain is as wide as the reader.** It is counted off the landscape
//     this caller would be served — the same derivation, the same per-request
//     visibility decision, the same restricted placeholders (ADR-0211 §3). A reader who
//     may see two applications of forty counts two, and the placeholders say how many
//     they did not.
//
//   - **A peer domain is as wide as the target's credential**, which is what a
//     federated read can be and no wider (§1). It carries the credential's reach
//     rather than the reader's, and the picture says so on every domain: the credential
//     that drew it, and how much of it that credential could not see.
//
//   - **Nothing of a peer's content crosses.** A domain carries a name, a count, a
//     state and a join — never a node of somebody else's landscape. Expanding a domain
//     is a read against *that* installation, with that reader's own rights over there,
//     which is the only place they can be resolved.
//
// What this posture costs, stated rather than left to be discovered: a landscape reader
// who is not an operator now learns the names of the peer domains, roughly how large
// each is, and which of them a promotion has reached. That is configuration this server
// already shows such a reader as `target` nodes on the landscape itself (ADR-0211 §7's
// legend names them), so the estate adds the size and the join to what was already
// visible rather than opening a category that was closed.

// handlePanoramaEstate serves the estate altitude for the calling principal.
//
// Three reads in the order their costs demand: the landscape on the loop (via the mesh
// service, which owns that turn), the target list and its credentials on the loop, and
// the peers off it. A remote call inside the single writer is the one thing every other
// request on this server is waiting for this one not to do (I3).
func (s *Server) handlePanoramaEstate(w http.ResponseWriter, r *http.Request) {
	// The reader's own landscape first, because it decides what the local domain may
	// claim — and because it answers 503 rather than an empty picture when the loop is
	// closing, which is the failure mode an estate must not paper over.
	local, ok := s.panoramaMesh.Derive(w, r)
	if !ok {
		return
	}

	var (
		desc  nodeDescriptor
		peers []remoteTarget
		err   error
		ran   bool
	)
	s.do(func() {
		ran = true
		// A descriptor that cannot be read costs this domain its *name*, not the
		// picture: the identity that matters for joining keys is on the landscape
		// already (ADR-0401 §1), and nodeDescriptorName falls back to the product name.
		// So the error is deliberately not propagated — unlike the target list below,
		// which decides whether there are peers at all.
		desc, _ = s.describeNode()
		var targets []deploymentTarget
		if targets, err = s.targets.LoadAll(); err != nil {
			return
		}
		peers = make([]remoteTarget, 0, len(targets))
		for _, t := range targets {
			// Resolved here because reading the vault is a loop read. It travels no
			// further than the Authorization header the fan-out sets, and reaches no
			// payload, no log line and no error message.
			peers = append(peers, remoteTarget{
				target: t, credential: s.resolveConnectorSecret(t.CredentialRef),
			})
		}
	})
	switch {
	case !ran:
		httpapi.Error(w, http.StatusServiceUnavailable, "server is shutting down")
		return
	case err != nil:
		// Refused rather than served short. An estate missing a peer is a picture of a
		// smaller estate, and nothing on it would say so — which is the one failure
		// this altitude cannot be allowed to have.
		httpapi.Error(w, http.StatusInternalServerError, "read deployment targets: "+err.Error())
		return
	}

	// Off the loop, bounded, cached, per-target isolated: see estateDomains. The cost is
	// one landscape read per configured target per freshness window, which is operator
	// configuration and the same bound the descriptor fan-out on every landscape read
	// already has.
	httpapi.JSON(w, http.StatusOK,
		panorama.DeriveEstate(localEstateDomain(local, desc), s.estateDomains(r.Context(), peers)))
}
