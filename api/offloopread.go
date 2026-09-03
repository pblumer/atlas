package api

import (
	"errors"

	"github.com/pblumer/atlas/compiler"
	"github.com/pblumer/atlas/state"
)

// errLoopClosing reports that the run loop refused work because the server is
// shutting down. It is the [runloop.Loop.Do] "fn did not run" case given a name,
// so a read path can distinguish "no answer" from "the empty answer".
var errLoopClosing = errors.New("server is shutting down")

// defMeta is the deployment metadata a query needs, copied out of the loop-owned
// map. It is a value, not the *deployment: the loop keeps mutating those (a
// deactivation toggle, a reload), and an off-loop reader holding one would be a
// data race. The compiled process is the exception — it is immutable once
// deployed (ADR-0004) and safe to share by pointer, which is what lets a query
// resolve element ids and the version tag without going back to the loop.
type defMeta struct {
	ProcessID  string
	Name       string
	Version    int32
	DeployedAt int64
	Inactive   bool
	cp         *compiler.CompiledProcess
}

// VersionTag is the tag the definition was deployed under, or "" when it carries
// none or its model is not loaded.
func (d defMeta) VersionTag() string {
	if d.cp == nil {
		return ""
	}
	return d.cp.VersionTag()
}

// defIndex is every deployment's metadata by definition key — the loop-owned
// deployment map reduced to what a read path may look at off the loop. It is
// sized by how many definitions are *deployed*, which is design-time size: the
// copy stays cheap however many instances exist.
type defIndex map[uint64]defMeta

// readOffLoop runs a read-only query with the run loop free.
//
// This is the shape every query that can grow with the instance population must
// have. The loop is Atlas's single writer (I3), and a closure dispatched with
// [Server.do] owns it for its whole duration — so a query that scans a column
// family inside do() stops the engine for as long as the scan takes. That is not
// a theoretical cost: an operator search over a store holding ~500k instances
// held the loop long enough that every other request timed out, the engine
// included. ADR-0080 removed the scans from the runtime views by maintaining
// counters; the queries that must still walk many rows need this instead.
//
// What happens on the loop is bounded and small: take a [state.ReadView] (one
// Pebble snapshot handle) and copy the deployment metadata. What happens off it
// is the scan. Because the view is a snapshot, the query also gets a *consistent*
// read — writes landing while it runs are invisible to it, so a scan can no
// longer see half of one instance's state and half of another's.
//
// fn must not touch loop-owned server state (the deployment map, the registries,
// the processor). Everything it is allowed to read is in its two arguments. It
// must also not dispatch onto the loop while it holds the view for longer than it
// has to — a view left open holds back compaction of everything written since.
func (s *Server) readOffLoop(fn func(v *state.ReadView, defs defIndex) error) error {
	var (
		view *state.ReadView
		defs defIndex
	)
	s.do(func() {
		view = s.store.ReadView()
		defs = make(defIndex, len(s.deployments))
		for key, d := range s.deployments {
			defs[key] = defMeta{
				ProcessID:  d.ProcessID,
				Name:       d.Name,
				Version:    d.Version,
				DeployedAt: d.DeployedAt,
				Inactive:   d.inactive,
				cp:         d.cp,
			}
		}
	})
	if view == nil {
		// Do returned without running fn: the loop is closing. No view was taken,
		// so there is nothing to close and nothing to answer with.
		return errLoopClosing
	}
	defer view.Close()
	return fn(view, defs)
}
