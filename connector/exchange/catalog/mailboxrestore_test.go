package catalog_test

import (
	"strings"
	"testing"

	"github.com/pblumer/atlas/connector/exchange/catalog"
)

// TestDisableMailboxIsExchangeServerOnly: Disable-Mailbox removes the Exchange
// attributes from the account and disconnects the mailbox, whose content stays in the
// database for the retention period — what restore-mailbox-content reads afterwards.
// Exchange Online has no such step for a user mailbox, so the operation is refused
// there with what to do instead (ADR-draft-informationstransfer-operations).
func TestDisableMailboxIsExchangeServerOnly(t *testing.T) {
	inv := build(t, "disable-mailbox", catalog.OnPrem, map[string]any{"Identity": "anna.muster"})
	if inv.Cmdlet != "Disable-Mailbox" || inv.Parameters["Identity"] != "anna.muster" || len(inv.Parameters) != 1 {
		t.Errorf("disable-mailbox = %#v", inv)
	}
	spec, _ := catalog.Lookup("disable-mailbox")
	if _, err := spec.Build("disable-mailbox", catalog.Online, "", map[string]any{"Identity": "anna.muster"}); err == nil || !strings.Contains(err.Error(), "licence") {
		t.Errorf("disable-mailbox on Exchange Online: err = %v, want a refusal that says the mailbox follows the account and its licence", err)
	}
	if err := spec.CheckNames("disable-mailbox", []string{"Archive"}); err == nil {
		t.Error("disable-mailbox took a parameter it does not declare")
	}
	if spec.Result != catalog.ResultNone {
		t.Errorf("disable-mailbox returns %v, want nothing", spec.Result)
	}
}

// TestRestoreMailboxContentRestoresADisconnectedMailbox: New-MailboxRestoreRequest
// copies a disconnected mailbox's content from its database into another mailbox. The
// operation sets AllowLegacyDNMismatch itself — the target is a different mailbox than
// the one disconnected, which Exchange refuses without it — and a model cannot turn it
// off. Exchange Online is refused: there a restore reads only a soft-deleted or
// inactive mailbox.
func TestRestoreMailboxContentRestoresADisconnectedMailbox(t *testing.T) {
	inv := build(t, "restore-mailbox-content", catalog.OnPrem, map[string]any{
		"SourceDatabase": "DB01", "SourceStoreMailbox": "1d20855f-fd54-4681-98e6-e249f7326ddd", "TargetMailbox": "anna.neu",
		"Name": "transfer-anna", "TargetRootFolder": "Altes Postfach", "BadItemLimit": 10,
	})
	if inv.Cmdlet != "New-MailboxRestoreRequest" || inv.Parameters["AllowLegacyDNMismatch"] != true ||
		inv.Parameters["SourceStoreMailbox"] != "1d20855f-fd54-4681-98e6-e249f7326ddd" || inv.Parameters["TargetRootFolder"] != "Altes Postfach" || inv.Parameters["BadItemLimit"] != 10 {
		t.Errorf("restore-mailbox-content = %#v", inv)
	}
	spec, _ := catalog.Lookup("restore-mailbox-content")
	if spec.Area != "mailbox" || spec.Result != catalog.ResultObject {
		t.Errorf("restore-mailbox-content: area %q, result %v; want mailbox and the request object", spec.Area, spec.Result)
	}
	for _, missing := range []string{"SourceDatabase", "SourceStoreMailbox", "TargetMailbox"} {
		keys := []string{}
		for _, k := range []string{"SourceDatabase", "SourceStoreMailbox", "TargetMailbox"} {
			if k != missing {
				keys = append(keys, k)
			}
		}
		if err := spec.CheckNames("restore-mailbox-content", keys); err == nil || !strings.Contains(err.Error(), missing) {
			t.Errorf("without %s: err = %v, want it named", missing, err)
		}
	}
	if err := spec.CheckNames("restore-mailbox-content", []string{"SourceDatabase", "SourceStoreMailbox", "TargetMailbox", "AllowLegacyDNMismatch"}); err == nil || !strings.Contains(err.Error(), "AllowLegacyDNMismatch") {
		t.Errorf("authoring AllowLegacyDNMismatch: err = %v, want a refusal naming it", err)
	}
	_, err := spec.Build("restore-mailbox-content", catalog.Online, "", map[string]any{"SourceDatabase": "DB01", "SourceStoreMailbox": "x", "TargetMailbox": "y"})
	if err == nil || !strings.Contains(err.Error(), "soft-deleted or inactive") {
		t.Errorf("restore-mailbox-content on Exchange Online: err = %v, want a refusal that says what Exchange Online restores", err)
	}
	// The request's progress is read with the generic read, not an operation of its own.
	if !catalog.ValidReadCmdlet("Get-MailboxRestoreRequest") {
		t.Error("Get-MailboxRestoreRequest is not a cmdlet run-get-cmdlet may run")
	}
}
