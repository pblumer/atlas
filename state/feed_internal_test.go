package state

import (
	"testing"

	"github.com/pblumer/atlas/model"
)

func readFeed(t *testing.T, s *Store, partition uint16, after uint64) []FeedEntry {
	t.Helper()
	var out []FeedEntry
	must(t, s.FeedAfter(partition, after, func(e FeedEntry) error {
		out = append(out, e)
		return nil
	}))
	return out
}

// TestTheFeedHoldsItsRowsInPositionOrderPerPartition: rows read back in position order
// from a cursor, one partition's never among another's; a row cannot carry a value its
// kind does not name; the sweep's cut stops at the first row recorded at or after the
// cutoff; a prune drops through its position, remembers the cut, and a lower cut later
// changes nothing.
func TestTheFeedHoldsItsRowsInPositionOrderPerPartition(t *testing.T) {
	s := openStore(t)
	tx := s.NewTransaction()
	grant := &model.EntitlementValue{Principal: "usr_ada", ItemID: "vpn", OrderID: "ord_1", Since: 10}
	must(t, tx.PutFeedEntry(1, 7, 100, FeedGranted, grant))
	must(t, tx.PutFeedEntry(1, 9, 300, FeedOutcome, outcomeOf("ord_1", "vpn", "c-1", "completed")))
	must(t, tx.PutFeedEntry(1, 8, 200, FeedRevoked, &model.EntitlementHistoryValue{Principal: "usr_ada", ItemID: "vpn", EndedAt: 20}))
	must(t, tx.PutFeedEntry(2, 1, 50, FeedGranted, grant))
	if err := tx.PutFeedEntry(1, 10, 400, FeedGranted, outcomeOf("ord_1", "vpn", "c-2", "failed")); err == nil {
		t.Fatal("a grant row carrying an outcome was written")
	}
	if err := tx.PutFeedEntry(1, 10, 400, FeedKind(9), grant); err == nil {
		t.Fatal("a row of an unknown kind was written")
	}
	commit(t, tx)

	rows := readFeed(t, s, 1, 0)
	if len(rows) != 3 || rows[0].Position != 7 || rows[1].Position != 8 || rows[2].Position != 9 {
		t.Fatalf("partition 1 = %+v", rows)
	}
	if rows[0].Granted == nil || rows[1].Revoked == nil || rows[2].Outcome == nil || rows[1].At != 200 {
		t.Fatalf("rows decoded as %+v", rows)
	}
	if after := readFeed(t, s, 1, 8); len(after) != 1 || after[0].Position != 9 {
		t.Fatalf("after 8 = %+v", after)
	}
	if none := readFeed(t, s, 1, ^uint64(0)); len(none) != 0 {
		t.Fatalf("after the last position = %+v", none)
	}
	if other := readFeed(t, s, 2, 0); len(other) != 1 {
		t.Fatalf("partition 2 = %+v", other)
	}

	if through, found, err := s.FeedThroughBefore(1, 250); err != nil || !found || through != 8 {
		t.Fatalf("through before 250 = %d %v %v, want 8", through, found, err)
	}
	if _, found, err := s.FeedThroughBefore(1, 50); err != nil || found {
		t.Fatalf("nothing is older than 50, yet found=%v err=%v", found, err)
	}

	tx = s.NewTransaction()
	must(t, tx.PruneFeed(1, 8))
	commit(t, tx)
	if rows := readFeed(t, s, 1, 0); len(rows) != 1 || rows[0].Position != 9 {
		t.Fatalf("after the prune = %+v", rows)
	}
	if through, err := s.FeedPrunedThrough(1); err != nil || through != 8 {
		t.Fatalf("pruned through = %d %v", through, err)
	}
	if through, err := s.FeedPrunedThrough(2); err != nil || through != 0 {
		t.Fatalf("partition 2 was pruned through %d %v", through, err)
	}
	tx = s.NewTransaction()
	must(t, tx.PruneFeed(1, 3))
	commit(t, tx)
	if through, _ := s.FeedPrunedThrough(1); through != 8 {
		t.Fatalf("a lower cut moved the mark to %d", through)
	}
	if rows := readFeed(t, s, 2, 0); len(rows) != 1 {
		t.Fatalf("pruning partition 1 touched partition 2: %+v", rows)
	}
}

