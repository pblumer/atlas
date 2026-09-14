package api

import (
	"fmt"
	"sort"
	"strings"
)

// Mirroring a Microsoft Entra tenant's accounts and groups into this server
// (ADR-0332).
//
// Atlas pulls: a scheduled process reads /users/delta and /groups/delta through the
// Entra Worker and reports what changed here. Nothing is published outbound, no
// inbound provisioning endpoint exists, and the tenant credential stays where it
// already was — in the worker (ADR-0172). The decision record carries the argument
// against the alternative, which is inbound SCIM.
//
// # The shape of this file
//
// Two functions and a hard line between them. [decideDirectorySync] reads what
// exists and produces a [directoryPlan]: every record it *would* write, complete.
// [applyDirectoryPlan] writes that plan and does nothing else — no lookups, no
// rules, no second opinion. A reporting run calls the first and not the second.
//
// That split is the whole point of the reporting mode, and it is why the preview is
// not computed by code of its own. A preview with its own implementation agrees with
// the real run until the day it stops, and the day it stops is invisible: the report
// is read, believed, and applied. Here there is nothing to disagree with, because
// there is only one decision — the second run does not re-decide, it writes down what
// the first decided. TestDryRunAndApplyDecideTheSame holds the two against the same
// input and compares the plans.

// The mode a message asks for, and why it is spelled this way round.
//
// The field is `apply`, not `dryRun`. A JSON field that is absent decodes to the zero
// value, so the message that forgot to say what it wanted says "write nothing" — and
// the message that forgot to say what it wanted is exactly the one that must not
// enumerate a tenant into an empty user store. Spelled the other way the same
// omission would mean "write everything", and that asymmetry is not a matter of
// taste: one direction fails into a report nobody acts on, the other into a directory
// nobody asked for.

// directorySyncMessage is what a synchronisation run reports: the two change sets it
// read, the cursors they ended at, the revision it read them against, and whether it
// may write.
type directorySyncMessage struct {
	// Apply is the mode. False — including absent — decides everything and writes
	// nothing.
	Apply bool `json:"apply"`

	// FromRevision is the revision the run started from, as the state endpoint
	// reported it. An apply is refused unless it is still current, which is what makes
	// a batch delivered twice harmless: the job protocol guarantees at least once
	// (ADR-0007), and without this the second delivery would move the cursor past
	// changes that were never read.
	FromRevision int64 `json:"fromRevision"`

	Users          []directoryUser  `json:"users"`
	UsersDeltaLink string           `json:"usersDeltaLink"`
	Groups         []directoryGroup `json:"groups"`
	// GroupsDeltaLink is the cursor the group read ended at. Both cursors are written
	// together or not at all: they describe one point in time, and advancing one
	// without the other would resume half a run.
	GroupsDeltaLink string `json:"groupsDeltaLink"`
}

// directoryRemoval is Graph's @removed annotation. Its presence is the whole
// signal — an object that carries it has left the collection — and the reason is
// carried for the report.
type directoryRemoval struct {
	Reason string `json:"reason"`
}

// directoryUser is one object from /users/delta.
//
// The optional fields are pointers and empty strings on purpose. A change-tracking
// page carries the whole object on the first, enumerating run and only the changed
// properties afterwards, so "absent" and "set to empty" are different statements and
// this type has to be able to tell them apart. Reading an absent accountEnabled as
// false would disable the tenant on the first incremental run.
type directoryUser struct {
	ID                string            `json:"id"`
	UserPrincipalName string            `json:"userPrincipalName"`
	DisplayName       string            `json:"displayName"`
	Mail              string            `json:"mail"`
	AccountEnabled    *bool             `json:"accountEnabled"`
	Removed           *directoryRemoval `json:"@removed"`
}

// directoryGroup is one object from /groups/delta. Members carries Graph's
// members@delta: the memberships added since the cursor, and the ones removed, each
// removal marked the same way an object's is.
type directoryGroup struct {
	ID          string               `json:"id"`
	DisplayName string               `json:"displayName"`
	Members     []directoryMemberRef `json:"members@delta"`
	Removed     *directoryRemoval    `json:"@removed"`
}

