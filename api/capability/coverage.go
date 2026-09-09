package capability

import "sort"

// Coverage: one capability with every mutable fact about it resolved at read time,
// and nothing about it stored.
//
// This is the read that answers the questions the registry exists for — what is
// actually doing this, what does it depend on and what have those promised, who
// depends on it, and which value stream stalls without it. Every answer is computed
// from the map plus the landscape the server handed over; a record that had cached
// any of it would be a second, stale copy of the deployment registry (ADR-0189 §4).

// MeasurementNotice is the standing answer to "and how is it doing?".
//
// It is a field rather than documentation because the read returns goals and
// thresholds, and a client with a number-shaped field in front of it will render a
// number. Saying plainly that nothing here is measured is what stops a goal being
// displayed as an achievement. When the measurement slice lands, this changes and
// clients that read it stop being wrong.
const MeasurementNotice = "not measured: the KPIs and SLAs on a capability are declarations. " +
	"Atlas holds the data to compute them (per-element visit counters, the instance timeline) " +
	"but this build computes none of it."

// CoverageReport is one capability, resolved.
type CoverageReport struct {
	Capability Capability `json:"capability"`
	// Realized is whether anything at all currently does this. It is the summary line
	// of the whole read, and it counts a clerk and a purchased system as realizations,
	// because they are.
	Realized     bool                  `json:"realized"`
	Realizations []ResolvedRealization `json:"realizations"`
	// Requires is what this capability depends on, each with the promise it has made.
	// The dependency plus the SLA is the whole interface, which is what lets an
	// end-to-end target be reasoned about without opening any box beneath it.
	Requires []Dependency `json:"requires"`
	// RequiredBy is the reverse: who would notice if this stopped.
	RequiredBy []Dependent `json:"requiredBy"`
	// ValueStreams are the streams whose stages name this capability, with the stages
	// themselves — an end-to-end capability is named by every stage it spans.
	ValueStreams []StreamUse `json:"valueStreams"`
	// Measurement is [MeasurementNotice]. See its comment for why it is on the wire.
	Measurement string `json:"measurement"`
}

// ResolvedRealization is one realization with what the installation says about it.
type ResolvedRealization struct {
	Realization
	// Resolved says the thing this realization names is here. A system or manual
	// realization is always resolved: Atlas cannot check either, and reporting them
	// as unresolved would read as broken when the truth is only that the work happens
	// somewhere Atlas cannot see.
	Resolved bool `json:"resolved"`
	// Restricted says the caller may not see what this points at. It is not Resolved
	// and it is not missing — asserting either would tell the caller something they
	// have no right to know, or something untrue.
	Restricted bool `json:"restricted"`
	// Name, ApplicationName, Version, Inactive, ActiveInstances are the live facts,
	// resolved now and stored nowhere. All empty on a restricted entry.
	Name            string `json:"name,omitempty"`
	ApplicationName string `json:"applicationName,omitempty"`
	WorkerType      string `json:"workerType,omitempty"`
	Version         int32  `json:"version,omitempty"`
	Inactive        bool   `json:"inactive,omitempty"`
	ActiveInstances int    `json:"activeInstances,omitempty"`
}

// Dependency is one required capability as its caller sees it: a black box with a
// name, a state, and whatever it has promised.
type Dependency struct {
	Key string `json:"key"`
	// Known is whether the map has this capability at all. False is a finding the gap
	// report also raises; it is here because the caller of this read is looking at the
	// dependency list and should not have to fetch a second document to learn that one
	// of them names nothing.
	Known    bool   `json:"known"`
	Name     string `json:"name,omitempty"`
	State    string `json:"state,omitempty"`
	Realized bool   `json:"realized"`
	Owner    Owner  `json:"owner,omitempty"`
	SLAs     []SLA  `json:"slas,omitempty"`
}