// TestTheFeedsLastPositionIsWhereNowStarts: a feed with rows answers its newest row's
// position, one partition's never another's; an empty feed answers where it was pruned
// through, or 0 when it never was.
func TestTheFeedsLastPositionIsWhereNowStarts(t *testing.T) {
	s := openStore(t)
	if last, err := s.FeedLast(1); err != nil || last != 0 {
		t.Fatalf("an empty feed's last = %d %v, want 0", last, err)
	}
	tx := s.NewTransaction()
	grant := &model.EntitlementValue{Principal: "usr_ada", ItemID: "vpn", OrderID: "ord_1", Since: 10}
	must(t, tx.PutFeedEntry(1, 7, 100, FeedGranted, grant))
	must(t, tx.PutFeedEntry(1, 9, 300, FeedGranted, grant))
	must(t, tx.PutFeedEntry(2, 40, 50, FeedGranted, grant))
	commit(t, tx)
	if last, err := s.FeedLast(1); err != nil || last != 9 {
		t.Fatalf("partition 1's last = %d %v, want 9", last, err)
	}
	tx = s.NewTransaction()
	must(t, tx.PruneFeed(1, 9))
	commit(t, tx)
	if last, err := s.FeedLast(1); err != nil || last != 9 {
		t.Fatalf("a feed pruned empty answers %d %v, want its cut 9", last, err)
	}
}

// TestAFeedRowCutShortIsAnError: a row the store cannot read back is reported, never
// served as a fact.
func TestAFeedRowCutShortIsAnError(t *testing.T) {
	key := keyFeedRow(1, 5)
	for _, raw := range [][]byte{{byte(FeedOutcome)}, append([]byte{9}, make([]byte, 8)...), append([]byte{byte(FeedOutcome)}, make([]byte, 9)...)} {
		if _, err := decodeFeedRow(1, key, raw); err == nil {
			t.Errorf("row %v decoded", raw)
		}
	}
}

// TestAnIncidentRowKeepsTheDefinitionItWasFoldedWith: an incident row reads back with
// the incident and the definition key the fold found, in position order among the
// catalogue's rows, and is no catalogue fact; an incident kind cannot be written
// without its definition, nor a catalogue kind as an incident.
func TestAnIncidentRowKeepsTheDefinitionItWasFoldedWith(t *testing.T) {
	s := openStore(t)
	tx := s.NewTransaction()
	inc := &model.IncidentValue{ProcessInstanceKey: 41, ElementInstanceKey: 42, JobKey: 43, ElementId: 3, RaisedAt: 90, Message: "boom"}
	must(t, tx.PutFeedIncident(1, 5, 100, FeedIncidentRaised, 7, inc))
	must(t, tx.PutFeedEntry(1, 6, 150, FeedGranted, &model.EntitlementValue{Principal: "usr_ada", ItemID: "vpn"}))
	must(t, tx.PutFeedIncident(1, 8, 200, FeedIncidentResolved, 0, inc))
	if err := tx.PutFeedEntry(1, 9, 300, FeedIncidentRaised, inc); err == nil {
		t.Fatal("an incident row was written without its definition")
	}
	if err := tx.PutFeedIncident(1, 9, 300, FeedGranted, 7, inc); err == nil {
		t.Fatal("a grant row was written as an incident")
	}
	commit(t, tx)

	rows := readFeed(t, s, 1, 0)
	if len(rows) != 3 {
		t.Fatalf("rows = %+v", rows)
	}
	raised, grant, resolved := rows[0], rows[1], rows[2]
	if raised.Kind != FeedIncidentRaised || raised.Incident == nil || raised.Definition != 7 ||
		raised.Incident.ElementInstanceKey != 42 || raised.Incident.JobKey != 43 || raised.At != 100 {
		t.Fatalf("raised = %+v (%+v)", raised, raised.Incident)
	}
	if resolved.Kind != FeedIncidentResolved || resolved.Incident == nil || resolved.Definition != 0 {
		t.Fatalf("resolved = %+v", resolved)
	}
	if raised.IsCatalogue() || resolved.IsCatalogue() || !grant.IsCatalogue() {
		t.Fatalf("catalogue facts: raised %v resolved %v grant %v", raised.IsCatalogue(), resolved.IsCatalogue(), grant.IsCatalogue())
	}
}

