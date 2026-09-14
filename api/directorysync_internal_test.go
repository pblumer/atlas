package api

import (
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/pblumer/atlas/limits"
)

// What a directory synchronisation must not get wrong
// (ADR-draft-entra-directory-provisioning).
//
// These run against [decideDirectorySync] directly, because that is where every
// decision is made. The HTTP half is tested where HTTP behaviour lives
// (directorysync_http_test.go); what is checked here is the thing a report claims to
// be a preview of.

// seqIDs is a deterministic id source. The decision function takes its id generator
// as an argument precisely so a test can do this: two runs over the same input
// produce the same plan, and a difference between them is a difference in the rules
// rather than in the random number generator.
func seqIDs(prefix string) func() (string, error) {
	n := 0
	return func() (string, error) {
		n++
		return fmt.Sprintf("%s%d", prefix, n), nil
	}
}

func enabled(v bool) *bool { return &v }

// decide runs one message against a population, with fresh id sources each time.
func decide(t *testing.T, msg directorySyncMessage, state directorySyncState, users []User, groups []group) directoryPlan {
	t.Helper()
	plan, err := decideDirectorySync(msg, state, users, groups, 1000, seqIDs("usr_"), seqIDs("grp_"))
	if err != nil {
		t.Fatalf("decide: %v", err)
	}
	return plan
}

// person is one ordinary directory user.
func person(id, upn, name string) directoryUser {
	return directoryUser{ID: id, UserPrincipalName: upn, DisplayName: name, Mail: upn, AccountEnabled: enabled(true)}
}

// TestDryRunAndApplyDecideTheSame is the property the reporting mode rests on.
//
// A preview computed by code of its own is a claim about what another piece of code
// would do, and the day the two diverge is the day nobody notices — the report is
// read, believed and applied. So there is one decision function, and the mode only
// decides whether its result is written. This holds the two modes against one input
// and compares what they decided, not what they wrote.
func TestDryRunAndApplyDecideTheSame(t *testing.T) {
	users := []User{
		{ID: "usr_old", Username: "ada", Email: "ada@example.org", Source: SourceOIDC, ExternalID: "sub-1", Roles: []string{RoleUser}},
		{ID: "usr_gone", Username: "bob", Email: "bob@example.org", Source: SourceEntra, ExternalID: "oid-b", DirectoryID: "oid-b", Roles: []string{RoleUser}},
	}
	groups := []group{{ID: "grp_x", Name: "Team", Source: SourceEntra, ExternalID: "oid-g", Members: []string{"usr_gone"}, ExternalMembers: []string{"oid-b"}}}
	body := directorySyncMessage{
		FromRevision: 7,
		Users: []directoryUser{
			person("OID-A", "ada@example.org", "Ada Lovelace"),
			{ID: "oid-b", Removed: &directoryRemoval{Reason: "changed"}},
			person("oid-c", "cleo@example.org", "Cleo"),
		},
		UsersDeltaLink: "https://graph/next-users",
		Groups: []directoryGroup{{ID: "oid-g", DisplayName: "Team", Members: []directoryMemberRef{
			{ID: "oid-c"}, {ID: "oid-b", Removed: &directoryRemoval{}},
		}}},
		GroupsDeltaLink: "https://graph/next-groups",
	}

	reporting, applying := body, body
	reporting.Apply, applying.Apply = false, true

	state := directorySyncState{ID: directorySyncStateID, Revision: 7}
	left := decide(t, reporting, state, users, groups)
	right := decide(t, applying, state, users, groups)

	if !reflect.DeepEqual(left.Users, right.Users) {
		t.Errorf("the two modes decided different accounts:\nreport: %+v\napply:  %+v", left.Users, right.Users)
	}
	if !reflect.DeepEqual(left.Groups, right.Groups) {
		t.Errorf("the two modes decided different groups:\nreport: %+v\napply:  %+v", left.Groups, right.Groups)
	}
	if !reflect.DeepEqual(left.Counts, right.Counts) || !reflect.DeepEqual(left.Notes, right.Notes) {
		t.Errorf("the two modes reported differently:\nreport: %+v\napply:  %+v", left, right)
	}
	// And the message is not trivial: a run that decided nothing would pass this test
	// while proving nothing at all.
	if left.Counts.UsersCreated == 0 || left.Counts.UsersMerged == 0 || left.Counts.UsersDisabled == 0 {
		t.Fatalf("the fixture stopped exercising create, merge and disable: %+v", left.Counts)
	}
}

// TestNothingInAMessageCanGrantARole is the worst outcome this mechanism has, made
// impossible rather than unlikely.
//
// A directory read that makes administrators is the failure nobody recovers from
// quietly, so there is no path at all from the message to the role: a created account
// gets a literal, and an account that already exists keeps what it holds. The message
// here tries every shape a tenant administrator could set.
func TestNothingInAMessageCanGrantARole(t *testing.T) {
	msg := directorySyncMessage{Users: []directoryUser{
		person("oid-1", "admin@example.org", "admin"),
		person("oid-2", "root@example.org", "Administrator"),
		{ID: "oid-3", UserPrincipalName: "x@example.org", DisplayName: "admin modeler operator", AccountEnabled: enabled(true)},
	}}
	plan := decide(t, msg, directorySyncState{}, nil, nil)
	if len(plan.Users) != 3 {
		t.Fatalf("decided %d accounts, want 3", len(plan.Users))
	}
	for _, dec := range plan.Users {
		if dec.Action != dirUserCreate {
			t.Fatalf("%s: action = %q, want a creation", dec.ObjectID, dec.Action)
		}
		if got := strings.Join(dec.Record.Roles, " "); got != RoleUser {
			t.Errorf("%s would be created holding %q; a mirror grants the default role and nothing else",
				dec.ObjectID, got)
		}
	}
}

