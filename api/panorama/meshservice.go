package panorama

import (
	"context"
	"net/http"

	"github.com/pblumer/atlas/api/httpapi"
	"github.com/pblumer/atlas/api/runloop"
)

// LandscapeCollector reads the server's resources and returns them filtered for
// this caller: every Application and Process it yields carries the CanView the
// caller's sharing scope resolves to (ADR-0071). It runs on the API run loop, so
// it may read the project and deployment registries directly and must not call
// Loop.Do recursively.
//
// The mesh takes it as a function rather than reaching for the stores itself.
// That is ADR-0147's rule — a per-area service holds a loop and takes every other
// dependency explicitly — and it is also what keeps this package free of the
// server object it would otherwise have to import.
// It also hands back the part of the landscape that cannot be read from local
// state — see [ReachOut] — or nil when there is none.
type LandscapeCollector func(r *http.Request) (Landscape, ReachOut, error)

// ReachOut completes a landscape with what only a call outside this process can
// tell it: whether the peers this server can promote to are answering.
//
// It is a closure the collector hands back rather than a second collector, and that
// shape is the whole point. Whatever it needs from the run loop — the target list,
// the credential each one presents — it captured while it was *on* the loop; what
// it does with them happens off it. A remote call must never hold the single writer
// (I3), and a landscape view that could is a landscape view that stops every other
// design-time request while somebody's peer times out.
//
// It never fails. A peer that cannot be reached is a finding about that peer, not
// an error about the landscape: one server rebooting must not blank a picture of
// four hundred nodes.
type ReachOut func(ctx context.Context, land *Landscape)

// Mesh serves Panorama's derived landscape altitude (ADR-0211). It is a separate
// service from [Service] deliberately: Service owns stored ArchiMate documents,
// Mesh owns a projection that is never stored, and conflating declared intent with
// derived fact is the thing both records exist to prevent.
// OverlayCollector supplies the Panorama models compared against the landscape,
// already filtered for this caller. Like LandscapeCollector it runs on the API run
// loop and must not call Loop.Do recursively. Optional: a nil collector leaves the
// mesh exactly as the derivation alone makes it.
type OverlayCollector func(r *http.Request) ([]Overlay, error)

type Mesh struct {
	loop     *runloop.Loop
	collect  LandscapeCollector
	overlays OverlayCollector
	maxNodes int
}

// NewMesh builds the mesh service. maxNodes is the size budget; zero is unlimited.
// overlays may be nil, in which case no desired-versus-observed comparison is made.
func NewMesh(loop *runloop.Loop, collect LandscapeCollector, overlays OverlayCollector,
	maxNodes int) *Mesh {
	return &Mesh{loop: loop, collect: collect, overlays: overlays, maxNodes: maxNodes}
}

// HandleGraph serves the whole-instance mesh for the calling principal.
func (m *Mesh) HandleGraph(w http.ResponseWriter, r *http.Request) {
	var (
		land     Landscape
		reach    ReachOut
		overlays []Overlay
		err      error
		// ran distinguishes "the collector reported nothing" from "the collector
		// never got to run". runloop.Do returns without executing anything once the
		// loop is closing, which would otherwise leave land empty and err nil — a
		// perfectly healthy-looking empty landscape. The mesh's whole claim is that
		// its picture is true, so it must not serve one it did not compute.
		ran bool
	)
	m.loop.Do(func() {
		ran = true
		if land, reach, err = m.collect(r); err != nil {
			return
		}
		// The comparison is additive: a landscape is worth showing even when the
		// models it would be compared against cannot be read, so a failure here
		// degrades the overlay rather than the mesh.
		if m.overlays != nil {
			overlays, _ = m.overlays(r)
		}
	})
	if !ran {
		httpapi.Error(w, http.StatusServiceUnavailable, "server is shutting down")
		return
	}
	if err != nil {
		httpapi.Error(w, http.StatusInternalServerError, "collect landscape: "+err.Error())
		return
	}
	// Asking the peers happens here, off the loop and before the derivation, for the
	// reason [ReachOut] gives: the loop is the single writer, and a network call is
	// the one thing that must never be done while holding it.
	if reach != nil {
		reach(r.Context(), &land)
	}
	// Derived off the loop: this is pure CPU over a snapshot the loop already
	// produced, and holding the single-writer goroutine through it would make every
	// other design-time request wait on one caller's graph.
	httpapi.JSON(w, http.StatusOK, DeriveGraph(land, Options{MaxNodes: m.maxNodes, Overlays: overlays}))
}