// TestATaskRowKeepsItsJobElementAndDefinition: a task row reads back with the job as
// the fold saw it, the job's key, the element it waits on and the definition its
// instance ran (ADR-0510 §1) — what the envelope needs
// once the element instance is gone. It is no catalogue fact; a task kind cannot be
// written as an entry or an incident, nor another kind as a task.
func TestATaskRowKeepsItsJobElementAndDefinition(t *testing.T) {
	s := openStore(t)
	tx := s.NewTransaction()
	job := &model.JobValue{ProcessInstanceKey: 41, ElementInstanceKey: 42, JobType: 1, Assignee: "alice",
		CandidateGroups: "hr,finance", Deadline: 900, CreatedAt: 80}
	for i, kind := range []FeedKind{FeedTaskCreated, FeedTaskAssigned, FeedTaskUnassigned, FeedTaskCompleted, FeedTaskCancelled} {
		must(t, tx.PutFeedTask(1, uint64(10+i), int64(100+i), kind, 7, 4300+uint64(i), 3, job))
	}
	if err := tx.PutFeedEntry(1, 20, 300, FeedTaskCreated, job); err == nil {
		t.Fatal("a task row was written without its key, element and definition")
	}
	if err := tx.PutFeedTask(1, 21, 300, FeedIncidentRaised, 7, 1, 3, job); err == nil {
		t.Fatal("an incident row was written as a task")
	}
	commit(t, tx)

	rows := readFeed(t, s, 1, 0)
	if len(rows) != 5 {
		t.Fatalf("rows = %+v", rows)
	}
	for i, want := range []FeedKind{FeedTaskCreated, FeedTaskAssigned, FeedTaskUnassigned, FeedTaskCompleted, FeedTaskCancelled} {
		r := rows[i]
		if r.Kind != want || r.Task == nil || r.TaskKey != 4300+uint64(i) || r.TaskElement != 3 || r.Definition != 7 || r.At != int64(100+i) {
			t.Fatalf("row %d = %+v (%+v), want kind %d", i, r, r.Task, want)
		}
		if r.Task.Assignee != "alice" || r.Task.CandidateGroups != "hr,finance" || r.Task.Deadline != 900 ||
			r.Task.CreatedAt != 80 || r.Task.ElementInstanceKey != 42 {
			t.Fatalf("row %d's job = %+v", i, r.Task)
		}
		if r.IsCatalogue() {
			t.Fatalf("a task row is a catalogue fact: %+v", r)
		}
	}
}