// TestAMirrorNeverNarrowsAnExistingAccountsRoles is the other direction, and it
// matters as much: a mirror that wrote its default over what it found would take an
// administrator's authority away the first time they appeared in a delta page.
func TestAMirrorNeverNarrowsAnExistingAccountsRoles(t *testing.T) {
	users := []User{{ID: "usr_1", Username: "root", Email: "root@example.org",
		Source: SourceLocal, DirectoryID: "oid-1", Roles: []string{RoleAdmin, RoleModeler}}}
	msg := directorySyncMessage{Users: []directoryUser{person("oid-1", "root@example.org", "Root Changed")}}

	plan := decide(t, msg, directorySyncState{}, users, nil)
	if len(plan.Users) != 1 || plan.Users[0].Action != dirUserUpdate {
		t.Fatalf("plan = %+v, want one update", plan.Users)
	}
	if got := strings.Join(plan.Users[0].Record.Roles, " "); got != "admin modeler" {
		t.Errorf("roles = %q, want them untouched", got)
	}
}

// TestTheLastAdministratorIsNotDisabled. A directory has no idea which account is the
// one that can repair this installation, and an instance whose every administrator
// has left the tenant is one nobody can enter. The refusal is a line in the report,
// not a silent skip.
func TestTheLastAdministratorIsNotDisabled(t *testing.T) {
	users := []User{
		{ID: "usr_admin", Username: "root", DirectoryID: "oid-1", Roles: []string{RoleAdmin}},
		{ID: "usr_other", Username: "ada", DirectoryID: "oid-2", Roles: []string{RoleUser}},
	}
	msg := directorySyncMessage{Users: []directoryUser{
		{ID: "oid-1", Removed: &directoryRemoval{Reason: "deleted"}},
		{ID: "oid-2", Removed: &directoryRemoval{Reason: "deleted"}},
	}}
	plan := decide(t, msg, directorySyncState{}, users, nil)

	var refused, disabled int
	for _, dec := range plan.Users {
		switch dec.Action {
		case dirUserRefuse:
			refused++
		case dirUserDisable:
			disabled++
			if dec.Record.ID == "usr_admin" {
				t.Error("the last administrator was disabled")
			}
		}
	}
	if refused != 1 || disabled != 1 {
		t.Fatalf("refused=%d disabled=%d, want exactly one of each", refused, disabled)
	}
	if !hasNote(plan, noteRefusal, "last enabled administrator") {
		t.Errorf("the refusal is not in the report: %+v", plan.Notes)
	}
}

// TestASecondAdministratorMayBeDisabled is the other half of the guard: it protects
// the last one, not administrators in general. Without this the rule above would pass
// while refusing every departure.
func TestASecondAdministratorMayBeDisabled(t *testing.T) {
	users := []User{
		{ID: "usr_a", Username: "root", DirectoryID: "oid-1", Roles: []string{RoleAdmin}},
		{ID: "usr_b", Username: "root2", DirectoryID: "oid-2", Roles: []string{RoleAdmin}},
	}
	msg := directorySyncMessage{Users: []directoryUser{{ID: "oid-1", Removed: &directoryRemoval{}}}}
	plan := decide(t, msg, directorySyncState{}, users, nil)
	if len(plan.Users) != 1 || plan.Users[0].Action != dirUserDisable {
		t.Fatalf("plan = %+v, want the departure to be carried out", plan.Users)
	}
}

// TestAMergeIsOneAccountAndIsReported. An account created by a federated login holds
// the provider's subject, which is pairwise per application and therefore never the
// directory object id; the address is the only bridge there is. What must not happen
// is a second account beside the first — permissions hang off one of them and the
// person signs in as the other — and what must happen is a line somebody reads.
func TestAMergeIsOneAccountAndIsReported(t *testing.T) {
	users := []User{{ID: "usr_1", Username: "ada", Email: "Ada@Example.org",
		Source: SourceOIDC, ExternalID: "pairwise-sub", Roles: []string{RoleUser, RoleAdmin}}}
	msg := directorySyncMessage{Users: []directoryUser{person("OID-1", "ada@example.org", "Ada Lovelace")}}

	plan := decide(t, msg, directorySyncState{}, users, nil)
	if len(plan.Users) != 1 {
		t.Fatalf("decided %d accounts, want one — a merge is not a second account", len(plan.Users))
	}
	dec := plan.Users[0]
	if dec.Action != dirUserMerge {
		t.Fatalf("action = %q, want a merge", dec.Action)
	}
	if dec.Record.ID != "usr_1" {
		t.Errorf("wrote a new record %q instead of the one that existed", dec.Record.ID)
	}
	if dec.Record.DirectoryID != "oid-1" {
		t.Errorf("directoryId = %q, want the object id, lower-cased", dec.Record.DirectoryID)
	}
	// The federated identity survives, or the next sign-in makes the duplicate this
	// whole rule exists to avoid.
	if dec.Record.Source != SourceOIDC || dec.Record.ExternalID != "pairwise-sub" {
		t.Errorf("the federated identity was overwritten: source=%q externalId=%q",
			dec.Record.Source, dec.Record.ExternalID)
	}
	if !hasNote(plan, noteMerge, "matched by address") {
		t.Errorf("the merge is not a line in the report: %+v", plan.Notes)
	}
	if !hasNote(plan, notePrivileged, "admin") {
		t.Errorf("a merge onto an account holding admin must say so: %+v", plan.Notes)
	}
}

