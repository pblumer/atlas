// Package catalog holds the product catalogue a self-service portal orders from:
// what may be ordered, how the parts of a product relate, and what a release
// proves before anything can be ordered against it.
//
// It is the first of the three models in ADR-0312.
// Design-time state throughout — nothing here reaches the event log, the processor
// or recovery, and none of the six engine invariants applies to it. What does apply
// is I5: everything provable when a catalogue is published is proved there, so
// ordering never interprets a graph (see [Publish]).
package catalog

// Lifecycle is the window in which an item may be ordered, in the server's own
// Unix time (nanoseconds, as every other timestamp in this tree).
//
// Zero means unbounded on that side, which is the ordinary case: most products are
// orderable from the moment they are published until somebody withdraws them. The
// window is data rather than a computed state so that a catalogue can be published
// ahead of the date it opens.
type Lifecycle struct {
	From  int64 `json:"from,omitempty"`
	Until int64 `json:"until,omitempty"`
}

// State is where an item stands in its own life.
//
// There is no deleted state, and that is load-bearing: an order placed years ago
// and an entitlement still held both resolve through the item, so an item is
// withdrawn rather than removed. A store that can delete one can orphan evidence.
type State string

const (
	// StateDraft is an item being written. It cannot be published.
	StateDraft State = "draft"
	// StateActive is an item a release may carry.
	StateActive State = "active"
	// StateWithdrawn is an item no longer orderable, still resolvable forever.
	StateWithdrawn State = "withdrawn"
)

// ApprovalKind names how an order line for an item gets approved.
//
// The four kinds are configuration a catalogue maintainer picks from a list; each
// is carried out by a process, so a fifth is modelled and registered under a name
// rather than built into the product. That is why this is a string and not an enum
// closed by the type system: KindNone, KindFixed, KindRole and KindSuperior are the
// shipped four, and a registered process name is as valid a value.
type ApprovalKind string

const (
	// KindNone orders without approval. A catalogue always answers which of its
	// items carry this, as a standing list — see ADR-0315.
	KindNone ApprovalKind = "none"
	// KindFixed routes to one named principal.
	KindFixed ApprovalKind = "fixed"
	// KindRole routes to whoever holds a named group; any one of them may approve.
	KindRole ApprovalKind = "role"
	// KindSuperior routes to the orderer's line manager, resolved from the directory.
	KindSuperior ApprovalKind = "superior"
)

// Approval is one item's approval rule.
type Approval struct {
	Kind ApprovalKind `json:"kind"`
	// Ref names the principal for KindFixed or the group for KindRole. It is empty
	// for the kinds that need no target, and Publish refuses a rule that needs one
	// and has none.
	Ref string `json:"ref,omitempty"`
}

// Variant is one orderable shape of an item — a laptop's size, a licence tier.
//
// Variants are unordered on purpose. An ordering would let the basket resolve a
// conflict by picking the higher one, and "higher" is meaningful for a licence tier
// and meaningless for Windows against Linux. The basket asks the orderer instead.
type Variant struct {
	ID    string            `json:"id"`
	Texts map[string]string `json:"texts"`
}

// TargetRef says what this item looks like in one target system: the group, the
// licence SKU, the role, named exactly as that system names it.
//
// It exists for the commissioning load and for the reconciliation after it
// (ADR-0333). A target system reports rights in its
// own vocabulary — "CN=VPN-Users", "ENTERPRISEPACK" — and an entitlement holds a
// catalogue item id. Something has to join the two, and the join has to be *data a
// person can read*, because the whole value of a load is a report somebody checks
// before it is applied. A join buried in a script produces a report that says
// "Alice holds VPN access" and gives the reader nothing to check it against.
//
// It is deliberately not the same knowledge as the provisioning process, though the
// two overlap. The process *acts*: it is what adds the membership. This is a
// *claim* about the target system, and a claim is the thing reconciliation can
// test. An installation that leaves this empty simply has an item no load will ever
// attribute — which is the right default, not a degraded one.
type TargetRef struct {
	// System is the target system's name in the installation's own vocabulary —
	// "entra", "ad", "jira". Atlas never interprets it; it only requires that an
	// observation and the item it should match agree on the spelling.
	System string `json:"system"`
	// Ref is the right as that system reports it. Compared literally, case and all:
	// a distinguished name differing only in case is two different strings to the
	// directory that issued them, and normalising here would attribute a right to
	// an item on a similarity Atlas invented.
	Ref string `json:"ref"`
}

