package panorama

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"

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
	now      Clock
}

// NewMesh builds the mesh service. maxNodes is the size budget; zero is unlimited.
// overlays may be nil, in which case no desired-versus-observed comparison is made.
// now stamps each answer with the moment its landscape was read; a nil clock leaves
// the stamp off rather than inventing one.
func NewMesh(loop *runloop.Loop, collect LandscapeCollector, overlays OverlayCollector,
	maxNodes int, now Clock) *Mesh {
	return &Mesh{loop: loop, collect: collect, overlays: overlays, maxNodes: maxNodes, now: now}
}

// HandleGraph serves the whole-instance mesh for the calling principal.
func (m *Mesh) HandleGraph(w http.ResponseWriter, r *http.Request) {
	graph, ok := m.derive(w, r)
	if !ok {
		return
	}
	httpapi.JSON(w, http.StatusOK, graph)
}

// HandleNotations serves the vocabularies the landscape can be drawn in, with each
// one's mapping and what it drops (ADR-0211 §8).
//
// It is served rather than duplicated in the browser for the reason ADR-0189's
// connection subset already gives: three things read this table — the picture's
// labels, the stamp on its image export, and the ArchiMate document generated from
// it — and a copy in the view would eventually have the picture calling a node one
// thing while the file called it another.
//
// No authorization beyond the route's own: this is a description of a mapping, not
// of anybody's resources.
func (m *Mesh) HandleNotations(w http.ResponseWriter, _ *http.Request) {
	httpapi.JSON(w, http.StatusOK, Notations())
}

// HandleArchiMate serves the landscape as an ArchiMate Open Exchange document.
//
// It derives exactly what HandleGraph derives and writes that, so the file and the
// picture are the same landscape seen twice rather than two answers to one question.
// Whatever the caller may not see is missing from both, identically, and the
// document says how much of it there was.
func (m *Mesh) HandleArchiMate(w http.ResponseWriter, r *http.Request) {
	graph, ok := m.derive(w, r)
	if !ok {
		return
	}
	var at int64
	if m.now != nil {
		at = m.now().Unix()
	}
	body := ExportArchiMate(graph, ArchiMateExport{Instance: r.Host, GeneratedAt: at})
	w.Header().Set("Content-Type", "application/xml; charset=utf-8")
	// Named for the instance and the day, because the second thing anybody does with
	// these is put two of them side by side.
	w.Header().Set("Content-Disposition",
		fmt.Sprintf("attachment; filename=%q", archiMateFilename(r.Host, at)))
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(body)
}

// archiMateFilename is the download's name. The host is sanitised into it rather
// than interpolated: a Host header is caller-supplied, and it must not be able to
// put a quote or a path separator into a response header.
func archiMateFilename(host string, at int64) string {
	var b strings.Builder
	b.WriteString("atlas-landscape")
	var wrote int
	for _, r := range host {
		if wrote >= 40 {
			break
		}
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '.':
			if wrote == 0 {
				b.WriteByte('-')
			}
			b.WriteRune(r)
			wrote++
		}
	}
	if at > 0 {
		b.WriteString("-" + time.Unix(at, 0).UTC().Format("20060102"))
	}
	b.WriteString(".xml")
	return b.String()
}

// derive is the shared half of every mesh answer: collect on the loop, ask the peers
// off it, derive off it. Reports whether it produced a graph; when it did not it has
// already written the refusal.
func (m *Mesh) derive(w http.ResponseWriter, r *http.Request) (Graph, bool) {
	// Read before the loop turn rather than after the derivation, so the stamp is the
	// oldest moment any fact in this answer could have been read. A picture that
	// dates itself later than its contents is the one an export must never carry:
	// it would make a stale landscape look freshly checked.
	var observedAt int64
	if m.now != nil {
		observedAt = m.now().Unix()
	}
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
		return Graph{}, false
	}
	if err != nil {
		httpapi.Error(w, http.StatusInternalServerError, "collect landscape: "+err.Error())
		return Graph{}, false
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
	return DeriveGraph(land, Options{
		MaxNodes: m.maxNodes, Overlays: overlays, ObservedAt: observedAt,
	}), true
}