// TestAnAddressAnotherObjectOwnsIsRefused. The address is a weak bridge, and the case
// where it is actively wrong — two directory objects claiming one address — is the
// one where guessing would attach a person to somebody else's account.
func TestAnAddressAnotherObjectOwnsIsRefused(t *testing.T) {
	users := []User{{ID: "usr_1", Username: "ada", Email: "ada@example.org",
		Source: SourceEntra, ExternalID: "oid-1", DirectoryID: "oid-1", Roles: []string{RoleUser}}}
	msg := directorySyncMessage{Users: []directoryUser{person("oid-2", "ada@example.org", "Someone Else")}}

	plan := decide(t, msg, directorySyncState{}, users, nil)
	if plan.Counts.UsersRefused != 1 || plan.Counts.UsersCreated != 0 {
		t.Fatalf("counts = %+v, want the second object refused and no account created", plan.Counts)
	}
	if !hasNote(plan, noteRefusal, "already belongs to an account") {
		t.Errorf("notes = %+v", plan.Notes)
	}
}

// TestAMembershipMayArriveBeforeItsAccount is the ordering property, and the reason
// a mirrored group keeps the directory's own ids rather than only the translated
// ones. A change-tracking read never mentions a membership twice, so a membership
// dropped because its account had not been read yet would be lost for good.
func TestAMembershipMayArriveBeforeItsAccount(t *testing.T) {
	// Run one: the group names somebody nothing here knows.
	first := directorySyncMessage{Groups: []directoryGroup{
		{ID: "oid-g", DisplayName: "Team", Members: []directoryMemberRef{{ID: "oid-late"}}}}}
	plan := decide(t, first, directorySyncState{}, nil, nil)
	if len(plan.Groups) != 1 || plan.Groups[0].Action != dirGroupCreate {
		t.Fatalf("plan = %+v, want the group created", plan.Groups)
	}
	made := plan.Groups[0].Record
	if len(made.Members) != 0 {
		t.Errorf("members = %v, want none resolved yet", made.Members)
	}
	if len(made.ExternalMembers) != 1 || made.ExternalMembers[0] != "oid-late" {
		t.Fatalf("externalMembers = %v, want the directory's id held", made.ExternalMembers)
	}
	if !hasNote(plan, noteUnresolved, "resolves on the run that reads the account") {
		t.Errorf("an unresolved membership must be reported: %+v", plan.Notes)
	}

	// Run two reads the account and mentions the group nowhere. The membership has to
	// resolve anyway.
	second := directorySyncMessage{Users: []directoryUser{person("oid-late", "late@example.org", "Late")}}
	plan = decide(t, second, directorySyncState{}, nil, []group{made})
	var joined []string
	for _, dec := range plan.Groups {
		if dec.ObjectID == "oid-g" {
			joined = dec.Record.Members
			if dec.Action != dirGroupUpdate {
				t.Errorf("action = %q, want the group updated by the account arriving", dec.Action)
			}
		}
	}
	if len(joined) != 1 {
		t.Fatalf("members after the account arrived = %v, want one", joined)
	}
}

// TestTheSameMessageTwiceChangesNothingTheSecondTime. The job protocol delivers at
// least once, so the second delivery of a batch must not create a second account,
// double a membership, or move anything.
func TestTheSameMessageTwiceChangesNothingTheSecondTime(t *testing.T) {
	msg := directorySyncMessage{
		Users:  []directoryUser{person("oid-1", "ada@example.org", "Ada")},
		Groups: []directoryGroup{{ID: "oid-g", DisplayName: "Team", Members: []directoryMemberRef{{ID: "oid-1"}}}},
	}
	first := decide(t, msg, directorySyncState{}, nil, nil)

	// Feed the plan's own output back in as the population, which is what applying it
	// would have produced, and deliver the message again.
	users := []User{first.Users[0].Record}
	groups := []group{first.Groups[0].Record}
	second := decide(t, msg, directorySyncState{}, users, groups)

	if second.writes() {
		t.Errorf("the second delivery would write: %+v / %+v", second.Users, second.Groups)
	}
	if second.Counts.UsersUnchanged != 1 || second.Counts.GroupsUnchanged != 1 {
		t.Errorf("counts = %+v, want everything recognised as unchanged", second.Counts)
	}
}

// TestAGroupTheDirectoryDroppedKeepsItsRecordAndLosesItsMembers. Deleting the group
// would take the grants made through it with it and leave every reference dangling;
// keeping it with its membership would leave access somebody's directory revoked.
func TestAGroupTheDirectoryDroppedKeepsItsRecordAndLosesItsMembers(t *testing.T) {
	groups := []group{{ID: "grp_1", Name: "Team", Source: SourceEntra, ExternalID: "oid-g",
		Members: []string{"usr_1"}, ExternalMembers: []string{"oid-1"}}}
	users := []User{{ID: "usr_1", Username: "ada", DirectoryID: "oid-1", Roles: []string{RoleUser}}}
	msg := directorySyncMessage{Groups: []directoryGroup{{ID: "oid-g", Removed: &directoryRemoval{Reason: "deleted"}}}}

	plan := decide(t, msg, directorySyncState{}, users, groups)
	if len(plan.Groups) != 1 || plan.Groups[0].Action != dirGroupClear {
		t.Fatalf("plan = %+v, want the group cleared", plan.Groups)
	}
	rec := plan.Groups[0].Record
	if rec.ID != "grp_1" || rec.Name != "Team" {
		t.Errorf("the record was replaced rather than emptied: %+v", rec)
	}
	if len(rec.Members) != 0 || len(rec.ExternalMembers) != 0 {
		t.Errorf("members = %v / %v, want both empty", rec.Members, rec.ExternalMembers)
	}
}

