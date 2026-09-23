package catalog

import (
	"net/http"
	"sort"

	"github.com/pblumer/atlas/api/httpapi"
)

// TranslationReport is what a maintainer still owes their catalogues.
//
// It exists because a gate was removed. Publishing used to refuse a product named
// in one declared language and not another; the refusal protected nobody — the
// portal falls back to the language the catalogue has — but it did make the gap
// impossible to ignore. Removing it without this would make a half-translated
// catalogue invisible again, which is the failure the gate was built against, and
// it would arrive as "the French portal reads oddly" months later.
//
// Read off the catalogues as they stand and NOT off their releases, unlike its
// two siblings. The fulfilment report reads the newest release because only what
// is published can park an order; this is a list of work to do, and work to do is
// about what is being edited. It is computed from the same input a publish is, so
// what it says is exactly what the next publish would have to live with.
type TranslationReport struct {
	// Checked is how many products were looked at, so a quiet report can be told
	// from an empty one.
	Checked int `json:"checked"`
	// Catalogs are the ids this report covers, sorted: the ones the caller may
	// maintain. A caller who maintains none gets an empty list rather than a 403,
	// because having nothing to maintain is not a refusal.
	Catalogs []string `json:"catalogs"`
	// Gaps is every place a product says something in one declared language and
	// not in another, each naming the catalogue and the product it is about.
	Gaps []Problem `json:"gaps"`
}

// HandleTranslationGaps answers it for every catalogue the caller may edit.
//
// Scoped to editors and not to readers, because it is a to-do list and a to-do
// list handed to the audience is a list of what the shop has not finished.
func (s *Service) HandleTranslationGaps(w http.ResponseWriter, r *http.Request) {
	p := httpapi.PrincipalFrom(r.Context())
	var (
		ids     []string
		gaps    []Problem
		checked int
		loadErr error
	)
	s.loop.Do(func() {
		cats, err := s.store.Catalogs()
		if err != nil {
			loadErr = err
			return
		}
		for _, cat := range cats {
			if !s.mayEdit(cat, p) {
				continue
			}
			in, found, e := s.store.InputFor(cat.ID, cat.Edges)
			if e != nil {
				loadErr = e
				return
			}
			if !found {
				continue
			}
			ids = append(ids, cat.ID)
			// The items the catalogue actually resolved, not the length of its id
			// list: an id naming no product is Publish's refusal to report, and
			// counting it here would say more was looked at than was.
			//
			// A product offered by two catalogues is counted twice, and that is
			// right rather than a rounding error: the languages are the
			// catalogue's, so the same product is two checks with two answers.
			checked += len(in.Items)
			gaps = append(gaps, TranslationGaps(in)...)
		}
	})
	if loadErr != nil {
		httpapi.Error(w, http.StatusInternalServerError, "translation report: "+loadErr.Error())
		return
	}
	sort.Strings(ids)
	if ids == nil {
		ids = []string{}
	}
	if gaps == nil {
		gaps = []Problem{}
	}
	httpapi.JSON(w, http.StatusOK, TranslationReport{
		Checked: checked, Catalogs: ids, Gaps: gaps,
	})
}