// Item is a product or a service. Which of the two it is follows from its position
// in the graph, not from a field: an item nothing composes is a product, an item
// with no parts is a service, and an item with both is a bundle inside a bundle.
// Levels nest arbitrarily.
type Item struct {
	ID string `json:"id"`
	// HomeCatalog is the catalogue whose scope governs editing this item. An item
	// is referenced by catalogues rather than owned by one — it legitimately appears
	// in several — so scope cannot be inherited from "the catalogue it is in", and a
	// per-item member list is the per-artifact ACL ADR-0071 refused. One home, and
	// every other catalogue includes it read-only.
	HomeCatalog string `json:"homeCatalog"`
	State       State  `json:"state"`
	// Texts holds the item's name per language tag. A release proves it carries one
	// for every language its catalogue declares.
	Texts     map[string]string `json:"texts"`
	Lifecycle Lifecycle         `json:"lifecycle,omitempty"`
	Variants  []Variant         `json:"variants,omitempty"`
	Approval  Approval          `json:"approval"`
	// ProvisionProcess and DeprovisionProcess are the BPMN process ids bound to this
	// item. Both are required to publish: a catalogue that can only grant is not a
	// lifecycle, and the day somebody must revoke at scale is the wrong day to find
	// the process was never written.
	ProvisionProcess   string `json:"provisionProcess"`
	DeprovisionProcess string `json:"deprovisionProcess"`
	// MultipleAllowed says whether a principal may hold this item more than once —
	// two licences, two mailboxes. Where it is false the basket marks an item the
	// orderer already holds as held, and skips it.
	MultipleAllowed bool `json:"multipleAllowed,omitempty"`
	// Targets is what this item is called in the systems that actually hold it, and
	// it is what lets a commissioning load attribute a right it found to this item
	// (ADR-0333). Empty is the ordinary state for an
	// item nothing outside Atlas grants, and it means no load will ever name it.
	//
	// Several are allowed: one service is legitimately two groups. The same ref
	// twice is not, and [Publish] refuses it — an observation matching two items
	// cannot be attributed, and guessing between them would write evidence Atlas
	// invented.
	Targets   []TargetRef `json:"targets,omitempty"`
	CreatedAt int64       `json:"createdAt"`
	UpdatedAt int64       `json:"updatedAt"`
}

// EdgeKind distinguishes the two questions an edge can answer. They are different
// questions and were conflated in the record this implements: structure says what
// belongs to what, precedence says what must exist first.
type EdgeKind string

const (
	// EdgeComposition is structure: the part is integral to the whole, always
	// ordered with it, never deselectable.
	EdgeComposition EdgeKind = "composition"
	// EdgeAggregation is structure: the part is optional, separately orderable, and
	// may belong to several wholes.
	EdgeAggregation EdgeKind = "aggregation"
	// EdgeRequires is precedence: From cannot be provisioned before To is. This is
	// the edge the fulfilment order is computed over, and the only one whose
	// direction means "after".
	EdgeRequires EdgeKind = "requires"
)

// Structural reports whether this edge describes containment rather than order.
func (k EdgeKind) Structural() bool { return k == EdgeComposition || k == EdgeAggregation }

// Edge links two items. For the structural kinds From is the whole and To the part;
// for EdgeRequires From is the dependent item and To its precondition.
type Edge struct {
	From string   `json:"from"`
	To   string   `json:"to"`
	Kind EdgeKind `json:"kind"`
}

// MemberRole is what a member may do. The two values are ADR-0071's, unchanged:
// a viewer reads, an editor reads and writes. Ownership is the implicit third and
// highest, and is not listed.
type MemberRole string

const (
	// RoleViewer reads the catalogue.
	RoleViewer MemberRole = "viewer"
	// RoleEditor reads and changes it, publishing releases included.
	RoleEditor MemberRole = "editor"
)

// PrincipalRef names who a grant is for. Type is "user" or "group"; a group grant
// reaches everyone in it, resolved from the membership the principal already
// carries, so no store read happens inside an authorization check (ADR-0180).
type PrincipalRef struct {
	Type string `json:"type"`
	ID   string `json:"id"`
}

// Member is one grant on a catalogue.
type Member struct {
	Ref  PrincipalRef `json:"ref"`
	Role MemberRole   `json:"role"`
}

// Catalog is a named set of items, with the appearance, audience and precedence
// that decide who sees it and how.
type Catalog struct {
	ID    string            `json:"id"`
	Texts map[string]string `json:"texts"`
	// Rank resolves the one-catalogue-per-user rule against directory group
	// membership, which is many-to-many: the highest rank a user reaches wins.
	// Ranks are unique across catalogues, and Publish refuses a tie rather than
	// falling back to an id nobody chose.
	Rank int `json:"rank"`
	// Languages are the language tags this catalogue offers. A release proves every
	// item it carries has a text for each, and a companion record forbids declaring
	// one the portal interface does not have.
	Languages []string `json:"languages"`
	// Items are the item ids this catalogue offers, whatever their home.
	Items []string `json:"items"`
	// Groups are the directory or Atlas groups whose members reach this catalogue.
	Groups []string `json:"groups,omitempty"`
	// Members are the object axis (ADR-0071/0180/0278): the role says a caller may
	// maintain catalogues at all, this says which ones. Without it a product
	// manager can rebuild and publish every customer's catalogue, with nothing in
	// the way but not knowing an id — which ADR-0278 states plainly is not an
	// access control.
	Members []Member `json:"members,omitempty"`
	// Theme is this catalogue's appearance, empty for the instance's own. Set by
	// an administrator rather than by whoever maintains the catalogue
	// (decision 12).
	Theme Theme `json:"theme,omitempty"`
	// Edges are the structure and precedence between the items this catalogue
	// offers. They belong to the catalogue rather than to the items because they
	// are what its release is computed from, and because the same two products can
	// relate differently in two catalogues.
	Edges []Edge `json:"edges,omitempty"`
	// OwnerID is whoever created it, because creation is the one moment where
	// there is nobody to ask. The owner may always read, write and share.
	OwnerID   string `json:"ownerId,omitempty"`
	CreatedAt int64  `json:"createdAt"`
	UpdatedAt int64  `json:"updatedAt"`
}
