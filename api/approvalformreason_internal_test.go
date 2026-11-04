package api

import (
	"encoding/json"
	"testing"
)

// The shipped approval form asks for a reason whenever it is not approving. A task
// completed through the form with "Genehmigen" left unticked is a rejection, and the
// shipped approval models then report it to the order, which refuses a rejection
// without a reason ("the orderer is told it") — the instance stopped on an incident.
// Found against a real tenant, where an administrator completed a manager's approval
// in Operations. The reason is required while it is shown, and shown while the box is
// not ticked, so form-js will not submit a rejection without one; approving hides it.
func TestTheShippedApprovalFormNeedsAReasonToReject(t *testing.T) {
	raw, err := systemProcessesFS.ReadFile("systemprocesses/form-genehmigung.json")
	if err != nil {
		t.Fatal(err)
	}
	var form struct {
		Components []struct {
			Key         string `json:"key"`
			Conditional struct {
				Hide string `json:"hide"`
			} `json:"conditional"`
			Validate struct {
				Required bool `json:"required"`
			} `json:"validate"`
		} `json:"components"`
	}
	if err := json.Unmarshal(raw, &form); err != nil {
		t.Fatal(err)
	}
	for _, c := range form.Components {
		if c.Key != "begruendung" {
			continue
		}
		if !c.Validate.Required || c.Conditional.Hide != "=genehmigt = true" {
			t.Fatalf("begruendung: required %v, hidden when %q; want required and hidden only when approving",
				c.Validate.Required, c.Conditional.Hide)
		}
		return
	}
	t.Fatal("the approval form no longer asks for begruendung")
}
