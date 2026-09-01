package panorama

import "sort"

// The observation projection (ADR-0189 §6, P4b).
//
// A Panorama model declares what an architecture *is*. This turns it into what
// the architecture is *doing*, without the document learning anything: the same
// bindings the resolver reads for names are read here for runtime facts, and the
// declarative XML is never touched by any of it. ADR-0189 is explicit about why —
// a drawing that stored health would be a second copy of the truth, and a stale
// one — so an observation is computed for one request and thrown away.
//
// This file is the pure half. Reading the stores, scanning instances and incidents,
// and deciding what this caller may see all happen on the API run loop before
// Observe is called; the projection itself is then deterministic and testable
// without an HTTP request or a loop turn.

// ObservationContractVersion is the shape of the document below. It is versioned
// for the same reason the binding contract is: what a consumer parses outlives the
// code that produced it, and a field that changes meaning without a version bump
// is a silent lie to whatever already reads it.
const ObservationContractVersion = 1

// Observation sources. A state is only as good as what produced it, so every
// observation names its source rather than presenting one anonymous verdict — an
// operator disagreeing with a finding needs to know where to go and look.
const (
	SourceDeployments = "deployments"
	SourceInstances   = "instances"
	SourceWorkers     = "workers"
	SourceReleases    = "releases"
	SourceNode        = "node"
	// SourceNone marks a binding no source on this server can say anything about.
	// It is a source in the payload precisely so that the absence is attributable:
	// "nobody looked" and "somebody looked and found nothing" are different
	// findings, and the second one is a claim.
	SourceNone = "none"
)

// Fact is one runtime fact the server observed about one Atlas resource. The
// server builds these; this package only projects them onto the elements that bind
// to them.
type Fact struct {
	Source string
	State  string
	Reason string
	// Detail is a small, flat set of specifics — a version, a count — for a reader
	// who wants the number behind the sentence. It is bounded on the way out
	// (see boundDetail): this document is fetched per model view, and an unbounded
	// detail object is how one becomes too big to render.
	Detail map[string]string
}

// Facts is what the server supplies, one lookup per binding kind, already filtered
// for the caller. As with [Catalog], a nil map and an empty map are different
// answers: nil means nothing on this server observes that kind at all, empty means
// it was looked at and holds none.
type Facts struct {
	Applications map[string]Fact
	Processes    map[string]Fact
	Connectors   map[string]Fact
	JobTypes     map[string]Fact
	Runtimes     map[string]Fact
	Targets      map[string]Fact
	Releases     map[string]Fact
}

func (f Facts) forKey(key string) map[string]Fact {
	switch key {
	case KeyApplicationID:
		return f.Applications
	case KeyProcessID:
		return f.Processes
	case KeyConnectorID:
		return f.Connectors
	case KeyJobType:
		return f.JobTypes
	case KeyRuntimeID:
		return f.Runtimes
	case KeyDeploymentTargetID:
		return f.Targets
	case KeyReleaseID:
		return f.Releases
	}
	return nil
}

// maxObservationDetail bounds one observation's detail object. The document is one
// response per model view, and a model can bind hundreds of elements, so the
// per-observation payload is what decides whether the whole thing stays renderable.
const maxObservationDetail = 8

// maxObservationDetailLen bounds one detail value. Values are short by
// construction — a version, a count, a name — and a long one is a symptom rather
// than something to render.
const maxObservationDetailLen = 200

// Observation is one bound value with what the server currently sees of it.
type Observation struct {
	ElementID   string `json:"elementId"`
	ElementType string `json:"elementType"`
	Key         string `json:"key"`
	Value       string `json:"value"`

	Source string `json:"source"`
	// State is one of ADR-0189 §6's seven; Severity is ADR-0211 §4's three-class
	// reading of it. Both travel: the class is what makes a hundred elements
	// legible at a glance, and the state is what somebody acts on.
	State    string `json:"state"`
	Severity string `json:"severity"`
	Reason   string `json:"reason,omitempty"`
	// ObservedAt is when this was read, in Unix seconds. It is on every observation
	// rather than only on the document, because the next slice resolves some of
	// them from remote nodes and those will not share one instant.
	ObservedAt int64             `json:"observedAt"`
	Detail     map[string]string `json:"detail,omitempty"`
}

// ObservationSummary counts the document by severity class, so a listing can say
// "3 need attention" without walking every element.
type ObservationSummary struct {
	OK        int `json:"ok"`
	Attention int `json:"attention"`
	Critical  int `json:"critical"`
	Unknown   int `json:"unknown"`
}

