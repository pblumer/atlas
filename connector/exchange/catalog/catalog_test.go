package catalog_test

import (
	"reflect"
	"regexp"
	"strings"
	"testing"

	"github.com/pblumer/atlas/connector/exchange/catalog"
)

var opName = regexp.MustCompile(`^[a-z]+(-[a-z]+)*$`)

// TestEveryOperationIsWellFormed holds the table to the rules the rest of the worker
// relies on without re-checking them: a name a model can write, at least one target,
// a cmdlet on every variant, and declared parameters that cannot collide with the ones
// the operation sets itself. A row that broke one of these would compile a model the
// worker then sends as something nobody authored.
func TestEveryOperationIsWellFormed(t *testing.T) {
	if len(catalog.Ops) == 0 {
		t.Fatal("the catalogue is empty")
	}
	areas := map[string]bool{}
	for _, a := range catalog.Areas {
		areas[a] = true
	}
	for name, op := range catalog.Ops {
		t.Run(name, func(t *testing.T) {
			if !opName.MatchString(name) {
				t.Errorf("operation name %q is not lower-case kebab", name)
			}
			if !areas[op.Area] {
				t.Errorf("area %q is not one of %v", op.Area, catalog.Areas)
			}
			if strings.TrimSpace(op.Label) == "" {
				t.Error("no label for error messages")
			}
			if op.Online == nil && op.OnPrem == nil {
				t.Fatal("no target can run it")
			}
			declared := map[string]bool{}
			for _, p := range append(append([]string{}, op.Required...), op.Optional...) {
				if !catalog.ValidParameterName(p) {
					t.Errorf("declared parameter %q is not a PowerShell parameter name", p)
				}
				if declared[strings.ToLower(p)] {
					t.Errorf("parameter %q is declared twice", p)
				}
				declared[strings.ToLower(p)] = true
			}
			if op.AnyOf && len(op.Optional) == 0 {
				t.Error("AnyOf without optional parameters can never be satisfied")
			}
			if op.Generic && op.Result != catalog.ResultList {
				t.Error("the generic read must return a list: it cannot know the cmdlet's shape")
			}
			if op.NeedsResult && op.Result == catalog.ResultNone {
				t.Error("an operation cannot need a result variable it never writes")
			}
			for _, v := range []*catalog.Variant{op.Online, op.OnPrem} {
				if v == nil {
					continue
				}
				if op.Generic {
					if v.Cmdlet != "" {
						t.Error("the generic read takes its cmdlet from the model, not the table")
					}
					continue
				}
				if !catalog.ValidCmdletName(v.Cmdlet) {
					t.Errorf("cmdlet %q is not Verb-Noun", v.Cmdlet)
				}
				if op.Sized && !strings.HasPrefix(v.Cmdlet, "Get-") {
					t.Errorf("only a Get- cmdlet takes ResultSize, not %s", v.Cmdlet)
				}
				for fixed := range v.Fixed {
					if declared[strings.ToLower(fixed)] {
						t.Errorf("%s sets %s itself and also lets a model author it", v.Cmdlet, fixed)
					}
				}
				for from := range v.Rename {
					if !declared[strings.ToLower(from)] {
						t.Errorf("rename of undeclared parameter %q", from)
					}
				}
				for from := range v.Into {
					if !declared[strings.ToLower(from)] {
						t.Errorf("hashtable mapping of undeclared parameter %q", from)
					}
				}
			}
		})
	}
}

// TestNamesAreSorted: the names are what an error lists, and a list that changed order
// between runs would make two identical failures read as different ones.
func TestNamesAreSorted(t *testing.T) {
	names := catalog.Names()
	if len(names) != len(catalog.Ops) {
		t.Fatalf("Names() has %d entries, Ops %d", len(names), len(catalog.Ops))
	}
	for i := 1; i < len(names); i++ {
		if names[i-1] >= names[i] {
			t.Fatalf("Names() is not sorted at %q, %q", names[i-1], names[i])
		}
	}
}

func build(t *testing.T, op string, target catalog.Target, params map[string]any) catalog.Invocation {
	t.Helper()
	spec, ok := catalog.Lookup(op)
	if !ok {
		t.Fatalf("no operation %q", op)
	}
	inv, err := spec.Build(op, target, "", params)
	if err != nil {
		t.Fatalf("Build(%s, %s): %v", op, target, err)
	}
	return inv
}

