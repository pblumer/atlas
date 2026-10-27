package archsketch

import (
	"cmp"
	"slices"
	"strings"
)

// The strategy layer of a release's architecture (ADR-0495 slice D): the capabilities a
// release process or a Worker the release names realises (ADR-0305), and the value
// streams whose stages use them.
//
// Both are read from the capability register at the moment the snapshot is taken. A
// capability's owner is recorded by name and role only; their contact, their Atlas
// account and who confirmed or edited the record are never recorded (ADR-0495 §5). The
// register is readable by every signed-in identity, so nothing here is withheld.

// The bounds on the strategy layer's lists (ADR-0495 §6), guards well above what a real
// register holds; what they leave out is counted in [Strategy.Omitted].
const (
	maxCapabilities = 100
	maxValueStreams = 50
	maxMeasures     = 20
	maxStages       = 30
)

// Strategy is the strategy layer, absent on a snapshot taken before format version 6.
type Strategy struct {
	Capabilities []Capability  `json:"capabilities"`
	ValueStreams []ValueStream `json:"valueStreams"`
	// Omitted counts, per kind, what the bounds left out.
	Omitted StrategyOmitted `json:"omitted,omitzero"`
}

// StrategyOmitted counts what the bounds left out of each list.
type StrategyOmitted struct {
	Capabilities int `json:"capabilities,omitempty"`
	ValueStreams int `json:"valueStreams,omitempty"`
	Measures     int `json:"measures,omitempty"`
	Stages       int `json:"stages,omitempty"`
}

// Owner is who answers for a capability or a value stream, by name and role only.
type Owner struct {
	Name string `json:"name,omitempty"`
	Role string `json:"role,omitempty"`
}

// Capability is one capability the release realises.
type Capability struct {
	Key   string `json:"key"`
	Name  string `json:"name"`
	State string `json:"state,omitempty"`
	// ConfirmedAt is when the record was last confirmed, in Unix seconds; zero when it
	// never was.
	ConfirmedAt int64         `json:"confirmedAt,omitempty"`
	Owner       *Owner        `json:"owner,omitempty"`
	KPIs        []KPI         `json:"kpis,omitempty"`
	SLAs        []SLA         `json:"slas,omitempty"`
	RealizedBy  []Realization `json:"realizedBy"`
}

// KPI is a measure a capability declares, as declared.
type KPI struct {
	Name      string `json:"name"`
	Metric    string `json:"metric,omitempty"`
	Goal      string `json:"goal,omitempty"`
	Direction string `json:"direction,omitempty"`
}

// SLA is a service level a capability declares, as declared.
type SLA struct {
	Name      string `json:"name"`
	Metric    string `json:"metric,omitempty"`
	Threshold string `json:"threshold,omitempty"`
	Window    string `json:"window,omitempty"`
	Scope     string `json:"scope,omitempty"`
}

// Realization kinds, as the register spells them.
const (
	RealizedByProcess = "process"
	RealizedByWorker  = "worker"
)

// Realization is what of the release realises a capability: one of its processes, or a
// Worker it names.
type Realization struct {
	Kind      string `json:"kind"`
	ProcessID string `json:"processId,omitempty"`
	Worker    string `json:"worker,omitempty"`
}

// ValueStream is one value stream a stage of which uses a capability the release
// realises, with all its stages, so the place of those capabilities in it shows.
type ValueStream struct {
	Key    string  `json:"key"`
	Name   string  `json:"name"`
	Owner  *Owner  `json:"owner,omitempty"`
	Stages []Stage `json:"stages"`
}

// Stage is one stage of a value stream, with the capabilities of the release it uses;
// its order is the stream's own.
type Stage struct {
	Key          string   `json:"key"`
	Name         string   `json:"name"`
	Capabilities []string `json:"capabilities"`
}

