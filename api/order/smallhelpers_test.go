package order

import "testing"

// Held sees every person an approval has passed through: the approver, the original
// one, and both ends of each escalation.
func TestAssignmentHeld(t *testing.T) {
	a := Assignment{Approver: "carol", Original: "alice", Escalations: []Escalation{{From: "alice", To: "bob"}}}
	for _, who := range []string{"alice", "bob", "carol"} {
		if !a.Held(who) {
			t.Errorf("Held(%s) = false", who)
		}
	}
	if a.Held("dave") {
		t.Error("Held(dave) = true for somebody the approval never reached")
	}
}

// Command finds the newest instance that took a command id, and nothing for an empty id.
func TestLineCommand(t *testing.T) {
	l := Line{Instances: []LineInstance{{Key: 1, CommandID: "c1"}, {Key: 2, CommandID: "c2"}, {Key: 3, CommandID: "c1"}}}
	if inst, ok := l.Command("c1"); !ok || inst.Key != 3 {
		t.Errorf("Command(c1) = %+v, %v; want the newest, key 3", inst, ok)
	}
	if _, ok := l.Command(""); ok {
		t.Error("Command(\"\") found an instance")
	}
	if _, ok := l.Command("nope"); ok {
		t.Error("Command(nope) found an instance")
	}
}

// Deleting an order removes it, and deleting one already gone is not an error.
func TestStoreDelete(t *testing.T) {
	s, err := NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Delete("no-such-order"); err != nil {
		t.Errorf("Delete of an absent order = %v", err)
	}
}
