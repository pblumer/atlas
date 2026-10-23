// Package archsketch is a release's architecture snapshot (ADR-0471): what a process
// application offers to its environment and what it depends on, taken by value when
// the release is recorded, and the renderings a reader takes away from it — a
// deterministic SVG and an ArchiMate Open Exchange document.
//
// The package holds no state and reads nothing. The server assembles a [Snapshot]
// from the compiled processes a release shipped, on its run loop, and stores it on the
// release record in the same write that records the release; the renderers turn a
// stored snapshot into bytes and never consult anything else. That is what makes a
// rendering of v7 the same picture tomorrow as today: nothing it is drawn from can
// change after the release was recorded.
//
// A snapshot holds the release's **logical part** — the part fixed by its content,
// identical on every installation that records it — and, from format version 2, the
// recording installation's **physical part**: which worker serves each name there, at
// which endpoint, and who opens which connection (ADR-0471 §1; see physical.go). A
// snapshot from before that has no physical part, and its renderings say so rather
// than drawing a guess.
package archsketch

import (
	"cmp"
	"slices"
)

// FormatVersion versions the shape of a stored [Snapshot]. It is written into every
// snapshot, so a reader can tell which fields a snapshot from an older build can be
// expected to carry. A snapshot is never rewritten when this changes.
//
// Version 2 added the physical part and job-type dependencies; version 3 the
// endpoints worker instances report for the names they serve.
const FormatVersion = 3

// MappingVersion versions how a snapshot is drawn and written as ArchiMate: which
// facts become which elements and relationships. It is a small public contract — a
// document generated under one version stays what it was — and every rendering
// states it. Version 2 draws the physical part: connections from the side that opens
// them, target systems, and job types. Version 3 draws a reported endpoint as
// reported.
//
// A snapshot keeps the version it was taken under, and a snapshot without a physical
// part is drawn exactly as version 1 drew it, so the version a rendering states is
// the snapshot's own.
const MappingVersion = 3

// Snapshot is the logical part of one release's architecture.
type Snapshot struct {
	FormatVersion  int `json:"formatVersion"`
	MappingVersion int `json:"mappingVersion"`
	// TakenAt is when the snapshot was taken, by the server's clock, in Unix seconds.
	TakenAt int64 `json:"takenAt"`
	// Processes are the release's process members, as they were deployed.
	Processes []Process `json:"processes"`
	// EntryPoints are where the environment reaches into the application.
	EntryPoints []EntryPoint `json:"entryPoints"`
	// Dependencies are what the application reaches out to.
	Dependencies []Dependency `json:"dependencies"`
	// Physical is the recording installation's half, absent on a snapshot taken
	// before format version 2.
	Physical *Physical `json:"physical,omitempty"`
}

// Process is one process the release shipped.
type Process struct {
	ProcessID string `json:"processId"`
	Name      string `json:"name,omitempty"`
	Version   int32  `json:"version"`
	// Unread is set when the definition was recorded as a member but its compiled
	// form was not there to read. Its entry points and dependencies are then missing
	// from the snapshot, and the snapshot says so rather than drawing a process that
	// offers and needs nothing.
	Unread bool `json:"unread,omitempty"`
}

// Use is one element that states an entry point or a dependency: what a line on the
// sketch can be traced back to.
type Use struct {
	ProcessID string `json:"processId"`
	ElementID string `json:"elementId,omitempty"`
}

// Entry point kinds.
const (
	// EntryMessage is a message name the application receives: published to the
	// server under that name, it reaches the elements that wait for it.
	EntryMessage = "message"
)

// EntryPoint is one way in.
type EntryPoint struct {
	Kind string `json:"kind"`
	Name string `json:"name"`
	// Starts is set when at least one element receiving this message starts an
	// instance; otherwise every receipt continues one that is already running.
	Starts bool  `json:"starts,omitempty"`
	UsedBy []Use `json:"usedBy"`
}

// Dependency kinds.
const (
	// DependencyWorker is a worker named in the model: work the engine hands to
	// whatever serves that name.
	DependencyWorker = "worker"
	// DependencyRuntimeWorker is a worker name authored as a FEEL expression. Which
	// worker it reaches is decided per instance, so the snapshot holds the expression
	// and nothing it could resolve to.
	DependencyRuntimeWorker = "runtimeWorker"
	// DependencyJobType is a service or send task's job type: work any worker that
	// subscribes to the type leases from Atlas's API (ADR-0007). The model names the
	// work, not who does it.
	DependencyJobType = "jobType"
	// DependencyCall is a call activity into a process this release did not ship.
	DependencyCall = "call"
	// DependencyDecision is a local DMN decision a business rule task evaluates. It
	// runs inside the engine and crosses no boundary, so it is recorded for
	// completeness and drawn inside the application rather than beside it.
	DependencyDecision = "decision"
)

// Dependency is one way out.
type Dependency struct {
	Kind string `json:"kind"`
	// Name is the worker name, the FEEL expression of a runtime worker, the job type,
	// the called process id, or the decision id.
	Name string `json:"name"`
	// WorkerType is a worker's Worker Type ("rest", "mail", …) as the model's task
	// type states it.
	WorkerType string `json:"workerType,omitempty"`
	// TargetApplicationID and TargetApplication name the application that held the
	// called process on the recording installation when the snapshot was taken. Empty
	// when nothing there held it — which says the call had no target there, not that
	// it has none anywhere.
	TargetApplicationID string `json:"targetApplicationId,omitempty"`
	TargetApplication   string `json:"targetApplication,omitempty"`
	UsedBy              []Use  `json:"usedBy"`
}