// normalize puts every list in a fixed order and applies the bounds. A value stream's
// stages keep the order the stream gives them.
func (s *Strategy) normalize() {
	for i := range s.Capabilities {
		c := &s.Capabilities[i]
		slices.SortFunc(c.RealizedBy, func(x, y Realization) int {
			return cmp.Or(cmp.Compare(x.Kind, y.Kind), cmp.Compare(x.ProcessID, y.ProcessID), cmp.Compare(x.Worker, y.Worker))
		})
		c.RealizedBy = slices.Compact(c.RealizedBy)
		if c.RealizedBy == nil {
			c.RealizedBy = []Realization{}
		}
		c.KPIs, s.Omitted.Measures = bound(c.KPIs, maxMeasures, s.Omitted.Measures)
		c.SLAs, s.Omitted.Measures = bound(c.SLAs, maxMeasures, s.Omitted.Measures)
	}
	slices.SortFunc(s.Capabilities, func(x, y Capability) int { return cmp.Or(cmp.Compare(x.Name, y.Name), cmp.Compare(x.Key, y.Key)) })
	s.Capabilities = slices.CompactFunc(s.Capabilities, func(x, y Capability) bool { return x.Key == y.Key })

	for i := range s.ValueStreams {
		v := &s.ValueStreams[i]
		for j := range v.Stages {
			slices.Sort(v.Stages[j].Capabilities)
			v.Stages[j].Capabilities = slices.Compact(v.Stages[j].Capabilities)
			if v.Stages[j].Capabilities == nil {
				v.Stages[j].Capabilities = []string{}
			}
		}
		v.Stages, s.Omitted.Stages = bound(v.Stages, maxStages, s.Omitted.Stages)
		if v.Stages == nil {
			v.Stages = []Stage{}
		}
	}
	slices.SortFunc(s.ValueStreams, func(x, y ValueStream) int { return cmp.Or(cmp.Compare(x.Name, y.Name), cmp.Compare(x.Key, y.Key)) })
	s.ValueStreams = slices.CompactFunc(s.ValueStreams, func(x, y ValueStream) bool { return x.Key == y.Key })

	s.Capabilities, s.Omitted.Capabilities = bound(s.Capabilities, maxCapabilities, s.Omitted.Capabilities)
	s.ValueStreams, s.Omitted.ValueStreams = bound(s.ValueStreams, maxValueStreams, s.Omitted.ValueStreams)
	if s.Capabilities == nil {
		s.Capabilities = []Capability{}
	}
	if s.ValueStreams == nil {
		s.ValueStreams = []ValueStream{}
	}
}

// text lists what the bounds left out.
func (o StrategyOmitted) text() string {
	var parts []string
	for _, c := range []struct {
		n         int
		one, many string
	}{
		{o.Capabilities, "capability", "capabilities"},
		{o.ValueStreams, "value stream", "value streams"},
		{o.Measures, "KPI or SLA", "KPIs and SLAs"},
		{o.Stages, "stage", "stages"},
	} {
		if c.n > 0 {
			parts = append(parts, plural(c.n, c.one, c.many))
		}
	}
	return strings.Join(parts, ", ")
}

// text names an owner as the register gives them.
func (o *Owner) text() string {
	switch {
	case o == nil || (o.Name == "" && o.Role == ""):
		return "no owner recorded"
	case o.Role == "":
		return "owner " + o.Name
	case o.Name == "":
		return "owner role " + o.Role
	}
	return "owner " + o.Name + " (" + o.Role + ")"
}

// stateText says what state a capability is in and when it was last confirmed.
func (c Capability) stateText() string {
	state := cmp.Or(c.State, "no state")
	if c.ConfirmedAt <= 0 {
		return state + " · never confirmed"
	}
	return state + " · confirmed " + formatDate(c.ConfirmedAt)
}

// realizedText says what of the release realises a capability.
func (c Capability) realizedText() string {
	parts := make([]string, 0, len(c.RealizedBy))
	for _, r := range c.RealizedBy {
		if r.Kind == RealizedByWorker {
			parts = append(parts, "worker "+r.Worker)
			continue
		}
		parts = append(parts, "process "+r.ProcessID)
	}
	return "realised by " + strings.Join(parts, ", ")
}

// measuresText names a capability's declared KPIs and SLAs.
func (c Capability) measuresText() string {
	var parts []string
	for _, k := range c.KPIs {
		text := "KPI " + k.Name
		if k.Goal != "" {
			text += " (" + k.Goal + ")"
		}
		parts = append(parts, text)
	}
	for _, s := range c.SLAs {
		text := "SLA " + s.Name
		if s.Threshold != "" {
			text += " (" + s.Threshold + ")"
		}
		parts = append(parts, text)
	}
	return strings.Join(parts, "; ")
}

// stagesText writes a value stream's stages in order, marking those that use a
// capability of the release.
func (v ValueStream) stagesText() string {
	names := make([]string, 0, len(v.Stages))
	for _, st := range v.Stages {
		name := st.Name
		if len(st.Capabilities) > 0 {
			name = "[" + name + "]"
		}
		names = append(names, name)
	}
	return strings.Join(names, " → ")
}

// usesText names the stages of a value stream that use the release's capabilities.
func (v ValueStream) usesText(capabilityName func(key string) string) string {
	var parts []string
	for _, st := range v.Stages {
		for _, key := range st.Capabilities {
			parts = append(parts, capabilityName(key)+" at "+st.Name)
		}
	}
	return "uses " + strings.Join(parts, ", ")
}
