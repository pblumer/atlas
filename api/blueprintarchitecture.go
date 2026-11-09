package api

import (
	"bytes"
	"net/http"

	"github.com/pblumer/atlas/api/archsketch"
	"github.com/pblumer/atlas/api/blueprintregistry"
	"github.com/pblumer/atlas/api/httpapi"
	"github.com/pblumer/atlas/api/infomodel"
	"github.com/pblumer/atlas/compiler"
)

// What an atlas hub release looks like before it is installed
// (ADR-draft-blueprint-architecture-preview): the architecture sketch of its
// application, derived from the process models its package carries, and the models
// themselves, for the Console's diagram viewer.
//
// Neither is a release's snapshot (ADR-0471). No installation has read the package,
// so the sketch carries only what the models fix — the messages the processes receive,
// the workers, job types, decisions and calls they name, their data objects, personal
// data, user-task roles and the names they throw and catch — and is marked Packaged,
// which words every "here" of the renderings for a package rather than an
// installation. The physical part, the strategy layer, catalogue products and the
// applications at the other end of a name belong to an installation and are listed as
// not shown.
//
// Everything here runs on the request's goroutine, off the run loop: it reads the
// verified package bytes and nothing of this server's state, so it holds no writer
// and needs none (I3). Each read fetches the release again through
// selectedBlueprint and is bound by ?digest= to the bytes the reader previewed, as the
// import is — a sketch of anything else would describe a package nobody reviewed.

// blueprintDiagram is one process model a package carries, as the viewer draws it.
type blueprintDiagram struct {
	ProcessID string `json:"processId"`
	Name      string `json:"name,omitempty"`
	Path      string `json:"path"`
	XML       string `json:"xml"`
}

// blueprintModels is a verified release's package read as a source tree.
type blueprintModels struct {
	release blueprintregistry.Release
	man     sourceManifest
	byPath  map[string][]byte
}

// verifiedBlueprintModels fetches the selected release, refuses it unless ?digest=
// names the bytes the reader previewed, inspects it and reads it as a source tree.
// It answers the refusal itself and reports whether it did.
func (s *Server) verifiedBlueprintModels(w http.ResponseWriter, r *http.Request) (blueprintModels, bool) {
	if !s.requireBlueprints(w) {
		return blueprintModels{}, false
	}
	digest := r.URL.Query().Get("digest")
	if digest == "" {
		httpapi.Error(w, http.StatusPreconditionRequired, "preview the Blueprint release first and pass the digest it reported as ?digest=")
		return blueprintModels{}, false
	}
	release, compressed, ok := s.selectedBlueprint(w, r)
	if !ok {
		return blueprintModels{}, false
	}
	if release.Artifact.Digest != digest {
		httpapi.Error(w, http.StatusPreconditionFailed, "Blueprint digest changed since preview; review again")
		return blueprintModels{}, false
	}
	if _, err := blueprintregistry.InspectPackage(release, compressed); err != nil {
		httpapi.Error(w, http.StatusBadGateway, "Blueprint package failed safe archive inspection")
		return blueprintModels{}, false
	}
	files, err := s.readSourceArchive(bytes.NewReader(compressed))
	if err != nil {
		httpapi.Error(w, http.StatusBadGateway, "Blueprint package is not a readable source tree: "+err.Error())
		return blueprintModels{}, false
	}
	man, byPath, err := parseSourceTree(files)
	if err != nil {
		httpapi.Error(w, http.StatusBadGateway, "Blueprint package is not a readable source tree: "+err.Error())
		return blueprintModels{}, false
	}
	return blueprintModels{release: release, man: man, byPath: byPath}, true
}

// blueprintSnapshot derives the sketch of a package's application from its process
// models alone. A process file that does not compile is recorded as unread, as a
// release member without a compiled form is, rather than drawn as a process that
// offers and needs nothing. A file holding a collaboration contributes each
// executable process in it, once.
func blueprintSnapshot(man sourceManifest, byPath map[string][]byte) *archsketch.Snapshot {
	var procs []sketchProcess
	seen := map[string]bool{}
	for _, p := range man.Processes {
		deployables, err := compiler.ParseAll(1, 1, bytes.NewReader(byPath[p.Path]))
		if err != nil {
			if !seen[p.ID] {
				seen[p.ID] = true
				procs = append(procs, sketchProcess{ID: p.ID})
			}
			continue
		}
		for _, d := range deployables {
			id := d.Process.ProcessId()
			if seen[id] {
				continue
			}
			seen[id] = true
			name := d.ProcessName
			if name == "" && id == p.ID {
				name = p.Name
			}
			procs = append(procs, sketchProcess{ID: id, Name: name, CP: d.Process})
		}
	}
	// No job runner and no call targets: which process runs a job type in-process and
	// which application a called process lives in are an installation's facts.
	l := logicalSketch(procs, 0, nil, nil)
	snap := l.snap
	snap.Packaged = true
	var models []infomodel.Model
	if arch, err := treeArchitecture(man, byPath); err == nil {
		for _, m := range arch.models {
			models = append(models, m.model)
		}
	}
	snap.Data = completeData(l.data, snap.Dependencies, infomodel.NewVocabulary(models))
	snap.Business = l.business
	snap.Coupling = l.coupling
	snap.Normalize()
	return snap
}