// ObservationDocument is what a caller asking "what is this model doing" gets.
type ObservationDocument struct {
	ContractVersion int `json:"contractVersion"`
	// ObservedAt is when this document was computed. Every observation in it was
	// read while serving this request — nothing here is cached — which is what
	// makes one timestamp meaningful for the whole document today.
	ObservedAt   int64              `json:"observedAt"`
	Observations []Observation      `json:"observations"`
	Summary      ObservationSummary `json:"summary"`
	// Unavailable names the observation states this build cannot produce at all.
	// It is the same list the landscape mesh publishes, from the same place, so the
	// two surfaces cannot disagree about what is unwatched.
	Unavailable []UnavailableState `json:"unavailable"`
	// Problems are the extractor's, carried through exactly as binding resolution
	// carries them: a declaration that was refused is as much a finding as one that
	// resolved.
	Problems []Problem `json:"problems"`
}

// Observe projects the server's facts onto the elements that bind to them.
//
// It never drops a bound value. A binding nothing can say anything about becomes
// an *unbound* observation naming why, not an absent one — the same rule binding
// resolution follows, and for the same reason: a model whose elements quietly
// vanished from the live view looks like a model with nothing wrong.
func Observe(set BindingSet, facts Facts, observedAt int64) ObservationDocument {
	doc := ObservationDocument{
		ContractVersion: set.ContractVersion,
		ObservedAt:      observedAt,
		Observations:    []Observation{},
		Unavailable:     unobservable,
		Problems:        []Problem{},
	}
	if doc.ContractVersion == 0 {
		doc.ContractVersion = BindingContractVersion
	}
	doc.Problems = append(doc.Problems, set.Problems...)

	for _, binding := range set.Bindings {
		lookup := facts.forKey(binding.Key)
		for _, value := range binding.Values {
			observation := Observation{
				ElementID: binding.ElementID, ElementType: binding.ElementType,
				Key: binding.Key, Value: value, ObservedAt: observedAt,
				Source: SourceNone, State: StateUnbound,
			}
			switch fact, found := lookup[value]; {
			case found:
				observation.Source, observation.State = fact.Source, fact.State
				observation.Reason, observation.Detail = fact.Reason, boundDetail(fact.Detail)
			case lookup == nil:
				// No source for this kind at all. Saying so is the whole of the
				// answer: reporting it as an absent resource would blame the model
				// for something this server never looked for.
				observation.Reason = "Nothing on this server observes " + binding.Key + "."
			default:
				// The kind is observed and this id is not among what it holds. That
				// is a real finding — the model names something the instance does
				// not have — but it is drift, not a runtime failure, and binding
				// resolution is where it is reported as missing.
				observation.Reason = "No resource with this id is present here, so there is nothing to observe."
			}
			observation.Severity = severityOf(observation.State)
			doc.Observations = append(doc.Observations, observation)
		}
	}

	// Stable order, so two identical requests produce a document that can be
	// diffed. Element id first, then key and value, which is the order somebody
	// reading it down the page expects.
	sort.SliceStable(doc.Observations, func(i, j int) bool {
		a, b := doc.Observations[i], doc.Observations[j]
		if a.ElementID != b.ElementID {
			return a.ElementID < b.ElementID
		}
		if a.Key != b.Key {
			return a.Key < b.Key
		}
		return a.Value < b.Value
	})

	for _, o := range doc.Observations {
		switch o.Severity {
		case SeverityOK:
			doc.Summary.OK++
		case SeverityAttention:
			doc.Summary.Attention++
		case SeverityCritical:
			doc.Summary.Critical++
		default:
			doc.Summary.Unknown++
		}
	}
	return doc
}

// boundDetail trims a fact's detail to what this document will carry. Entries are
// dropped in sorted-key order rather than in map order, so a detail object that is
// over the bound loses the same entries on every request instead of a different
// arbitrary subset each time — an answer that changed shape between two identical
// requests would be worse than one that is simply short.
func boundDetail(in map[string]string) map[string]string {
	if len(in) == 0 {
		return nil
	}
	keys := make([]string, 0, len(in))
	for key := range in {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	if len(keys) > maxObservationDetail {
		keys = keys[:maxObservationDetail]
	}
	out := make(map[string]string, len(keys))
	for _, key := range keys {
		value := in[key]
		if len(value) > maxObservationDetailLen {
			value = value[:maxObservationDetailLen]
		}
		out[key] = value
	}
	return out
}
