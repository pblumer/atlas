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
