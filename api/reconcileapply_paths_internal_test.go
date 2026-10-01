package api

import (
	"encoding/json"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/pblumer/atlas/api/catalog"
	"github.com/pblumer/atlas/limits"
)

// How a reconciliation run's conclusions reach the journal (ADR-0334), when the
// journal is full or cannot take a write, and how the report is cut for reading.

// reconcileApplyPathsBlockSave puts a directory where the store writes a record's
// temporary file, so saving that one record fails while every read of the store —
// a listing, a lookup — still succeeds. It is the narrowest broken write there
// is: one record, nothing else disturbed.
func reconcileApplyPathsBlockSave(t *testing.T, recordPath string) {
	t.Helper()
	if err := os.MkdirAll(recordPath+".tmp", 0o755); err != nil {
		t.Fatalf("block %s: %v", recordPath, err)
	}
}

// TestReconcileApplyAFullJournalRecordsWhatFitsAndSaysSo. The ceiling refuses new
// findings and the report says how many were not recorded: a run that found
// several things and recorded one must not read like a quiet one.
func TestReconcileApplyAFullJournalRecordsWhatFitsAndSaysSo(t *testing.T) {
	budgets := limits.Default()
	budgets.ReconcileJournal = 1
	srv := newServerWithOptions(t, WithLimits(budgets))
	reconcileHTTPPathsEstate(t, srv)
	var err error
	srv.do(func() {
		for _, u := range []User{
			{ID: "usr_bo", Username: "bo", DirectoryID: "oid-bo"},
			{ID: "usr_cy", Username: "cy", DirectoryID: "oid-cy"},
		} {
			if err = srv.users.Save(u); err != nil {
				return
			}
		}
	})
	if err != nil {
		t.Fatalf("seed accounts: %v", err)
	}

	code, raw := reconcileHTTPPathsPost(t, srv, `{"system":"ad","refs":["CN=VPN"],"observations":[
		{"subject":"oid-ada","ref":"CN=VPN"},
		{"subject":"oid-bo","ref":"CN=VPN"},
		{"subject":"oid-cy","ref":"CN=VPN"}]}`)
	if code != http.StatusOK {
		t.Fatalf("run = %d %s", code, raw)
	}
	var rep reconcileReport
	if err := json.Unmarshal([]byte(raw), &rep); err != nil {
		t.Fatalf("decode report: %v", err)
	}
	if rep.Counts.Unmanaged != 2 || rep.Opened != 1 || rep.NotRecorded != 1 {
		t.Errorf("unmanaged = %d opened = %d notRecorded = %d, want 2 found, 1 recorded, 1 refused",
			rep.Counts.Unmanaged, rep.Opened, rep.NotRecorded)
	}
	if !strings.Contains(rep.Reason, "1 new finding(s) were not recorded") {
		t.Errorf("reason = %q, want the refusal said", rep.Reason)
	}
	if open := reconcileHTTPPathsOpen(t, srv, ""); len(open) != 1 {
		t.Errorf("journal holds %d open finding(s), want the ceiling of 1", len(open))
	}
}

// TestReconcileApplyAFindingThatCannotBeWrittenFailsTheRun. A run whose new
// finding cannot be recorded answers 500 naming it, rather than a report that
// describes a finding the journal does not hold.
func TestReconcileApplyAFindingThatCannotBeWrittenFailsTheRun(t *testing.T) {
	srv := newServerForErrors(t)
	reconcileHTTPPathsEstate(t, srv)
	var err error
	srv.do(func() { err = srv.users.Save(User{ID: "usr_bo", Username: "bo", DirectoryID: "oid-bo"}) })
	if err != nil {
		t.Fatalf("seed bo: %v", err)
	}
	id := discrepancyID(recUnmanaged, "ad", "usr_bo", "vpn")
	reconcileApplyPathsBlockSave(t, srv.discrepancies.FileFor(id))

	code, body := reconcileHTTPPathsPost(t, srv, `{"system":"ad","refs":["CN=VPN"],"observations":[
		{"subject":"oid-ada","ref":"CN=VPN"},{"subject":"oid-bo","ref":"CN=VPN"}]}`)
	if code != http.StatusInternalServerError || !strings.Contains(body, "record discrepancy "+id) {
		t.Errorf("run = %d %s, want 500 naming the finding", code, body)
	}
	if open := reconcileHTTPPathsOpen(t, srv, ""); len(open) != 0 {
		t.Errorf("journal = %+v, want nothing recorded", open)
	}
}

