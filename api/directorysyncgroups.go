package api

import (
	"fmt"
	"sort"
	"strings"
)

// The group half of a directory synchronisation
// (ADR-draft-entra-directory-provisioning).
//
// A group is harder than an account for one reason: Atlas groups hold *Atlas* user
// ids, because that is what a scope grant resolves against (ADR-0180), while the
// directory reports its own object ids. The translation needs the accounts, and the
// accounts and the memberships arrive in the same message with no guaranteed order
// between them.
//
// So a mirrored group keeps both. ExternalMembers is what the directory said, id for
// id, resolved or not; Members is the subset that resolves, recomputed from it on
// every run. Nothing is discarded and nothing is retried: an id that resolves to
// nobody today simply resolves tomorrow, when the account it names has been read.
// That is worth stating plainly, because the obvious implementation — translate on
// arrival, drop what does not translate — loses the membership permanently. A
// change-tracking read never mentions it again.

// pendingGroup is one mirrored group as this run would leave it, plus what the
// report needs to know about how it got that way.
type pendingGroup struct {
	rec group
	// orig is the record as the store holds it, so "did this run change anything"
	// is answered against what was there rather than against a second lookup.
	orig group
	// isNew marks a group this run would create.
	isNew bool
	// cleared marks one the directory says is gone. Its membership is emptied and the
	// record is kept, for the reason a departed account is disabled and not deleted:
	// a group is the anchor of the grants made through it.
	cleared bool
}

// pendingFor returns the working copy of a mirrored group, seeding it from the store
// or minting a new record.
func (d *directoryDecider) pendingFor(oid string) (*pendingGroup, error) {
	if ps, ok := d.groupState[oid]; ok {
		return ps, nil
	}
	ps := &pendingGroup{}
	if existing, ok := d.groupByDirectory[oid]; ok {
		ps.orig = existing
		ps.rec = existing
		ps.rec.Members = append([]string(nil), existing.Members...)
		ps.rec.ExternalMembers = append([]string(nil), existing.ExternalMembers...)
	} else {
		id, err := d.newGroupID()
		if err != nil {
			return nil, err
		}
		ps.isNew = true
		ps.rec = group{ID: id, Source: SourceEntra, ExternalID: oid, CreatedAt: d.now}
	}
	d.groupState[oid] = ps
	d.groupOrder = append(d.groupOrder, oid)
	return ps, nil
}

// decideGroup folds one object from /groups/delta into the working copy. It emits no
// decision: what a group's record ends up being depends on accounts that may be
// decided after it, so every group decision is made once, at the end, by
// [directoryDecider.resolveMemberships].
func (d *directoryDecider) decideGroup(mg directoryGroup) error {
	oid := directoryIDOf(mg.ID)
	if oid == "" {
		d.counts.GroupsRefused++
		d.note(directoryNote{Kind: noteRefusal, Subject: strings.TrimSpace(mg.DisplayName),
			Detail: "the directory returned a group with no id; nothing can be matched to it"})
		return nil
	}
	ps, err := d.pendingFor(oid)
	if err != nil {
		return err
	}
	if mg.Removed != nil {
		if ps.isNew {
			// A group this installation never mirrored, reported as gone. Creating an empty
			// record for it would be manufacturing a group out of its own deletion.
			d.dropPending(oid)
			d.note(directoryNote{Kind: noteSkipped, ObjectID: oid,
				Detail: "the directory reports this group as removed and no group here mirrors it"})
			return nil
		}
		ps.cleared = true
		dropped := len(ps.rec.Members)
		ps.rec.ExternalMembers = nil
		d.note(directoryNote{Kind: noteDisable, ObjectID: oid, Subject: ps.rec.Name,
			Detail: fmt.Sprintf("the directory no longer holds this group%s; its %d membership(s) are "+
				"withdrawn and the group itself is kept, because grants were made through it",
				removalReason(mg.Removed), dropped)})
		return nil
	}
	if name := strings.TrimSpace(mg.DisplayName); name != "" && name != ps.rec.Name {
		ps.rec.Name = name
		d.noteNameCollision(oid, name, ps.rec.ID)
	}
	if ps.isNew && ps.rec.Name == "" {
		// A group with no name has nothing an administrator could recognise it by, and
		// a mirror that invented one would be naming somebody else's object.
		d.dropPending(oid)
		d.counts.GroupsRefused++
		d.note(directoryNote{Kind: noteRefusal, ObjectID: oid,
			Detail: "the directory gave this group no displayName; widen the $select of the delta query"})
		return nil
	}
	d.applyMemberDelta(ps, mg.Members, oid)
	return nil
}

