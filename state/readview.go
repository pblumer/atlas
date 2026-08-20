package state

import (
	"github.com/cockroachdb/pebble"

	"github.com/pblumer/atlas/model"
)

// Reader is the read surface a job handler needs: the element instance its job
// sits on, and the variables in scope there.
//
// It exists so a handler can be given a *consistent* view instead of the live
// store. An in-process handler used to run on the single-writer goroutine, where
// its several reads could not interleave with a write; moving it off that
// goroutine (ADR-0149 option 3, ADR-0157 step 6) takes that guarantee away, and a
// [ReadView] gives it back. Both *Store and *ReadView satisfy it, so a handler
// neither knows nor needs to know which it holds.
type Reader interface {
	GetElementInstance(key uint64) (*model.ElementInstanceValue, bool, error)
	VariablesOfScope(scope uint64, fn func(v *model.VariableValue) error) error
}

// ReadView is a consistent read-only view of the store as of the moment it was
// taken. Later writes are invisible to it, so a handler reading an element
// instance and then its variables sees one coherent state rather than two halves
// of different ones.
//
// It holds resources in the store's engine, so it must be closed. Take one on the
// run loop (the store's owner), use it off the loop, close it when the work is
// done — the same lifetime as the job it was taken for.
type ReadView struct {
	snap *pebble.Snapshot
}

// ReadView takes a consistent read-only view. The caller owns it and must Close it.
//
// This is deliberately not called Snapshot: [Store.Snapshot] already means the
// on-disk backup checkpoint (ADR-0107), and conflating a durable copy of the whole
// store with an in-memory read view would be a genuinely dangerous ambiguity.
func (s *Store) ReadView() *ReadView { return &ReadView{snap: s.db.NewSnapshot()} }

// Close releases the view. A view left open holds back the compaction of
// everything written since it was taken, so it must not outlive the work it was
// taken for.
func (v *ReadView) Close() error { return v.snap.Close() }

// GetElementInstance implements [Reader] against the view.
func (v *ReadView) GetElementInstance(key uint64) (*model.ElementInstanceValue, bool, error) {
	return decodeElementInstance(getCopy(v.snap, keyElementInstance(key)))
}

// VariablesOfScope implements [Reader] against the view.
func (v *ReadView) VariablesOfScope(scope uint64, fn func(v *model.VariableValue) error) error {
	return scanPrefixWith(v.snap, variablePrefix(scope), func(_, raw []byte) error {
		return decodeVariable(raw, fn)
	})
}
