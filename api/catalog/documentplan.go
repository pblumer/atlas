package catalog

import (
	"encoding/json"
	"maps"
	"reflect"
	"slices"

	"github.com/pblumer/atlas/api/importplan"
)

// Importing a document asks before it replaces work done here
// (ADR-0467). Every catalogue, product and process the document carries is
// compared with what this server holds and with the version the last import brought
// (its base): new, unchanged, behind (as the last import left it, and moved on in
// the document — taken), ahead (edited here, and as before in the document — kept)
// or changed (both moved, or never imported). A changed one is written only when the
// importer said so: named under ?overwrite=, or left as it is when named under
// ?keep=. One named under neither stops the import before anything is written. What
// is unchanged is not written at all, and a catalogue is published again only when
// the import changed what its release is made of, so the same document imported
// twice changes nothing the second time.

// Baselines remembers, per artifact, the hash of the version the last import of a
// document brought. Both methods run on the run loop.
type Baselines interface {
	ImportBaselines() (map[string]string, error)
	RecordImportBaselines(hashes map[string]string) error
}

// bases answers what the last import brought, empty without a store. Runs on the
// run loop.
func (s *Service) bases() (map[string]string, error) {
	if s.Baselines == nil {
		return map[string]string{}, nil
	}
	return s.Baselines.ImportBaselines()
}

// decide classes each catalogue and product of the plan and settles what is
// written. One that is unchanged or kept is put back in the plan as it is stored,
// so a release computed from the plan is the release of what the store will hold.
// Runs on the plan alone, wherever the plan is.
func (plan *importPlan) decide(d importplan.Decisions, bases map[string]string) {
	plan.write, plan.artifacts, plan.undecided, plan.incoming = map[string]bool{}, nil, nil, map[string]string{}
	note := func(subject, name string, existing bool, local, in string) bool {
		plan.incoming[subject] = in
		status := importplan.Classify(existing, local, in, bases[subject])
		outcome := d.Outcome(subject, status)
		// Offering products in the catalogue the importer named is the import's own
		// act, built on that catalogue as it is stored: nothing done here is lost.
		if subject == "catalog:"+plan.into && status != importplan.New && status != importplan.Unchanged {
			outcome = importplan.Updated
		}
		plan.artifacts = append(plan.artifacts, importplan.Artifact{Ref: subject, Name: name, Status: status, Outcome: outcome})
		if outcome == importplan.Undecided {
			plan.undecided = append(plan.undecided, subject)
		}
		plan.write[subject] = importplan.Writes(outcome)
		return plan.write[subject]
	}
	for _, id := range plan.order {
		next := plan.catalogs[id]
		prev, existing := plan.prevCatalogs[id]
		if !note("catalog:"+id, textOf(next.Texts), existing, catalogHash(prev), catalogHash(next)) && existing {
			plan.catalogs[id] = prev
		}
	}
	for _, id := range plan.items {
		next := plan.products[id]
		prev, existing := plan.prevProducts[id]
		if !note("product:"+id, textOf(next.Texts), existing, itemHash(prev), itemHash(next)) && existing {
			plan.products[id] = prev
		}
	}
}

// textOf is a record's name for a listing: German, else English, else any.
func textOf(texts map[string]string) string {
	for _, l := range []string{"de", "en"} {
		if t := texts[l]; t != "" {
			return t
		}
	}
	for _, l := range slices.Sorted(maps.Keys(texts)) {
		return texts[l]
	}
	return ""
}

// catalogHash is what a catalogue is compared by: what it says, setting aside what
// the store stamps on every write and what an import never carries — revision,
// times, owner and theme.
func catalogHash(c Catalog) string {
	c.Revision, c.CreatedAt, c.UpdatedAt, c.OwnerID, c.Theme = 0, 0, 0, "", Theme{}
	return recordHash(c)
}

// itemHash is catalogHash for a product.
func itemHash(it Item) string {
	it.Revision, it.CreatedAt, it.UpdatedAt = 0, 0, 0
	return recordHash(it)
}

// processHash is what a process a document deploys is compared by: its model.
func processHash(xml string) string { return importplan.Hash([]byte(xml)) }

// recordHash hashes a record as its JSON says it, empty and absent taken as one.
func recordHash(v any) string {
	raw, _ := json.Marshal(canonical(v))
	return importplan.Hash(raw)
}

// sameCatalog reports whether two catalogues say the same thing.
func sameCatalog(a, b Catalog) bool { return catalogHash(a) == catalogHash(b) }

// sameItem is sameCatalog for a product.
func sameItem(a, b Item) bool { return itemHash(a) == itemHash(b) }

// sameRecord compares two records as their JSON says them, with empty and absent
// taken as one: a list the store keeps as [] and a document leaves out say the
// same thing, and a document read back from an export must compare equal to the
// record it was exported from.
func sameRecord(a, b any) bool {
	return reflect.DeepEqual(canonical(a), canonical(b))
}

func canonical(v any) any {
	raw, err := json.Marshal(v)
	if err != nil {
		return v
	}
	var x any
	if json.Unmarshal(raw, &x) != nil {
		return v
	}
	return pruned(x)
}

// pruned drops what says nothing: null, false, zero, "", and lists and objects
// with nothing left in them.
func pruned(v any) any {
	switch x := v.(type) {
	case map[string]any:
		out := map[string]any{}
		for k, e := range x {
			if p := pruned(e); p != nil {
				out[k] = p
			}
		}
		if len(out) == 0 {
			return nil
		}
		return out
	case []any:
		var out []any
		for _, e := range x {
			out = append(out, pruned(e))
		}
		if len(out) == 0 {
			return nil
		}
		return out
	case string:
		if x == "" {
			return nil
		}
	case bool:
		if !x {
			return nil
		}
	case float64:
		if x == 0 {
			return nil
		}
	}
	return v
}

// toPublish names the plan's catalogues a publishing import releases: one with no
// release yet, and one the import changed — itself, or a product it offers —
// whose release would differ from its newest. A catalogue the import left as it
// was is not released again, and neither is one whose release would be the newest
// once more. Runs on the run loop.
func (s *Service) toPublish(plan importPlan, inputs []Input) (map[string]Release, error) {
	// Publish is run here as well as by the caller: the decision needs the release,
	// and the caller needs its problems; both are pure.
	out := map[string]Release{}
	for _, in := range inputs {
		rel, problems := Publish(in)
		releases, err := s.store.ReleasesOf(in.CatalogID)
		if err != nil {
			return nil, err
		}
		changed := plan.write["catalog:"+in.CatalogID]
		for _, it := range in.Items {
			changed = changed || plan.write["product:"+it.ID]
		}
		// A first release is always due. A refused one is kept, so whoever asks
		// reports why.
		if len(releases) > 0 && (!changed || len(problems) == 0 && sameRelease(newest(releases), rel)) {
			continue
		}
		out[in.CatalogID] = rel
	}
	return out, nil
}

func newest(releases []Release) Release {
	n := releases[0]
	for _, r := range releases[1:] {
		if r.CreatedAt > n.CreatedAt {
			n = r
		}
	}
	return n
}

// sameRelease compares what two releases freeze, not which release they are.
func sameRelease(a, b Release) bool {
	for _, r := range []*Release{&a, &b} {
		r.ID, r.CatalogID, r.CreatedAt = "", "", 0
		items := make([]Item, len(r.Items))
		for i, it := range r.Items {
			it.Revision, it.CreatedAt, it.UpdatedAt = 0, 0, 0
			items[i] = it
		}
		r.Items = items
	}
	return sameRecord(a, b)
}
