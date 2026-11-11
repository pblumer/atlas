package api

import (
	"net/http"
	"slices"
)

// An installation's status is read, never reported (ADR-0499 amendment h). A
// Console install is several writes the server cannot bind into one: the
// application's source (the hub import, which keeps the record), its constants, its
// release and its catalogues. Rather than store a "complete" a client could claim
// and a crash could leave wrong, every read compares what the record expects — the
// identities the verified package carried — with what this server holds now.

// The statuses of one component.
const (
	hubPresent     = "present"     // the application, a process, form, decision, constant or product is here
	hubMissing     = "missing"     // it is not, or not where the release put it
	hubPublished   = "published"   // a release carries what is here: the application's, or a catalogue's
	hubUnpublished = "unpublished" // a release exists, but behind what is here
)

// The status of an installation as a whole.
const (
	// hubStatusComplete: the import finished and every component is here, the
	// application's release carrying its processes as they are.
	hubStatusComplete = "complete"
	// hubStatusPartial: the import finished, and a component is missing or behind.
	// Installing the same release again picks up where it stopped.
	hubStatusPartial = "partial"
	// hubStatusPending: the server stopped between recording the import and
	// finishing it; what the application holds is listed, but not vouched for.
	hubStatusPending = "pending"
	// hubStatusRemoved: the application the release was installed into is gone.
	hubStatusRemoved = "removed"
)

// hubExpected is what the verified package carried, taken before the import wrote
// anything: the identities an installation's status is read against.
type hubExpected struct {
	Processes  []string `json:"processes"`
	Forms      []string `json:"forms"`
	Decisions  []string `json:"decisions"`
	Constants  []string `json:"constants"`
	Catalogues []string `json:"catalogues"`
	Products   []string `json:"products"`
	// PublishCatalogues says the package's catalogue document asks for its
	// catalogues to be published on import, so a catalogue without a release is not
	// yet installed as the package meant it.
	PublishCatalogues bool `json:"publishCatalogues,omitempty"`
}

// hubComponent is one component of an installation and what this server holds of it.
type hubComponent struct {
	Kind   string `json:"kind"` // application, process, form, decision, constant, release, catalogue, product
	ID     string `json:"id"`
	Status string `json:"status"`
	// Version is the application's newest release, on the release component.
	Version int32 `json:"version,omitempty"`
}

// hubInstallationView is a record as the listing answers it: the record, its status
// as a whole and its components.
type hubInstallationView struct {
	hubInstallation
	Status     string         `json:"status"`
	Components []hubComponent `json:"components"`
}

// hubHoldings is what the server holds that an installation's status is read
// against, read once for a whole listing.
type hubHoldings struct {
	s *Server
	// newest is each application's newest release, by application id.
	newest map[string]applicationRelease
}

func (s *Server) hubHoldings() (hubHoldings, error) {
	all, err := s.releases.LoadAll()
	if err != nil {
		return hubHoldings{}, err
	}
	newest := map[string]applicationRelease{}
	for _, rel := range all {
		if cur, ok := newest[rel.ApplicationID]; !ok || rel.Version > cur.Version {
			newest[rel.ApplicationID] = rel.listing()
		}
	}
	return hubHoldings{s: s, newest: newest}, nil
}