// directoryMemberRef is one membership change: which object, of which kind, and
// whether it joined or left.
type directoryMemberRef struct {
	ID      string            `json:"id"`
	Type    string            `json:"@odata.type"`
	Removed *directoryRemoval `json:"@removed"`
}

// The actions a plan may hold for an account. They are named rather than derived
// from the record, because the report groups by them and because "created" and
// "merged onto an account that already existed" are the same write and entirely
// different events.
const (
	dirUserCreate    = "create"
	dirUserMerge     = "merge"
	dirUserUpdate    = "update"
	dirUserDisable   = "disable"
	dirUserRefuse    = "refuse"
	dirUserUnchanged = "unchanged"
)

// The actions a plan may hold for a group.
const (
	dirGroupCreate    = "create"
	dirGroupUpdate    = "update"
	dirGroupClear     = "clear"
	dirGroupUnchanged = "unchanged"
)

// The note kinds. A note is a line a person reads; a count is a number they skim.
// Everything that could surprise somebody is a note, and nothing that is the
// ordinary case is.
const (
	noteMerge        = "merge"          // a directory object attached to an account that already existed
	notePrivileged   = "privileged"     // ... and that account holds more than the default role
	noteDisable      = "disable"        // an account this run would disable
	noteRefusal      = "refusal"        // something this run refuses to do, and why
	noteUnresolved   = "unresolved"     // a membership naming an object with no account here
	noteMemberRemove = "member-removed" // a membership this run takes away
	noteSkipped      = "skipped"        // an object this run has no rule for
	noteNameTaken    = "name-taken"     // a mirrored group whose name a local group already uses
)

// directoryNote is one line of the report.
type directoryNote struct {
	Kind     string `json:"kind"`
	ObjectID string `json:"objectId,omitempty"`
	Subject  string `json:"subject,omitempty"`
	UserID   string `json:"userId,omitempty"`
	Detail   string `json:"detail"`
}

// directoryUserDecision is one account the plan would write, or deliberately would
// not. Record is complete: applying is a Save and nothing else.
type directoryUserDecision struct {
	Action   string `json:"action"`
	ObjectID string `json:"objectId"`
	Record   User   `json:"-"`

	// Disabling marks a decision that turns an enabled account off, on whichever path
	// reached that — a departure, a disabled flag, or a merge onto somebody who had
	// already left. It is a field rather than a reading of Action because a merge that
	// also disables is both, and because what depends on it is not cosmetic: a record
	// written with Disabled set stops nothing on its own. The session that is already
	// open and the OAuth grant that is already standing have to be taken away too, and
	// that is the caller's work, after the write.
	Disabling bool `json:"disabling,omitempty"`
}

// directoryGroupDecision is one group the plan would write, on the same terms.
type directoryGroupDecision struct {
	Action   string `json:"action"`
	ObjectID string `json:"objectId"`
	Record   group  `json:"-"`

	// Joined and Left are the membership change in Atlas user ids. They are carried
	// because a group id lives in a live session's snapshot and nowhere else on the
	// access path (ADR-0185): writing the group record alone leaves everybody who is
	// signed in holding the grants of a group they have just left, until they next log
	// in. The caller pushes these into the sessions after the write.
	Joined []string `json:"joined,omitempty"`
	Left   []string `json:"left,omitempty"`
}

// directoryCounts is the expected, in numbers.
type directoryCounts struct {
	UsersRead      int `json:"usersRead"`
	UsersCreated   int `json:"usersCreated"`
	UsersMerged    int `json:"usersMerged"`
	UsersUpdated   int `json:"usersUpdated"`
	UsersDisabled  int `json:"usersDisabled"`
	UsersUnchanged int `json:"usersUnchanged"`
	UsersRefused   int `json:"usersRefused"`

	GroupsRead      int `json:"groupsRead"`
	GroupsCreated   int `json:"groupsCreated"`
	GroupsUpdated   int `json:"groupsUpdated"`
	GroupsCleared   int `json:"groupsCleared"`
	GroupsUnchanged int `json:"groupsUnchanged"`
	GroupsRefused   int `json:"groupsRefused"`

	MembersResolved   int `json:"membersResolved"`
	MembersUnresolved int `json:"membersUnresolved"`
}

