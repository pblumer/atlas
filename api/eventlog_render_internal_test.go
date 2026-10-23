package api

import (
	"net/http"
	"strings"
	"testing"

	"github.com/pblumer/atlas/compiler"
	"github.com/pblumer/atlas/model"
	"github.com/pblumer/atlas/state"
)

// TestEveryEventLogRowRendersAsItsKind: each kind of row the table holds is answered
// under its own kind, with the parties and the facts that kind has, and is read under
// the definition it is about (ADR-0472 §3, §5).
func TestEveryEventLogRowRendersAsItsKind(t *testing.T) {
	defs := map[uint64]eventLogDef{2: {processID: "billing", name: "Billing", projectID: "p-1", version: 3}}
	directed := &model.DispatchValue{Channel: model.DispatchMessage, Source: model.DispatchDirected, Name: "tool.reset",
		CorrelationKey: "pos-1", TargetProcessInstanceKey: 40, TargetProcessDefKey: 2, Reached: 1}
	inbound := &model.DispatchValue{Channel: model.DispatchMessage, Source: model.DispatchFromInbound, Name: "jira.created"}
	started := &model.SignalSubscriptionValue{SignalName: "go", ProcessDefKey: 2, ElementId: 0, SenderProcessInstanceKey: 9}
	startedByMessage := &model.MessageFlowValue{SenderProcessInstanceKey: 9, ReceiverProcessDefKey: 2, MessageName: "m", CorrelationKey: "k"}
	received := &model.MessageFlowValue{ReceiverProcessInstanceKey: 41, ReceiverProcessDefKey: 2, MessageName: "m"}
	kept := &model.MessageValue{MessageName: "m", CorrelationKey: "k", MessageId: "id-1", ExpiresAt: 1_700_000_000_000_000_000}

	cases := []struct {
		entry   state.EventLogEntry
		kind    string
		channel string
		def     uint64
		check   func(eventLogRow) bool
	}{
		{state.EventLogEntry{Kind: state.EventLogDispatched, Dispatch: directed}, "message.sent", "message", 2,
			func(r eventLogRow) bool {
				return r.Source == "directed" && r.Target != nil && r.Target.InstanceKey == 40 && r.Target.ProcessID == "billing" &&
					r.Sender == nil && *r.Reached == 1
			}},
		{state.EventLogEntry{Kind: state.EventLogDispatched, Dispatch: inbound}, "message.sent", "message", 0,
			func(r eventLogRow) bool { return r.Source == "inbound" && r.Sender == nil && *r.Reached == 0 }},
		{state.EventLogEntry{Kind: state.EventLogSignalStarted, Signal: started}, "signal.started", "signal", 2,
			func(r eventLogRow) bool {
				return r.Receiver != nil && r.Receiver.InstanceKey == 0 && r.Receiver.ProjectID == "p-1" && r.Sender.InstanceKey == 9
			}},
		{state.EventLogEntry{Kind: state.EventLogMessageDelivered, Flow: startedByMessage}, "message.started", "message", 2,
			func(r eventLogRow) bool {
				return r.Receiver.InstanceKey == 0 && r.Sender.InstanceKey == 9 && r.CorrelationKey == "k"
			}},
		{state.EventLogEntry{Kind: state.EventLogMessageDelivered, Flow: received}, "message.received", "message", 2,
			func(r eventLogRow) bool { return r.Receiver.InstanceKey == 41 && r.Sender == nil }},
		{state.EventLogEntry{Kind: state.EventLogMessageKept, Message: kept}, "message.kept", "message", 0,
			func(r eventLogRow) bool { return r.MessageID == "id-1" && strings.HasPrefix(r.ExpiresAt, "2023-") }},
		{state.EventLogEntry{Kind: state.EventLogMessageTaken, Message: kept}, "message.taken", "message", 0, nil},
		{state.EventLogEntry{Kind: state.EventLogMessageExpired, Message: &model.MessageValue{MessageName: "m"}}, "message.expired", "message", 0,
			func(r eventLogRow) bool { return r.ExpiresAt == "" }},
	}
	for _, c := range cases {
		r := renderEventLogRow(c.entry, defs)
		if r.Kind != c.kind || r.Channel != c.channel {
			t.Errorf("%+v rendered as %s on %s, want %s on %s", c.entry, r.Kind, r.Channel, c.kind, c.channel)
		}
		if c.check != nil && !c.check(r) {
			t.Errorf("%s rendered as %+v", c.kind, r)
		}
		if got := rowDefinition(c.entry); got != c.def {
			t.Errorf("%s is about definition %d, want %d", c.kind, got, c.def)
		}
		// A project member reads a row only under a definition they may view; a row
		// that names none is never theirs.
		if rowVisible(c.entry, map[uint64]bool{2: true}) != (c.def == 2) {
			t.Errorf("%s visible to a member of the definition's project = %v", c.kind, !(c.def == 2))
		}
	}
	if ch, name := rowChannelAndName(state.EventLogEntry{}); ch != "" || name != "" {
		t.Errorf("an empty row is %q %q", ch, name)
	}
	if p := party(defs, 5, 77, 3); p.ProcessID != "" || p.InstanceKey != 5 || p.DefinitionKey != 77 {
		t.Errorf("a party on a definition no longer deployed = %+v", p)
	}
}

