package capability

import "time"

// The two records of the business architecture (ADR-0305):
// a business capability, and the value stream whose stages are performed by
// capabilities.
//
// Both are design-time state. Nothing here reaches the event log, the processor or
// recovery, and applyToState never sees one (I2, I4).

// Capability is one business capability: what the organisation must be able to do,
// stated independently of how it is currently done.
//
// The shape is the method's own definition — scope, input, output, owner, resources,
// metrics and controls — and two of its rules are structural rather than advisory:
//
//   - There is no parent, and no children. The method's advice is to resist a
//     capability hierarchy, because an "end-to-end" capability is regularly invoked
//     from inside another one, so any tree is wrong from some direction, and the
//     argument about which tree is right consumes the time the architecture was meant
//     to save. An end-to-end process is a capability carrying a Tag. Refusing the
//     field is what keeps that a property of the system; TestCapabilityHasNoParent
//     holds it.
//   - Requires names other capabilities as black boxes. It is declared, never derived
//     from the model: from the caller's side the required capability is normally a
//     service task or a message, not a call activity, so a derived graph would
//     systematically miss it. The gap report compares the two and never merges them.
type Capability struct {
	// Key is the identity, everywhere: the URL, the filename, what Requires and a
	// value stream's stages name, and what an export carries. There is deliberately no
	// second local id — a random one would make an exported document unreadable and
	// force an import into another installation to remap it, and the map is meant to
	// travel. The cost is that the key is not renameable in place; see the record.
	Key string `json:"key"`
	// Name is what a person calls it; Summary is the one line a list shows.
	Name    string `json:"name"`
	Summary string `json:"summary,omitempty"`
	// Scope says what this capability is responsible for and — the half that earns
	// the field — what it is not. It is where boundary arguments are settled.
	Scope string `json:"scope,omitempty"`
	// Inputs and Outputs are the interface: what triggers the capability and with
	// what data, and what it hands back.
	Inputs  []Interface `json:"inputs,omitempty"`
	Outputs []Interface `json:"outputs,omitempty"`
	// Owner is the *business* owner, accountable for how the capability performs.
	// Not the application's sharing scope, which decides who may open and change a
	// model; those are regularly different people.
	Owner Owner `json:"owner"`
	// Resources are the teams, systems and other capabilities this one draws on.
	Resources []Resource `json:"resources,omitempty"`
	// Realizations are how it is currently done. Empty is a meaningful state and the
	// single most useful line in the record: it says the work is not automated here.
	Realizations []Realization `json:"realizations,omitempty"`
	// Requires are the capability keys this one depends on, as black boxes.
	Requires []string `json:"requires,omitempty"`
	// KPIs and SLAs are declarations. Nothing in this build computes one, and no read
	// returns a live figure — measurement is a later slice, and saying so here is what
	// stops the field from being read as a number.
	KPIs []KPI `json:"kpis,omitempty"`
	SLAs []SLA `json:"slas,omitempty"`
	// Tags are the only classification there is. A view of the map — by business area,
	// by "implements an end-to-end process", by an imported reference model's level —
	// is a tag query, never a tree walk.
	Tags []string `json:"tags,omitempty"`
	// State is where the capability is in its own life: proposed, active, deprecated.
	// A field somebody sets, not a lifecycle Atlas drives.
	State string `json:"state"`
	// Confirmation is when a person last said this record still describes reality, and
	// who said it. It is set by an explicit confirmation and by no edit — see
	// [Confirmation] and ADR-0304.
	Confirmation Confirmation `json:"confirmation"`
	// Revision is optimistic concurrency: a write against a stale one is refused
	// rather than silently overwriting somebody else's edit.
	Revision  int64  `json:"revision"`
	CreatedAt int64  `json:"createdAt"`
	CreatedBy string `json:"createdBy,omitempty"`
	UpdatedAt int64  `json:"updatedAt"`
	UpdatedBy string `json:"updatedBy,omitempty"`
}