// directoryPlan is one synchronisation, decided and not yet written.
type directoryPlan struct {
	// Stale marks a message whose FromRevision is no longer current: a second
	// delivery, or a run overtaken by another. It is decided here rather than at the
	// write so both modes report it identically — a reporting run that quietly ignored
	// staleness would be reporting about a state that no longer exists.
	Stale bool
	// Current is the revision the state actually holds, for the message that says so.
	Current int64

	Users  []directoryUserDecision
	Groups []directoryGroupDecision
	Notes  []directoryNote
	Counts directoryCounts
}

// writes reports whether applying this plan would change anything.
func (p directoryPlan) writes() bool {
	for _, d := range p.Users {
		if d.Action != dirUserUnchanged && d.Action != dirUserRefuse {
			return true
		}
	}
	for _, d := range p.Groups {
		if d.Action != dirGroupUnchanged {
			return true
		}
	}
	return false
}

// directoryIDOf normalises a directory object id the way the stores hold one.
func directoryIDOf(raw string) string { return strings.ToLower(strings.TrimSpace(raw)) }

// directoryDecider carries what one decision needs: the population as it is now, and
// the two things a decision cannot compute — a fresh id and the current time.
//
// It exists so the rules are a function of their inputs and nothing else. The id
// generator is injected for the same reason the clock is: two runs over the same
// input have to be comparable, and a random id makes them differ in a way that says
// nothing. In the server it is [newUserID]; in the parity test it counts.
type directoryDecider struct {
	// groups is the population as the store holds it, kept for the two questions the
	// indexes cannot answer: whether a name is already in use anywhere, and which
	// mirrored groups the message did not mention.
	groups []group
	now    int64
	// newUserID and newGroupID mint the keys for records that do not exist yet.
	newUserID  func() (string, error)
	newGroupID func() (string, error)

	// byDirectory, byEmail and takenUsernames are the indexes the rules ask. They are
	// built once and kept current as the plan grows, so an object appearing twice in
	// one message resolves to the account the first occurrence created rather than to
	// a second one.
	byDirectory   map[string]User
	byEmail       map[string]User
	takenUsername map[string]bool
	// groupByDirectory is the same for groups: the mirrored ones, by object id.
	groupByDirectory map[string]group

	// userPlan and groupPlan are the decisions so far, in the order they were made.
	userPlan  []directoryUserDecision
	groupPlan []directoryGroupDecision

	// groupState is the working copy of every mirrored group this run touches, and
	// groupOrder the order it decides them in — message order first, then store order
	// for the ones the message did not mention. A map alone would decide them in
	// whatever order Go felt like, and a plan that differs run to run cannot be
	// compared with the plan the reporting run showed somebody.
	groupState map[string]*pendingGroup
	groupOrder []string

	notes  []directoryNote
	counts directoryCounts
	// adminsOf is the set of accounts that are enabled administrators right now. It
	// shrinks as the plan disables people, which is what makes the last-administrator
	// guard hold across a message that disables several.
	adminsOf map[string]bool
}

