// Package taskfolder serves the Tasks app's folders: the saved filters a person
// builds for themselves out of listboxes, so a recurring question ("what is open
// on customer enquiries?") becomes a place in the sidebar instead of something
// retyped into the search box every morning (ADR-0268).
//
// A folder stores a *rule*, not an expression. The rule is a small structured
// document — a match mode and a list of field/operator/value conditions — and the
// FEEL expression that decides which tasks belong is generated from it. That
// direction is the whole design: a generated expression can always be rendered
// back into the listboxes that produced it, and the person who built the folder
// never has to write, or read, a language to own their own worklist.
//
// Everything here is design-time state, like forms and projects: a folder never
// enters the event log, the processor or recovery. The single-writer boundary is
// the [runloop.Loop] the [Service] holds — every store access goes through it and
// there is no other route from here to shared state (I3, ADR-0002/0147).
package taskfolder

// Visibility says who else may see a folder. It is deliberately not an access
// control on the *tasks*: a folder is a saved question, and answering it still
// runs under the asker's own role. Sharing a folder shares the question.
const (
	// VisibilityPrivate is the default: only the owner sees the folder.
	VisibilityPrivate = "private"
	// VisibilityGroup shares the folder with one identity group (ADR-0180), which
	// is how a team lead builds the queues their team works from.
	VisibilityGroup = "group"
	// VisibilityOrg shares the folder with every signed-in identity.
	VisibilityOrg = "org"
)

// Match says how a rule's conditions combine.
const (
	MatchAll = "all" // every condition must hold
	MatchAny = "any" // at least one must hold
)

// Condition is one row of the folder editor: a field, an operator over it, and
// the value the operator compares against. Which of Value and Values carries the
// comparand depends on the operator — a single-choice operator uses Value, a
// multi-choice one Values, and an operator like "is overdue" needs neither.
type Condition struct {
	Field  string   `json:"field"`
	Op     string   `json:"op"`
	Value  string   `json:"value,omitempty"`
	Values []string `json:"values,omitempty"`
	// Unit qualifies a numeric Value where the number alone is ambiguous: the
	// instance-age conditions count hours or days.
	Unit string `json:"unit,omitempty"`
}

// Rule is what a folder actually stores. It is the source of truth; the FEEL
// expression is derived from it (see [Rule.FEEL]) and never stored, so the two
// cannot drift apart and a saved folder can always be reopened in the editor.
type Rule struct {
	Match      string      `json:"match"`
	Conditions []Condition `json:"conditions"`
}

// Folder is one saved filter in a person's sidebar.
type Folder struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	// Owner is the user id that created the folder, empty when the server runs
	// without authentication — where there is no identity, there is one shared set
	// of folders, which is the same bargain the rest of the app makes (ADR-0045).
	Owner      string `json:"owner,omitempty"`
	Visibility string `json:"visibility"`
	// GroupID names the identity group a group-visible folder is shared with.
	GroupID string `json:"groupId,omitempty"`
	// Position orders the sidebar. Ties fall back to creation order, so a listing
	// is deterministic even before anyone has reordered anything.
	Position  int   `json:"position"`
	Rule      Rule  `json:"rule"`
	CreatedAt int64 `json:"createdAt"`
	UpdatedAt int64 `json:"updatedAt"`
}

// VisibleTo reports whether a viewer may see this folder: their own, one shared
// with a group they are in, or one shared with the whole organisation.
func (f Folder) VisibleTo(userID string, groupIDs []string) bool {
	if f.Owner == userID {
		return true
	}
	switch f.Visibility {
	case VisibilityOrg:
		return true
	case VisibilityGroup:
		for _, g := range groupIDs {
			if g == f.GroupID {
				return true
			}
		}
	}
	return false
}

// EditableBy reports whether a viewer may change or delete this folder. Only the
// owner may: a shared folder is shared to be *used*, and a queue rewritten under
// the team by whoever opened it last is not a queue anybody can rely on. An admin
// override lives at the route layer, not here.
func (f Folder) EditableBy(userID string) bool { return f.Owner == userID }