// TestMembersThatAreNotPeopleAreReportedAndDropped. A group's members include nested
// groups, service principals and devices. Holding their ids would put a line in every
// report from now until somebody deleted the group, so they are dropped — once,
// loudly, with the kind named.
func TestMembersThatAreNotPeopleAreReportedAndDropped(t *testing.T) {
	msg := directorySyncMessage{Groups: []directoryGroup{{ID: "oid-g", DisplayName: "Team",
		Members: []directoryMemberRef{
			{ID: "oid-n", Type: "#microsoft.graph.group"},
			{ID: "oid-s", Type: "#microsoft.graph.servicePrincipal"},
			{ID: "oid-u", Type: "#microsoft.graph.user"},
		}}}}
	plan := decide(t, msg, directorySyncState{}, nil, nil)
	held := plan.Groups[0].Record.ExternalMembers
	if len(held) != 1 || held[0] != "oid-u" {
		t.Fatalf("externalMembers = %v, want only the person", held)
	}
	if !hasNote(plan, noteSkipped, "nested group") || !hasNote(plan, noteSkipped, "service principal") {
		t.Errorf("both non-people must be named in the report: %+v", plan.Notes)
	}
}

// TestAMembershipAddedByHandIsWithdrawnAndSaid. For a mirrored group the directory
// decides the membership — that is what mirroring means — but a membership
// disappearing without a word is how somebody loses access and nobody can say why.
func TestAMembershipAddedByHandIsWithdrawnAndSaid(t *testing.T) {
	users := []User{{ID: "usr_1", Username: "ada", DirectoryID: "oid-1", Roles: []string{RoleUser}}}
	groups := []group{{ID: "grp_1", Name: "Team", Source: SourceEntra, ExternalID: "oid-g",
		Members: []string{"usr_1", "usr_handadded"}, ExternalMembers: []string{"oid-1"}}}

	plan := decide(t, directorySyncMessage{}, directorySyncState{}, users, groups)
	if len(plan.Groups) != 1 || plan.Groups[0].Action != dirGroupUpdate {
		t.Fatalf("plan = %+v, want the group corrected", plan.Groups)
	}
	if got := plan.Groups[0].Record.Members; len(got) != 1 || got[0] != "usr_1" {
		t.Errorf("members = %v, want only the directory's own", got)
	}
	if !hasNote(plan, noteMemberRemove, "withdraws it") {
		t.Errorf("the withdrawal must be a line: %+v", plan.Notes)
	}
}

// TestAnAbsentPropertyIsNotAnEmptyOne. A change-tracking page carries the whole
// object on the enumerating run and only what changed afterwards, so reading an
// absent accountEnabled as false would disable a tenant on its first incremental run,
// and an absent displayName as "" would erase every name.
func TestAnAbsentPropertyIsNotAnEmptyOne(t *testing.T) {
	users := []User{{ID: "usr_1", Username: "ada", DisplayName: "Ada Lovelace",
		Email: "ada@example.org", DirectoryID: "oid-1", Roles: []string{RoleUser}}}
	msg := directorySyncMessage{Users: []directoryUser{{ID: "oid-1"}}}

	plan := decide(t, msg, directorySyncState{}, users, nil)
	if len(plan.Users) != 1 || plan.Users[0].Action != dirUserUnchanged {
		t.Fatalf("plan = %+v, want nothing to change", plan.Users)
	}
	if plan.writes() {
		t.Error("a page that said nothing about an account would have rewritten it")
	}
}

// TestAStaleMessageIsDecidedAndMarked. Staleness is decided here rather than at the
// write, so a reporting run says which batch it was looking at instead of reporting
// about a state that has moved on.
func TestAStaleMessageIsDecidedAndMarked(t *testing.T) {
	msg := directorySyncMessage{FromRevision: 3, Users: []directoryUser{person("oid-1", "a@example.org", "A")}}
	plan := decide(t, msg, directorySyncState{Revision: 5}, nil, nil)
	if !plan.Stale || plan.Current != 5 {
		t.Fatalf("stale=%v current=%d, want it marked against revision 5", plan.Stale, plan.Current)
	}
	if plan.Counts.UsersCreated != 1 {
		t.Error("a stale message is still decided in full, so the report can say what it held")
	}
}

// TestObjectsWithNoIdAreRefusedRatherThanGuessedAt.
func TestObjectsWithNoIdAreRefusedRatherThanGuessedAt(t *testing.T) {
	msg := directorySyncMessage{
		Users:  []directoryUser{{UserPrincipalName: "nobody@example.org"}},
		Groups: []directoryGroup{{DisplayName: "Nameless"}, {ID: "oid-g"}},
	}
	plan := decide(t, msg, directorySyncState{}, nil, nil)
	if plan.Counts.UsersRefused != 1 || plan.Counts.GroupsRefused != 2 {
		t.Fatalf("counts = %+v, want one account and two groups refused", plan.Counts)
	}
	if plan.writes() {
		t.Error("a refusal must write nothing")
	}
}

// TestAMirroredGroupTakingAUsedNameIsReportedNotRefused. The directory owns the name;
// refusing would lose the group, since the change-tracking read will not mention it
// again. So both exist and somebody is told to rename one.
func TestAMirroredGroupTakingAUsedNameIsReportedNotRefused(t *testing.T) {
	groups := []group{{ID: "grp_local", Name: "Team"}}
	msg := directorySyncMessage{Groups: []directoryGroup{{ID: "oid-g", DisplayName: "Team"}}}

	plan := decide(t, msg, directorySyncState{}, nil, groups)
	if len(plan.Groups) != 1 || plan.Groups[0].Action != dirGroupCreate {
		t.Fatalf("plan = %+v, want the group created anyway", plan.Groups)
	}
	if !hasNote(plan, noteNameTaken, "already called this") {
		t.Errorf("the collision must be reported: %+v", plan.Notes)
	}
}