// decideDirectorySync turns one message into a plan, against the population as it
// stands. It writes nothing and reads nothing but its arguments.
//
// The order is not an implementation detail. Accounts are decided first because a
// membership is a reference to one, then the groups in the message, then — and this
// is the part that makes the run order stop mattering — every mirrored group is
// re-resolved against the accounts that now exist, including the ones this plan
// would create. A person can appear in a group before their own account has been
// read; a change-tracking read will never mention that membership again, so nothing
// may depend on the two arriving in a convenient order.
func decideDirectorySync(msg directorySyncMessage, state directorySyncState, users []User, groups []group,
	now int64, newUserID, newGroupID func() (string, error)) (directoryPlan, error) {

	d := &directoryDecider{
		groups: groups, now: now,
		newUserID: newUserID, newGroupID: newGroupID,
		byDirectory:      map[string]User{},
		byEmail:          map[string]User{},
		takenUsername:    map[string]bool{},
		groupByDirectory: map[string]group{},
		groupState:       map[string]*pendingGroup{},
		adminsOf:         map[string]bool{},
	}
	for _, u := range users {
		if u.DirectoryID != "" {
			d.byDirectory[u.DirectoryID] = u
		}
		if u.Email != "" {
			d.byEmail[strings.ToLower(u.Email)] = u
		}
		d.takenUsername[strings.ToLower(u.Username)] = true
		if !u.Disabled && u.hasRole(RoleAdmin) {
			d.adminsOf[u.ID] = true
		}
	}
	for _, g := range groups {
		if g.Source == SourceEntra && g.ExternalID != "" {
			d.groupByDirectory[g.ExternalID] = g
		}
	}

	plan := directoryPlan{Current: state.Revision}
	// A stale message is decided in full and applied not at all. Deciding it anyway is
	// what lets the answer say which batch it was and what it would have done, instead
	// of a bare conflict that leaves an operator guessing.
	plan.Stale = msg.FromRevision != state.Revision

	for _, mu := range msg.Users {
		d.counts.UsersRead++
		if err := d.decideUser(mu); err != nil {
			return directoryPlan{}, err
		}
	}
	for _, mg := range msg.Groups {
		d.counts.GroupsRead++
		if err := d.decideGroup(mg); err != nil {
			return directoryPlan{}, err
		}
	}
	d.resolveMemberships()

	plan.Users, plan.Groups = d.userPlan, d.groupPlan
	plan.Notes, plan.Counts = d.notes, d.counts
	return plan, nil
}

// note appends a line to the report. Nothing is bounded here: the ceiling belongs to
// the report, which is what a person reads, and a plan that dropped its own notes
// would make the two modes disagree about what happened.
func (d *directoryDecider) note(n directoryNote) { d.notes = append(d.notes, n) }

// decideUser decides one account.
func (d *directoryDecider) decideUser(mu directoryUser) error {
	oid := directoryIDOf(mu.ID)
	if oid == "" {
		d.counts.UsersRefused++
		d.note(directoryNote{Kind: noteRefusal, Subject: strings.TrimSpace(mu.UserPrincipalName),
			Detail: "the directory returned an object with no id; nothing can be matched to it"})
		return nil
	}
	if mu.Removed != nil {
		return d.decideDeparture(mu, oid)
	}
	if existing, ok := d.byDirectory[oid]; ok {
		return d.decideKnownUser(mu, oid, existing)
	}
	// Not known by object id. An account may still be this person: one created by a
	// federated login holds the provider's subject, which is pairwise per application
	// and therefore never the directory object id, so the address is the only bridge
	// there is. It is a weaker one — an address can be reassigned — which is why every
	// merge is a line in the report and not a number in it.
	if addr := directoryAddress(mu); addr != "" {
		if owner, ok := d.byEmail[strings.ToLower(addr)]; ok {
			if owner.DirectoryID == "" {
				return d.decideMerge(mu, oid, owner)
			}
			d.counts.UsersRefused++
			d.note(directoryNote{Kind: noteRefusal, ObjectID: oid, Subject: addr, UserID: owner.ID,
				Detail: "this address already belongs to an account mirroring a different directory object; " +
					"one of the two is wrong and no guess here would be better than the other"})
			return nil
		}
	}
	return d.decideNewUser(mu, oid)
}

