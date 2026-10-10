package catalog

import (
	"slices"
	"sort"
	"strings"
)

// What a product's configuration form asks reaches the processes the product binds
// (ADR-0441): each answer as a process variable under its field's key. A few names are
// the order's own — which order and position this is, who it is for, which approval
// applies — and a field may not take one of them. Its answer would be left out rather
// than stand in for what the order says, and a product manager who named a field
// "recipient" would look for it in vain; so publishing says so instead.

// OrderVariables are the variables the order gives the processes it starts for a
// position. A configuration form's field may not be named after one.
var OrderVariables = map[string]bool{
	"orderId": true, "itemId": true, "positionId": true, "variantId": true,
	"recipient": true, "orderer": true, "provisionProcess": true, "approvalRef": true,
	"atlasApiBase": true, "portalBaseUrl": true, "commandId": true, "reason": true,
}

// FormField is one field a form asks: the variable it writes — the root of its key —
// and whether the form says its answer is not personal data.
type FormField struct {
	Key string
	// NotPersonal is the form author's word that this answer names nobody: the
	// custom property personal=false on the field. It quiets
	// [AnswerWarnings] for this field; nothing else reads it.
	NotPersonal bool
}

// FormFieldLookup answers which fields a form has. found is false for a form the
// server does not hold.
type FormFieldLookup interface {
	FormFields(formID string) (fields []FormField, found bool)
}

// PersonalLookup answers which variables the newest deployed version of a process
// declares personal data (ADR-0314), and whether any version is deployed.
type PersonalLookup interface {
	PersonalVariables(processID string) (names []string, deployed bool)
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
		fields, found := forms.FormFields(id)
		if !found {
			continue
		}
		var clash []string
		for _, f := range fields {
			if OrderVariables[f.Key] && !slices.Contains(clash, f.Key) {
				clash = append(clash, f.Key)
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

// AnswerWarnings names, per product, the answers that would reach one of its processes
// in the clear: an answer of its configuration form that a process the product binds
// receives (ADR-0441) and does not declare personal data (ADR-0314). Such an answer
// is written to that process's history as it is, and outlives the person it names in
// every copy of the log.
//
// It warns and does not refuse. Whether an answer is personal data is the form
// author's to say, and most are not — a cost centre, a size. A field that names
// nobody is marked so in the form, with the custom property personal=false, and is
// not warned about again; a warning that could never be answered would teach people
// to read past it. A process that is not deployed is not asked: publishing refuses
// what a lifecycle binding needs, and a process deployed later is asked at the next
// publish.
func AnswerWarnings(items []Item, forms FormFieldLookup, processes PersonalLookup) []Problem {
	if forms == nil || processes == nil {
		return nil
	}
	var out []Problem
	for _, it := range items {
		id := strings.TrimSpace(it.ConfigForm)
		if id == "" {
			continue
		}
		fields, found := forms.FormFields(id)
		if !found {
			continue
		}
		var asked []string
		for _, f := range fields {
			if !f.NotPersonal && !OrderVariables[f.Key] && !slices.Contains(asked, f.Key) {
				asked = append(asked, f.Key)
			}
		}
		if len(asked) == 0 {
			continue
		}
		sort.Strings(asked)
		for _, proc := range answerProcesses(it) {
			declared, deployed := processes.PersonalVariables(proc)
			if !deployed {
				continue
			}
			var clear []string
			for _, k := range asked {
				if !slices.Contains(declared, k) {
					clear = append(clear, k)
				}
			}
			if len(clear) == 0 {
				continue
			}
			out = append(out, Problem{Item: it.ID, Message: "the answers " + strings.Join(clear, ", ") +
				" of form " + id + " reach " + proc + " in the clear: declare the personal ones in " +
				"that process with atlas:personal and atlas:dataSubject=\"recipient\", and mark a field " +
				"that names nobody with the custom property personal=false"})
		}
	}
	return out
}

// answerProcesses are the processes a product binds that the order gives its
// answers: its provisioning and deprovisioning, its lifecycle process, and an
// approval model of the installation's own. Atlas's own approval models are not
// given them.
func answerProcesses(it Item) []string {
	var out []string
	add := func(p string) {
		if p = strings.TrimSpace(p); p != "" && !slices.Contains(out, p) {
			out = append(out, p)
		}
	}
	add(it.ProvisionProcess)
	add(it.DeprovisionProcess)
	add(it.LifecycleProcess)
	switch it.Approval.Kind {
	case "", KindNone, KindFixed, KindRole, KindSuperior:
	default:
		add(string(it.Approval.Kind))
	}
	return out
}
