package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/pblumer/atlas/model"
)

// The summary's three properties that a test driving the engine cannot pin, because
// each of them is about the *order the incident family is scanned in* — which the
// engine decides, and which an HTTP-level test can only hope for. Writing the records
// straight into the state store is what makes them assertable: the keys and the
// raisedAt stamps are chosen, so "oldest wins" and "biggest first" are checked against
// a scan order that deliberately disagrees with both.

// putIncident writes one incident and the instance it belongs to, so the summary can
// attribute it to a definition. The element instance key is the scan order: the family
// is walked in key order, so a caller chooses where in the walk this incident falls.
func putIncident(t *testing.T, srv *Server, elKey, piKey, defKey uint64, element int32, raisedAt int64, message string) {
	t.Helper()
	srv.do(func() {
		tx := srv.store.NewTransaction()
		if err := tx.PutProcessInstance(piKey, &model.ProcessInstanceValue{ProcessDefKey: defKey, State: model.PIActive}); err != nil {
			t.Errorf("PutProcessInstance: %v", err)
			return
		}
		if err := tx.PutIncident(&model.IncidentValue{
			ElementInstanceKey: elKey,
			ProcessInstanceKey: piKey,
			ElementId:          element,
			JobKey:             elKey + 1, // non-zero → a job incident, the common kind
			RaisedAt:           raisedAt,
			Message:            message,
		}); err != nil {
			t.Errorf("PutIncident: %v", err)
			return
		}
		if err := tx.Commit(); err != nil {
			t.Errorf("Commit: %v", err)
		}
	})
}

// putTimerIncident is putIncident for a job-less incident — the kind a timer whose
// FEEL schedule stopped resolving raises (ADR-0064/0111), which the summary groups
// apart from a parked job on the same element.
func putTimerIncident(t *testing.T, srv *Server, elKey, piKey, defKey uint64, element int32, raisedAt int64, message string) {
	t.Helper()
	srv.do(func() {
		tx := srv.store.NewTransaction()
		if err := tx.PutProcessInstance(piKey, &model.ProcessInstanceValue{ProcessDefKey: defKey, State: model.PIActive}); err != nil {
			t.Errorf("PutProcessInstance: %v", err)
			return
		}
		if err := tx.PutIncident(&model.IncidentValue{
			ElementInstanceKey: elKey,
			ProcessInstanceKey: piKey,
			ElementId:          element,
			RaisedAt:           raisedAt,
			Message:            message,
		}); err != nil {
			t.Errorf("PutIncident: %v", err)
			return
		}
		if err := tx.Commit(); err != nil {
			t.Errorf("Commit: %v", err)
		}
	})
}

func readSummary(t *testing.T, srv *Server) incidentSummaryResp {
	t.Helper()
	w := httptest.NewRecorder()
	srv.handleIncidentSummary(w, httptest.NewRequest("GET", "/api/v1/incidents/summary", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("summary: status=%d body=%s", w.Code, w.Body.String())
	}
	var resp incidentSummaryResp
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode summary: %v (%s)", err, w.Body.String())
	}
	return resp
}

// TestSummaryRepresentativeIsTheOldestWhateverTheScanOrder pins the message a group
// shows. It is the oldest incident's, so two polls of an unchanged flood agree and an
// operator is not reading a different sample every time they refresh — and the scan
// reaches it last here, which is exactly the case a "first one wins" implementation
// gets wrong.
func TestSummaryRepresentativeIsTheOldestWhateverTheScanOrder(t *testing.T) {
	srv, _ := newOffLoopServer(t)
	// Ascending keys (the walk order), descending raisedAt: the oldest is scanned last.
	putIncident(t, srv, 100, 900, 7, 3, 3000, "third")
	putIncident(t, srv, 101, 901, 7, 3, 2000, "second")
	putIncident(t, srv, 102, 902, 7, 3, 1000, "first")

	s := readSummary(t, srv)
	if len(s.Groups) != 1 || s.Groups[0].Count != 3 {
		t.Fatalf("groups = %+v, want one cause of 3", s.Groups)
	}
	g := s.Groups[0]
	if g.Message != "first" {
		t.Errorf("message = %q, want the oldest incident's", g.Message)
	}
	if g.OldestRaisedAt != 1000 || g.NewestRaisedAt != 3000 {
		t.Errorf("window = [%d, %d], want [1000, 3000]", g.OldestRaisedAt, g.NewestRaisedAt)
	}
	if !g.MessageVaries {
		t.Errorf("messageVaries = false, want true — the group holds three wordings")
	}
}