// dropPending forgets a group the run decided not to touch after all, so nothing
// downstream has to know that a working copy can be abandoned.
func (d *directoryDecider) dropPending(oid string) {
	delete(d.groupState, oid)
	kept := d.groupOrder[:0]
	for _, id := range d.groupOrder {
		if id != oid {
			kept = append(kept, id)
		}
	}
	d.groupOrder = kept
}

// applyMemberDelta amends the directory's own membership set with what this page
// reported: ids that joined, ids that left.
//
// It is an amendment and not a replacement because that is what Graph sends — a full
// membership on the enumerating run, only the changes afterwards — and because an
// amendment is what makes a repeated delivery harmless. Adding an id already in the
// set leaves the set alone.
func (d *directoryDecider) applyMemberDelta(ps *pendingGroup, refs []directoryMemberRef, oid string) {
	set := map[string]bool{}
	for _, id := range ps.rec.ExternalMembers {
		set[id] = true
	}
	for _, ref := range refs {
		mid := directoryIDOf(ref.ID)
		if mid == "" {
			continue
		}
		if kind := memberKind(ref.Type); kind != "" {
			// A group's members are not only people: nested groups, service principals and
			// devices are members too, and none of them is an account here. They are dropped
			// from the set rather than kept as ids that can never resolve, because an
			// unresolved id is a line in every future report and this one would never stop.
			d.note(directoryNote{Kind: noteSkipped, ObjectID: oid, Subject: ps.rec.Name,
				Detail: fmt.Sprintf("member %s is a %s, which is not an account here", mid, kind)})
			continue
		}
		if ref.Removed != nil {
			delete(set, mid)
			continue
		}
		set[mid] = true
	}
	ps.rec.ExternalMembers = sortedIDs(set)
}

// memberKind names the member types this mirror has no account for, and returns ""
// for the one it does. An absent @odata.type is read as a user: Graph sends the type
// on a delta page, and refusing a member because an annotation was missing would
// drop a real person over a missing string.
func memberKind(odataType string) string {
	switch strings.ToLower(strings.TrimSpace(odataType)) {
	case "", "#microsoft.graph.user":
		return ""
	case "#microsoft.graph.group":
		return "nested group"
	case "#microsoft.graph.serviceprincipal":
		return "service principal"
	case "#microsoft.graph.device":
		return "device"
	case "#microsoft.graph.orgcontact":
		return "directory contact"
	default:
		return strings.TrimPrefix(strings.TrimSpace(odataType), "#microsoft.graph.")
	}
}

// noteNameCollision says so when a mirrored group takes a name a group here already
// uses. It is not refused: the directory owns the name, and refusing would lose the
// group for good. It is reported so somebody renames one of the two before a person
// picks the wrong one out of a list.
func (d *directoryDecider) noteNameCollision(oid, name, selfID string) {
	for _, g := range d.groups {
		if g.ID != selfID && strings.EqualFold(g.Name, name) {
			d.note(directoryNote{Kind: noteNameTaken, ObjectID: oid, Subject: name,
				Detail: fmt.Sprintf("a %s group here is already called this; both will be listed under "+
					"the same name until one is renamed", sourceLabel(g.Source))})
			return
		}
	}
}

// resolveMemberships is the pass that makes the order of a run irrelevant: every
// mirrored group — the ones this message touched and the ones it did not — has its
// Members recomputed from its ExternalMembers against the accounts that now exist,
// including the accounts this same plan would create.
//
// Walking the groups the message left alone is the point rather than an oversight. A
// membership whose account had not been read yet is held in ExternalMembers, and this
// is the run where it finally resolves; nothing scheduled that retry and nothing had
// to remember it.
func (d *directoryDecider) resolveMemberships() {
	// Seed whatever the message did not mention, in store order, so the walk is
	// complete and the plan is the same plan every time it is built.
	for _, g := range d.groups {
		if g.Source != SourceEntra || g.ExternalID == "" {
			continue
		}
		if _, seen := d.groupState[g.ExternalID]; seen {
			continue
		}
		ps := &pendingGroup{rec: g, orig: g}
		ps.rec.Members = append([]string(nil), g.Members...)
		ps.rec.ExternalMembers = append([]string(nil), g.ExternalMembers...)
		d.groupState[g.ExternalID] = ps
		d.groupOrder = append(d.groupOrder, g.ExternalID)
	}

	for _, oid := range d.groupOrder {
		d.resolveOne(oid, d.groupState[oid])
	}
}

