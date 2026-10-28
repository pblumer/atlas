package api

import (
	"cmp"
	"net/http"
	"slices"

	"github.com/pblumer/atlas/api/archsketch"
	"github.com/pblumer/atlas/api/httpapi"
)

// The architecture sketch outside Atlas (ADR-0493, as amended for ADR-0495's
// sketches): a wiki that already draws this server's processes in its pages draws a
// release's sketch beside them. It picks one from the list below and reads the picture
// from the embed route (handleReleaseArchitectureEmbed), which answers every reader
// the same minimal view, because the page shows it to everybody who can open it.

// architectureListing is every release this caller may read a sketch of, by
// application. It carries what a picker needs and nothing about a person: not who
// published a release, nor the note they wrote.
type architectureListing struct {
	Applications []architectureApplication `json:"applications"`
}

type architectureApplication struct {
	ApplicationID string                `json:"applicationId"`
	Application   string                `json:"application"`
	Releases      []architectureRelease `json:"releases"`
}

type architectureRelease struct {
	Version     int32              `json:"version"`
	ReleaseID   string             `json:"releaseId"`
	PublishedAt int64              `json:"publishedAt"`
	TakenAt     int64              `json:"takenAt"`
	Recorded    []archsketch.Layer `json:"recorded"`
}

// handleListArchitectures answers GET /api/v1/architectures: the applications this
// caller may view (ADR-0071, the application list's rule), each with its releases
// that carry an architecture snapshot, newest first. An application with none is left
// out, since there is nothing of it to draw.
func (s *Server) handleListArchitectures(w http.ResponseWriter, r *http.Request) {
	out := architectureListing{Applications: []architectureApplication{}}
	var loadErr error
	s.do(func() {
		projs, err := s.projects.LoadAll()
		if err != nil {
			loadErr = err
			return
		}
		principal := httpapi.PrincipalFrom(r.Context())
		for _, p := range projs {
			if scopeRank(p.effectiveRole(principal, s.authEnabled)) < scopeRank(ScopeRoleViewer) {
				continue
			}
			rels, err := s.releases.forApplication(p.ID)
			if err != nil {
				loadErr = err
				return
			}
			app := architectureApplication{ApplicationID: p.ID, Application: p.Name, Releases: []architectureRelease{}}
			for _, rel := range rels {
				if rel.Architecture == nil {
					continue
				}
				app.Releases = append(app.Releases, architectureRelease{
					Version: rel.Version, ReleaseID: rel.ID, PublishedAt: rel.PublishedAt,
					TakenAt: rel.Architecture.TakenAt, Recorded: rel.Architecture.Recorded().Layers(),
				})
			}
			if len(app.Releases) == 0 {
				continue
			}
			slices.SortFunc(app.Releases, func(a, b architectureRelease) int { return cmp.Compare(b.Version, a.Version) })
			out.Applications = append(out.Applications, app)
		}
	})
	if loadErr != nil {
		httpapi.Error(w, http.StatusInternalServerError, "list architectures: "+loadErr.Error())
		return
	}
	slices.SortFunc(out.Applications, func(a, b architectureApplication) int {
		return cmp.Or(cmp.Compare(a.Application, b.Application), cmp.Compare(a.ApplicationID, b.ApplicationID))
	})
	httpapi.JSON(w, http.StatusOK, out)
}
