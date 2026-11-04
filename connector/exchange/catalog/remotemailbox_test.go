package catalog_test

import (
	"strings"
	"testing"

	"github.com/pblumer/atlas/connector/exchange/catalog"
)

// TestRemoteMailboxesAreExchangeServerOnly: in an Exchange hybrid organization the mail
// attributes of a synchronised account are written on premises (Enable-RemoteMailbox),
// and the remote-mailbox cmdlets exist only there. On Exchange Online the operations are
// refused with what to do instead, not sent as a cmdlet that does not exist (ADR-0462,
// amended 2026-11-04).
func TestRemoteMailboxesAreExchangeServerOnly(t *testing.T) {
	enable := build(t, "enable-remote-mailbox", catalog.OnPrem, map[string]any{
		"Identity": "CN=Anna Muster,OU=Personen,DC=contoso,DC=local", "RemoteRoutingAddress": "anna.muster@contoso.mail.onmicrosoft.com",
	})
	if enable.Cmdlet != "Enable-RemoteMailbox" || enable.Parameters["RemoteRoutingAddress"] != "anna.muster@contoso.mail.onmicrosoft.com" {
		t.Errorf("enable-remote-mailbox = %#v", enable)
	}
	for op, cmdlet := range map[string]string{
		"get-remote-mailbox": "Get-RemoteMailbox", "list-remote-mailboxes": "Get-RemoteMailbox", "disable-remote-mailbox": "Disable-RemoteMailbox",
	} {
		params := map[string]any{"Identity": "anna.muster"}
		if op == "list-remote-mailboxes" {
			params = map[string]any{"Filter": "Alias -eq 'anna.muster'"}
		}
		if inv := build(t, op, catalog.OnPrem, params); inv.Cmdlet != cmdlet {
			t.Errorf("%s runs %s, want %s", op, inv.Cmdlet, cmdlet)
		}
	}
	for _, op := range []string{"enable-remote-mailbox", "get-remote-mailbox", "list-remote-mailboxes", "disable-remote-mailbox"} {
		spec, _ := catalog.Lookup(op)
		_, err := spec.Build(op, catalog.Online, "", map[string]any{"Identity": "anna.muster", "RemoteRoutingAddress": "x@y"})
		if err == nil || !strings.Contains(err.Error(), "get-mailbox") {
			t.Errorf("%s on Exchange Online: err = %v, want a refusal that names get-mailbox", op, err)
		}
	}
	// A remote mailbox without its routing address in Exchange Online is refused at
	// deploy, not at the first order.
	spec, _ := catalog.Lookup("enable-remote-mailbox")
	if err := spec.CheckNames("enable-remote-mailbox", []string{"Identity"}); err == nil || !strings.Contains(err.Error(), "RemoteRoutingAddress") {
		t.Errorf("enable-remote-mailbox without RemoteRoutingAddress: err = %v", err)
	}
}
