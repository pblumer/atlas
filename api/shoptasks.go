package api

import (
	"net/http"
	"sort"
	"strings"

	"github.com/pblumer/atlas/api/httpapi"
	"github.com/pblumer/atlas/api/order"
	"github.com/pblumer/atlas/api/taskfolder"
	"github.com/pblumer/atlas/compiler"
	"github.com/pblumer/atlas/model"
	"github.com/pblumer/atlas/state"
)

// The open tasks of an order, in the shop (ADR-draft-the-shop-shows-an-orders-open-tasks).
//
// "Wartet" says a position is not done. It does not say on whom: a line manager,
// a group in IT, a process nobody has modelled yet. That is what the shop's orders
// table shows under each position now — every open task of the processes working
// it, whom it is waiting for, and, for whoever holds it, the task's own form to
// answer it there.
//
// # Who sees what
//
// The orderer and the recipient see every open task of their own orders. Those are
// found through the instances the order records on each position (order.go,
// positioninstance.go) — a read of those instances and nothing else, however large
// the server.
//
// Whoever *holds* an open task of an order sees that order too: the line manager
// who has to approve it is the case this is for. The server cannot ask who
// somebody's line manager is — that is a directory question, answered by a model
// through a worker (escalation.go) — but it knows who holds a task. Finding those
// orders is a walk of the open tasks, bounded the way the approval list's is,
// and it says when the bound bit.
//
// What is shown of a task is where it is and whom it waits for — not its
// variables. Reading and answering the form is the task route's business, under
// the task route's own gates (taskauthority.go, instancescope.go): holding the
// task is what opens it, here as in the inbox.

// shopTaskHolder is whom a task waits for, in words a person reads.
type shopTaskHolder struct {
	// Kind says how the task is addressed. For an approval it is the rule the line
	// is approved under — "fixed", "role", "superior" — because that is what an
	// orderer knows the approval as. For any other task it is "person", "group",
	// or "open" for a task the model addressed to nobody.
	Kind string `json:"kind"`
	// Name is the person's display name or the groups' names, comma-separated.
	// Empty for an open task.
	Name string `json:"name,omitempty"`
}

// shopTask is one open task of an order position.
type shopTask struct {
	Key                uint64         `json:"key"`
	OrderID            string         `json:"orderId"`
	PositionID         string         `json:"positionId"`
	ProcessInstanceKey uint64         `json:"processInstanceKey"`
	ElementInstanceKey uint64         `json:"elementInstanceKey,omitempty"`
	ProcessID          string         `json:"processId,omitempty"`
	Name               string         `json:"name"`
	FormID             string         `json:"formId,omitempty"`
	DueDate            int64          `json:"dueDate,omitempty"`
	Approval           bool           `json:"approval"`
	Holder             shopTaskHolder `json:"holder"`
	// MayWork is whether the shop offers the caller the task's form. Never wider
	// than what the task routes allow, so a form offered here is never refused; and
	// narrower in one place, see [Server.mayWorkShopTask].
	MayWork bool `json:"mayWork"`
}

// shopTasksResp is the whole answer.
type shopTasksResp struct {
	Tasks []shopTask `json:"tasks"`
	// Orders are the orders the caller neither placed nor receives and holds an
	// open task in. The shop lists them beside the caller's own.
	Orders []order.Order `json:"orders"`
	// Truncated says the walk that finds those held orders stopped at its bound, so
	// an order the caller holds a task in may be missing. The caller's own orders
	// are never affected: they are not found by that walk.
	Truncated bool `json:"truncated"`
}