// view reads one record against what is held. A record kept before amendment h
// carries no expectations beyond its catalogues; its source is then not checked.
func (h hubHoldings) view(rec hubInstallation) (hubInstallationView, error) {
	s := h.s
	v := hubInstallationView{hubInstallation: rec, Components: []hubComponent{}}
	exp := hubExpected{Catalogues: rec.CataloguesNotImported}
	if rec.Expected != nil {
		exp = *rec.Expected
	}
	add := func(kind, id, status string) {
		v.Components = append(v.Components, hubComponent{Kind: kind, ID: id, Status: status})
	}
	present := func(ok bool) string {
		if ok {
			return hubPresent
		}
		return hubMissing
	}

	app, found, err := s.hubApplication(rec)
	if err != nil {
		return v, err
	}
	add("application", rec.ApplicationKey, present(found))

	// The source: each process, form and decision the package carried, in this
	// application. The newest of the processes' saves is what a release must cover.
	var newestSave int64
	for _, id := range exp.Processes {
		d, ok, err := s.drafts.Get(id)
		if err != nil {
			return v, err
		}
		ok = ok && found && d.ProjectID == app.ID
		if ok && d.SavedAt > newestSave {
			newestSave = d.SavedAt
		}
		add("process", id, present(ok))
	}
	for _, id := range exp.Forms {
		f, ok, err := s.forms.Get(id)
		if err != nil {
			return v, err
		}
		add("form", id, present(ok && found && f.ProjectID == app.ID))
	}
	for _, id := range exp.Decisions {
		d, ok, err := s.dmnrefs.Get(id)
		if err != nil {
			return v, err
		}
		add("decision", id, present(ok && found && d.ProjectID == app.ID))
	}

	if len(exp.Constants) > 0 {
		held := map[string]bool{}
		if found {
			stored, ok, err := s.appConstants.Get(app.ID)
			if err != nil {
				return v, err
			}
			if ok {
				for _, c := range stored.Constants {
					held[c.Name] = true
				}
			}
		}
		for _, name := range exp.Constants {
			add("constant", name, present(held[name]))
		}
	}

	// The release: the application's newest one carries every process and decision
	// of the package, and is not older than the newest save of those processes.
	if len(exp.Processes)+len(exp.Decisions) > 0 {
		c := hubComponent{Kind: "release", ID: rec.ApplicationKey, Status: hubMissing}
		if rel, ok := h.newest[app.ID]; found && ok {
			c.Version, c.Status = rel.Version, hubUnpublished
			if releaseCovers(rel, exp) && rel.PublishedAt >= newestSave {
				c.Status = hubPublished
			}
		}
		v.Components = append(v.Components, c)
	}

	for _, id := range exp.Catalogues {
		_, ok, err := s.catalogStore.Catalog(id)
		if err != nil {
			return v, err
		}
		status := present(ok)
		if ok && exp.PublishCatalogues {
			rels, err := s.catalogStore.NewestReleasesOf(id, 1)
			if err != nil {
				return v, err
			}
			status = hubPublished
			if len(rels) == 0 {
				status = hubUnpublished
			}
		}
		add("catalogue", id, status)
	}
	for _, id := range exp.Products {
		_, ok, err := s.catalogStore.Item(id)
		if err != nil {
			return v, err
		}
		add("product", id, present(ok))
	}

	switch {
	case rec.State == hubPending:
		v.Status = hubStatusPending
	case !found:
		v.Status = hubStatusRemoved
	default:
		v.Status = hubStatusComplete
		for _, c := range v.Components {
			if c.Status != hubPresent && c.Status != hubPublished {
				v.Status = hubStatusPartial
				break
			}
		}
	}
	return v, nil
}

// hubApplication finds the application a record names: by its id once the import
// finished, by its key while the record is pending (the id is not known then).
func (s *Server) hubApplication(rec hubInstallation) (project, bool, error) {
	if rec.ApplicationID != "" {
		app, ok, err := s.projects.Get(rec.ApplicationID)
		return app, ok && app.Key == rec.ApplicationKey, err
	}
	apps, err := s.projects.LoadAll()
	if err != nil {
		return project{}, false, err
	}
	for _, app := range apps {
		if app.Key == rec.ApplicationKey {
			return app, true, nil
		}
	}
	return project{}, false, nil
}

// releaseCovers reports whether a release names every process and decision the
// package carried.
func releaseCovers(rel applicationRelease, exp hubExpected) bool {
	has := func(kind, ref string) bool {
		return slices.ContainsFunc(rel.Members, func(m releaseMember) bool { return m.Kind == kind && m.Ref == ref })
	}
	for _, id := range exp.Processes {
		if !has("process", id) {
			return false
		}
	}
	for _, id := range exp.Decisions {
		if !has("decision", id) {
			return false
		}
	}
	return true
}

// hubVisible reports whether the caller may see an installation: an administrator
// sees every one; anybody else the installations into applications they may view.
// A record whose application is gone is an administrator's only.
//
// Its project lookup reads off the run loop, which sidecar reads allow.
func (s *Server) hubVisible(r *http.Request, rec hubInstallation) (bool, error) {
	if s.isAdmin(r) {
		return true, nil
	}
	app, ok, err := s.hubApplication(rec)
	if err != nil || !ok {
		return false, err
	}
	code, _ := s.checkProjectRole(r, app, ScopeRoleViewer)
	return code == 0, nil
}