// handleBlueprintArchitecture answers GET /api/v1/blueprints/{id}/releases/{version}/architecture.
func (s *Server) handleBlueprintArchitecture(w http.ResponseWriter, r *http.Request) {
	s.serveBlueprintArchitecture(w, r, "json")
}

// handleBlueprintArchitectureSVG answers GET …/architecture.svg.
func (s *Server) handleBlueprintArchitectureSVG(w http.ResponseWriter, r *http.Request) {
	s.serveBlueprintArchitecture(w, r, "svg")
}

func (s *Server) serveBlueprintArchitecture(w http.ResponseWriter, r *http.Request, format string) {
	selection, err := archsketch.ParseSelection(r.URL.Query().Get("layers"))
	if err != nil {
		httpapi.Error(w, http.StatusBadRequest, err.Error())
		return
	}
	pkg, ok := s.verifiedBlueprintModels(w, r)
	if !ok {
		return
	}
	snap := blueprintSnapshot(pkg.man, pkg.byPath)
	recorded := snap.Recorded()
	if selection&recorded == 0 {
		httpapi.Error(w, http.StatusNotFound, "the package records none of the layers asked for ("+selection.String()+"); it records "+recorded.String())
		return
	}
	view := snap.Select(selection)
	m := archsketch.Meta{Application: pkg.man.Name, Blueprint: &archsketch.BlueprintOrigin{
		ID: pkg.release.BlueprintID, Version: pkg.release.Version, Digest: pkg.release.Artifact.Digest,
	}}
	if format == "svg" {
		serveSketch(w, "image/svg+xml; charset=utf-8", archsketch.SVG(view, m))
		return
	}
	httpapi.JSON(w, http.StatusOK, struct {
		BlueprintID    string                    `json:"blueprintId"`
		Version        string                    `json:"version"`
		Digest         string                    `json:"digest"`
		ApplicationKey string                    `json:"applicationKey"`
		Application    string                    `json:"application"`
		Layers         releaseArchitectureLayers `json:"layers"`
		Architecture   *archsketch.Snapshot      `json:"architecture"`
	}{
		BlueprintID: pkg.release.BlueprintID, Version: pkg.release.Version, Digest: pkg.release.Artifact.Digest,
		ApplicationKey: pkg.man.Key, Application: pkg.man.Name,
		Layers:       releaseArchitectureLayers{Recorded: recorded.Layers(), Shown: (selection & recorded).Layers()},
		Architecture: &view,
	})
}

// handleBlueprintDiagrams answers GET /api/v1/blueprints/{id}/releases/{version}/diagrams:
// the BPMN models the package carries, one per process file in the manifest's order,
// for the Console to draw read-only before anything is installed. The text is the
// package's own and reaches the browser as data inside JSON, never as markup.
func (s *Server) handleBlueprintDiagrams(w http.ResponseWriter, r *http.Request) {
	pkg, ok := s.verifiedBlueprintModels(w, r)
	if !ok {
		return
	}
	out := []blueprintDiagram{}
	seen := map[string]bool{}
	for _, p := range pkg.man.Processes {
		if seen[p.Path] {
			continue
		}
		seen[p.Path] = true
		out = append(out, blueprintDiagram{ProcessID: p.ID, Name: p.Name, Path: p.Path, XML: string(pkg.byPath[p.Path])})
	}
	httpapi.JSON(w, http.StatusOK, struct {
		BlueprintID string             `json:"blueprintId"`
		Version     string             `json:"version"`
		Digest      string             `json:"digest"`
		Processes   []blueprintDiagram `json:"processes"`
	}{pkg.release.BlueprintID, pkg.release.Version, pkg.release.Artifact.Digest, out})
}