// handleShopTasks answers the shop's orders table: every open task of the caller's
// orders, and of the orders the caller holds a task in.
func (s *Server) handleShopTasks(w http.ResponseWriter, r *http.Request) {
	p := httpapi.PrincipalFrom(r.Context())
	out := shopTasksResp{Tasks: []shopTask{}, Orders: []order.Order{}}
	if p == nil {
		httpapi.JSON(w, http.StatusOK, out)
		return
	}

	var (
		own     []order.Order
		loadErr error
	)
	s.do(func() { own, loadErr = s.orderStore.For(p.UserID) })
	if loadErr != nil {
		httpapi.Error(w, http.StatusInternalServerError, "read orders: "+loadErr.Error())
		return
	}
	mine := make(map[string]bool, len(own))
	for _, o := range own {
		mine[o.ID] = true
	}

	// The orders held through a task, found first so their tasks are read in the
	// same pass as the caller's own. Only where there is identity to hold a task
	// with: in single-user mode every task is everybody's.
	held := map[string]order.Order{}
	if s.authEnabled {
		var err error
		if out.Truncated, err = s.ordersHeldBy(p, mine, held); err != nil {
			httpapi.Error(w, http.StatusInternalServerError, "read held tasks: "+err.Error())
			return
		}
	}

	orders := append([]order.Order(nil), own...)
	heldIDs := make([]string, 0, len(held))
	for id := range held {
		heldIDs = append(heldIDs, id)
	}
	sort.Strings(heldIDs)
	for _, id := range heldIDs {
		orders = append(orders, held[id])
	}

	var tasks []shopTask
	err := s.readOffLoop(func(rv *state.ReadView, defs defIndex) error {
		def := defsMeta(defs)
		for _, o := range orders {
			for _, l := range o.Lines {
				for _, inst := range l.Instances {
					if err := rv.ElementInstancesOfProcess(inst.Key, func(elKey uint64) error {
						jobKey, ok, err := rv.JobOfElement(elKey)
						if err != nil || !ok {
							return err
						}
						jv, ok, err := rv.GetJob(jobKey)
						if err != nil || !ok {
							return err
						}
						// Open user tasks only, as the inbox lists them: a job with no
						// retries left is parked behind an incident, not waiting on a
						// person.
						if jv.JobType != compiler.UserTaskJobTypeIndex || jv.Retries <= 0 {
							return nil
						}
						tr := enrichTaskWith(rv, def, jobKey, jv)
						tasks = append(tasks, s.shopTaskOf(p, o, l, tr))
						return nil
					}); err != nil {
						return err
					}
				}
			}
		}
		return nil
	})
	if err != nil {
		httpapi.Error(w, http.StatusInternalServerError, "read tasks: "+err.Error())
		return
	}
	if tasks != nil {
		out.Tasks = tasks
	}
	for _, id := range heldIDs {
		out.Orders = append(out.Orders, forHolder(held[id]))
	}
	httpapi.JSON(w, http.StatusOK, out)
}

// ordersHeldBy fills held with the orders, other than the caller's own, in which
// the caller holds an open task, and reports whether the walk hit its bound.
//
// A held task names its order through the instance's own variables, which is how
// the approval list reads it; it is read only for the tasks the caller holds, so
// the cost beyond the walk is bounded by one person's work.
func (s *Server) ordersHeldBy(p *httpapi.Principal, mine map[string]bool, held map[string]order.Order) (bool, error) {
	var instances []uint64
	budgetHit, err := s.visitOpenTasks(0, false, func(_ uint64, tr taskResp, _ taskfolder.Task) bool {
		// Addressed tasks only. One the model addressed to nobody is anybody's to
		// pick up, and holding it in that sense would put every order with such a
		// task in front of every signed-in person.
		if tr.Assignee == "" && tr.CandidateGroups == "" {
			return true
		}
		if s.holdsTask(p, tr.Assignee, tr.CandidateGroups) {
			instances = append(instances, tr.ProcessInstanceKey)
		}
		return true
	})
	if err != nil || len(instances) == 0 {
		return budgetHit, err
	}

	ids := map[string]bool{}
	err = s.readOffLoop(func(rv *state.ReadView, _ defIndex) error {
		for _, k := range instances {
			var orderID string
			if err := rv.VisibleVariablesOfScope(k, func(v *model.VariableValue) error {
				if v.Name == approvalOrderVar {
					if s, ok := nativeString(v); ok {
						orderID = s
					}
				}
				return nil
			}); err != nil {
				return err
			}
			if orderID != "" && !mine[orderID] {
				ids[orderID] = true
			}
		}
		return nil
	})
	if err != nil {
		return budgetHit, err
	}
	for id := range ids {
		o, ok, err := s.orderStore.Get(id)
		if err != nil {
			return budgetHit, err
		}
		if ok {
			held[id] = o
		}
	}
	return budgetHit, nil
}

