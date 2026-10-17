// Package catalog is the Exchange Worker's operation table: which Exchange cmdlet each
// low-code operation runs, which parameters a model must and may author for it, and
// which ones the operation sets itself (ADR-0462).
//
// # Why it is a package of its own
//
// Every other Worker Type keeps its operation rules twice — once in its connector
// package and once in the compiler — because the connector imports the compiler and
// the compiler therefore cannot import it back; a behavioural drift test then keeps the
// two copies in step. That is bearable at ten operations with three fields each. The
// Exchange table is some forty operations, each with its own parameter list, and a
// second copy of it would be the larger half of the code.
//
// So the table lives here, in a package that imports nothing of Atlas's, and both the
// compiler (deploy-time checks) and connector/exchange (call-time checks and the
// request itself) read the same rows. The cause of the duplication — the direction of
// the import — is removed instead of guarded.
//
// # What an operation is
//
// An operation is a named, checked call to one Exchange cmdlet. Its value over a
// hand-authored cmdlet call is threefold: the model says what it means ("give Anna
// full access to the shared mailbox") and the operation adds the encoding Exchange
// needs for it (AccessRights FullAccess, InheritanceType All); the parameters a call
// cannot work without are refused at deploy rather than at call time; and where
// Exchange Online and Exchange Server need different cmdlets for the same intent —
// Send As is a recipient permission in one and an Active Directory permission in the
// other — the operation picks the right one for the target, so one model runs against
// either.
//
// Parameter names are the cmdlets' own (Identity, User, AccessRights, …), so
// Microsoft's documentation of each cmdlet stays the documentation of the operation.
// The few places an operation renames one are stated in its [Variant].
package catalog

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
)

// Target is the kind of Exchange organization a Worker talks to.
type Target string

const (
	// Online is Exchange Online in Microsoft 365, reached over its REST endpoint.
	Online Target = "online"
	// OnPrem is Exchange Server in the customer's own forest, reached over Remote
	// PowerShell.
	OnPrem Target = "onprem"
)

// ParseTarget reads a target as an operator writes it. Empty is [Online]: it is the
// common case, and the one an Entra-style credential bundle describes.
func ParseTarget(s string) (Target, error) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "", "online", "exchange-online":
		return Online, nil
	case "onprem", "on-prem", "on-premises", "onpremises", "server":
		return OnPrem, nil
	}
	return "", fmt.Errorf("exchange: unknown target %q (want online or onprem)", s)
}

// Result is what an operation hands back to a result variable.
type Result int

const (
	// ResultNone marks an operation whose cmdlet returns nothing worth keeping (a Set-,
	// a Remove-). A result variable on it would name a value that is never written,
	// so the compiler refuses one.
	ResultNone Result = iota
	// ResultObject is one object, or null when the cmdlet returned none.
	ResultObject
	// ResultList is an array, empty when the cmdlet returned nothing.
	ResultList
)

// Areas are the groups the Modeler lists operations under, in display order.
var Areas = []string{"mailbox", "permission", "group", "settings", "generic"}

// HashKey names where an authored value goes when the cmdlet takes it inside a
// hashtable: Set-Mailbox -EmailAddresses @{Add="smtp:…"} is Param "EmailAddresses",
// Key "Add".
type HashKey struct {
	Param string
	Key   string
}

// Variant is how one operation reaches one target.
type Variant struct {
	// Cmdlet is the Exchange cmdlet the operation runs. It is empty on the generic
	// read, whose cmdlet the model names.
	Cmdlet string
	// Rename maps an authored parameter to the name this cmdlet calls it — Online's
	// Add-RecipientPermission calls the person a Trustee where every other permission
	// cmdlet calls them a User, and a model should not have to know which.
	Rename map[string]string
	// Into moves an authored value into a hashtable parameter.
	Into map[string]HashKey
	// Fixed are the parameters the operation sets itself: they are what makes
	// Add-MailboxPermission mean "full access". A nil value is sent as an explicit
	// null, which is how Exchange clears a property.
	Fixed map[string]any
}

