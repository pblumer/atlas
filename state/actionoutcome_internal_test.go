package state

import (
	"testing"

	"github.com/pblumer/atlas/model"
)

func outcomeOf(order, position, command, outcome string) *model.ActionOutcomeValue {
	return &model.ActionOutcomeValue{OrderID: order, Position: position, CommandID: command,
		Source: "test", Action: "reset", Effect: "service", Outcome: outcome, At: 1000}
}

// TestAnActionOutcomeIsReadBackByItsCommandOnly: an outcome answers for its own
// order, position and command; an order or a position whose id prefixes another's
// reads nothing of the other's; one written earlier in the same transaction counts;
// and a position's outcomes are listed in command order.
func TestAnActionOutcomeIsReadBackByItsCommandOnly(t *testing.T) {
	s := openStore(t)
	tx := s.NewTransaction()
	must(t, tx.PutActionOutcome(outcomeOf("ord_1", "mailbox", "b", "completed")))
	if v, ok, err := tx.ActionOutcome("ord_1", "mailbox", "b"); err != nil || !ok || v.Outcome != "completed" {
		t.Fatalf("same transaction: %+v ok=%v err=%v", v, ok, err)
	}
	must(t, tx.PutActionOutcome(outcomeOf("ord_1", "mailbox", "a", "failed")))
	must(t, tx.PutActionOutcome(outcomeOf("ord_1", "mailbox2", "c", "completed")))
	must(t, tx.PutActionOutcome(outcomeOf("ord_10", "mailbox", "d", "completed")))
	commit(t, tx)

	for _, c := range []struct {
		order, position, command string
		want                     bool
	}{
		{"ord_1", "mailbox", "a", true},
		{"ord_1", "mailbox", "z", false},
		{"ord_1", "mail", "boxa", false},
		{"ord_", "1", "mailbox", false},
	} {
		if _, ok, err := s.ActionOutcome(c.order, c.position, c.command); err != nil || ok != c.want {
			t.Errorf("ActionOutcome(%q, %q, %q) = %v (%v), want %v", c.order, c.position, c.command, ok, err, c.want)
		}
	}
	var got []string
	must(t, s.ActionOutcomesOf("ord_1", "mailbox", func(v *model.ActionOutcomeValue) error {
		got = append(got, v.CommandID+":"+v.Outcome)
		return nil
	}))
	if len(got) != 2 || got[0] != "a:failed" || got[1] != "b:completed" {
		t.Fatalf("outcomes of ord_1/mailbox = %v, want a:failed, b:completed", got)
	}
}

// TestPurgingAnInstanceLeavesTheOutcomeItReportedStanding: an outcome outlives the
// instance that carried the action out, as the right it changed does.
func TestPurgingAnInstanceLeavesTheOutcomeItReportedStanding(t *testing.T) {
	s := openStore(t)
	const defKey = uint64(9)
	piKey := model.NewKey(1, 1)
	tx := s.NewTransaction()
	must(t, tx.PutProcessInstanceHistory(piKey, &model.ProcessInstanceValue{
		ProcessDefKey: defKey, State: model.PICompleted, CompletedPosition: 5}))
	o := outcomeOf("ord_1", "mailbox", "cmd-1", "completed")
	o.InstanceKey = piKey
	must(t, tx.PutActionOutcome(o))
	commit(t, tx)

	tx = s.NewTransaction()
	must(t, tx.PurgeInstanceHistory(piKey, defKey, 0))
	commit(t, tx)
	if _, ok, err := s.ActionOutcome("ord_1", "mailbox", "cmd-1"); err != nil || !ok {
		t.Fatalf("after the purge: ok=%v err=%v, want the outcome standing", ok, err)
	}
}