// TestUsernamesDoNotCollide: two directory objects whose principal names reduce to
// the same username each get one, rather than the second failing or overwriting.
func TestUsernamesDoNotCollide(t *testing.T) {
	users := []User{{ID: "usr_0", Username: "ada", Roles: []string{RoleUser}}}
	msg := directorySyncMessage{Users: []directoryUser{
		{ID: "oid-1", UserPrincipalName: "ada@one.example", AccountEnabled: enabled(true)},
		{ID: "oid-2", UserPrincipalName: "ada@two.example", AccountEnabled: enabled(true)},
	}}
	plan := decide(t, msg, directorySyncState{}, users, nil)
	seen := map[string]bool{"ada": true}
	for _, dec := range plan.Users {
		name := dec.Record.Username
		if name == "" || seen[name] {
			t.Fatalf("username %q is empty or already taken", name)
		}
		seen[name] = true
	}
}

// TestTheReportBoundsItsLinesAndCountsWhatItDropped. Nobody reads ten thousand lines,
// and a report that quietly stopped at five hundred would be read as a complete one.
func TestTheReportBoundsItsLinesAndCountsWhatItDropped(t *testing.T) {
	var plan directoryPlan
	for i := 0; i < 12; i++ {
		plan.Notes = append(plan.Notes, directoryNote{Kind: noteMerge, Detail: fmt.Sprint(i)})
	}
	budgets := limits.Default()
	budgets.DirectoryReport = 5

	rep := directoryReportOf(plan, directorySyncMessage{}, directorySyncState{}, false, budgets)
	if len(rep.Notes) != 5 || rep.NotesOmitted != 7 {
		t.Fatalf("notes=%d omitted=%d, want 5 and 7", len(rep.Notes), rep.NotesOmitted)
	}
}

// TestEveryReportThatWroteNothingSaysWhy. A mirror left in reporting mode for a month
// must not look like a mirror with nothing to do, and a repeat delivery must not look
// like somebody forgetting to arm it.
func TestEveryReportThatWroteNothingSaysWhy(t *testing.T) {
	budgets := limits.Default()

	reporting := directoryReportOf(directoryPlan{}, directorySyncMessage{}, directorySyncState{}, false, budgets)
	if reporting.Mode != directoryModeReport || reporting.Applied {
		t.Errorf("mode=%q applied=%v", reporting.Mode, reporting.Applied)
	}
	if !strings.Contains(reporting.Reason, "asked for a report") || !strings.Contains(reporting.Reason, "cursor has not moved") {
		t.Errorf("reason = %q, want it to name the mode and say the cursor stayed", reporting.Reason)
	}
	if reporting.EverApplied {
		t.Error("everApplied must be false on an installation that never wrote")
	}

	stale := directoryReportOf(directoryPlan{Stale: true, Current: 9},
		directorySyncMessage{Apply: true, FromRevision: 4}, directorySyncState{Revision: 9}, false, budgets)
	if stale.Mode != directoryModeApply {
		t.Errorf("mode = %q, want the mode the message asked for", stale.Mode)
	}
	if !strings.Contains(stale.Reason, "revision 4") || !strings.Contains(stale.Reason, "revision 9") {
		t.Errorf("reason = %q, want both revisions named", stale.Reason)
	}

	written := directoryReportOf(directoryPlan{}, directorySyncMessage{Apply: true},
		directorySyncState{Revision: 2, AppliedAt: 100}, true, budgets)
	if written.Reason != "" || !written.Applied || written.Revision != 3 {
		t.Errorf("an applied run = %+v, want no reason and the next revision", written)
	}
}

// TestAnEmptyCursorLeavesTheStoredOneAlone. A message carrying only accounts says
// nothing about where the group read stands, and overwriting that cursor with nothing
// would schedule a full enumeration nobody asked for.
func TestAnEmptyCursorLeavesTheStoredOneAlone(t *testing.T) {
	state := directorySyncState{Revision: 4, UsersDeltaLink: "u-old", GroupsDeltaLink: "g-old"}
	next := advanceDirectoryState(state, directorySyncMessage{UsersDeltaLink: "u-new"}, 55)
	if next.UsersDeltaLink != "u-new" || next.GroupsDeltaLink != "g-old" {
		t.Errorf("cursors = %q / %q, want the named one moved and the other kept",
			next.UsersDeltaLink, next.GroupsDeltaLink)
	}
	if next.Revision != 5 || next.AppliedAt != 55 || next.ID != directorySyncStateID {
		t.Errorf("state = %+v", next)
	}
}

// TestAnIdGeneratorThatFailsStopsTheRun rather than producing a plan with a record
// that has no key.
func TestAnIdGeneratorThatFailsStopsTheRun(t *testing.T) {
	boom := func() (string, error) { return "", fmt.Errorf("no entropy") }
	for _, tc := range []struct {
		name string
		msg  directorySyncMessage
		user bool
	}{
		{"an account", directorySyncMessage{Users: []directoryUser{person("oid-1", "a@example.org", "A")}}, true},
		{"a group", directorySyncMessage{Groups: []directoryGroup{{ID: "oid-g", DisplayName: "Team"}}}, false},
	} {
		userIDs, groupIDs := seqIDs("usr_"), seqIDs("grp_")
		if tc.user {
			userIDs = boom
		} else {
			groupIDs = boom
		}
		if _, err := decideDirectorySync(tc.msg, directorySyncState{}, nil, nil, 1, userIDs, groupIDs); err == nil {
			t.Errorf("%s: decide returned no error", tc.name)
		}
	}
}