// TestFullAccessCarriesItsEncoding is the reason a named operation exists: a model says
// who gets access to which mailbox, and the worker adds the access right and the
// inheritance Exchange needs for it to mean "full access".
func TestFullAccessCarriesItsEncoding(t *testing.T) {
	inv := build(t, "add-full-access", catalog.Online, map[string]any{"Identity": "shared@contoso.com", "User": "anna@contoso.com", "AutoMapping": false})
	want := catalog.Invocation{Cmdlet: "Add-MailboxPermission", Parameters: map[string]any{
		"Identity": "shared@contoso.com", "User": "anna@contoso.com", "AutoMapping": false,
		"AccessRights": []any{"FullAccess"}, "InheritanceType": "All",
	}}
	if !reflect.DeepEqual(inv, want) {
		t.Errorf("got %#v\nwant %#v", inv, want)
	}
}

// TestSendAsIsADifferentCmdletPerTarget: Exchange Online grants Send As on the
// recipient, Exchange Server on the Active Directory object. A model says "Send As" once
// and runs against either.
func TestSendAsIsADifferentCmdletPerTarget(t *testing.T) {
	params := map[string]any{"Identity": "info@contoso.com", "User": "anna@contoso.com"}
	online := build(t, "add-send-as", catalog.Online, params)
	if online.Cmdlet != "Add-RecipientPermission" ||
		online.Parameters["Trustee"] != "anna@contoso.com" ||
		online.Parameters["User"] != nil ||
		!reflect.DeepEqual(online.Parameters["AccessRights"], []any{"SendAs"}) {
		t.Errorf("online send-as = %#v", online)
	}
	onprem := build(t, "add-send-as", catalog.OnPrem, params)
	if onprem.Cmdlet != "Add-ADPermission" ||
		onprem.Parameters["User"] != "anna@contoso.com" ||
		!reflect.DeepEqual(onprem.Parameters["ExtendedRights"], []any{"Send As"}) {
		t.Errorf("on-premises send-as = %#v", onprem)
	}
}

// TestAddressGoesIntoAHashtable: Exchange modifies a multi-valued property through a
// hashtable of Add and Remove, which is exactly the encoding a model should not have
// to know.
func TestAddressGoesIntoAHashtable(t *testing.T) {
	inv := build(t, "add-email-address", catalog.Online, map[string]any{"Identity": "anna@contoso.com", "Address": "smtp:a.muster@contoso.com"})
	want := map[string]any{
		"Identity":       "anna@contoso.com",
		"EmailAddresses": map[string]any{"Add": "smtp:a.muster@contoso.com"},
	}
	if inv.Cmdlet != "Set-Mailbox" || !reflect.DeepEqual(inv.Parameters, want) {
		t.Errorf("got %#v", inv)
	}
	inv = build(t, "revoke-send-on-behalf", catalog.OnPrem, map[string]any{"Identity": "info@contoso.com", "User": "anna"})
	if !reflect.DeepEqual(inv.Parameters["GrantSendOnBehalfTo"], map[string]any{"Remove": "anna"}) {
		t.Errorf("revoke-send-on-behalf = %#v", inv.Parameters)
	}
}

// TestClearForwardingSendsNulls: removing a forward is setting both forwarding
// properties to nothing, not leaving them out.
func TestClearForwardingSendsNulls(t *testing.T) {
	inv := build(t, "clear-forwarding", catalog.Online, map[string]any{"Identity": "anna@contoso.com"})
	for _, k := range []string{"ForwardingSmtpAddress", "ForwardingAddress"} {
		v, ok := inv.Parameters[k]
		if !ok || v != nil {
			t.Errorf("%s = %#v (present %v), want an explicit null", k, v, ok)
		}
	}
	if inv.Parameters["DeliverToMailboxAndForward"] != false {
		t.Errorf("DeliverToMailboxAndForward = %#v", inv.Parameters["DeliverToMailboxAndForward"])
	}
}

// TestParameterNamesAreCaseInsensitive: PowerShell binds -identity to -Identity, so a
// model that wrote it in lower case is not missing it — and is sent in the spelling the
// documentation uses.
func TestParameterNamesAreCaseInsensitive(t *testing.T) {
	inv := build(t, "get-mailbox", catalog.Online, map[string]any{"identity": "anna@contoso.com"})
	if inv.Parameters["Identity"] != "anna@contoso.com" {
		t.Errorf("got %#v", inv.Parameters)
	}
}

