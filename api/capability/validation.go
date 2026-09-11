package capability

import (
	"fmt"
	"regexp"
	"strings"
)

// The vocabularies. Each is small, closed, and served to clients through the
// authoring subset, so a picker and a refusal cannot disagree about what a word is.

// Lifecycle states of a capability. A field somebody sets, not a lifecycle Atlas
// drives — Atlas is the engine an organisation would model an approval workflow *in*.
const (
	StateProposed   = "proposed"
	StateActive     = "active"
	StateDeprecated = "deprecated"
)

// Interface kinds: how a capability is reached, or hands its result back.
const (
	KindAPI     = "api"
	KindEvent   = "event"
	KindMessage = "message"
	// KindManual is a first-class kind. A capability nobody has automated still has
	// an interface — an email with the customer's details, a form on a clerk's desk —
	// and writing it down is what makes the automation of it a described change rather
	// than a discovery.
	KindManual = "manual"
)

// Resource kinds: what a capability draws on.
const (
	ResourceTeam       = "team"
	ResourceSystem     = "system"
	ResourceCapability = "capability"
)

// Realization kinds: how a capability is currently done.
const (
	RealizationProcess = "process"
	RealizationWorker  = "worker"
	RealizationSystem  = "system"
	RealizationManual  = "manual"
)

// KPI directions: which way is better.
const (
	DirectionUp   = "up"
	DirectionDown = "down"
)

// SLA scopes. Internal is a promise between two teams here; external is a contract
// or a regulator. They carry different consequences, so the record keeps them apart
// rather than leaving it to the wording.
const (
	SLAInternal = "internal"
	SLAExternal = "external"
)

var (
	states           = []string{StateProposed, StateActive, StateDeprecated}
	interfaceKinds   = []string{KindAPI, KindEvent, KindMessage, KindManual}
	resourceKinds    = []string{ResourceTeam, ResourceSystem, ResourceCapability}
	realizationKinds = []string{RealizationProcess, RealizationWorker, RealizationSystem, RealizationManual}
	directions       = []string{DirectionUp, DirectionDown}
	slaScopes        = []string{SLAInternal, SLAExternal}
)