// TestReconcileApplyAFindingThatCannotBeClosedStaysOpen. A finding that has gone
// away is closed by the next run; if that write fails the run says so and the
// finding still stands — the safe direction, since the next run closes it again.
func TestReconcileApplyAFindingThatCannotBeClosedStaysOpen(t *testing.T) {
	srv := newServerForErrors(t)
	reconcileHTTPPathsEstate(t, srv)
	now := time.Now().Unix()
	standing := discrepancyRecord{ID: discrepancyID(recMissing, "ad", "usr_ada", "vpn"), Kind: recMissing,
		System: "ad", Principal: "usr_ada", ItemID: "vpn", OpenedAt: now, LastSeenAt: now}
	seedDiscrepancy(t, srv, standing)
	reconcileApplyPathsBlockSave(t, srv.discrepancies.FileFor(standing.ID))

	// Ada is now seen in the group, so the missing finding is gone.
	code, body := reconcileHTTPPathsPost(t, srv, `{"system":"ad","refs":["CN=VPN"],"observations":[
		{"subject":"oid-ada","ref":"CN=VPN"}]}`)
	if code != http.StatusInternalServerError || !strings.Contains(body, "close discrepancy "+standing.ID) {
		t.Errorf("run = %d %s, want 500 naming the finding it could not close", code, body)
	}
	if open := reconcileHTTPPathsOpen(t, srv, ""); len(open) != 1 || open[0].ID != standing.ID {
		t.Errorf("journal = %+v, want the finding still open", open)
	}
}

// TestReconcileApplyAReportIsCutAcrossFindingsAndNotes. The two share one
// ceiling, because what a reader can absorb is a number of lines, and what was
// cut is counted rather than lost.
func TestReconcileApplyAReportIsCutAcrossFindingsAndNotes(t *testing.T) {
	budgets := limits.Default()
	budgets.ReconcileReport = 1
	plan := reconcilePlan{
		Found: []discrepancy{
			{ID: "a", Kind: recUnmanaged, Principal: "usr_a", ItemID: "vpn"},
			{ID: "b", Kind: recUnmanaged, Principal: "usr_b", ItemID: "vpn"},
		},
		Notes: []discrepancy{{Kind: recNoSubject, Principal: "oid-x", Ref: "CN=VPN"}},
	}
	rep := reconcileReportOf(plan, reconcileMessage{System: "ad"}, 0, 0, 0, 0, budgets)

	if len(rep.Findings) != 1 || len(rep.Notes) != 0 {
		t.Errorf("findings = %d notes = %d, want one line in all", len(rep.Findings), len(rep.Notes))
	}
	if rep.Omitted != 2 {
		t.Errorf("omitted = %d, want the second finding and the note counted", rep.Omitted)
	}
	if len(rep.Scope) != 0 || rep.Scope == nil || rep.Subjects == nil {
		t.Errorf("scope = %#v subjects = %#v, want empty lists rather than nil", rep.Scope, rep.Subjects)
	}
}

// TestReconcileApplyAnEmptyReadingOfAnEmptyScopeSaysBothSidesAreEmpty. A reading
// with no observations over a group nobody is recorded as holding is agreement,
// and the reason says so in those words rather than leaving a bare zero.
func TestReconcileApplyAnEmptyReadingOfAnEmptyScopeSaysBothSidesAreEmpty(t *testing.T) {
	in := reconcileInput{Users: []User{ada()}, Items: []catalog.Item{{ID: "vpn", State: catalog.StateActive,
		Targets: []catalog.TargetRef{{System: "ad", Ref: "CN=VPN"}}}}}
	msg := reading("ad", []string{"CN=VPN"})
	plan := decideReconcile(msg, in)

	if len(plan.Found) != 0 {
		t.Fatalf("found = %+v, want nothing", plan.Found)
	}
	if why := reconcileReason(plan, msg); !strings.Contains(why, "Both sides are empty") {
		t.Errorf("reason = %q, want it to say both sides are empty", why)
	}
}