// Interface is one side of a capability's contract: a trigger it accepts or a
// result it hands back.
type Interface struct {
	Name string `json:"name"`
	// Kind is how it is reached: an API call, an event, a message, or a human being
	// handed something. Manual is a first-class kind because a capability nobody has
	// automated still has an interface, and it is the one worth writing down.
	Kind        string `json:"kind"`
	Description string `json:"description,omitempty"`
}

// Owner is who is accountable, in business terms. Free text with an optional Atlas
// account, and in that order deliberately: the person accountable for a capability
// is frequently an SVP or a department head who has never signed in here, and
// demanding a principal would either exclude them or fill the directory with ghosts.
type Owner struct {
	Name    string `json:"name,omitempty"`
	Role    string `json:"role,omitempty"`
	Contact string `json:"contact,omitempty"`
	// Username links the owner to an Atlas account where there is one. Advisory: it
	// grants nothing and is not resolved against the user store, because an owner who
	// leaves must not make the record unreadable.
	Username string `json:"username,omitempty"`
}

// Resource is something a capability draws on: a team, a system, or another
// capability.
type Resource struct {
	Kind string `json:"kind"`
	Name string `json:"name"`
	// Ref points at the thing where Atlas has a name for it — a capability key for
	// kind "capability", a Worker reference or a system identifier otherwise. Free
	// text; a resource is a description of reality, not a foreign key.
	Ref string `json:"ref,omitempty"`
}

// Realization is how a capability is currently done. Four kinds, and the two that
// are not Atlas resources are the reason this edge lives on the record rather than
// in a BPMN model: a capability performed by a purchased system or by a person is
// the normal state before the work starts, which is exactly when the map is most
// useful.
type Realization struct {
	Kind string `json:"kind"`
	// ApplicationKey and ProcessID address a process realization. The application key
	// is the portable one (ADR-0134), not a local id, so the reference survives the
	// map being moved to another installation.
	ApplicationKey string `json:"applicationKey,omitempty"`
	ProcessID      string `json:"processId,omitempty"`
	// WorkerRef addresses a Worker realization: one configured target and identity of
	// a Worker Type (ADR-0203).
	WorkerRef string `json:"workerRef,omitempty"`
	// Note is what a system or manual realization is, in words. Required for those
	// two kinds, because "a system does it" with no name is not a realization.
	Note string `json:"note,omitempty"`
}

// KPI is a direction with a goal. Nobody is fined for missing one.
type KPI struct {
	Name   string `json:"name"`
	Metric string `json:"metric"`
	Goal   string `json:"goal,omitempty"`
	// Direction says which way is better, so a reader knows whether a number moving
	// up is good news. Empty where neither is.
	Direction string `json:"direction,omitempty"`
	Note      string `json:"note,omitempty"`
}

// SLA is a commitment: a threshold, a window and somebody it is owed to. The
// difference from a KPI is the element of commitment, and it is why an end-to-end
// target can be distributed across the capabilities beneath it without opening any
// of them.
type SLA struct {
	Name      string `json:"name"`
	Metric    string `json:"metric"`
	Threshold string `json:"threshold"`
	// ThresholdSeconds is the same threshold as a number, where the author chose to
	// give one. It is optional and deliberately separate from Threshold rather than
	// replacing it: "within five business days" is what the business agreed and what
	// belongs in the record, and no parser should be asked to decide what a business
	// day means here.
	//
	// Supplying it is what makes the SLA measurable — a measurement compares against
	// this and reports nothing when it is absent, rather than guessing. So the record
	// stays writable in prose by somebody who has no number, and rewards the one who
	// does.
	ThresholdSeconds int64  `json:"thresholdSeconds,omitempty"`
	Window           string `json:"window,omitempty"`
	// Scope is internal (a promise between two teams here) or external (a contract or
	// a regulator). The two carry different consequences, so the record keeps them
	// apart rather than leaving it to the wording.
	Scope        string `json:"scope"`
	Counterparty string `json:"counterparty,omitempty"`
	Note         string `json:"note,omitempty"`
}