// Op is one low-code operation.
type Op struct {
	// Area groups the operation in the Modeler (one of [Areas]).
	Area string
	// Label describes the operation in an error message.
	Label string
	// Required are the parameters a model must author; Optional the ones it may.
	Required []string
	Optional []string
	// Open lets a model author parameters beyond the declared ones. It is set on the
	// update operations, whose cmdlets take more parameters than any list here would
	// keep up with; on every other operation an undeclared parameter is refused,
	// because the encoding the operation adds would otherwise be overridable.
	Open bool
	// AnyOf requires at least one of Optional — a quota change that changes no quota
	// is a call made for nothing.
	AnyOf bool
	// Result is what the operation returns; NeedsResult that it is a read whose result
	// a model must keep.
	Result      Result
	NeedsResult bool
	// Sized marks a listing whose cmdlet takes -ResultSize. The worker sets it from
	// the task's maxResults, one past the cap, so an oversized listing is detected
	// without being read in full — which is why a model may not author it here.
	Sized bool
	// Generic marks the read-only escape hatch: the model names any Get- cmdlet.
	Generic bool
	// Online and OnPrem are how the operation reaches each target; nil where it
	// cannot.
	Online *Variant
	OnPrem *Variant
	// Unavailable says what to do instead on a target whose variant is nil.
	Unavailable string
}

// Invocation is one cmdlet call, ready for a backend: the cmdlet and its parameters
// with every rename, hashtable and fixed value applied.
type Invocation struct {
	Cmdlet     string
	Parameters map[string]any
}

// GenericOp is the name of the read-only escape hatch.
const GenericOp = "run-get-cmdlet"

// same is the common case: an operation that runs the same cmdlet on either target.
func same(o Op, v Variant) Op {
	o.Online, o.OnPrem = &v, &v
	return o
}

// split is an operation whose cmdlet differs between the two targets.
func split(o Op, online, onprem *Variant) Op {
	o.Online, o.OnPrem = online, onprem
	return o
}

var (
	fullAccess = map[string]any{"AccessRights": []any{"FullAccess"}, "InheritanceType": "All"}

	// identity is the parameter almost every operation addresses its object by.
	identity = []string{"Identity"}

	newRecipient = []string{"DisplayName", "Alias", "PrimarySmtpAddress", "OrganizationalUnit", "UserPrincipalName"}
)