// TestSummaryOrdersByCountThenStably pins the ordering the Operations view depends on:
// the biggest cause first, and — for causes of equal size — an order that does not
// wander between two reads of an unchanged store. A map iteration is random in Go, so
// "stable" here means the sort has to answer it, not the walk.
func TestSummaryOrdersByCountThenStably(t *testing.T) {
	srv, _ := newOffLoopServer(t)
	// One cause of two, and four of one that tie on count and age — so each ordering
	// key below it has to decide one of the pairs.
	putIncident(t, srv, 200, 800, 5, 1, 1000, "a")
	putIncident(t, srv, 201, 801, 5, 1, 1000, "a")
	putIncident(t, srv, 202, 802, 5, 2, 1000, "a") // same definition, later element
	putIncident(t, srv, 203, 803, 4, 1, 1000, "a") // earlier definition, same element
	putIncident(t, srv, 204, 804, 4, 2, 1000, "a") // …and a second element under it
	// A timer incident (no job) on an element that already holds a job one: same
	// definition, same element, same age — everything the ordering looks at except the
	// kind. The comparator has to be total, or these two swap between reads.
	putTimerIncident(t, srv, 205, 805, 4, 2, 1000, "a")

	first := readSummary(t, srv)
	if len(first.Groups) != 5 {
		t.Fatalf("groups = %+v, want five causes", first.Groups)
	}
	if first.Groups[0].Count != 2 {
		t.Errorf("first group = %+v, want the two-incident cause", first.Groups[0])
	}
	// The singletons tie on count and on age, so the definition decides, then the
	// element index, then the kind.
	rest := first.Groups[1:]
	want := []struct {
		def     uint64
		element int32
		kind    string
	}{{4, 1, "job"}, {4, 2, "job"}, {4, 2, "timer"}, {5, 2, "job"}}
	for i, w := range want {
		if rest[i].ProcessDefKey != w.def || rest[i].ElementIndex != w.element || rest[i].Type != w.kind {
			t.Errorf("tie %d = def %d element %d %s, want def %d element %d %s", i,
				rest[i].ProcessDefKey, rest[i].ElementIndex, rest[i].Type, w.def, w.element, w.kind)
		}
	}
	second := readSummary(t, srv)
	for i := range first.Groups {
		if first.Groups[i] != second.Groups[i] {
			t.Errorf("group %d differs between two reads of an unchanged store: %+v vs %+v",
				i, first.Groups[i], second.Groups[i])
		}
	}
}

// TestSummarySaysWhatItsCapLeftOut is the honesty property of the cap. A response that
// simply stopped at the last group it had room for would undercount silently, and the
// number an operator reads is the one they decide from — so `total` counts every
// incident the walk saw, and `ungrouped` says how many are behind the edge.
func TestSummarySaysWhatItsCapLeftOut(t *testing.T) {
	srv, _ := newOffLoopServer(t)
	defer func(n int) { maxIncidentSummaryGroups = n }(maxIncidentSummaryGroups)
	maxIncidentSummaryGroups = 1

	putIncident(t, srv, 300, 700, 9, 1, 1000, "kept")
	putIncident(t, srv, 301, 701, 9, 2, 1000, "cut")
	putIncident(t, srv, 302, 702, 9, 3, 1000, "cut")

	s := readSummary(t, srv)
	if len(s.Groups) != 1 {
		t.Fatalf("groups = %+v, want the cap's one", s.Groups)
	}
	if s.Total != 3 {
		t.Errorf("total = %d, want 3 — every incident the walk saw", s.Total)
	}
	if !s.GroupsTruncated || s.Ungrouped != 2 {
		t.Errorf("groupsTruncated=%v ungrouped=%d, want the cap declared with its 2", s.GroupsTruncated, s.Ungrouped)
	}
}
