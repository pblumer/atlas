package interaction

import (
	"errors"
	"reflect"
	"sort"
	"testing"

	"github.com/pblumer/atlas/state"
)

// scanBefore and scanAfter read rows as the event log's two cursors do.
func scanBefore(before uint64, rows ...state.EventLogEntry) func(func(state.EventLogEntry) error) error {
	sort.Slice(rows, func(i, j int) bool { return rows[i].Position > rows[j].Position })
	return func(fn func(state.EventLogEntry) error) error {
		for _, r := range rows {
			if before != 0 && r.Position >= before {
				continue
			}
			if err := fn(r); err != nil {
				return err
			}
		}
		return nil
	}
}

func scanAfter(after uint64, rows ...state.EventLogEntry) func(func(state.EventLogEntry) error) error {
	sort.Slice(rows, func(i, j int) bool { return rows[i].Position < rows[j].Position })
	return func(fn func(state.EventLogEntry) error) error {
		for _, r := range rows {
			if r.Position <= after {
				continue
			}
			if err := fn(r); err != nil {
				return err
			}
		}
		return nil
	}
}

func replay(t *testing.T, in ReplayInput) ReplayPage {
	t.Helper()
	if in.ProcessID == "" {
		in.ProcessID = "order"
	}
	if in.Definitions == nil {
		in.Definitions = fixture()
	}
	if in.ScanLimit == 0 {
		in.ScanLimit = 1000
	}
	if in.Limit == 0 {
		in.Limit = 100
	}
	if in.Now == 0 {
		in.Now = at(60)
	}
	p, err := Replay(in)
	if err != nil {
		t.Fatalf("Replay: %v", err)
	}
	return p
}

type brief struct{ kind, from, to, message string }

func briefs(p ReplayPage) []brief {
	out := make([]brief, len(p.Events))
	for i, e := range p.Events {
		out[i] = brief{e.Kind, e.From, e.To, e.Message}
	}
	return out
}

// TestTheReplayListsTheGraphsArrivalsOldestFirst: the history of the window, read
// newest first, answers the arrivals oldest first — the kept message, the publish, the
// send that reached nobody, the release that started shipping — and nothing of the
// audit process; it reached the window's start, so there is no more to read.
func TestTheReplayListsTheGraphsArrivalsOldestFirst(t *testing.T) {
	p := replay(t, ReplayInput{Input: Input{Scan: scanBefore(0, logRows()...), ReadsAll: true, From: at(0)}})
	want := []brief{
		{EventReceived, "publish", "process:order", "Shipped"},
		{EventReceived, "publish", "process:order", "Shipped"},
		{EventUnreceived, "process:order", "", "OrderReleased"},
		{EventStarted, "process:order", "process:shipping", "OrderReleased"},
	}
	if got := briefs(p); !reflect.DeepEqual(got, want) {
		t.Fatalf("events = %+v, want %+v", got, want)
	}
	if p.Events[0].At != stamp(at(10)) || p.Events[3].Position != "100" || p.Events[3].Count != 1 {
		t.Errorf("first and last event = %+v / %+v", p.Events[0], p.Events[3])
	}
	if p.More || p.Next != "60" || p.Latest != "101" || p.Rows != 9 {
		t.Errorf("page = more %v next %s latest %s rows %d, want the window read to its start", p.More, p.Next, p.Latest, p.Rows)
	}
	ids := map[string]bool{}
	for _, pt := range p.Participants {
		ids[pt.ID] = true
	}
	if !ids["process:order"] || !ids["process:shipping"] || !ids["publish"] || ids["process:audit"] {
		t.Errorf("participants = %+v", p.Participants)
	}
	if p.Participants[0].ID != "process:order" || !p.Participants[0].Center {
		t.Errorf("first participant = %+v, want the center", p.Participants[0])
	}
}