// keyPattern is the shape of a capability or value-stream key.
//
// It is tight on purpose, because the key is three things at once: the identity in
// the URL, the name of the file the record lives in, and what another record's
// reference says. A key that could name a path is a key that could address a file
// outside the store, and the sidecar's own guard is the second line rather than the
// first — a refusal here says *why*, which a silent miss would not.
var keyPattern = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]{0,62}[a-z0-9])?$`)

func validKey(key string) bool { return keyPattern.MatchString(key) }

// oneOf reports whether v is in the vocabulary, and renders the vocabulary for a
// refusal that teaches it rather than only refusing.
func oneOf(v string, vocabulary []string) bool {
	for _, w := range vocabulary {
		if v == w {
			return true
		}
	}
	return false
}

func vocabulary(words []string) string { return strings.Join(words, ", ") }

// NormalizeCapability trims every field a person typed and drops the blanks a form
// leaves behind, so that validation and storage see one shape. It defaults an
// unstated State to proposed: a capability somebody has written down but not yet
// agreed is exactly what "proposed" means, and it is the honest default for a record
// that arrives without one.
func NormalizeCapability(c *Capability) {
	c.Key = strings.TrimSpace(c.Key)
	c.Name = strings.TrimSpace(c.Name)
	c.Summary = strings.TrimSpace(c.Summary)
	c.Scope = strings.TrimSpace(c.Scope)
	c.State = strings.TrimSpace(c.State)
	if c.State == "" {
		c.State = StateProposed
	}
	normalizeOwner(&c.Owner)
	for i := range c.Inputs {
		normalizeInterface(&c.Inputs[i])
	}
	for i := range c.Outputs {
		normalizeInterface(&c.Outputs[i])
	}
	for i := range c.Resources {
		c.Resources[i].Kind = strings.TrimSpace(c.Resources[i].Kind)
		c.Resources[i].Name = strings.TrimSpace(c.Resources[i].Name)
		c.Resources[i].Ref = strings.TrimSpace(c.Resources[i].Ref)
	}
	for i := range c.Realizations {
		r := &c.Realizations[i]
		r.Kind = strings.TrimSpace(r.Kind)
		r.ApplicationKey = strings.TrimSpace(r.ApplicationKey)
		r.ProcessID = strings.TrimSpace(r.ProcessID)
		r.WorkerRef = strings.TrimSpace(r.WorkerRef)
		r.Note = strings.TrimSpace(r.Note)
	}
	for i := range c.KPIs {
		normalizeKPI(&c.KPIs[i])
	}
	for i := range c.SLAs {
		s := &c.SLAs[i]
		s.Name = strings.TrimSpace(s.Name)
		s.Metric = strings.TrimSpace(s.Metric)
		s.Threshold = strings.TrimSpace(s.Threshold)
		s.Window = strings.TrimSpace(s.Window)
		s.Scope = strings.TrimSpace(s.Scope)
		s.Counterparty = strings.TrimSpace(s.Counterparty)
		s.Note = strings.TrimSpace(s.Note)
	}
	c.Requires = trimmedList(c.Requires)
	c.Tags = trimmedList(c.Tags)
}

// NormalizeValueStream is NormalizeCapability's twin for a stream and its stages.
func NormalizeValueStream(v *ValueStream) {
	v.Key = strings.TrimSpace(v.Key)
	v.Name = strings.TrimSpace(v.Name)
	v.Description = strings.TrimSpace(v.Description)
	normalizeOwner(&v.Owner)
	for i := range v.Stages {
		st := &v.Stages[i]
		st.Key = strings.TrimSpace(st.Key)
		st.Name = strings.TrimSpace(st.Name)
		st.Description = strings.TrimSpace(st.Description)
		st.Capabilities = trimmedList(st.Capabilities)
	}
	for i := range v.KPIs {
		normalizeKPI(&v.KPIs[i])
	}
	v.Tags = trimmedList(v.Tags)
}

func normalizeOwner(o *Owner) {
	o.Name = strings.TrimSpace(o.Name)
	o.Role = strings.TrimSpace(o.Role)
	o.Contact = strings.TrimSpace(o.Contact)
	o.Username = strings.TrimSpace(o.Username)
}

func normalizeInterface(i *Interface) {
	i.Name = strings.TrimSpace(i.Name)
	i.Kind = strings.TrimSpace(i.Kind)
	i.Description = strings.TrimSpace(i.Description)
}

func normalizeKPI(k *KPI) {
	k.Name = strings.TrimSpace(k.Name)
	k.Metric = strings.TrimSpace(k.Metric)
	k.Goal = strings.TrimSpace(k.Goal)
	k.Direction = strings.TrimSpace(k.Direction)
	k.Note = strings.TrimSpace(k.Note)
}

// trimmedList trims each entry and drops the empty ones, returning nil for a list
// with nothing left in it so the record serialises without the field.
func trimmedList(in []string) []string {
	out := make([]string, 0, len(in))
	for _, s := range in {
		if s = strings.TrimSpace(s); s != "" {
			out = append(out, s)
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// ValidateCapability reports everything wrong with a record, rather than the first
// thing: an author fixing one field at a time through a form would otherwise make one
// round trip per mistake.
//
// What it deliberately does *not* refuse is a capability with no realization, no
// KPI, no SLA and no interface. That is a capability somebody has named and not yet
// worked on, it is the majority of any new map, and it is the row the gap report
// exists to count. A validator that demanded completeness would make the map
// impossible to start and would be refusing the method's own first step.
func ValidateCapability(c Capability) []string {
	var findings []string
	if c.Key == "" {
		findings = append(findings, "key is required")
	} else if !validKey(c.Key) {
		findings = append(findings, fmt.Sprintf(
			"key %q is not a valid key: lower-case letters, digits and dashes, 1 to 64 characters, "+
				"starting and ending with a letter or digit", c.Key))
	}
	if c.Name == "" {
		findings = append(findings, "name is required")
	}
	if !oneOf(c.State, states) {
		findings = append(findings, fmt.Sprintf("state %q is not one of: %s", c.State, vocabulary(states)))
	}
	findings = append(findings, validateInterfaces("input", c.Inputs)...)
	findings = append(findings, validateInterfaces("output", c.Outputs)...)

	for i, r := range c.Resources {
		if r.Name == "" {
			findings = append(findings, fmt.Sprintf("resource %d: name is required", i+1))
		}
		if !oneOf(r.Kind, resourceKinds) {
			findings = append(findings, fmt.Sprintf("resource %d: kind %q is not one of: %s",
				i+1, r.Kind, vocabulary(resourceKinds)))
		}
	}

	for i, r := range c.Realizations {
		findings = append(findings, validateRealization(i+1, r)...)
	}

	seenRequires := map[string]bool{}
	for _, key := range c.Requires {
		switch {
		case key == c.Key:
			findings = append(findings, "requires names itself")
		case !validKey(key):
			findings = append(findings, fmt.Sprintf("requires names %q, which is not a valid capability key", key))
		case seenRequires[key]:
			findings = append(findings, fmt.Sprintf("requires names %q twice", key))
		}
		seenRequires[key] = true
	}

	findings = append(findings, validateKPIs(c.KPIs)...)

	for i, s := range c.SLAs {
		n := i + 1
		if s.Name == "" {
			findings = append(findings, fmt.Sprintf("sla %d: name is required", n))
		}
		if s.Metric == "" {
			findings = append(findings, fmt.Sprintf("sla %d: metric is required", n))
		}
		if s.Threshold == "" {
			findings = append(findings, fmt.Sprintf(
				"sla %d: threshold is required — an SLA without one is a KPI", n))
		}
		if !oneOf(s.Scope, slaScopes) {
			findings = append(findings, fmt.Sprintf("sla %d: scope %q is not one of: %s",
				n, s.Scope, vocabulary(slaScopes)))
		}
	}

	findings = append(findings, validateTags(c.Tags)...)
	return findings
}

func validateInterfaces(side string, list []Interface) []string {
	var findings []string
	for i, in := range list {
		if in.Name == "" {
			findings = append(findings, fmt.Sprintf("%s %d: name is required", side, i+1))
		}
		if !oneOf(in.Kind, interfaceKinds) {
			findings = append(findings, fmt.Sprintf("%s %d: kind %q is not one of: %s",
				side, i+1, in.Kind, vocabulary(interfaceKinds)))
		}
	}
	return findings
}

// validateRealization holds the one rule that makes a realization a statement rather
// than a gesture: each kind names the thing it realises the capability with.
func validateRealization(n int, r Realization) []string {
	var findings []string
	if !oneOf(r.Kind, realizationKinds) {
		return append(findings, fmt.Sprintf("realization %d: kind %q is not one of: %s",
			n, r.Kind, vocabulary(realizationKinds)))
	}
	switch r.Kind {
	case RealizationProcess:
		if r.ApplicationKey == "" {
			findings = append(findings, fmt.Sprintf(
				"realization %d: a process realization needs an applicationKey — the portable "+
					"application key, so the reference survives being moved to another installation", n))
		}
		if r.ProcessID == "" {
			findings = append(findings, fmt.Sprintf("realization %d: a process realization needs a processId", n))
		}
	case RealizationWorker:
		if r.WorkerRef == "" {
			findings = append(findings, fmt.Sprintf("realization %d: a worker realization needs a workerRef", n))
		}
	case RealizationSystem, RealizationManual:
		if r.Note == "" {
			findings = append(findings, fmt.Sprintf(
				"realization %d: a %s realization needs a note saying what it is — %q with no name "+
					"is not a realization", n, r.Kind, r.Kind))
		}
	}
	return findings
}

func validateKPIs(list []KPI) []string {
	var findings []string
	for i, k := range list {
		n := i + 1
		if k.Name == "" {
			findings = append(findings, fmt.Sprintf("kpi %d: name is required", n))
		}
		if k.Metric == "" {
			findings = append(findings, fmt.Sprintf("kpi %d: metric is required", n))
		}
		if k.Direction != "" && !oneOf(k.Direction, directions) {
			findings = append(findings, fmt.Sprintf("kpi %d: direction %q is not one of: %s",
				n, k.Direction, vocabulary(directions)))
		}
	}
	return findings
}

// validateTags is the shape check only. What a tag *means* is the installation's
// business, which is the whole point of tags being the only classification: a rule
// about their vocabulary here would be the hierarchy arriving by another door.
func validateTags(tags []string) []string {
	var findings []string
	seen := map[string]bool{}
	for _, t := range tags {
		switch {
		case strings.TrimSpace(t) == "":
			findings = append(findings, "tag is empty")
		case seen[t]:
			findings = append(findings, fmt.Sprintf("tag %q appears twice", t))
		}
		seen[t] = true
	}
	return findings
}

// ValidateValueStream reports everything wrong with a stream.
//
// It accepts the same capability key in several stages, and that is deliberate: an
// end-to-end capability spans several stages of a stream, which is the method's own
// accepted inconsistency. A validator "fixing" it would be enforcing a containment
// tree the method rejects.
func ValidateValueStream(v ValueStream) []string {
	var findings []string
	if v.Key == "" {
		findings = append(findings, "key is required")
	} else if !validKey(v.Key) {
		findings = append(findings, fmt.Sprintf(
			"key %q is not a valid key: lower-case letters, digits and dashes, 1 to 64 characters, "+
				"starting and ending with a letter or digit", v.Key))
	}
	if v.Name == "" {
		findings = append(findings, "name is required")
	}
	seenStage := map[string]bool{}
	for i, st := range v.Stages {
		n := i + 1
		switch {
		case st.Key == "":
			findings = append(findings, fmt.Sprintf("stage %d: key is required", n))
		case !validKey(st.Key):
			findings = append(findings, fmt.Sprintf("stage %d: key %q is not a valid key", n, st.Key))
		case seenStage[st.Key]:
			findings = append(findings, fmt.Sprintf("stage %d: key %q is used twice", n, st.Key))
		}
		seenStage[st.Key] = true
		if st.Name == "" {
			findings = append(findings, fmt.Sprintf("stage %d: name is required", n))
		}
		seenCap := map[string]bool{}
		for _, key := range st.Capabilities {
			switch {
			case !validKey(key):
				findings = append(findings, fmt.Sprintf(
					"stage %d: %q is not a valid capability key", n, key))
			case seenCap[key]:
				findings = append(findings, fmt.Sprintf("stage %d: capability %q is named twice", n, key))
			}
			seenCap[key] = true
		}
	}
	findings = append(findings, validateKPIs(v.KPIs)...)
	findings = append(findings, validateTags(v.Tags)...)
	return findings
}

// AuthoringSubset is every vocabulary this build accepts, served rather than
// duplicated in the browser. A picker offering a word the write path refuses is a
// promise the server breaks, which is why the list has one definition.
type AuthoringSubset struct {
	States           []string `json:"states"`
	InterfaceKinds   []string `json:"interfaceKinds"`
	ResourceKinds    []string `json:"resourceKinds"`
	RealizationKinds []string `json:"realizationKinds"`
	Directions       []string `json:"kpiDirections"`
	SLAScopes        []string `json:"slaScopes"`
	FindingKinds     []string `json:"gapFindingKinds"`
	// KeyPattern is the regular expression a key must match, so a form can refuse a
	// bad key while it is being typed with the same rule the server writes through.
	KeyPattern string `json:"keyPattern"`
	// DefaultHorizonMonths is how long a confirmation stays fresh where an installation
	// has not said otherwise. The horizon actually in force is an installation setting
	// and travels with the answer that applied it, never with this one — the subset
	// says what this build does, not how this server is configured.
	DefaultHorizonMonths int `json:"defaultConfirmationHorizonMonths"`
	// Hierarchy states, in the surface itself, that there is none. A client author
	// looking for a parent field finds the reason instead of assuming an oversight.
	Hierarchy string `json:"hierarchy"`
}

// Subset returns the authoring subset.
func Subset() AuthoringSubset {
	return AuthoringSubset{
		States: states, InterfaceKinds: interfaceKinds, ResourceKinds: resourceKinds,
		RealizationKinds: realizationKinds, Directions: directions, SLAScopes: slaScopes,
		FindingKinds:         FindingKinds(),
		KeyPattern:           keyPattern.String(),
		DefaultHorizonMonths: DefaultHorizonMonths,
		Hierarchy: "none: capabilities are a flat list and are classified by tags. An end-to-end " +
			"process is a capability carrying a tag, not a capability at a higher level.",
	}
}