// Ops is the operation table. It covers the four areas an Exchange administration
// process touches — mailboxes, the permissions on them, distribution groups and mail
// contacts, and per-mailbox settings — plus one read-only escape hatch for whatever a
// Get- cmdlet can answer that no row here does.
var Ops = map[string]Op{
	// Mailboxes.
	"get-mailbox": same(Op{Area: "mailbox", Label: "read a mailbox", Required: identity, Result: ResultObject, NeedsResult: true},
		Variant{Cmdlet: "Get-Mailbox"}),
	"list-mailboxes": same(Op{Area: "mailbox", Label: "list mailboxes", Optional: []string{"Filter", "RecipientTypeDetails", "Anr", "OrganizationalUnit"}, Result: ResultList, NeedsResult: true, Sized: true},
		Variant{Cmdlet: "Get-Mailbox"}),
	"create-shared-mailbox": same(Op{Area: "mailbox", Label: "create a shared mailbox", Required: []string{"Name"}, Optional: newRecipient, Result: ResultObject},
		Variant{Cmdlet: "New-Mailbox", Fixed: map[string]any{"Shared": true}}),
	"create-room-mailbox": same(Op{Area: "mailbox", Label: "create a room mailbox", Required: []string{"Name"}, Optional: append([]string{"ResourceCapacity"}, newRecipient...), Result: ResultObject},
		Variant{Cmdlet: "New-Mailbox", Fixed: map[string]any{"Room": true}}),
	"create-equipment-mailbox": same(Op{Area: "mailbox", Label: "create an equipment mailbox", Required: []string{"Name"}, Optional: newRecipient, Result: ResultObject},
		Variant{Cmdlet: "New-Mailbox", Fixed: map[string]any{"Equipment": true}}),
	"update-mailbox": same(Op{Area: "mailbox", Label: "update a mailbox", Required: identity, Open: true},
		Variant{Cmdlet: "Set-Mailbox"}),
	"convert-mailbox": same(Op{Area: "mailbox", Label: "convert a mailbox's type", Required: []string{"Identity", "Type"}},
		Variant{Cmdlet: "Set-Mailbox"}),
	"add-email-address": same(Op{Area: "mailbox", Label: "add an e-mail address", Required: []string{"Identity", "Address"}},
		Variant{Cmdlet: "Set-Mailbox", Into: map[string]HashKey{"Address": {"EmailAddresses", "Add"}}}),
	"remove-email-address": same(Op{Area: "mailbox", Label: "remove an e-mail address", Required: []string{"Identity", "Address"}},
		Variant{Cmdlet: "Set-Mailbox", Into: map[string]HashKey{"Address": {"EmailAddresses", "Remove"}}}),
	"remove-mailbox": same(Op{Area: "mailbox", Label: "remove a mailbox", Required: identity},
		Variant{Cmdlet: "Remove-Mailbox"}),
	"get-mailbox-statistics": same(Op{Area: "mailbox", Label: "read a mailbox's statistics", Required: identity, Result: ResultObject, NeedsResult: true},
		Variant{Cmdlet: "Get-MailboxStatistics"}),

	// Permissions.
	"add-full-access": same(Op{Area: "permission", Label: "grant full access", Required: []string{"Identity", "User"}, Optional: []string{"AutoMapping"}, Result: ResultObject},
		Variant{Cmdlet: "Add-MailboxPermission", Fixed: fullAccess}),
	"remove-full-access": same(Op{Area: "permission", Label: "revoke full access", Required: []string{"Identity", "User"}},
		Variant{Cmdlet: "Remove-MailboxPermission", Fixed: fullAccess}),
	"list-mailbox-permissions": same(Op{Area: "permission", Label: "list mailbox permissions", Required: identity, Optional: []string{"User"}, Result: ResultList, NeedsResult: true},
		Variant{Cmdlet: "Get-MailboxPermission"}),
	"add-send-as": split(Op{Area: "permission", Label: "grant send as", Required: []string{"Identity", "User"}, Result: ResultObject},
		&Variant{Cmdlet: "Add-RecipientPermission", Rename: map[string]string{"User": "Trustee"}, Fixed: map[string]any{"AccessRights": []any{"SendAs"}}},
		&Variant{Cmdlet: "Add-ADPermission", Fixed: map[string]any{"ExtendedRights": []any{"Send As"}}}),
	"remove-send-as": split(Op{Area: "permission", Label: "revoke send as", Required: []string{"Identity", "User"}},
		&Variant{Cmdlet: "Remove-RecipientPermission", Rename: map[string]string{"User": "Trustee"}, Fixed: map[string]any{"AccessRights": []any{"SendAs"}}},
		&Variant{Cmdlet: "Remove-ADPermission", Fixed: map[string]any{"ExtendedRights": []any{"Send As"}}}),
	"list-send-as": {Area: "permission", Label: "list send-as permissions", Required: identity, Optional: []string{"User"}, Result: ResultList, NeedsResult: true,
		Online:      &Variant{Cmdlet: "Get-RecipientPermission", Rename: map[string]string{"User": "Trustee"}},
		Unavailable: "Exchange Server keeps Send As as an Active Directory permission; read it with run-get-cmdlet and Get-ADPermission"},
	"grant-send-on-behalf": same(Op{Area: "permission", Label: "grant send on behalf", Required: []string{"Identity", "User"}},
		Variant{Cmdlet: "Set-Mailbox", Into: map[string]HashKey{"User": {"GrantSendOnBehalfTo", "Add"}}}),
	"revoke-send-on-behalf": same(Op{Area: "permission", Label: "revoke send on behalf", Required: []string{"Identity", "User"}},
		Variant{Cmdlet: "Set-Mailbox", Into: map[string]HashKey{"User": {"GrantSendOnBehalfTo", "Remove"}}}),
	"add-folder-permission": same(Op{Area: "permission", Label: "grant a folder permission", Required: []string{"Identity", "User", "AccessRights"}, Optional: []string{"SharingPermissionFlags", "SendNotificationToUser"}, Result: ResultObject},
		Variant{Cmdlet: "Add-MailboxFolderPermission"}),
	"set-folder-permission": same(Op{Area: "permission", Label: "change a folder permission", Required: []string{"Identity", "User", "AccessRights"}, Optional: []string{"SharingPermissionFlags", "SendNotificationToUser"}},
		Variant{Cmdlet: "Set-MailboxFolderPermission"}),
	"remove-folder-permission": same(Op{Area: "permission", Label: "revoke a folder permission", Required: []string{"Identity", "User"}},
		Variant{Cmdlet: "Remove-MailboxFolderPermission"}),
	"list-folder-permissions": same(Op{Area: "permission", Label: "list folder permissions", Required: identity, Optional: []string{"User"}, Result: ResultList, NeedsResult: true},
		Variant{Cmdlet: "Get-MailboxFolderPermission"}),

	// Distribution groups and mail contacts.
	"create-distribution-group": same(Op{Area: "group", Label: "create a distribution group", Required: []string{"Name"},
		Optional: []string{"DisplayName", "Alias", "PrimarySmtpAddress", "Type", "ManagedBy", "Members", "Notes", "OrganizationalUnit", "MemberJoinRestriction", "MemberDepartRestriction", "RequireSenderAuthenticationEnabled"}, Result: ResultObject},
		Variant{Cmdlet: "New-DistributionGroup"}),
	"get-distribution-group": same(Op{Area: "group", Label: "read a distribution group", Required: identity, Result: ResultObject, NeedsResult: true},
		Variant{Cmdlet: "Get-DistributionGroup"}),
	"list-distribution-groups": same(Op{Area: "group", Label: "list distribution groups", Optional: []string{"Filter", "Anr", "RecipientTypeDetails", "OrganizationalUnit"}, Result: ResultList, NeedsResult: true, Sized: true},
		Variant{Cmdlet: "Get-DistributionGroup"}),
	"update-distribution-group": same(Op{Area: "group", Label: "update a distribution group", Required: identity, Open: true},
		Variant{Cmdlet: "Set-DistributionGroup"}),
	"remove-distribution-group": same(Op{Area: "group", Label: "remove a distribution group", Required: identity, Optional: []string{"BypassSecurityGroupManagerCheck"}},
		Variant{Cmdlet: "Remove-DistributionGroup"}),
	"add-distribution-group-member": same(Op{Area: "group", Label: "add a distribution group member", Required: []string{"Identity", "Member"}, Optional: []string{"BypassSecurityGroupManagerCheck"}},
		Variant{Cmdlet: "Add-DistributionGroupMember"}),
	"remove-distribution-group-member": same(Op{Area: "group", Label: "remove a distribution group member", Required: []string{"Identity", "Member"}, Optional: []string{"BypassSecurityGroupManagerCheck"}},
		Variant{Cmdlet: "Remove-DistributionGroupMember"}),
	"list-distribution-group-members": same(Op{Area: "group", Label: "list distribution group members", Required: identity, Result: ResultList, NeedsResult: true, Sized: true},
		Variant{Cmdlet: "Get-DistributionGroupMember"}),
	"create-mail-contact": same(Op{Area: "group", Label: "create a mail contact", Required: []string{"Name", "ExternalEmailAddress"},
		Optional: []string{"DisplayName", "Alias", "FirstName", "LastName", "PrimarySmtpAddress", "OrganizationalUnit"}, Result: ResultObject},
		Variant{Cmdlet: "New-MailContact"}),
	"get-mail-contact": same(Op{Area: "group", Label: "read a mail contact", Required: identity, Result: ResultObject, NeedsResult: true},
		Variant{Cmdlet: "Get-MailContact"}),
	"update-mail-contact": same(Op{Area: "group", Label: "update a mail contact", Required: identity, Open: true},
		Variant{Cmdlet: "Set-MailContact"}),
	"remove-mail-contact": same(Op{Area: "group", Label: "remove a mail contact", Required: identity},
		Variant{Cmdlet: "Remove-MailContact"}),

	// Per-mailbox settings.
	"get-auto-reply": same(Op{Area: "settings", Label: "read an automatic reply", Required: identity, Result: ResultObject, NeedsResult: true},
		Variant{Cmdlet: "Get-MailboxAutoReplyConfiguration"}),
	"set-auto-reply": same(Op{Area: "settings", Label: "set an automatic reply", Required: []string{"Identity", "AutoReplyState"},
		Optional: []string{"InternalMessage", "ExternalMessage", "ExternalAudience", "StartTime", "EndTime"}},
		Variant{Cmdlet: "Set-MailboxAutoReplyConfiguration"}),
	"set-forwarding": same(Op{Area: "settings", Label: "set mail forwarding", Required: []string{"Identity", "ForwardingSmtpAddress"}, Optional: []string{"DeliverToMailboxAndForward"}},
		Variant{Cmdlet: "Set-Mailbox"}),
	"clear-forwarding": same(Op{Area: "settings", Label: "clear mail forwarding", Required: identity},
		Variant{Cmdlet: "Set-Mailbox", Fixed: map[string]any{"ForwardingSmtpAddress": nil, "ForwardingAddress": nil, "DeliverToMailboxAndForward": false}}),
	"enable-litigation-hold": same(Op{Area: "settings", Label: "enable litigation hold", Required: identity, Optional: []string{"LitigationHoldDuration", "LitigationHoldOwner", "RetentionComment", "RetentionUrl"}},
		Variant{Cmdlet: "Set-Mailbox", Fixed: map[string]any{"LitigationHoldEnabled": true}}),
	"disable-litigation-hold": same(Op{Area: "settings", Label: "disable litigation hold", Required: identity},
		Variant{Cmdlet: "Set-Mailbox", Fixed: map[string]any{"LitigationHoldEnabled": false}}),
	"get-calendar-processing": same(Op{Area: "settings", Label: "read calendar processing", Required: identity, Result: ResultObject, NeedsResult: true},
		Variant{Cmdlet: "Get-CalendarProcessing"}),
	"set-calendar-processing": same(Op{Area: "settings", Label: "set calendar processing", Required: identity, Open: true},
		Variant{Cmdlet: "Set-CalendarProcessing"}),
	"set-mailbox-quota": same(Op{Area: "settings", Label: "set mailbox quotas", Required: identity, AnyOf: true,
		Optional: []string{"IssueWarningQuota", "ProhibitSendQuota", "ProhibitSendReceiveQuota", "UseDatabaseQuotaDefaults", "RecoverableItemsQuota", "RecoverableItemsWarningQuota"}},
		Variant{Cmdlet: "Set-Mailbox"}),

	// The read-only escape hatch.
	GenericOp: same(Op{Area: "generic", Label: "run a Get- cmdlet", Open: true, Result: ResultList, NeedsResult: true, Generic: true},
		Variant{}),
}