// TestAUsernameThatCannotBeMadeFreeIsAnError rather than a hundred-and-first attempt
// silently reusing one.
func TestAUsernameThatCannotBeMadeFreeIsAnError(t *testing.T) {
	users := []User{{ID: "usr_0", Username: "ada", Roles: []string{RoleUser}}}
	for i := 2; i <= 100; i++ {
		users = append(users, User{ID: fmt.Sprintf("usr_%d", i), Username: fmt.Sprintf("ada-%d", i)})
	}
	msg := directorySyncMessage{Users: []directoryUser{person("oid-1", "ada@example.org", "Ada")}}
	if _, err := decideDirectorySync(msg, directorySyncState{}, users, nil, 1, seqIDs("n_"), seqIDs("g_")); err == nil {
		t.Fatal("decide returned no error")
	}
}

// TestAnObjectWithNoUsableNameStillGetsOne: the username falls back through the
// principal name, the address and the display name before it invents anything.
func TestAnObjectWithNoUsableNameStillGetsOne(t *testing.T) {
	cases := []struct {
		in   directoryUser
		want string
	}{
		{directoryUser{UserPrincipalName: "Ada.Lovelace@example.org"}, "ada.lovelace"},
		{directoryUser{Mail: "grace@example.org"}, "grace"},
		{directoryUser{DisplayName: "Katherine"}, "katherine"},
		{directoryUser{}, "entra-user"},
	}
	for _, tc := range cases {
		if got := directoryUsername(tc.in); got != tc.want {
			t.Errorf("directoryUsername(%+v) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// TestMemberKindNamesWhatItSkips, including a type nobody has thought of yet — which
// must read as itself rather than as a person.
func TestMemberKindNamesWhatItSkips(t *testing.T) {
	cases := map[string]string{
		"":                              "",
		"#microsoft.graph.user":         "",
		"#microsoft.graph.group":        "nested group",
		"#microsoft.graph.device":       "device",
		"#microsoft.graph.orgContact":   "directory contact",
		"#microsoft.graph.somethingNew": "somethingNew",
	}
	for in, want := range cases {
		if got := memberKind(in); got != want {
			t.Errorf("memberKind(%q) = %q, want %q", in, got, want)
		}
	}
}

// TestSourceLabelNamesEveryOrigin, because a merge note says which kind of account it
// attached to and "" is a real origin rather than a missing one.
func TestSourceLabelNamesEveryOrigin(t *testing.T) {
	cases := map[string]string{"": "local", SourceOIDC: "federated", SourceEntra: "mirrored", "ldap": "ldap"}
	for in, want := range cases {
		if got := sourceLabel(in); got != want {
			t.Errorf("sourceLabel(%q) = %q, want %q", in, got, want)
		}
	}
}

// TestADepartureNobodyMirroredIsANoteAndNotAnError. An installation that started
// mirroring last week has no record of who left the week before.
func TestADepartureNobodyMirroredIsANoteAndNotAnError(t *testing.T) {
	msg := directorySyncMessage{Users: []directoryUser{{ID: "oid-x", Removed: &directoryRemoval{Reason: "deleted"}}}}
	plan := decide(t, msg, directorySyncState{}, nil, nil)
	if plan.writes() || len(plan.Users) != 0 {
		t.Fatalf("plan = %+v, want nothing decided", plan.Users)
	}
	if !hasNote(plan, noteSkipped, "no account here mirrors it") {
		t.Errorf("notes = %+v", plan.Notes)
	}
}

// TestAnAlreadyDisabledDepartureIsUnchanged, so a leaver does not appear in every
// report for the rest of the installation's life.
func TestAnAlreadyDisabledDepartureIsUnchanged(t *testing.T) {
	users := []User{{ID: "usr_1", Username: "ada", DirectoryID: "oid-1", Disabled: true, Roles: []string{RoleUser}}}
	msg := directorySyncMessage{Users: []directoryUser{{ID: "oid-1", Removed: &directoryRemoval{}}}}
	plan := decide(t, msg, directorySyncState{}, users, nil)
	if plan.writes() || plan.Counts.UsersUnchanged != 1 {
		t.Errorf("plan = %+v counts = %+v", plan.Users, plan.Counts)
	}
}

// TestAnAddressChangeIsCarriedAcross, and releases the old one so a later object may
// take it.
func TestAnAddressChangeIsCarriedAcross(t *testing.T) {
	users := []User{{ID: "usr_1", Username: "ada", Email: "old@example.org",
		DirectoryID: "oid-1", Roles: []string{RoleUser}}}
	msg := directorySyncMessage{Users: []directoryUser{
		person("oid-1", "new@example.org", "Ada"),
		person("oid-2", "old@example.org", "Somebody New"),
	}}
	plan := decide(t, msg, directorySyncState{}, users, nil)
	if len(plan.Users) != 2 {
		t.Fatalf("plan = %+v", plan.Users)
	}
	if plan.Users[0].Record.Email != "new@example.org" {
		t.Errorf("the address did not move: %+v", plan.Users[0].Record)
	}
	if plan.Users[1].Action != dirUserCreate || plan.Users[1].Record.Email != "old@example.org" {
		t.Errorf("the freed address was not reusable: %+v", plan.Users[1])
	}
}

// TestAnAccountTheDirectoryDisablesIsDisabledAndReported — the other leaver signal
// beside @removed, and the more common one.
func TestAnAccountTheDirectoryDisablesIsDisabledAndReported(t *testing.T) {
	users := []User{{ID: "usr_1", Username: "ada", DirectoryID: "oid-1", Roles: []string{RoleUser}}}
	msg := directorySyncMessage{Users: []directoryUser{
		{ID: "oid-1", DisplayName: "Ada", AccountEnabled: enabled(false)}}}
	plan := decide(t, msg, directorySyncState{}, users, nil)
	if len(plan.Users) != 1 || plan.Users[0].Action != dirUserDisable || !plan.Users[0].Record.Disabled {
		t.Fatalf("plan = %+v", plan.Users)
	}
	if !hasNote(plan, noteDisable, "reports this account as disabled") {
		t.Errorf("notes = %+v", plan.Notes)
	}
}

// TestAMergeOntoADisabledObjectStillMerges, and disables — the case of somebody who
// signed in once and had left by the time the mirror was armed.
func TestAMergeOntoADisabledObjectStillMerges(t *testing.T) {
	users := []User{{ID: "usr_1", Username: "ada", Email: "ada@example.org",
		Source: SourceOIDC, ExternalID: "sub", Roles: []string{RoleUser}}}
	msg := directorySyncMessage{Users: []directoryUser{
		{ID: "oid-1", UserPrincipalName: "ada@example.org", Mail: "ada@example.org", AccountEnabled: enabled(false)}}}
	plan := decide(t, msg, directorySyncState{}, users, nil)
	if len(plan.Users) != 1 || plan.Users[0].Action != dirUserMerge || !plan.Users[0].Record.Disabled {
		t.Fatalf("plan = %+v", plan.Users)
	}
}

// TestAMergeThatWouldStrandTheLastAdminStillMerges. The guard holds on every path
// into a disable, not only the one a departure takes — but it refuses the *disable*
// and not the whole page. The merge is what stops a second account existing, and the
// object will not be reported a second time, so abandoning it here would leave the
// duplicate permanently.
func TestAMergeThatWouldStrandTheLastAdminStillMerges(t *testing.T) {
	users := []User{{ID: "usr_1", Username: "root", Email: "root@example.org", Roles: []string{RoleAdmin}}}
	msg := directorySyncMessage{Users: []directoryUser{
		{ID: "oid-1", UserPrincipalName: "root@example.org", Mail: "root@example.org", AccountEnabled: enabled(false)}}}
	plan := decide(t, msg, directorySyncState{}, users, nil)

	if len(plan.Users) != 1 || plan.Users[0].Action != dirUserMerge {
		t.Fatalf("plan = %+v, want the merge carried out", plan.Users)
	}
	if plan.Users[0].Record.Disabled {
		t.Error("the last administrator was disabled after all")
	}
	if plan.Users[0].Record.DirectoryID != "oid-1" {
		t.Error("the account was not linked, so the next run would create a duplicate")
	}
	if plan.Counts.UsersRefused != 1 || !hasNote(plan, noteRefusal, "last enabled administrator") {
		t.Errorf("the refusal is not reported: counts=%+v notes=%+v", plan.Counts, plan.Notes)
	}
}

// TestARefusedDisableKeepsTheRestOfThePage. A change-tracking read sends a changed
// property once; dropping the display name that arrived alongside a refused disable
// would lose it for good.
func TestARefusedDisableKeepsTheRestOfThePage(t *testing.T) {
	users := []User{{ID: "usr_1", Username: "root", DisplayName: "Old", DirectoryID: "oid-1", Roles: []string{RoleAdmin}}}
	msg := directorySyncMessage{Users: []directoryUser{
		{ID: "oid-1", DisplayName: "New Name", AccountEnabled: enabled(false)}}}
	plan := decide(t, msg, directorySyncState{}, users, nil)

	if len(plan.Users) != 1 || plan.Users[0].Action != dirUserUpdate {
		t.Fatalf("plan = %+v, want an ordinary update", plan.Users)
	}
	if plan.Users[0].Record.DisplayName != "New Name" || plan.Users[0].Record.Disabled {
		t.Errorf("record = %+v, want the name carried and the account left enabled", plan.Users[0].Record)
	}
}

// TestADisableThatIsAlreadyTrueOnAKnownAccountChangesNothing — the incremental page
// that repeats accountEnabled: false.
func TestADisableThatIsAlreadyTrueOnAKnownAccountChangesNothing(t *testing.T) {
	users := []User{{ID: "usr_1", Username: "ada", DirectoryID: "oid-1", Disabled: true, Roles: []string{RoleUser}}}
	msg := directorySyncMessage{Users: []directoryUser{{ID: "oid-1", AccountEnabled: enabled(false)}}}
	plan := decide(t, msg, directorySyncState{}, users, nil)
	if plan.writes() {
		t.Errorf("plan = %+v, want nothing to change", plan.Users)
	}
}

// TestAnAccountTheDirectoryEnablesAgainIsEnabled — a leaver who came back.
func TestAnAccountTheDirectoryEnablesAgainIsEnabled(t *testing.T) {
	users := []User{{ID: "usr_1", Username: "ada", DirectoryID: "oid-1", Disabled: true, Roles: []string{RoleUser}}}
	msg := directorySyncMessage{Users: []directoryUser{{ID: "oid-1", AccountEnabled: enabled(true)}}}
	plan := decide(t, msg, directorySyncState{}, users, nil)
	if len(plan.Users) != 1 || plan.Users[0].Action != dirUserUpdate || plan.Users[0].Record.Disabled {
		t.Fatalf("plan = %+v", plan.Users)
	}
}

// TestAnAddressAlreadyHeldIsNotMovedOntoAKnownAccount: the store's uniqueness is what
// a login and an invitation rely on, so the address is left as it was and said.
func TestAnAddressAlreadyHeldIsNotMovedOntoAKnownAccount(t *testing.T) {
	users := []User{
		{ID: "usr_1", Username: "ada", Email: "ada@example.org", DirectoryID: "oid-1", Roles: []string{RoleUser}},
		{ID: "usr_2", Username: "bob", Email: "bob@example.org", DirectoryID: "oid-2", Roles: []string{RoleUser}},
	}
	msg := directorySyncMessage{Users: []directoryUser{person("oid-1", "bob@example.org", "Ada")}}
	plan := decide(t, msg, directorySyncState{}, users, nil)
	for _, dec := range plan.Users {
		if dec.Record.Email == "bob@example.org" && dec.Record.ID == "usr_1" {
			t.Fatal("two accounts would hold one address")
		}
	}
	if !hasNote(plan, noteRefusal, "another account") {
		t.Errorf("notes = %+v", plan.Notes)
	}
}

// TestARemovedMembershipOnAnUnknownGroupCreatesItEmpty — Graph may report a
// membership change on a group this installation has never seen, and inventing a
// group with a member it was told to remove would be worse than an empty one.
func TestARemovedMembershipOnAnUnknownGroupCreatesItEmpty(t *testing.T) {
	msg := directorySyncMessage{Groups: []directoryGroup{{ID: "oid-g", DisplayName: "Team",
		Members: []directoryMemberRef{{ID: "oid-1", Removed: &directoryRemoval{}}, {ID: ""}}}}}
	plan := decide(t, msg, directorySyncState{}, nil, nil)
	if len(plan.Groups) != 1 || len(plan.Groups[0].Record.ExternalMembers) != 0 {
		t.Fatalf("plan = %+v", plan.Groups)
	}
}

// hasNote reports whether the plan carries a note of this kind whose detail contains
// the phrase.
func hasNote(plan directoryPlan, kind, phrase string) bool {
	for _, n := range plan.Notes {
		if n.Kind == kind && strings.Contains(n.Detail, phrase) {
			return true
		}
	}
	return false
}

// TestTheSyncStateStartsAtZeroAndSurvivesAWrite. The absence of the record is a real
// answer — revision 0, no cursor, a first run that enumerates — rather than an error a
// caller would have to turn into the same thing.
func TestTheSyncStateStartsAtZeroAndSurvivesAWrite(t *testing.T) {
	store, err := newDirectorySyncStore(t.TempDir())
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	fresh, err := store.current()
	if err != nil {
		t.Fatalf("current: %v", err)
	}
	if fresh.Revision != 0 || fresh.UsersDeltaLink != "" || fresh.ID != directorySyncStateID {
		t.Fatalf("a fresh state = %+v", fresh)
	}

	next := advanceDirectoryState(fresh, directorySyncMessage{UsersDeltaLink: "u", GroupsDeltaLink: "g"}, 42)
	if err := store.Save(next); err != nil {
		t.Fatalf("save: %v", err)
	}
	back, err := store.current()
	if err != nil {
		t.Fatalf("current: %v", err)
	}
	if back.Revision != 1 || back.UsersDeltaLink != "u" || back.GroupsDeltaLink != "g" || back.AppliedAt != 42 {
		t.Errorf("read back %+v", back)
	}
}

// TestWritesAsksAboutBothHalves. A plan that only decided groups still writes, and one
// whose every decision is a no-op does not — the answer is what the report's "nothing
// changed" rests on.
func TestWritesAsksAboutBothHalves(t *testing.T) {
	cases := []struct {
		name string
		plan directoryPlan
		want bool
	}{
		{"empty", directoryPlan{}, false},
		{"only no-ops", directoryPlan{
			Users:  []directoryUserDecision{{Action: dirUserUnchanged}, {Action: dirUserRefuse}},
			Groups: []directoryGroupDecision{{Action: dirGroupUnchanged}},
		}, false},
		{"an account", directoryPlan{Users: []directoryUserDecision{{Action: dirUserCreate}}}, true},
		{"only a group", directoryPlan{
			Users:  []directoryUserDecision{{Action: dirUserUnchanged}},
			Groups: []directoryGroupDecision{{Action: dirGroupUpdate}},
		}, true},
	}
	for _, tc := range cases {
		if got := tc.plan.writes(); got != tc.want {
			t.Errorf("%s: writes = %v, want %v", tc.name, got, tc.want)
		}
	}
}

// TestAGroupRemovedBeforeItWasEverMirroredIsNotCreated. A change-tracking read can
// report the deletion of a group this installation never saw — because it was made
// and deleted between two runs. Creating an empty record for it would be
// manufacturing a group out of its own deletion.
func TestAGroupRemovedBeforeItWasEverMirroredIsNotCreated(t *testing.T) {
	msg := directorySyncMessage{Groups: []directoryGroup{
		{ID: "oid-ghost", Removed: &directoryRemoval{Reason: "deleted"}},
		{ID: "oid-real", DisplayName: "Team"},
	}}
	plan := decide(t, msg, directorySyncState{}, nil, nil)
	if len(plan.Groups) != 1 || plan.Groups[0].ObjectID != "oid-real" {
		t.Fatalf("plan = %+v, want only the group that exists", plan.Groups)
	}
	if !hasNote(plan, noteSkipped, "no group here mirrors it") {
		t.Errorf("notes = %+v", plan.Notes)
	}
}

// TestAGroupWithNoNameIsRefusedAndTheRestStillDecided. Dropping a working copy must
// not disturb the order the other groups are decided in.
func TestAGroupWithNoNameIsRefusedAndTheRestStillDecided(t *testing.T) {
	msg := directorySyncMessage{Groups: []directoryGroup{
		{ID: "oid-a", DisplayName: "Alpha"},
		{ID: "oid-nameless"},
		{ID: "oid-b", DisplayName: "Beta"},
	}}
	plan := decide(t, msg, directorySyncState{}, nil, nil)
	if len(plan.Groups) != 2 {
		t.Fatalf("plan = %+v, want the two named groups", plan.Groups)
	}
	if plan.Groups[0].ObjectID != "oid-a" || plan.Groups[1].ObjectID != "oid-b" {
		t.Errorf("order = %q, %q; the drop disturbed it", plan.Groups[0].ObjectID, plan.Groups[1].ObjectID)
	}
	if plan.Counts.GroupsRefused != 1 {
		t.Errorf("counts = %+v", plan.Counts)
	}
}