// Normalize puts every list in a fixed order and merges duplicates, so the same
// release produces the same snapshot — and the same rendering bytes — whatever order
// its members were read in.
func (s *Snapshot) Normalize() {
	slices.SortFunc(s.Processes, func(a, b Process) int {
		return cmp.Or(cmp.Compare(a.ProcessID, b.ProcessID), cmp.Compare(a.Version, b.Version))
	})

	entries := map[[2]string]*EntryPoint{}
	for _, e := range s.EntryPoints {
		key := [2]string{e.Kind, e.Name}
		if have, ok := entries[key]; ok {
			have.Starts = have.Starts || e.Starts
			have.UsedBy = append(have.UsedBy, e.UsedBy...)
			continue
		}
		cp := e
		cp.UsedBy = append([]Use(nil), e.UsedBy...)
		entries[key] = &cp
	}
	s.EntryPoints = s.EntryPoints[:0]
	for _, e := range entries {
		e.UsedBy = normalizeUses(e.UsedBy)
		s.EntryPoints = append(s.EntryPoints, *e)
	}
	slices.SortFunc(s.EntryPoints, func(a, b EntryPoint) int {
		return cmp.Or(cmp.Compare(a.Kind, b.Kind), cmp.Compare(a.Name, b.Name))
	})

	deps := map[[3]string]*Dependency{}
	for _, d := range s.Dependencies {
		key := [3]string{d.Kind, d.Name, d.WorkerType}
		if have, ok := deps[key]; ok {
			have.UsedBy = append(have.UsedBy, d.UsedBy...)
			continue
		}
		cp := d
		cp.UsedBy = append([]Use(nil), d.UsedBy...)
		deps[key] = &cp
	}
	s.Dependencies = s.Dependencies[:0]
	for _, d := range deps {
		d.UsedBy = normalizeUses(d.UsedBy)
		s.Dependencies = append(s.Dependencies, *d)
	}
	slices.SortFunc(s.Dependencies, func(a, b Dependency) int {
		return cmp.Or(
			cmp.Compare(dependencyRank(a.Kind), dependencyRank(b.Kind)),
			cmp.Compare(a.Name, b.Name),
			cmp.Compare(a.WorkerType, b.WorkerType))
	})
	if s.Physical != nil {
		s.Physical.normalize()
	}
}

// dependencyRank orders the kinds the way the sketch lists them: the dependencies
// that leave the server first, then the ones that stay inside it.
func dependencyRank(kind string) int {
	switch kind {
	case DependencyWorker:
		return 0
	case DependencyRuntimeWorker:
		return 1
	case DependencyJobType:
		return 2
	case DependencyCall:
		return 3
	case DependencyDecision:
		return 4
	}
	return 5
}

func normalizeUses(uses []Use) []Use {
	slices.SortFunc(uses, func(a, b Use) int {
		return cmp.Or(cmp.Compare(a.ProcessID, b.ProcessID), cmp.Compare(a.ElementID, b.ElementID))
	})
	return slices.Compact(uses)
}

// Outbound is the dependencies that cross the server's boundary — everything but the
// local decisions, which the engine evaluates itself.
func (s Snapshot) Outbound() []Dependency {
	var out []Dependency
	for _, d := range s.Dependencies {
		if d.Kind != DependencyDecision {
			out = append(out, d)
		}
	}
	return out
}

// LocalDecisions counts the distinct local decisions the application evaluates.
func (s Snapshot) LocalDecisions() int {
	n := 0
	for _, d := range s.Dependencies {
		if d.Kind == DependencyDecision {
			n++
		}
	}
	return n
}

// UnreadProcesses counts the members whose compiled form was not there to read.
func (s Snapshot) UnreadProcesses() int {
	n := 0
	for _, p := range s.Processes {
		if p.Unread {
			n++
		}
	}
	return n
}

// Meta is what a rendering says about the release it was drawn from. It comes from
// the release record and the application, not from the snapshot, because the snapshot
// is the release's content and these are its names.
type Meta struct {
	ApplicationID string
	Application   string
	ReleaseID     string
	Version       int32
}

// NotShown is what a rendering of this snapshot lists as absent, in this order. A
// sketch is believed for everything it does not contradict, so the gaps travel inside
// it.
func (s Snapshot) NotShown() []string {
	first := "the physical view: which worker serves each name here, its endpoint, and who opens which connection"
	switch {
	case s.Physical != nil && s.FormatVersion >= 3:
		first = "the endpoint of a worker that keeps its own configuration and had not polled since the server started, or whose Worker Type names none"
	case s.Physical != nil:
		first = "the endpoint of a worker that keeps its own configuration, which such a worker does not report yet"
	}
	return []string{
		first,
		"this installation's own communication: outbound proxy, identity providers, exporters, metrics scraping, deployment targets",
		"addresses a task builds at run time, and which worker a name authored as FEEL reaches",
		"what sits behind a target system: gateways, load balancers, the systems they forward to",
	}
}