// Names lists the operations, sorted, for the messages that have to say what was
// expected.
func Names() []string {
	out := make([]string, 0, len(Ops))
	for n := range Ops {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}

// Lookup returns the operation called name.
func Lookup(name string) (Op, bool) {
	o, ok := Ops[name]
	return o, ok
}

var (
	paramName  = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9]*$`)
	cmdletName = regexp.MustCompile(`^[A-Za-z]+-[A-Za-z][A-Za-z0-9]*$`)
)

// ValidParameterName reports whether name can be a PowerShell parameter name. The
// check is what keeps an authored name from being anything but a name on its way to a
// cmdlet, whichever backend binds it.
func ValidParameterName(name string) bool { return paramName.MatchString(name) }

// ValidCmdletName reports whether name has the Verb-Noun shape of a cmdlet.
func ValidCmdletName(name string) bool { return cmdletName.MatchString(name) }

// ValidReadCmdlet reports whether name is a cmdlet the generic read may run: Get- and
// nothing else. Exchange's Get- cmdlets read; every verb that changes something is
// reached only through an operation that names what it changes.
func ValidReadCmdlet(name string) bool {
	return ValidCmdletName(name) && strings.EqualFold(name[:4], "Get-")
}

// commonParameters are PowerShell's own parameters, which every cmdlet takes and none
// of which a model should set. Most would change how the call reports rather than
// what it does; WhatIf would make it do nothing while the process believes it changed
// something, and Confirm is the worker's to manage.
var commonParameters = map[string]bool{
	"confirm": true, "whatif": true, "verbose": true, "debug": true,
	"erroraction": true, "errorvariable": true, "warningaction": true, "warningvariable": true,
	"informationaction": true, "informationvariable": true, "outvariable": true,
	"outbuffer": true, "pipelinevariable": true, "progressaction": true,
}

// ResultSizeParameter is the parameter a Sized listing is bounded by.
const ResultSizeParameter = "ResultSize"

// CheckNames checks the authored parameter names against the operation — the half of
// [Op.Build] that does not need the values, which is what the compiler can check at
// deploy where a value may still be a FEEL expression. name is the operation's own
// name, for the messages.
//
// An empty set is accepted: a model that supplies its parameters through a variable
// has none to show at deploy, and the call-time check in Build is then the one that
// speaks.
func (o Op) CheckNames(name string, keys []string) error {
	if len(keys) == 0 {
		return nil
	}
	_, err := o.canonical(name, keys)
	if err != nil {
		return err
	}
	return o.checkRequired(name, keys, func(string) bool { return true })
}

// CheckAuthored checks an authored parameter object at deploy: the names as
// [Op.CheckNames] does, and a required parameter authored as null or a blank string as
// missing — a value the model wrote down and that can never be sent. A FEEL expression
// (a string starting with '=') is a value; whether it evaluates to one is the call's
// question.
func (o Op) CheckAuthored(name string, params map[string]any) error {
	keys := make([]string, 0, len(params))
	for k := range params {
		keys = append(keys, k)
	}
	if _, err := o.canonical(name, keys); err != nil {
		return err
	}
	return o.checkRequired(name, keys, func(k string) bool { return hasValue(params[k]) })
}

// canonical maps every authored key to the spelling the operation declares (PowerShell
// binds parameter names case-insensitively, so -identity is -Identity) and refuses the
// names no call may carry.
func (o Op) canonical(name string, keys []string) (map[string]string, error) {
	declared := map[string]string{}
	for _, p := range append(append([]string{}, o.Required...), o.Optional...) {
		declared[strings.ToLower(p)] = p
	}
	fixed := map[string]bool{}
	for _, v := range []*Variant{o.Online, o.OnPrem} {
		if v == nil {
			continue
		}
		for f := range v.Fixed {
			fixed[strings.ToLower(f)] = true
		}
	}
	sorted := append([]string(nil), keys...)
	sort.Strings(sorted)
	out := make(map[string]string, len(keys))
	seen := map[string]string{}
	for _, k := range sorted {
		if !ValidParameterName(k) {
			return nil, fmt.Errorf("exchange: %s: %q is not a parameter name (letters and digits, starting with a letter)", name, k)
		}
		low := strings.ToLower(k)
		if prev, dup := seen[low]; dup {
			return nil, fmt.Errorf("exchange: %s: parameter %s is authored twice (%q and %q); PowerShell does not tell the two apart", name, k, prev, k)
		}
		seen[low] = k
		if commonParameters[low] {
			return nil, fmt.Errorf("exchange: %s: %s is a PowerShell common parameter the Worker manages; remove it", name, k)
		}
		if fixed[low] {
			return nil, fmt.Errorf("exchange: %s sets %s itself — that is what makes it %q; use another operation for a different value", name, fixedSpelling(o, low), o.Label)
		}
		if o.Sized && low == strings.ToLower(ResultSizeParameter) {
			return nil, fmt.Errorf("exchange: %s bounds its listing with the task's maxResults; author that instead of ResultSize", name)
		}
		if canon, ok := declared[low]; ok {
			out[k] = canon
			continue
		}
		if !o.Open {
			return nil, fmt.Errorf("exchange: %s does not take %q (it takes %s)", name, k, strings.Join(append(append([]string{}, o.Required...), o.Optional...), ", "))
		}
		out[k] = k
	}
	return out, nil
}

// fixedSpelling returns a fixed parameter in the spelling the table gives it.
func fixedSpelling(o Op, low string) string {
	for _, v := range []*Variant{o.Online, o.OnPrem} {
		if v == nil {
			continue
		}
		for f := range v.Fixed {
			if strings.ToLower(f) == low {
				return f
			}
		}
	}
	return low
}

// checkRequired reports the first required parameter that is missing, and an AnyOf
// operation with none of its optional ones. present says whether an authored key
// carries a value.
func (o Op) checkRequired(name string, keys []string, present func(key string) bool) error {
	have := map[string]bool{}
	for _, k := range keys {
		if present(k) {
			have[strings.ToLower(k)] = true
		}
	}
	for _, r := range o.Required {
		if !have[strings.ToLower(r)] {
			return fmt.Errorf("exchange: %s needs %s", name, r)
		}
	}
	if o.AnyOf {
		for _, p := range o.Optional {
			if have[strings.ToLower(p)] {
				return nil
			}
		}
		return fmt.Errorf("exchange: %s needs at least one of %s", name, strings.Join(o.Optional, ", "))
	}
	return nil
}

// Variant returns how the operation reaches target, or an error that says what to do
// instead when it cannot.
func (o Op) Variant(name string, target Target) (*Variant, error) {
	var v *Variant
	switch target {
	case Online:
		v = o.Online
	case OnPrem:
		v = o.OnPrem
	default:
		return nil, fmt.Errorf("exchange: unknown target %q (want online or onprem)", target)
	}
	if v == nil {
		where := "Exchange Online"
		if target == OnPrem {
			where = "Exchange Server"
		}
		msg := fmt.Sprintf("exchange: %s is not available against %s", name, where)
		if o.Unavailable != "" {
			msg += ": " + o.Unavailable
		}
		return nil, fmt.Errorf("%s", msg)
	}
	return v, nil
}

// Build turns an operation and its authored, evaluated parameters into the cmdlet
// call a backend sends to target. cmdlet is the authored cmdlet of the generic read
// and must be empty on every other operation.
//
// A required parameter whose value is null or blank is missing: a FEEL expression
// that evaluated to nothing is the common way to arrive here, and sending it would
// make Exchange answer about an object called "".
func (o Op) Build(name string, target Target, cmdlet string, params map[string]any) (Invocation, error) {
	v, err := o.Variant(name, target)
	if err != nil {
		return Invocation{}, err
	}
	cmdlet = strings.TrimSpace(cmdlet)
	switch {
	case o.Generic && cmdlet == "":
		return Invocation{}, fmt.Errorf("exchange: %s needs a cmdlet to run (a Get- cmdlet)", name)
	case o.Generic && !ValidReadCmdlet(cmdlet):
		return Invocation{}, fmt.Errorf("exchange: %s runs Get- cmdlets only, not %q; a change goes through the operation that names it", name, cmdlet)
	case !o.Generic && cmdlet != "":
		return Invocation{}, fmt.Errorf("exchange: %s takes no cmdlet; it runs %s", name, v.Cmdlet)
	}
	keys := make([]string, 0, len(params))
	for k := range params {
		keys = append(keys, k)
	}
	canon, err := o.canonical(name, keys)
	if err != nil {
		return Invocation{}, err
	}
	if err := o.checkRequired(name, keys, func(k string) bool { return hasValue(params[k]) }); err != nil {
		return Invocation{}, err
	}
	out := make(map[string]any, len(params)+len(v.Fixed))
	for k, val := range params {
		// A null is left out rather than sent. It is almost always a FEEL expression
		// over a variable that is not set, and Exchange reads an explicit null as
		// "clear this property" — a missing input must not empty a mailbox's
		// forwarding or a group's owner. Clearing is what the operations that set a
		// null themselves (clear-forwarding) are for.
		if val == nil {
			continue
		}
		p := canon[k]
		if hk, ok := v.Into[p]; ok {
			h, _ := out[hk.Param].(map[string]any)
			if h == nil {
				h = map[string]any{}
				out[hk.Param] = h
			}
			h[hk.Key] = val
			continue
		}
		if to, ok := v.Rename[p]; ok {
			p = to
		}
		out[p] = val
	}
	for k, val := range v.Fixed {
		out[k] = val
	}
	if o.Generic {
		v = &Variant{Cmdlet: cmdlet}
	}
	return Invocation{Cmdlet: v.Cmdlet, Parameters: out}, nil
}

// hasValue reports whether an authored value says anything.
func hasValue(v any) bool {
	switch t := v.(type) {
	case nil:
		return false
	case string:
		return strings.TrimSpace(t) != ""
	}
	return true
}
