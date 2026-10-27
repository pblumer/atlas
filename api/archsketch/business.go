package archsketch

import (
	"cmp"
	"slices"
	"strings"
)

// The business layer of a release's architecture (ADR-0495 slice D): its processes as
// business processes, the human roles their tasks and lanes name, and the catalogue
// products whose provisioning, change or deprovisioning a release process carries out.
//
// The processes and roles are read off the release's compiled processes, the products
// off the newest published release of each catalogue, at the moment the snapshot is
// taken. A role is recorded the way the model writes it — a candidate group as its
// literal entry, a lane by its name — and a task assigned to one person by name is
// counted, never named: that name is an Atlas account, which a release record cannot
// take back (ADR-0495 §5). A product is shown only to a reader who may maintain its
// home catalogue, the check the starmap applies (ADR-0396); everyone else is told how
// many were withheld.

// The bounds on the business layer's lists (ADR-0495 §6), guards well above what a real
// installation holds; what they leave out is counted in [Business.Omitted].
const (
	maxBusinessRoles   = 100
	maxProducts        = 100
	maxProductActions  = 50
	maxRoleUses        = 200
	maxProductBindings = 10
)

// Role kinds.
const (
	// RoleCandidateGroup is a candidate group a user task names as a literal.
	RoleCandidateGroup = "candidateGroup"
	// RoleLane is a lane of a process.
	RoleLane = "lane"
)

// Business is the business layer, absent on a snapshot taken before format version 6.
type Business struct {
	Processes []BusinessProcess `json:"processes"`
	Roles     []BusinessRole    `json:"roles"`
	Products  []Product         `json:"products"`
	// ProductsWithheld counts the products a read withheld from its reader; it is set
	// by [Business.Redacted], never stored.
	ProductsWithheld int `json:"productsWithheld,omitempty"`
	// Omitted counts, per kind, what the bounds left out.
	Omitted BusinessOmitted `json:"omitted,omitzero"`
}

// BusinessOmitted counts what the bounds left out of each list.
type BusinessOmitted struct {
	Roles          int `json:"roles,omitempty"`
	RoleUses       int `json:"roleUses,omitempty"`
	Products       int `json:"products,omitempty"`
	ProductActions int `json:"productActions,omitempty"`
}

// BusinessProcess is one release process as the work it does, with how its user tasks
// say who does them.
type BusinessProcess struct {
	ProcessID string `json:"processId"`
	Name      string `json:"name,omitempty"`
	// GroupTasks are the user tasks offered to candidate groups the model names,
	// PersonTasks those assigned to one person by name, RuntimeTasks those whose
	// assignee or candidate groups the model computes per instance, and OpenTasks
	// those that name nobody.
	GroupTasks   int `json:"groupTasks,omitempty"`
	PersonTasks  int `json:"personTasks,omitempty"`
	RuntimeTasks int `json:"runtimeTasks,omitempty"`
	OpenTasks    int `json:"openTasks,omitempty"`
}

// BusinessRole is one human role the release's processes name: a candidate group, as
// the model writes it, or a lane.
type BusinessRole struct {
	Name string `json:"name"`
	Kind string `json:"kind"`
	// UsedBy are the user tasks offered to a candidate group, or for a lane the
	// processes that have it.
	UsedBy []Use `json:"usedBy"`
}

// Product is one catalogue product a release process carries out (ADR-0396,
// ADR-0425, ADR-0429), as its catalogue last published it.
type Product struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	CatalogID   string `json:"catalogId"`
	CatalogName string `json:"catalogName,omitempty"`
	State       string `json:"state,omitempty"`
	// Bindings are the operations a release process carries out for it.
	Bindings []ProductBinding `json:"bindings"`
	// Actions are the commands a lifecycle product declares, with what each publishes.
	Actions []ProductAction `json:"actions,omitempty"`
}

// ProductBinding is one operation of a product and the release process that carries it
// out; Message is set when a lifecycle process receives the operation as a message.
type ProductBinding struct {
	Op        string `json:"op"`
	ProcessID string `json:"processId"`
	Message   string `json:"message,omitempty"`
}

// ProductAction is one command a lifecycle product declares (ADR-0429).
type ProductAction struct {
	Key      string           `json:"key"`
	Message  string           `json:"message"`
	Effect   string           `json:"effect,omitempty"`
	Triggers []string         `json:"triggers,omitempty"`
	Outcomes []ProductOutcome `json:"outcomes,omitempty"`
}

// ProductOutcome is the event type an action publishes for one outcome.
type ProductOutcome struct {
	Outcome   string `json:"outcome"`
	EventType string `json:"eventType"`
}

