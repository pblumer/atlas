package api

import (
	"net/http"

	"github.com/pblumer/atlas/api/httpapi"
	"github.com/pblumer/atlas/model"
)

// taskCompleterVar is the variable a user task's completion writes naming who
// completed it, as a principal id — the same identifier an order records for who
// decided a line (order.Line.DecidedBy).
//
// A process needs it the moment a task is offered to a group rather than to a
// person. An approval assigned to one named approver could report that name as
// the decider, because the task could only have been theirs. One offered to a
// group of integration managers could not: the model knows the group, and the
// record of a refusal said "the group decided", which is not an answer an audit
// accepts. The completion is the one place that knows the person, so it says.
const taskCompleterVar = "completedBy"

// withCompleter returns the completion's variables with taskCompleterVar set to
// the signed-in caller. A value the body carried under that name is dropped first:
// who decided is the server's to say, and a form that could write it would let
// anybody record a decision in somebody else's name. Without a principal — auth
// off — there is nobody to name, and the variables are left as they came.
func withCompleter(r *http.Request, vars []model.VariableValue) []model.VariableValue {
	p := httpapi.PrincipalFrom(r.Context())
	if p == nil || p.UserID == "" {
		return vars
	}
	out := make([]model.VariableValue, 0, len(vars)+1)
	for _, v := range vars {
		if v.Name != taskCompleterVar {
			out = append(out, v)
		}
	}
	return append(out, model.VariableValue{Name: taskCompleterVar, Kind: model.VarString, Text: p.UserID})
}