// TestATaskThatEndedIsFoundByItsKeyWhileTheFeedHoldsItsEnd (ADR-0512 §2): the row that
// completed or cancelled a user task is found by the task's key; a task still open is
// not; and the prune that drops the row drops the entry with it, so a task is known as
// ended exactly as long as the feed holds its end.
func TestATaskThatEndedIsFoundByItsKeyWhileTheFeedHoldsItsEnd(t *testing.T) {
	s := openStore(t)
	tx := s.NewTransaction()
	job := &model.JobValue{ProcessInstanceKey: 41, ElementInstanceKey: 42, JobType: 1, Assignee: "alice"}
	must(t, tx.PutFeedTask(1, 10, 100, FeedTaskCreated, 7, 500, 3, job))
	must(t, tx.PutFeedTask(1, 11, 110, FeedTaskCreated, 7, 501, 3, job))
	must(t, tx.PutFeedTask(1, 12, 120, FeedTaskCreated, 7, 502, 3, job))
	must(t, tx.PutFeedTask(1, 13, 130, FeedTaskCompleted, 7, 500, 3, job))
	must(t, tx.PutFeedTask(1, 14, 140, FeedTaskCancelled, 7, 501, 3, job))
	// The same key on another partition is another task.
	must(t, tx.PutFeedTask(2, 13, 130, FeedTaskCompleted, 9, 500, 4, job))
	commit(t, tx)

	e, ok, err := s.FeedTaskEnd(1, 500)
	if err != nil || !ok || e.Kind != FeedTaskCompleted || e.At != 130 || e.Definition != 7 || e.Task.Assignee != "alice" {
		t.Fatalf("FeedTaskEnd(500) = %+v %v %v, want the completion at 130", e, ok, err)
	}
	if e, ok, _ := s.FeedTaskEnd(1, 501); !ok || e.Kind != FeedTaskCancelled || e.Position != 14 {
		t.Fatalf("FeedTaskEnd(501) = %+v %v, want the cancellation at 14", e, ok)
	}
	if _, ok, err := s.FeedTaskEnd(1, 502); ok || err != nil {
		t.Fatalf("an open task was found as ended (%v, %v)", ok, err)
	}
	if _, ok, _ := s.FeedTaskEnd(1, 999); ok {
		t.Fatal("a task nobody opened was found as ended")
	}
	if e, ok, _ := s.FeedTaskEnd(2, 500); !ok || e.Definition != 9 {
		t.Fatalf("partition 2's task = %+v %v", e, ok)
	}

	// Pruning through the completion drops its entry and keeps the cancellation's.
	tx = s.NewTransaction()
	must(t, tx.PruneFeed(1, 13))
	commit(t, tx)
	if _, ok, err := s.FeedTaskEnd(1, 500); ok || err != nil {
		t.Fatalf("a pruned end is still found (%v, %v)", ok, err)
	}
	if _, ok, err := getCopy(s.db, keyFeedTaskEnd(1, 500)); ok || err != nil {
		t.Fatalf("the pruned end's entry is still stored (%v, %v)", ok, err)
	}
	if _, ok, _ := s.FeedTaskEnd(1, 501); !ok {
		t.Fatal("the prune dropped an end it did not reach")
	}
	if _, ok, _ := s.FeedTaskEnd(2, 500); !ok {
		t.Fatal("a prune of partition 1 dropped partition 2's entry")
	}
}

// TestACorruptTaskEndEntryIsAnError: an entry that is not a position, or that names a
// row about another task, is reported rather than read as an answer.
func TestACorruptTaskEndEntryIsAnError(t *testing.T) {
	s := openStore(t)
	tx := s.NewTransaction()
	job := &model.JobValue{ProcessInstanceKey: 41, ElementInstanceKey: 42, JobType: 1}
	must(t, tx.PutFeedTask(1, 10, 100, FeedTaskCompleted, 7, 500, 3, job))
	must(t, tx.b.Set(keyFeedTaskEnd(1, 600), []byte{1, 2, 3}, nil))
	must(t, tx.b.Set(keyFeedTaskEnd(1, 700), appendBE64(nil, 10), nil))
	commit(t, tx)
	if _, _, err := s.FeedTaskEnd(1, 600); err == nil {
		t.Error("a short entry was read as an answer")
	}
	if _, _, err := s.FeedTaskEnd(1, 700); err == nil {
		t.Error("an entry naming another task's row was read as an answer")
	}
}