// ValueStream is the high-level, ordered activity the organisation performs to meet
// a customer need. Its stages name the capabilities that perform them.
//
// Ordered, because the whole purpose of a value stream is to show the sequence;
// a capability list is unordered because its whole purpose is to be composable.
//
// The method's own inconsistency is accepted rather than engineered away: an
// end-to-end process spans several stages *and* is itself a capability, so such a
// capability is named by every stage it spans, and a stream cannot be zoomed into
// through a strict containment tree. Inventing a level to hide that would buy
// tidiness in the picture and lose information in the stream.
type ValueStream struct {
	Key         string   `json:"key"`
	Name        string   `json:"name"`
	Description string   `json:"description,omitempty"`
	Owner       Owner    `json:"owner"`
	Stages      []Stage  `json:"stages,omitempty"`
	KPIs        []KPI    `json:"kpis,omitempty"`
	Tags        []string `json:"tags,omitempty"`
	// Confirmation is the same freshness record a capability carries, for the same
	// reason: a stream's owner and KPIs decay exactly as a capability's do.
	Confirmation Confirmation `json:"confirmation"`
	Revision     int64        `json:"revision"`
	CreatedAt    int64        `json:"createdAt"`
	CreatedBy    string       `json:"createdBy,omitempty"`
	UpdatedAt    int64        `json:"updatedAt"`
	UpdatedBy    string       `json:"updatedBy,omitempty"`
}

// Stage is one step of a value stream and the capabilities that perform it.
type Stage struct {
	Key          string   `json:"key"`
	Name         string   `json:"name"`
	Description  string   `json:"description,omitempty"`
	Capabilities []string `json:"capabilities,omitempty"`
}

// Confirmation is the freshness half of a record: when a person last said it still
// describes reality, who said it, and who they asked.
//
// It exists because the gap report can check a realization against the deployment
// registry and can check nothing else. The owner, the scope and the SLAs — the fields
// anybody actually acts on — are prose about people and promises, and a map that is
// confidently wrong about those is worse than no map. Atlas cannot verify them; it can
// record when somebody last stood behind them, which is the honest thing it can do.
//
// The shape is the third instance of one pattern in this tree: a Worker Type's setup
// steps carry a checked date (ADR-0289) and a decision record's open question carries
// one (ADR-0293). Both of those are enforced by a Go test over content in this
// repository. This one cannot be — it is runtime data in somebody else's installation —
// so the gap report is the enforcement instead.
type Confirmation struct {
	// At is unix seconds, zero when the record has never been confirmed. Zero is not
	// "confirmed at the epoch": it is the honest statement that nobody has said this is
	// true since the field existed, and the report distinguishes the two in words.
	At int64 `json:"at,omitempty"`
	// By is the principal who confirmed. Empty on an unconfirmed record, and empty on a
	// confirmation made with auth off, where there is no principal to name.
	By string `json:"by,omitempty"`
	// With names who was *asked*, where that is somebody other than the confirmer.
	// Empty means the confirmer spoke for the record alone — a legitimate confirmation
	// and a weaker one, which this says rather than implying the opposite.
	//
	// It is another claim Atlas cannot verify, and it earns its place for the reason
	// Owner does: the alternative is that who was asked lives only in the head of
	// whoever asked them.
	With string `json:"with,omitempty"`
	// Note is one line saying what the review found, replaced on each confirmation
	// rather than appended. This area is a correlation surface, not a log (ADR-0189).
	Note string `json:"note,omitempty"`
}

// Confirmed reports whether anybody has ever confirmed the record.
func (c Confirmation) Confirmed() bool { return c.At > 0 }

