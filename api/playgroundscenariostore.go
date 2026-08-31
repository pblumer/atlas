package api

import (
	"github.com/pblumer/atlas/api/sidecar"
)

// playgroundScenario is a saved Playground run: the three requests that make it,
// what it must show, and the last report it produced.
//
// Spec is kept as an opaque JSON string for the same reason a form's schema is
// (ADR-0028): design-time storage has no business understanding a stub policy or
// an arrival profile, and a second copy of those shapes here would be a second
// place to keep in step with the Playground API. What the shape *is* lives in
// [github.com/pblumer/atlas/api/playground.Scenario], beside the endpoints it
// names.
//
// Baseline is the same bargain for the other direction: the report a run produced,
// stored so the next run can be set beside it. A comparison is a pure function of
// two reports, so keeping one is all "did that change help?" needs.
type playgroundScenario struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	// ProcessID is the diagram this scenario exercises. A scenario outlives the
	// sandbox it was built in, and it is listed by the draft it belongs to.
	ProcessID string `json:"processId"`
	ProjectID string `json:"projectId,omitempty"`
	// OwnerID is the creator's user id, stamped on first save (ADR-0071). It governs
	// access only while the scenario is Ungrouped; under a project, the project's
	// scope governs.
	OwnerID string `json:"ownerId,omitempty"`
	SavedAt int64  `json:"savedAt"`
	Spec    string `json:"spec"`
	// Baseline is the last recorded run's report, and BaselineAt when it was
	// recorded. Empty until a run is kept.
	Baseline   string `json:"baseline,omitempty"`
	BaselineAt int64  `json:"baselineAt,omitempty"`
}

// playgroundScenarioStore is a durable store for saved Playground scenarios, one
// JSON file per id. Like the draft (ADR-0021) and form (ADR-0028) stores it reuses
// the on-disk sidecar approach of the deployment store (ADR-0019) and is owned
// solely by the server's run-loop goroutine, so it needs no locking of its own.
type playgroundScenarioStore = sidecar.Store[playgroundScenario]

// newPlaygroundScenarioStore opens (creating if needed) the scenarios directory.
// Scenarios list most recently saved first.
func newPlaygroundScenarioStore(dir string) (*playgroundScenarioStore, error) {
	return sidecar.NewStore(dir, "playgroundscenariostore",
		func(rec playgroundScenario) string { return rec.ID },
		sidecar.Order(func(a, b playgroundScenario) bool { return a.SavedAt > b.SavedAt }),
	)
}
