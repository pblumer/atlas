package catalog

import (
	"context"
	"net/http"
	"sort"
	"strings"

	"github.com/pblumer/atlas/api/httpapi"
)

// The wiki's services as building blocks (tshopper phase A, TSH3;
// ADR-0515).
//
// In the tshopper picture the product owner describes a service in
// wikiwikibangbang and the product manager composes offerings from those
// descriptions. The binding already exists — a product names its pages
// (ADR-0491) — but it is reached from a product, so the question "which services
// are described and not yet offered?" had no place to be asked. This is that
// place: the source searched as a list, each page with the products that use it.
//
// A product the reader does not maintain is counted and not named: which
// catalogue offers what is maintenance information about somebody else's estate,
// and the count is enough to say "this one is taken".

// BuildingBlock is one page a search found, and what already uses it.
type BuildingBlock struct {
	ID    string `json:"id"`
	Key   string `json:"key"`
	Title string `json:"title"`
	Space string `json:"space"`
	State string `json:"state,omitempty"`
	URL   string `json:"url,omitempty"`
	// UsedBy names the products the reader maintains whose binding names this page.
	UsedBy []string `json:"usedBy"`
	// UsedElsewhere counts the products that use it and the reader does not maintain.
	UsedElsewhere int `json:"usedElsewhere,omitempty"`
}

type buildingBlocks struct {
	// Sources are the configured sources to search, answered when no source is
	// named: the list of sources is otherwise an administrator's, and a product
	// manager needs the names to choose from, not the configuration.
	Sources []sourceSummary `json:"sources,omitempty"`
	Source  string          `json:"source"`
	Blocks  []BuildingBlock `json:"blocks"`
}

// HandleBuildingBlocks searches a content source (?source=, ?q=) and answers each
// page with the products that use it.
func (s *Service) HandleBuildingBlocks(w http.ResponseWriter, r *http.Request) {
	if s.noSources(w) {
		return
	}
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	source := strings.TrimSpace(r.URL.Query().Get("source"))
	if source == "" && q == "" {
		all, err := s.Content.Sources()
		if err != nil {
			httpapi.Error(w, http.StatusInternalServerError, "building blocks: "+err.Error())
			return
		}
		out := buildingBlocks{Sources: []sourceSummary{}, Blocks: []BuildingBlock{}}
		for _, src := range all {
			out.Sources = append(out.Sources, sourceSummary{ID: src.ID, Name: src.Name, Kind: src.Kind})
		}
		httpapi.JSON(w, http.StatusOK, out)
		return
	}
	_, ad, ok := s.openSource(w, source)
	if !ok {
		return
	}
	if q == "" {
		httpapi.Error(w, http.StatusBadRequest, "name what to search for: ?q=")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), fetchTimeout)
	defer cancel()
	hits, err := ad.Search(ctx, q)
	if err != nil {
		httpapi.Error(w, http.StatusBadGateway, "search the content source: "+err.Error())
		return
	}

	p := httpapi.PrincipalFrom(r.Context())
	type user struct {
		mine   []string
		others int
	}
	usage := map[string]*user{}
	var loadErr error
	s.loop.Do(func() {
		items, err := s.store.Items()
		if err != nil {
			loadErr = err
			return
		}
		cats, err := s.store.Catalogs()
		if err != nil {
			loadErr = err
			return
		}
		home := make(map[string]Catalog, len(cats))
		for _, c := range cats {
			home[c.ID] = c
		}
		for _, it := range items {
			cs := it.ContentSource
			if cs == nil || cs.Source != source {
				continue
			}
			c, known := home[it.HomeCatalog]
			mine := known && s.MayMaintain(c, p)
			seen := map[string]bool{}
			for _, pg := range cs.Pages {
				if pg.Ref == "" || seen[pg.Ref] {
					continue
				}
				seen[pg.Ref] = true
				u := usage[pg.Ref]
				if u == nil {
					u = &user{}
					usage[pg.Ref] = u
				}
				if mine {
					u.mine = append(u.mine, it.ID)
				} else {
					u.others++
				}
			}
		}
	})
	if loadErr != nil {
		httpapi.Error(w, http.StatusInternalServerError, "building blocks: "+loadErr.Error())
		return
	}

	out := buildingBlocks{Source: source, Blocks: make([]BuildingBlock, 0, len(hits))}
	for _, h := range hits {
		b := BuildingBlock{ID: h.ID, Key: h.Key, Title: h.Title, Space: h.Space, State: h.State, URL: h.URL,
			UsedBy: []string{}}
		if u := usage[h.ID]; u != nil {
			b.UsedBy = append(b.UsedBy, u.mine...)
			sort.Strings(b.UsedBy)
			b.UsedElsewhere = u.others
		}
		out.Blocks = append(out.Blocks, b)
	}
	httpapi.JSON(w, http.StatusOK, out)
}