// TestOpenOperationsPassFurtherParameters: an update takes whatever the cmdlet takes —
// Set-Mailbox has well over a hundred parameters and a list of them would be out of
// date before it was finished.
func TestOpenOperationsPassFurtherParameters(t *testing.T) {
	inv := build(t, "update-mailbox", catalog.OnPrem, map[string]any{"Identity": "anna", "CustomAttribute7": "Kostenstelle 4711", "HiddenFromAddressListsEnabled": true})
	if inv.Parameters["CustomAttribute7"] != "Kostenstelle 4711" || inv.Parameters["HiddenFromAddressListsEnabled"] != true {
		t.Errorf("got %#v", inv.Parameters)
	}
}

func TestBuildRefusals(t *testing.T) {
	cases := []struct {
		name   string
		op     string
		target catalog.Target
		cmdlet string
		params map[string]any
		want   string
	}{
		{"missing required", "add-full-access", catalog.Online, "", map[string]any{"Identity": "x"}, "needs User"},
		{"empty required", "add-full-access", catalog.Online, "", map[string]any{"Identity": "x", "User": "  "}, "needs User"},
		{"null required", "get-mailbox", catalog.Online, "", map[string]any{"Identity": nil}, "needs Identity"},
		{"unknown on closed op", "add-full-access", catalog.Online, "", map[string]any{"Identity": "x", "User": "y", "Deny": true}, `does not take "Deny"`},
		{"fixed collision", "add-full-access", catalog.Online, "", map[string]any{"Identity": "x", "User": "y", "AccessRights": "ReadPermission"}, "sets AccessRights itself"},
		{"fixed collision on open op", "enable-litigation-hold", catalog.Online, "", map[string]any{"Identity": "x", "LitigationHoldEnabled": false}, "sets LitigationHoldEnabled itself"},
		{"common parameter", "update-mailbox", catalog.Online, "", map[string]any{"Identity": "x", "WhatIf": true}, "common parameter"},
		{"invalid name", "update-mailbox", catalog.Online, "", map[string]any{"Identity": "x", "Foo; Remove-Mailbox": 1}, "not a parameter name"},
		{"duplicate by case", "update-mailbox", catalog.Online, "", map[string]any{"Identity": "x", "identity": "y"}, "twice"},
		{"result size on sized op", "list-mailboxes", catalog.Online, "", map[string]any{"ResultSize": "Unlimited"}, "maxResults"},
		{"none of any-of", "set-mailbox-quota", catalog.Online, "", map[string]any{"Identity": "x"}, "at least one of"},
		{"not on this target", "list-send-as", catalog.OnPrem, "", map[string]any{"Identity": "x"}, "Exchange Server"},
		{"generic without cmdlet", "run-get-cmdlet", catalog.Online, "", nil, "needs a cmdlet"},
		{"generic write cmdlet", "run-get-cmdlet", catalog.Online, "Set-Mailbox", nil, "Get-"},
		{"generic malformed cmdlet", "run-get-cmdlet", catalog.Online, "Get-Mailbox; Remove-Mailbox", nil, "Get-"},
		{"cmdlet on named op", "get-mailbox", catalog.Online, "Get-User", map[string]any{"Identity": "x"}, "takes no cmdlet"},
		{"unknown target", "get-mailbox", catalog.Target("cloud"), "", map[string]any{"Identity": "x"}, "target"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			spec, ok := catalog.Lookup(c.op)
			if !ok {
				t.Fatalf("no operation %q", c.op)
			}
			_, err := spec.Build(c.op, c.target, c.cmdlet, c.params)
			if err == nil {
				t.Fatalf("Build accepted %v", c.params)
			}
			if !strings.Contains(err.Error(), c.want) {
				t.Errorf("error %q does not mention %q", err, c.want)
			}
		})
	}
}

// TestGenericReadIsTheAuthoredCmdlet: the escape hatch runs the Get- cmdlet the model
// names with whatever parameters it authors, ResultSize included — the worker cannot
// know whether that cmdlet takes one.
func TestGenericReadIsTheAuthoredCmdlet(t *testing.T) {
	spec, _ := catalog.Lookup("run-get-cmdlet")
	inv, err := spec.Build("run-get-cmdlet", catalog.OnPrem, "Get-ADPermission", map[string]any{"Identity": "info", "ResultSize": 10})
	if err != nil {
		t.Fatal(err)
	}
	if inv.Cmdlet != "Get-ADPermission" || inv.Parameters["ResultSize"] != 10 {
		t.Errorf("got %#v", inv)
	}
}