// normalize puts every list in a fixed order, merges roles named by several tasks or
// processes, and applies the bounds.
func (b *Business) normalize() {
	slices.SortFunc(b.Processes, func(x, y BusinessProcess) int { return cmp.Compare(x.ProcessID, y.ProcessID) })

	roles := map[[2]string]*BusinessRole{}
	for _, r := range b.Roles {
		key := [2]string{r.Kind, r.Name}
		if have, ok := roles[key]; ok {
			have.UsedBy = append(have.UsedBy, r.UsedBy...)
			continue
		}
		cp := r
		cp.UsedBy = append([]Use(nil), r.UsedBy...)
		roles[key] = &cp
	}
	b.Roles = b.Roles[:0]
	for _, r := range roles {
		r.UsedBy = normalizeUses(r.UsedBy)
		r.UsedBy, b.Omitted.RoleUses = bound(r.UsedBy, maxRoleUses, b.Omitted.RoleUses)
		if r.UsedBy == nil {
			r.UsedBy = []Use{}
		}
		b.Roles = append(b.Roles, *r)
	}
	slices.SortFunc(b.Roles, func(x, y BusinessRole) int {
		return cmp.Or(cmp.Compare(x.Kind, y.Kind), cmp.Compare(x.Name, y.Name))
	})

	for i := range b.Products {
		p := &b.Products[i]
		slices.SortFunc(p.Bindings, func(x, y ProductBinding) int {
			return cmp.Or(cmp.Compare(opRank(x.Op), opRank(y.Op)), cmp.Compare(x.ProcessID, y.ProcessID), cmp.Compare(x.Message, y.Message))
		})
		p.Bindings = slices.Compact(p.Bindings)
		p.Bindings, _ = bound(p.Bindings, maxProductBindings, 0)
		if p.Bindings == nil {
			p.Bindings = []ProductBinding{}
		}
		for j := range p.Actions {
			a := &p.Actions[j]
			slices.Sort(a.Triggers)
			slices.SortFunc(a.Outcomes, func(x, y ProductOutcome) int { return cmp.Compare(x.Outcome, y.Outcome) })
		}
		slices.SortFunc(p.Actions, func(x, y ProductAction) int { return cmp.Compare(x.Key, y.Key) })
		p.Actions, b.Omitted.ProductActions = bound(p.Actions, maxProductActions, b.Omitted.ProductActions)
	}
	slices.SortFunc(b.Products, func(x, y Product) int { return cmp.Or(cmp.Compare(x.Name, y.Name), cmp.Compare(x.ID, y.ID)) })
	b.Products = slices.CompactFunc(b.Products, func(x, y Product) bool { return x.ID == y.ID })

	b.Roles, b.Omitted.Roles = bound(b.Roles, maxBusinessRoles, b.Omitted.Roles)
	b.Products, b.Omitted.Products = bound(b.Products, maxProducts, b.Omitted.Products)
	if b.Processes == nil {
		b.Processes = []BusinessProcess{}
	}
	if b.Roles == nil {
		b.Roles = []BusinessRole{}
	}
	if b.Products == nil {
		b.Products = []Product{}
	}
}

// opRank orders a product's operations the way its life runs.
func opRank(op string) int {
	switch op {
	case "provision":
		return 0
	case "change":
		return 1
	case "deprovision":
		return 2
	}
	return 3
}

// Redacted is the business layer as one reader sees it: each product whose home
// catalogue mayMaintain refuses is withheld and counted. The layer it is called on is
// not changed.
func (b Business) Redacted(mayMaintain func(catalogID string) bool) Business {
	out := b
	out.Products = []Product{}
	for _, p := range b.Products {
		if mayMaintain(p.CatalogID) {
			out.Products = append(out.Products, p)
			continue
		}
		out.ProductsWithheld++
	}
	return out
}

// text lists what the bounds left out.
func (o BusinessOmitted) text() string {
	var parts []string
	for _, c := range []struct {
		n         int
		one, many string
	}{
		{o.Roles, "role", "roles"},
		{o.RoleUses, "task naming a role", "tasks naming a role"},
		{o.Products, "product", "products"},
		{o.ProductActions, "product action", "product actions"},
	} {
		if c.n > 0 {
			parts = append(parts, plural(c.n, c.one, c.many))
		}
	}
	return strings.Join(parts, ", ")
}

// tasksText says how a business process's user tasks name who does them.
func (p BusinessProcess) tasksText() string {
	var parts []string
	for _, c := range []struct {
		n         int
		one, many string
	}{
		{p.GroupTasks, "task for a group", "tasks for a group"},
		{p.PersonTasks, "for a named person", "for a named person"},
		{p.RuntimeTasks, "decided at run time", "decided at run time"},
		{p.OpenTasks, "naming nobody", "naming nobody"},
	} {
		if c.n > 0 {
			parts = append(parts, plural(c.n, c.one, c.many))
		}
	}
	if len(parts) == 0 {
		return "no user task"
	}
	return strings.Join(parts, " · ")
}

// usesText says where a role is named: the tasks offered to a candidate group, the
// processes that have a lane.
func (r BusinessRole) usesText() string {
	if r.Kind == RoleLane {
		pids := make([]string, 0, len(r.UsedBy))
		for _, u := range r.UsedBy {
			pids = append(pids, u.ProcessID)
		}
		return "in " + strings.Join(pids, ", ")
	}
	return "at " + usesText(r.UsedBy)
}

// bindingsText names the operations a product's release processes carry out.
func (p Product) bindingsText() string {
	ops := make([]string, 0, len(p.Bindings))
	for _, b := range p.Bindings {
		if !slices.Contains(ops, b.Op) {
			ops = append(ops, b.Op)
		}
	}
	if len(ops) == 0 {
		return "no operation carried out here"
	}
	return strings.Join(ops, ", ")
}

// bindingText says which release process carries out each operation of a product.
func (p Product) bindingText() string {
	parts := make([]string, 0, len(p.Bindings))
	for _, b := range p.Bindings {
		text := b.Op + " by process " + b.ProcessID
		if b.Message != "" {
			text += " on message " + b.Message
		}
		parts = append(parts, text)
	}
	if len(parts) == 0 {
		return "no operation carried out here"
	}
	return strings.Join(parts, ", ")
}

// actionsText names a product's actions.
func (p Product) actionsText() string {
	if len(p.Actions) == 0 {
		return "no actions declared"
	}
	keys := make([]string, 0, len(p.Actions))
	for _, a := range p.Actions {
		keys = append(keys, a.Key)
	}
	return "actions: " + strings.Join(keys, ", ")
}
