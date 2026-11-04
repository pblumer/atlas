package capability

import (
	"fmt"
	"slices"
	"strings"

	"github.com/pblumer/atlas/api/sidecar"
)

// Realizations an application bundle brings (ADR-draft-application-bundle-architecture).
//
// The capability map stays the server's: one flat register of what the organisation
// can do (ADR-0305). An application does not own a capability; it realizes some. What
// it realizes travels in its bundle and is filed here, one record per application,
// beside the map rather than inside it:
//
//   - the application owns these realizations — an import writes and replaces them, a
//     deletion removes them, and nobody edits them through the capability routes;
//   - the capability record keeps the realizations a person wrote by hand, and every
//     read of the map sees both (WithBundled), each bundled one marked with the
//     application that brought it (Realization.BundledBy).
//
// A bundled realization names an Atlas resource only — a process of the application or
// a configured Worker. A product is filed under capabilities by its catalogue
// (ADR-0524: a Marktleistung's category and product group are capability keys), so a
// bundle need not say it twice. System and manual realizations describe work outside
// Atlas and stay on the record, where a person writes them.
//
// Design-time state throughout: a sidecar record, never an event, never seen by
// recovery (I2, I4).

// BundledRealization is one realization as a bundle states it.
type BundledRealization struct {
	// Capability is the key of the capability realized, as the import settled it: the
	// bundle's own key, or the existing key the caller mapped it onto.
	Capability string `json:"capability"`
	// Kind is process or worker.
	Kind string `json:"kind"`
	// ProcessID is a process of the application, for a process realization.
	ProcessID string `json:"processId,omitempty"`
	// WorkerRef names a configured Worker, for a worker realization.
	WorkerRef string `json:"workerRef,omitempty"`
}

// ApplicationRealizations is the record of one application: every realization its
// last import brought.
type ApplicationRealizations struct {
	// ApplicationID is the owning application here; it is the record's key.
	ApplicationID string `json:"applicationId"`
	// ApplicationKey is its portable key (ADR-0134), what a process realization is
	// matched by once it is read as part of the map.
	ApplicationKey string               `json:"applicationKey"`
	Realizations   []BundledRealization `json:"realizations"`
	UpdatedAt      int64                `json:"updatedAt"`
	UpdatedBy      string               `json:"updatedBy,omitempty"`
}

// RealizationStore holds one ApplicationRealizations per application, keyed by the
// application's id.
type RealizationStore struct {
	*sidecar.Store[ApplicationRealizations]
}

// NewRealizationStore opens the directory of bundled realizations.
func NewRealizationStore(dir string) (*RealizationStore, error) {
	store, err := sidecar.NewStore(dir, "realizationstore",
		func(r ApplicationRealizations) string { return r.ApplicationID },
		sidecar.Order(func(a, b ApplicationRealizations) bool {
			if a.ApplicationKey != b.ApplicationKey {
				return a.ApplicationKey < b.ApplicationKey
			}
			return a.ApplicationID < b.ApplicationID
		}),
	)
	if err != nil {
		return nil, err
	}
	return &RealizationStore{store}, nil
}

// bundledKinds are the realization kinds a bundle may state.
var bundledKinds = []string{RealizationProcess, RealizationWorker}

// NormalizeBundledRealizations trims every field, drops exact duplicates and sorts
// the list, so two bundles that say the same thing compare equal however they are
// written.
func NormalizeBundledRealizations(list []BundledRealization) []BundledRealization {
	out := make([]BundledRealization, 0, len(list))
	seen := map[BundledRealization]bool{}
	for _, r := range list {
		r.Capability = strings.TrimSpace(r.Capability)
		r.Kind = strings.TrimSpace(r.Kind)
		r.ProcessID = strings.TrimSpace(r.ProcessID)
		r.WorkerRef = strings.TrimSpace(r.WorkerRef)
		if seen[r] {
			continue
		}
		seen[r] = true
		out = append(out, r)
	}
	slices.SortFunc(out, func(a, b BundledRealization) int {
		return strings.Compare(a.Capability+"\x00"+a.Kind+"\x00"+a.ProcessID+"\x00"+a.WorkerRef,
			b.Capability+"\x00"+b.Kind+"\x00"+b.ProcessID+"\x00"+b.WorkerRef)
	})
	return out
}

// ValidateBundledRealizations checks a normalized list. processes is the set of process
// ids the bundle carries: a process realization must name one of them, because a
// bundle speaks for its own processes only.
func ValidateBundledRealizations(list []BundledRealization, processes map[string]bool) []string {
	var findings []string
	for i, r := range list {
		n := i + 1
		if !validKey(r.Capability) {
			findings = append(findings, fmt.Sprintf("realization %d: capability %q is not a capability key", n, r.Capability))
		}
		switch r.Kind {
		case RealizationProcess:
			if r.ProcessID == "" || r.WorkerRef != "" {
				findings = append(findings, fmt.Sprintf("realization %d: a process realization names a processId and no workerRef", n))
			} else if !processes[r.ProcessID] {
				findings = append(findings, fmt.Sprintf("realization %d: process %q is not a process of this application", n, r.ProcessID))
			}
		case RealizationWorker:
			if r.WorkerRef == "" || r.ProcessID != "" {
				findings = append(findings, fmt.Sprintf("realization %d: a worker realization names a workerRef and no processId", n))
			}
		default:
			findings = append(findings, fmt.Sprintf("realization %d: kind %q is not one a bundle states (%s); a system or manual realization is written on the capability",
				n, r.Kind, vocabulary(bundledKinds)))
		}
	}
	return findings
}

// Overlay reads every application's realizations as they join the map: capability key
// to the realizations bundles brought for it, each marked with its application.
func (st *RealizationStore) Overlay() (map[string][]Realization, error) {
	if st == nil {
		return nil, nil
	}
	all, err := st.LoadAll()
	if err != nil {
		return nil, err
	}
	return OverlayOf(all), nil
}

// OverlayOf is Overlay over records already read.
func OverlayOf(records []ApplicationRealizations) map[string][]Realization {
	out := map[string][]Realization{}
	for _, rec := range records {
		for _, r := range rec.Realizations {
			real := Realization{Kind: r.Kind, BundledBy: rec.ApplicationKey}
			switch r.Kind {
			case RealizationProcess:
				real.ApplicationKey, real.ProcessID = rec.ApplicationKey, r.ProcessID
			case RealizationWorker:
				real.WorkerRef = r.WorkerRef
			}
			out[r.Capability] = append(out[r.Capability], real)
		}
	}
	return out
}

// WithBundled returns the capabilities as the map reads them: each record's own
// realizations followed by those bundles brought for it. The records are copied; the
// slice handed in is left as it was. A bundled realization the record already states by
// hand is not repeated.
func WithBundled(caps []Capability, overlay map[string][]Realization) []Capability {
	if len(overlay) == 0 {
		return caps
	}
	out := make([]Capability, len(caps))
	for i, c := range caps {
		out[i] = WithBundledOne(c, overlay)
	}
	return out
}

// WithBundledOne is WithBundled for one record.
func WithBundledOne(c Capability, overlay map[string][]Realization) Capability {
	extra := overlay[c.Key]
	if len(extra) == 0 {
		return c
	}
	merged := slices.Clone(c.Realizations)
	for _, r := range extra {
		if slices.ContainsFunc(merged, func(m Realization) bool {
			return m.Kind == r.Kind && m.ApplicationKey == r.ApplicationKey && m.ProcessID == r.ProcessID && m.WorkerRef == r.WorkerRef
		}) {
			continue
		}
		merged = append(merged, r)
	}
	c.Realizations = merged
	return c
}
