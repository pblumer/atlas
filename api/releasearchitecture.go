package api

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/pblumer/atlas/api/archsketch"
	"github.com/pblumer/atlas/api/httpapi"
)

// A release's architecture sketch (ADR-0471).
//
// The snapshot is taken on the run loop in the same step that saves the release
// record — on publish (mintRelease) and on the import of a promoted release — and it
// rides on the record, so the one atomic write that records a release records its
// snapshot too. A release never exists without one, and nothing here enters the WAL.
//
// What the snapshot holds is fixed by the release's content: the messages its
// processes receive, the workers they name, the local decisions they evaluate and the
// calls they make into processes the release did not ship. Where those workers are,
// and who connects to whom, is the physical part and comes in a later slice; the
// renderings say so.

// architectureSnapshot reads the release's process members from their compiled forms.
// Run-loop goroutine only: it reads the deployment registry and the project store.
//
// The members are deployed at this moment by construction — the publish or import
// that records the release has just deployed them — so a member with no compiled form
// is a fault worth saying, and it is recorded as unread rather than drawn as a process
// that offers and needs nothing.
func (s *Server) architectureSnapshot(appID string, members []releaseMember, takenAt int64) *archsketch.Snapshot {
	snap := &archsketch.Snapshot{
		FormatVersion:  archsketch.FormatVersion,
		MappingVersion: archsketch.MappingVersion,
		TakenAt:        takenAt,
		Processes:      []archsketch.Process{},
		EntryPoints:    []archsketch.EntryPoint{},
		Dependencies:   []archsketch.Dependency{},
	}
	shipped := map[string]bool{}
	for _, m := range members {
		if m.Kind == "process" {
			shipped[m.Ref] = true
		}
	}
	for _, m := range members {
		if m.Kind != "process" {
			continue
		}
		proc := archsketch.Process{ProcessID: m.Ref, Version: m.ArtifactVer}
		d := s.deployments[m.Key]
		if d == nil || d.cp == nil {
			proc.Unread = true
			snap.Processes = append(snap.Processes, proc)
			continue
		}
		proc.Name = d.Name
		snap.Processes = append(snap.Processes, proc)
		cp := d.cp
		use := func(elementID string) []archsketch.Use {
			return []archsketch.Use{{ProcessID: m.Ref, ElementID: elementID}}
		}

		for _, r := range cp.MessageReceipts() {
			snap.EntryPoints = append(snap.EntryPoints, archsketch.EntryPoint{
				Kind: archsketch.EntryMessage, Name: r.MessageName, Starts: r.Starts, UsedBy: use(r.ElementId),
			})
		}

		// The same enumeration the landscape draws its worker edges from (workerRefs),
		// for the same reason: a reference whose job type no managed Worker Type claims
		// is not a worker reference at all.
		for _, ref := range cp.ConnectorRefs() {
			kind := connectorKindOfJobType(ref.JobType)
			if kind == "" {
				continue
			}
			dep := archsketch.Dependency{
				Kind: archsketch.DependencyWorker, Name: ref.Connector, WorkerType: kind, UsedBy: use(ref.ElementId),
			}
			if name := strings.TrimSpace(ref.Connector); strings.HasPrefix(name, "=") {
				dep.Kind, dep.Name = archsketch.DependencyRuntimeWorker, name
			}
			snap.Dependencies = append(snap.Dependencies, dep)
		}

		for _, ref := range cp.BusinessRuleDecisionRefs() {
			snap.Dependencies = append(snap.Dependencies, archsketch.Dependency{
				Kind: archsketch.DependencyDecision, Name: ref.DecisionId, UsedBy: use(cp.ElementBpmnId(ref.ElementIndex)),
			})
		}

		for _, ref := range cp.CallActivities() {
			if shipped[ref.CalledProcessId] {
				// A call between two processes of this release stays inside the
				// application; it is not a way out of it.
				continue
			}
			dep := archsketch.Dependency{Kind: archsketch.DependencyCall, Name: ref.CalledProcessId, UsedBy: use(ref.ElementId)}
			if target := s.latestDeploymentByProcessID(ref.CalledProcessId); target != nil && target.ProjectID != "" {
				if target.ProjectID == appID {
					continue
				}
				dep.TargetApplicationID = target.ProjectID
				if proj, ok, err := s.projects.Get(target.ProjectID); err == nil && ok {
					dep.TargetApplication = proj.Name
				}
			}
			snap.Dependencies = append(snap.Dependencies, dep)
		}
	}
	snap.Normalize()
	return snap
}