// TestAReplayPageEndsBetweenCommands: with room for one event, the first page holds
// the newest command's arrival and stops before the next command; the second page,
// read before its cursor, continues there.
func TestAReplayPageEndsBetweenCommands(t *testing.T) {
	first := replay(t, ReplayInput{Input: Input{Scan: scanBefore(0, logRows()...), ReadsAll: true, From: at(0)}, Limit: 1})
	if got := briefs(first); len(got) != 1 || got[0].kind != EventStarted || !first.More || first.Next != "100" {
		t.Fatalf("first page = %+v, more %v, next %s", got, first.More, first.Next)
	}
	second := replay(t, ReplayInput{Input: Input{Scan: scanBefore(100, logRows()...), ReadsAll: true, From: at(0)}, Limit: 1})
	if got := briefs(second); len(got) != 1 || got[0].kind != EventUnreceived || second.Next != "90" {
		t.Fatalf("second page = %+v, next %s", got, second.Next)
	}

	// The row bound cuts a page the same way.
	cut := replay(t, ReplayInput{Input: Input{Scan: scanBefore(0, logRows()...), ReadsAll: true, From: at(0), ScanLimit: 2}})
	if !cut.More || cut.Rows != 2 || len(cut.Events) != 1 {
		t.Errorf("page cut at two rows = %+v", cut)
	}
}

// TestTheReplayFollowsWhatCameAfter: reading Newer after a cursor answers what came
// since, oldest first, and echoes the cursor when nothing did.
func TestTheReplayFollowsWhatCameAfter(t *testing.T) {
	p := replay(t, ReplayInput{Input: Input{Scan: scanAfter(85, logRows()...), ReadsAll: true, From: at(0)}, Direction: Newer, After: 85})
	want := []brief{
		{EventUnreceived, "process:order", "", "OrderReleased"},
		{EventStarted, "process:order", "process:shipping", "OrderReleased"},
	}
	if got := briefs(p); !reflect.DeepEqual(got, want) || p.Latest != "101" || p.More {
		t.Fatalf("after 85 = %+v (latest %s, more %v), want %+v", got, p.Latest, p.More, want)
	}
	idle := replay(t, ReplayInput{Input: Input{Scan: scanAfter(101, logRows()...), ReadsAll: true, From: at(0)}, Direction: Newer, After: 101})
	if len(idle.Events) != 0 || idle.Latest != "101" {
		t.Errorf("after the newest = %+v, want nothing and the same cursor", idle)
	}
	// Rows older than the window are skipped, not a reason to stop: a cursor from
	// before the window still reads what lies inside it.
	old := replay(t, ReplayInput{Input: Input{Scan: scanAfter(0, logRows()...), ReadsAll: true, From: at(0)}, Direction: Newer})
	if len(old.Events) != 4 || old.Events[0].At != stamp(at(10)) {
		t.Errorf("from the log's start = %+v", briefs(old))
	}
}

// TestTheReplayKeepsTheReadersView: a member of the order's project sees the release
// arrive outside their view, counted from their own send row, and the publish arrive
// from outside it.
func TestTheReplayKeepsTheReadersView(t *testing.T) {
	visible := func(key uint64) bool { return key == 1 || key == 2 }
	p := replay(t, ReplayInput{Input: Input{Scan: scanBefore(0, logRows()...), Visible: visible, From: at(0)}})
	want := []brief{
		{EventReceived, "outside-view", "process:order", "Shipped"},
		{EventReceived, "outside-view", "process:order", "Shipped"},
		{EventUnreceived, "process:order", "", "OrderReleased"},
		{EventOutside, "process:order", "outside-view", "OrderReleased"},
	}
	if got := briefs(p); !reflect.DeepEqual(got, want) {
		t.Fatalf("member's events = %+v, want %+v", got, want)
	}
	if last := p.Events[3]; last.Count != 1 || last.Started != 1 {
		t.Errorf("outside event = %+v, want one arrival, starting an instance", last)
	}
}

func TestReplayErrors(t *testing.T) {
	if _, err := Replay(ReplayInput{Input: Input{ProcessID: "nowhere", Definitions: fixture()}}); !errors.Is(err, ErrUnknownProcess) {
		t.Errorf("unknown process = %v", err)
	}
	boom := errors.New("disk")
	_, err := Replay(ReplayInput{Input: Input{ProcessID: "order", Definitions: fixture(), ScanLimit: 10,
		Scan: func(func(state.EventLogEntry) error) error { return boom }}, Limit: 10})
	if !errors.Is(err, boom) {
		t.Errorf("scan error = %v", err)
	}
	empty := replay(t, ReplayInput{})
	if len(empty.Events) != 0 || empty.More || empty.Next != "0" || empty.Latest != "0" {
		t.Errorf("no log = %+v", empty)
	}
}
