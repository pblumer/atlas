package jobtype

import (
	"sort"

	"github.com/pblumer/atlas/api/sidecar"
	"github.com/pblumer/atlas/compiler"
)

// Detecting a store the reserved range grew over.
//
// Dynamic indices are issued from one past the reserved range, and that range grows
// whenever a built-in connector is added. A store written before such an addition can
// therefore hold a model-authored type on an index that now belongs to a built-in.
// Loading it cannot simply keep the record — the index means the built-in now, and
// two names on one index is the corruption to avoid — so [Registry.remember] drops
// it. Dropping it *silently* is the actual problem: jobs already on disk carry the
// old index, so they would be read as the built-in's, while new jobs of the same
// model-authored type get a fresh index higher up.
//
// So the drop is reported rather than hidden. What to do about an affected store is a
// separate decision; this is the measurement it needs, and it is cheap enough to run
// at every startup.

// Collision is a stored assignment whose index the reserved range has since taken.
type Collision struct {
	// Name is the model-authored job type that was issued this index.
	Name string `json:"name"`
	// Index is the index it holds on disk, and that its jobs still carry.
	Index int32 `json:"index"`
	// NowMeans is the built-in job type that index stands for in this build.
	NowMeans string `json:"nowMeans"`
}

// Collisions reports the stored assignments in dir whose index is now reserved,
// without opening a full registry. A directory that was never written is clean, not
// an error: a fresh server has no table yet.
func Collisions(dir string) ([]Collision, error) {
	store, err := sidecar.NewStore(dir, "job types", func(e Entry) string { return e.Name })
	if err != nil {
		return nil, err
	}
	entries, err := store.LoadAll()
	if err != nil {
		return nil, err
	}
	return collisionsIn(entries), nil
}

// collisionsIn is the shared test, so a startup load and an offline check cannot
// disagree about what counts as affected.
func collisionsIn(entries []Entry) []Collision {
	reserved := compiler.ReservedJobTypes()
	var out []Collision
	for _, e := range entries {
		if int(e.Index) < 0 || int(e.Index) >= len(reserved) {
			continue // above the reserved range: an ordinary dynamic assignment
		}
		out = append(out, Collision{Name: e.Name, Index: e.Index, NowMeans: reserved[e.Index]})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Index != out[j].Index {
			return out[i].Index < out[j].Index
		}
		return out[i].Name < out[j].Name
	})
	return out
}

// Dropped reports the assignments this registry could not load because the reserved
// range had grown over their indices. Empty for every healthy store.
//
// A caller is expected to surface it — a server logs it at startup. The registry
// itself does not refuse, because refusing would take an instance down over a
// condition whose repair has not been decided yet, and a report that reaches an
// operator is what that decision needs.
func (r *Registry) Dropped() []Collision { return r.dropped }