// shopTaskOf builds one row.
func (s *Server) shopTaskOf(p *httpapi.Principal, o order.Order, l order.Line, tr taskResp) shopTask {
	t := shopTask{
		Key: tr.Key, OrderID: o.ID, PositionID: l.Key(),
		ProcessInstanceKey: tr.ProcessInstanceKey, ElementInstanceKey: tr.ElementInstanceKey,
		ProcessID: tr.ProcessID, Name: firstNonEmpty(tr.Name, tr.ElementID),
		FormID: tr.FormID, DueDate: tr.DueDate,
		Approval: tr.ProcessID != "" && l.ApprovalProcess() == tr.ProcessID,
	}
	t.Holder = s.holderOf(tr, l, t.Approval)
	t.MayWork = s.mayWorkShopTask(p, tr)
	return t
}

// holderOf names whom a task waits for.
func (s *Server) holderOf(tr taskResp, l order.Line, approval bool) shopTaskHolder {
	var h shopTaskHolder
	switch {
	case tr.Assignee != "":
		h = shopTaskHolder{Kind: "person", Name: s.personName(tr.Assignee)}
	case strings.TrimSpace(tr.CandidateGroups) != "":
		h = shopTaskHolder{Kind: "group", Name: s.groupNames(tr.CandidateGroups)}
	default:
		return shopTaskHolder{Kind: "open"}
	}
	// An approval is known to the orderer by its rule, not by how the model
	// addressed the task: "your line manager", "the group that approves this".
	if approval {
		switch l.Approval.Kind {
		case "fixed", "role", "superior":
			h.Kind = l.Approval.Kind
		}
	}
	return h
}

// mayWorkShopTask decides whether the shop offers a task's form to the caller.
//
// It is [Server.mayWorkTask]'s decision with one exception. A task the model
// addressed to nobody is, by BPMN convention, anybody's to pick up, and the task
// routes keep it that way. The shop does not offer it: the reader of an order's
// row is, first of all, the person who ordered, and a manual provisioning step of
// their own order drawn with a button beside it is an invitation to report their
// own laptop as handed over. Such a task stays answerable where it always was — in
// Tasks, and here for an operator.
func (s *Server) mayWorkShopTask(p *httpapi.Principal, tr taskResp) bool {
	if !s.authEnabled {
		return true
	}
	if p == nil {
		return false
	}
	if p.HasRole(RoleOperator) || p.HasRole(RoleAdmin) {
		return true
	}
	if tr.Assignee == "" && tr.CandidateGroups == "" {
		return false
	}
	return s.holdsTask(p, tr.Assignee, tr.CandidateGroups)
}

// personName is a username as a person reads it: the display name where the
// account has one, the username where it has not.
func (s *Server) personName(username string) string {
	if s.users != nil {
		if u, ok, err := s.users.byUsername(username); err == nil && ok && u.DisplayName != "" {
			return u.DisplayName
		}
	}
	return username
}

// groupNames is a candidate-group list as a person reads it. A model may name a
// group by its id or by its name; an id is resolved to the name, a name is kept.
func (s *Server) groupNames(candidateGroups string) string {
	var names []string
	for _, g := range strings.Split(candidateGroups, ",") {
		g = strings.TrimSpace(g)
		if g == "" {
			continue
		}
		if s.groups != nil {
			if rec, ok, err := s.groups.Get(g); err == nil && ok && rec.Name != "" {
				g = rec.Name
			}
		}
		names = append(names, g)
	}
	return strings.Join(names, ", ")
}

// forHolder is an order as somebody who holds one of its tasks may read it here:
// what was ordered and where each position stands, without the answers the orderer
// gave on the products' forms. Those belong to the task's own form where the model
// asks for them, and to nobody else.
func forHolder(o order.Order) order.Order {
	out := o
	out.Lines = make([]order.Line, len(o.Lines))
	for i, l := range o.Lines {
		l.Config, l.Amendments, l.Instances = nil, nil, nil
		out.Lines[i] = l
	}
	return out
}

// nativeString reads a string variable, and nothing else.
func nativeString(v *model.VariableValue) (string, bool) {
	if v.Kind != model.VarString {
		return "", false
	}
	return v.Text, true
}
