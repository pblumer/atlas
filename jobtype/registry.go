// Package jobtype carries Atlas's engine-wide job-type table: the one mapping
// from a job type's name to the integer index a job on disk carries.
//
// It exists because the compiler interns strings *per compiled process*
// (ADR-0004), while the activatable-job index is engine-wide. Two definitions
// therefore disagree about what a given index means — measured on the tree that
// found this, `send-email` in one process and `charge-card` in another were both
// index 16 — so a worker subscribing by job type would be handed the wrong work.
// ADR-0007 names this table as the correctness prerequisite for the type-keyed
// pull, and ADR-0157 as step 1 of moving side-effecting work onto workers.
//
// The table has two halves. The **reserved** half is the built-in job types
// (DMN, user task, the connector kinds, the script languages): their indices are
// compile-time constants that every builder reserves in order, so the registry
// seeds itself from [compiler.ReservedJobTypes] and never persists them — a
// stored copy could only drift from the constants. The **dynamic** half is the
// model-authored types (a `<zeebe:taskDefinition type>`), assigned from
// [compiler.FirstDynamicJobTypeIndex] upward and persisted, because nothing in
// the code remembers them.
//
// An index, once issued, is permanent: jobs already on disk carry it, so the
// registry never recycles one, not even after a record is removed by hand.
//
// Like the design-time stores it sits beside, a Registry does no locking of its
// own — it is owned by the server's run-loop goroutine, the single writer of
// deploy-time state (invariant I3).
package jobtype

import (
	"fmt"

	"github.com/pblumer/atlas/api/sidecar"
	"github.com/pblumer/atlas/compiler"
)

// Entry is one persisted assignment: a model-authored job type and the
// engine-wide index it was given. Reserved types have no Entry.
type Entry struct {
	Name  string `json:"name"`
	Index int32  `json:"index"`
}

// Registry is the engine-wide job-type table. Build one with [NewRegistry].
type Registry struct {
	store   *sidecar.Store[Entry]
	byName  map[string]int32
	byIndex map[int32]string
	// next is one past the highest index ever issued, so an index is never
	// recycled even if the record that held it is gone.
	next int32
	// dropped are the stored assignments the reserved range had grown over by the
	// time this table was loaded. See collision.go.
	dropped []Collision
}

// NewRegistry opens (creating if needed) the directory backing the table, seeds
// the reserved half from the compiler's constants, and loads whatever dynamic
// assignments are already on disk.
func NewRegistry(dir string) (*Registry, error) {
	store, err := sidecar.NewStore(dir, "job types", func(e Entry) string { return e.Name })
	if err != nil {
		return nil, err
	}
	r := &Registry{
		store:   store,
		byName:  map[string]int32{},
		byIndex: map[int32]string{},
		next:    compiler.FirstDynamicJobTypeIndex(),
	}
	for i, name := range compiler.ReservedJobTypes() {
		r.byName[name] = int32(i)
		r.byIndex[int32(i)] = name
	}
	entries, err := store.LoadAll()
	if err != nil {
		return nil, err
	}
	for _, e := range entries {
		r.remember(e)
	}
	// What the load had to discard, kept so a caller can say so. Silence here is
	// exactly what made a grown reserved range invisible.
	r.dropped = collisionsIn(entries)
	return r, nil
}

// remember installs one assignment and keeps next past it.
//
// The reserved half of the table is defined by the compiler's constants, and a
// record read off disk is not allowed to disturb it in either direction: not a
// reserved *name* re-pointed at another index, and not a reserved *index*
// re-labelled with another name. Either would silently change what every job
// already written under that index means, and neither can be produced by
// [Registry.Intern] — only by a corrupted or hand-edited directory.
func (r *Registry) remember(e Entry) {
	if idx, known := r.byName[e.Name]; known && idx < compiler.FirstDynamicJobTypeIndex() {
		return // a reserved name: the constant decides its index, not this record
	}
	if e.Index < compiler.FirstDynamicJobTypeIndex() {
		return // a reserved index: it already stands for a built-in job type
	}
	r.byName[e.Name] = e.Index
	r.byIndex[e.Index] = e.Name
	if e.Index >= r.next {
		r.next = e.Index + 1
	}
}

// Intern returns the engine-wide index for a job type, assigning and persisting a
// new one the first time a name is seen. It is idempotent — the same name always
// comes back with the same index — which is what lets every deploy and every
// reload resolve their processes through it.
//
// Its signature is the resolution seam [compiler.CompiledProcess.ResolveJobTypes]
// takes, so it can be passed there directly.
func (r *Registry) Intern(name string) (int32, error) {
	if name == "" {
		return 0, fmt.Errorf("job types: cannot intern an empty job type")
	}
	if idx, ok := r.byName[name]; ok {
		return idx, nil
	}
	e := Entry{Name: name, Index: r.next}
	if err := r.store.Save(e); err != nil {
		return 0, err
	}
	r.remember(e)
	return e.Index, nil
}

// Index reports the engine-wide index a job type already holds, without assigning
// one. Use it to read the table; use [Registry.Intern] to extend it.
func (r *Registry) Index(name string) (int32, bool) {
	idx, ok := r.byName[name]
	return idx, ok
}

// Name is the reverse direction: the job type an index stands for, which is what
// turns the number on a job record back into something an operator can read.
func (r *Registry) Name(index int32) (string, bool) {
	name, ok := r.byIndex[index]
	return name, ok
}

// All returns every job type the engine knows, reserved and dynamic alike, in
// index order — the whole table, which is what the Workers view lists so an
// operator sees the kinds nobody is serving as well as the ones being worked.
func (r *Registry) All() []Entry {
	all := make([]Entry, 0, len(r.byIndex))
	for i := int32(0); i < r.next; i++ {
		if name, ok := r.byIndex[i]; ok {
			all = append(all, Entry{Name: name, Index: i})
		}
	}
	return all
}