// TestCheckNamesAgreesWithBuild: the compiler checks the authored names at deploy, where
// the values may still be FEEL, through the same rules Build applies at call time — so a
// model that deploys is not refused later for a name.
func TestCheckNamesAgreesWithBuild(t *testing.T) {
	spec, _ := catalog.Lookup("add-full-access")
	if err := spec.CheckNames("add-full-access", []string{"Identity", "User"}); err != nil {
		t.Errorf("CheckNames refused a complete model: %v", err)
	}
	if err := spec.CheckNames("add-full-access", []string{"Identity"}); err == nil || !strings.Contains(err.Error(), "needs User") {
		t.Errorf("CheckNames without User = %v", err)
	}
	if err := spec.CheckNames("add-full-access", []string{"Identity", "User", "Deny"}); err == nil {
		t.Error("CheckNames accepted an undeclared parameter on a closed operation")
	}
	// A model that supplies its parameters through a variable has no names to check at
	// deploy; the empty set must not be refused there for what only the value can say.
	if err := spec.CheckNames("add-full-access", nil); err != nil {
		t.Errorf("CheckNames(nil) = %v", err)
	}
}

func TestValidNames(t *testing.T) {
	for name, want := range map[string]bool{
		"Identity": true, "CustomAttribute15": true, "": false, "1st": false,
		"Identity ": false, "-Identity": false, "Foo-Bar": false,
	} {
		if got := catalog.ValidParameterName(name); got != want {
			t.Errorf("ValidParameterName(%q) = %v", name, got)
		}
	}
	for name, want := range map[string]bool{
		"Get-Mailbox": true, "Add-ADPermission": true, "Get-Mailbox;": false,
		"Get-": false, "Mailbox": false, "Get-Mailbox Remove-Mailbox": false,
	} {
		if got := catalog.ValidCmdletName(name); got != want {
			t.Errorf("ValidCmdletName(%q) = %v", name, got)
		}
	}
	for name, want := range map[string]bool{
		"Get-Mailbox": true, "get-mailbox": true, "Set-Mailbox": false, "Get-": false,
	} {
		if got := catalog.ValidReadCmdlet(name); got != want {
			t.Errorf("ValidReadCmdlet(%q) = %v", name, got)
		}
	}
}

func TestParseTarget(t *testing.T) {
	for in, want := range map[string]catalog.Target{"": catalog.Online, "online": catalog.Online, " OnPrem ": catalog.OnPrem, "on-premises": catalog.OnPrem} {
		got, err := catalog.ParseTarget(in)
		if err != nil || got != want {
			t.Errorf("ParseTarget(%q) = %q, %v", in, got, err)
		}
	}
	if _, err := catalog.ParseTarget("cloud"); err == nil {
		t.Error("ParseTarget accepted an unknown target")
	}
}

// TestNullParametersAreLeftOut: a FEEL expression over a variable that is not set
// evaluates to null, and Exchange would read a null as "clear this property".
func TestNullParametersAreLeftOut(t *testing.T) {
	inv := build(t, "update-mailbox", catalog.Online, map[string]any{"Identity": "anna", "CustomAttribute1": nil, "Office": "Bern"})
	if _, sent := inv.Parameters["CustomAttribute1"]; sent {
		t.Errorf("a null parameter was sent: %#v", inv.Parameters)
	}
	if inv.Parameters["Office"] != "Bern" {
		t.Errorf("got %#v", inv.Parameters)
	}
}

func TestCheckAuthored(t *testing.T) {
	spec, _ := catalog.Lookup("add-full-access")
	if err := spec.CheckAuthored("add-full-access", map[string]any{"Identity": "=postfach", "User": "anna"}); err != nil {
		t.Errorf("a complete template was refused: %v", err)
	}
	for _, params := range []map[string]any{
		{"Identity": "x", "User": ""},
		{"Identity": "x", "User": nil},
		{"Identity": "x"},
		{},
	} {
		if err := spec.CheckAuthored("add-full-access", params); err == nil || !strings.Contains(err.Error(), "needs") {
			t.Errorf("CheckAuthored(%v) = %v", params, err)
		}
	}
}