// decideDeparture handles an account the directory says is gone: @removed on
// /users/delta, which is what a deletion looks like from a change-tracking read.
//
// It disables and never deletes. A record with permissions hanging off it is the
// anchor of an audit trail, and removing it turns every reference to it into a
// dangling one — which is a harder problem to have than a disabled account nobody
// cleans up. A departure Atlas never knew about is a note and not an error: an
// installation that started mirroring last week has no record of who left the week
// before.
func (d *directoryDecider) decideDeparture(mu directoryUser, oid string) error {
	existing, ok := d.byDirectory[oid]
	if !ok {
		d.note(directoryNote{Kind: noteSkipped, ObjectID: oid,
			Detail: "the directory reports this object as removed and no account here mirrors it"})
		return nil
	}
	rec := existing
	if rec.Disabled {
		d.counts.UsersUnchanged++
		d.record(directoryUserDecision{Action: dirUserUnchanged, ObjectID: oid})
		return nil
	}
	if !d.mayDisable(rec, oid) {
		// Nothing else is pending for a departure, so the refusal is the whole decision.
		d.record(directoryUserDecision{Action: dirUserRefuse, ObjectID: oid})
		return nil
	}
	rec.Disabled = true
	rec.UpdatedAt = d.now
	d.counts.UsersDisabled++
	d.note(directoryNote{Kind: noteDisable, ObjectID: oid, Subject: rec.Username, UserID: rec.ID,
		Detail: "the directory no longer holds this object" + removalReason(mu.Removed)})
	d.commitUser(directoryUserDecision{Action: dirUserDisable, ObjectID: oid, Record: rec, Disabling: true})
	return nil
}

// decideKnownUser updates the account already mirroring this object.
func (d *directoryDecider) decideKnownUser(mu directoryUser, oid string, rec User) error {
	before := rec
	d.applyProfile(&rec, mu)
	// A refused disable does not discard the rest of the page. The profile change in
	// the same object is a change a change-tracking read will never send again, and
	// dropping it to register a refusal would lose it for good.
	disabling := false
	if mu.AccountEnabled != nil && rec.Disabled != !*mu.AccountEnabled {
		switch {
		case *mu.AccountEnabled:
			rec.Disabled = false
		case d.mayDisable(rec, oid):
			rec.Disabled, disabling = true, true
		}
	}
	if sameUserRecord(before, rec) {
		d.counts.UsersUnchanged++
		d.record(directoryUserDecision{Action: dirUserUnchanged, ObjectID: oid})
		return nil
	}
	rec.UpdatedAt = d.now
	action := dirUserUpdate
	if disabling {
		action = dirUserDisable
		d.counts.UsersDisabled++
		d.note(directoryNote{Kind: noteDisable, ObjectID: oid, Subject: rec.Username, UserID: rec.ID,
			Detail: "the directory reports this account as disabled"})
	} else {
		d.counts.UsersUpdated++
	}
	d.commitUser(directoryUserDecision{Action: action, ObjectID: oid, Record: rec, Disabling: disabling})
	return nil
}

// decideMerge attaches the directory object to an account that already exists — one
// somebody created here, or one a federated login made — instead of creating a
// second one beside it.
//
// A second account is the outcome this avoids at some cost, and the cost is worth
// naming: permissions, task assignments and the audit trail hang off an account, and
// a duplicate leaves the person holding one of them and signing in as the other.
// Roles are not touched, in either direction. Widening them here would be the mirror
// granting authority; narrowing them would be the mirror taking an administrator's
// away because a directory that knows nothing about Atlas roles said nothing about
// them.
func (d *directoryDecider) decideMerge(mu directoryUser, oid string, rec User) error {
	rec.DirectoryID = oid
	d.applyProfile(&rec, mu)
	// Linking the account happens either way: the merge is what stops a second
	// account existing, and refusing it because the disable was refused would leave
	// the duplicate this rule exists to avoid — with no later run to try again, since
	// the object will not be reported a second time.
	disabling := false
	if mu.AccountEnabled != nil && !*mu.AccountEnabled && !rec.Disabled && d.mayDisable(rec, oid) {
		rec.Disabled, disabling = true, true
	}
	rec.UpdatedAt = d.now
	d.counts.UsersMerged++
	d.note(directoryNote{Kind: noteMerge, ObjectID: oid, Subject: rec.Username, UserID: rec.ID,
		Detail: fmt.Sprintf("matched by address %q onto the existing %s account; from now on the directory "+
			"object identifies it", rec.Email, sourceLabel(rec.Source))})
	if extra := nonDefaultRoles(rec.Roles); len(extra) > 0 {
		d.note(directoryNote{Kind: notePrivileged, ObjectID: oid, Subject: rec.Username, UserID: rec.ID,
			Detail: "the account this merge attaches to holds " + strings.Join(extra, ", ") +
				"; the merge neither grants nor removes a role, so it keeps them"})
	}
	d.commitUser(directoryUserDecision{Action: dirUserMerge, ObjectID: oid, Record: rec, Disabling: disabling})
	return nil
}