// resolveOne recomputes one group's Members and decides what to do with it.
func (d *directoryDecider) resolveOne(oid string, ps *pendingGroup) {
	before := ps.rec.Members
	resolved := make([]string, 0, len(ps.rec.ExternalMembers))
	for _, mid := range ps.rec.ExternalMembers {
		u, ok := d.byDirectory[mid]
		if !ok {
			d.counts.MembersUnresolved++
			d.note(directoryNote{Kind: noteUnresolved, ObjectID: oid, Subject: ps.rec.Name,
				Detail: fmt.Sprintf("member %s has no account here yet; the membership is held and "+
					"resolves on the run that reads the account", mid)})
			continue
		}
		d.counts.MembersResolved++
		resolved = append(resolved, u.ID)
	}
	sort.Strings(resolved)

	if !ps.cleared && !ps.isNew {
		for _, id := range before {
			if !containsID(resolved, id) {
				d.note(directoryNote{Kind: noteMemberRemove, ObjectID: oid, Subject: ps.rec.Name, UserID: id,
					Detail: "this membership is not in the directory's own list for the group, so the " +
						"mirror withdraws it"})
			}
		}
	}
	ps.rec.Members = resolved
	joined, left := membershipDelta(before, resolved)

	switch {
	case ps.isNew:
		ps.rec.UpdatedAt = d.now
		d.counts.GroupsCreated++
		d.groupPlan = append(d.groupPlan, directoryGroupDecision{
			Action: dirGroupCreate, ObjectID: oid, Record: ps.rec, Joined: joined, Left: left})
	case ps.cleared:
		ps.rec.UpdatedAt = d.now
		d.counts.GroupsCleared++
		d.groupPlan = append(d.groupPlan, directoryGroupDecision{
			Action: dirGroupClear, ObjectID: oid, Record: ps.rec, Joined: joined, Left: left})
	case sameGroupRecord(ps.orig, ps.rec):
		d.counts.GroupsUnchanged++
		d.groupPlan = append(d.groupPlan, directoryGroupDecision{Action: dirGroupUnchanged, ObjectID: oid})
	default:
		ps.rec.UpdatedAt = d.now
		d.counts.GroupsUpdated++
		d.groupPlan = append(d.groupPlan, directoryGroupDecision{
			Action: dirGroupUpdate, ObjectID: oid, Record: ps.rec, Joined: joined, Left: left})
	}
}

// membershipDelta says who joined a group and who left it, in Atlas user ids.
//
// It exists because the group record is not where a running session reads its
// memberships from: a session carries a snapshot (ADR-0185), and a group somebody has
// just been removed from keeps granting whatever it grants until they sign in again.
// So the change itself has to travel, not only the new state.
func membershipDelta(before, after []string) (joined, left []string) {
	for _, id := range after {
		if !containsID(before, id) {
			joined = append(joined, id)
		}
	}
	for _, id := range before {
		if !containsID(after, id) {
			left = append(left, id)
		}
	}
	return joined, left
}

// containsID reports whether a sorted-or-not slice holds an id.
func containsID(ids []string, want string) bool {
	for _, id := range ids {
		if id == want {
			return true
		}
	}
	return false
}

// sameGroupRecord reports whether two group records are the same in every field this
// mirror writes — UpdatedAt deliberately excluded, so a run that changed nothing
// writes nothing rather than touching every group's timestamp once an hour.
func sameGroupRecord(a, b group) bool {
	return a.Name == b.Name && a.Source == b.Source && a.ExternalID == b.ExternalID &&
		equalIDs(a.Members, b.Members) && equalIDs(a.ExternalMembers, b.ExternalMembers)
}

// equalIDs compares two id lists, treating nil and empty as the same thing — which
// they are on disk, since an empty list is omitted from the JSON.
func equalIDs(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