// StaleAt reports whether the confirmation has lapsed as of now, given the
// installation's horizon in months. A record nobody has ever confirmed is stale: the
// whole point is that nobody has asserted it.
func (c Confirmation) StaleAt(now time.Time, horizonMonths int) bool {
	if !c.Confirmed() {
		return true
	}
	if horizonMonths <= 0 {
		return false // a horizon of zero or less switches the check off
	}
	return time.Unix(c.At, 0).AddDate(0, horizonMonths, 0).Before(now)
}

// CapabilitySummary is the list representation: enough to render a row and decide
// what to open, without the whole definition.
type CapabilitySummary struct {
	Key     string   `json:"key"`
	Name    string   `json:"name"`
	Summary string   `json:"summary,omitempty"`
	Owner   Owner    `json:"owner"`
	State   string   `json:"state"`
	Tags    []string `json:"tags,omitempty"`
	// RealizationCount and Realized answer the one question a list of capabilities is
	// opened to ask: which of these is anybody actually doing anything about.
	RealizationCount int  `json:"realizationCount"`
	Realized         bool `json:"realized"`
	RequiresCount    int  `json:"requiresCount"`
	KPICount         int  `json:"kpiCount"`
	SLACount         int  `json:"slaCount"`
	// ConfirmedAt and Stale are the review half of the row, the twin of Realized: one
	// list answers "what does nobody automate", the other "what has nobody re-read".
	ConfirmedAt int64  `json:"confirmedAt,omitempty"`
	Stale       bool   `json:"stale"`
	Revision    int64  `json:"revision"`
	UpdatedAt   int64  `json:"updatedAt"`
	UpdatedBy   string `json:"updatedBy,omitempty"`
}

func summarizeCapability(c Capability, now time.Time, horizonMonths int) CapabilitySummary {
	return CapabilitySummary{
		Key: c.Key, Name: c.Name, Summary: c.Summary, Owner: c.Owner,
		State: c.State, Tags: c.Tags,
		RealizationCount: len(c.Realizations), Realized: len(c.Realizations) > 0,
		RequiresCount: len(c.Requires), KPICount: len(c.KPIs), SLACount: len(c.SLAs),
		ConfirmedAt: c.Confirmation.At,
		Stale:       c.Confirmation.StaleAt(now, horizonMonths),
		Revision:    c.Revision, UpdatedAt: c.UpdatedAt, UpdatedBy: c.UpdatedBy,
	}
}

// ValueStreamSummary is the list representation of a value stream.
type ValueStreamSummary struct {
	Key         string   `json:"key"`
	Name        string   `json:"name"`
	Description string   `json:"description,omitempty"`
	Owner       Owner    `json:"owner"`
	Tags        []string `json:"tags,omitempty"`
	StageCount  int      `json:"stageCount"`
	// CapabilityCount is how many distinct capabilities the stages name — the
	// stream's breadth, which the stage count alone does not give.
	CapabilityCount int    `json:"capabilityCount"`
	KPICount        int    `json:"kpiCount"`
	ConfirmedAt     int64  `json:"confirmedAt,omitempty"`
	Stale           bool   `json:"stale"`
	Revision        int64  `json:"revision"`
	UpdatedAt       int64  `json:"updatedAt"`
	UpdatedBy       string `json:"updatedBy,omitempty"`
}

func summarizeValueStream(v ValueStream, now time.Time, horizonMonths int) ValueStreamSummary {
	seen := map[string]bool{}
	for _, st := range v.Stages {
		for _, key := range st.Capabilities {
			seen[key] = true
		}
	}
	return ValueStreamSummary{
		Key: v.Key, Name: v.Name, Description: v.Description, Owner: v.Owner, Tags: v.Tags,
		StageCount: len(v.Stages), CapabilityCount: len(seen), KPICount: len(v.KPIs),
		ConfirmedAt: v.Confirmation.At,
		Stale:       v.Confirmation.StaleAt(now, horizonMonths),
		Revision:    v.Revision, UpdatedAt: v.UpdatedAt, UpdatedBy: v.UpdatedBy,
	}
}