// decideNewUser creates an account for an object nothing here mirrors yet.
//
// Its roles are exactly [RoleUser], from a literal and not from anything in the
// message. There is no mapping, no claim, and no attribute a tenant administrator
// could set that reaches this line — because the worst outcome this whole mechanism
// has is a directory read that makes administrators, and the only way to be sure it
// cannot is for there to be no path from the input to the role.
func (d *directoryDecider) decideNewUser(mu directoryUser, oid string) error {
	id, err := d.newUserID()
	if err != nil {
		return err
	}
	username, err := d.freeName(directoryUsername(mu))
	if err != nil {
		return err
	}
	rec := User{
		ID:              id,
		Username:        username,
		DisplayName:     strings.TrimSpace(mu.DisplayName),
		Roles:           []string{RoleUser},
		Source:          SourceEntra,
		ExternalID:      oid,
		DirectoryID:     oid,
		CreatedAt:       d.now,
		UpdatedAt:       d.now,
		RolesUpgradedAt: d.now,
	}
	if addr := directoryAddress(mu); addr != "" {
		rec.Email = addr
	}
	if mu.AccountEnabled != nil {
		rec.Disabled = !*mu.AccountEnabled
	}
	d.counts.UsersCreated++
	d.commitUser(directoryUserDecision{Action: dirUserCreate, ObjectID: oid, Record: rec})
	return nil
}

// mayDisable answers whether this account may be disabled, and reports the refusal
// when it may not. It records no decision of its own: whether a refused disable ends
// the object's story or only one part of it is the caller's question.
//
// It refuses the one disable that could lock an installation out of itself:
// the last enabled administrator. It is the same guard the administration API
// applies by hand (enabledAdminCount), asked here for the same reason — an
// instance whose every administrator has left the tenant is one nobody can repair,
// and a directory has no idea that this account is the one that matters.
func (d *directoryDecider) mayDisable(rec User, oid string) bool {
	if !d.adminsOf[rec.ID] {
		return true
	}
	remaining := 0
	for id := range d.adminsOf {
		if id != rec.ID {
			remaining++
		}
	}
	if remaining > 0 {
		return true
	}
	d.counts.UsersRefused++
	d.note(directoryNote{Kind: noteRefusal, ObjectID: oid, Subject: rec.Username, UserID: rec.ID,
		Detail: "this is the last enabled administrator; the directory does not get to lock this " +
			"installation out of itself, so the account stays enabled and somebody has to decide"})
	return false
}

// applyProfile carries the display material across. An absent field is left alone:
// an incremental page carries only what changed, so "" means the directory said
// nothing about it, never that it is now empty.
func (d *directoryDecider) applyProfile(rec *User, mu directoryUser) {
	if name := strings.TrimSpace(mu.DisplayName); name != "" {
		rec.DisplayName = name
	}
	addr := directoryAddress(mu)
	if addr == "" || strings.EqualFold(addr, rec.Email) {
		return
	}
	// An address another account already holds is not taken: the store's uniqueness is
	// what a login and an invitation rely on, and two accounts sharing one would make
	// byEmail answer about whichever was written first.
	if owner, ok := d.byEmail[strings.ToLower(addr)]; ok && owner.ID != rec.ID {
		d.note(directoryNote{Kind: noteRefusal, ObjectID: rec.DirectoryID, Subject: rec.Username, UserID: rec.ID,
			Detail: fmt.Sprintf("the directory gives this account the address %q, which another account "+
				"already holds; the address is left as it was", addr)})
		return
	}
	if rec.Email != "" {
		delete(d.byEmail, strings.ToLower(rec.Email))
	}
	rec.Email = addr
}

