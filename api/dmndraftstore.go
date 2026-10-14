package api

import (
	"github.com/pblumer/atlas/api/sidecar"
)

// dmnDraft is decision work in progress: DMN XML kept without writing the model
// every reference resolves to (ADR-0321).
//
// A decision artifact has one layer more than a BPMN diagram. A diagram is a draft
// until it is deployed; a decision is a draft, then a *model* under a handle that
// other references, other applications and the business-rule-task picker all
// resolve, and only then a deployed decision (ADR-0319). Before this store there
// was nowhere to put unfinished work: every save wrote the shared model, so a
// half-typed FEEL expression refused a colleague's publish of the same application
// and offered its half-named output to the next business rule task that adopted it.
//
// A draft exists only while it differs from the model: writing the model clears it.
// That is what makes the Explorer's "Draft" marker mean something — a decision
// carrying one has work in it nothing else can see yet.
type dmnDraft struct {
	// ID is the key. For a decision that already exists in the model it is that
	// decision's dmnRef id, so "does this decision have unsaved work?" is one Get.
	// A decision that has never been written to the model has no reference, so its
	// draft is minted a key of its own, prefixed dmnDraftIDPrefix, which the two
	// spaces cannot collide across.
	ID string `json:"id"`
	// Name is what the model calls itself, read out of the XML for the listing: the
	// <definitions name>, falling back to a decision's name while the model has none.
	Name string `json:"name"`
	// RefID is the reference this draft edits; empty for a decision that is not in
	// the model yet. When set it equals ID — it is carried explicitly so a reader
	// does not have to know that.
	RefID string `json:"refId,omitempty"`
	// ModelRef is the model handle behind that reference, for the editor's chip and
	// so a save can update the model in place rather than deriving a new handle.
	ModelRef string `json:"modelRef,omitempty"`
	// ProjectID is the application this draft is filed into (ADR-0034); empty means
	// Ungrouped.
	ProjectID string `json:"projectId,omitempty"`
	// OwnerID is the creator, stamped on first save (ADR-0071). It governs access
	// only while the draft is Ungrouped; under an application that scope governs.
	OwnerID string `json:"ownerId,omitempty"`
	SavedAt int64  `json:"savedAt"`
	XML     string `json:"xml"`
}

// dmnDraftIDPrefix marks a draft key that is the draft's own rather than a
// reference's. A decision not yet in the model has no reference to be keyed by, and
// the prefix is what keeps that minted key from ever being read as one.
const dmnDraftIDPrefix = "dd-"

// dmnDraftStore is a durable store for decision drafts, one JSON file per id under
// dmn-drafts/ — the on-disk sidecar approach every design-time store uses
// (ADR-0019). Like them it is owned solely by the server's run-loop goroutine, so
// it needs no locking of its own.
//
// It is deliberately its own store rather than a second kind of record in drafts/:
// that directory is keyed by process id and read by the BPMN publish path, the
// collaborative-session machinery (ADR-0140), the source export and the MIM import,
// and a discriminator six readers must honour is a rule the store does not hold.
type dmnDraftStore = sidecar.Store[dmnDraft]

// newDmnDraftStore opens (creating if needed) the decision-drafts directory. Drafts
// list most recently saved first, the order the Modeler shows them in.
func newDmnDraftStore(dir string) (*dmnDraftStore, error) {
	return sidecar.NewStore(dir, "dmndraftstore",
		func(rec dmnDraft) string { return rec.ID },
		sidecar.Order(func(a, b dmnDraft) bool { return a.SavedAt > b.SavedAt }),
	)
}
