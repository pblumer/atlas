package catalog

import (
	"net/http"
	"sort"

	"github.com/pblumer/atlas/api/httpapi"
)

// What the portal is serving, against what the catalogue now says.
//
// The two are deliberately different things. The portal reads a **release** — a
// frozen copy of the catalogue's offering, taken when somebody published, so that
// an order placed against it cannot change under the person placing it. Everything
// a maintainer edits goes to the live records beside it, and none of it reaches
// anybody until the next publish.
//
// That separation is right and it has one cost, which this exists to pay. Take a
// product out of a catalogue and it leaves your screen at once; the portal goes on
// offering it, from the release, with nothing anywhere saying so. The screen listed
// the releases by date and left the reader to work out whether today's catalogue is
// one of them — which is not a question a date answers.
//
// So the answer is computed rather than guessed at: what would change for the
// people ordering if this were published now.

// Unpublished is the difference between the newest release and the catalogue as it
// stands.
//
// Empty lists with Released set means the two agree — the portal is serving this
// catalogue as it is. Released false means nothing has ever been published, which
// is a different sentence entirely: the portal offers nothing from here at all, and
// the offering below is what the first release would carry.
type Unpublished struct {
	// Released reports whether a release exists, and the two fields after it
	// identify the newest one.
	Released  bool   `json:"released"`
	ReleaseID string `json:"releaseId,omitempty"`
	// ReleasedAt is when it was published, in the server's own seconds.
	ReleasedAt int64 `json:"releasedAt,omitempty"`

	// Added is offered now and absent from the release: publishing puts it in front
	// of people.
	Added []UnpublishedItem `json:"added"`
	// Removed is in the release and no longer offered: **the portal is still
	// offering it**, and publishing is what takes it away. This is the one a
	// maintainer cannot otherwise see, because it is gone from every screen they
	// have and present on the one they do not.
	Removed []UnpublishedItem `json:"removed"`
	// Changed is offered in both and edited since: the portal is showing an older
	// name, price, state or description than the catalogue holds.
	Changed []UnpublishedItem `json:"changed"`
}

// Any reports whether publishing would change anything for the people ordering.
func (u Unpublished) Any() bool {
	return len(u.Added) > 0 || len(u.Removed) > 0 || len(u.Changed) > 0
}

// UnpublishedItem names one product in terms a maintainer can act on.
type UnpublishedItem struct {
	ID string `json:"id"`
	// Texts is the item's name per language. For a removed product it is the
	// **frozen** copy's, because that is the name on the portal right now, and
	// naming it any other way would describe something the reader cannot see.
	Texts map[string]string `json:"texts,omitempty"`
	// State is the live record's, and empty for a removed product whose record is
	// no longer readable here.
	State State `json:"state,omitempty"`
}

// unpublishedFor computes the difference. Run-loop goroutine only — it reads two
// stores, and a catalogue edited between the two reads would produce a difference
// nobody made.
//
// Changed is decided on the revision and not on a field-by-field comparison. The
// revision is the record's own account of having been written, every writer
// advances it (TestEveryWriterAdvancesTheRevision names them all), and a comparison
// of the fields this package happened to think of would miss the next one added.
// It reports more than a reader might expect — a save that changed nothing still
// advances it — and that is the safer direction: the remedy is publishing, which
// costs a release and never loses anything.
func unpublishedFor(cat Catalog, items []Item, releases []Release) Unpublished {
	out := Unpublished{Added: []UnpublishedItem{}, Removed: []UnpublishedItem{}, Changed: []UnpublishedItem{}}

	live := make(map[string]Item, len(items))
	for _, it := range items {
		live[it.ID] = it
	}
	offered := make(map[string]bool, len(cat.Items))
	for _, id := range cat.Items {
		offered[id] = true
	}

	// Newest first is how ReleasesOf answers and how the portal reads it, so the
	// comparison is against the one being served rather than the first ever made.
	if len(releases) == 0 {
		for id := range offered {
			out.Added = append(out.Added, namedItem(id, live))
		}
		sortItems(out.Added)
		return out
	}
	rel := releases[0]
	out.Released, out.ReleaseID, out.ReleasedAt = true, rel.ID, rel.CreatedAt

	frozen := make(map[string]Item, len(rel.Items))
	for _, it := range rel.Items {
		frozen[it.ID] = it
	}

	for id := range offered {
		was, published := frozen[id]
		now, readable := live[id]
		switch {
		case !published:
			out.Added = append(out.Added, namedItem(id, live))
		// Compared only where the record is readable from here. A product homed in a
		// catalogue this caller may not maintain is offered legitimately and is none
		// of their business to compare (ADR-0315); an unreadable record reads as a
		// zero Item, so comparing it anyway would report every one of them as
		// changed and ask somebody to publish away a difference they cannot see.
		case readable && now.Revision != was.Revision:
			out.Changed = append(out.Changed, namedItem(id, live))
		}
	}
	for _, it := range rel.Items {
		if !offered[it.ID] {
			// The frozen copy's own texts, deliberately: this is what the portal has
			// on screen, and the live record may say something else or be
			// unreadable here.
			out.Removed = append(out.Removed, UnpublishedItem{ID: it.ID, Texts: copyTexts(it.Texts)})
		}
	}

	sortItems(out.Added)
	sortItems(out.Removed)
	sortItems(out.Changed)
	return out
}

// namedItem names a product from the live record where there is one, and by id
// alone where there is not — an offered id with no readable record still has to
// appear, because it is exactly the case somebody needs to see.
func namedItem(id string, live map[string]Item) UnpublishedItem {
	it, known := live[id]
	if !known {
		return UnpublishedItem{ID: id}
	}
	return UnpublishedItem{ID: id, Texts: copyTexts(it.Texts), State: it.State}
}

// sortItems puts a list in id order, so two reads of an unchanged catalogue
// produce the same answer rather than whichever way the map iterated.
func sortItems(in []UnpublishedItem) {
	sort.Slice(in, func(a, b int) bool { return in[a].ID < in[b].ID })
}

// HandleUnpublished answers what publishing this catalogue would change.
//
// Read authority and not write, the same as listing the releases: it says nothing
// the caller cannot already assemble from GET on the catalogue and on its releases.
// Requiring the authority to publish would withhold the answer from exactly the
// person who needs to ask somebody else to.
func (s *Service) HandleUnpublished(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	p := httpapi.PrincipalFrom(r.Context())

	var (
		out     Unpublished
		visible bool
		loadErr error
	)
	s.loop.Do(func() {
		cat, found, e := s.store.Catalog(id)
		if e != nil {
			loadErr = e
			return
		}
		if visible = found && s.mayRead(cat, p); !visible {
			return
		}
		items, e := s.store.Items()
		if e != nil {
			loadErr = e
			return
		}
		releases, e := s.store.ReleasesOf(id)
		if e != nil {
			loadErr = e
			return
		}
		out = unpublishedFor(cat, items, releases)
	})

	switch {
	case loadErr != nil:
		httpapi.Error(w, http.StatusInternalServerError, "unpublished changes: "+loadErr.Error())
	case !visible:
		httpapi.Error(w, http.StatusNotFound, "no catalogue "+id)
	default:
		httpapi.JSON(w, http.StatusOK, out)
	}
}