// freeName finds a username nothing holds, in the shape the federated login uses.
func (d *directoryDecider) freeName(want string) (string, error) {
	for i := 0; i < 100; i++ {
		candidate := want
		if i > 0 {
			candidate = fmt.Sprintf("%s-%d", want, i+1)
		}
		if !d.takenUsername[strings.ToLower(candidate)] {
			return candidate, nil
		}
	}
	return "", fmt.Errorf("entra sync: no free username derived from %q", want)
}

// record adds a decision that writes nothing.
func (d *directoryDecider) record(dec directoryUserDecision) { d.userPlan = append(d.userPlan, dec) }

// commitUser adds a decision that writes, and folds its record into the indexes so
// the rest of this run sees it. That is what makes an object appearing twice in one
// message resolve to one account, and what lets a membership find a person whose
// account this same plan creates.
func (d *directoryDecider) commitUser(dec directoryUserDecision) {
	d.userPlan = append(d.userPlan, dec)
	rec := dec.Record
	d.byDirectory[rec.DirectoryID] = rec
	if rec.Email != "" {
		d.byEmail[strings.ToLower(rec.Email)] = rec
	}
	d.takenUsername[strings.ToLower(rec.Username)] = true
	if rec.Disabled {
		delete(d.adminsOf, rec.ID)
	}
}

// directoryAddress is the address to carry over: the mail attribute where the
// directory has one, and the user principal name where it does not — which is what a
// tenant without Exchange looks like.
func directoryAddress(mu directoryUser) string {
	if mail := strings.TrimSpace(mu.Mail); mail != "" {
		return mail
	}
	return strings.TrimSpace(mu.UserPrincipalName)
}

// directoryUsername proposes a username for a new account, on the same rules a
// federated login uses so the two look alike in a task list.
func directoryUsername(mu directoryUser) string {
	for _, candidate := range []string{mu.UserPrincipalName, mu.Mail, mu.DisplayName} {
		if name := sanitizeUsername(candidate); name != "" {
			return name
		}
	}
	return "entra-user"
}

// sameUserRecord reports whether two records are the same in every field this
// mirror writes. It asks about the fields rather than comparing the structs so that
// UpdatedAt — which every run would otherwise change — does not turn a no-op into a
// write, and so that adding a field nobody mirrors does not silently start counting
// as a change.
func sameUserRecord(a, b User) bool {
	return a.DisplayName == b.DisplayName && a.Email == b.Email &&
		a.Disabled == b.Disabled && a.DirectoryID == b.DirectoryID
}

// nonDefaultRoles lists what an account holds beyond the role this mirror would give
// a new one. It is what makes a merge onto an administrator's account a line
// somebody reads rather than a number.
func nonDefaultRoles(roles []string) []string {
	var out []string
	for _, r := range roles {
		if r != RoleUser {
			out = append(out, r)
		}
	}
	return out
}

// sourceLabel names an account's origin for a sentence a person reads.
func sourceLabel(source string) string {
	switch source {
	case SourceOIDC:
		return "federated"
	case SourceEntra:
		return "mirrored"
	case "":
		return "local"
	default:
		return source
	}
}

// removalReason appends Graph's stated reason where it gave one.
func removalReason(rm *directoryRemoval) string {
	if rm == nil || strings.TrimSpace(rm.Reason) == "" {
		return ""
	}
	return " (" + strings.TrimSpace(rm.Reason) + ")"
}

// sortedIDs returns a set's members in a stable order, so a plan built twice from
// the same message is the same plan — map iteration is not, and a decision that
// differed by iteration order would make the parity test flap instead of failing.
func sortedIDs(set map[string]bool) []string {
	out := make([]string, 0, len(set))
	for id := range set {
		out = append(out, id)
	}
	sort.Strings(out)
	return out
}