// TestTheEventLogFilterNarrowsByChannelNameAndInstance: an instance is matched where it
// sent, received or was addressed, and a buffer row, which names none, never matches one.
func TestTheEventLogFilterNarrowsByChannelNameAndInstance(t *testing.T) {
	sent := state.EventLogEntry{Dispatch: &model.DispatchValue{Channel: model.DispatchSignal, Name: "go", SenderProcessInstanceKey: 3}}
	directed := state.EventLogEntry{Dispatch: &model.DispatchValue{Channel: model.DispatchMessage, Name: "m", TargetProcessInstanceKey: 4}}
	recv := state.EventLogEntry{Signal: &model.SignalSubscriptionValue{SignalName: "go", ProcessInstanceKey: 5, SenderProcessInstanceKey: 3}}
	flow := state.EventLogEntry{Flow: &model.MessageFlowValue{MessageName: "m", ReceiverProcessInstanceKey: 6, SenderProcessInstanceKey: 3}}
	kept := state.EventLogEntry{Message: &model.MessageValue{MessageName: "m"}}
	for _, c := range []struct {
		f     eventLogFilter
		entry state.EventLogEntry
		want  bool
	}{
		{eventLogFilter{channel: "message"}, sent, false},
		{eventLogFilter{channel: "signal"}, sent, true},
		{eventLogFilter{name: "other"}, recv, false},
		{eventLogFilter{instance: 3}, sent, true},
		{eventLogFilter{instance: 4}, directed, true},
		{eventLogFilter{instance: 3}, recv, true},
		{eventLogFilter{instance: 5}, recv, true},
		{eventLogFilter{instance: 6}, flow, true},
		{eventLogFilter{instance: 3}, flow, true},
		{eventLogFilter{instance: 9}, flow, false},
		{eventLogFilter{instance: 3}, kept, false},
		{eventLogFilter{channel: "message", name: "m"}, kept, true},
	} {
		if got := c.f.matches(c.entry); got != c.want {
			t.Errorf("%+v matches %+v = %v, want %v", c.f, c.entry, got, c.want)
		}
	}
}

// TestAnEventLogCursorMustBeAPosition: a cursor that is not a decimal position is a
// 400, for either direction.
func TestAnEventLogCursorMustBeAPosition(t *testing.T) {
	srv := eventServer(t)
	for _, q := range []string{"?after=x", "?before=-1", "?limit=0", "?channel=fax"} {
		if code, body := serveInternal(t, srv, http.MethodGet, "/api/v1/event-log"+q, "", ""); code != http.StatusBadRequest {
			t.Errorf("GET /api/v1/event-log%s = %d %s, want 400", q, code, body)
		}
	}
	if code, body := serveInternal(t, srv, http.MethodGet, "/api/v1/event-log?after=5", "", ""); code != http.StatusOK ||
		!strings.Contains(string(body), `"latest":"5"`) {
		t.Errorf("an empty log after a cursor = %d %s, want the cursor back", code, body)
	}
}

// TestTheWordingOfTheInventoryFindings: the sentences a finding is made of name their
// parties in the reader's words — nobody, one, two, many; a project, none, atlas's own.
func TestTheWordingOfTheInventoryFindings(t *testing.T) {
	for parts, want := range map[string]string{"": "", "a": "a", "a|b": "a and b", "a|b|c": "a, b and c"} {
		var in []string
		if parts != "" {
			in = strings.Split(parts, "|")
		}
		if got := joinAnd(in); got != want {
			t.Errorf("joinAnd(%q) = %q, want %q", parts, got, want)
		}
	}
	if got := projectsText([]string{"", systemProjectID, "p-1"}); got != "a definition without a project, atlas's system project and project p-1" {
		t.Errorf("projectsText = %q", got)
	}
	if signalListed(compiler.SignalPoint{SignalName: "  "}, false) {
		t.Error("a signal with no name was listed")
	}
	a := deployedPointView{ProcessID: "p", Version: 1, ElementID: "a"}
	b := deployedPointView{ProcessID: "p", Version: 1, ElementID: "b"}
	if !pointBefore(a, b) || pointBefore(b, a) {
		t.Error("points of one version are not ordered by element")
	}

	// A watch and a product action that send a name nothing waits for are named as its
	// senders, and a row without a name is no name of its own.
	groups := groupMessageSources([]messageSourceView{
		{MessageName: "  ", SourceKind: sourceInboundWatch, Enabled: true},
		{MessageName: "m", SourceKind: sourceInboundWatch, ConnectorName: "Jira", Enabled: true},
		{MessageName: "m", SourceKind: sourceProductAction, Action: "reset", ProductID: "tool", Enabled: true},
		{MessageName: "m", SourceKind: sourceProductAction, Action: "off", ProductID: "tool"},
	})
	if len(groups) != 1 || groups[0].Name != "m" {
		t.Fatalf("groups = %+v, want the one named group", groups)
	}
	msg := rulesOf(groups[0].Findings)[findingMessageUnreceived]
	if !strings.Contains(msg, "the inbound watch of worker Jira and action reset of product tool") || strings.Contains(msg, "off") {
		t.Errorf("unreceived = %q, want the enabled watch and the active action named", msg)
	}
}