// Dependent is one capability that requires this one.
type Dependent struct {
	Key   string `json:"key"`
	Name  string `json:"name,omitempty"`
	State string `json:"state,omitempty"`
	Owner Owner  `json:"owner,omitempty"`
}

// StreamUse is one value stream that uses this capability, and where.
type StreamUse struct {
	Key    string     `json:"key"`
	Name   string     `json:"name,omitempty"`
	Stages []StageRef `json:"stages"`
}

// StageRef names one stage of a value stream.
type StageRef struct {
	Key  string `json:"key"`
	Name string `json:"name,omitempty"`
}

// Coverage resolves one capability against the map and the landscape.
//
// Pure, like [Gaps], and for the same reason: it can then be tested against a
// landscape written by hand, and it never needs the run loop.
func Coverage(c Capability, all []Capability, streams []ValueStream, land Landscape) CoverageReport {
	byKey := make(map[string]Capability, len(all))
	for _, other := range all {
		byKey[other.Key] = other
	}
	processes := land.processIndex()
	workers := land.workerIndex()

	rep := CoverageReport{
		Capability:   c,
		Realizations: make([]ResolvedRealization, 0, len(c.Realizations)),
		Requires:     make([]Dependency, 0, len(c.Requires)),
		RequiredBy:   []Dependent{},
		ValueStreams: []StreamUse{},
		Measurement:  MeasurementNotice,
	}

	for _, r := range c.Realizations {
		resolved := ResolvedRealization{Realization: r}
		switch r.Kind {
		case RealizationProcess:
			if p, ok := processes[realizationRef{app: r.ApplicationKey, process: r.ProcessID}]; ok {
				if p.CanView {
					resolved.Resolved = true
					resolved.Name = p.Name
					resolved.ApplicationName = p.ApplicationName
					resolved.Version = p.Version
					resolved.Inactive = p.Inactive
					resolved.ActiveInstances = p.ActiveInstances
				} else {
					resolved.Restricted = true
				}
			}
		case RealizationWorker:
			if w, ok := workers[r.WorkerRef]; ok {
				if w.CanView {
					resolved.Resolved = true
					resolved.Name = w.Name
					resolved.WorkerType = w.Type
				} else {
					resolved.Restricted = true
				}
			}
		case RealizationSystem, RealizationManual:
			resolved.Resolved = true
			resolved.Name = r.Note
		}
		if resolved.Resolved {
			rep.Realized = true
		}
		rep.Realizations = append(rep.Realizations, resolved)
	}

	requires := append([]string(nil), c.Requires...)
	sort.Strings(requires)
	for _, key := range requires {
		dep := Dependency{Key: key}
		if other, ok := byKey[key]; ok {
			dep.Known = true
			dep.Name = other.Name
			dep.State = other.State
			dep.Owner = other.Owner
			dep.Realized = len(other.Realizations) > 0
			dep.SLAs = other.SLAs
		}
		rep.Requires = append(rep.Requires, dep)
	}

	for _, other := range all {
		for _, key := range other.Requires {
			if key == c.Key {
				rep.RequiredBy = append(rep.RequiredBy, Dependent{
					Key: other.Key, Name: other.Name, State: other.State, Owner: other.Owner})
				break
			}
		}
	}
	sort.Slice(rep.RequiredBy, func(i, j int) bool { return rep.RequiredBy[i].Key < rep.RequiredBy[j].Key })

	for _, v := range streams {
		use := StreamUse{Key: v.Key, Name: v.Name, Stages: []StageRef{}}
		for _, st := range v.Stages {
			for _, key := range st.Capabilities {
				if key == c.Key {
					use.Stages = append(use.Stages, StageRef{Key: st.Key, Name: st.Name})
					break
				}
			}
		}
		if len(use.Stages) > 0 {
			rep.ValueStreams = append(rep.ValueStreams, use)
		}
	}
	sort.Slice(rep.ValueStreams, func(i, j int) bool { return rep.ValueStreams[i].Key < rep.ValueStreams[j].Key })

	return rep
}
