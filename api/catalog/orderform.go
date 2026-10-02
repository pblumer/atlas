package catalog

import (
	"sort"
	"strings"
)

// What a product's configuration form asks reaches the processes the product binds
// (ADR-draft-a-position-s-answers-reach-its-processes): each answer as a process
// variable under its field's key. A few names are the order's own — which order and
// position this is, who it is for, which approval applies — and a field may not take
// one of them. Its answer would be left out rather than stand in for what the order
// says, and a product manager who named a field "recipient" would look for it in
// vain; so publishing says so instead.

// OrderVariables are the variables the order gives the processes it starts for a
// position. A configuration form's field may not be named after one.
var OrderVariables = map[string]bool{
	"orderId": true, "itemId": true, "positionId": true, "variantId": true,
	"recipient": true, "orderer": true, "provisionProcess": true, "approvalRef": true,
	"atlasApiBase": true, "portalBaseUrl": true, "commandId": true, "reason": true,
}

// FormFieldLookup answers which variables a form's fields write: the root of each
// field's key. found is false for a form the server does not hold.
type FormFieldLookup interface {
	FormFields(formID string) (keys []string, found bool)
}

// OrderFormProblems refuses a product whose configuration form names a field after
// one of [OrderVariables]. A form the lookup does not hold is not a problem here:
// publishing has never asked whether a configuration form exists, and this check
// does not start to.
func OrderFormProblems(items []Item, forms FormFieldLookup) []Problem {
	if forms == nil {
		return nil
	}
	var out []Problem
	for _, it := range items {
		id := strings.TrimSpace(it.ConfigForm)
		if id == "" {
			continue
		}
		keys, found := forms.FormFields(id)
		if !found {
			continue
		}
		var clash []string
		for _, k := range keys {
			if OrderVariables[k] {
				clash = append(clash, k)
			}
		}
		if len(clash) == 0 {
			continue
		}
		sort.Strings(clash)
		out = append(out, Problem{Item: it.ID, Message: "configuration form " + id + " has a field " +
			strings.Join(clash, ", ") + ", a name the order gives its processes itself; " +
			"the answer would not reach them — rename the field"})
	}
	return out
}
