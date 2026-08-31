package panorama

// Resolving a binding turns the opaque id a model carries into something a person
// can read, without the model ever storing it.
//
// ADR-0189 §4 keeps names, health and versions out of the document on purpose: a
// drawing that stored them would be a second copy of the truth, and a stale one.
// So the id is what travels, and the server supplies the rest at read time —
// filtered by what this caller may see, because resolution must not become a way
// to read the name of a resource whose sharing scope withholds it.

// Binding resolution statuses.
const (
	// StatusResolved: the resource exists and this caller may see it.
	StatusResolved = "resolved"
	// StatusForbidden: the resource exists, but outside this caller's access. Kept
	// distinct from missing on purpose — "you may not see it" and "nothing here has
	// that id" are fixed in different places, and one is a sharing decision while
	// the other is a typo or a deleted resource. Collapsing them would send
	// somebody to correct a model that is already correct.
	//
	// The disclosure this makes is that the id resolves to something. That id is
	// already in the caller's own model, which they can export; what the scope
	// withholds is the name, and the name is what stays out.
	StatusForbidden = "forbidden"
	// StatusMissing: nothing on this server has that id.
	StatusMissing = "missing"
)

// ResourceRef is one Atlas resource as the resolver sees it. CanView is decided by
// the server against the caller's sharing scope before resolution runs; this
// package honors that decision and never makes one.
type ResourceRef struct {
	ID      string
	Name    string
	CanView bool
}

// Catalog is what the server supplies to resolve a document's bindings: one lookup
// per binding kind, already filtered for the caller. Each key resolves against its
// own map — looking a process id up among applications would report every binding
// as missing, which reads as a broken model rather than as a broken resolver.
type Catalog struct {
	Applications map[string]ResourceRef
	Processes    map[string]ResourceRef
	Connectors   map[string]ResourceRef
	JobTypes     map[string]ResourceRef
	Runtimes     map[string]ResourceRef
	Targets      map[string]ResourceRef
	Releases     map[string]ResourceRef
}

func (c Catalog) forKey(key string) map[string]ResourceRef {
	switch key {
	case KeyApplicationID:
		return c.Applications
	case KeyProcessID:
		return c.Processes
	case KeyConnectorID:
		return c.Connectors
	case KeyJobType:
		return c.JobTypes
	case KeyRuntimeID:
		return c.Runtimes
	case KeyDeploymentTargetID:
		return c.Targets
	case KeyReleaseID:
		return c.Releases
	}
	return nil
}

// ResolvedValue is one bound id with what the server could say about it. Name is
// present only when the caller may see the resource.
type ResolvedValue struct {
	Value  string `json:"value"`
	Status string `json:"status"`
	Name   string `json:"name,omitempty"`
}

// ResolvedBinding is one key on one element, with every value it binds.
type ResolvedBinding struct {
	ElementID   string          `json:"elementId"`
	ElementType string          `json:"elementType"`
	Key         string          `json:"key"`
	Values      []ResolvedValue `json:"values"`
}

// Resolution is what a caller asking "what is bound in this model" gets.
type Resolution struct {
	ContractVersion int               `json:"contractVersion"`
	Bindings        []ResolvedBinding `json:"bindings"`
	// Unresolved counts values that are forbidden or missing. It is the number a
	// listing shows as a badge: a model whose bindings no longer resolve is drifting
	// from the instance it describes, and that is worth seeing without opening it.
	Unresolved int `json:"unresolved"`
	// Problems are the extractor's, carried through: a caller needs to hear about
	// declarations that were refused as much as about the ones that stood.
	Problems []Problem `json:"problems"`
}

// ResolveBindings resolves every value in a set against the catalog.
//
// It never drops a value. ADR-0189 §4 requires a missing or inaccessible resource
// to stay an explicit unresolved binding: removing it would make a broken binding
// look like an absent one, and the model would then look correct.
func ResolveBindings(set BindingSet, catalog Catalog) Resolution {
	out := Resolution{
		ContractVersion: set.ContractVersion,
		Bindings:        []ResolvedBinding{},
		Problems:        []Problem{},
	}
	if out.ContractVersion == 0 {
		out.ContractVersion = BindingContractVersion
	}
	out.Problems = append(out.Problems, set.Problems...)

	for _, binding := range set.Bindings {
		lookup := catalog.forKey(binding.Key)
		resolved := ResolvedBinding{
			ElementID: binding.ElementID, ElementType: binding.ElementType,
			Key: binding.Key, Values: []ResolvedValue{},
		}
		for _, value := range binding.Values {
			entry := ResolvedValue{Value: value, Status: StatusMissing}
			if ref, found := lookup[value]; found {
				if ref.CanView {
					entry.Status, entry.Name = StatusResolved, ref.Name
				} else {
					// Deliberately no name: that is precisely what the scope withholds.
					entry.Status = StatusForbidden
				}
			}
			if entry.Status != StatusResolved {
				out.Unresolved++
			}
			resolved.Values = append(resolved.Values, entry)
		}
		out.Bindings = append(out.Bindings, resolved)
	}
	return out
}
