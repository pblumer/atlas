package api

import (
	"bytes"
	"fmt"

	"github.com/pblumer/atlas/compiler"
)

// An import writes a draft, and a draft is not a running process — so nothing an
// import does can stop an instance mid-flight. What it can do is quieter and
// worse: land on the id something else already occupies.
//
//   - A draft with the same process id is *replaced*. Whatever was being worked
//     on there is gone, with no version history behind a draft to get it back.
//   - A deployed process with the same id is not touched, but the draft that
//     lands is the one the next deploy of that process will use. Atlas migrates a
//     running instance by matching element ids (ADR-0162), so an import whose ids
//     do not line up with the deployed version's is an instance that cannot be
//     carried over — and a MIM import derives its ids from the workflow, which
//     means a model built by hand and one imported from MIM rarely agree.
//
// Both are worth knowing *before* the import lands, which is why this runs first
// and the import stops on it unless the caller says to overwrite.

// mimDraftImpact is the draft an import would replace.
type mimDraftImpact struct {
	Name      string `json:"name"`
	SavedAt   int64  `json:"savedAt"`
	ProjectID string `json:"projectId,omitempty"`
}

// mimDeployedImpact is the deployed process an import shares its id with, and
// what a later deploy of the imported model would mean for what is running on it.
type mimDeployedImpact struct {
	Version         int32 `json:"version"`
	DeployedAt      int64 `json:"deployedAt"`
	ActiveInstances int   `json:"activeInstances"`
	// KeptElements and DroppedElements split the deployed version's elements by
	// whether the imported model still has an element of that id — which is
	// exactly what decides whether a running instance could be migrated onto it.
	KeptElements    int      `json:"keptElements"`
	DroppedElements []string `json:"droppedElements,omitempty"`
	// DroppedDataObjects are the deployed version's data objects the imported
	// model does not declare: the process data an instance carries and a migrated
	// instance would no longer have a place for.
	DroppedDataObjects []string `json:"droppedDataObjects,omitempty"`
}

// mimImpact is the answer to "what is already on this id, and what would this
// import mean for it?" — written nowhere, and the same shape whether the import
// then proceeds or stops.
type mimImpact struct {
	ProcessID string             `json:"processId"`
	Name      string             `json:"name"`
	Draft     *mimDraftImpact    `json:"draft,omitempty"`
	Deployed  *mimDeployedImpact `json:"deployed,omitempty"`
}

// occupied reports whether anything already holds this process id, which is the
// condition an import needs the caller's confirmation to write through.
func (i mimImpact) occupied() bool { return i.Draft != nil || i.Deployed != nil }

// atRisk reports whether a running instance would be stranded by a later deploy
// of this model: elements it is standing on may be gone.
func (i mimImpact) atRisk() bool {
	return i.Deployed != nil && i.Deployed.ActiveInstances > 0 && len(i.Deployed.DroppedElements) > 0
}

// modelIdentity is what the preflight needs to know about an imported model, read
// off the compiler rather than the XML so it sees what a deploy would see.
type modelIdentity struct {
	elements    map[string]bool
	dataObjects map[string]bool
}

// identifyModel compiles a generated model far enough to list what it declares.
// It runs off the run loop, like every other compile on an import path
// (handleImportBundle), because compiling is not the single writer's work.
func identifyModel(bpmn []byte) (modelIdentity, error) {
	deployables, err := compiler.ParseAll(1, 1, bytes.NewReader(bpmn))
	if err != nil {
		return modelIdentity{}, err
	}
	id := modelIdentity{elements: map[string]bool{}, dataObjects: map[string]bool{}}
	for _, d := range deployables {
		cp := d.Process
		if cp == nil {
			continue
		}
		for i := int32(0); int(i) < cp.NodeCount(); i++ {
			if e := cp.ElementBpmnId(i); e != "" {
				id.elements[e] = true
			}
		}
		for _, do := range cp.DataObjects() {
			if n := cp.Intern(do.Name); n != "" {
				id.dataObjects[n] = true
			}
		}
	}
	return id, nil
}

// mimImpactOf reports what already holds processID and what the imported model
// would mean for it, and hands back the draft that is there so the caller can
// authorize against it without a second read. It reads shared state, so it runs
// on the run loop; the compile that produced want has already happened off it.
//
// The caller holds the loop: this is the body of an s.do, not a call that takes it.
func (s *Server) mimImpactOf(processID, name string, want modelIdentity) (mimImpact, draft, error) {
	out := mimImpact{ProcessID: processID, Name: name}

	held, ok, err := s.drafts.Get(processID)
	if err != nil {
		return mimImpact{}, draft{}, err
	}
	if ok {
		out.Draft = &mimDraftImpact{Name: held.Name, SavedAt: held.SavedAt, ProjectID: held.ProjectID}
	}

	dep := s.latestDeploymentOf(processID)
	if dep == nil || dep.cp == nil {
		return out, held, nil
	}
	impact := &mimDeployedImpact{Version: dep.Version, DeployedAt: dep.DeployedAt}
	if n, err := s.store.DefInstanceCount(dep.Key); err == nil {
		// A count that cannot be read is not worth failing the import over; the
		// element comparison below is the part that carries the warning.
		impact.ActiveInstances = n
	}
	for i := int32(0); int(i) < dep.cp.NodeCount(); i++ {
		e := dep.cp.ElementBpmnId(i)
		if e == "" {
			continue
		}
		if want.elements[e] {
			impact.KeptElements++
		} else {
			impact.DroppedElements = append(impact.DroppedElements, e)
		}
	}
	for _, do := range dep.cp.DataObjects() {
		n := dep.cp.Intern(do.Name)
		if n != "" && !want.dataObjects[n] {
			impact.DroppedDataObjects = append(impact.DroppedDataObjects, n)
		}
	}
	out.Deployed = impact
	return out, held, nil
}

// latestDeploymentOf returns the highest-version deployment of a process id, or
// nil when nothing by that id is deployed. Registration order is version order,
// so the last match wins.
func (s *Server) latestDeploymentOf(processID string) *deployment {
	var found *deployment
	for _, key := range s.order {
		d := s.deployments[key]
		if d == nil || d.ProcessID != processID {
			continue
		}
		if found == nil || d.Version >= found.Version {
			found = d
		}
	}
	return found
}

// mimImpactSummary renders one impact as a sentence for a log line or a CLI. The
// Console renders the structure itself.
func mimImpactSummary(i mimImpact) string {
	switch {
	case i.Draft != nil && i.Deployed != nil:
		return fmt.Sprintf("%s: replaces an existing draft and shares its id with deployed version %d",
			i.ProcessID, i.Deployed.Version)
	case i.Draft != nil:
		return fmt.Sprintf("%s: replaces an existing draft", i.ProcessID)
	case i.Deployed != nil:
		return fmt.Sprintf("%s: shares its id with deployed version %d", i.ProcessID, i.Deployed.Version)
	default:
		return i.ProcessID + ": nothing holds this id"
	}
}