// releaseArchitectureResp is the JSON answer: the release's names beside the snapshot,
// because the snapshot is the release's content and these are what a reader calls it.
type releaseArchitectureResp struct {
	ApplicationID string               `json:"applicationId"`
	Application   string               `json:"application"`
	ReleaseID     string               `json:"releaseId"`
	Version       int32                `json:"version"`
	PublishedAt   int64                `json:"publishedAt"`
	Architecture  *archsketch.Snapshot `json:"architecture"`
}

func (s *Server) handleReleaseArchitecture(w http.ResponseWriter, r *http.Request) {
	s.serveReleaseArchitecture(w, r, "json")
}

func (s *Server) handleReleaseArchitectureSVG(w http.ResponseWriter, r *http.Request) {
	s.serveReleaseArchitecture(w, r, "svg")
}

func (s *Server) handleReleaseArchitectureXML(w http.ResponseWriter, r *http.Request) {
	s.serveReleaseArchitecture(w, r, "xml")
}

// serveReleaseArchitecture answers one release's snapshot under the application's
// sharing scope (ADR-0071), in the format the route names. The rendering runs off the
// loop: it reads nothing but the snapshot it is handed.
func (s *Server) serveReleaseArchitecture(w http.ResponseWriter, r *http.Request, format string) {
	appID := r.PathValue("id")
	version64, err := strconv.ParseInt(r.PathValue("version"), 10, 32)
	if err != nil || version64 <= 0 {
		httpapi.Error(w, http.StatusBadRequest, "invalid release version")
		return
	}
	version := int32(version64)
	proj, code, msg := s.authorizeProject(r, appID, ScopeRoleViewer)
	if code != 0 {
		httpapi.Error(w, code, msg)
		return
	}

	var (
		rel     *applicationRelease
		loadErr error
	)
	s.do(func() {
		rels, err := s.releases.forApplication(appID)
		if err != nil {
			loadErr = err
			return
		}
		for i := range rels {
			if rels[i].Version == version {
				rel = &rels[i]
				return
			}
		}
	})
	switch {
	case loadErr != nil:
		httpapi.Error(w, http.StatusInternalServerError, "load releases: "+loadErr.Error())
		return
	case rel == nil:
		httpapi.Error(w, http.StatusNotFound, "no release v"+strconv.Itoa(int(version))+" of this application")
		return
	case rel.Architecture == nil:
		// Said in words rather than left as a bare 404, because the two read alike and
		// send somebody to different places: this release exists and simply predates
		// snapshots, so publishing again is the remedy, not looking for a typo.
		httpapi.Error(w, http.StatusNotFound, "release v"+strconv.Itoa(int(version))+
			" has no architecture snapshot: it was recorded before Atlas took them (ADR-0471). "+
			"A release published from now on carries one.")
		return
	}

	m := archsketch.Meta{ApplicationID: appID, Application: proj.Name, ReleaseID: rel.ID, Version: rel.Version}
	switch format {
	case "svg":
		serveSketch(w, "image/svg+xml; charset=utf-8", archsketch.SVG(*rel.Architecture, m))
	case "xml":
		serveSketch(w, "application/xml; charset=utf-8", archsketch.ArchiMate(*rel.Architecture, m))
	default:
		httpapi.JSON(w, http.StatusOK, releaseArchitectureResp{
			ApplicationID: appID, Application: proj.Name, ReleaseID: rel.ID,
			Version: rel.Version, PublishedAt: rel.PublishedAt, Architecture: rel.Architecture,
		})
	}
}

// serveSketch writes a rendering under the headers that keep it inert wherever it is
// opened: the type pinned, and a policy with no script origin. The renderings carry
// no script to begin with; the policy is what keeps it that way if one ever did.
func serveSketch(w http.ResponseWriter, contentType string, body []byte) {
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Content-Security-Policy", "default-src 'none'; style-src 'unsafe-inline'; sandbox")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(body)
}
